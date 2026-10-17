// Package templates is the catalog of ready-made backends a user can start a
// project from. Each template is a directory under catalog/ holding a
// template.yaml manifest, an instancez.yaml and optional functions/*.js. See
// docs/templates.md in this repo for the authoring contract.
package templates

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"gopkg.in/yaml.v3"
)

//go:embed all:catalog
var embedded embed.FS

var Categories = []string{"commerce", "content", "booking", "productivity", "business", "community", "education", "tools"}

var idPattern = regexp.MustCompile(`^[a-z]+(-[a-z]+){0,2}$`)

const maxIDLen = 30

// ValidID reports whether s is a well-formed template id (1-3 lowercase hyphenated words).
func ValidID(s string) bool { return len(s) <= maxIDLen && idPattern.MatchString(s) }

var (
	secretPattern = regexp.MustCompile(`^INSTANCEZ_ENV_[A-Z0-9_]+$`)
	// envRef mirrors the engine's ${NAME} / ${NAME:-default} pattern; group 2 is set when a default is given.
	envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)
)

// Secret is a value the user supplies (Stripe key, SMTP token...) for a feature to work.
type Secret struct {
	Name        string `yaml:"name" json:"name"`
	Label       string `yaml:"label" json:"label"`
	Description string `yaml:"description" json:"description"`
	NeededFor   string `yaml:"needed_for" json:"needed_for"`
	Required    bool   `yaml:"required" json:"required"`
	URL         string `yaml:"url" json:"url,omitempty"`
}

// Includes is derived from instancez.yaml so the UI summary can never drift from the config.
type Includes struct {
	Tables    []string `json:"tables"`
	Functions []string `json:"functions"`
	Buckets   []string `json:"buckets"`
}

type Manifest struct {
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Tagline     string   `yaml:"tagline" json:"tagline"`
	Description string   `yaml:"description" json:"description"`
	Category    string   `yaml:"category" json:"category"`
	Tags        []string `yaml:"tags" json:"tags"`
	Secrets     []Secret `yaml:"secrets" json:"secrets"`
	Setup       []string `yaml:"setup" json:"setup"`
	Includes    Includes `yaml:"-" json:"includes"`
}

type Template struct {
	Manifest
	YAML  string            `json:"-"`
	Files map[string]string `json:"-"` // functions/<name>.js -> source
}

type Catalog struct {
	list []Template
	byID map[string]*Template
}

func (c *Catalog) List() []Template { return c.list }

func (c *Catalog) Get(id string) (*Template, bool) {
	t, ok := c.byID[id]
	return t, ok
}

var (
	defaultOnce sync.Once
	defaultCat  *Catalog
)

// Default returns the embedded catalog. A broken catalog is a CI failure
// (TestCatalogContract); at runtime it degrades to an empty list.
func Default() *Catalog {
	defaultOnce.Do(func() {
		c, err := Load(embedded, "catalog")
		if err != nil {
			log.Printf("templates: catalog invalid, serving none: %v", err)
			c = &Catalog{byID: map[string]*Template{}}
		}
		defaultCat = c
	})
	return defaultCat
}

// Load reads and structurally validates every template under root.
func Load(fsys fs.FS, root string) (*Catalog, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}
	c := &Catalog{byID: map[string]*Template{}}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := loadOne(fsys, path.Join(root, e.Name()), e.Name())
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", e.Name(), err)
		}
		c.list = append(c.list, *t)
	}
	sort.Slice(c.list, func(i, j int) bool { return c.list[i].Name < c.list[j].Name })
	for i := range c.list {
		c.byID[c.list[i].ID] = &c.list[i]
	}
	return c, nil
}

func loadOne(fsys fs.FS, dir, dirName string) (*Template, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, "template.yaml"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("template.yaml: %w", err)
	}
	cfg, err := fs.ReadFile(fsys, path.Join(dir, "instancez.yaml"))
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	if fnEntries, err := fs.ReadDir(fsys, path.Join(dir, "functions")); err == nil {
		for _, fe := range fnEntries {
			b, err := fs.ReadFile(fsys, path.Join(dir, "functions", fe.Name()))
			if err != nil {
				return nil, err
			}
			files["functions/"+fe.Name()] = string(b)
		}
	}
	// Non-nil so the JSON API renders [] rather than null.
	m.Tags, m.Secrets, m.Setup = nonNil(m.Tags), nonNil(m.Secrets), nonNil(m.Setup)
	t := &Template{Manifest: m, YAML: string(cfg), Files: files}
	if err := t.check(dirName); err != nil {
		return nil, err
	}
	inc, err := deriveIncludes(t.YAML)
	if err != nil {
		return nil, err
	}
	t.Includes = inc
	return t, nil
}

func ValidSecretName(name string) bool { return secretPattern.MatchString(name) }

func unsafeRune(r rune) bool {
	return !unicode.IsSpace(r) && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r))
}

// CleanN is Clean capped at n runes, with an ellipsis when cut.
func CleanN(s string, n int) string {
	r := []rune(Clean(s))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// Clean strips control and format characters and collapses whitespace.
func Clean(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unsafeRune(r) {
			return -1
		}
		return r
	}, s)), " ")
}

func (t *Template) check(dirName string) error {
	switch {
	case !ValidID(t.ID):
		return fmt.Errorf("id %q must be 1-3 lowercase hyphenated words, max %d chars", t.ID, maxIDLen)
	case t.ID != dirName:
		return fmt.Errorf("id %q must equal directory name %q", t.ID, dirName)
	case t.Name == "" || t.Description == "":
		return fmt.Errorf("name and description are required")
	case t.Tagline == "" || len(t.Tagline) > 90:
		return fmt.Errorf("tagline is required and at most 90 chars")
	case !slices.Contains(Categories, t.Category):
		return fmt.Errorf("category %q must be one of %v", t.Category, Categories)
	}
	declared := map[string]bool{}
	for _, s := range t.Secrets {
		switch {
		case !secretPattern.MatchString(s.Name):
			return fmt.Errorf("secret %q must match %s", s.Name, secretPattern)
		case declared[s.Name]:
			return fmt.Errorf("secret %q declared twice", s.Name)
		case Clean(s.Label) != s.Label || Clean(s.NeededFor) != s.NeededFor || Clean(s.URL) != s.URL:
			return fmt.Errorf("secret %s has control characters or odd spacing in label, needed_for or url", s.Name)
		case s.Label == "" || s.Description == "" || s.NeededFor == "":
			return fmt.Errorf("secret %s needs label, description and needed_for", s.Name)
		}
		declared[s.Name] = true
	}
	referenced := map[string]bool{}
	for _, m := range envRef.FindAllStringSubmatch(t.YAML, -1) {
		name, hasDefault := m[1], m[2] != ""
		if !hasDefault {
			return fmt.Errorf("${%s} needs a :- default so the app boots without the secret", name)
		}
		if !declared[name] {
			return fmt.Errorf("${%s} is referenced but not declared in template.yaml secrets", name)
		}
		referenced[name] = true
	}
	for name := range declared {
		if !referenced[name] {
			return fmt.Errorf("secret %s is declared but never referenced in instancez.yaml", name)
		}
	}
	return t.checkFunctionFiles()
}

// packageJSON is the one non-handler file a template may ship. The runtime only
// vendors @supabase/supabase-js (behind ctx.supabase / ctx.serviceClient) when it is listed here.
const packageJSON = "functions/package.json"

func (t *Template) checkPackageJSON() error {
	usesClient := false
	for p, src := range t.Files {
		if p != packageJSON && (strings.Contains(src, "ctx.supabase") || strings.Contains(src, "ctx.serviceClient")) {
			usesClient = true
		}
	}
	raw, has := t.Files[packageJSON]
	if !has {
		if usesClient {
			return fmt.Errorf("functions use ctx.supabase/ctx.serviceClient so %s must list @supabase/supabase-js", packageJSON)
		}
		return nil
	}
	var pkg struct {
		Type         string            `json:"type"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		return fmt.Errorf("%s: %w", packageJSON, err)
	}
	if pkg.Type != "module" {
		return fmt.Errorf(`%s must set "type": "module" (handlers are ESM)`, packageJSON)
	}
	for name := range pkg.Dependencies {
		if name != "@supabase/supabase-js" {
			return fmt.Errorf("%s: only @supabase/supabase-js is allowed, found %s", packageJSON, name)
		}
	}
	if usesClient && pkg.Dependencies["@supabase/supabase-js"] == "" {
		return fmt.Errorf("%s must list @supabase/supabase-js", packageJSON)
	}
	if !usesClient {
		return fmt.Errorf("%s is shipped but no function uses ctx.supabase/ctx.serviceClient", packageJSON)
	}
	return nil
}

func (t *Template) checkFunctionFiles() error {
	if err := t.checkPackageJSON(); err != nil {
		return err
	}
	var doc struct {
		Functions map[string]struct {
			File string `yaml:"file"`
		} `yaml:"functions"`
	}
	if err := yaml.Unmarshal([]byte(t.YAML), &doc); err != nil {
		return fmt.Errorf("instancez.yaml: %w", err)
	}
	used := map[string]bool{}
	for name, f := range doc.Functions {
		if _, ok := t.Files[f.File]; !ok {
			return fmt.Errorf("function %s: file %q missing from template", name, f.File)
		}
		used[f.File] = true
	}
	for p := range t.Files {
		if p == packageJSON {
			continue
		}
		if !used[p] {
			return fmt.Errorf("%s is not referenced by any function", p)
		}
	}
	return nil
}

func deriveIncludes(cfg string) (Includes, error) {
	var doc struct {
		Tables    map[string]any `yaml:"tables"`
		Functions map[string]any `yaml:"functions"`
		Storage   map[string]any `yaml:"storage"`
	}
	if err := yaml.Unmarshal([]byte(cfg), &doc); err != nil {
		return Includes{}, fmt.Errorf("instancez.yaml: %w", err)
	}
	return Includes{Tables: keys(doc.Tables), Functions: keys(doc.Functions), Buckets: keys(doc.Storage)}, nil
}

// keys is never nil so the JSON API renders [] rather than null.
func keys(m map[string]any) []string { return nonNil(slices.Sorted(maps.Keys(m))) }

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
