package docs_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/pthm/orchard/internal/docs"
	"github.com/pthm/orchard/pkg/protocol"
)

// updateGolden rewrites the golden file from the renderer's current
// output. Run with `go test ./internal/docs -update` after an
// intentional rendering change to accept the diff.
var updateGolden = flag.Bool("update", false, "update golden file")

// TestRenderGolden pins the Markdown the renderer emits for a
// fixture schema. The fixture is deliberately small but exercises
// every field the renderer touches: descriptions, notes, typed
// attrs with defaults, outputs with markdown, per-action examples,
// provider-level examples.
//
// When the renderer changes shape on purpose, run with -update to
// regenerate the golden and review the diff in a PR.
func TestRenderGolden(t *testing.T) {
	srcPath := filepath.Join("testdata", "fixture.schema.hcl")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	schema, err := protocol.ParseHCLSchema(src, srcPath)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	got := docs.Render(schema, "builtin/demo")

	goldenPath := filepath.Join("testdata", "fixture.golden.md")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create it)", err)
	}

	if !bytes.Equal([]byte(got), want) {
		t.Fatalf("rendered markdown differs from golden.\n--- got ---\n%s\n--- want ---\n%s",
			got, string(want))
	}
}
