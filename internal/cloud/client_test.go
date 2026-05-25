package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientDeviceCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/auth/device/code", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dc_abc",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://x/device",
			"expires_in":       900,
			"interval":         5,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	resp, err := c.DeviceCode()
	assert.NoError(t, err)
	assert.Equal(t, "dc_abc", resp.DeviceCode)
	assert.Equal(t, "WDJB-MJHT", resp.UserCode)
	assert.Equal(t, "https://x/device", resp.VerificationURI)
	assert.Equal(t, 900, resp.ExpiresIn)
	assert.Equal(t, 5, resp.Interval)
}
