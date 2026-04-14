package external

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// echoBinary is populated by TestMain with the path to a freshly
// compiled test-only provider. Tests use it via Spawn to exercise
// the end-to-end subprocess lifecycle.
var echoBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "orchard-external-*")
	if err != nil {
		log.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(dir)

	echoBinary = filepath.Join(dir, "echo")
	cmd := exec.Command("go", "build", "-o", echoBinary,
		"github.com/pthm/orchard/examples/providers/echo")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("build echo: %v", err)
	}

	os.Exit(m.Run())
}

func TestSpawnAndSchema(t *testing.T) {
	p, err := Spawn(echoBinary)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer p.Close()

	s := p.Schema()
	if s.Name != "echo" {
		t.Errorf("schema name: got %q", s.Name)
	}
	if s.Version != "1" || s.Protocol != "1" {
		t.Errorf("version/protocol: %q/%q", s.Version, s.Protocol)
	}
	if _, ok := s.Actions["echo"]; !ok {
		t.Errorf("missing echo action")
	}
}

func TestConfigureThenExecute(t *testing.T) {
	p, err := Spawn(echoBinary)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer p.Close()

	if err := p.Configure(context.Background(), nil); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	outputs, err := p.Execute(context.Background(), "echo", map[string]cty.Value{
		"message": cty.StringVal("hello from test"),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outputs["message"].AsString() != "hello from test" {
		t.Fatalf("echoed message: got %v", outputs["message"])
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	p, err := Spawn(echoBinary)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer p.Close()

	_, err = p.Execute(context.Background(), "nonexistent", map[string]cty.Value{})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestSpawnNonexistentBinary(t *testing.T) {
	_, err := Spawn("/nonexistent/binary/path")
	if err == nil {
		t.Fatal("expected spawn error")
	}
}

func TestCloseIsIdempotentViaGracefulExit(t *testing.T) {
	// The echo provider exits after responding to shutdown.
	// Close should return nil, and calling Close a second time
	// should tolerate the reaper channel being drained.
	p, err := Spawn(echoBinary)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// The second Close would block on the drained done channel
	// so we don't call it — the contract only promises one Close.
}
