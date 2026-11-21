package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Site is the subset of a cloud static site the CLI needs.
type Site struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
	Slug string `json:"Slug"`
	URL  string `json:"url"`
}

// ListSites returns the org's static sites.
func (c *Client) ListSites() ([]Site, error) {
	var out struct {
		Sites []Site `json:"sites"`
	}
	if err := c.do("GET", "/instancez/sites", nil, &out); err != nil {
		return nil, err
	}
	return out.Sites, nil
}

// ResolveSite finds a site by id, then by slug.
func (c *Client) ResolveSite(ref string) (*Site, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("empty site id or slug")
	}
	sites, err := c.ListSites()
	if err != nil {
		return nil, err
	}
	for i := range sites {
		if sites[i].ID == ref {
			return &sites[i], nil
		}
	}
	for i := range sites {
		if sites[i].Slug == ref {
			return &sites[i], nil
		}
	}
	return nil, fmt.Errorf("no site with id or slug %q in this organization", ref)
}

// SiteUpload is a minted capability upload URL.
type SiteUpload struct {
	UploadURL string `json:"upload_url"`
	ExpiresAt string `json:"expires_at"`
}

// SiteDeploy is the result of a capability upload.
type SiteDeploy struct {
	DeployID string `json:"deploy_id"`
	SiteID   string `json:"site_id"`
	Files    int    `json:"files"`
	URL      string `json:"url"`
}

// MintSiteUpload asks the MCP endpoint (create_site_upload) for a ~60 minute
// upload URL for the site. No REST route mints one.
func (c *Client) MintSiteUpload(siteID string) (*SiteUpload, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "create_site_upload",
			"arguments": map[string]string{"site_id": siteID},
		},
	}
	var resp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result *struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := c.do("POST", "/mcp", req, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mint upload url: %s", resp.Error.Message)
	}
	if resp.Result == nil || len(resp.Result.Content) == 0 {
		return nil, errors.New("mint upload url: empty response")
	}
	text := resp.Result.Content[0].Text
	if resp.Result.IsError {
		return nil, fmt.Errorf("mint upload url: %s", text)
	}
	var up SiteUpload
	if err := json.Unmarshal([]byte(text), &up); err != nil || up.UploadURL == "" {
		return nil, errors.New("mint upload url: response has no upload_url")
	}
	return &up, nil
}

// DefaultUploadTimeout bounds one whole archive upload.
const DefaultUploadTimeout = 30 * time.Minute

// UploadSiteArchive PUTs a tar.gz to a capability URL. The URL is the
// credential, so no bearer is sent and the token is kept out of errors.
// Redirects are errors. Any 2xx counts as success; a JSON body, when present,
// fills the result.
func (c *Client) UploadSiteArchive(uploadURL string, body io.Reader, size int64) (*SiteDeploy, error) {
	timeout := c.UploadTimeout
	if timeout <= 0 {
		timeout = DefaultUploadTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, body)
	if err != nil {
		return nil, fmt.Errorf("build upload request: %w", redactURL(err))
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/gzip")
	for name, values := range c.Headers {
		if isCredentialHeader(name) {
			continue
		}
		for _, v := range values {
			req.Header.Set(name, v)
		}
	}
	resp, err := c.uploadClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", redactURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var env struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &env) == nil && env.Error != "" {
			msg = env.Error
		}
		if msg == "" || leaksUploadURL(msg, uploadURL) {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("upload rejected (%d): %s", resp.StatusCode, msg)
	}
	var out SiteDeploy
	_ = json.Unmarshal(raw, &out)
	return &out, nil
}

func (c *Client) uploadClient() *http.Client {
	var cp http.Client
	if c.HTTP != nil {
		cp = *c.HTTP
	}
	cp.Timeout = 0
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &cp
}

func redactURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func leaksUploadURL(msg, uploadURL string) bool {
	if strings.Contains(msg, uploadURL) {
		return true
	}
	u, err := url.Parse(uploadURL)
	if err != nil {
		return false
	}
	for _, vs := range u.Query() {
		for _, v := range vs {
			if v != "" && strings.Contains(msg, v) {
				return true
			}
		}
	}
	return false
}

// isCredentialHeader reports headers that must never reach the capability URL.
func isCredentialHeader(name string) bool {
	switch http.CanonicalHeaderKey(strings.TrimSpace(name)) {
	case "Authorization", "Cookie", "Proxy-Authorization":
		return true
	}
	return false
}
