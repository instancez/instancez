package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/instancez/instancez/pkg/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var demoSecrets = []templates.Secret{
	{Name: "INSTANCEZ_ENV_OPT", Label: "Optional key", NeededFor: "extras", Required: false},
	{Name: "INSTANCEZ_ENV_REQ", Label: "Stripe key", NeededFor: "checkout", Required: true, URL: "https://x.test/keys"},
}

func TestAppendSecretsRequiredFirstAndBlank(t *testing.T) {
	out := appendSecrets("# base\n", "course-platform", demoSecrets)
	assert.True(t, strings.HasPrefix(out, "# base\n"))
	req, opt := strings.Index(out, "INSTANCEZ_ENV_REQ="), strings.Index(out, "INSTANCEZ_ENV_OPT=")
	require.True(t, req > 0 && opt > 0)
	assert.Less(t, req, opt, "required secrets come first")
	assert.Contains(t, out, "# Stripe key: checkout")
	assert.Contains(t, out, "INSTANCEZ_ENV_REQ=\n")
}

func TestAppendSecretsIsIdempotentAndKeepsUserValues(t *testing.T) {
	first := appendSecrets("", "x", demoSecrets)
	second := appendSecrets(first, "x", demoSecrets)
	assert.Equal(t, first, second)

	edited := strings.Replace(first, "INSTANCEZ_ENV_REQ=\n", "INSTANCEZ_ENV_REQ=sk_live_123\n", 1)
	again := appendSecrets(edited, "x", demoSecrets)
	assert.Equal(t, 1, strings.Count(again, "INSTANCEZ_ENV_REQ="))
	assert.Contains(t, again, "INSTANCEZ_ENV_REQ=sk_live_123")
}

func TestAppendSecretsAddsOnlyMissingKeys(t *testing.T) {
	existing := "INSTANCEZ_ENV_OPT=keep\n"
	out := appendSecrets(existing, "x", demoSecrets)
	assert.Equal(t, 1, strings.Count(out, "INSTANCEZ_ENV_OPT="))
	assert.Contains(t, out, "INSTANCEZ_ENV_REQ=")
}

func TestAppendSecretsNoSecretsLeavesFileUntouched(t *testing.T) {
	assert.Equal(t, "# base\n", appendSecrets("# base\n", "x", nil))
	assert.Equal(t, "", appendSecrets("", "x", []templates.Secret{}))
}

func TestAppendSecretsNoTrailingNewlineInExisting(t *testing.T) {
	out := appendSecrets("A=1", "x", demoSecrets)
	assert.Contains(t, out, "A=1\n")
	assert.NotContains(t, out, "A=1#")
}

func TestNextStepsListsSecretsByEnvFile(t *testing.T) {
	var buf bytes.Buffer
	m := templates.Manifest{ID: "course-platform", Secrets: demoSecrets, Setup: []string{"Add the webhook in Stripe"}}
	renderNextSteps(&buf, m, "/work/proj", "/work")
	out := buf.String()
	assert.Contains(t, out, `Secrets for "course-platform"`)
	assert.Contains(t, out, "REQUIRED  INSTANCEZ_ENV_REQ")
	assert.Contains(t, out, "https://x.test/keys")
	assert.Contains(t, out, "optional  INSTANCEZ_ENV_OPT")
	assert.Contains(t, out, ".development.env")
	assert.Contains(t, out, "inz dev")
	assert.Contains(t, out, ".production.env")
	assert.Contains(t, out, "inz serve")
	assert.Contains(t, out, "503")
	assert.Contains(t, out, "1. Add the webhook in Stripe")
	assert.Contains(t, out, "cd proj")
	assert.Less(t, strings.Index(out, "REQUIRED"), strings.Index(out, "optional"))
}

func TestNextStepsWithoutSecretsOrSetup(t *testing.T) {
	var buf bytes.Buffer
	renderNextSteps(&buf, templates.Manifest{ID: "blog"}, "/work", "/work")
	out := buf.String()
	assert.NotContains(t, out, "Secrets for")
	assert.NotContains(t, out, "Manual steps")
	assert.NotContains(t, out, "cd ")
	assert.Contains(t, out, "inz dev")
}

func TestInitTemplateWritesSecretsIntoBothExamples(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: dir, template: "course-platform", templateSet: true}))
	tpl, _ := templates.Default().Get("course-platform")
	require.NotEmpty(t, tpl.Secrets)
	for _, f := range []string{".development.env.example", ".production.env.example"} {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		require.NoError(t, err)
		for _, s := range tpl.Secrets {
			assert.Equal(t, 1, strings.Count(string(raw), s.Name+"="), "%s in %s", s.Name, f)
		}
	}
}

func TestInitTemplateTwiceWithForceDoesNotDuplicateSecrets(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	o := initOptions{dir: dir, template: "course-platform", templateSet: true}
	require.NoError(t, runInit(o))
	o.force = true
	require.NoError(t, runInit(o))
	raw, _ := os.ReadFile(filepath.Join(dir, ".development.env.example"))
	tpl, _ := templates.Default().Get("course-platform")
	for _, s := range tpl.Secrets {
		assert.Equal(t, 1, strings.Count(string(raw), s.Name+"="))
	}
}

func TestInitTemplateWithoutSecretsLeavesExamplesPlain(t *testing.T) {
	fakeCloud(t)
	var plain string
	for _, tpl := range templates.Default().List() {
		if len(tpl.Secrets) == 0 {
			plain = tpl.ID
			break
		}
	}
	if plain == "" {
		t.Skip("every template declares secrets")
	}
	dir := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: dir, template: plain, templateSet: true}))
	raw, _ := os.ReadFile(filepath.Join(dir, ".development.env.example"))
	assert.NotContains(t, string(raw), "INSTANCEZ_ENV_")
}

func TestAppendSecretsPrefixNameIsNotTreatedAsPresent(t *testing.T) {
	secrets := []templates.Secret{{Name: "INSTANCEZ_ENV_STRIPE", Label: "S", NeededFor: "n", Required: true}}
	out := appendSecrets("INSTANCEZ_ENV_STRIPE_KEY=abc\n", "x", secrets)
	assert.Contains(t, out, "\nINSTANCEZ_ENV_STRIPE=\n")
	assert.Contains(t, out, "INSTANCEZ_ENV_STRIPE_KEY=abc")
}

func TestAppendSecretsRespectsExportedExistingKey(t *testing.T) {
	existing := "export INSTANCEZ_ENV_REQ=sk_1\n"
	out := appendSecrets(existing, "x", demoSecrets)
	assert.Equal(t, 1, strings.Count(out, "INSTANCEZ_ENV_REQ"))
	assert.Contains(t, out, "export INSTANCEZ_ENV_REQ=sk_1")
	assert.Contains(t, out, "INSTANCEZ_ENV_OPT=")
}

func TestInitTemplateRefusesSymlinkedScaffoldFiles(t *testing.T) {
	for _, name := range []string{".gitignore", ".development.env.example", ".production.env.example"} {
		t.Run(name, func(t *testing.T) {
			fakeCloud(t)
			dir := t.TempDir()
			outside := filepath.Join(t.TempDir(), "victim")
			require.NoError(t, os.WriteFile(outside, []byte("keep\n"), 0o644))
			require.NoError(t, os.Symlink(outside, filepath.Join(dir, name)))
			err := runInit(initOptions{dir: dir, template: "course-platform", templateSet: true})
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
			raw, _ := os.ReadFile(outside)
			assert.Equal(t, "keep\n", string(raw))
			for _, other := range []string{".gitignore", ".development.env.example", ".production.env.example"} {
				if other != name {
					_, statErr := os.Lstat(filepath.Join(dir, other))
					assert.True(t, os.IsNotExist(statErr), "%s must not be written", other)
				}
			}
		})
	}
}

func TestInitTemplateRefusesDanglingSymlinkedExample(t *testing.T) {
	fakeCloud(t)
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "missing")
	require.NoError(t, os.Symlink(target, filepath.Join(dir, ".development.env.example")))
	err := runInit(initOptions{dir: dir, template: "course-platform", templateSet: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".development.env.example")
	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "dangling target must not be created")
}

func TestAppendSecretsSanitizesHostileFields(t *testing.T) {
	hostile := []templates.Secret{{Name: "INSTANCEZ_ENV_OK", Label: "x\nINSTANCEZ_ENV_EVIL=1", NeededFor: "\x1b[31mred\u009b", Required: true}}
	out := appendSecrets("", "x", hostile)
	for _, line := range strings.Split(out, "\n") {
		assert.False(t, strings.HasPrefix(line, "INSTANCEZ_ENV_EVIL"), line)
	}
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\u009b")
	assert.Contains(t, out, "INSTANCEZ_ENV_OK=\n")
}

func TestAppendSecretsSkipsInvalidNames(t *testing.T) {
	bad := []templates.Secret{
		{Name: "evil\nINSTANCEZ_ENV_X", Label: "l", NeededFor: "n"},
		{Name: "instancez_env_lower", Label: "l", NeededFor: "n"},
		{Name: "", Label: "l", NeededFor: "n"},
	}
	assert.Equal(t, "# base\n", appendSecrets("# base\n", "x", bad))
}

func TestNextStepsSanitizesHostileManifest(t *testing.T) {
	var buf bytes.Buffer
	m := templates.Manifest{
		ID: "id\x1b[2J",
		Secrets: []templates.Secret{
			{Name: "INSTANCEZ_ENV_A", NeededFor: "n\nFAKE LINE", URL: "https://x\x1b]0;t\x07", Required: true},
			{Name: "bad\nname", NeededFor: "n"},
		},
		Setup: []string{"step\none\x1b[31m", "two"},
	}
	renderNextSteps(&buf, m, "/work", "/work")
	out := buf.String()
	assert.NotContains(t, out, "\x1b")
	assert.NotContains(t, out, "\x07")
	assert.NotContains(t, out, "bad")
	assert.NotContains(t, out, "\nFAKE LINE")
	assert.Contains(t, out, "\n  1. step one[31m\n")
	assert.Contains(t, out, "\n  2. two\n")
	assert.Equal(t, 1, strings.Count(out, "INSTANCEZ_ENV_A"))
}

func TestRelOr(t *testing.T) {
	assert.Equal(t, "proj", relOr("/work", "/work/proj"))
	assert.Equal(t, "/other/proj", relOr("/work", "/other/proj"))
}
