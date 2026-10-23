package protocol

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestParseHCLSchemaPostgres(t *testing.T) {
	src, err := os.ReadFile("testdata/postgres.schema.hcl")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	s, err := ParseHCLSchema(src, "postgres.schema.hcl")
	if err != nil {
		t.Fatalf("ParseHCLSchema: %v", err)
	}

	if s.Name != "postgres" {
		t.Errorf("name: got %q want postgres", s.Name)
	}
	if s.Version != "1" || s.Protocol != "1" {
		t.Errorf("version/protocol: %q/%q", s.Version, s.Protocol)
	}

	if len(s.Config) != 2 {
		t.Fatalf("expected 2 config attrs, got %d", len(s.Config))
	}
	dsn := s.Config["dsn"]
	if dsn.Type != "string" || !dsn.Required {
		t.Errorf("config.dsn: got %+v", dsn)
	}
	maxConns := s.Config["max_conns"]
	if maxConns.Type != "number" {
		t.Errorf("config.max_conns type: got %q", maxConns.Type)
	}
	if string(maxConns.Default) != "10" {
		t.Errorf("config.max_conns default: got %s want 10", maxConns.Default)
	}

	sql, ok := s.Actions["sql"]
	if !ok {
		t.Fatalf("missing action.sql")
	}
	if sql.Attrs["query"].Type != "string" || !sql.Attrs["query"].Required {
		t.Errorf("action.sql.query: got %+v", sql.Attrs["query"])
	}
	if sql.Outputs == nil || sql.Outputs.Type != "any" {
		t.Errorf("action.sql.outputs: got %+v, want any", sql.Outputs)
	}

	insert, ok := s.Actions["insert"]
	if !ok {
		t.Fatalf("missing action.insert")
	}
	if insert.Attrs["values"].Type != "map(dynamic)" && insert.Attrs["values"].Type != "map(any)" {
		t.Errorf("action.insert.values type: got %q", insert.Attrs["values"].Type)
	}
	if insert.Attrs["returning"].Type != "list(string)" {
		t.Errorf("action.insert.returning type: got %q", insert.Attrs["returning"].Type)
	}
	// Default for returning is `[]`, coerced to list(string). The JSON
	// form ctyjson produces for an empty list is `[]`.
	if string(insert.Attrs["returning"].Default) != "[]" {
		t.Errorf("action.insert.returning default: got %s", insert.Attrs["returning"].Default)
	}
	if insert.Outputs == nil {
		t.Fatal("action.insert.outputs is nil")
	}
	if !strings.Contains(insert.Outputs.Type, "id") {
		t.Errorf("action.insert.outputs type: got %q", insert.Outputs.Type)
	}

	// And the parsed schema must round-trip through JSON.
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ProviderSchema
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestParseHCLSchemaErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "no provider block",
			src:  `# empty`,
			want: "expected exactly one provider block",
		},
		{
			name: "missing version",
			src: `
provider "x" {
  protocol = "1"
  action "a" {
    attribute "q" {
      type = string
    }
  }
}`,
			want: "version",
		},
		{
			name: "wrong protocol",
			src: `
provider "x" {
  version  = "1"
  protocol = "99"
  action "a" {
    attribute "q" {
      type = string
    }
  }
}`,
			want: "protocol version",
		},
		{
			name: "invalid type expression",
			src: `
provider "x" {
  version  = "1"
  protocol = "1"
  action "a" {
    attribute "q" {
      type = notatype
    }
  }
}`,
			want: "type",
		},
		{
			name: "duplicate action",
			src: `
provider "x" {
  version  = "1"
  protocol = "1"
  action "a" {
    attribute "q" {
      type = string
    }
  }
  action "a" {
    attribute "q" {
      type = string
    }
  }
}`,
			want: "duplicate action",
		},
		{
			name: "duplicate attribute",
			src: `
provider "x" {
  version  = "1"
  protocol = "1"
  action "a" {
    attribute "q" {
      type = string
    }
    attribute "q" {
      type = number
    }
  }
}`,
			want: "duplicate attribute",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseHCLSchema([]byte(c.src), c.name)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected error containing %q, got %q", c.want, err.Error())
			}
		})
	}
}

func TestParseHCLSchemaPathAttribute(t *testing.T) {
	s, err := ParseHCLSchema([]byte(`
provider "x" {
  version  = "1"
  protocol = "1"
  config {
    attribute "file" {
      type = string
      path = true
    }
    attribute "name" {
      type = string
    }
  }
  action "a" {
    attribute "q" {
      type = string
    }
  }
}`), "x.hcl")
	if err != nil {
		t.Fatalf("ParseHCLSchema: %v", err)
	}
	if !s.Config["file"].Path || s.Config["name"].Path {
		t.Fatalf("path flags: file=%v name=%v", s.Config["file"].Path, s.Config["name"].Path)
	}
}

func TestParseHCLSchemaPathMustBeString(t *testing.T) {
	_, err := ParseHCLSchema([]byte(`
provider "x" {
  version  = "1"
  protocol = "1"
  config {
    attribute "port" {
      type = number
      path = true
    }
  }
  action "a" {
    attribute "q" {
      type = string
    }
  }
}`), "x.hcl")
	if err == nil || !strings.Contains(err.Error(), "path attributes must be strings") {
		t.Fatalf("expected a path-type error, got %v", err)
	}
}
