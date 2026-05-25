package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Ultrabase Cloud API. Bearer is the PAT (or "" for
// unauthenticated calls like the device-flow start). HTTP is the underlying
// http.Client; tests inject one bound to httptest.Server.
type Client struct {
	BaseURL string
	Bearer  string
	HTTP    *http.Client
}

// NewClient returns a client with sane defaults.
func NewClient(baseURL, bearer string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Bearer:  bearer,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
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

// APIError is returned for non-2xx responses. Code is the body's "error" field
// if present (matches the v2 envelope), otherwise the HTTP status text.
type APIError struct {
	Status int
	Code   string
	Body   string
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		apiErr := &APIError{Status: resp.StatusCode, Body: string(respBody)}
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &env) == nil {
			apiErr.Code = env.Error
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
