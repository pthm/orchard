package sdk_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/pthm/orchard/pkg/provider"
	"github.com/pthm/orchard/pkg/provider/external"
	"github.com/pthm/orchard/pkg/sdk"
)

type config struct {
	Prefix string `json:"prefix" doc:"Prepended to every email."`
}

type address struct {
	City string  `json:"city"`
	Zip  *string `json:"zip"`
}

type createUser struct {
	Email   string    `json:"email"`
	Tags    []string  `json:"tags,omitempty"`
	Address *address  `json:"address"`
	Born    time.Time `json:"born,omitempty"`
}

type user struct {
	ID    int       `json:"id"`
	Email string    `json:"email"`
	Born  time.Time `json:"born"`
	Note  string    `json:"note,omitempty"`
}

type deleteUser struct {
	ID   int      `json:"id"`
	Tags []string `json:"tags,omitempty"`
}

// TestMain runs the test binary as the provider when the test spawns it.
func TestMain(m *testing.M) {
	if os.Getenv("SDK_TEST_PROVIDER") == "" {
		os.Exit(m.Run())
	}
	var prefix string
	p := sdk.New("app", "0.1.0")
	p.Configure(func(_ context.Context, c config) error {
		prefix = c.Prefix
		return nil
	})
	p.Action("app_create_user", "Create a user.", func(_ context.Context, in createUser) (*user, error) {
		fmt.Println("stdout is safe") // Serve sends it to stderr
		return &user{ID: 7, Email: prefix + in.Email, Born: in.Born}, nil
	})
	p.Action("app_delete_user", "Delete a user.", func(_ context.Context, in deleteUser) error {
		if in.ID != 7 || len(in.Tags) != 1 {
			return fmt.Errorf("unexpected undo input %+v", in)
		}
		return nil
	})
	p.Undo("app_create_user", "app_delete_user", func(in createUser, out *user) *deleteUser {
		if in.Email == "keep@example.com" {
			return nil
		}
		return &deleteUser{ID: out.ID, Tags: in.Tags}
	})
	p.Action("app_fail", "", func(context.Context) error { return errors.New("no such tenant") })
	p.Action("app_panic", "", func(context.Context) error { panic("boom") })
	if err := p.Serve(); err != nil {
		println(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

func TestProvider(t *testing.T) {
	t.Setenv("SDK_TEST_PROVIDER", "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	sp, err := external.LoadSchema(bin)
	if err != nil {
		t.Fatal(err)
	}
	s := sp.Schema()
	in := s.Actions["app_create_user"]
	for name, want := range map[string]string{
		"email":   "string",
		"tags":    "list(string)",
		"address": "object({city=string, zip=optional(string)})",
		"born":    "string",
	} {
		if got := in.Attrs[name].Type; got != want {
			t.Errorf("attr %s: type %q, want %q", name, got, want)
		}
	}
	if !in.Attrs["email"].Required || in.Attrs["tags"].Required || in.Attrs["address"].Required {
		t.Errorf("required flags wrong: %+v", in.Attrs)
	}
	if got := in.Outputs.Type; got != "object({born=string, email=string, id=number, note=optional(string)})" {
		t.Errorf("outputs type %q", got)
	}
	if got := s.Config["prefix"].Description; got != "Prepended to every email." {
		t.Errorf("config doc %q", got)
	}

	p, err := external.Spawn(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx := context.Background()
	if err := p.Configure(ctx, map[string]cty.Value{"prefix": cty.StringVal("qa+")}); err != nil {
		t.Fatal(err)
	}
	out, err := p.Execute(ctx, "app_create_user", map[string]cty.Value{
		"email":   cty.StringVal("a@example.com"),
		"tags":    cty.NullVal(cty.List(cty.String)),
		"address": cty.ObjectVal(map[string]cty.Value{"city": cty.StringVal("Leeds"), "zip": cty.NullVal(cty.String)}),
		"born":    cty.StringVal("2000-01-02T00:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["email"].AsString(); got != "qa+a@example.com" {
		t.Errorf("email = %q", got)
	}
	if got := out["born"].AsString(); got != "2000-01-02T00:00:00Z" {
		t.Errorf("born = %q", got)
	}
	if !out["note"].IsNull() {
		t.Errorf("note = %#v, want null", out["note"])
	}

	attrs := map[string]cty.Value{
		"email":   cty.StringVal("a@example.com"),
		"tags":    cty.ListVal([]cty.Value{cty.StringVal("qa")}),
		"address": cty.NullVal(cty.Object(map[string]cty.Type{"city": cty.String, "zip": cty.String})),
		"born":    cty.NullVal(cty.String),
	}
	_, undo, err := p.ExecuteUndo(ctx, "app_create_user", attrs, provider.InverseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if undo == nil || undo.Action != "app_delete_user" {
		t.Fatalf("undo = %+v", undo)
	}
	// The run record hands the attributes back with JSON's implied
	// types, a tuple for the list.
	if _, err := p.Execute(ctx, undo.Action, map[string]cty.Value{
		"id":   cty.NumberIntVal(7),
		"tags": cty.TupleVal([]cty.Value{cty.StringVal("qa")}),
	}); err != nil {
		t.Errorf("running undo: %v", err)
	}
	attrs["email"] = cty.StringVal("keep@example.com")
	if _, undo, err := p.ExecuteUndo(ctx, "app_create_user", attrs, provider.InverseOptions{}); err != nil || undo != nil {
		t.Errorf("nil undo: got %+v, %v", undo, err)
	}

	if _, err := p.Execute(ctx, "app_fail", nil); err == nil || !strings.Contains(err.Error(), "no such tenant") {
		t.Errorf("app_fail error = %v", err)
	}
	if _, err := p.Execute(ctx, "app_panic", nil); err == nil || !strings.Contains(err.Error(), "panic: boom") {
		t.Errorf("app_panic error = %v", err)
	}
	// The provider survives a panic.
	if _, err := p.Execute(ctx, "app_fail", nil); err == nil || !strings.Contains(err.Error(), "no such tenant") {
		t.Errorf("after panic: %v", err)
	}
}

func TestActionRejectsBadShapes(t *testing.T) {
	for name, fn := range map[string]any{
		"no ctx":        func(createUser) error { return nil },
		"no error":      func(context.Context, createUser) user { return user{} },
		"scalar arg":    func(context.Context, string) error { return nil },
		"scalar result": func(context.Context) (int, error) { return 0, nil },
		"chan field":    func(context.Context, struct{ C chan int }) error { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			sdk.New("x", "1").Action("x_y", "", fn)
		})
	}
}

func TestUndoRejectsBadShapes(t *testing.T) {
	for name, fn := range map[string]any{
		"wrong args":   func(user) deleteUser { return deleteUser{} },
		"wrong result": func(createUser, user) user { return user{} },
		"no result":    func(createUser, user) {},
	} {
		t.Run(name, func(t *testing.T) {
			p := sdk.New("x", "1")
			p.Action("x_create", "", func(context.Context, createUser) (user, error) { return user{}, nil })
			p.Action("x_delete", "", func(context.Context, deleteUser) error { return nil })
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			p.Undo("x_create", "x_delete", fn)
		})
	}
}
