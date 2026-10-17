package cli

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
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
	conflicts, err := checkTargets(dir, []string{"instancez.yaml"}, opts.force)
	if err != nil {
		return err
	}
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
	paths := append(slices.Sorted(maps.Keys(bundle.Files)), "functions")
	if conflicts, err = checkTargets(dir, paths, opts.force); err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%s already exist; use --force to overwrite", strings.Join(conflicts, ", "))
	}
	if _, err = checkTargets(dir, scaffoldFiles, true); err != nil {
		return err
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

var scaffoldFiles = []string{".gitignore", ".development.env.example", ".production.env.example"}

// checkTargets Lstats each path so symlinks are never followed, even with force.
func checkTargets(dir string, paths []string, force bool) ([]string, error) {
	var conflicts []string
	for _, p := range paths {
		fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return nil, fmt.Errorf("check %s: %w", p, err)
		case fi.Mode()&os.ModeSymlink != 0:
			return nil, fmt.Errorf("refusing to write through symlink %s", p)
		case p == "functions" && !fi.IsDir():
			return nil, errors.New("functions exists and is not a directory")
		case p != "functions" && !force:
			conflicts = append(conflicts, p)
		}
	}
	sort.Strings(conflicts)
	return conflicts, nil
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
	if err := writeGitignore(dir); err != nil {
		return err
	}
	writeExample := func(name, base string) error {
		return applyWrite(dir, name, func(existing string) (string, writeAction) {
			merged := appendSecrets(cmp.Or(existing, base), b.Manifest.ID, b.Manifest.Secrets)
			return merged, diffAction(existing, merged)
		})
	}
	if err := writeExample(".development.env.example", scaffoldDevelopmentEnvExample()); err != nil {
		return err
	}
	if err := writeExample(".production.env.example", scaffoldProductionEnvExample()); err != nil {
		return err
	}
	nodeWarning(b)
	validateScaffold(dir)
	renderNextSteps(os.Stdout, b.Manifest, dir, mustCwd())
	return nil
}
