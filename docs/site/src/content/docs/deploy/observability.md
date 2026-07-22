---
title: Observability
description: Export traces and logs to any OTLP backend with standard OTEL_* environment variables.
---

instancez exports traces and logs over OTLP using the standard `OTEL_*` environment variables — no instancez-specific config. Export is opt-in and additive: with none of those variables set, the binary behaves exactly as it does today, and stdout/stderr logging keeps working whether export is on or off.

## Enable it

Set the usual OTel SDK variables:

```sh
export OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example.com
export OTEL_SERVICE_NAME=my-instancez-app
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer secret123"
```

Export turns on if any of these is set to a non-empty value:

- `OTEL_EXPORTER_OTLP_ENDPOINT`
- `OTEL_TRACES_EXPORTER`
- `OTEL_LOGS_EXPORTER`

Leave all three unset and instancez skips OTel setup entirely. This gate exists because the OTel SDK's default exporter points at `localhost:4318` even with no config, and without it every deployment without a collector would fail a batch export on a timer and spam the logs.

Logs go out through an slog bridge, so anything already going to `log/slog` — request logs, migration logs, background job logs — is exported without call-site changes. stdout/stderr logging is unaffected either way; OTel export is teed alongside it, not a replacement.

Per-request logs carry the request's `trace_id` and `span_id`, so a log lines up with its trace in the backend. This holds in `inz dev` too: the aligned request line still prints to the console, and the same record is exported through the bridge when OTel is on.

## What gets traced

- **HTTP requests** — every inbound request gets a server span.
- **Postgres queries** — child spans for queries against both connection pools, including schema migrations run at startup.
- **Resend** — outbound calls to the email API.
- **S3** — outbound calls to the storage API.
- **Function invocations** — the call from the Go runtime to the Node worker process. instancez injects a `traceparent` header into that call, so the trace context reaches the worker even though nothing inside the worker uses it yet (see [Known gaps](#known-gaps)).

## SDK environment knobs

These are standard OTel SDK variables, read automatically — nothing instancez-specific to configure:

| Variable | Purpose |
|----------|---------|
| `OTEL_TRACES_SAMPLER` | Trace sampling strategy (e.g. `traceidratio`, `parentbased_always_on`) |
| `OTEL_BSP_*` | Batch span processor tuning (`OTEL_BSP_SCHEDULE_DELAY`, `OTEL_BSP_MAX_EXPORT_BATCH_SIZE`, etc.) |
| `OTEL_RESOURCE_ATTRIBUTES` | Extra resource attributes attached to every span and log record |
| `OTEL_SERVICE_NAME` | Service name in the resource; defaults to `instancez` if unset |

## Local testing without a backend

To see spans and logs on your own terminal instead of standing up a collector, use the console exporter:

```sh
export OTEL_TRACES_EXPORTER=console
export OTEL_LOGS_EXPORTER=console
```

## Lambda

instancez runs long-lived behind the Lambda Web Adapter rather than as a per-invocation handler, so the batch exporter and the shutdown flush both work as they do anywhere else. The one wrinkle is that Lambda freezes the container between requests: if a batch is still buffered when the freeze happens, it goes out on the next invocation instead of the current one. Spans and logs arrive delayed, not lost.

## Known gaps

- **Function code isn't instrumented yet.** Spans and logs from code running inside the Node worker need the JS OTel SDK wired into the worker bootstrap, which is a later phase. The `traceparent` header injected into the worker call means that work can pick up the existing trace once it lands.
- **No metrics yet.** This is traces and logs only; metrics export is a later phase. The existing Prometheus `/metrics` endpoint is unaffected by any of this.
