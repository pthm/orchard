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

	// Resolve variables: defaults + CLI overrides.
	resolved, err := resolveVariables(sf.Scenario.Variables, vars)
	if err != nil {
		return nil, fmt.Errorf("resolving variables: %w", err)
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
		Scenario:   sf.Scenario,
		Components: components,
		Variables:  resolved,
	}, nil
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

	return cf.Component, nil
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
