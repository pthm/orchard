package sensitive

import (
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestRedactorRoundTrip(t *testing.T) {
	r, err := New(map[string]cty.Value{
		"key":   cty.StringVal("s3cret"),
		"long":  cty.StringVal("s3cret-and-more"),
		"plain": cty.StringVal("visible"),
		"empty": cty.StringVal(""),
	}, map[string]bool{"key": true, "long": true, "empty": true})
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]cty.Value{
		"out": cty.ObjectVal(map[string]cty.Value{
			"stdout": cty.StringVal("token=s3cret-and-more s3cret visible"),
			"id":     cty.NumberIntVal(7),
		}),
	}
	red := r.Values(in)
	got := red["out"].GetAttr("stdout").AsString()
	if want := "token=" + Marker("long") + " " + Marker("key") + " visible"; got != want {
		t.Errorf("redact: got %q want %q", got, want)
	}
	if back := r.Restore(red)["out"]; !back.RawEquals(in["out"]) {
		t.Errorf("restore: got %#v", back)
	}
	if msg := r.Error(errors.New("bad s3cret")).Error(); strings.Contains(msg, "s3cret") {
		t.Errorf("error not redacted: %q", msg)
	}
}

func TestNewRefusesShortSecrets(t *testing.T) {
	_, err := New(map[string]cty.Value{"pin": cty.StringVal("us")}, map[string]bool{"pin": true})
	if err == nil || !strings.Contains(err.Error(), `"pin"`) {
		t.Fatalf("want a short-secret error naming pin, got %v", err)
	}
}
