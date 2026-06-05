package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLoginCmdHasUseAndShort(t *testing.T) {
	cmd := newLoginCmd()
	assert.Equal(t, "login", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}

// TestEnsureLoggedInValidCreds: existing creds with a non-empty PAT short
// circuit — no prompt, no flow. Creds are isolated via HOME (like every other
// test in this package).
func TestEnsureLoggedInValidCreds(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, cloud.Save(cloud.Credentials{PAT: "tok-123", Email: "a@b.c"}))

	flowCalled := false
	confirmCalled := false
	got, err := ensureLoggedIn(ensureLoginOpts{
		isTTY:   func() bool { return true },
		confirm: func(string) bool { confirmCalled = true; return true },
		runFlow: func() (cloud.Credentials, error) {
			flowCalled = true
			return cloud.Credentials{PAT: "new"}, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "tok-123", got.PAT)
	assert.False(t, flowCalled, "runFlow must not be called when creds already valid")
	assert.False(t, confirmCalled, "confirm must not be called when creds already valid")
}

// TestEnsureLoggedInNonTTY: no creds + not a TTY → hard error pointing at
// `ultra login`; the flow is never invoked.
func TestEnsureLoggedInNonTTY(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no creds

	flowCalled := false
	_, err := ensureLoggedIn(ensureLoginOpts{
		isTTY: func() bool { return false },
		runFlow: func() (cloud.Credentials, error) {
			flowCalled = true
			return cloud.Credentials{}, nil
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ultra login")
	assert.False(t, flowCalled, "runFlow must not be called in a non-TTY session")
}

// TestEnsureLoggedInNonTTYAssumeYes: --yes does NOT bypass the non-TTY guard —
// you still can't drive a browser in a non-interactive session.
func TestEnsureLoggedInNonTTYAssumeYes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	flowCalled := false
	_, err := ensureLoggedIn(ensureLoginOpts{
		assumeYes: true,
		isTTY:     func() bool { return false },
		runFlow: func() (cloud.Credentials, error) {
			flowCalled = true
			return cloud.Credentials{}, nil
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ultra login")
	assert.False(t, flowCalled, "assumeYes must not bypass the non-TTY guard")
}

// TestEnsureLoggedInTTYConfirmYes: TTY + user confirms → flow runs and its
// creds are returned.
func TestEnsureLoggedInTTYConfirmYes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	flowCalled := false
	got, err := ensureLoggedIn(ensureLoginOpts{
		isTTY:   func() bool { return true },
		confirm: func(string) bool { return true },
		runFlow: func() (cloud.Credentials, error) {
			flowCalled = true
			return cloud.Credentials{PAT: "fresh-tok"}, nil
		},
	})
	require.NoError(t, err)
	assert.True(t, flowCalled, "runFlow must be called after a yes confirm")
	assert.Equal(t, "fresh-tok", got.PAT)
}

// TestEnsureLoggedInTTYConfirmNo: TTY + user declines (not assumeYes) → error,
// flow not invoked.
func TestEnsureLoggedInTTYConfirmNo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	flowCalled := false
	_, err := ensureLoggedIn(ensureLoginOpts{
		isTTY:   func() bool { return true },
		confirm: func(string) bool { return false },
		runFlow: func() (cloud.Credentials, error) {
			flowCalled = true
			return cloud.Credentials{}, nil
		},
	})
	require.Error(t, err)
	assert.False(t, flowCalled, "runFlow must not be called when the user declines")
}

// TestEnsureLoggedInTTYAssumeYesSkipsPrompt: assumeYes runs the flow without
// ever calling confirm.
func TestEnsureLoggedInTTYAssumeYesSkipsPrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	confirmCalled := false
	flowCalled := false
	got, err := ensureLoggedIn(ensureLoginOpts{
		assumeYes: true,
		isTTY:     func() bool { return true },
		confirm:   func(string) bool { confirmCalled = true; return false },
		runFlow: func() (cloud.Credentials, error) {
			flowCalled = true
			return cloud.Credentials{PAT: "auto-tok"}, nil
		},
	})
	require.NoError(t, err)
	assert.False(t, confirmCalled, "assumeYes must skip the confirm prompt")
	assert.True(t, flowCalled, "assumeYes must still run the flow")
	assert.Equal(t, "auto-tok", got.PAT)
}

// TestEnsureLoggedInFlowError: a flow failure propagates out unchanged.
func TestEnsureLoggedInFlowError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	flowErr := errors.New("device flow blew up")
	_, err := ensureLoggedIn(ensureLoginOpts{
		assumeYes: true,
		isTTY:     func() bool { return true },
		runFlow:   func() (cloud.Credentials, error) { return cloud.Credentials{}, flowErr },
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "device flow blew up"))
}
