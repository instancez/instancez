//go:build integration

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	scaffoldSecretKey = "inz_secret_scaffoldtest"
	scaffoldPubKey    = "inz_publishable_scaffoldtest"
)

type scaffoldAPI struct {
	t    *testing.T
	base string
}

func (a scaffoldAPI) do(method, path, token string, body []byte, hdr map[string]string) (int, []byte) {
	a.t.Helper()
	req, err := http.NewRequest(method, a.base+path, bytes.NewReader(body))
	require.NoError(a.t, err)
	req.Header.Set("apikey", scaffoldPubKey)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (a scaffoldAPI) signup(email string) (token, uid string) {
	a.t.Helper()
	status, out := a.do("POST", "/auth/v1/signup", "", []byte(fmt.Sprintf(`{"email":%q,"password":"Passw0rd!123"}`, email)), map[string]string{"Content-Type": "application/json"})
	require.Equal(a.t, 200, status, string(out))
	var r struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	require.NoError(a.t, json.Unmarshal(out, &r), string(out))
	require.NotEmpty(a.t, r.AccessToken, string(out))
	return r.AccessToken, r.User.ID
}

func (a scaffoldAPI) upload(token, path string, upsert bool) int {
	a.t.Helper()
	hdr := map[string]string{"Content-Type": "image/png"}
	if upsert {
		hdr["x-upsert"] = "true"
	}
	status, _ := a.do("POST", "/storage/v1/object/avatars/"+path, token, []byte("\x89PNG\r\n\x1a\n"), hdr)
	return status
}

func TestScaffoldBootsAndEnforces(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not on PATH")
	}
	bin := filepath.Join(t.TempDir(), "inz")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/inz").CombinedOutput(); err != nil {
		t.Fatalf("build inz: %v\n%s", err, out)
	}
	proj := t.TempDir()
	require.NoError(t, runInit(initOptions{dir: proj, name: "scaffold"}))

	port := reloadTestPort(t)
	var logs lockedBuffer
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command, go_subproc_rule-subproc
	cmd := exec.Command(bin, "dev", "--embedded-pg", "--port", fmt.Sprint(port))
	cmd.Dir = proj
	cmd.Env = append(os.Environ(), "INSTANCEZ_TELEMETRY_DISABLED=1", "INSTANCEZ_SECRET_KEY="+scaffoldSecretKey, "INSTANCEZ_PUBLISHABLE_KEY="+scaffoldPubKey)
	cmd.Stdout, cmd.Stderr = &logs, &logs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		_ = cmd.Wait()
	})

	api := scaffoldAPI{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if resp, err := http.Get(api.base + "/auth/v1/health"); err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			if isEmbeddedPGEnvLimitation(fmt.Errorf("%s", logs.String())) {
				t.Skip("embedded Postgres unavailable in this environment")
			}
			t.Fatalf("server did not start:\n%s", logs.String())
		}
		time.Sleep(500 * time.Millisecond)
	}

	tokA, uidA := api.signup("a@example.com")
	tokB, uidB := api.signup("b@example.com")
	jsonHdr := map[string]string{"Content-Type": "application/json"}

	t.Run("function validates with zod under the runtime", func(t *testing.T) {
		var status int
		var out []byte
		for end := time.Now().Add(2 * time.Minute); time.Now().Before(end); time.Sleep(time.Second) {
			status, out = api.do("POST", "/functions/v1/todos", tokA, []byte(`{"title":""}`), jsonHdr)
			if status == 400 {
				break
			}
		}
		require.Equal(t, 400, status, string(out)+"\n"+logs.String())
		var r struct {
			Issues []any `json:"issues"`
		}
		require.NoError(t, json.Unmarshal(out, &r))
		assert.NotEmpty(t, r.Issues)

		status, out = api.do("POST", "/functions/v1/todos", tokA, []byte(`{"title":"t"}`), jsonHdr)
		assert.Equal(t, 201, status, string(out))
	})

	t.Run("avatars are owner scoped", func(t *testing.T) {
		assert.Equal(t, 200, api.upload(tokA, uidA+"/a.png", false))
		assert.GreaterOrEqual(t, api.upload(tokA, uidB+"/a.png", false), 400)
		assert.GreaterOrEqual(t, api.upload(tokB, uidA+"/a.png", true), 400)

		status, _ := api.do("DELETE", "/storage/v1/object/avatars/"+uidA+"/a.png", tokB, nil, nil)
		assert.NotEqual(t, 200, status)
		status, _ = api.do("GET", "/storage/v1/object/public/avatars/"+uidA+"/a.png", "", nil, nil)
		assert.Equal(t, 200, status, strings.TrimSpace(logs.String()))
	})
}
