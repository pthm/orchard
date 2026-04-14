package exec

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"
)

// These tests rely on a POSIX shell + core utilities (sh, echo,
// env, pwd, cat, false). All green on macOS and Linux; skipped on
// Windows where the shell is different.
func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("exec provider POSIX tests skipped on Windows")
	}
}

func configured(t *testing.T, cfg map[string]cty.Value) *Provider {
	t.Helper()
	p := New()
	if err := p.Configure(context.Background(), cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestSchemaLoadsFromEmbeddedHCL(t *testing.T) {
	p := New()
	s := p.Schema()
	if s.Name != "exec" {
		t.Fatalf("name: got %q want exec", s.Name)
	}
	if _, ok := s.Actions["run"]; !ok {
		t.Fatal("missing run action")
	}
	attrs := s.Actions["run"].Attrs
	if attrs["command"].Type != "string" || !attrs["command"].Required {
		t.Errorf("run.command: %+v", attrs["command"])
	}
}

func TestExecSimpleEcho(t *testing.T) {
	skipIfWindows(t)
	p := configured(t, nil)
	out, err := p.Execute(context.Background(), "run", map[string]cty.Value{
		"command": cty.StringVal("echo"),
		"args":    cty.ListVal([]cty.Value{cty.StringVal("hello")}),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := out["stdout"].AsString(); strings.TrimSpace(got) != "hello" {
		t.Errorf("stdout: got %q", got)
	}
	code, _ := out["exit_code"].AsBigFloat().Int64()
	if code != 0 {
		t.Errorf("exit_code: got %d want 0", code)
	}
}

func TestExecArgsPassedInOrder(t *testing.T) {
	skipIfWindows(t)
	p := configured(t, nil)
	out, err := p.Execute(context.Background(), "run", map[string]cty.Value{
		"command": cty.StringVal("echo"),
		"args": cty.ListVal([]cty.Value{
			cty.StringVal("one"), cty.StringVal("two"), cty.StringVal("three"),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out["stdout"].AsString()); got != "one two three" {
		t.Errorf("stdout: got %q", got)
	}
}

func TestExecNonZeroExitErrors(t *testing.T) {
	skipIfWindows(t)
	p := configured(t, nil)
	_, err := p.Execute(context.Background(), "run", map[string]cty.Value{
		"command": cty.StringVal("sh"),
		"args": cty.ListVal([]cty.Value{
			cty.StringVal("-c"),
			cty.StringVal("echo 'oh no' 1>&2; exit 7"),
		}),
	})
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "code 7") {
		t.Errorf("error should name the exit code: %v", err)
	}
	if !strings.Contains(err.Error(), "oh no") {
		t.Errorf("error should include stderr preview: %v", err)
	}
}

func TestExecStdinPassed(t *testing.T) {
	skipIfWindows(t)
	p := configured(t, nil)
	out, err := p.Execute(context.Background(), "run", map[string]cty.Value{
		"command": cty.StringVal("cat"),
		"stdin":   cty.StringVal("line one\nline two\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["stdout"].AsString(); got != "line one\nline two\n" {
		t.Errorf("stdout: got %q", got)
	}
}

func TestExecEnvMerging(t *testing.T) {
	skipIfWindows(t)
	p := configured(t, map[string]cty.Value{
		"env": cty.MapVal(map[string]cty.Value{
			"ORCHARD_TEST_DEFAULT":  cty.StringVal("from-provider"),
			"ORCHARD_TEST_OVERRIDE": cty.StringVal("provider-value"),
		}),
	})
	out, err := p.Execute(context.Background(), "run", map[string]cty.Value{
		"command": cty.StringVal("sh"),
		"args": cty.ListVal([]cty.Value{
			cty.StringVal("-c"),
			cty.StringVal(`echo "$ORCHARD_TEST_DEFAULT|$ORCHARD_TEST_OVERRIDE|$ORCHARD_TEST_ACTION"`),
		}),
		"env": cty.MapVal(map[string]cty.Value{
			"ORCHARD_TEST_OVERRIDE": cty.StringVal("action-wins"),
			"ORCHARD_TEST_ACTION":   cty.StringVal("from-action"),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out["stdout"].AsString())
	want := "from-provider|action-wins|from-action"
	if got != want {
		t.Errorf("stdout: got %q want %q", got, want)
	}
}

func TestExecWorkingDir(t *testing.T) {
	skipIfWindows(t)
	dir := t.TempDir()
	p := configured(t, nil)
	out, err := p.Execute(context.Background(), "run", map[string]cty.Value{
		"command": cty.StringVal("pwd"),
		"dir":     cty.StringVal(dir),
	})
	if err != nil {
		t.Fatal(err)
	}
	// macOS symlinks /var to /private/var; accept either form.
	got := strings.TrimSpace(out["stdout"].AsString())
	if got != dir && !strings.HasSuffix(got, dir) {
		t.Errorf("pwd: got %q, expected to match %q", got, dir)
	}
}

func TestExecCtxCancellation(t *testing.T) {
	skipIfWindows(t)
	p := configured(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Execute(ctx, "run", map[string]cty.Value{
		"command": cty.StringVal("sleep"),
		"args":    cty.ListVal([]cty.Value{cty.StringVal("5")}),
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if elapsed > 1*time.Second {
		t.Fatalf("cancellation should be prompt, took %s", elapsed)
	}
}

func TestExecMissingCommandErrors(t *testing.T) {
	p := configured(t, nil)
	_, err := p.Execute(context.Background(), "run", map[string]cty.Value{
		"command": cty.StringVal("/definitely/not/a/real/binary"),
	})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestExecUnknownActionRejected(t *testing.T) {
	p := configured(t, nil)
	_, err := p.Execute(context.Background(), "imaginary", map[string]cty.Value{})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown-action, got %v", err)
	}
}
