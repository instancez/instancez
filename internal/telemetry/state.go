package telemetry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
)

type state struct {
	AnonymousID string `json:"anonymous_id"`
	Disabled    bool   `json:"disabled"`
	NoticeShown bool   `json:"notice_shown"`
}

func statePath(home string) string {
	return filepath.Join(home, ".instancez", "telemetry.json")
}

// readState returns an error for a state file that exists but cannot be used.
func readState(home string) (s state, err error) {
	if home == "" {
		return state{}, nil
	}
	b, err := os.ReadFile(statePath(home))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return state{}, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}

// loadState reads the state file once; fresh is true when it held no usable id.
func loadState(home string) (s state, fresh bool, err error) {
	if s, err = readState(home); err != nil {
		s = state{}
	}
	if s.AnonymousID == "" {
		s.AnonymousID, fresh = uuid.NewString(), true
	}
	return s, fresh, err
}

// saveState writes via a temp file so a torn write never corrupts the opt-out.
func saveState(home string, s state) error {
	p := statePath(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".telemetry-*.json")
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), p)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
	}
	return werr
}

// SetDisabled persists the opt-out choice, keeping a usable anonymous id.
func SetDisabled(home string, disabled bool) error {
	s, _, _ := loadState(home)
	s.Disabled = disabled
	return saveState(home, s)
}
