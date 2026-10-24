package language

import (
	"crypto/sha3"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// timeParam is the optional timestamp the time-ordered IDs take.
var timeParam = &ParamDef{Name: "timestamp", Type: "string", Description: "RFC3339 time to embed instead of now. Optional."}

func init() {
	register(
		FunctionDef{
			Name: "uuid", Category: "identity",
			Description: "Generate a random v4 UUID string.",
			Detail:      "Each call produces a new value. Use for primary keys, correlation IDs, or any field that needs a unique identifier.",
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `uuid()`, Result: `"a3f29e5b-1234-4abc-9def-0123456789ab"`}},
			Random:      uuidFunc,
		},
		FunctionDef{
			Name: "uuidv7", Category: "identity",
			Description: "Generate a time-ordered v7 UUID string.",
			Detail:      "The first 48 bits are the Unix time in milliseconds, so v7 UUIDs sort by creation time. Pass a timestamp to backdate one to match a `created_at`.",
			VarParam:    timeParam,
			ReturnType:  "string",
			Examples: []ExampleDef{
				{Code: `uuidv7()`, Result: `"019a1c5e-7a10-7c3e-9b1f-3d2a6c0e4f81"`},
				{Code: `uuidv7("2026-01-01T00:00:00Z")`},
			},
			Random: func(r *Rand) function.Function { return timedID(r, "uuidv7", uuidV7) },
		},
		FunctionDef{
			Name: "ulid", Category: "identity",
			Description: "Generate a ULID: a 26-character, time-ordered, Crockford base32 ID.",
			VarParam:    timeParam,
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `ulid()`, Result: `"01K8E5WYGG4Q8F3T6N2VJ9ZB7C"`}},
			Random:      func(r *Rand) function.Function { return timedID(r, "ulid", ulid) },
		},
		FunctionDef{
			Name: "ksuid", Category: "identity",
			Description: "Generate a KSUID: a 27-character, time-ordered, base62 ID (Segment's format).",
			VarParam:    timeParam,
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `ksuid()`, Result: `"34iLqKcLmVGVZTQ3PjmCsPXxzmg"`}},
			Random:      func(r *Rand) function.Function { return timedID(r, "ksuid", ksuid) },
		},
		FunctionDef{
			Name: "xid", Category: "identity",
			Description: "Generate an xid: a 20-character, time-ordered, base32hex ID.",
			Detail:      "Uses rs/xid's layout. The machine, process and counter fields are random.",
			VarParam:    timeParam,
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `xid()`, Result: `"d3ve5ao8b4kn29gsq0jg"`}},
			Random:      func(r *Rand) function.Function { return timedID(r, "xid", xid) },
		},
		FunctionDef{
			Name: "objectid", Category: "identity",
			Description: "Generate a MongoDB ObjectId as a 24-character hex string.",
			Detail:      "The first 4 bytes are the Unix time in seconds; the rest are random.",
			VarParam:    timeParam,
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `objectid()`, Result: `"68f9a1c27d3e4b0a91c2e5f4"`}},
			Random:      func(r *Rand) function.Function { return timedID(r, "objectid", objectID) },
		},
		FunctionDef{
			Name: "typeid", Category: "identity",
			Description: "Generate a TypeID: a type prefix and a base32 v7 UUID, like `user_01k8e5wygg4q8f3t6n2vj9zb7c`.",
			Params:      []ParamDef{{Name: "prefix", Type: "string", Description: "Lowercase letters and underscores, up to 63; empty for no prefix."}},
			VarParam:    timeParam,
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `typeid("user")`, Result: `"user_01k8e5wygg4q8f3t6n2vj9zb7c"`}},
			Random:      typeIDFunc,
		},
		FunctionDef{
			Name: "snowflake", Category: "identity",
			Description: "Generate a 64-bit Snowflake ID as a decimal string.",
			Detail: "41 bits of milliseconds since `epoch`, a 10-bit `node`, and a 12-bit sequence, as Twitter's IDs. " +
				"`epoch` defaults to Twitter's (1288834974657, in Unix milliseconds); Discord's is 1420070400000. " +
				"IDs from one orchard process are unique and increasing. It's a string because JSON and JavaScript can't hold 64-bit integers exactly; databases convert it on insert.",
			VarParam:   &ParamDef{Name: "node, epoch", Type: "number", Description: "Node ID (0–1023, default 0), then the epoch in Unix milliseconds. Optional."},
			ReturnType: "string",
			Examples: []ExampleDef{
				{Code: `snowflake()`, Result: `"1981467329371627520"`},
				{Code: `snowflake(3, 1420070400000)`},
			},
			Random: func(*Rand) function.Function { return snowflakeFunc() },
		},
		FunctionDef{
			Name: "nanoid", Category: "identity",
			Description: "Generate a Nano ID: a URL-safe random string, 21 characters by default.",
			Detail:      "For another alphabet, use `randstring(length, charset)`.",
			VarParam:    &ParamDef{Name: "length", Type: "number", Description: "Optional; defaults to 21."},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `nanoid()`, Result: `"V1StGXR8_Z5jdHi6B-myT"`}},
			Random:      nanoIDFunc,
		},
		FunctionDef{
			Name: "cuid2", Category: "identity",
			Description: "Generate a CUID2: a lowercase, letter-first, hash-based ID, 24 characters by default.",
			VarParam:    &ParamDef{Name: "length", Type: "number", Description: "From 2 to 32. Optional; defaults to 24."},
			ReturnType:  "string",
			Examples:    []ExampleDef{{Code: `cuid2()`, Result: `"tz4a98xxat96iws9zmbrgj3a"`}},
			Random:      cuid2Func,
		},
	)
}

// timedID builds a function taking an optional timestamp, for IDs that
// embed their creation time.
func timedID(r *Rand, name string, gen func(*Rand, time.Time) string) function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "timestamp", Type: cty.String},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			t, err := optionalTime(name, args)
			if err != nil {
				return cty.NilVal, err
			}
			return cty.StringVal(gen(r, t)), nil
		},
	})
}

func optionalTime(name string, args []cty.Value) (time.Time, error) {
	switch len(args) {
	case 0:
		return time.Now(), nil
	case 1:
		t, err := time.Parse(time.RFC3339, args[0].AsString())
		if err != nil {
			return time.Time{}, fmt.Errorf("%s: timestamp: %w", name, err)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%s: takes at most one timestamp, got %d arguments", name, len(args))
}

// uuidV7Bytes lays out RFC 9562's version 7.
func uuidV7Bytes(r *Rand, t time.Time) [16]byte {
	var b [16]byte
	r.fill(b[6:])
	ms := uint64(t.UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return b
}

func uuidV7(r *Rand, t time.Time) string {
	b := uuidV7Bytes(r, t)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// crockford128 encodes 128 bits as 26 Crockford base32 characters,
// the top character carrying 3 bits (ULID and TypeID's encoding).
func crockford128(b [16]byte) string {
	hi, lo := binary.BigEndian.Uint64(b[:8]), binary.BigEndian.Uint64(b[8:])
	out := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		out[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out)
}

func ulid(r *Rand, t time.Time) string {
	var b [16]byte
	r.fill(b[6:])
	ms := uint64(t.UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	return crockford128(b)
}

// ksuidEpoch is KSUID's epoch, 2014-05-13T16:53:20Z.
const ksuidEpoch = 1400000000

func ksuid(r *Rand, t time.Time) string {
	var b [20]byte
	binary.BigEndian.PutUint32(b[:4], uint32(t.Unix()-ksuidEpoch))
	r.fill(b[4:])
	s := new(big.Int).SetBytes(b[:]).Text(62)
	// big.Int's base 62 digits are 0-9a-zA-Z; KSUID's are 0-9A-Za-z.
	s = strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z':
			return c - 'a' + 'A'
		case c >= 'A' && c <= 'Z':
			return c - 'A' + 'a'
		}
		return c
	}, s)
	return strings.Repeat("0", 27-len(s)) + s
}

var base32hex = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)

func xid(r *Rand, t time.Time) string {
	var b [12]byte
	binary.BigEndian.PutUint32(b[:4], uint32(t.Unix()))
	r.fill(b[4:])
	return base32hex.EncodeToString(b[:])
}

func objectID(r *Rand, t time.Time) string {
	var b [12]byte
	binary.BigEndian.PutUint32(b[:4], uint32(t.Unix()))
	r.fill(b[4:])
	return hex.EncodeToString(b[:])
}

var typeIDPrefix = regexp.MustCompile(`^([a-z]([a-z_]{0,61}[a-z])?)?$`)

func typeIDFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		Params:   []function.Parameter{{Name: "prefix", Type: cty.String}},
		VarParam: &function.Parameter{Name: "timestamp", Type: cty.String},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			prefix := args[0].AsString()
			if !typeIDPrefix.MatchString(prefix) {
				return cty.NilVal, fmt.Errorf("typeid: prefix %q must be up to 63 lowercase letters and underscores, starting and ending with a letter", prefix)
			}
			t, err := optionalTime("typeid", args[1:])
			if err != nil {
				return cty.NilVal, err
			}
			suffix := strings.ToLower(crockford128(uuidV7Bytes(r, t)))
			if prefix == "" {
				return cty.StringVal(suffix), nil
			}
			return cty.StringVal(prefix + "_" + suffix), nil
		},
	})
}

// twitterEpoch is Twitter's Snowflake epoch, in Unix milliseconds.
const twitterEpoch = 1288834974657

const seqMask = 1<<12 - 1

// snowflakes keeps IDs from one process unique: within a millisecond
// the sequence counts up, and when it runs out the clock is borrowed
// from the next millisecond.
var snowflakes struct {
	sync.Mutex
	last map[[2]int64]int64 // (node, epoch) → last id
}

func snowflakeFunc() function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "node", Type: cty.Number},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			node, epoch := int64(0), int64(twitterEpoch)
			var err error
			switch len(args) {
			case 2:
				if epoch, err = intArg("snowflake", "epoch", args[1]); err != nil {
					return cty.NilVal, err
				}
				fallthrough
			case 1:
				if node, err = intArg("snowflake", "node", args[0]); err != nil {
					return cty.NilVal, err
				}
			case 0:
			default:
				return cty.NilVal, fmt.Errorf("snowflake: takes at most a node and an epoch, got %d arguments", len(args))
			}
			if node < 0 || node > 1023 {
				return cty.NilVal, fmt.Errorf("snowflake: node must be between 0 and 1023, got %d", node)
			}
			ms := time.Now().UnixMilli() - epoch
			if ms < 0 || ms >= 1<<41 {
				return cty.NilVal, fmt.Errorf("snowflake: epoch %d puts now outside the 41-bit timestamp", epoch)
			}
			id := ms<<22 | node<<12

			snowflakes.Lock()
			defer snowflakes.Unlock()
			if snowflakes.last == nil {
				snowflakes.last = map[[2]int64]int64{}
			}
			key := [2]int64{node, epoch}
			if last := snowflakes.last[key]; id <= last {
				// Same millisecond: the next sequence number, or after
				// 4096 of them, the next millisecond.
				id = last + 1
				if last&seqMask == seqMask {
					id = (last>>22+1)<<22 | node<<12
				}
			}
			snowflakes.last[key] = id
			return cty.StringVal(strconv.FormatInt(id, 10)), nil
		},
	})
}

const nanoIDAlphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict"

func nanoIDFunc(r *Rand) function.Function {
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "length", Type: cty.Number},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			switch len(args) {
			case 0:
				return randChars(r, "nanoid", cty.NumberIntVal(21), nanoIDAlphabet)
			case 1:
				return randChars(r, "nanoid", args[0], nanoIDAlphabet)
			}
			return cty.NilVal, fmt.Errorf("nanoid: takes at most a length, got %d arguments", len(args))
		},
	})
}

// cuid2Func follows paralleldrive/cuid2: a random letter, then a
// SHA3-512 hash of the time, random salt, a counter and a fingerprint,
// in base 36.
func cuid2Func(r *Rand) function.Function {
	counter := int64(r.Uint32())
	fingerprint := fmt.Sprintf("%x", r.Uint64())
	return function.New(&function.Spec{
		VarParam: &function.Parameter{Name: "length", Type: cty.Number},
		Type:     function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			n := int64(24)
			switch len(args) {
			case 0:
			case 1:
				var err error
				if n, err = intArg("cuid2", "length", args[0]); err != nil {
					return cty.NilVal, err
				}
			default:
				return cty.NilVal, fmt.Errorf("cuid2: takes at most a length, got %d arguments", len(args))
			}
			if n < 2 || n > 32 {
				return cty.NilVal, fmt.Errorf("cuid2: length must be between 2 and 32, got %d", n)
			}
			counter++
			salt, _ := randChars(r, "cuid2", cty.NumberIntVal(n), "0123456789abcdefghijklmnopqrstuvwxyz")
			input := strconv.FormatInt(time.Now().UnixMilli(), 36) + salt.AsString() + strconv.FormatInt(counter, 36) + fingerprint
			sum := sha3.Sum512([]byte(input))
			hash := new(big.Int).SetBytes(sum[:]).Text(36)[1:]
			first := string(rune('a' + r.IntN(26)))
			return cty.StringVal(first + hash[:n-1]), nil
		},
	})
}
