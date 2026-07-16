package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLogoutCmd(t *testing.T) {
	cmd := newLogoutCmd()
	assert.Equal(t, "logout", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
