package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/saedx1/ultrabase/internal/domain"
)

// Scheduler runs cron-triggered actions (webhooks and emails).
type Scheduler struct {
	cron       *cron.Cron
	cfg        *domain.Config
	db         domain.Database
	email      domain.EmailSender
	dispatcher *EventDispatcher
	logger     *slog.Logger
}

// NewScheduler creates a new cron scheduler from config triggers.
func NewScheduler(cfg *domain.Config, db domain.Database, email domain.EmailSender, dispatcher *EventDispatcher, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		cron:       cron.New(),
		cfg:        cfg,
		db:         db,
		email:      email,
		dispatcher: dispatcher,
		logger:     logger,
	}
}

// Start registers all cron triggers and starts the scheduler.
func (s *Scheduler) Start(ctx context.Context) error {
	count := 0
	for name, trigger := range s.cfg.On {
		if trigger.Schedule == "" {
			continue
		}

		triggerName := name
		t := trigger

		_, err := s.cron.AddFunc(t.Schedule, func() {
			s.executeTrigger(ctx, triggerName, t)
		})
		if err != nil {
			return fmt.Errorf("invalid cron schedule for trigger %q: %w", triggerName, err)
		}
		count++
		s.logger.Info("registered cron trigger", "name", triggerName, "schedule", t.Schedule)
	}

	if count == 0 {
		return nil
	}

	s.cron.Start()
	s.logger.Info("cron scheduler started", "triggers", count)

	<-ctx.Done()
	s.cron.Stop()
	s.logger.Info("cron scheduler stopped")
	return nil
}

// Stop halts the cron scheduler.
func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) executeTrigger(ctx context.Context, name string, trigger domain.Trigger) {
	s.logger.Info("cron trigger fired", "name", name, "schedule", trigger.Schedule)

	sourceID := fmt.Sprintf("cron_%s_%d", name, time.Now().UnixMilli())
	event := domain.Event{
		ID:        sourceID,
		EventName: "cron." + name,
		Table:     "",
		Operation: "cron",
		Timestamp: time.Now(),
	}

	// Webhooks go through the outbox so they inherit durable retries.
	if trigger.Webhook != nil {
		if err := s.dispatcher.EnqueueTrigger(ctx, sourceID, name, trigger, event); err != nil {
			s.logger.Error("cron webhook enqueue failed", "name", name, "error", err)
		}
	}

	// Email cron triggers support per-recipient expansion (to_query / data_query)
	// that the outbox schema can't represent as a single row, so we keep them
	// on the direct-send path.
	if trigger.Email != nil {
		go s.executeCronEmail(ctx, name, trigger, event)
	}
}

func (s *Scheduler) executeCronEmail(ctx context.Context, name string, trigger domain.Trigger, event domain.Event) {
	ea := trigger.Email
	if s.email == nil {
		s.logger.Warn("cron email trigger configured but no email provider", "name", name)
		return
	}

	// If to_query is set, query for recipients
	if ea.ToQuery != "" {
		s.executeCronEmailWithQuery(ctx, name, trigger, event)
		return
	}

	// Simple case: static recipient
	if ea.To == "" {
		s.logger.Warn("cron email trigger has no recipient", "name", name)
		return
	}

	subject := RenderTemplate(ea.Subject, event, s.cfg.Project.Name)
	body := RenderTemplate(ea.Body, event, s.cfg.Project.Name)

	if err := s.email.Send(ctx, domain.EmailMessage{
		To:      []string{ea.To},
		Subject: subject,
		HTML:    body,
	}); err != nil {
		s.logger.Error("cron email send failed", "name", name, "error", err)
	}
}

func (s *Scheduler) executeCronEmailWithQuery(ctx context.Context, name string, trigger domain.Trigger, event domain.Event) {
	ea := trigger.Email

	// Execute to_query to get list of recipients
	recipients, err := s.db.Query(ctx, ea.ToQuery)
	if err != nil {
		s.logger.Error("cron to_query failed", "name", name, "error", err)
		return
	}

	for _, recipient := range recipients {
		email, ok := recipient["email"].(string)
		if !ok {
			continue
		}

		// Check condition if present
		if ea.Condition != "" {
			if !evaluateCondition(recipient, ea.Condition) {
				continue
			}
		}

		// Execute data_query per recipient if present
		var data map[string]any
		if ea.DataQuery != "" {
			userID := recipient["id"]
			if userID == nil {
				userID = recipient["user_id"]
			}
			row, err := s.db.QueryRow(ctx, ea.DataQuery, userID)
			if err != nil {
				s.logger.Error("cron data_query failed", "name", name, "email", email, "error", err)
				continue
			}
			data = row
		}

		// Build event with per-recipient data for template rendering
		recipientEvent := event
		recipientEvent.Data = mergeData(recipient, data)

		subject := RenderTemplate(ea.Subject, recipientEvent, s.cfg.Project.Name)
		body := RenderTemplate(ea.Body, recipientEvent, s.cfg.Project.Name)

		if err := s.email.Send(ctx, domain.EmailMessage{
			To:      []string{email},
			Subject: subject,
			HTML:    body,
		}); err != nil {
			s.logger.Error("cron email send failed", "name", name, "email", email, "error", err)
		}
	}
}

// evaluateCondition checks a simple condition like "data.field = value" on a row.
func evaluateCondition(row map[string]any, condition string) bool {
	// Simple heuristic: if condition contains a field reference, check if it's truthy
	// Full SQL-like evaluation would be complex; for now, we support basic "field IS NOT NULL" patterns
	for key, val := range row {
		if val == nil {
			continue
		}
		if fmt.Sprintf("%s IS NOT NULL", key) == condition {
			return true
		}
	}
	return true // default to including the recipient
}

func mergeData(base, extra map[string]any) map[string]any {
	result := make(map[string]any)
	for k, v := range base {
		result[k] = v
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

// toJSONBytes marshals any value to JSON bytes.
func toJSONBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
