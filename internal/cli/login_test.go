package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLoginCmdHasUseAndShort(t *testing.T) {
	cmd := newLoginCmd()
	assert.Equal(t, "login", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
