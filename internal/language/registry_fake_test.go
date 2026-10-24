package language

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestFakeEveryKind calls every kind with no parameters: each either
// generates a value of the type fake() declares for it or fails with
// an error (kinds with required parameters), and none panics.
func TestFakeEveryKind(t *testing.T) {
	fn := RandomFuncs(NewRand(1, "x"))["fake"]
	ok := 0
	for _, name := range fakeNames() {
		args := []cty.Value{cty.StringVal(name)}
		want, err := fn.ReturnTypeForValues(args)
		if err != nil {
			t.Errorf("%s: type: %v", name, err)
			continue
		}
		v, err := fn.Call(args)
		if err != nil {
			continue
		}
		ok++
		if want != cty.DynamicPseudoType && !v.Type().Equals(want) {
			t.Errorf("%s: declared %s, returned %s", name, want.FriendlyName(), v.Type().FriendlyName())
		}
	}
	if ok < 300 {
		t.Errorf("only %d kinds generate without parameters", ok)
	}
}

func TestFake(t *testing.T) {
	email := callSeeded(t, "x", "fake", cty.StringVal("email")).AsString()
	if !strings.Contains(email, "@") {
		t.Errorf("fake(email) = %q", email)
	}
	if again := callSeeded(t, "x", "fake", cty.StringVal("email")).AsString(); again != email {
		t.Errorf("same stream gave %q then %q", email, again)
	}

	params := cty.ObjectVal(map[string]cty.Value{"min": num(18), "max": num(20)})
	for range 50 {
		n, _ := callSeeded(t, "y", "fake", cty.StringVal("number"), params).AsBigFloat().Int64()
		if n < 18 || n > 20 {
			t.Fatalf("fake(number, 18..20) = %d", n)
		}
	}

	addr := callSeeded(t, "x", "fake", cty.StringVal("address"))
	if !addr.Type().IsObjectType() || !addr.Type().HasAttribute("city") {
		t.Errorf("fake(address) = %#v", addr)
	}

	pick := callSeeded(t, "x", "fake", cty.StringVal("randomstring"),
		cty.ObjectVal(map[string]cty.Value{"strs": strs("a", "b")})).AsString()
	if pick != "a" && pick != "b" {
		t.Errorf("a list parameter should pass every value: got %q", pick)
	}
}

func TestFakeErrors(t *testing.T) {
	fn := RandomFuncs(NewRand(1, "x"))["fake"]
	for _, tc := range []struct {
		args []cty.Value
		want string
	}{
		{[]cty.Value{cty.StringVal("emial")}, `no fake data kind "emial" (did you mean "email"?)`},
		{[]cty.Value{cty.StringVal("number"), cty.ObjectVal(map[string]cty.Value{"mni": num(1)})}, `has no parameter "mni" (did you mean "min"?); it takes min, max`},
		{[]cty.Value{cty.StringVal("email"), cty.ObjectVal(map[string]cty.Value{"x": num(1)})}, `"email" takes no parameters`},
		{[]cty.Value{cty.StringVal("number"), cty.StringVal("x")}, "params must be an object"},
	} {
		// The type check is what plan runs.
		_, err := fn.ReturnTypeForValues(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("fake%v: got %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestFakeTemplate(t *testing.T) {
	s := callSeeded(t, "x", "faketemplate", cty.StringVal("{firstname}-###-??")).AsString()
	parts := strings.Split(s, "-")
	if len(parts) != 3 || parts[0] == "{firstname}" || len(parts[1]) != 3 || strings.Trim(parts[1], "0123456789") != "" || len(parts[2]) != 2 {
		t.Errorf("faketemplate = %q", s)
	}
}
