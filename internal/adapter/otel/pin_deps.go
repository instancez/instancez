package otel

// ponytail: otelpgx and otelaws aren't imported by real code until later
// tasks wire them into the postgres pool / aws adapters. Without a live
// import here, `go mod tidy` prunes them from go.mod, and a later bare
// `go get github.com/exaring/otelpgx` (no version) would default to a
// version that forces an otel core bump (v0.11.x requires otel 1.43+).
// These blank imports just hold the pin at the versions this task
// verified are compatible with otel v1.42.0. Delete this file once a
// later task adds a real import of both packages.
import (
	_ "github.com/exaring/otelpgx"
	_ "go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
)
