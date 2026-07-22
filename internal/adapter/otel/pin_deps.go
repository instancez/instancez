package otel

// ponytail: otelpgx is now imported for real by internal/adapter/postgres/pool.go.
// otelaws is still unused until a later task wires it into the aws adapter.
// Without a live import here, `go mod tidy` prunes it from go.mod, and a later
// bare `go get` (no version) could default to one that forces an otel core
// bump. This blank import just holds the pin at the version this task
// verified is compatible with otel v1.42.0. Delete this file once a later
// task adds a real import of otelaws.
import (
	_ "go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
)
