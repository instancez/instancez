package cli

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// resolveVersion fills in the version and commit of go build/go install builds from build info.
func resolveVersion(ver, com string, info *debug.BuildInfo) (string, string) {
	if info == nil {
		return ver, com
	}
	if ver == "dev" {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			ver = strings.TrimPrefix(v, "v")
		}
	}
	if com == "unknown" {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				com = s.Value[:min(7, len(s.Value))]
			}
		}
	}
	return ver, com
}

func versionString() string {
	bi, _ := debug.ReadBuildInfo()
	v, c := resolveVersion(version, commit, bi)
	return fmt.Sprintf("instancez v%s (%s)", v, c)
}
