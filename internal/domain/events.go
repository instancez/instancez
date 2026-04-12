package domain

import "context"

// WALConsumer is the port for consuming PostgreSQL WAL changes.
type WALConsumer interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// EventDispatcher dispatches events to configured actions (webhooks, emails).
type EventDispatcher interface {
	Dispatch(ctx context.Context, event Event) error
}
