package parser

import (
	"github.com/hashicorp/hcl/v2"
)

// ScenarioFile is the top-level HCL structure for a scenario file.
//
// Scenarios is a slice so a single file can hold multiple scenario
// blocks — useful for grouping related scenarios (e.g. all UAT
// cases for a feature) without proliferating files. Callers pick
// one by name via LoadScenario's scenario parameter; a file
// containing a single scenario can still pass "" and have it
// auto-selected.
type ScenarioFile struct {
	Scenarios []*ScenarioBlock `hcl:"scenario,block"`
}

// ScenarioBlock represents: scenario "name" { ... }
//
// Description and Tags are optional metadata surfaced by `orchard
// list` and reserved for future search / categorisation tooling. They
// have no effect on execution and are ignored by the engine.
type ScenarioBlock struct {
	Name              string                  `hcl:"name,label"`
	Description       string                  `hcl:"description,optional"`
	Tags              []string                `hcl:"tags,optional"`
	Seed              *uint64                 `hcl:"seed,optional"`
	RequiredProviders *RequiredProvidersBlock `hcl:"required_providers,block"`
	Providers         []*ProviderConfigBlock  `hcl:"provider,block"`
	Variables         []*VariableBlock        `hcl:"variable,block"`
	Components        []*ComponentRef         `hcl:"component,block"`
	Actions           []*ActionBlock          `hcl:"action,block"`
	Outputs           []*OutputBlock          `hcl:"output,block"`
}

// RequiredProvidersBlock declares the providers a scenario needs.
// The body is a flat attribute set where each attribute is a
// provider name assigned to an object describing the source:
//
//	required_providers {
//	  postgres = { source = "builtin/postgres" }
//	  http     = { source = "builtin/http" }
//	}
//
// The body is captured as hcl.Body so each entry's object can be
// decoded dynamically — gohcl cannot flatten a "remaining-attribute-
// set-of-objects" shape into a typed struct.
type RequiredProvidersBlock struct {
	Body hcl.Body `hcl:",remain"`
}

// ProviderConfigBlock represents: provider "<name>" { <config attributes> }.
// The body is captured for deferred evaluation against the scenario's
// variable context.
type ProviderConfigBlock struct {
	Name   string   `hcl:"name,label"`
	Remain hcl.Body `hcl:",remain"`
}

// ComponentFile is the top-level HCL structure for a component definition file.
type ComponentFile struct {
	Component *ComponentDefBlock `hcl:"component,block"`
}

// ComponentDefBlock defines a reusable component: component "name" { ... }
//
// Description and Tags mirror the scenario-level fields: free-form
// metadata surfaced by tooling, ignored by the engine.
type ComponentDefBlock struct {
	Name        string           `hcl:"name,label"`
	Description string           `hcl:"description,optional"`
	Tags        []string         `hcl:"tags,optional"`
	Variables   []*VariableBlock `hcl:"variable,block"`
	Actions     []*ActionBlock   `hcl:"action,block"`
	Outputs     []*OutputBlock   `hcl:"output,block"`
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

// VariableBlock represents: variable "name" { type = <typeexpr>, default = <value>, description = "..." }.
//
// Body captures the block's attributes as-is (via gohcl's
// `,remain`). We decode type/default/description manually in the
// parser rather than relying on gohcl's optional hcl.Expression
// handling, which synthesizes a cty.NullVal placeholder for absent
// optional fields — making "absent default" and "default = null"
// indistinguishable.
type VariableBlock struct {
	Name string   `hcl:"name,label"`
	Body hcl.Body `hcl:",remain"`
}

// OutputBlock represents: output "name" { value = <expr> }
type OutputBlock struct {
	Name  string         `hcl:"name,label"`
	Value hcl.Expression `hcl:"value"`
}
