package parser

import (
	"fmt"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// ParsedScenario holds the structurally-decoded scenario with deferred expressions.
type ParsedScenario struct {
	Scenario   *ScenarioBlock
	Components map[string]*ParsedComponent // keyed by "type.name"
	Variables  map[string]cty.Value

	// RequiredProviders maps a provider name to its declared source,
	// e.g. "postgres" → {Source: "builtin/postgres"}. Populated from
	// the scenario's required_providers block. Empty when absent.
	RequiredProviders map[string]RequiredProviderSpec

	// ProviderConfigs maps a provider name to the evaluated attribute
	// values from its `provider "<name>" {}` block. These are passed
	// to provider.Registry.Configure. Providers without a config
	// block receive an empty map rather than being absent, matching
	// the Configure contract that lets providers start up with
	// default or zero configuration.
	ProviderConfigs map[string]map[string]cty.Value
}

// RequiredProviderSpec captures the declared source of a provider.
// Version and explicit configuration-alias fields land here when we
// add them; the struct form lets us evolve without breaking callers.
type RequiredProviderSpec struct {
	Source string
}

// ParsedComponent holds a resolved component definition alongside its instance ref.
type ParsedComponent struct {
	Ref *ComponentRef
	Def *ComponentDefBlock
}

// LoadScenario parses a scenario HCL file, resolves component sources,
// and merges CLI variable overrides with defaults.
func LoadScenario(path string, vars map[string]string) (*ParsedScenario, error) {
	parser := hclparse.NewParser()

	file, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parsing scenario file: %s", diags.Error())
	}

	var sf ScenarioFile
	diags = gohcl.DecodeBody(file.Body, nil, &sf)
	if diags.HasErrors() {
		return nil, fmt.Errorf("decoding scenario: %s", diags.Error())
	}

	if sf.Scenario == nil {
		return nil, fmt.Errorf("no scenario block found in %s", path)
	}

	if err := validateScenarioUniqueness(sf.Scenario); err != nil {
		return nil, err
	}

	// Resolve variables: defaults + CLI overrides.
	resolved, err := resolveVariables(sf.Scenario.Variables, vars)
	if err != nil {
		return nil, fmt.Errorf("resolving variables: %w", err)
	}

	// Extract required_providers before provider config blocks so we
	// can validate that every provider block names a declared
	// provider.
	required, err := extractRequiredProviders(sf.Scenario.RequiredProviders)
	if err != nil {
		return nil, fmt.Errorf("required_providers: %w", err)
	}

	providerConfigs, err := evaluateProviderConfigs(sf.Scenario.Providers, required, resolved)
	if err != nil {
		return nil, fmt.Errorf("provider config: %w", err)
	}

	// Load component definitions from their source paths.
	scenarioDir := filepath.Dir(path)
	components := make(map[string]*ParsedComponent)

	for _, ref := range sf.Scenario.Components {
		compPath := filepath.Join(scenarioDir, ref.Source)
		def, err := loadComponent(parser, compPath)
		if err != nil {
			return nil, fmt.Errorf("loading component %s.%s: %w", ref.Type, ref.Name, err)
		}
		key := ref.Type + "." + ref.Name
		components[key] = &ParsedComponent{Ref: ref, Def: def}
	}

	return &ParsedScenario{
		Scenario:          sf.Scenario,
		Components:        components,
		Variables:         resolved,
		RequiredProviders: required,
		ProviderConfigs:   providerConfigs,
	}, nil
}

// extractRequiredProviders decodes the required_providers block into
// a map keyed by provider name. Each declaration must supply at
// minimum a `source` string; other keys are accepted but ignored
// today so that adding fields (version, alias) later is additive.
func extractRequiredProviders(block *RequiredProvidersBlock) (map[string]RequiredProviderSpec, error) {
	out := make(map[string]RequiredProviderSpec)
	if block == nil {
		return out, nil
	}
	attrs, diags := block.Body.JustAttributes()
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s", diags.Error())
	}
	for name, attr := range attrs {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", name, diags.Error())
		}
		if !val.Type().IsObjectType() {
			return nil, fmt.Errorf("%s: expected object with source, got %s", name, val.Type().FriendlyName())
		}
		if !val.Type().HasAttribute("source") {
			return nil, fmt.Errorf("%s: missing required attribute \"source\"", name)
		}
		src := val.GetAttr("source")
		if src.Type() != cty.String {
			return nil, fmt.Errorf("%s: source must be a string", name)
		}
		out[name] = RequiredProviderSpec{Source: src.AsString()}
	}
	return out, nil
}

// evaluateProviderConfigs evaluates each provider block's body
// against the scenario's resolved variables, producing a per-provider
// attribute map. Every declared required provider is represented in
// the result, even if it has no config block — providers may use
// schema defaults alone.
func evaluateProviderConfigs(
	blocks []*ProviderConfigBlock,
	required map[string]RequiredProviderSpec,
	vars map[string]cty.Value,
) (map[string]map[string]cty.Value, error) {
	result := make(map[string]map[string]cty.Value)
	for name := range required {
		result[name] = map[string]cty.Value{}
	}

	evalCtx := &hcl.EvalContext{Variables: map[string]cty.Value{}}
	if len(vars) > 0 {
		evalCtx.Variables["var"] = cty.ObjectVal(vars)
	}

	seen := make(map[string]bool)
	for _, block := range blocks {
		if _, ok := required[block.Name]; !ok {
			return nil, fmt.Errorf(
				"provider %q is configured but not declared in required_providers",
				block.Name)
		}
		if seen[block.Name] {
			return nil, fmt.Errorf("duplicate provider config for %q", block.Name)
		}
		seen[block.Name] = true

		attrs, diags := block.Remain.JustAttributes()
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s", block.Name, diags.Error())
		}
		config := make(map[string]cty.Value, len(attrs))
		for attrName, attr := range attrs {
			val, diags := attr.Expr.Value(evalCtx)
			if diags.HasErrors() {
				return nil, fmt.Errorf("%s.%s: %s", block.Name, attrName, diags.Error())
			}
			config[attrName] = val
		}
		result[block.Name] = config
	}
	return result, nil
}

func loadComponent(p *hclparse.Parser, path string) (*ComponentDefBlock, error) {
	file, diags := p.ParseHCLFile(path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parsing component file: %s", diags.Error())
	}

	var cf ComponentFile
	diags = gohcl.DecodeBody(file.Body, nil, &cf)
	if diags.HasErrors() {
		return nil, fmt.Errorf("decoding component: %s", diags.Error())
	}

	if cf.Component == nil {
		return nil, fmt.Errorf("no component block found in %s", path)
	}

	if err := validateComponentUniqueness(cf.Component); err != nil {
		return nil, fmt.Errorf("component %s in %s: %w", cf.Component.Name, path, err)
	}

	return cf.Component, nil
}

// validateScenarioUniqueness refuses scenarios that contain duplicate
// variable names, component instance keys, action keys, or output
// names. Scenario authors rely on these being unique — the engine's
// maps would silently overwrite otherwise, so the fail-fast happens
// here with the full parsed block in hand.
//
// Duplicate provider blocks and duplicate required_providers entries
// are caught inside evaluateProviderConfigs and extractRequiredProviders
// respectively, so not repeated here.
func validateScenarioUniqueness(s *ScenarioBlock) error {
	seenVars := make(map[string]bool)
	for _, v := range s.Variables {
		if seenVars[v.Name] {
			return fmt.Errorf("scenario %q: duplicate variable %q", s.Name, v.Name)
		}
		seenVars[v.Name] = true
	}

	seenActions := make(map[string]bool)
	for _, a := range s.Actions {
		key := a.Type + "." + a.Name
		if seenActions[key] {
			return fmt.Errorf("scenario %q: duplicate action %q", s.Name, key)
		}
		seenActions[key] = true
	}

	seenComponents := make(map[string]bool)
	for _, c := range s.Components {
		key := c.Type + "." + c.Name
		if seenComponents[key] {
			return fmt.Errorf("scenario %q: duplicate component instance %q", s.Name, key)
		}
		seenComponents[key] = true
	}

	seenOutputs := make(map[string]bool)
	for _, o := range s.Outputs {
		if seenOutputs[o.Name] {
			return fmt.Errorf("scenario %q: duplicate output %q", s.Name, o.Name)
		}
		seenOutputs[o.Name] = true
	}

	return nil
}

// validateComponentUniqueness applies the same checks to a component
// definition. A component's internal actions are namespaced under
// the component's instance key when they reach the graph, so a
// duplicate here would be remapped into a graph-level collision —
// better to fail with the component name in the error.
func validateComponentUniqueness(c *ComponentDefBlock) error {
	seenVars := make(map[string]bool)
	for _, v := range c.Variables {
		if seenVars[v.Name] {
			return fmt.Errorf("duplicate variable %q", v.Name)
		}
		seenVars[v.Name] = true
	}

	seenActions := make(map[string]bool)
	for _, a := range c.Actions {
		key := a.Type + "." + a.Name
		if seenActions[key] {
			return fmt.Errorf("duplicate action %q", key)
		}
		seenActions[key] = true
	}

	seenOutputs := make(map[string]bool)
	for _, o := range c.Outputs {
		if seenOutputs[o.Name] {
			return fmt.Errorf("duplicate output %q", o.Name)
		}
		seenOutputs[o.Name] = true
	}

	return nil
}

func resolveVariables(blocks []*VariableBlock, overrides map[string]string) (map[string]cty.Value, error) {
	result := make(map[string]cty.Value)

	// Evaluate defaults with a minimal context (no dependencies).
	ctx := &hcl.EvalContext{}

	for _, v := range blocks {
		if override, ok := overrides[v.Name]; ok {
			result[v.Name] = cty.StringVal(override)
			continue
		}
		if v.Default != nil {
			val, diags := v.Default.Value(ctx)
			if diags.HasErrors() {
				return nil, fmt.Errorf("evaluating default for %q: %s", v.Name, diags.Error())
			}
			result[v.Name] = val
		} else {
			return nil, fmt.Errorf("variable %q has no default and no override provided", v.Name)
		}
	}

	return result, nil
}
