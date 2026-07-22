// Package otel wires OpenTelemetry export on top of instancez's existing
// stdout logging. Export is opt-in: with no OTEL_* env set, Setup is a no-op
// and Enabled reports false, so the binary behaves exactly as it did before.
package otel

import (
	"context"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// gateVars are the env vars that switch OTLP export on. autoexport otherwise
// defaults to otlp -> localhost:4318 and fails quietly forever, so we gate on
// the operator having pointed us somewhere on purpose.
var gateVars = []string{
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_TRACES_EXPORTER",
	"OTEL_LOGS_EXPORTER",
}

const scopeName = "github.com/instancez/instancez"

func noopShutdown(context.Context) error { return nil }

// Setup wires OpenTelemetry from OTEL_* env. When disabled it returns a nil
// slog handler (callers keep their stdout handler as-is) and a no-op shutdown.
// When enabled it sets the global tracer provider + W3C propagator (so
// otelhttp/otelpgx/otelaws pick them up with no threading), a log provider, and
// returns an slog bridge plus a shutdown that flushes both.
func Setup(ctx context.Context) (slog.Handler, func(context.Context) error, error) {
	if !Enabled() {
		return nil, noopShutdown, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(), // OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, noopShutdown, err
	}
	if res, err = resource.Merge(resource.Default(), res); err != nil {
		return nil, noopShutdown, err
	}

	spanExp, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, noopShutdown, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(spanExp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	logExp, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, noopShutdown, err
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res),
	)
	global.SetLoggerProvider(lp)

	handler := otelslog.NewHandler(scopeName, otelslog.WithLoggerProvider(lp))

	shutdown := func(ctx context.Context) error {
		errT := tp.Shutdown(ctx)
		errL := lp.Shutdown(ctx)
		if errT != nil {
			return errT
		}
		return errL
	}
	return handler, shutdown, nil
}

// ComposeLogger returns a logger that writes to base, plus the OTLP bridge when
// bridge is non-nil. When bridge is nil it just wraps base — callers can pass
// the Setup handler through unconditionally.
func ComposeLogger(base slog.Handler, bridge slog.Handler) *slog.Logger {
	if bridge == nil {
		return slog.New(base)
	}
	return slog.New(newFanout(base, bridge))
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
