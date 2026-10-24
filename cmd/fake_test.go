package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeAtPlanAndRun(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "fake.hcl")
	write := func(arg string) {
		writeFile(t, scenario, `scenario "fake" {
  required_providers {
    sh = { source = "builtin/exec" }
  }
  action "exec_run" "a" {
    command = "echo"
    args    = [`+arg+`]
  }
  output "city" { value = fake("address").city }
}
`)
	}

	write(`fake("emial")`)
	out, err := runCLI(t, "plan", scenario, "--no-color")
	if err == nil || !strings.Contains(out+err.Error(), `did you mean "email"?`) {
		t.Fatalf("plan should reject an unknown kind with a hint: %v\n%s", err, out)
	}

	write(`fake("email"), faketemplate("{firstname}-###"), "${fake("number", { min = 1, max = 9 })}"`)
	first, _ := seededValues(t, scenario, "--seed", "3")
	if !strings.Contains(first, "@") || !strings.Contains(first, `"city"`) {
		t.Fatalf("unexpected values: %s", first)
	}
	if again, _ := seededValues(t, scenario, "--seed", "3"); again != first {
		t.Errorf("same seed, different fake data:\n%s\n%s", first, again)
	}
}
