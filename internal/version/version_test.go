package version

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	Version, Commit, Date = "dev", "none", "unknown"
	fromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v0.2.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef0123456789"},
			{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		},
	})
	if Version != "0.2.0" || Commit != "abcdef0" || Date != "2026-01-02T03:04:05Z" {
		t.Fatalf("got %q %q %q", Version, Commit, Date)
	}

	Version = "dev"
	fromBuildInfo(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if Version != "dev" {
		t.Fatalf("(devel) should keep dev, got %q", Version)
	}
}
