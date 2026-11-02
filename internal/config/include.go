package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

// Resolved is a main config file with its include: fragments merged in.
type Resolved struct {
	Data    []byte
	Version string // changes when the main file or any fragment changes
}

var includeSections = []string{"tables", "storage", "rpc", "functions"}

// ReadConfigFile returns the config at path with includes merged; without include: the bytes are untouched.
func ReadConfigFile(path string) ([]byte, error) {
	r, err := ResolveIncludes(path)
	if err != nil {
		return nil, err
	}
	return r.Data, nil
}

// HasInclude reports whether the top-level include: key lists at least one pattern.
func HasInclude(data []byte) bool {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	_, v := mappingEntry(rootMapping(&doc), "include")
	return v != nil && v.Kind == yaml.SequenceNode && len(v.Content) > 0
}

func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

func mappingEntry(m *yaml.Node, key string) (int, *yaml.Node) {
	if m == nil {
		return -1, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i, m.Content[i+1]
		}
	}
	return -1, nil
}

func removeEntry(m *yaml.Node, idx int) { m.Content = slices.Delete(m.Content, idx, idx+2) }

func cfgErr(path, msg string, err error) error {
	return &domain.ConfigError{Path: path, Message: msg, Err: err}
}

// ResolveIncludes merges the files named by the main file's include: list into one YAML document.
func ResolveIncludes(path string) (*Resolved, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, cfgErr(path, "cannot read file", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, cfgErr(path, "cannot stat file", err)
	}
	version := fileVersionToken(info)
	plain := &Resolved{Data: data, Version: version}

	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return plain, nil // the normal parse reports the syntax error
	}
	root := rootMapping(&doc)
	idx, inc := mappingEntry(root, "include")
	if inc == nil || (inc.Kind != yaml.SequenceNode && (inc.Kind != yaml.ScalarNode || inc.Tag != "!!null")) {
		return plain, nil
	}

	absMain, err := filepath.Abs(path)
	if err != nil {
		return nil, cfgErr(path, "cannot resolve path", err)
	}
	realDir, err := filepath.EvalSymlinks(filepath.Dir(absMain))
	if err != nil {
		return nil, cfgErr(path, "cannot resolve project directory", err)
	}
	realMain, err := filepath.EvalSymlinks(absMain)
	if err != nil {
		return nil, cfgErr(path, "cannot resolve path", err)
	}
	dir := filepath.Dir(absMain)

	files := map[string]string{} // real path -> display path
	for _, n := range inc.Content {
		if n.Kind != yaml.ScalarNode || n.Value == "" {
			return nil, cfgErr(path, fmt.Sprintf("line %d: include entries must be non-empty strings", n.Line), nil)
		}
		matches, err := globInclude(dir, realDir, realMain, n.Value)
		if err != nil {
			return nil, cfgErr(path, err.Error(), nil)
		}
		for real, disp := range matches {
			files[real] = disp
		}
	}

	reals := make([]string, 0, len(files))
	for r := range files {
		reals = append(reals, r)
	}
	sort.Slice(reals, func(i, j int) bool { return files[reals[i]] < files[reals[j]] })

	mainName := filepath.ToSlash(filepath.Base(absMain))
	origin := map[string]string{} // "section.key" -> file
	for _, sec := range includeSections {
		if _, v := mappingEntry(root, sec); v != nil && v.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(v.Content); i += 2 {
				origin[sec+"."+v.Content[i].Value] = mainName
			}
		}
	}

	hash := sha256.New()
	for _, real := range reals {
		disp := files[real]
		fp := filepath.Join(dir, filepath.FromSlash(disp))
		fdata, err := os.ReadFile(real)
		if err != nil {
			return nil, cfgErr(fp, "cannot read file", err)
		}
		if err := mergeFragment(root, fp, disp, fdata, origin, mainName); err != nil {
			return nil, err
		}
		fi, err := os.Stat(real)
		if err != nil {
			return nil, cfgErr(fp, "cannot stat file", err)
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\n", fp, fileVersionToken(fi))
	}

	removeEntry(root, idx)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, cfgErr(path, "cannot merge includes", err)
	}
	_ = enc.Close()

	return &Resolved{
		Data:    buf.Bytes(),
		Version: fmt.Sprintf("%s+%x", version, hash.Sum(nil)[:8]),
	}, nil
}

// checkSyntax reports a file's syntax error or unknown keys with the file name and line.
func checkSyntax(path string, data []byte) error {
	var probe domain.Config
	if err := decodeYAMLStrict(data, &probe); err != nil && !errors.Is(err, io.EOF) {
		return cfgErr(path, err.Error(), nil)
	}
	for _, e := range probe.UnknownKeys {
		if strings.HasPrefix(e.Message, "unknown key") {
			return cfgErr(path, fmt.Sprintf("line %d: %s under %s", e.Line, e.Message, e.Path), nil)
		}
	}
	return nil
}

func globInclude(dir, realDir, realMain, pattern string) (map[string]string, error) {
	if strings.Contains(pattern, "${") {
		return nil, fmt.Errorf("include %q: ${VAR} is not supported in include patterns", pattern)
	}
	if filepath.IsAbs(pattern) {
		return nil, fmt.Errorf("include %q: absolute paths are not allowed", pattern)
	}
	if strings.ContainsAny(filepath.Dir(pattern), "*?[") {
		return nil, fmt.Errorf("include %q: glob characters are only allowed in the last path element", pattern)
	}
	clean := filepath.Clean(pattern)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("include %q: resolves outside the project directory", pattern)
	}
	matches, err := filepath.Glob(filepath.Join(dir, clean))
	if err != nil {
		return nil, fmt.Errorf("include %q: %v", pattern, err)
	}
	out := map[string]string{}
	sawMain := false
	candidates := 0
	for _, m := range matches {
		if isEditorTemp(filepath.Base(m), filepath.Base(clean)) {
			continue
		}
		candidates++
		real, err := filepath.EvalSymlinks(m)
		if err != nil {
			return nil, fmt.Errorf("include %q: %v", pattern, err)
		}
		rel, err := filepath.Rel(realDir, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("include %q: resolves outside the project directory", pattern)
		}
		fi, err := os.Stat(real)
		if err != nil || !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("include %q: %s is not a regular file", pattern, filepath.Base(m))
		}
		if real == realMain {
			sawMain = true
			continue
		}
		disp, err := filepath.Rel(dir, m)
		if err != nil {
			disp = filepath.Base(m)
		}
		out[real] = filepath.ToSlash(disp)
	}
	if candidates == 0 {
		return nil, fmt.Errorf("include %q: matches no files", pattern)
	}
	if len(out) == 0 && sawMain {
		slog.Warn("include pattern matches only the main config file, so it includes nothing", "pattern", pattern)
	}
	return out, nil
}

// isEditorTemp reports dotfiles and editor backup or swap names, unless the pattern itself names a dotfile.
func isEditorTemp(base, patternBase string) bool {
	if strings.HasPrefix(base, ".") {
		return !strings.HasPrefix(patternBase, ".")
	}
	return strings.HasSuffix(base, "~") || (strings.HasPrefix(base, "#") && strings.HasSuffix(base, "#"))
}

func mergeFragment(root *yaml.Node, path, disp string, data []byte, origin map[string]string, mainName string) error {
	if err := checkSyntax(path, data); err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return cfgErr(path, err.Error(), nil)
	}
	frag := rootMapping(&doc)
	if frag == nil {
		if doc.Kind == 0 || (doc.Kind == yaml.DocumentNode && (len(doc.Content) == 0 || doc.Content[0].Kind == yaml.ScalarNode && doc.Content[0].Tag == "!!null")) {
			return nil // empty or comment-only
		}
		return cfgErr(path, "must be a mapping of tables, storage, rpc or functions", nil)
	}
	for i := 0; i+1 < len(frag.Content); i += 2 {
		k, v := frag.Content[i], frag.Content[i+1]
		if k.Value == "include" {
			return cfgErr(path, fmt.Sprintf("line %d: nested include is not supported", k.Line), nil)
		}
		if !slices.Contains(includeSections, k.Value) {
			return cfgErr(path, fmt.Sprintf("line %d: key %q is not allowed in an included file (allowed: %s)", k.Line, k.Value, strings.Join(includeSections, ", ")), nil)
		}
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			continue
		}
		if v.Kind != yaml.MappingNode {
			return cfgErr(path, fmt.Sprintf("line %d: %s must be a mapping", k.Line, k.Value), nil)
		}
		dst, err := sectionNode(root, k.Value, mainName)
		if err != nil {
			return err
		}
		for j := 0; j+1 < len(v.Content); j += 2 {
			name := k.Value + "." + v.Content[j].Value
			if prev, dup := origin[name]; dup {
				return cfgErr(mainName, fmt.Sprintf("%s is defined in both %s and %s", name, prev, disp), nil)
			}
			origin[name] = disp
			dst.Content = append(dst.Content, v.Content[j], v.Content[j+1])
		}
	}
	return nil
}

// sectionNode returns main's mapping for a section, creating it when absent or null.
func sectionNode(root *yaml.Node, section, mainName string) (*yaml.Node, error) {
	_, v := mappingEntry(root, section)
	if v == nil {
		v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: section}, v)
		return v, nil
	}
	if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
		*v = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return v, nil
	}
	if v.Kind != yaml.MappingNode {
		return nil, cfgErr(mainName, fmt.Sprintf("%s must be a mapping to accept included entries", section), nil)
	}
	return v, nil
}

func matchesAnyPattern(patterns []string, name string) bool {
	for _, p := range patterns {
		if isEditorTemp(filepath.Base(name), filepath.Base(p)) {
			continue
		}
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}

// includeWatchTargets lists what to watch for the main file's include: patterns. It returns nothing when the file cannot be parsed.
func includeWatchTargets(mainPath string) (dirs, patterns, files []string) {
	data, err := os.ReadFile(mainPath)
	if err != nil {
		return nil, nil, nil
	}
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return nil, nil, nil
	}
	_, inc := mappingEntry(rootMapping(&doc), "include")
	if inc == nil || inc.Kind != yaml.SequenceNode {
		return nil, nil, nil
	}
	abs, err := filepath.Abs(mainPath)
	if err != nil {
		return nil, nil, nil
	}
	base := filepath.Dir(abs)
	seen := map[string]bool{}
	addDir := func(d string) {
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, n := range inc.Content {
		if n.Kind != yaml.ScalarNode || n.Value == "" || filepath.IsAbs(n.Value) || strings.Contains(n.Value, "${") {
			continue
		}
		pat := filepath.Join(base, n.Value)
		patterns = append(patterns, pat)
		for d := filepath.Dir(pat); strings.HasPrefix(d, base); d = filepath.Dir(d) {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				addDir(d)
			}
			if d == base {
				break
			}
		}
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			files = append(files, m)
			if real, err := filepath.EvalSymlinks(m); err == nil && real != m {
				files = append(files, real)
				addDir(filepath.Dir(real))
			}
		}
	}
	return dirs, patterns, files
}

// isPatternDirAncestor reports whether dir is, or contains, the directory of an include pattern.
func isPatternDirAncestor(patterns []string, dir string) bool {
	for _, p := range patterns {
		if rel, err := filepath.Rel(dir, filepath.Dir(p)); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
