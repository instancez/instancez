package cli

import (
	"bytes"
	"testing"

	"github.com/instancez/instancez/internal/cloud"
	"github.com/stretchr/testify/assert"
)

func TestRenderStatusDirtyWithDeploy(t *testing.T) {
	deployedAt := "2026-06-01T12:00:00Z"
	app := &cloud.GetAppResponse{
		ID:     "app-uuid",
		Name:   "My App",
		URL:    "https://my-app.instancez.app",
		Status: "DEPLOYED",
		Deployment: cloud.DeploymentInfo{
			Status:     "deploy_done",
			DeployedAt: &deployedAt,
			Error:      "",
		},
		DraftDirty: true,
	}

	var buf bytes.Buffer
	renderStatus(&buf, app)
	out := buf.String()

	assert.Contains(t, out, "My App")
	assert.Contains(t, out, "app-uuid")
	assert.Contains(t, out, "https://my-app.instancez.app")
	assert.Contains(t, out, "deploy_done")
	assert.Contains(t, out, deployedAt)
	assert.Contains(t, out, "has unpublished changes")
}

func TestRenderStatusCleanNoDeploy(t *testing.T) {
	app := &cloud.GetAppResponse{
		ID:     "app-uuid",
		Name:   "My App",
		Status: "DRAFT",
		Deployment: cloud.DeploymentInfo{
			Status:     "not_ready",
			DeployedAt: nil,
			Error:      "",
		},
		DraftDirty: false,
	}

	var buf bytes.Buffer
	renderStatus(&buf, app)
	out := buf.String()

	assert.Contains(t, out, "not_ready")
	assert.Contains(t, out, "Draft:")
	assert.Contains(t, out, "clean")
	assert.NotContains(t, out, "has unpublished changes")
	// No deploy timestamp / error lines when absent.
	assert.NotContains(t, out, "Deployed:")
	assert.NotContains(t, out, "Error:")
}

func TestRenderStatusShowsDeployError(t *testing.T) {
	app := &cloud.GetAppResponse{
		ID:   "app-uuid",
		Name: "My App",
		Deployment: cloud.DeploymentInfo{
			Status: "deploy_failed",
			Error:  "lambda init crashed",
		},
		DraftDirty: true,
	}

	var buf bytes.Buffer
	renderStatus(&buf, app)
	out := buf.String()

	assert.Contains(t, out, "deploy_failed")
	assert.Contains(t, out, "Error:")
	assert.Contains(t, out, "lambda init crashed")
}
