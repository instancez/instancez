package app

import (
	"sync"
	"time"
)

const (
	DriftStatusOK    = "ok"
	DriftStatusDrift = "drift"
)

// DriftState is the snapshot returned to the admin status endpoint and the
// dashboard.
type DriftState struct {
	Status           string
	ConfigSource     string
	RunningAppliedAt time.Time
	RunningChecksum  string
	SourceChecksum   string
	SourceLastSeenAt time.Time
	LastError        string
}

// DriftTracker is thread-safe.
type DriftTracker struct {
	mu     sync.RWMutex
	source string
	state  DriftState
}

func NewDriftTracker(source string) *DriftTracker {
	return &DriftTracker{
		source: source,
		state: DriftState{
			Status:       DriftStatusOK,
			ConfigSource: source,
		},
	}
}

// MarkOK records that the given config has been applied successfully and
// is now the running config. Clears any drift state.
func (t *DriftTracker) MarkOK(checksum string, appliedAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = DriftState{
		Status:           DriftStatusOK,
		ConfigSource:     t.source,
		RunningChecksum:  checksum,
		RunningAppliedAt: appliedAt,
	}
}

// MarkDrift records that the source has a new checksum that failed to apply.
// The existing running checksum/appliedAt are preserved so the snapshot
// shows what's actually live.
func (t *DriftTracker) MarkDrift(sourceChecksum, lastError string, seenAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Status = DriftStatusDrift
	t.state.SourceChecksum = sourceChecksum
	t.state.SourceLastSeenAt = seenAt
	t.state.LastError = lastError
}

// Snapshot returns a copy of the current state.
func (t *DriftTracker) Snapshot() DriftState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}
