package cli

import (
	"fmt"
	"regexp"
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
	if com == "unknown" {
		if m := pseudoRev.FindStringSubmatch(ver); m != nil {
			com = m[1][:7]
		}
	}
	return ver, com
}

// pseudoRev matches the 12-char revision of a pseudo-version such as 0.0.13-0.20261102120000-abcdef123456.
var pseudoRev = regexp.MustCompile(`[-.]\d{14}-([0-9a-f]{12})(?:\+|$)`)

func versionString() string {
	bi, _ := debug.ReadBuildInfo()
	v, c := resolveVersion(version, commit, bi)
	return fmt.Sprintf("instancez v%s (%s)", v, c)
}
