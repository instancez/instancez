package otel

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func TestEnabled(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"none set", map[string]string{}, false},
		{"endpoint set", map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4318"}, true},
		{"traces exporter set", map[string]string{"OTEL_TRACES_EXPORTER": "otlp"}, true},
		{"logs exporter set", map[string]string{"OTEL_LOGS_EXPORTER": "console"}, true},
		{"empty value ignored", map[string]string{"OTEL_TRACES_EXPORTER": ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear all three first so cases don't leak into each other.
			for _, k := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_TRACES_EXPORTER", "OTEL_LOGS_EXPORTER"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := Enabled(); got != tc.want {
				t.Fatalf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSetupDisabledIsNoop(t *testing.T) {
	for _, k := range gateVars {
		t.Setenv(k, "")
	}
	h, shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup returned err: %v", err)
	}
	if h != nil {
		t.Fatal("disabled Setup should return a nil handler")
	}
	if shutdown == nil {
		t.Fatal("shutdown must never be nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("noop shutdown returned err: %v", err)
	}
}

func TestSetupEnabledConsole(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	t.Setenv("OTEL_LOGS_EXPORTER", "console")
	h, shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if h == nil {
		t.Fatal("enabled Setup should return a non-nil handler")
	}
	if shutdown == nil {
		t.Fatal("shutdown must not be nil")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestBuildResourceServiceName(t *testing.T) {
	t.Run("defaults to instancez", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "")
		res, err := buildResource(context.Background())
		if err != nil {
			t.Fatalf("buildResource: %v", err)
		}
		got, ok := res.Set().Value(semconv.ServiceNameKey)
		if !ok {
			t.Fatal("service.name attribute missing")
		}
		if got.AsString() != defaultServiceName {
			t.Fatalf("service.name = %q, want %q", got.AsString(), defaultServiceName)
		}
	})

	t.Run("OTEL_SERVICE_NAME overrides the default", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "custom-svc")
		res, err := buildResource(context.Background())
		if err != nil {
			t.Fatalf("buildResource: %v", err)
		}
		got, ok := res.Set().Value(semconv.ServiceNameKey)
		if !ok {
			t.Fatal("service.name attribute missing")
		}
		if got.AsString() != "custom-svc" {
			t.Fatalf("service.name = %q, want %q", got.AsString(), "custom-svc")
		}
	})
}

func TestComposeLoggerNilBridge(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	ComposeLogger(base, nil).Info("x")
	if !strings.Contains(buf.String(), "x") {
		t.Fatal("base handler should still receive records when bridge is nil")
	}
}
