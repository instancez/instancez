package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAPIURLDefault(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "")
	assert.Equal(t, defaultCloudAPI, APIURL())
}

func TestAPIURLFromEnv(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "https://staging.cloud.example.com")
	assert.Equal(t, "https://staging.cloud.example.com", APIURL())
}

func TestAPIURLTrimsTrailingSlash(t *testing.T) {
	t.Setenv("INSTANCEZ_CLOUD_API", "https://x.example.com/")
	assert.Equal(t, "https://x.example.com", APIURL())
}
