// Package cloud provides the CLI-side client for Instancez Cloud (v2 backend).
package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoCredentials means no credentials file exists yet. Callers typically
// translate this into "run `inz cloud login` first".
var ErrNoCredentials = errors.New("no credentials; run `inz cloud login` first")

// Credentials are the minimal state needed to authenticate against the
// Instancez Cloud API. PAT is a Personal Access Token returned by the
// device-code flow. Email is informational (printed in `whoami`-style
// messages); never derived from the token client-side.
type Credentials struct {
	PAT   string `json:"pat"`
	Email string `json:"email,omitempty"`
}

// credentialsPath returns the absolute path to ~/.instancez/credentials.
// Honors HOME for testability.
func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".instancez", "credentials"), nil
}

// Load reads credentials from disk. Returns ErrNoCredentials if the file
// does not exist; any other error (corrupt JSON, permission denied) is
// surfaced verbatim.
func Load() (Credentials, error) {
	p, err := credentialsPath()
	if err != nil {
		return Credentials{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Credentials{}, ErrNoCredentials
		}
		return Credentials{}, fmt.Errorf("read credentials: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, fmt.Errorf("parse credentials: %w", err)
	}
	return c, nil
}

// Save writes credentials to ~/.instancez/credentials with mode 0600. Creates
// the parent directory (mode 0700) if missing. Overwrites any existing
// file atomically (write-to-temp + rename).
func Save(c Credentials) error {
	p, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("rename credentials: %w", err)
	}
	return nil
}

// Delete removes the credentials file. Missing file is not an error.
func Delete() error {
	p, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete credentials: %w", err)
	}
	return nil
}
