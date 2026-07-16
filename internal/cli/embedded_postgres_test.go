package cli

import (
	"errors"
	"strings"
	"testing"
)

func TestEmbeddedPGHint(t *testing.T) {
	cases := []struct {
		name    string
		errMsg  string
		wantSub string // substring the hint must contain
	}{
		{
			name:    "unsupported platform: no version",
			errMsg:  "start embedded Postgres: no version found matching 16.4.0",
			wantSub: "no prebuilt binary",
		},
		{
			name:    "unsupported platform: no binary in archive",
			errMsg:  "error fetching postgres: cannot find binary in archive retrieved from https://repo",
			wantSub: "no prebuilt binary",
		},
		{
			name:    "first-run download: connect to host",
			errMsg:  "start embedded Postgres: unable to connect to repo1.maven.org",
			wantSub: "downloads Postgres binaries",
		},
		{
			name:    "first-run download: checksum mismatch",
			errMsg:  "start embedded Postgres: downloaded checksums do not match",
			wantSub: "downloads Postgres binaries",
		},
		{
			name:    "filesystem: write password file",
			errMsg:  "start embedded Postgres: unable to write password file to pgdata/runtime/pwfile",
			wantSub: "write permissions",
		},
		{
			name:    "filesystem: permission denied",
			errMsg:  "start embedded Postgres: mkdir /pgdata: permission denied",
			wantSub: "write permissions",
		},
		{
			name:    "corrupt data dir: could not start postgres",
			errMsg:  "start embedded Postgres: could not start postgres using /bin/pg_ctl:\nFATAL: database files are incompatible",
			wantSub: "--reset-pg",
		},
		{
			name:    "corrupt data dir: init database",
			errMsg:  "start embedded Postgres: unable to init database using 'initdb'",
			wantSub: "--reset-pg",
		},
		{
			name:    "stale process: port already listening",
			errMsg:  "start embedded Postgres: process already listening on port 54213",
			wantSub: "--reset-pg",
		},
		{
			name:    "post-start connect failure is treated as corrupt, not network",
			errMsg:  "start embedded Postgres: unable to connect to create database with custom name postgres",
			wantSub: "--reset-pg",
		},
		{
			name:    "unknown error falls back to generic",
			errMsg:  "start embedded Postgres: something nobody anticipated",
			wantSub: "INSTANCEZ_DATABASE_URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := embeddedPGHint(errors.New(tc.errMsg))
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("embeddedPGHint(%q)\n  = %q\n  want substring %q", tc.errMsg, got, tc.wantSub)
			}
		})
	}
}
