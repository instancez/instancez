package config

import (
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	// from "x", import "x", import("x"): ESM needs the exact file.
	esmImportRE    = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(?\s*)["'](\.{1,2}/[^"']*)["']`)
	cjsImportRE    = regexp.MustCompile(`\brequire\s*\(\s*["'](\.{1,2}/[^"']*)["']\s*\)`)
	lineCommentRE  = regexp.MustCompile(`(?m)(^|[\s;{}()])//[^\n]*`)
	blockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func isJSFile(p string) bool {
	switch path.Ext(p) {
	case ".js", ".mjs", ".cjs":
		return true
	}
	return false
}

// CheckFunctionImports returns one message per relative import in the
// project-relative file set whose target file is missing.
func CheckFunctionImports(files map[string]string) []string {
	var probs []string
	for _, p := range sortedFileNames(files) {
		if !isJSFile(p) {
			continue
		}
		src := blockCommentRE.ReplaceAllString(lineCommentRE.ReplaceAllString(files[p], "$1"), "")
		for _, m := range esmImportRE.FindAllStringSubmatch(src, -1) {
			if strings.ContainsAny(m[1], "?#") {
				continue
			}
			if _, ok := files[path.Join(path.Dir(p), m[1])]; !ok {
				probs = append(probs, fmt.Sprintf("%s imports %q, which is not in functions/", p, m[1]))
			}
		}
		for _, m := range cjsImportRE.FindAllStringSubmatch(src, -1) {
			if !cjsResolves(files, path.Join(path.Dir(p), m[1])) {
				probs = append(probs, fmt.Sprintf("%s requires %q, which is not in functions/", p, m[1]))
			}
		}
	}
	return probs
}

func cjsResolves(files map[string]string, target string) bool {
	for _, c := range []string{target, target + ".js", target + ".cjs", target + ".json", target + "/index.js"} {
		if _, ok := files[c]; ok {
			return true
		}
	}
	return false
}

func sortedFileNames(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }
