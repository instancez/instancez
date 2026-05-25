package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewWhoamiCmd(t *testing.T) {
	cmd := newWhoamiCmd()
	assert.Equal(t, "whoami", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
