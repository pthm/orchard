package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// These variables are set via ldflags by GoReleaser.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// init fills Version/Commit/Date from the embedded build info when they were
// not stamped via ldflags, so "go install ...@vX" builds report their real
// version.
func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		fromBuildInfo(info)
	}
}

func fromBuildInfo(info *debug.BuildInfo) {
	if v := info.Main.Version; v != "" && v != "(devel)" {
		// Match GoReleaser's {{.Version}}, which has no "v" prefix.
		Version = strings.TrimPrefix(v, "v")
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value[:min(7, len(s.Value))]
		case "vcs.time":
			Date = s.Value
		}
	}
}

// Info returns formatted version information.
func Info() string {
	return fmt.Sprintf("orchard %s (commit: %s, built: %s) %s",
		Version, Commit, Date, runtime.Version())
}
