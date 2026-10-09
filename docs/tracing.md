# OpenTelemetry tracing

Both processes initialize a batching OpenTelemetry TracerProvider before opening
Redis or MongoDB connections. Shutdown drains requests and queued clicks, closes
storage, and then flushes the trace exporter with a 10-second timeout.

The default service names are `shortener-api` and `shortener-analytics-worker`.
Set `OTEL_SERVICE_NAME` independently for each process to override these names.
`OTEL_RESOURCE_ATTRIBUTES` supplies additional resource attributes.

## Export to local Jaeger

The existing Docker Compose Jaeger service enables OTLP and exposes ports 4317
and 4318. Start Jaeger, then run the API and worker on the host.

The default protocol is gRPC and the default local endpoint is
`http://localhost:4317`. To set these explicitly:

```dotenv
OTEL_EXPORTER_OTLP_PROTOCOL=grpc
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
```

For OTLP over HTTP with protobuf:

```dotenv
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
```

Use `http://jaeger:4317` or `http://jaeger:4318` when running a process inside
Docker on the same network. An explicit `https://` endpoint uses TLS. Exporter
headers, certificates, timeout and retry settings use the standard OTLP exporter
configuration. Trace-specific settings such as
`OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` and `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
override their general counterparts. For an HTTP trace-specific endpoint, include
the full path: `http://localhost:4318/v1/traces`.

Sampling uses the SDK's environment configuration, including `OTEL_TRACES_SAMPLER`
and `OTEL_TRACES_SAMPLER_ARG`. The default respects an incoming parent's sampling
decision and samples new traces.

## Recorded spans

- `POST /api/v1/links` and `GET /{code}` create server spans with stable route
  names. W3C `traceparent` and `tracestate` are extracted, and a valid remote
  parent is continued. Requests without a valid parent start a new trace.
  Authentication, rate limiting and handler work run inside these spans.
- `redisotel` records client command spans, including link-cache GET/SET,
  rate limiting, and analytics XADD. Redis command statements are enabled.
- The MongoDB command monitor records command latency and database statements.
- Asynchronous click publication retains the redirect's span context and uses
  an independent two-second publish timeout. The XADD span can finish after
  the HTTP server span ends, while remaining in the same trace.

Command statements include Redis arguments and MongoDB command documents.
The worker creates spans for startup, Redis reads and claims, each batch, and
shutdown. A batch span parents MongoDB commands and XACK; context-aware logs use
its trace and span IDs. Processing and acknowledgment retain the parent span
context while using an independent timeout, so shutdown can drain a current batch.
Trace context is not serialized into Redis stream event fields, so worker traces
are separate from the original HTTP trace.

On cache misses, Redis GET, MongoDB find, Redis SET, and queued XADD are client
spans under the HTTP server span, displayed in execution order. XADD may finish
after the HTTP span ends. Cache hits omit the MongoDB lookup.

HTTP responses >=400 mark the server span as Error with `error.code`,
`error.type`, `http.response.status_code` and an exception event. Backend errors
also record an exception. Redis failures carry protocol codes such as WRONGTYPE;
Redis Nil is a cache miss and is not marked as a failure. MongoDB command failures
record their code name when supplied by the v1 driver's failure event. Write-error
and write-concern replies and typed MongoDB errors also retain numeric
`db.response.status_code` values when available.

## Manual verification

Dependency resolution, formatting and tests have not been run for this change.
Run these from the project root:

```sh
go mod tidy
gofmt -w internal/telemetry internal/config/redis.go internal/repository/mongodb/mongo.go internal/api/router.go internal/analytics/tracker.go internal/analytics/tracker_test.go cmd/server/server.go cmd/worker/main.go
go test ./...
```

Start Jaeger, the API and the worker. Send a create-link request or a redirect
request with a known trace parent, for example:

```sh
curl -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' http://localhost:8080/your-code
```

In Jaeger on port 16686, inspect service `shortener-api`. Verify the HTTP span's
trace ID matches the header and its parent ID is `00f067aa0ba902b7`. A cached
redirect should have a Redis GET child; a cache miss should also show the MongoDB
find and Redis SET. Successful redirects should show an XADD child after queued
publication. Repeat without the header to verify a new trace is created. Switch
to the HTTP exporter settings above and repeat to verify both transports.
