# Observability acceptance checks

Implementation and regression checks are prepared. Go commands, tests, and live
Prometheus/Jaeger verification have not been run by the agent for these changes.

## Metrics

| Criterion | Expected behavior | Regression coverage |
| --- | --- | --- |
| GET /metrics | HTTP 200, text Prometheus exposition accepted by the Prometheus parser | internal/api/metrics_test.go |
| Redirect latency | Finite buckets from 100 microseconds through 2.5 seconds, plus +Inf | internal/metrics/metrics_test.go |
| Bounded dimensions | Only type/status, operation/collection, and configured stream/group labels; no slug or user dimensions | internal/api/metrics_test.go |
| Stream gauge | Redis is queried each scrape; new messages increase backlog and acknowledgments decrease it | internal/metrics/stream_test.go |

The stream gauge retains the configured backlog definition: unread group lag plus
pending acknowledgments. Delivery moves messages from unread to pending; the total
does not decrease until acknowledgment. Unknown Redis lag is counted from existing
entries after the last delivered ID. Missing streams report zero. Redis errors
omit the sample rather than publish stale values.

## Traces

A redirect cache miss produces client spans for GET, MongoDB find, SET, and XADD
under the HTTP server span. Operations appear in execution order. XADD is queued
and may complete after the HTTP span ends while retaining its trace identity.
Cache hits omit the MongoDB lookup.

The MongoDB trace regression uses actual command-monitor callbacks with synthetic
BSON replies; live MongoDB verification is still needed. Redis commands and queued
XADD are exercised against miniredis in the prepared hierarchy test.

- HTTP failure responses record Error status, an exception event, and the HTTP code.
- Redis errors record an exception and protocol error code; Redis Nil stays a miss.
- MongoDB command failure events record an exception and a code name when supplied.
  Numeric typed-driver errors and write/write-concern reply errors preserve the
  database response code. Write errors within successful protocol replies mark
  the command span as failed.
- Worker startup, polling, batch processing, and shutdown have operation spans.
  MongoDB processing and acknowledgment run under the batch span. Shutdown
  cancellation does not discard the current batch's parent trace context.

Coverage is in internal/telemetry/acceptance_test.go,
internal/telemetry/tracing_test.go, and internal/analytics/worker_test.go.

## Logs

Production logger setup uses JSON before configuration parsing and after full
startup. Application, recovery, and exporter logs use the sanitizing handler.
HTTP completion and worker batch records carry the corresponding trace and span
IDs. Startup records outside an active span omit those fields.

HTTP duration is measured at request completion and recorded as fractional
`duration_ms`. Batch completion also records fractional milliseconds. Access logs
contain route templates, final response status, bytes, and redirect cache outcome.
Raw client addresses, header tokens, query values, and driver error contents are
excluded or redacted. Logging checks are in internal/logging and the worker tests.

## Manual run

After `go mod tidy`, format changed Go files and run `go test ./...`. Then:

1. Start Redis, MongoDB, Jaeger, the API and the worker with APP_ENV=production.
2. Scrape the API's /metrics endpoint and validate the output with Prometheus or
   promtool. Inspect histogram bounds and all shortener metric labels.
3. Create a link, clear its Redis cache entry, and send a redirect request with a
   known sampled traceparent. Check GET, find, SET and XADD trace parentage in Jaeger.
4. Repeat with a cached link; confirm the MongoDB find span is absent.
5. Request an absent link and simulate Redis WRONGTYPE or a database write failure;
   inspect span statuses, exception events and response codes.
6. Add click events, observe a scrape, and allow the worker to acknowledge them.
   Confirm stream backlog changes with unconsumed entries rather than stream length.
7. Inspect production stdout as JSON. Compare access/batch trace IDs with Jaeger and
   confirm duration_ms is numeric, with fractional values when elapsed time warrants.
