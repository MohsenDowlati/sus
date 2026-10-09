# Prometheus metrics

The API serves `/metrics` on `SERVER_ADDRESS` (default `:8080`). The analytics
worker serves `/metrics` on `WORKER_METRICS_ADDRESS` (default `:9091`). Start both
processes to collect database operations from both. Docker Prometheus scrapes
these host ports via `host.docker.internal`; adjust targets for other deployments.

- `shortener_requests_total{type="custom|auto|redirect",status="..."}` counts
  completed create and redirect HTTP requests, including authentication and rate
  limit failures. Status labels reflect the actual response: creation uses 201
  and redirects use 307; 400, 401, 409, 404, 410, 429 and 500 are also observable.
  Creation defaults to `auto` when the bounded 64 KiB JSON prefix has no valid
  `custom` type. Slug checks, analytics, auth, health and scrapes are excluded.
- `shortener_redirect_latency_seconds` measures the full redirect handler duration
  for successes and failures. Buckets are 0.0001, 0.00025, 0.0005, 0.001,
  0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1 and 2.5 seconds, plus +Inf.
  The smallest bucket is 100 microseconds.
- `shortener_cache_hits_total` and `shortener_cache_misses_total` count Redis link
  cache GET results. Negative cache entries count as hits; Redis errors count as
  neither. These measure lookups, including the second lookup within singleflight.
- `shortener_stream_lag{stream="stream:clicks",consumer_group="analytics-workers"}`
  is collected by the API using configured stream/group names. Backlog is Redis
  group lag plus pending acknowledgments, so delivery alone does not clear it.
  Unknown Redis lag falls back to paginated XRANGE after the last delivered ID. A missing
  stream reports zero; a missing group reports the stream length. Redis failures
  omit the gauge for that scrape rather than publish a stale or false zero.
- `shortener_db_operations_total{operation="insert|find|update",collection="links|click_events"}`
  counts attempted MongoDB calls, including failed calls and retries. Bulk writes
  count once per represented operation kind, not per document. Click event upsert
  batches use `update`; daily summaries and user operations are excluded.

## Grafana / PromQL

Cache hit ratio (0–1; undefined when there are no lookups):

```promql
sum(rate(shortener_cache_hits_total[5m]))
/
(sum(rate(shortener_cache_hits_total[5m])) + sum(rate(shortener_cache_misses_total[5m])))
```

Redirect p50, p95 and p99 in seconds (use 0.50, 0.95 or 0.99):

```promql
histogram_quantile(0.95, sum by (le) (rate(shortener_redirect_latency_seconds_bucket[5m])))
```

Request rate by outcome:

```promql
sum by (type, status) (rate(shortener_requests_total[5m]))
```

Total database operation rate across API and workers:

```promql
sum by (operation, collection) (rate(shortener_db_operations_total[5m]))
```
