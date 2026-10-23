package s3

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
)

func TestSchema(t *testing.T) {
	s := New().Schema()
	for _, a := range []string{"s3_create_bucket", "s3_put_object", "s3_delete_object", "s3_delete_bucket"} {
		if _, ok := s.Actions[a]; !ok {
			t.Errorf("missing %s", a)
		}
	}
	if _, ok := s.Actions["s3_restore"]; ok {
		t.Error("s3_restore is internal and mustn't be in the schema")
	}
}

func TestConfigureValidation(t *testing.T) {
	cases := map[string]map[string]cty.Value{
		"together": {"access_key_id": cty.StringVal("k")},
		"gcs":      {"project": cty.StringVal("p")},
	}
	for want, cfg := range cases {
		err := New().Configure(context.Background(), cfg)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: got %v, want %q", cfg, err, want)
		}
	}
	if err := New().Configure(context.Background(), map[string]cty.Value{
		"endpoint": cty.StringVal("http://localhost:9000"), "path_style": cty.True,
		"access_key_id": cty.StringVal("k"), "secret_access_key": cty.StringVal("s"),
	}); err != nil {
		t.Errorf("static credentials: %v", err)
	}
}

func TestPlanActionChecks(t *testing.T) {
	p := New()
	put := func(extra map[string]cty.Value) map[string]cty.Value {
		attrs := map[string]cty.Value{"bucket": cty.StringVal("uploads"), "key": cty.StringVal("a.txt")}
		for k, v := range extra {
			attrs[k] = v
		}
		return attrs
	}
	bad := []struct {
		attrs map[string]cty.Value
		want  string
	}{
		{put(nil), "exactly one"},
		{put(map[string]cty.Value{"content": cty.StringVal("x"), "file": cty.StringVal("f")}), "exactly one"},
		{put(map[string]cty.Value{"content_base64": cty.StringVal("!!")}), "base64"},
		{put(map[string]cty.Value{"content": cty.StringVal("x"), "on_conflict": cty.StringVal("error")}), "on_conflict"},
		{put(map[string]cty.Value{"content": cty.StringVal("x"), "bucket": cty.StringVal("My Bucket")}), "bucket name"},
	}
	for _, c := range bad {
		if _, err := p.PlanAction(context.Background(), "s3_put_object", c.attrs); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: got %v, want %q", c.attrs, err, c.want)
		}
	}
	// An unknown source counts as set.
	if _, err := p.PlanAction(context.Background(), "s3_put_object", put(map[string]cty.Value{"content": cty.UnknownVal(cty.String)})); err != nil {
		t.Errorf("unknown content: %v", err)
	}
}

func TestInverse(t *testing.T) {
	cases := []struct {
		name      string
		w         *written
		requested bool
		action    string
		key       string
	}{
		{"created bucket", &written{bucket: "b", createdBucket: true}, false, "s3_delete_bucket", ""},
		{"created object", &written{bucket: "b", key: "k", createdObject: true}, false, "s3_delete_object", "k"},
		{"owned overwrite", &written{bucket: "b", key: "k", backup: "bk", owned: true}, false, "s3_restore", "k"},
		{"requested overwrite", &written{bucket: "b", key: "k", backup: "bk"}, true, "s3_restore", "k"},
		{"unowned overwrite", &written{bucket: "b", key: "k", backup: "bk"}, false, "s3_delete_object", "bk"},
		{"existing bucket", &written{bucket: "b"}, false, "", ""},
	}
	for _, c := range cases {
		p := &Provider{last: c.w}
		u := p.Inverse("", nil, nil, provider.InverseOptions{Requested: c.requested})
		switch {
		case c.action == "" && u != nil:
			t.Errorf("%s: want no undo, got %s", c.name, u.Action)
		case c.action != "" && (u == nil || u.Action != c.action):
			t.Errorf("%s: want %s, got %#v", c.name, c.action, u)
		case c.key != "" && u.Attrs["key"].AsString() != c.key:
			t.Errorf("%s: key %s", c.name, u.Attrs["key"].AsString())
		}
	}
}

func TestCopySource(t *testing.T) {
	if got := copySource("b", "a dir/ü.png"); got != "b/a%20dir/%C3%BC.png" {
		t.Errorf("got %q", got)
	}
}
