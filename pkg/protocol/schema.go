package protocol

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// ProtocolVersion is the wire protocol version the engine speaks. Providers
// advertise their own supported version in ProviderSchema.Protocol during
// initialize; a mismatch is a hard error at handshake time.
const ProtocolVersion = "1"

// ProviderSchema is the self-description a provider returns during
// initialize. It is also the parse target for schema.hcl files.
//
// Description is rendered immediately after the page header and
// carries the provider's intro prose. Notes is rendered at the
// bottom of the page — the field exists so end-of-page caveats
// (runtime behaviour, limitations, migration notes) have a natural
// home without being tucked into the intro.
type ProviderSchema struct {
	Name        string                  `json:"name"`
	Version     string                  `json:"version"`
	Protocol    string                  `json:"protocol"`
	Description string                  `json:"description,omitempty"`
	Notes       string                  `json:"notes,omitempty"`
	Config      map[string]AttrSchema   `json:"config,omitempty"`
	Actions     map[string]ActionSchema `json:"actions"`
	Examples    []Example               `json:"examples,omitempty"`
}

// ActionSchema describes one action type offered by a provider.
type ActionSchema struct {
	Description string                `json:"description,omitempty"`
	Notes       string                `json:"notes,omitempty"`
	Attrs       map[string]AttrSchema `json:"attrs"`
	Outputs     *AttrSchema           `json:"outputs,omitempty"`
	Examples    []Example             `json:"examples,omitempty"`
}

// Example is a worked HCL snippet attached to a provider or action. It
// is carried in the schema so tooling — docs generation in particular —
// can render authoritative, schema-adjacent examples without a separate
// templates tree.
//
// Code is the raw HCL as it should appear in documentation. Description
// is an optional one-line caption rendered above the snippet.
type Example struct {
	Description string `json:"description,omitempty"`
	Code        string `json:"code"`
}

// AttrSchema describes a single attribute or an output shape.
//
// Type is a cty type expression in textual form ("string", "list(number)",
// "object({id=number, name=string})", "any"). It is parsed by CtyType.
//
// Default is optional. When present, it carries a JSON-encoded cty value
// compatible with Type; the engine applies it when the attribute is
// omitted from an action body.
type AttrSchema struct {
	Type        string          `json:"type"`
	Required    bool            `json:"required,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Description string          `json:"description,omitempty"`
}

// CtyType parses the schema's textual cty type expression and returns
// the corresponding cty.Type. An empty Type string is treated as
// cty.DynamicPseudoType so providers can declare free-form outputs.
func (a AttrSchema) CtyType() (cty.Type, error) {
	if a.Type == "" {
		return cty.DynamicPseudoType, nil
	}
	expr, diags := hclsyntax.ParseExpression([]byte(a.Type), "schema", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return cty.NilType, fmt.Errorf("parse type %q: %s", a.Type, diags.Error())
	}
	t, diags := typeexpr.TypeConstraint(expr)
	if diags.HasErrors() {
		return cty.NilType, fmt.Errorf("invalid type %q: %s", a.Type, diags.Error())
	}
	return t, nil
}

// Validate checks that the schema is self-consistent: required fields
// are set, every declared type parses, and the protocol version matches
// what this build of Orchard speaks. It does not touch defaults —
// those need a cty.Type to decode, so the engine validates them as
// part of its own per-attribute checks.
func (s ProviderSchema) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("provider: name is required")
	}
	if s.Version == "" {
		return fmt.Errorf("provider %q: version is required", s.Name)
	}
	if s.Protocol == "" {
		return fmt.Errorf("provider %q: protocol is required", s.Name)
	}
	if s.Protocol != ProtocolVersion {
		return fmt.Errorf("provider %q: protocol version %q not supported (want %q)",
			s.Name, s.Protocol, ProtocolVersion)
	}
	if len(s.Actions) == 0 {
		return fmt.Errorf("provider %q: at least one action is required", s.Name)
	}
	for name, attr := range s.Config {
		if _, err := attr.CtyType(); err != nil {
			return fmt.Errorf("provider %q config.%s: %w", s.Name, name, err)
		}
	}
	for actionName, action := range s.Actions {
		for attrName, attr := range action.Attrs {
			if _, err := attr.CtyType(); err != nil {
				return fmt.Errorf("provider %q action.%s.%s: %w",
					s.Name, actionName, attrName, err)
			}
		}
		if action.Outputs != nil {
			t, err := action.Outputs.CtyType()
			if err != nil {
				return fmt.Errorf("provider %q action.%s.outputs: %w",
					s.Name, actionName, err)
			}
			// Runtime always returns an object (fields keyed by
			// attribute name), so the schema must declare either a
			// concrete object type or `any` for open-ended shapes.
			// List/scalar declarations would typecheck at plan time
			// but fail at execute — rejecting at schema load surfaces
			// the mismatch where authors can act on it.
			if t != cty.DynamicPseudoType && !t.IsObjectType() {
				return fmt.Errorf(
					"provider %q action.%s outputs: type must be an object or `any`, got %s",
					s.Name, actionName, t.FriendlyName())
			}
		}
	}
	return nil
}

// ActionNames returns the action type names in sorted order. Useful
// for deterministic error messages and plan output.
func (s ProviderSchema) ActionNames() []string {
	names := make([]string, 0, len(s.Actions))
	for name := range s.Actions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
