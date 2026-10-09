# Staged k6 benchmark

The `benchmark` Compose profile prepares a repeatable link fixture and runs an
arrival-rate workload. The `loadtest` profile remains available for the small
health/redirect smoke test.

## Workload

Every measured iteration sends exactly one HTTP request. Across each block of 20
iterations, 19 requests resolve existing links and one creates a link:

| Operation | Share | Expected response |
| --- | --- | --- |
| Redirect to a seeded custom or Base62 code | 95% | 307 with the configured Location |
| Create a fresh automatic link | 2.5% | 201 |
| Create an intentionally duplicate custom link | 2.5% | 409 |

The split uses the global scenario iteration index rather than random selection
per VU. Redirect selection cycles across the entire pool with a coprime stride;
both halves of the fixture are exercised evenly over complete cycles. A final
partial block can differ by fewer than 20 operations. Redirects are never followed.
Expected conflicts count as successful HTTP operations; 429s and other unexpected
responses fail the test.

| Stage | Duration | Total arrival rate |
| --- | --- | --- |
| Warm-up | 1 minute | 0 → 2,000 RPS |
| Peak ramp | 3 minutes | 2,000 → 12,000 RPS |
| Sustained soak | 5 minutes | 12,000 RPS |
| Cool-down | 1 minute | 12,000 → 0 RPS |

`K6_PEAK_RPS` overrides the peak and soak target; it must be at least 10,000.
The measured schedule lasts 10 minutes, with up to 30 seconds to finish in-flight
iterations. Setup and database seeding happen before this schedule.

These are offered-load targets, not a claim about achieved throughput. The test
fails when k6 drops iterations because it cannot schedule the requested traffic.
Increase VU allocation or use a more capable load generator if needed. There is
no sleep in the arrival-rate iteration.

## Seed and warm-up

[seed-pool.json](../Docker/k6/seed-pool.json) contains exactly 10,000 distinct,
stable codes in a fixed order:

- 5,000 custom codes: `k6-warm-00000` through `k6-warm-04999`.
- 5,000 seven-character Base62 codes with the `K6` prefix. Their values are
  generated deterministically for reproducible fixtures and stored with type
  `auto`. Measured automatic POSTs exercise the application's random generator.

Compose runs [seed.mongodb.js](../Docker/k6/seed.mongodb.js) in `k6-seed` before
starting `k6-benchmark`. It upserts the fixture directly into MongoDB, using a
unique code index and batches of 500. Repeated runs update only documents carrying
the fixture marker; a code collision with an unrelated document fails seeding.
The seed does not delete existing application data.

The k6 script loads the fixture into an in-memory `SharedArray` shared by VUs.
Its `setup()` validates the access token, verifies an intentional 409 and the
creation rate limit, then requests all 10,000 redirects in batches of 50 to verify
the database fixture and warm Redis. Setup traffic is excluded from the workload's
latency and error thresholds. Warm-up continues exercising the warmed cache.

## Run

1. Start MongoDB, Redis, and the API using the README setup. For worker analytics,
   use a MongoDB replica set and start the worker as described there.
2. Set `CREATE_LINK_RATE_LIMIT_PER_MINUTE=100000` in the API's environment and
   restart the API. The ordinary default remains **20 per user per minute**.
   Both successful and conflicting POSTs consume the rate limit. The preflight
   requires headroom of twice the peak create rate per minute: at 12,000 total
   RPS, this is 72,000. This override is intended for a dedicated benchmark deployment.
3. Register/sign in and copy a valid access token, as shown in the README.
4. From the project root, run:

```sh
export K6_ACCESS_TOKEN='paste-your-access-token-here'
docker compose --profile benchmark run --rm k6-benchmark
```

The API is reached at `http://host.docker.internal:8080`. The default seed database
is `shorts` on the Compose `db` service, with its configured root credentials.
The API and seed job must use the same database. The access token must remain
valid through setup and the full test.

| Variable | Default | Purpose |
| --- | --- | --- |
| `K6_BASE_URL` | `http://host.docker.internal:8080` | API address from the k6 container |
| `K6_ACCESS_TOKEN` | Required | Token used for measured POSTs and authentication checks |
| `K6_DB_NAME` | `shorts` | Seed database name; match the API's DB_NAME |
| `K6_MONGODB_URI` | Compose MongoDB | Optional seed connection string, reachable from its container |
| `K6_TARGET_URL` | `https://example.com` | Destination for seeded and newly created links |
| `K6_PEAK_RPS` | `12000` | Peak and soak arrival rate |
| `K6_PREALLOCATED_VUS` | `512` | VUs allocated before the measured scenario |
| `K6_MAX_VUS` | `4096` | Maximum available VUs |

The target must pass the API's URL validation. Creating links can resolve its
hostname, but the benchmark never follows redirects or fetches the target page.

If the database or target changes, rerun the seed job explicitly before the
benchmark so an earlier completed dependency cannot be reused:

```sh
docker compose --profile benchmark run --rm k6-seed
docker compose --profile benchmark run --rm k6-benchmark
```

Existing Redis fixture entries with a different destination will fail the setup
verification; clear those specific cache entries or use a fresh benchmark cache
before changing the target. Restore the normal creation limit after benchmarking.

## Results and verification

Terminal output includes HTTP rates/latencies, checks, dropped iterations, and:

- `shortener_load_operations`, tagged by bounded operation/kind, for the traffic mix.
- `shortener_load_conflicts`, counting expected 409 responses.
- `shortener_load_unexpected_responses`, counting failed response checks.

Thresholds require zero dropped iterations, zero unexpected response checks,
less than 1% failed measured HTTP requests, more than 99% successful measured
checks, a redirect p95 below 500 ms, and at least one verified conflict. Fixture
and setup validation failures stop the run before the measured stages.

At the default target, the schedule offers about **5.28 million operations**:
5.016 million redirects and 264,000 POSTs, including about 132,000 new links.
Setup redirects also publish click events. Monitor Redis memory, worker backlog,
the API's click queue failures, and database growth when evaluating the results.
Successful redirects alone do not prove that every asynchronous click was retained.
Seed documents have `benchmark_seed=shorts-k6-fixture-v1`; no automatic cleanup of
seeded or newly created links is performed.

Prometheus exposes application metrics independently of k6's terminal report.
See [Metrics](metrics.md) and [Observability checks](observability-checks.md) for
additional measurements. Docker/k6 integration and this high-load schedule still
require a manual live run; no Go commands or live traffic were executed by the agent.
