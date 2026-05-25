package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDeployCmd(t *testing.T) {
	cmd := newDeployCmd()
	assert.Equal(t, "deploy", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
