package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestAttrSchemaCtyType(t *testing.T) {
	cases := []struct {
		in   string
		want cty.Type
	}{
		{"", cty.DynamicPseudoType},
		{"string", cty.String},
		{"number", cty.Number},
		{"bool", cty.Bool},
		{"list(string)", cty.List(cty.String)},
		{"map(number)", cty.Map(cty.Number)},
		{"object({id=number,name=string})", cty.Object(map[string]cty.Type{
			"id":   cty.Number,
			"name": cty.String,
		})},
		{"any", cty.DynamicPseudoType},
	}
	for _, c := range cases {
		got, err := (AttrSchema{Type: c.in}).CtyType()
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !got.Equals(c.want) {
			t.Errorf("%q: got %s want %s", c.in, got.FriendlyName(), c.want.FriendlyName())
		}
	}
}

func TestAttrSchemaCtyTypeInvalid(t *testing.T) {
	_, err := (AttrSchema{Type: "not_a_type"}).CtyType()
	if err == nil {
		t.Fatal("expected error for invalid type, got nil")
	}
}

func TestProviderSchemaValidate(t *testing.T) {
	valid := ProviderSchema{
		Name: "postgres", Version: "1", Protocol: "1",
		Actions: map[string]ActionSchema{
			"sql": {Attrs: map[string]AttrSchema{"query": {Type: "string", Required: true}}},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*ProviderSchema)
		want   string
	}{
		{"missing name", func(s *ProviderSchema) { s.Name = "" }, "name is required"},
		{"missing version", func(s *ProviderSchema) { s.Version = "" }, "version is required"},
		{"missing protocol", func(s *ProviderSchema) { s.Protocol = "" }, "protocol is required"},
		{"wrong protocol", func(s *ProviderSchema) { s.Protocol = "99" }, "protocol version"},
		{"no actions", func(s *ProviderSchema) { s.Actions = nil }, "at least one action"},
		{"bad attr type", func(s *ProviderSchema) {
			s.Actions["sql"] = ActionSchema{Attrs: map[string]AttrSchema{"query": {Type: "not_a_type"}}}
		}, "action.sql.query"},
	}
	for _, c := range cases {
		s := valid
		// Deep-copy actions to avoid cross-test mutation.
		s.Actions = map[string]ActionSchema{}
		for k, v := range valid.Actions {
			s.Actions[k] = v
		}
		c.mutate(&s)
		err := s.Validate()
		if err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not contain %q", c.name, err.Error(), c.want)
		}
	}
}

func TestProviderSchemaJSONRoundTrip(t *testing.T) {
	orig := ProviderSchema{
		Name: "http", Version: "1", Protocol: "1",
		Config: map[string]AttrSchema{
			"base_url": {Type: "string", Required: true},
		},
		Actions: map[string]ActionSchema{
			"get": {
				Description: "HTTP GET",
				Attrs: map[string]AttrSchema{
					"path":    {Type: "string", Required: true},
					"headers": {Type: "map(string)", Default: json.RawMessage(`{}`)},
				},
				Outputs: &AttrSchema{Type: "object({status=number,body=string})"},
			},
		},
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ProviderSchema
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("validate after round-trip: %v", err)
	}
	if got.Actions["get"].Outputs.Type != "object({status=number,body=string})" {
		t.Fatalf("outputs type lost in round-trip: %+v", got.Actions["get"].Outputs)
	}
}

func TestActionNamesSorted(t *testing.T) {
	s := ProviderSchema{Actions: map[string]ActionSchema{
		"zulu": {}, "alpha": {}, "mike": {},
	}}
	got := s.ActionNames()
	want := []string{"alpha", "mike", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("at %d: got %q want %q", i, got[i], want[i])
		}
	}
}
