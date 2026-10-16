package cli

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/instancez/instancez/internal/adapter/funcs"
	"github.com/instancez/instancez/internal/cloud"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/pkg/templates"
	"gopkg.in/yaml.v3"
)

const maxBundleFiles = 50

var functionFile = regexp.MustCompile(`^functions/[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9_-])?$`)

var windowsReserved = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

func checkBundlePaths(files map[string]string) error {
	if _, ok := files["instancez.yaml"]; !ok {
		return errors.New("template response has no instancez.yaml")
	}
	if len(files) > maxBundleFiles {
		return fmt.Errorf("template response has %d files, limit %d", len(files), maxBundleFiles)
	}
	seen := make(map[string]string, len(files))
	for p := range files {
		if prev, dup := seen[strings.ToLower(p)]; dup {
			return fmt.Errorf("template response contains an unsafe path %q (collides with %q)", p, prev)
		}
		seen[strings.ToLower(p)] = p
		if p == "instancez.yaml" {
			continue
		}
		base := strings.ToLower(strings.TrimPrefix(p, "functions/"))
		base, _, _ = strings.Cut(base, ".")
		if !functionFile.MatchString(p) || path.Clean(p) != p || windowsReserved.MatchString(base) {
			return fmt.Errorf("template response contains an unsafe path %q", p)
		}
	}
	return nil
}

func setProjectName(yamlText, name string) (string, error) {
	if strings.ContainsAny(name, "\r\n\x00") {
		return "", errors.New("project name must be a single line")
	}
	scalar, err := yaml.Marshal(name)
	if err != nil {
		return "", err
	}
	lines := strings.Split(yamlText, "\n")
	inProject := false
	for i, l := range lines {
		switch {
		case l == "project:":
			inProject = true
		case inProject && strings.HasPrefix(l, "  name:"):
			lines[i] = "  name: " + strings.TrimRight(string(scalar), "\n")
			out := strings.Join(lines, "\n")
			var doc struct {
				Project struct {
					Name string `yaml:"name"`
				} `yaml:"project"`
			}
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil || doc.Project.Name != name {
				return "", fmt.Errorf("could not set project name %q", name)
			}
			return out, nil
		case inProject && l != "" && !strings.HasPrefix(l, " "):
			inProject = false
		}
	}
	return "", errors.New("template has no project.name to replace")
}

func runInitTemplate(opts initOptions, dir string) error {
	id := templates.NormalizeID(opts.template)
	conflicts := existingTargets(dir, opts.force)
	if len(conflicts) > 0 {
		return fmt.Errorf("%s already exist; use --force to overwrite", strings.Join(conflicts, ", "))
	}
	bundle, err := cloud.NewClient(cloud.APIURL(), "").GetTemplate(id)
	if err != nil {
		var nf *cloud.TemplateNotFoundError
		if errors.As(err, &nf) {
			return fmt.Errorf("%w; run `inz init --list-templates`", err)
		}
		return fmt.Errorf("fetch template %q: %w", id, err)
	}
	if err := checkBundlePaths(bundle.Files); err != nil {
		return err
	}
	conflicts, err = bundleConflicts(dir, bundle, opts.force)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%s already exist; use --force to overwrite", strings.Join(conflicts, ", "))
	}

	yamlText := bundle.LocalYAML()
	if opts.name != "" {
		if yamlText, err = setProjectName(yamlText, opts.name); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := writeTemplateFiles(dir, bundle, yamlText); err != nil {
		return err
	}
	return finishTemplateInit(dir, bundle)
}

// existingTargets runs before the network call so a doomed init costs nothing.
func existingTargets(dir string, force bool) []string {
	if force {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(dir, "instancez.yaml")); err == nil {
		return []string{"instancez.yaml"}
	}
	return nil
}

// bundleConflicts uses Lstat so symlinks are never followed, even with --force.
func bundleConflicts(dir string, b *templates.Bundle, force bool) ([]string, error) {
	if fi, err := os.Lstat(filepath.Join(dir, "functions")); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("refusing to write through symlink functions")
		}
		if !fi.IsDir() {
			return nil, errors.New("functions exists and is not a directory")
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("check functions: %w", err)
	}
	var out []string
	for p := range b.Files {
		fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			return nil, fmt.Errorf("refusing to write through symlink %s", p)
		case err == nil:
			if !force {
				out = append(out, p)
			}
		case !os.IsNotExist(err):
			return nil, fmt.Errorf("check %s: %w", p, err)
		}
	}
	sort.Strings(out)
	return out, nil
}

func writeTemplateFiles(dir string, b *templates.Bundle, yamlText string) error {
	paths := make([]string, 0, len(b.Files))
	for p := range b.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		content := b.Files[p]
		if p == "instancez.yaml" {
			content = yamlText
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
		fmt.Printf("  + %s\n", p)
	}
	return nil
}

func nodeWarning(b *templates.Bundle) {
	if len(b.Manifest.Includes.Functions) == 0 || funcs.RequireNode() == nil {
		return
	}
	alertf("  ! Node.js not found on PATH. This template has code functions that need Node.js >= %d.", funcs.MinNodeMajorVersion)
}

func validateScaffold(dir string) {
	cfg, err := config.Load(filepath.Join(dir, "instancez.yaml"))
	if err == nil {
		if errs := config.Validate(cfg); errs != nil {
			err = fmt.Errorf("%v", errs)
		}
	}
	if err != nil {
		alertf("%s", scaffoldWarning(err))
	}
}

func scaffoldWarning(err error) string {
	return fmt.Sprintf("  ! The template did not validate with this inz version (%s). Upgrade inz or report it.", templates.CleanN(err.Error(), 300))
}

func finishTemplateInit(dir string, b *templates.Bundle) error {
	if err := refuseScaffoldSymlinks(dir); err != nil {
		return err
	}
	if err := applyWrite(dir, ".gitignore", func(existing string) (string, writeAction) {
		merged := mergeGitignore(existing, scaffoldGitignore())
		switch {
		case existing == "":
			return merged, actionCreate
		case merged == existing:
			return existing, actionSkip
		default:
			return merged, actionUpdate
		}
	}); err != nil {
		return err
	}
	examples := map[string]string{
		".development.env.example": scaffoldDevelopmentEnvExample(),
		".production.env.example":  scaffoldProductionEnvExample(),
	}
	for _, name := range []string{".development.env.example", ".production.env.example"} {
		base := examples[name]
		if err := applyWrite(dir, name, func(existing string) (string, writeAction) {
			if existing == "" {
				return appendSecrets(base, b.Manifest.ID, b.Manifest.Secrets), actionCreate
			}
			merged := appendSecrets(existing, b.Manifest.ID, b.Manifest.Secrets)
			if merged == existing {
				return existing, actionSkip
			}
			return merged, actionUpdate
		}); err != nil {
			return err
		}
	}
	nodeWarning(b)
	validateScaffold(dir)
	renderNextSteps(os.Stdout, b.Manifest, dir, mustCwd())
	return nil
}

func refuseScaffoldSymlinks(dir string) error {
	for _, name := range []string{".gitignore", ".development.env.example", ".production.env.example"} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("refusing to write through symlink %s", name)
		case err != nil && !os.IsNotExist(err):
			return fmt.Errorf("check %s: %w", name, err)
		}
	}
	return nil
}
