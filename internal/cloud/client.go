package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Instancez Cloud API. Bearer is the PAT (or "" for
// unauthenticated calls like the device-flow start). HTTP is the underlying
// http.Client; tests inject one bound to httptest.Server.
type Client struct {
	BaseURL string
	Bearer  string
	HTTP    *http.Client
	// Headers are attached to every request (e.g. an edge/WAF bypass token from
	// INSTANCEZ_CLOUD_HEADERS), applied after Authorization and Content-Type.
	Headers http.Header
}

// NewClient returns a client with sane defaults.
func NewClient(baseURL, bearer string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Bearer:  bearer,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		Headers: ExtraHeaders(),
	}
}

// DeviceCodeResponse mirrors POST /auth/device/code in the v2 backend.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceCode starts a new device authorization flow.
func (c *Client) DeviceCode() (*DeviceCodeResponse, error) {
	var out DeviceCodeResponse
	if err := c.do("POST", "/auth/device/code", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeviceToken polls for completion of an in-flight device authorization
// grant. On success returns the raw PAT. On RFC 8628 polling errors
// (authorization_pending, slow_down, access_denied, expired_token), returns
// an *APIError with Code set — caller inspects to decide whether to keep polling.
func (c *Client) DeviceToken(deviceCode string) (string, error) {
	payload := map[string]string{"device_code": deviceCode}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do("POST", "/auth/device/token", payload, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}

// CreateProjectResponse mirrors POST /instancez/projects.
type CreateProjectResponse struct {
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
}

// CreateProject creates a new backend-only App in Instancez Cloud. Requires
// a Bearer PAT.
func (c *Client) CreateProject(name string) (*CreateProjectResponse, error) {
	var out CreateProjectResponse
	if err := c.do("POST", "/instancez/projects", map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChangeKind is the kind of structural change for a diff entry. Mirrors
// server.ChangeKind's JSON encoding exactly.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeRemoved  ChangeKind = "removed"
	ChangeModified ChangeKind = "modified"
)

// ColumnChange is an added/removed/modified column within a modified table.
type ColumnChange struct {
	Name   string     `json:"name"`
	Change ChangeKind `json:"change"`
}

// TableChange is an added/removed/modified table. Columns is populated only
// for modified tables.
type TableChange struct {
	Name    string         `json:"name"`
	Change  ChangeKind     `json:"change"`
	Columns []ColumnChange `json:"columns,omitempty"`
}

// SectionChange is a change to a non-table config section.
type SectionChange struct {
	Path   string     `json:"path"`
	Change ChangeKind `json:"change"`
}

// ConfigDiff is the page-free structural summary the cloud API returns for
// both the config preview endpoint and a branch write: tables and top-level
// config sections only. Never carries page-builder content, since inz-driven
// backend-only projects never have pages.
type ConfigDiff struct {
	Tables     []TableChange   `json:"tables"`
	Sections   []SectionChange `json:"config_sections"`
	HasChanges bool            `json:"has_changes"`
}

// uploadYAMLResponse is the shape of a successful PUT
// /instancez/projects/:id/yaml response. Dropped carries any providers
// content the server stripped before persisting. Diff is the page-free
// structural summary of what this write actually changed.
type uploadYAMLResponse struct {
	Dropped []Problem  `json:"dropped"`
	Diff    ConfigDiff `json:"diff"`
}

// wireBranch is sent on every write/preview call for backward compat with the
// server's request shape. The server resolves it to the app's one and only
// version regardless of value — apps have a single version now, no
// draft/production split — so this is never varied client-side.
const wireBranch = "production"

// UploadYAML writes yamlContent to projectID's Defs and triggers a rebuild.
// Called by `inz cloud deploy`. Returns any providers content the server
// dropped (non-blocking: providers: is local-dev-only and inert in the cloud
// runtime) and the page-free diff of what was actually written.
func (c *Client) UploadYAML(projectID, yamlContent string) ([]Problem, ConfigDiff, error) {
	var out uploadYAMLResponse
	if err := c.do("PUT", "/instancez/projects/"+projectID+"/yaml",
		map[string]string{"yaml": yamlContent, "branch": wireBranch}, &out); err != nil {
		return nil, ConfigDiff{}, err
	}
	return out.Dropped, out.Diff, nil
}

// previewResponse is the shape of a successful POST
// /instancez/projects/:id/config/preview response.
type previewResponse struct {
	Dropped []Problem  `json:"dropped"`
	Diff    ConfigDiff `json:"diff"`
}

// PreviewConfig returns the page-free diff between yamlContent and the
// project's current config, without writing anything server-side. Called by
// `inz validate --project`.
func (c *Client) PreviewConfig(projectID, yamlContent string) ([]Problem, ConfigDiff, error) {
	var out previewResponse
	if err := c.do("POST", "/instancez/projects/"+projectID+"/config/preview",
		map[string]string{"yaml": yamlContent, "branch": wireBranch}, &out); err != nil {
		return nil, ConfigDiff{}, err
	}
	return out.Dropped, out.Diff, nil
}

// UploadFunctions replaces the project's function sources with the given
// path-keyed map (keys are project-relative, e.g. "functions/hello.js"). The
// cloud builds the functions bundle from these at the next rebuild. Called by
// `inz cloud deploy`.
func (c *Client) UploadFunctions(projectID string, files map[string]string) error {
	return c.do("PUT", "/instancez/projects/"+projectID+"/functions",
		map[string]any{"files": files, "branch": wireBranch}, nil)
}

// UploadSecrets pushes name->value INSTANCEZ_ENV_ secrets to the project's
// app_secrets. The cloud encrypts and upserts them; keys absent from this call
// are left untouched. Called by `inz deploy`.
func (c *Client) UploadSecrets(projectID string, secrets map[string]string) error {
	return c.do("PUT", "/instancez/projects/"+projectID+"/secrets",
		map[string]any{"secrets": secrets, "branch": wireBranch}, nil)
}

// GetAppResponse mirrors GET /instancez/projects/:id. It carries the project
// fields plus the app's deploy state (Deployment). Note: Status is the
// project lifecycle status, distinct from Deployment.Status (the deploy state).
type GetAppResponse struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	URL            string         `json:"url"`
	Status         string         `json:"status"`
	PublishableKey string         `json:"publishable_key"`
	Deployment     DeploymentInfo `json:"deployment"`
}

// DeploymentInfo is the app's deploy state. Status is one of
// building/build_done/deploying/deploy_done/deploy_failed/not_ready. DeployedAt
// is nil until a successful deploy; Error is empty unless the deploy failed.
type DeploymentInfo struct {
	Status     string  `json:"status"`
	DeployedAt *string `json:"deployed_at"`
	Error      string  `json:"error"`
}

// GetApp fetches the cloud project's current state: project metadata and
// deploy status.
func (c *Client) GetApp(projectID string) (*GetAppResponse, error) {
	var out GetAppResponse
	if err := c.do("GET", "/instancez/projects/"+projectID, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WhoamiResponse mirrors GET /instancez/whoami.
type WhoamiResponse struct {
	Email  string `json:"email"`
	UserID string `json:"user_id"`
}

// Whoami returns the identity of the PAT holder. Useful for `inz cloud whoami`
// and as a post-login sanity check.
func (c *Client) Whoami() (*WhoamiResponse, error) {
	var out WhoamiResponse
	if err := c.do("GET", "/instancez/whoami", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ErrDeviceAccessDenied is returned when the user denies the device flow in
// the browser. Terminal — don't retry.
var ErrDeviceAccessDenied = errors.New("user denied authorization")

// ErrDeviceExpired is returned when the device flow's expires_in window
// passes without confirmation. Terminal.
var ErrDeviceExpired = errors.New("device code expired")

// pollDeviceToken polls /auth/device/token until success, denial, or timeout.
// `sleep` is parameterized for tests (use time.Sleep in production).
func pollDeviceToken(c *Client, deviceCode string, timeout, interval time.Duration, sleep func(time.Duration)) (string, error) {
	deadline := time.Now().Add(timeout)
	curInterval := interval

	for time.Now().Before(deadline) {
		token, err := c.DeviceToken(deviceCode)
		if err == nil {
			return token, nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			// Network error — back off and retry.
			sleep(curInterval)
			continue
		}
		switch apiErr.Code {
		case "authorization_pending":
			sleep(curInterval)
		case "slow_down":
			curInterval += 5 * time.Second
			sleep(curInterval)
		case "access_denied":
			return "", ErrDeviceAccessDenied
		case "expired_token":
			return "", ErrDeviceExpired
		default:
			return "", err
		}
	}
	return "", ErrDeviceExpired
}

// PollDeviceToken is the exported wrapper. Uses time.Sleep for waits.
func PollDeviceToken(c *Client, deviceCode string, timeout, interval time.Duration) (string, error) {
	return pollDeviceToken(c, deviceCode, timeout, interval, time.Sleep)
}

// Problem is one structural or cloud-policy config validation failure.
// Mirrors configvalidate.Problem: the cloud API returns the same shape
// under the "problems" field of a config-validation-failed response.
type Problem struct {
	Path       string `json:"path"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// APIError is returned for non-2xx responses. Code is the body's "error" field
// if present (matches the v2 envelope), otherwise the HTTP status text.
// Problems is populated when the body also carries a "problems" array (config
// validation failures from UploadYAML) so callers can render the per-field
// detail instead of just the summary Code.
type APIError struct {
	Status   int
	Code     string
	Body     string
	Problems []Problem
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("cloud api: %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("cloud api: %d %s", e.Status, http.StatusText(e.Status))
}

// do is the low-level request helper. payload is JSON-encoded if non-nil;
// out is JSON-decoded if non-nil and status is 2xx.
func (c *Client) do(method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	}
	for name, values := range c.Headers {
		for _, v := range values {
			req.Header.Set(name, v)
		}
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		apiErr := &APIError{Status: resp.StatusCode, Body: string(respBody)}
		var env struct {
			Error    string    `json:"error"`
			Problems []Problem `json:"problems"`
		}
		if json.Unmarshal(respBody, &env) == nil {
			apiErr.Code = env.Error
			apiErr.Problems = env.Problems
		}
		return apiErr
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
