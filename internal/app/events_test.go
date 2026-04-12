package app

import (
	"testing"
	"time"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestMatchEventPattern(t *testing.T) {
	tests := []struct {
		pattern   string
		eventName string
		want      bool
	}{
		{"todos.insert", "todos.insert", true},
		{"todos.insert", "todos.update", false},
		{"todos.insert", "users.insert", false},
		{"*.insert", "todos.insert", true},
		{"*.insert", "users.insert", true},
		{"*.insert", "todos.update", false},
		{"todos.*", "todos.insert", true},
		{"todos.*", "todos.update", true},
		{"todos.*", "todos.delete", true},
		{"todos.*", "users.insert", false},
		{"*.*", "todos.insert", true},
		{"*.*", "users.delete", true},
		{"*.delete", "todos.delete", true},
		{"*.delete", "todos.insert", false},
		{"invalid", "todos.insert", false},
		{"todos.insert", "invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.eventName, func(t *testing.T) {
			got := MatchEventPattern(tt.pattern, tt.eventName)
			if got != tt.want {
				t.Errorf("MatchEventPattern(%q, %q) = %v, want %v", tt.pattern, tt.eventName, got, tt.want)
			}
		})
	}
}

func TestRenderTemplate(t *testing.T) {
	event := domain.Event{
		EventName: "todos.insert",
		Table:     "todos",
		Operation: "insert",
		Timestamp: time.Date(2026, 4, 5, 10, 42, 15, 0, time.UTC),
		Data: map[string]any{
			"id":    1,
			"title": "Test Task",
			"email": "user@example.com",
		},
		OldData: map[string]any{
			"status": "pending",
		},
	}

	tests := []struct {
		name     string
		template string
		want     string
	}{
		{"plain text", "hello world", "hello world"},
		{"event name", "{{event}}", "todos.insert"},
		{"table", "{{table}}", "todos"},
		{"operation", "{{operation}}", "insert"},
		{"project name", "Welcome to {{project.name}}!", "Welcome to My App!"},
		{"data field", "{{data.email}}", "user@example.com"},
		{"data field int", "{{data.id}}", "1"},
		{"old_data field", "{{old_data.status}}", "pending"},
		{"mixed", "New {{data.title}} in {{table}}", "New Test Task in todos"},
		{"unresolved", "Hello {{unknown.field}}", "Hello "},
		{"no templates", "just text", "just text"},
		{"timestamp", "{{timestamp}}", "2026-04-05T10:42:15Z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderTemplate(tt.template, event, "My App")
			if got != tt.want {
				t.Errorf("RenderTemplate(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

func TestComputeBackoff(t *testing.T) {
	tests := []struct {
		attempt  int
		strategy string
		want     time.Duration
	}{
		{1, "exponential", 1 * time.Second},
		{2, "exponential", 2 * time.Second},
		{3, "exponential", 4 * time.Second},
		{4, "exponential", 8 * time.Second},
		{1, "linear", 1 * time.Second},
		{2, "linear", 2 * time.Second},
		{3, "linear", 3 * time.Second},
		{1, "", 1 * time.Second},  // default is exponential
		{3, "", 4 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.strategy+"-"+string(rune('0'+tt.attempt)), func(t *testing.T) {
			got := ComputeBackoff(tt.attempt, tt.strategy)
			if got != tt.want {
				t.Errorf("ComputeBackoff(%d, %q) = %v, want %v", tt.attempt, tt.strategy, got, tt.want)
			}
		})
	}
}

func TestComputeHMACSignature(t *testing.T) {
	sig := ComputeHMACSignature("secret", "12345", `{"data":"test"}`)
	if sig == "" {
		t.Error("expected non-empty signature")
	}
	if len(sig) < 10 {
		t.Error("signature too short")
	}
	if sig[:7] != "sha256=" {
		t.Errorf("expected sha256= prefix, got %s", sig[:7])
	}

	// Deterministic
	sig2 := ComputeHMACSignature("secret", "12345", `{"data":"test"}`)
	if sig != sig2 {
		t.Error("same inputs should produce same signature")
	}

	// Different with different inputs
	sig3 := ComputeHMACSignature("other", "12345", `{"data":"test"}`)
	if sig == sig3 {
		t.Error("different secrets should produce different signatures")
	}
}

func TestMatchTriggers(t *testing.T) {
	cfg := &domain.Config{
		On: map[string]domain.Trigger{
			"welcome": {
				Events:  []string{"users.insert"},
				Webhook: &domain.WebhookAction{URL: "https://example.com"},
			},
			"audit_deletes": {
				Events:  []string{"*.delete"},
				Webhook: &domain.WebhookAction{URL: "https://audit.example.com"},
			},
			"cron_job": {
				Schedule: "0 9 * * *",
				Email:    &domain.EmailAction{To: "admin@example.com", Subject: "Daily", Body: "Report"},
			},
		},
	}

	d := &EventDispatcher{cfg: cfg}

	// users.insert should match "welcome" but not "audit_deletes" or "cron_job"
	matched := d.matchTriggers(domain.Event{EventName: "users.insert"})
	if len(matched) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matched))
	}
	if _, ok := matched["welcome"]; !ok {
		t.Error("expected 'welcome' trigger to match")
	}

	// todos.delete should match "audit_deletes"
	matched = d.matchTriggers(domain.Event{EventName: "todos.delete"})
	if len(matched) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matched))
	}
	if _, ok := matched["audit_deletes"]; !ok {
		t.Error("expected 'audit_deletes' trigger to match")
	}

	// todos.insert should match nothing
	matched = d.matchTriggers(domain.Event{EventName: "todos.insert"})
	if len(matched) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matched))
	}

	// users.delete should match "audit_deletes"
	matched = d.matchTriggers(domain.Event{EventName: "users.delete"})
	if len(matched) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matched))
	}
}
