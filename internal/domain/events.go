package domain

import "context"

// EventDispatcher dispatches events to configured actions (webhooks, emails).
type EventDispatcher interface {
	Dispatch(ctx context.Context, event Event) error
}
