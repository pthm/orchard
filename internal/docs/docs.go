// Package docs renders a protocol.ProviderSchema into human-readable
// Markdown. The layout is fixed — there are no templates — so any
// provider, builtin or external, can produce identical-looking docs
// from its schema alone.
//
// Narrative content (prose intros, notes, worked examples) lives in
// the schema itself: description fields accept Markdown, and example
// blocks carry HCL snippets with optional captions. This is the
// trade-off for template-free generation: provider authors put more
// into their schema, and tooling picks up the rest automatically.
package docs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pthm/orchard/pkg/protocol"
)

// Render returns the Markdown documentation page for schema. The
// source string is the provider source the user would put in their
// scenario (e.g. "builtin/postgres" or "exec:./bin/my-provider");
// it appears in the "Source" line.
func Render(schema protocol.ProviderSchema, source string) string {
	var b strings.Builder
	writeFrontMatter(&b, schema)
	writeHeader(&b, schema, source)
	writeConfig(&b, schema)
	writeActions(&b, schema)
	writeProviderExamples(&b, schema)
	writeNotes(&b, "Notes", schema.Notes)
	return b.String()
}

func writeFrontMatter(b *strings.Builder, schema protocol.ProviderSchema) {
	// weight 10 sorts built-in provider pages above hand-written ones like
	// "External providers" in the sidebar.
	fmt.Fprintf(b, "---\ntitle: %s\nweight: 10\n---\n\n", schema.Name)
}

func writeHeader(b *strings.Builder, schema protocol.ProviderSchema, source string) {
	fmt.Fprintf(b, "**Source:** `%s`\n\n", source)
	if schema.Description != "" {
		b.WriteString(strings.TrimSpace(schema.Description))
		b.WriteString("\n\n")
	}
}

func writeConfig(b *strings.Builder, schema protocol.ProviderSchema) {
	if len(schema.Config) == 0 {
		return
	}
	b.WriteString("## Provider configuration\n\n")
	writeAttrTable(b, schema.Config)
}

func writeActions(b *strings.Builder, schema protocol.ProviderSchema) {
	if len(schema.Actions) == 0 {
		return
	}
	b.WriteString("## Actions\n\n")
	for _, name := range schema.ActionNames() {
		writeAction(b, name, schema.Actions[name])
	}
}

func writeAction(b *strings.Builder, name string, action protocol.ActionSchema) {
	fmt.Fprintf(b, "### `%s`\n\n", name)

	if action.Description != "" {
		b.WriteString(strings.TrimSpace(action.Description))
		b.WriteString("\n\n")
	}

	// Examples before the reference tables: reading a usage snippet
	// first, then cross-referencing attributes against it, is the
	// style Terraform provider docs use and it works well.
	for _, ex := range action.Examples {
		writeExample(b, ex)
	}

	if len(action.Attrs) > 0 {
		b.WriteString("**Attributes**\n\n")
		writeAttrTable(b, action.Attrs)
	}

	if action.Outputs != nil {
		b.WriteString("**Outputs**\n\n")
		fmt.Fprintf(b, "Type: `%s`.\n\n", action.Outputs.Type)
		if action.Outputs.Description != "" {
			b.WriteString(strings.TrimSpace(action.Outputs.Description))
			b.WriteString("\n\n")
		}
	}

	if action.Notes != "" {
		b.WriteString(strings.TrimSpace(action.Notes))
		b.WriteString("\n\n")
	}
}

func writeProviderExamples(b *strings.Builder, schema protocol.ProviderSchema) {
	if len(schema.Examples) == 0 {
		return
	}
	b.WriteString("## Examples\n\n")
	for _, ex := range schema.Examples {
		writeExample(b, ex)
	}
}

func writeNotes(b *strings.Builder, heading, notes string) {
	if strings.TrimSpace(notes) == "" {
		return
	}
	fmt.Fprintf(b, "## %s\n\n", heading)
	b.WriteString(strings.TrimSpace(notes))
	b.WriteString("\n\n")
}

func writeExample(b *strings.Builder, ex protocol.Example) {
	if ex.Description != "" {
		fmt.Fprintf(b, "%s\n\n", strings.TrimSpace(ex.Description))
	}
	code := strings.TrimRight(ex.Code, "\n")
	fmt.Fprintf(b, "```hcl\n%s\n```\n\n", code)
}

// writeAttrTable renders an attribute map as a reference table. The
// table columns match the hand-written reference pages: name, type,
// required, default, description.
func writeAttrTable(b *strings.Builder, attrs map[string]protocol.AttrSchema) {
	b.WriteString("| Name | Type | Required | Default | Description |\n")
	b.WriteString("|------|------|----------|---------|-------------|\n")

	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		attr := attrs[name]
		fmt.Fprintf(b, "| `%s` | `%s` | %s | %s | %s |\n",
			name,
			attr.Type,
			boolCell(attr.Required),
			defaultCell(attr.Default),
			descCell(attr.Description),
		)
	}
	b.WriteString("\n")
}

func boolCell(required bool) string {
	if required {
		return "yes"
	}
	return "no"
}

// defaultCell renders a JSON-encoded cty default into a compact cell
// representation. Strings lose their surrounding quotes so the table
// doesn't stutter; other types are rendered as their JSON form.
func defaultCell(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "—"
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "—"
	}
	// Strip outer quotes from string defaults so the cell reads
	// `"GET"` as GET, not "\"GET\"" in rendered markdown.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return fmt.Sprintf("`%q`", s)
	}
	return fmt.Sprintf("`%s`", trimmed)
}

// descCell collapses newlines in a description so a multi-line field
// still renders as a single table row. Callers that want multi-line
// prose should keep it in the provider/action description, which is
// rendered outside the table.
func descCell(desc string) string {
	if desc == "" {
		return ""
	}
	// A description intended for tables should be a single line.
	// If one slips in with newlines, collapse whitespace rather than
	// break the table layout.
	desc = strings.TrimSpace(desc)
	desc = strings.ReplaceAll(desc, "\r\n", " ")
	desc = strings.ReplaceAll(desc, "\n", " ")
	return desc
}
