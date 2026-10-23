package grpc

import (
	"encoding/base64"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/pthm/orchard/internal/suggest"
)

// Messages map to cty field by field, by proto field name. The
// provider notes in schema.hcl list how each proto type is written.

// messageType is the cty type of message md. A message nested inside
// itself is `any` at the point of recursion, so types stay finite.
func messageType(md protoreflect.MessageDescriptor) cty.Type {
	return msgType(md, nil)
}

func msgType(md protoreflect.MessageDescriptor, stack []protoreflect.FullName) cty.Type {
	if t, ok := wellKnownType(md); ok {
		return t
	}
	for _, n := range stack {
		if n == md.FullName() {
			return cty.DynamicPseudoType
		}
	}
	stack = append(stack, md.FullName())
	fields := md.Fields()
	attrs := make(map[string]cty.Type, fields.Len())
	for i := range fields.Len() {
		f := fields.Get(i)
		attrs[string(f.Name())] = fieldType(f, stack)
	}
	return cty.Object(attrs)
}

func fieldType(f protoreflect.FieldDescriptor, stack []protoreflect.FullName) cty.Type {
	switch {
	case f.IsMap():
		return cty.Map(singularType(f.MapValue(), stack))
	case f.IsList():
		return cty.List(singularType(f, stack))
	}
	return singularType(f, stack)
}

func singularType(f protoreflect.FieldDescriptor, stack []protoreflect.FullName) cty.Type {
	switch f.Kind() {
	case protoreflect.BoolKind:
		return cty.Bool
	case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.EnumKind:
		return cty.String
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return msgType(f.Message(), stack)
	default:
		return cty.Number
	}
}

const wkt = "google.protobuf."

func wellKnownType(md protoreflect.MessageDescriptor) (cty.Type, bool) {
	switch strings.TrimPrefix(string(md.FullName()), wkt) {
	case "Timestamp", "Duration", "FieldMask", "StringValue", "BytesValue":
		return cty.String, true
	case "Struct", "Value", "ListValue", "Any":
		return cty.DynamicPseudoType, true
	case "Int32Value", "Int64Value", "UInt32Value", "UInt64Value", "FloatValue", "DoubleValue":
		return cty.Number, true
	case "BoolValue":
		return cty.Bool, true
	case "Empty":
		return cty.EmptyObject, true
	}
	return cty.NilType, false
}

// conv converts between cty and dynamicpb messages. types resolves Any
// payloads.
type conv struct {
	types *dynamicpb.Types
}

// ---- cty to proto ----

// fill sets m's fields from v, an object of field = value, checking
// every rule in the spec's Validation table on the way. Unknown values
// (references, at plan time) are checked by type where it's known, and
// otherwise skipped. path prefixes field names in errors.
func (c conv) fill(m protoreflect.Message, v cty.Value, path string) error {
	if !v.IsKnown() || v.IsNull() {
		return nil
	}
	md := m.Descriptor()
	if _, ok := wellKnownType(md); ok {
		return c.fillWellKnown(m, v, path)
	}
	if !v.Type().IsObjectType() && !v.Type().IsMapType() {
		return fmt.Errorf("%s is %s; got %s", subject(path), md.FullName(), v.Type().FriendlyName())
	}
	fields := md.Fields()
	oneofs := map[protoreflect.FullName]string{}
	for _, name := range sortedKeys(v) {
		fv := elem(v, name)
		f := fields.ByName(protoreflect.Name(name))
		if f == nil {
			f = fields.ByJSONName(name)
		}
		if f == nil {
			names := make([]string, fields.Len())
			for i := range names {
				names[i] = string(fields.Get(i).Name())
			}
			return fmt.Errorf("%s has no field %q%s", md.FullName(), join(path, name), suggest.Hint(name, names))
		}
		if fv.IsKnown() && fv.IsNull() {
			continue
		}
		if o := f.ContainingOneof(); o != nil && !o.IsSynthetic() {
			if other, ok := oneofs[o.FullName()]; ok {
				return fmt.Errorf("fields %q and %q are both in oneof %q", join(path, other), join(path, name), o.Name())
			}
			oneofs[o.FullName()] = name
		}
		if err := c.setField(m, f, fv, join(path, string(f.Name()))); err != nil {
			return err
		}
	}
	return nil
}

func (c conv) setField(m protoreflect.Message, f protoreflect.FieldDescriptor, v cty.Value, path string) error {
	if !v.IsKnown() {
		return checkUnknown(f, v, path)
	}
	switch {
	case f.IsMap():
		if !v.Type().IsObjectType() && !v.Type().IsMapType() {
			return fmt.Errorf("%s is a map; got %s", subject(path), v.Type().FriendlyName())
		}
		mp := m.Mutable(f).Map()
		for _, k := range sortedKeys(v) {
			key, err := mapKey(f.MapKey(), k, path)
			if err != nil {
				return err
			}
			ev := elem(v, k)
			if !ev.IsKnown() {
				continue
			}
			if ev.IsNull() {
				return fmt.Errorf("%s[%q] is null", subject(path), k)
			}
			val, err := c.value(mp.NewValue, f.MapValue(), ev, fmt.Sprintf("%s[%q]", path, k))
			if err != nil {
				return err
			}
			mp.Set(key, val)
		}
	case f.IsList():
		if !v.Type().IsListType() && !v.Type().IsTupleType() && !v.Type().IsSetType() {
			return fmt.Errorf("%s is a list; got %s", subject(path), v.Type().FriendlyName())
		}
		list := m.Mutable(f).List()
		i := 0
		for it := v.ElementIterator(); it.Next(); i++ {
			_, ev := it.Element()
			p := fmt.Sprintf("%s[%d]", path, i)
			if !ev.IsKnown() {
				if err := checkUnknown(f, ev, p); err != nil {
					return err
				}
				continue
			}
			if ev.IsNull() {
				return fmt.Errorf("%s is null", subject(p))
			}
			val, err := c.value(list.NewElement, f, ev, p)
			if err != nil {
				return err
			}
			list.Append(val)
		}
	default:
		val, err := c.value(func() protoreflect.Value { return m.NewField(f) }, f, v, path)
		if err != nil {
			return err
		}
		m.Set(f, val)
	}
	return nil
}

// value converts one known, non-null singular value. newMessage makes
// the message to fill for message fields.
func (c conv) value(newMessage func() protoreflect.Value, f protoreflect.FieldDescriptor, v cty.Value, path string) (protoreflect.Value, error) {
	if f.Kind() == protoreflect.MessageKind || f.Kind() == protoreflect.GroupKind {
		msg := newMessage()
		return msg, c.fill(msg.Message(), v, path)
	}
	return scalar(f, v, path)
}

func scalar(f protoreflect.FieldDescriptor, v cty.Value, path string) (protoreflect.Value, error) {
	mismatch := func() (protoreflect.Value, error) {
		return protoreflect.Value{}, fmt.Errorf("%s is %s; got %s", subject(path), kindName(f), v.Type().FriendlyName())
	}
	switch f.Kind() {
	case protoreflect.BoolKind:
		if v.Type() != cty.Bool {
			return mismatch()
		}
		return protoreflect.ValueOfBool(v.True()), nil
	case protoreflect.StringKind:
		if v.Type() != cty.String {
			return mismatch()
		}
		return protoreflect.ValueOfString(v.AsString()), nil
	case protoreflect.BytesKind:
		if v.Type() != cty.String {
			return mismatch()
		}
		b, err := base64.StdEncoding.DecodeString(v.AsString())
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("%s is bytes, written as base64: %w", subject(path), err)
		}
		return protoreflect.ValueOfBytes(b), nil
	case protoreflect.EnumKind:
		ed := f.Enum()
		switch v.Type() {
		case cty.String:
			if ev := ed.Values().ByName(protoreflect.Name(v.AsString())); ev != nil {
				return protoreflect.ValueOfEnum(ev.Number()), nil
			}
			names := make([]string, ed.Values().Len())
			for i := range names {
				names[i] = string(ed.Values().Get(i).Name())
			}
			return protoreflect.Value{}, fmt.Errorf("%s is %s (%s); got %q", subject(path), ed.FullName(), strings.Join(names, ", "), v.AsString())
		case cty.Number:
			n, err := integer(v, math.MinInt32, math.MaxInt32)
			if err != nil {
				return protoreflect.Value{}, fmt.Errorf("%s is %s: %w", subject(path), ed.FullName(), err)
			}
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(n.Int64())), nil
		}
		return mismatch()
	}
	if v.Type() != cty.Number {
		return mismatch()
	}
	var lo, hi *big.Int
	switch f.Kind() {
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		fl, _ := v.AsBigFloat().Float64()
		if f.Kind() == protoreflect.FloatKind {
			return protoreflect.ValueOfFloat32(float32(fl)), nil
		}
		return protoreflect.ValueOfFloat64(fl), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		lo, hi = big.NewInt(math.MinInt32), big.NewInt(math.MaxInt32)
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		lo, hi = big.NewInt(0), big.NewInt(math.MaxUint32)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		lo, hi = big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64)
	default: // uint64, fixed64
		lo, hi = big.NewInt(0), new(big.Int).SetUint64(math.MaxUint64)
	}
	n, err := integerIn(v, lo, hi)
	if err != nil {
		return protoreflect.Value{}, fmt.Errorf("%s is %s: %w", subject(path), kindName(f), err)
	}
	switch f.Kind() {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(n.Int64())), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(uint32(n.Uint64())), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(n.Int64()), nil
	default:
		return protoreflect.ValueOfUint64(n.Uint64()), nil
	}
}

func integer(v cty.Value, lo, hi int64) (*big.Int, error) {
	return integerIn(v, big.NewInt(lo), big.NewInt(hi))
}

func integerIn(v cty.Value, lo, hi *big.Int) (*big.Int, error) {
	n, acc := v.AsBigFloat().Int(nil)
	if acc != big.Exact {
		return nil, fmt.Errorf("%s isn't a whole number", v.AsBigFloat().Text('g', -1))
	}
	if n.Cmp(lo) < 0 || n.Cmp(hi) > 0 {
		return nil, fmt.Errorf("%s is out of range", n)
	}
	return n, nil
}

func mapKey(f protoreflect.FieldDescriptor, k, path string) (protoreflect.MapKey, error) {
	if f.Kind() == protoreflect.StringKind {
		return protoreflect.ValueOfString(k).MapKey(), nil
	}
	var v cty.Value
	if f.Kind() == protoreflect.BoolKind {
		switch k {
		case "true", "false":
			v = cty.BoolVal(k == "true")
		default:
			return protoreflect.MapKey{}, fmt.Errorf("%s has bool keys; got %q", subject(path), k)
		}
	} else {
		n, err := cty.ParseNumberVal(k)
		if err != nil {
			return protoreflect.MapKey{}, fmt.Errorf("%s has %s keys; got %q", subject(path), kindName(f), k)
		}
		v = n
	}
	val, err := scalar(f, v, fmt.Sprintf("%s key %q", path, k))
	return val.MapKey(), err
}

// checkUnknown type-checks a value that isn't known yet, where its type
// is: a string for a number field is wrong already at plan.
func checkUnknown(f protoreflect.FieldDescriptor, v cty.Value, path string) error {
	want := singularType(f, nil)
	if f.IsMap() || (f.IsList() && !v.Type().IsPrimitiveType()) {
		return nil
	}
	got := v.Type()
	if got == cty.DynamicPseudoType || !want.IsPrimitiveType() || !got.IsPrimitiveType() || got == want {
		return nil
	}
	if f.Kind() == protoreflect.EnumKind && got == cty.Number {
		return nil
	}
	return fmt.Errorf("%s is %s; got %s", subject(path), kindName(f), got.FriendlyName())
}

func (c conv) fillWellKnown(m protoreflect.Message, v cty.Value, path string) error {
	md := m.Descriptor()
	fields := md.Fields()
	name := strings.TrimPrefix(string(md.FullName()), wkt)
	wantString := func() error {
		if v.Type() != cty.String {
			return fmt.Errorf("%s is %s; got %s", subject(path), md.FullName(), v.Type().FriendlyName())
		}
		return nil
	}
	switch name {
	case "Timestamp":
		if err := wantString(); err != nil {
			return err
		}
		t, err := time.Parse(time.RFC3339Nano, v.AsString())
		if err != nil {
			return fmt.Errorf("%s is a timestamp, written in RFC 3339 like \"2026-01-02T15:04:05Z\"; got %q", subject(path), v.AsString())
		}
		m.Set(fields.ByName("seconds"), protoreflect.ValueOfInt64(t.Unix()))
		m.Set(fields.ByName("nanos"), protoreflect.ValueOfInt32(int32(t.Nanosecond())))
	case "Duration":
		if err := wantString(); err != nil {
			return err
		}
		d, err := time.ParseDuration(v.AsString())
		if err != nil {
			return fmt.Errorf("%s is a duration like \"1.5s\"; got %q", subject(path), v.AsString())
		}
		m.Set(fields.ByName("seconds"), protoreflect.ValueOfInt64(int64(d/time.Second)))
		m.Set(fields.ByName("nanos"), protoreflect.ValueOfInt32(int32(d%time.Second)))
	case "FieldMask":
		if err := wantString(); err != nil {
			return err
		}
		paths := m.Mutable(fields.ByName("paths")).List()
		for p := range strings.SplitSeq(v.AsString(), ",") {
			if p = strings.TrimSpace(p); p != "" {
				paths.Append(protoreflect.ValueOfString(p))
			}
		}
	case "Empty":
		if (!v.Type().IsObjectType() && !v.Type().IsMapType()) || v.LengthInt() != 0 {
			return fmt.Errorf("%s is google.protobuf.Empty; set it to {}", subject(path))
		}
	case "Struct", "Value", "ListValue", "Any":
		if !v.IsWhollyKnown() {
			return nil
		}
		b, err := ctyjson.Marshal(v, v.Type())
		if err != nil {
			return fmt.Errorf("%s: %w", subject(path), err)
		}
		if err := (protojson.UnmarshalOptions{Resolver: c.types}).Unmarshal(b, m.Interface()); err != nil {
			return fmt.Errorf("%s is %s: %w", subject(path), md.FullName(), err)
		}
	default: // wrappers
		f := fields.ByName("value")
		val, err := scalar(f, v, path)
		if err != nil {
			return err
		}
		m.Set(f, val)
	}
	return nil
}

// ---- proto to cty ----

// messageValue converts m to a value of messageType(m.Descriptor()).
// Unset scalars are zero values (proto3); unset messages, optional
// fields and oneof members are null.
func (c conv) messageValue(m protoreflect.Message) cty.Value {
	return c.msgValue(m, nil)
}

func (c conv) msgValue(m protoreflect.Message, stack []protoreflect.FullName) cty.Value {
	md := m.Descriptor()
	if v, ok := c.wellKnownValue(m); ok {
		return v
	}
	for _, n := range stack {
		if n == md.FullName() {
			// `any` in the type: convert from here with a fresh stack.
			return c.msgValue(m, nil)
		}
	}
	stack = append(stack, md.FullName())
	fields := md.Fields()
	if fields.Len() == 0 {
		return cty.EmptyObjectVal
	}
	attrs := make(map[string]cty.Value, fields.Len())
	for i := range fields.Len() {
		f := fields.Get(i)
		attrs[string(f.Name())] = c.fieldValue(m, f, stack)
	}
	return cty.ObjectVal(attrs)
}

func (c conv) fieldValue(m protoreflect.Message, f protoreflect.FieldDescriptor, stack []protoreflect.FullName) cty.Value {
	switch {
	case f.IsMap():
		elem := singularType(f.MapValue(), stack)
		vals := map[string]cty.Value{}
		m.Get(f).Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			vals[k.String()] = c.singular(f.MapValue(), v, stack)
			return true
		})
		return mapOf(vals, elem)
	case f.IsList():
		elem := singularType(f, stack)
		list := m.Get(f).List()
		vals := make([]cty.Value, list.Len())
		for i := range vals {
			vals[i] = c.singular(f, list.Get(i), stack)
		}
		return listOf(vals, elem)
	case f.HasPresence() && !m.Has(f):
		return cty.NullVal(singularType(f, stack))
	}
	return c.singular(f, m.Get(f), stack)
}

func (c conv) singular(f protoreflect.FieldDescriptor, v protoreflect.Value, stack []protoreflect.FullName) cty.Value {
	switch f.Kind() {
	case protoreflect.BoolKind:
		return cty.BoolVal(v.Bool())
	case protoreflect.StringKind:
		return cty.StringVal(v.String())
	case protoreflect.BytesKind:
		return cty.StringVal(base64.StdEncoding.EncodeToString(v.Bytes()))
	case protoreflect.EnumKind:
		if ev := f.Enum().Values().ByNumber(v.Enum()); ev != nil {
			return cty.StringVal(string(ev.Name()))
		}
		return cty.StringVal(fmt.Sprint(int32(v.Enum())))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return cty.NumberIntVal(v.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return cty.NumberUIntVal(v.Uint())
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		fl := v.Float()
		if math.IsNaN(fl) || math.IsInf(fl, 0) {
			// ponytail: cty numbers can't be NaN or infinite.
			return cty.NullVal(cty.Number)
		}
		return cty.NumberFloatVal(fl)
	default:
		return c.msgValue(v.Message(), stack)
	}
}

func (c conv) wellKnownValue(m protoreflect.Message) (cty.Value, bool) {
	md := m.Descriptor()
	fields := md.Fields()
	get := func(name string) protoreflect.Value { return m.Get(fields.ByName(protoreflect.Name(name))) }
	switch strings.TrimPrefix(string(md.FullName()), wkt) {
	case "Timestamp":
		t := time.Unix(get("seconds").Int(), get("nanos").Int()).UTC()
		return cty.StringVal(t.Format(time.RFC3339Nano)), true
	case "Duration":
		d := time.Duration(get("seconds").Int())*time.Second + time.Duration(get("nanos").Int())
		return cty.StringVal(d.String()), true
	case "FieldMask":
		list := get("paths").List()
		paths := make([]string, list.Len())
		for i := range paths {
			paths[i] = list.Get(i).String()
		}
		return cty.StringVal(strings.Join(paths, ",")), true
	case "Empty":
		return cty.EmptyObjectVal, true
	case "Struct", "Value", "ListValue", "Any":
		b, err := (protojson.MarshalOptions{Resolver: c.types, UseProtoNames: true}).Marshal(m.Interface())
		if err != nil {
			return cty.StringVal(fmt.Sprintf("<%s: %v>", md.FullName(), err)), true
		}
		ty, err := ctyjson.ImpliedType(b)
		if err != nil {
			return cty.NullVal(cty.DynamicPseudoType), true
		}
		v, err := ctyjson.Unmarshal(b, ty)
		if err != nil {
			return cty.NullVal(cty.DynamicPseudoType), true
		}
		return v, true
	case "StringValue", "BytesValue", "Int32Value", "Int64Value", "UInt32Value", "UInt64Value",
		"FloatValue", "DoubleValue", "BoolValue":
		return c.singular(fields.ByName("value"), get("value"), nil), true
	}
	return cty.NilVal, false
}

// listOf and mapOf build collections of elem, falling back to a tuple
// or object when elem is `any` and the elements' types differ.
func listOf(vals []cty.Value, elem cty.Type) cty.Value {
	if len(vals) == 0 {
		return cty.ListValEmpty(elem)
	}
	if !sameTypes(vals) {
		return cty.TupleVal(vals)
	}
	return cty.ListVal(vals)
}

func mapOf(vals map[string]cty.Value, elem cty.Type) cty.Value {
	if len(vals) == 0 {
		return cty.MapValEmpty(elem)
	}
	list := make([]cty.Value, 0, len(vals))
	for _, v := range vals {
		list = append(list, v)
	}
	if !sameTypes(list) {
		return cty.ObjectVal(vals)
	}
	return cty.MapVal(vals)
}

func sameTypes(vals []cty.Value) bool {
	for _, v := range vals[1:] {
		if !v.Type().Equals(vals[0].Type()) {
			return false
		}
	}
	return true
}

// ---- helpers ----

func sortedKeys(v cty.Value) []string {
	var keys []string
	for it := v.ElementIterator(); it.Next(); {
		k, _ := it.Element()
		keys = append(keys, k.AsString())
	}
	sort.Strings(keys)
	return keys
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func subject(path string) string {
	if path == "" {
		return "request"
	}
	return fmt.Sprintf("field %q", path)
}

func kindName(f protoreflect.FieldDescriptor) string {
	switch f.Kind() {
	case protoreflect.EnumKind:
		return string(f.Enum().FullName())
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return string(f.Message().FullName())
	}
	return f.Kind().String()
}

// elem is v[k] for an object or map.
func elem(v cty.Value, k string) cty.Value {
	if v.Type().IsObjectType() {
		return v.GetAttr(k)
	}
	return v.Index(cty.StringVal(k))
}
