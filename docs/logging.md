# Structured logging

API and analytics worker loggers write JSON to stdout when `APP_ENV=production`.
Other environments use text output. Both pass through the same trace and
redaction handler. `LOG_LEVEL` controls the minimum level.

Use context-aware methods (`InfoContext`, `WarnContext`, `ErrorContext`, or
`LogAttrs`) to correlate a record with its active OpenTelemetry span. Valid span
contexts automatically add root-level `trace_id` and `span_id`, including with
`logger.With(...)` and `logger.WithGroup(...)`. Startup records without a valid
span omit these fields. Worker startup, polling, processing and shutdown use
operation spans and context-aware log calls; batch completion also records
fractional `duration_ms`. Caller-supplied trace IDs are discarded.

HTTP access records are emitted once after request handling and recovery finish.
`duration_ms` is elapsed time measured with `time.Since`, converted to a floating
point millisecond value; it is not rounded to whole milliseconds. The record
contains method, route template, final status and response bytes. Redirects also
include code and cache_hit when resolution was attempted. Cached negative,
disabled and expired entries count as cache hits; invalid cache JSON does not.
Statuses reflect the actual HTTP response, including the existing 307 redirect.

The outer access logger captures the route middleware's server span so completion
records use that span's identity after it ends. Query strings, request bodies,
headers, raw paths, raw client addresses and caller-supplied request IDs are not
included in access records. Rate-limit failures identify the policy rather than
the IP-bearing Redis key.

## Redaction

The handler sanitizes messages and structured attributes before serialization:

- Sensitive keys (passwords, tokens, authorization, credentials, cookies, API
  keys, client address fields, request objects and related fields) are redacted.
- Nested groups and bound attributes receive the same treatment.
- IPv4/IPv6 address literals, URL user credentials, Bearer/Basic authorization,
  JWT strings and credential assignments are redacted from string values.
- Configured JWT secrets, database password, Redis password and analytics salt
  are scrubbed from string values.
- Errors retain their Go type rather than their raw driver error text. Opaque
  objects and maps are redacted rather than serialized with their contents.
- Startup configuration failures omit raw input and driver error text; exporter
  errors use the same sanitized structured logger.
- Panic recovery records only the panic type and writes HTTP 500, avoiding raw
  panic values or stack output containing request or database data.

Keep log messages static and use structured fields. Never put arbitrary request
values or unlabeled credentials in free-form messages: an unmarked opaque secret
cannot be identified reliably by text pattern matching. Use a sensitive field key
when recording an operation involving credentials so its value is redacted.

These rules apply to application logs. OTLP database statement attributes are
configured separately in the tracing setup.

## Manual validation

Go commands and tests have not been run for this change. After resolving tracing
requirements with `go mod tidy`, format the changed files and run `go test ./...`.
The new logging tests cover grouped trace fields, redaction, HTTP server-span
correlation, elapsed duration, implicit 200 responses and recovered panics.

With production logging enabled, request a short link using a known `traceparent`
and inspect the resulting JSON. Its trace ID should match the incoming trace,
while its span ID should identify the server span. Send headers and query strings
containing test secrets and confirm they are absent from stdout.
