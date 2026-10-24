// Package sensitive keeps the values of `sensitive = true` variables
// out of everything Orchard writes or prints. A Redactor replaces each
// occurrence of a sensitive string with a marker naming its variable
// («sensitive:dsn»), wherever the value has travelled: an action's
// outputs, an undo step, an error message, a component input. The
// markers are reversible, so teardown, once it has the values again,
// restores what the run really saw.
//
// ponytail: only string values (and the strings inside lists, maps and
// objects) are matched; a sensitive number or bool, or a secret used
// as an object key, isn't.
package sensitive

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

// Marker is what replaces the value of the sensitive variable name.
func Marker(name string) string { return "«sensitive:" + name + "»" }

// Redactor swaps sensitive strings for markers and back. A nil
// *Redactor, and one with nothing to redact, leaves values alone.
type Redactor struct {
	redact, restore *strings.Replacer
}

// MinLength is the shortest sensitive string New accepts. Every
// occurrence of the value is replaced, so a secret such as "us" would
// turn up in unrelated outputs and bury the record in markers.
const MinLength = 4

// New builds a Redactor for the variables in vars that names marks
// sensitive. It refuses a sensitive string shorter than MinLength.
func New(vars map[string]cty.Value, names map[string]bool) (*Redactor, error) {
	type secret struct{ name, value string }
	var secrets []secret
	for name := range names {
		v, ok := vars[name]
		if !ok {
			continue
		}
		for _, s := range stringsIn(v) {
			if s == "" {
				continue // nothing to leak
			}
			if len(s) < MinLength {
				return nil, fmt.Errorf("sensitive variable %q: a value shorter than %d characters can't be told apart from other text, so it can't be kept out of the record", name, MinLength)
			}
			secrets = append(secrets, secret{name, s})
		}
	}
	if len(secrets) == 0 {
		return nil, nil
	}
	// Longest first, so a secret containing another is replaced whole.
	slices.SortFunc(secrets, func(a, b secret) int {
		return cmp.Or(len(b.value)-len(a.value), strings.Compare(a.value, b.value))
	})
	var fwd, back []string
	seen := map[string]bool{}
	for _, s := range secrets {
		if seen[s.value] {
			continue
		}
		seen[s.value] = true
		fwd = append(fwd, s.value, Marker(s.name))
		back = append(back, Marker(s.name), s.value)
	}
	return &Redactor{redact: strings.NewReplacer(fwd...), restore: strings.NewReplacer(back...)}, nil
}

// String redacts s.
func (r *Redactor) String(s string) string {
	if r == nil {
		return s
	}
	return r.redact.Replace(s)
}

// Error redacts err's message, keeping err when nothing changes.
func (r *Redactor) Error(err error) error {
	if r == nil || err == nil {
		return err
	}
	if msg := r.String(err.Error()); msg != err.Error() {
		return errors.New(msg)
	}
	return err
}

// Value redacts every string inside v.
func (r *Redactor) Value(v cty.Value) cty.Value {
	if r == nil {
		return v
	}
	return replaceStrings(v, r.redact)
}

// Values redacts each value in m, returning a new map.
func (r *Redactor) Values(m map[string]cty.Value) map[string]cty.Value {
	if r == nil || m == nil {
		return m
	}
	out := make(map[string]cty.Value, len(m))
	for k, v := range m {
		out[k] = r.Value(v)
	}
	return out
}

// Restore puts the sensitive values back in place of their markers.
func (r *Redactor) Restore(m map[string]cty.Value) map[string]cty.Value {
	if r == nil || m == nil {
		return m
	}
	out := make(map[string]cty.Value, len(m))
	for k, v := range m {
		out[k] = replaceStrings(v, r.restore)
	}
	return out
}

func replaceStrings(v cty.Value, rep *strings.Replacer) cty.Value {
	out, _ := cty.Transform(v, func(_ cty.Path, v cty.Value) (cty.Value, error) {
		if v.Type() == cty.String && v.IsKnown() && !v.IsNull() && !v.IsMarked() {
			return cty.StringVal(rep.Replace(v.AsString())), nil
		}
		return v, nil
	})
	return out
}

// stringsIn lists the known strings inside v.
func stringsIn(v cty.Value) []string {
	var out []string
	_ = cty.Walk(v, func(_ cty.Path, v cty.Value) (bool, error) {
		if v.Type() == cty.String && v.IsKnown() && !v.IsNull() && !v.IsMarked() {
			out = append(out, v.AsString())
		}
		return true, nil
	})
	return out
}
