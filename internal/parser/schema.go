package parser

import (
	"github.com/hashicorp/hcl/v2"
)

// ScenarioFile is the top-level HCL structure for a scenario file.
type ScenarioFile struct {
	Scenario *ScenarioBlock `hcl:"scenario,block"`
}

// ScenarioBlock represents: scenario "name" { ... }
type ScenarioBlock struct {
	Name       string           `hcl:"name,label"`
	Variables  []*VariableBlock `hcl:"variable,block"`
	Components []*ComponentRef  `hcl:"component,block"`
	Actions    []*ActionBlock   `hcl:"action,block"`
	Outputs    []*OutputBlock   `hcl:"output,block"`
}

// ComponentFile is the top-level HCL structure for a component definition file.
type ComponentFile struct {
	Component *ComponentDefBlock `hcl:"component,block"`
}

// ComponentDefBlock defines a reusable component: component "name" { ... }
type ComponentDefBlock struct {
	Name      string           `hcl:"name,label"`
	Variables []*VariableBlock `hcl:"variable,block"`
	Actions   []*ActionBlock   `hcl:"action,block"`
	Outputs   []*OutputBlock   `hcl:"output,block"`
}

// ComponentRef is a component instance within a scenario.
// component "<type>" "<name>" { source = "..." inputs = { ... } }
type ComponentRef struct {
	Type   string   `hcl:"type,label"`
	Name   string   `hcl:"name,label"`
	Source string   `hcl:"source,attr"`
	Inputs hcl.Body `hcl:",remain"`
}

// ActionBlock represents: action "<type>" "<name>" { ... }
type ActionBlock struct {
	Type   string   `hcl:"type,label"`
	Name   string   `hcl:"name,label"`
	Remain hcl.Body `hcl:",remain"`
}

// VariableBlock represents: variable "name" { type = string; default = "value" }
type VariableBlock struct {
	Name    string         `hcl:"name,label"`
	Default hcl.Expression `hcl:"default,optional"`
	Remain  hcl.Body       `hcl:",remain"`
}

// OutputBlock represents: output "name" { value = <expr> }
type OutputBlock struct {
	Name  string         `hcl:"name,label"`
	Value hcl.Expression `hcl:"value"`
}
