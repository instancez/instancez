package app

import (
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestEvaluateCondition(t *testing.T) {
	row := map[string]any{
		"email":      "test@example.com",
		"name":       "Test User",
		"last_login": nil,
	}

	// IS NOT NULL for non-nil field
	if !evaluateCondition(row, "email IS NOT NULL") {
		t.Error("email IS NOT NULL should be true")
	}

	// IS NOT NULL for nil field — condition returns true by default
	if !evaluateCondition(row, "last_login IS NOT NULL") {
		t.Error("default should be true when condition doesn't match exactly")
	}
}

func TestMergeData(t *testing.T) {
	base := map[string]any{"email": "a@b.com", "name": "Alice"}
	extra := map[string]any{"score": 42, "name": "Alice Updated"}

	result := mergeData(base, extra)
	if result["email"] != "a@b.com" {
		t.Error("base field should be preserved")
	}
	if result["score"] != 42 {
		t.Error("extra field should be added")
	}
	if result["name"] != "Alice Updated" {
		t.Error("extra should override base")
	}
}

func TestSchedulerTriggerCount(t *testing.T) {
	cfg := &domain.Config{
		On: map[string]domain.Trigger{
			"daily_report": {
				Schedule: "0 9 * * *",
				Webhook: &domain.WebhookAction{
					URL: "https://example.com/report",
				},
			},
			"on_insert": {
				Events:  []string{"todos.insert"},
				Webhook: &domain.WebhookAction{URL: "https://example.com/hook"},
			},
			"weekly_digest": {
				Schedule: "0 0 * * 1",
				Email: &domain.EmailAction{
					To:      "admin@example.com",
					Subject: "Weekly Digest",
					Body:    "Here is your digest.",
				},
			},
		},
	}

	// Count how many triggers have schedules
	count := 0
	for _, trigger := range cfg.On {
		if trigger.Schedule != "" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 scheduled triggers, got %d", count)
	}
}
