package cloud

import (
	"net/http"
	"os"
	"strings"
)

// defaultCloudAPI is the Instancez Cloud API endpoint. Can be overridden by
// the INSTANCEZ_CLOUD_API env var.
const defaultCloudAPI = "https://my.instancez.ai/api"

// APIURL returns the base URL for the Instancez Cloud API.
func APIURL() string {
	if v := os.Getenv("INSTANCEZ_CLOUD_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultCloudAPI
}

// ExtraHeaders parses INSTANCEZ_CLOUD_HEADERS into headers attached to every
// cloud API request. Each newline-separated entry is "Name: Value"; blank
// entries and entries without a colon are skipped. This exists so automated
// callers (CI) can pass an edge/WAF bypass token — e.g. a header a Cloudflare
// skip rule matches on — so datacenter runners aren't bot-challenged. Returns
// nil when the env var is unset or yields no valid header.
func ExtraHeaders() http.Header {
	raw := os.Getenv("INSTANCEZ_CLOUD_HEADERS")
	if raw == "" {
		return nil
	}
	h := http.Header{}
	for _, line := range strings.Split(raw, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		h.Add(name, strings.TrimSpace(value))
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

// orgOverride is set by the global --org flag; it takes precedence over the
// INSTANCEZ_ORG env var.
var orgOverride string

// SetOrg records the org id chosen via the --org flag.
func SetOrg(id string) { orgOverride = id }

// SelectedOrg returns the org id to send in X-Instancez-Org: the --org flag,
// else INSTANCEZ_ORG. Empty means "let the server pick" — it resolves to the
// user's org when they belong to exactly one.
func SelectedOrg() string {
	if orgOverride != "" {
		return orgOverride
	}
	return os.Getenv("INSTANCEZ_ORG")
}
