package record

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// NewID returns a run identifier of the form YYYYMMDDTHHMMSSZ_<8hex>.
// The timestamp prefix ensures lexicographic sort matches chronological
// order; the hex suffix handles collisions within the same second. UTC
// is always used so the ID is stable across machines.
//
// 8 hex characters give 2^32 values — ample for "same second" collision
// avoidance without bloating filenames.
func NewID(now time.Time) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failure is catastrophic; the zero suffix still
		// produces a valid, sortable ID.
		return fmt.Sprintf("%s_00000000", now.UTC().Format("20060102T150405Z"))
	}
	return fmt.Sprintf("%s_%s", now.UTC().Format("20060102T150405Z"), hex.EncodeToString(buf[:]))
}

// Filename returns the on-disk filename for a record with the given
// scenario name and run ID. The scenario name is sanitised —
// anything outside [A-Za-z0-9_-] becomes an underscore so operators
// can still find records with familiar shell globs even if the
// scenario block uses spaces, slashes, or other metacharacters.
func Filename(scenario, id string) string {
	return fmt.Sprintf("orchard_%s_run_%s.json", sanitiseScenarioName(scenario), id)
}

// sanitiseScenarioName keeps the filename predictable regardless of
// what the scenario block calls itself.
func sanitiseScenarioName(name string) string {
	if name == "" {
		return "unnamed"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "unnamed"
	}
	return out
}
