// Package snapshot reads and writes schema snapshots: the files
// `orchard schema pull` writes to .orchard/schema/<alias>.json so that
// plan-time checks and editor tooling can see the shape of an
// external system (a database today; an OpenAPI spec or protobuf
// descriptors later) without connecting to it.
//
// Every snapshot has the same envelope. Its body is specific to its
// Kind and is interpreted only by the provider that wrote it (for
// "database", see pkg/dbschema).
package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Format is the envelope format version. Readers reject snapshots
// with a format they don't know.
const Format = "1"

// ProjectDirName is the directory that marks an Orchard project root
// and holds its snapshots.
const ProjectDirName = ".orchard"

// Snapshot is one captured schema.
type Snapshot struct {
	Format string `json:"format"`
	// Kind names the body's format, e.g. "database".
	Kind string `json:"kind"`
	// Target identifies what was captured, without secrets, e.g.
	// "postgres/app_dev". `schema pull` won't replace a snapshot of a
	// different target without --force.
	Target     string          `json:"target"`
	Provider   Provider        `json:"provider"`
	CapturedAt time.Time       `json:"captured_at"`
	Body       json.RawMessage `json:"body"`
}

// Provider records which provider produced a snapshot. Tooling that
// can't tell the alias (a component file names action types, not
// aliases) matches snapshots by Source.
type Provider struct {
	Source string `json:"source"`
	Alias  string `json:"alias"`
}

// FindProject walks up from start (a file or directory) to the
// nearest directory containing a .orchard directory, the way git
// finds .git. It returns that directory and true, or "" and false.
func FindProject(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, ProjectDirName)); err == nil && info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Path returns where the snapshot for a provider alias lives in a
// project.
func Path(project, alias string) string {
	return filepath.Join(project, ProjectDirName, "schema", alias+".json")
}

// ErrNotFound is returned by Read when no snapshot exists at path.
var ErrNotFound = errors.New("snapshot not found")

// Read loads the snapshot at path. A missing file returns an error
// wrapping ErrNotFound.
func Read(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if s.Format != Format {
		return nil, fmt.Errorf("%s: snapshot format %q not supported (want %q)", path, s.Format, Format)
	}
	return &s, nil
}

// Write stores s at path, creating parent directories. The file is
// indented JSON with a trailing newline, so it diffs cleanly when
// committed, and is replaced atomically (temp file + rename). Format
// is filled in if empty.
func Write(path string, s *Snapshot) error {
	if s.Format == "" {
		s.Format = Format
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(out.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
