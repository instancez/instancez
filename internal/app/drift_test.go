package app

import (
	"testing"
	"time"
)

func TestDriftStateMarkOK(t *testing.T) {
	d := NewDriftTracker("file://x")
	d.MarkOK("checksum-1", time.Now())
	got := d.Snapshot()
	if got.Status != DriftStatusOK {
		t.Fatalf("status = %q", got.Status)
	}
	if got.RunningChecksum != "checksum-1" {
		t.Fatalf("running checksum = %q", got.RunningChecksum)
	}
	if got.LastError != "" {
		t.Fatalf("expected empty last error, got %q", got.LastError)
	}
}

func TestDriftStateMarkDrift(t *testing.T) {
	d := NewDriftTracker("file://x")
	d.MarkOK("good", time.Now())
	d.MarkDrift("bad", "ERROR: column \"foo\" cannot be cast to type bar", time.Now())
	got := d.Snapshot()
	if got.Status != DriftStatusDrift {
		t.Fatalf("status = %q", got.Status)
	}
	if got.RunningChecksum != "good" {
		t.Fatalf("running checksum should still be 'good', got %q", got.RunningChecksum)
	}
	if got.SourceChecksum != "bad" {
		t.Fatalf("source checksum = %q", got.SourceChecksum)
	}
	if got.LastError == "" {
		t.Fatalf("last error should be set")
	}
}

func TestDriftStateClearedOnSuccess(t *testing.T) {
	d := NewDriftTracker("file://x")
	d.MarkOK("v1", time.Now())
	d.MarkDrift("v2", "boom", time.Now())
	d.MarkOK("v3", time.Now())
	got := d.Snapshot()
	if got.Status != DriftStatusOK || got.LastError != "" || got.SourceChecksum != "" {
		t.Fatalf("drift not cleared: %+v", got)
	}
}
