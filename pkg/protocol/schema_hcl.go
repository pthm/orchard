package protocol

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclparse"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// ParseHCLSchema parses a provider schema file (schema.hcl) into a
// ProviderSchema. The filename is used only for diagnostic source
// locations and may be empty.
//
// The HCL authoring form expects a single top-level provider block:
//
//	provider "name" {
//	  version  = "1"
//	  protocol = "1"
//
//	  config {
//	    attribute "dsn" { type = string, required = true }
//	  }
//
//	  action "sql" {
//	    attribute "query" { type = string, required = true }
//	    output { type = any }
//	  }
//	}
//
// Type fields hold HCL type expressions (bare identifiers like `string`
// or compound forms like `object({id=number})`), not string literals.
// Default values are full HCL expressions evaluated against the
// attribute's declared type at parse time.
func ParseHCLSchema(src []byte, filename string) (ProviderSchema, error) {
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL(src, filename)
	if diags.HasErrors() {
		return ProviderSchema{}, fmt.Errorf("parse %s: %s", filename, diags.Error())
	}

	root, diags := file.Body.Content(rootSchema)
	if diags.HasErrors() {
		return ProviderSchema{}, fmt.Errorf("parse %s: %s", filename, diags.Error())
	}
	if len(root.Blocks) != 1 {
		return ProviderSchema{}, fmt.Errorf("parse %s: expected exactly one provider block, got %d",
			filename, len(root.Blocks))
	}

	return decodeProvider(root.Blocks[0])
}

var rootSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "provider", LabelNames: []string{"name"}},
	},
}

var providerBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "version", Required: true},
		{Name: "protocol", Required: true},
		{Name: "description"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "config"},
		{Type: "action", LabelNames: []string{"name"}},
	},
}

var configBodySchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "attribute", LabelNames: []string{"name"}},
	},
}

var actionBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "description"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "attribute", LabelNames: []string{"name"}},
		{Type: "output"},
	},
}

var attributeBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "type", Required: true},
		{Name: "required"},
		{Name: "default"},
		{Name: "description"},
	},
}

var outputBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "type", Required: true},
		{Name: "description"},
	},
}

func decodeProvider(block *hcl.Block) (ProviderSchema, error) {
	out := ProviderSchema{Name: block.Labels[0]}

	content, diags := block.Body.Content(providerBodySchema)
	if diags.HasErrors() {
		return ProviderSchema{}, fmt.Errorf("provider %q: %s", out.Name, diags.Error())
	}

	if err := decodeStringAttr(content.Attributes, "version", &out.Version, true); err != nil {
		return ProviderSchema{}, fmt.Errorf("provider %q: %w", out.Name, err)
	}
	if err := decodeStringAttr(content.Attributes, "protocol", &out.Protocol, true); err != nil {
		return ProviderSchema{}, fmt.Errorf("provider %q: %w", out.Name, err)
	}

	out.Actions = make(map[string]ActionSchema)
	for _, b := range content.Blocks {
		switch b.Type {
		case "config":
			cfg, err := decodeAttributeBlocks(b.Body, configBodySchema)
			if err != nil {
				return ProviderSchema{}, fmt.Errorf("provider %q config: %w", out.Name, err)
			}
			out.Config = cfg
		case "action":
			name := b.Labels[0]
			if _, dup := out.Actions[name]; dup {
				return ProviderSchema{}, fmt.Errorf("provider %q: duplicate action %q", out.Name, name)
			}
			action, err := decodeAction(b)
			if err != nil {
				return ProviderSchema{}, fmt.Errorf("provider %q action %q: %w", out.Name, name, err)
			}
			out.Actions[name] = action
		}
	}

	if err := out.Validate(); err != nil {
		return ProviderSchema{}, err
	}
	return out, nil
}

func decodeAction(block *hcl.Block) (ActionSchema, error) {
	content, diags := block.Body.Content(actionBodySchema)
	if diags.HasErrors() {
		return ActionSchema{}, fmt.Errorf("%s", diags.Error())
	}

	var action ActionSchema
	if err := decodeStringAttr(content.Attributes, "description", &action.Description, false); err != nil {
		return ActionSchema{}, err
	}

	attrs := make(map[string]AttrSchema)
	for _, b := range content.Blocks {
		switch b.Type {
		case "attribute":
			name := b.Labels[0]
			if _, dup := attrs[name]; dup {
				return ActionSchema{}, fmt.Errorf("duplicate attribute %q", name)
			}
			attr, err := decodeAttribute(b.Body)
			if err != nil {
				return ActionSchema{}, fmt.Errorf("attribute %q: %w", name, err)
			}
			attrs[name] = attr
		case "output":
			if action.Outputs != nil {
				return ActionSchema{}, fmt.Errorf("duplicate output block")
			}
			out, err := decodeOutput(b.Body)
			if err != nil {
				return ActionSchema{}, fmt.Errorf("output: %w", err)
			}
			action.Outputs = &out
		}
	}
	action.Attrs = attrs
	return action, nil
}

func decodeAttributeBlocks(body hcl.Body, schema *hcl.BodySchema) (map[string]AttrSchema, error) {
	content, diags := body.Content(schema)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s", diags.Error())
	}
	out := make(map[string]AttrSchema)
	for _, b := range content.Blocks {
		name := b.Labels[0]
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("duplicate attribute %q", name)
		}
		attr, err := decodeAttribute(b.Body)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", name, err)
		}
		out[name] = attr
	}
	return out, nil
}

func decodeAttribute(body hcl.Body) (AttrSchema, error) {
	content, diags := body.Content(attributeBodySchema)
	if diags.HasErrors() {
		return AttrSchema{}, fmt.Errorf("%s", diags.Error())
	}

	var attr AttrSchema

	// type is an HCL type expression, not a value. Parse it with typeexpr,
	// then re-render canonically so the stored string round-trips through
	// the JSON wire format.
	typeAttr := content.Attributes["type"]
	ctyType, diags := typeexpr.TypeConstraint(typeAttr.Expr)
	if diags.HasErrors() {
		return AttrSchema{}, fmt.Errorf("type: %s", diags.Error())
	}
	attr.Type = typeexpr.TypeString(ctyType)

	if a, ok := content.Attributes["required"]; ok {
		val, diags := a.Expr.Value(nil)
		if diags.HasErrors() {
			return AttrSchema{}, fmt.Errorf("required: %s", diags.Error())
		}
		if val.Type().FriendlyName() != "bool" {
			return AttrSchema{}, fmt.Errorf("required: expected bool, got %s", val.Type().FriendlyName())
		}
		attr.Required = val.True()
	}

	if a, ok := content.Attributes["default"]; ok {
		val, diags := a.Expr.Value(nil)
		if diags.HasErrors() {
			return AttrSchema{}, fmt.Errorf("default: %s", diags.Error())
		}
		// Coerce the literal to the declared type so e.g. `default = 5` on
		// a `number` attribute produces a canonical encoding.
		raw, err := ctyjson.Marshal(val, ctyType)
		if err != nil {
			return AttrSchema{}, fmt.Errorf("default: %w", err)
		}
		attr.Default = json.RawMessage(raw)
	}

	if err := decodeStringAttr(content.Attributes, "description", &attr.Description, false); err != nil {
		return AttrSchema{}, err
	}
	return attr, nil
}

func decodeOutput(body hcl.Body) (AttrSchema, error) {
	content, diags := body.Content(outputBodySchema)
	if diags.HasErrors() {
		return AttrSchema{}, fmt.Errorf("%s", diags.Error())
	}
	var out AttrSchema
	typeAttr := content.Attributes["type"]
	ctyType, diags := typeexpr.TypeConstraint(typeAttr.Expr)
	if diags.HasErrors() {
		return AttrSchema{}, fmt.Errorf("type: %s", diags.Error())
	}
	out.Type = typeexpr.TypeString(ctyType)
	if err := decodeStringAttr(content.Attributes, "description", &out.Description, false); err != nil {
		return AttrSchema{}, err
	}
	return out, nil
}

func decodeStringAttr(attrs hcl.Attributes, name string, dst *string, required bool) error {
	a, ok := attrs[name]
	if !ok {
		if required {
			return fmt.Errorf("%s: required attribute missing", name)
		}
		return nil
	}
	val, diags := a.Expr.Value(nil)
	if diags.HasErrors() {
		return fmt.Errorf("%s: %s", name, diags.Error())
	}
	if val.Type().FriendlyName() != "string" {
		return fmt.Errorf("%s: expected string, got %s", name, val.Type().FriendlyName())
	}
	*dst = val.AsString()
	return nil
}

// MustParseHCL is the panic-on-error form of ParseHCLSchema, suitable
// for embedded schema files loaded at provider init.
func MustParseHCL(src []byte) ProviderSchema {
	s, err := ParseHCLSchema(src, "embedded")
	if err != nil {
		panic(fmt.Sprintf("protocol: %v", err))
	}
	return s
}
