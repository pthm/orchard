package parser

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
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

// LoadScenario parses a scenario HCL file, picks the scenario block
// matching name, resolves component sources, and merges CLI variable
// overrides with defaults.
//
// An empty name selects the sole scenario when the file contains
// exactly one — a convenience that keeps `orchard run foo.hcl`
// ergonomic for the single-scenario case. When a file has multiple
// scenarios and name is empty, the error lists the available names so
// the caller can retry with --scenario.
func LoadScenario(path, name string, vars map[string]string) (*ParsedScenario, error) {
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

	if len(sf.Scenarios) == 0 {
		return nil, fmt.Errorf("no scenario block found in %s", path)
	}

	// Guard against multiple scenarios sharing the same name — that
	// makes --scenario ambiguous and is surely a mistake.
	seen := make(map[string]bool, len(sf.Scenarios))
	for _, s := range sf.Scenarios {
		if seen[s.Name] {
			return nil, fmt.Errorf("%s: duplicate scenario name %q", path, s.Name)
		}
		seen[s.Name] = true
	}

	scenario, err := pickScenario(sf.Scenarios, name, path)
	if err != nil {
		return nil, err
	}

	if err := validateScenarioUniqueness(scenario); err != nil {
		return nil, err
	}

	// Resolve variables: defaults + CLI overrides.
	resolved, err := resolveVariables(scenario.Variables, vars)
	if err != nil {
		return nil, fmt.Errorf("resolving variables: %w", err)
	}

	// Extract required_providers before provider config blocks so we
	// can validate that every provider block names a declared
	// provider.
	required, err := extractRequiredProviders(scenario.RequiredProviders)
	if err != nil {
		return nil, fmt.Errorf("required_providers: %w", err)
	}

	// Resolve `exec:<path>` sources against the scenario file's
	// directory, mirroring component `source` resolution. Without
	// this step, running `orchard run /abs/scenario.hcl` from any
	// other cwd fails with "no such file or directory" because the
	// binary path is interpreted relative to the process cwd rather
	// than the scenario.
	scenarioDir := filepath.Dir(path)
	if err := resolveExecSources(required, scenarioDir); err != nil {
		return nil, err
	}

	providerConfigs, err := evaluateProviderConfigs(scenario.Providers, required, resolved)
	if err != nil {
		return nil, fmt.Errorf("provider config: %w", err)
	}

	// Load component definitions from their source paths.
	components := make(map[string]*ParsedComponent)

	for _, ref := range scenario.Components {
		compPath := filepath.Join(scenarioDir, ref.Source)
		def, err := loadComponent(parser, compPath)
		if err != nil {
			return nil, fmt.Errorf("loading component %s.%s: %w", ref.Type, ref.Name, err)
		}
		key := ref.Type + "." + ref.Name
		components[key] = &ParsedComponent{Ref: ref, Def: def}
	}

	// Validate each component instance's inputs against its declared
	// variables. Static check only — value expressions may reference
	// runtime outputs that do not exist yet.
	for _, comp := range components {
		if err := ValidateComponentInputs(comp); err != nil {
			return nil, err
		}
	}

	return &ParsedScenario{
		Scenario:          scenario,
		Components:        components,
		Variables:         resolved,
		RequiredProviders: required,
		ProviderConfigs:   providerConfigs,
	}, nil
}

// resolveExecSources rewrites each `exec:<path>` source so the path
// is absolute and anchored at scenarioDir. Non-exec sources and
// already-absolute exec paths pass through untouched. Returns an
// error on malformed sources (empty path) so callers don't discover
// them later at Spawn time.
//
// The rewrite happens at parse time so the resolved source flows
// through the entire pipeline — run, plan, and any cmd or test code
// that reads ParsedScenario.RequiredProviders sees the same
// absolute path.
func resolveExecSources(required map[string]RequiredProviderSpec, scenarioDir string) error {
	for name, spec := range required {
		rest, ok := strings.CutPrefix(spec.Source, "exec:")
		if !ok {
			continue
		}
		if rest == "" {
			return fmt.Errorf("provider %q: exec source requires a path", name)
		}
		if filepath.IsAbs(rest) {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(scenarioDir, rest))
		if err != nil {
			return fmt.Errorf("provider %q: resolve exec path %q: %w", name, rest, err)
		}
		spec.Source = "exec:" + abs
		required[name] = spec
	}
	return nil
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

	evalCtx := &hcl.EvalContext{
		Variables: map[string]cty.Value{},
		Functions: StdFunctions(),
	}
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
// pickScenario returns the scenario matching name. An empty name
// succeeds when there is exactly one scenario in the file. An empty
// name against multiple scenarios produces an error listing the
// available names so the caller can retry with --scenario.
func pickScenario(scenarios []*ScenarioBlock, name, path string) (*ScenarioBlock, error) {
	if name == "" {
		if len(scenarios) == 1 {
			return scenarios[0], nil
		}
		names := make([]string, len(scenarios))
		for i, s := range scenarios {
			names[i] = s.Name
		}
		return nil, fmt.Errorf(
			"%s contains multiple scenarios; pass --scenario to pick one of: %s",
			path, strings.Join(names, ", "))
	}
	for _, s := range scenarios {
		if s.Name == name {
			return s, nil
		}
	}
	names := make([]string, len(scenarios))
	for i, s := range scenarios {
		names[i] = s.Name
	}
	return nil, fmt.Errorf(
		"%s: no scenario %q found (have: %s)",
		path, name, strings.Join(names, ", "))
}

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
	ctx := &hcl.EvalContext{Functions: StdFunctions()}

	for _, v := range blocks {
		declaredType, err := ResolveVariableType(v)
		if err != nil {
			return nil, fmt.Errorf("variable %q: %w", v.Name, err)
		}

		if override, ok := overrides[v.Name]; ok {
			val, err := parseOverride(override, declaredType)
			if err != nil {
				return nil, fmt.Errorf("variable %q: %w", v.Name, err)
			}
			result[v.Name] = val
			continue
		}

		defaultExpr, err := VariableDefault(v)
		if err != nil {
			return nil, err
		}
		if defaultExpr != nil {
			val, diags := defaultExpr.Value(ctx)
			if diags.HasErrors() {
				return nil, fmt.Errorf("evaluating default for %q: %s", v.Name, diags.Error())
			}
			coerced, err := coerceToType(val, declaredType)
			if err != nil {
				return nil, fmt.Errorf("default for %q: %w", v.Name, err)
			}
			result[v.Name] = coerced
			continue
		}

		return nil, fmt.Errorf("variable %q has no default and no override provided", v.Name)
	}

	return result, nil
}

// variableBodySchema is the hcl.BodySchema for a variable block's
// inner attributes. Decoding with this schema lets us distinguish
// genuinely-absent attributes from ones set to `null`, which gohcl's
// `,optional` handling collapses together.
var variableBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "type"},
		{Name: "default"},
		{Name: "description"},
	},
}

// variableParts holds the decoded attributes of a variable block.
type variableParts struct {
	typeExpr    hcl.Expression // nil when absent
	defaultExpr hcl.Expression // nil when absent
	description string         // empty when absent
}

func decodeVariable(v *VariableBlock) (variableParts, error) {
	content, diags := v.Body.Content(variableBodySchema)
	if diags.HasErrors() {
		return variableParts{}, fmt.Errorf("variable %q: %s", v.Name, diags.Error())
	}
	var p variableParts
	if a, ok := content.Attributes["type"]; ok {
		p.typeExpr = a.Expr
	}
	if a, ok := content.Attributes["default"]; ok {
		p.defaultExpr = a.Expr
	}
	if a, ok := content.Attributes["description"]; ok {
		val, diags := a.Expr.Value(nil)
		if diags.HasErrors() {
			return variableParts{}, fmt.Errorf("variable %q description: %s", v.Name, diags.Error())
		}
		if val.Type() == cty.String {
			p.description = val.AsString()
		}
	}
	return p, nil
}

// ResolveVariableType parses the variable's `type` HCL expression
// (e.g. `string`, `list(number)`, `object({id=number})`) into a
// cty.Type. A variable with no declared type is treated as
// cty.DynamicPseudoType — any value shape is accepted.
func ResolveVariableType(v *VariableBlock) (cty.Type, error) {
	p, err := decodeVariable(v)
	if err != nil {
		return cty.NilType, err
	}
	if p.typeExpr == nil {
		return cty.DynamicPseudoType, nil
	}
	t, diags := typeexpr.TypeConstraint(p.typeExpr)
	if diags.HasErrors() {
		return cty.NilType, fmt.Errorf("type: %s", diags.Error())
	}
	return t, nil
}

// VariableDefault returns the variable's default-value expression,
// or nil if the variable declared no default. A variable that
// explicitly sets `default = null` is treated as having a default
// (typed null); only genuinely-absent defaults return nil.
func VariableDefault(v *VariableBlock) (hcl.Expression, error) {
	p, err := decodeVariable(v)
	if err != nil {
		return nil, err
	}
	return p.defaultExpr, nil
}

// coerceToType converts val to the declared type. A dynamic
// (undeclared) type accepts the value verbatim.
func coerceToType(val cty.Value, declared cty.Type) (cty.Value, error) {
	if declared == cty.DynamicPseudoType {
		return val, nil
	}
	coerced, err := convert.Convert(val, declared)
	if err != nil {
		return cty.NilVal, fmt.Errorf("type mismatch: %w", err)
	}
	return coerced, nil
}

// parseOverride turns a CLI --var override string into a cty.Value
// compatible with the variable's declared type. String-typed (or
// untyped) variables take the raw string verbatim, which keeps the
// common `--var name=Acme` case working without quoting. Other
// types parse the override through HCL's expression grammar, so
// `--var count=42`, `--var flag=true`, and `--var tags=["a","b"]`
// all work as expected.
func parseOverride(raw string, declared cty.Type) (cty.Value, error) {
	if declared == cty.DynamicPseudoType || declared == cty.String {
		return cty.StringVal(raw), nil
	}
	expr, diags := hclsyntax.ParseExpression([]byte(raw), "<override>", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return cty.NilVal, fmt.Errorf("parse override %q: %s", raw, diags.Error())
	}
	val, diags := expr.Value(nil)
	if diags.HasErrors() {
		return cty.NilVal, fmt.Errorf("evaluate override %q: %s", raw, diags.Error())
	}
	return coerceToType(val, declared)
}
