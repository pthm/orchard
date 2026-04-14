package parser

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestVariableTypedDefault(t *testing.T) {
	path := writeTempScenario(t, `
scenario "typed" {
  variable "count" {
    type    = number
    default = 5
  }
  variable "flag" {
    type    = bool
    default = true
  }
  variable "tags" {
    type    = list(string)
    default = ["a", "b"]
  }
}`)
	ps, err := LoadScenario(path, nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if got := ps.Variables["count"]; got.Type() != cty.Number {
		t.Errorf("count: got type %s", got.Type().FriendlyName())
	}
	if got := ps.Variables["flag"]; got.Type() != cty.Bool {
		t.Errorf("flag: got type %s", got.Type().FriendlyName())
	}
	if tags := ps.Variables["tags"]; !tags.Type().IsListType() {
		t.Errorf("tags: got type %s", tags.Type().FriendlyName())
	}
}

func TestVariableOverrideCoercion(t *testing.T) {
	path := writeTempScenario(t, `
scenario "typed_override" {
  variable "count" {
    type    = number
    default = 5
  }
  variable "flag" {
    type    = bool
    default = false
  }
}`)
	ps, err := LoadScenario(path, map[string]string{
		"count": "42",
		"flag":  "true",
	})
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	n, _ := ps.Variables["count"].AsBigFloat().Int64()
	if n != 42 {
		t.Errorf("count override: got %d", n)
	}
	if !ps.Variables["flag"].True() {
		t.Errorf("flag override: got false")
	}
}

func TestVariableOverrideTypeMismatch(t *testing.T) {
	path := writeTempScenario(t, `
scenario "mismatch" {
  variable "count" {
    type = number
  }
}`)
	_, err := LoadScenario(path, map[string]string{"count": "not_a_number"})
	if err == nil {
		t.Fatal("expected coercion error")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Fatalf("error should name the variable: %v", err)
	}
}

func TestVariableDefaultTypeMismatch(t *testing.T) {
	path := writeTempScenario(t, `
scenario "bad_default" {
  variable "count" {
    type    = number
    default = "not a number"
  }
}`)
	_, err := LoadScenario(path, nil)
	if err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("expected type-mismatch on default, got %v", err)
	}
}

func TestVariableUntypedStillWorks(t *testing.T) {
	// Regression: removing the Remain field should not break existing
	// scenarios that omit type.
	path := writeTempScenario(t, `
scenario "untyped" {
  variable "name" {
    default = "Acme"
  }
}`)
	ps, err := LoadScenario(path, nil)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if ps.Variables["name"].AsString() != "Acme" {
		t.Errorf("name: got %#v", ps.Variables["name"])
	}
}

func TestVariableStringOverrideRaw(t *testing.T) {
	// String-typed or untyped variables take the raw string verbatim,
	// so users don't have to quote on the command line.
	path := writeTempScenario(t, `
scenario "strings" {
  variable "explicit" {
    type    = string
    default = "default"
  }
  variable "implicit" {
    default = "default"
  }
}`)
	ps, err := LoadScenario(path, map[string]string{
		"explicit": "has spaces and symbols !@#",
		"implicit": "also raw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ps.Variables["explicit"].AsString() != "has spaces and symbols !@#" {
		t.Errorf("explicit: got %q", ps.Variables["explicit"].AsString())
	}
	if ps.Variables["implicit"].AsString() != "also raw" {
		t.Errorf("implicit: got %q", ps.Variables["implicit"].AsString())
	}
}
