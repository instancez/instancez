package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/saedx1/ultrabase/internal/domain"
)

const slotName = "ultrabase_cdc"

// WALConsumer listens to PostgreSQL logical replication for CDC events.
type WALConsumer struct {
	pool       *pgxpool.Pool
	dispatcher domain.EventDispatcher
	logger     *slog.Logger
	cancel     context.CancelFunc
	done       chan struct{}
}

func NewWALConsumer(pool *pgxpool.Pool, dispatcher domain.EventDispatcher, logger *slog.Logger) *WALConsumer {
	return &WALConsumer{
		pool:       pool,
		dispatcher: dispatcher,
		logger:     logger,
		done:       make(chan struct{}),
	}
}

// Start begins consuming WAL changes. It blocks until Stop is called or ctx is cancelled.
func (w *WALConsumer) Start(ctx context.Context) error {
	// Verify WAL requirements
	if err := w.verifyWALConfig(ctx); err != nil {
		return fmt.Errorf("WAL check: %w", err)
	}

	// Ensure replication slot exists
	if err := w.ensureSlot(ctx); err != nil {
		return fmt.Errorf("WAL slot: %w", err)
	}

	ctx, w.cancel = context.WithCancel(ctx)

	go w.consume(ctx)

	w.logger.Info("WAL consumer started", "slot", slotName)
	return nil
}

// Stop gracefully stops the WAL consumer.
func (w *WALConsumer) Stop(ctx context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *WALConsumer) verifyWALConfig(ctx context.Context) error {
	var walLevel string
	err := w.pool.QueryRow(ctx, "SHOW wal_level").Scan(&walLevel)
	if err != nil {
		return fmt.Errorf("cannot check wal_level: %w", err)
	}
	if walLevel != "logical" {
		return fmt.Errorf("wal_level is %q, must be 'logical'. See: https://ultrabase.dev/docs/wal-setup", walLevel)
	}
	return nil
}

func (w *WALConsumer) ensureSlot(ctx context.Context) error {
	// Check if slot exists
	var count int
	err := w.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM pg_replication_slots WHERE slot_name = $1", slotName).Scan(&count)
	if err != nil {
		return fmt.Errorf("check slot: %w", err)
	}

	if count == 0 {
		_, err := w.pool.Exec(ctx,
			"SELECT pg_create_logical_replication_slot($1, 'wal2json')", slotName)
		if err != nil {
			return fmt.Errorf("create slot: %w", err)
		}
		w.logger.Info("created replication slot", "slot", slotName)
	}

	return nil
}

func (w *WALConsumer) consume(ctx context.Context) {
	defer close(w.done)

	// Poll for changes using pg_logical_slot_get_changes
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.pollChanges(ctx)
		}
	}
}

func (w *WALConsumer) pollChanges(ctx context.Context) {
	rows, err := w.pool.Query(ctx,
		"SELECT lsn, xid, data FROM pg_logical_slot_peek_changes($1, NULL, 100, 'format-version', '2')",
		slotName)
	if err != nil {
		w.logger.Error("WAL poll error", "error", err)
		return
	}

	var lastLSN string
	var dispatched int
	for rows.Next() {
		var lsn, data string
		var xid int64
		if err := rows.Scan(&lsn, &xid, &data); err != nil {
			w.logger.Error("WAL scan error", "error", err)
			continue
		}
		lastLSN = lsn

		event, err := parseWALEvent(lsn, data)
		if err != nil {
			w.logger.Error("WAL parse error", "error", err, "data", data)
			continue
		}
		if event == nil {
			continue
		}
		if err := w.dispatcher.Dispatch(ctx, *event); err != nil {
			w.logger.Error("WAL dispatch error", "error", err, "event", event.EventName)
			// Stop here: if enqueue failed, don't advance past this LSN,
			// so the next poll can retry.
			rows.Close()
			return
		}
		dispatched++
	}
	rows.Close()

	if dispatched == 0 || lastLSN == "" {
		return
	}

	// Advance the slot to the last LSN we durably enqueued into _events.
	// Args: (slot_name, upto_lsn, upto_nchanges). NULL nchanges means
	// "consume everything up to upto_lsn."
	if _, err := w.pool.Exec(ctx,
		"SELECT 1 FROM pg_logical_slot_get_changes($1, $2, NULL, 'format-version', '2')",
		slotName, lastLSN); err != nil {
		w.logger.Error("WAL slot advance error", "error", err, "lsn", lastLSN)
	}
}

// SlotLag returns the current WAL slot lag in bytes.
func SlotLag(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var lag int64
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(
			pg_wal_lsn_diff(pg_current_wal_lsn(), confirmed_flush_lsn),
			0
		)::BIGINT
		FROM pg_replication_slots
		WHERE slot_name = $1
	`, slotName).Scan(&lag)
	return lag, err
}

// DropSlot drops the replication slot (for emergency reset).
func DropSlot(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slotName)
	return err
}

// parseWALEvent converts raw WAL JSON output to a domain Event.
// The LSN is used as the event ID so the outbox can deduplicate on
// (source_id, trigger_name) if pollChanges retries after a crash.
func parseWALEvent(lsn, data string) (*domain.Event, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, fmt.Errorf("parse WAL JSON: %w", err)
	}

	action, _ := raw["action"].(string)
	if action == "B" || action == "C" {
		// Begin/Commit markers, not actual data changes
		return nil, nil
	}

	table, _ := raw["table"].(string)
	schema, _ := raw["schema"].(string)
	if schema == "pg_catalog" || schema == "information_schema" {
		return nil, nil
	}

	var op string
	switch action {
	case "I":
		op = "insert"
	case "U":
		op = "update"
	case "D":
		op = "delete"
	default:
		return nil, nil
	}

	eventName := fmt.Sprintf("%s.%s", table, op)

	event := &domain.Event{
		ID:        lsn,
		EventName: eventName,
		Table:     table,
		Operation: op,
		Timestamp: time.Now(),
	}

	// Extract column data
	if columns, ok := raw["columns"].([]any); ok {
		event.Data = columnsToMap(columns)
	}
	if identity, ok := raw["identity"].([]any); ok {
		event.OldData = columnsToMap(identity)
	}

	return event, nil
}

func columnsToMap(columns []any) map[string]any {
	m := make(map[string]any)
	for _, col := range columns {
		c, ok := col.(map[string]any)
		if !ok {
			continue
		}
		name, _ := c["name"].(string)
		value := c["value"]
		if name != "" {
			m[name] = value
		}
	}
	return m
}
