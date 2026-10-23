package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pthm/orchard/pkg/provider/grpc/grpctest"
)

// TestPlanChecksGRPCCallsFromSnapshot pulls a protobuf snapshot over
// reflection, stops the server, and plans: method, request and
// response-field typos all fail with no server running.
func TestPlanChecksGRPCCallsFromSnapshot(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".orchard"), 0o755); err != nil {
		t.Fatal(err)
	}
	addr, stop, err := grpctest.Start(grpctest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	scenario := func(name, body string) string {
		path := filepath.Join(project, name+".hcl")
		writeFile(t, path, `scenario "`+name+`" {
  required_providers {
    accounts = { source = "builtin/grpc" }
  }
  provider "accounts" {
    address = "`+addr+`"
  }
`+body+`
}
`)
		return path
	}
	good := scenario("good", `
  action "grpc_call" "org" {
    method  = "accounts.v1.Accounts/CreateOrg"
    request = { name = "Acme", plan = "PLAN_TEAM" }
  }
  action "grpc_call" "get" {
    method  = "accounts.v1.Accounts/GetOrg"
    request = { org_id = action.grpc_call.org.response.org.id }
  }
  output "created_at" { value = action.grpc_call.get.response.created_at }`)

	out, err := runCLI(t, "schema", "pull", good)
	if err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	stop()

	if out, err := runCLI(t, "plan", good, "--no-color"); err != nil || strings.Contains(out, "note:") {
		t.Fatalf("a valid scenario should plan cleanly: %v\n%s", err, out)
	}

	cases := []struct{ name, body, want string }{
		{"method_typo", `
  action "grpc_call" "org" {
    method = "accounts.v1.Accounts/CreateOrgs"
  }`, `has no method "CreateOrgs" (did you mean "CreateOrg"?)`},
		{"request_typo", `
  action "grpc_call" "org" {
    method  = "accounts.v1.Accounts/CreateOrg"
    request = { nme = "Acme" }
  }`, `has no field "nme" (did you mean "name"?)`},
		{"response_typo", `
  action "grpc_call" "org" {
    method  = "accounts.v1.Accounts/CreateOrg"
    request = { name = "Acme" }
  }
  output "id" { value = action.grpc_call.org.response.org.idd }`, `attribute named "idd"`},
		{"wrong_type_reference", `
  action "grpc_call" "org" {
    method  = "accounts.v1.Accounts/CreateOrg"
    request = { name = "Acme" }
  }
  action "grpc_call" "again" {
    method  = "accounts.v1.Accounts/CreateOrg"
    request = { name = "B", owner = { age = action.grpc_call.org.response.org.name } }
  }`, `field "owner.age" is int32; got string`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := runCLI(t, "plan", scenario(c.name, c.body), "--no-color")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected %q, got %v\n%s", c.want, err, out)
			}
		})
	}
}
