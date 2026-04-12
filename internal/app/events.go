package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/saedx1/ultrabase/internal/domain"
)

// EventDispatcher matches events to triggers and enqueues rows in the _events outbox.
// It does NOT perform delivery itself — EventWorker handles that.
type EventDispatcher struct {
	cfg        *domain.Config
	db         domain.Database
	logger     *slog.Logger
	hmacSecret string
}

func NewEventDispatcher(cfg *domain.Config, db domain.Database, logger *slog.Logger, hmacSecret string) *EventDispatcher {
	return &EventDispatcher{
		cfg:        cfg,
		db:         db,
		logger:     logger,
		hmacSecret: hmacSecret,
	}
}

// Dispatch matches the event against all WAL triggers and inserts one outbox
// row per match. Called by the WAL consumer.
func (d *EventDispatcher) Dispatch(ctx context.Context, event domain.Event) error {
	matched := d.matchTriggers(event)
	if len(matched) == 0 {
		return nil
	}
	for name, trigger := range matched {
		if err := d.enqueue(ctx, event.ID, name, trigger, event); err != nil {
			d.logger.Error("enqueue event", "trigger", name, "error", err)
		}
	}
	return nil
}

// EnqueueTrigger inserts an outbox row for a specific trigger. Used by the
// cron scheduler, which already knows which trigger fired.
func (d *EventDispatcher) EnqueueTrigger(ctx context.Context, sourceID, triggerName string, trigger domain.Trigger, event domain.Event) error {
	return d.enqueue(ctx, sourceID, triggerName, trigger, event)
}

func (d *EventDispatcher) matchTriggers(event domain.Event) map[string]domain.Trigger {
	matched := make(map[string]domain.Trigger)
	for name, trigger := range d.cfg.On {
		if trigger.Schedule != "" {
			continue
		}
		for _, pattern := range trigger.Events {
			if MatchEventPattern(pattern, event.EventName) {
				matched[name] = trigger
				break
			}
		}
	}
	return matched
}

func (d *EventDispatcher) enqueue(ctx context.Context, sourceID, triggerName string, trigger domain.Trigger, event domain.Event) error {
	delivery := buildDelivery(d.cfg, d.hmacSecret, trigger, event)
	if delivery.Type == "" {
		return nil // nothing to deliver
	}
	deliveryJSON, err := json.Marshal(delivery)
	if err != nil {
		return fmt.Errorf("marshal delivery: %w", err)
	}
	payloadJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	_, err = d.db.Exec(ctx, `
INSERT INTO _events (source_id, trigger_name, event_name, table_name, operation, payload, delivery)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)
ON CONFLICT (source_id, trigger_name) DO NOTHING`,
		sourceID, triggerName, event.EventName, event.Table, event.Operation,
		string(payloadJSON), string(deliveryJSON))
	return err
}

// Delivery is a self-contained snapshot of how an event should be delivered.
// It is frozen at enqueue time and stored as JSONB on the _events row.
type Delivery struct {
	Type       string            `json:"type"` // "webhook" or "email"
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	HMACSecret string            `json:"hmac_secret,omitempty"`
	To         string            `json:"to,omitempty"`
	Subject    string            `json:"subject,omitempty"`
	Body       string            `json:"body,omitempty"`
	MaxRetries int               `json:"max_retries"`
	Backoff    string            `json:"backoff"`
}

func buildDelivery(cfg *domain.Config, secret string, trigger domain.Trigger, event domain.Event) Delivery {
	projectName := cfg.Project.Name
	if trigger.Webhook != nil {
		headers := make(map[string]string, len(trigger.Webhook.Headers))
		for k, v := range trigger.Webhook.Headers {
			headers[k] = RenderTemplate(v, event, projectName)
		}
		max := trigger.Webhook.Retry.Max
		if max == 0 {
			max = 1
		}
		return Delivery{
			Type:       "webhook",
			URL:        RenderTemplate(trigger.Webhook.URL, event, projectName),
			Headers:    headers,
			HMACSecret: secret,
			MaxRetries: max,
			Backoff:    trigger.Webhook.Retry.Backoff,
		}
	}
	if trigger.Email != nil {
		return Delivery{
			Type:       "email",
			To:         RenderTemplate(trigger.Email.To, event, projectName),
			Subject:    RenderTemplate(trigger.Email.Subject, event, projectName),
			Body:       RenderTemplate(trigger.Email.Body, event, projectName),
			MaxRetries: 3,
			Backoff:    "exponential",
		}
	}
	return Delivery{}
}

// MatchEventPattern checks if "table.operation" matches a pattern like "todos.insert" or "*.delete".
func MatchEventPattern(pattern, eventName string) bool {
	parts := strings.SplitN(pattern, ".", 2)
	evParts := strings.SplitN(eventName, ".", 2)

	if len(parts) != 2 || len(evParts) != 2 {
		return false
	}

	tableMatch := parts[0] == "*" || parts[0] == evParts[0]
	opMatch := parts[1] == "*" || parts[1] == evParts[1]

	return tableMatch && opMatch
}

// EventWorker polls the _events outbox and delivers pending rows.
type EventWorker struct {
	db       domain.Database
	email    domain.EmailSender
	logger   *slog.Logger
	client   *http.Client
	interval time.Duration
	batch    int
}

func NewEventWorker(db domain.Database, email domain.EmailSender, logger *slog.Logger) *EventWorker {
	return &EventWorker{
		db:       db,
		email:    email,
		logger:   logger,
		client:   &http.Client{Timeout: 10 * time.Second},
		interval: 1 * time.Second,
		batch:    100,
	}
}

// Start runs the worker loop until ctx is cancelled.
func (w *EventWorker) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.logger.Info("event worker started")
	for {
		select {
		case <-ctx.Done():
			w.logger.Info("event worker stopped")
			return nil
		case <-ticker.C:
			w.pollOnce(ctx)
		}
	}
}

func (w *EventWorker) pollOnce(ctx context.Context) {
	rows, err := w.db.Query(ctx, `
SELECT id, trigger_name, event_name, payload, delivery, attempts
FROM _events
WHERE status = 'pending' AND next_attempt_at <= NOW()
ORDER BY id
LIMIT $1`, w.batch)
	if err != nil {
		w.logger.Error("poll outbox", "error", err)
		return
	}
	for _, row := range rows {
		w.deliverRow(ctx, row)
	}
}

func (w *EventWorker) deliverRow(ctx context.Context, row map[string]any) {
	id, _ := toInt64(row["id"])
	triggerName, _ := row["trigger_name"].(string)
	attempts, _ := toInt64(row["attempts"])

	delivery, err := decodeDelivery(row["delivery"])
	if err != nil {
		w.logger.Error("decode delivery", "id", id, "error", err)
		w.markDead(ctx, id, "decode delivery: "+err.Error())
		return
	}

	payload, _ := row["payload"].([]byte)
	if payload == nil {
		if s, ok := row["payload"].(string); ok {
			payload = []byte(s)
		}
	}

	var sendErr error
	switch delivery.Type {
	case "webhook":
		sendErr = w.sendWebhook(ctx, delivery, payload)
	case "email":
		sendErr = w.sendEmail(ctx, delivery)
	default:
		w.markDead(ctx, id, "unknown delivery type: "+delivery.Type)
		return
	}

	if sendErr == nil {
		w.markDelivered(ctx, id)
		return
	}

	nextAttempts := attempts + 1
	w.logger.Warn("delivery failed",
		"id", id, "trigger", triggerName,
		"attempt", nextAttempts, "max", delivery.MaxRetries, "error", sendErr)

	if int(nextAttempts) >= delivery.MaxRetries {
		w.markDead(ctx, id, sendErr.Error())
		return
	}

	backoff := ComputeBackoff(int(nextAttempts), delivery.Backoff)
	w.markRetry(ctx, id, nextAttempts, sendErr.Error(), backoff)
}

func (w *EventWorker) sendWebhook(ctx context.Context, d Delivery, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", d.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ultrabase-Webhook", "true")

	if d.HMACSecret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		sig := ComputeHMACSignature(d.HMACSecret, timestamp, string(payload))
		req.Header.Set("X-Ultrabase-Timestamp", timestamp)
		req.Header.Set("X-Ultrabase-Signature", sig)
	}
	for k, v := range d.Headers {
		req.Header.Set(k, v)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("http status %d", resp.StatusCode)
}

func (w *EventWorker) sendEmail(ctx context.Context, d Delivery) error {
	if w.email == nil {
		return fmt.Errorf("no email provider configured")
	}
	return w.email.Send(ctx, domain.EmailMessage{
		To:      []string{d.To},
		Subject: d.Subject,
		HTML:    d.Body,
	})
}

func (w *EventWorker) markDelivered(ctx context.Context, id int64) {
	_, err := w.db.Exec(ctx,
		`UPDATE _events SET status = 'delivered', delivered_at = NOW(), attempts = attempts + 1 WHERE id = $1`, id)
	if err != nil {
		w.logger.Error("mark delivered", "id", id, "error", err)
	}
}

func (w *EventWorker) markDead(ctx context.Context, id int64, lastError string) {
	_, err := w.db.Exec(ctx,
		`UPDATE _events SET status = 'dead', last_error = $2, attempts = attempts + 1 WHERE id = $1`,
		id, lastError)
	if err != nil {
		w.logger.Error("mark dead", "id", id, "error", err)
	}
}

func (w *EventWorker) markRetry(ctx context.Context, id, attempts int64, lastError string, backoff time.Duration) {
	_, err := w.db.Exec(ctx,
		`UPDATE _events SET attempts = $2, last_error = $3, next_attempt_at = NOW() + ($4 || ' milliseconds')::interval WHERE id = $1`,
		id, attempts, lastError, strconv.FormatInt(backoff.Milliseconds(), 10))
	if err != nil {
		w.logger.Error("mark retry", "id", id, "error", err)
	}
}

func decodeDelivery(v any) (Delivery, error) {
	var d Delivery
	switch t := v.(type) {
	case []byte:
		return d, json.Unmarshal(t, &d)
	case string:
		return d, json.Unmarshal([]byte(t), &d)
	case map[string]any:
		b, err := json.Marshal(t)
		if err != nil {
			return d, err
		}
		return d, json.Unmarshal(b, &d)
	}
	return d, fmt.Errorf("unsupported delivery type: %T", v)
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	}
	return 0, false
}

// ComputeBackoff returns the retry delay for the given attempt number.
func ComputeBackoff(attempt int, strategy string) time.Duration {
	if strategy == "linear" {
		return time.Duration(attempt) * time.Second
	}
	// exponential (default)
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

// ComputeHMACSignature signs "timestamp.body" with HMAC-SHA256.
func ComputeHMACSignature(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "." + body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// RenderTemplate does mustache-style {{...}} variable substitution.
func RenderTemplate(tmpl string, event domain.Event, projectName string) string {
	if !strings.Contains(tmpl, "{{") {
		return tmpl
	}

	result := tmpl

	result = strings.ReplaceAll(result, "{{event}}", event.EventName)
	result = strings.ReplaceAll(result, "{{table}}", event.Table)
	result = strings.ReplaceAll(result, "{{operation}}", event.Operation)
	result = strings.ReplaceAll(result, "{{timestamp}}", event.Timestamp.Format(time.RFC3339))
	result = strings.ReplaceAll(result, "{{project.name}}", projectName)

	if event.Data != nil {
		for key, val := range event.Data {
			placeholder := "{{data." + key + "}}"
			result = strings.ReplaceAll(result, placeholder, fmt.Sprint(val))
		}
	}

	if event.OldData != nil {
		for key, val := range event.OldData {
			placeholder := "{{old_data." + key + "}}"
			result = strings.ReplaceAll(result, placeholder, fmt.Sprint(val))
		}
	}

	unresolvedPattern := regexp.MustCompile(`\{\{[^}]+\}\}`)
	result = unresolvedPattern.ReplaceAllString(result, "")

	return result
}
