// Package otel wires OpenTelemetry export on top of instancez's existing
// stdout logging. Export is opt-in: with no OTEL_* env set, Setup is a no-op
// and Enabled reports false, so the binary behaves exactly as it did before.
package otel

import "os"

// gateVars are the env vars that switch OTLP export on. autoexport otherwise
// defaults to otlp -> localhost:4318 and fails quietly forever, so we gate on
// the operator having pointed us somewhere on purpose.
var gateVars = []string{
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_TRACES_EXPORTER",
	"OTEL_LOGS_EXPORTER",
}

// Enabled reports whether OpenTelemetry export should be turned on. Read live
// (uncached) so it stays honest under tests that flip the env.
func Enabled() bool {
	for _, k := range gateVars {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}
