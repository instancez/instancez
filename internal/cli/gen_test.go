package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/typegen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const genValidYAML = "version: 1\n" + cleanYAML

const genInvalidYAML = `version: 1
tables:
  posts:
    fields:
      - {name: id, type: nonsense_type, primary_key: true}
`

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func runGen(t *testing.T, stdout io.Writer, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var errBuf bytes.Buffer
	cmd.SetOut(stdout)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(append([]string{"gen"}, args...))
	err := cmd.Execute()
	return errBuf.String(), err
}

func runGenOut(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	var out bytes.Buffer
	_, err = runGen(t, &out, args...)
	return out.String(), err
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	require.NoError(t, w.Close())
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(b)
}

func expectedTypes(t *testing.T, yaml string) string {
	t.Helper()
	cfg, err := config.ParseBytesLenient([]byte(yaml), "instancez.yaml")
	require.NoError(t, err)
	return string(typegen.Generate(cfg))
}

func TestGenTypesStdout(t *testing.T) {
	p := writeTemp(t, genValidYAML)
	out, err := runGenOut(t, "types", "--config", p)
	require.NoError(t, err)
	assert.Equal(t, expectedTypes(t, genValidYAML), out)
	assert.Contains(t, out, "export type Database")
}

func TestGenTypesOutFile(t *testing.T) {
	p := writeTemp(t, genValidYAML)
	dst := filepath.Join(t.TempDir(), "database.types.ts")
	out, err := runGenOut(t, "types", "--config", p, "--out", dst)
	require.NoError(t, err)
	assert.Empty(t, out)
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, expectedTypes(t, genValidYAML), string(got))
}

func TestGenTypesOutIgnoresEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("INSTANCEZ_OUT", "from-env.ts")
	p := writeTemp(t, genValidYAML)
	out, err := runGenOut(t, "types", "--config", p)
	require.NoError(t, err)
	assert.NotEmpty(t, out)
	assert.NoFileExists(t, filepath.Join(dir, "from-env.ts"))
}

func TestGenTypesConfigFromEnv(t *testing.T) {
	t.Setenv("INSTANCEZ_CONFIG", writeTemp(t, genValidYAML))
	out, err := runGenOut(t, "types")
	require.NoError(t, err)
	assert.Equal(t, expectedTypes(t, genValidYAML), out)
}

func TestGenTypesInvalidConfig(t *testing.T) {
	p := writeTemp(t, genInvalidYAML)
	dst := filepath.Join(t.TempDir(), "out.ts")
	var out string
	var err error
	stderr := captureStderr(t, func() {
		out, err = runGenOut(t, "types", "--config", p, "--out", dst)
	})
	assert.ErrorIs(t, err, errReported)
	assert.Empty(t, out)
	assert.Contains(t, stderr, "posts")
	assert.NoFileExists(t, dst)
}

func TestGenTypesMissingConfig(t *testing.T) {
	_, err := runGenOut(t, "types", "--config", filepath.Join(t.TempDir(), "nope.yaml"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, errReported)
}

func TestGenTypesRemoteConfigRejected(t *testing.T) {
	_, err := runGenOut(t, "types", "--config", "s3://bucket/key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "local filesystem")
}

func TestGenTypesConfigIsDirectory(t *testing.T) {
	_, err := runGenOut(t, "types", "--config", t.TempDir())
	require.Error(t, err)
}

func TestGenTypesOutIsDirectory(t *testing.T) {
	p := writeTemp(t, genValidYAML)
	_, err := runGenOut(t, "types", "--config", p, "--out", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write ")
}

func TestGenTypesStdoutWriteFails(t *testing.T) {
	p := writeTemp(t, genValidYAML)
	_, err := runGen(t, failingWriter{}, "types", "--config", p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestGenTypesBadEnvDefault(t *testing.T) {
	t.Setenv("INSTANCEZ_HELP", "bogus")
	_, err := runGenOut(t, "types", "--config", writeTemp(t, genValidYAML))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INSTANCEZ_HELP")
}

func TestGenTypesUnsetEnvRefInStringField(t *testing.T) {
	y := genValidYAML + "project:\n  name: ${INSTANCEZ_ENV_GEN_UNSET_X}\n"
	out, err := runGenOut(t, "types", "--config", writeTemp(t, y))
	require.NoError(t, err)
	assert.Contains(t, out, "export type Database")
}

func TestGenTypesUnsetEnvRefInNonStringFieldFails(t *testing.T) {
	y := genValidYAML + "server:\n  port: ${INSTANCEZ_ENV_GEN_UNSET_PORT}\n"
	var err error
	stderr := captureStderr(t, func() {
		_, err = runGenOut(t, "types", "--config", writeTemp(t, y))
	})
	assert.ErrorIs(t, err, errReported)
	assert.Contains(t, stderr, "cannot unmarshal")
}

func TestGenTypesMalformedYAML(t *testing.T) {
	_, err := runGenOut(t, "types", "--config", writeTemp(t, "tables: [\n"))
	require.Error(t, err)
	assert.NotErrorIs(t, err, errReported)
}

func TestGenAloneShowsHelp(t *testing.T) {
	out, err := runGenOut(t)
	require.NoError(t, err)
	assert.Contains(t, out, "types")
}

func TestGenTypesRejectsArgs(t *testing.T) {
	_, err := runGenOut(t, "types", "extra")
	require.Error(t, err)
}
