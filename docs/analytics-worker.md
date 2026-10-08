# Analytics worker

The analytics worker consumes the stream configured by `REDIS_ANALYTICS_STREAM` through the `REDIS_ANALYTICS_CONSUMER_GROUP` consumer group. Each worker instance must have its own `REDIS_ANALYTICS_CONSUMER_NAME`.

## Raw events

`click_events` stores the Redis stream message ID as MongoDB `_id` and these event fields:

```json
{
  "code": "my-cool-project",
  "user_agent": "Mozilla/5.0 ...",
  "referer": "https://twitter.com/",
  "ip_hash": "sha256-hash",
  "timestamp": "2026-10-02T12:30:00Z"
}
```

The worker never stores the source IP address. Empty or missing referrers are stored as `direct`. The `{ code: 1, timestamp: -1 }` index supports per-link event queries.

Set `CLICK_EVENTS_RETENTION_DAYS` to the number of days raw events should be retained. A value of `0` disables raw-event expiration. When enabled, MongoDB creates a TTL index on `timestamp`.

## Daily summaries

`link_analytics_daily` contains one document per link and UTC calendar date. The worker normalizes referrers and browser families, groups each Redis batch by `{code, date}`, and applies one atomic `$inc` update per daily document. The unique `{ code: 1, date: 1 }` index prevents duplicate summary documents.

Known referrer categories include `direct`, `twitter`, `linkedin`, `facebook`, `instagram`, `reddit`, `youtube`, `google`, `bing`, and `yahoo`; unmatched hosts use `other`. Browser categories include `chrome`, `safari`, `edge`, `firefox`, `opera`, `internet_explorer`, `bot`, and `other`.

## Idempotency

The Redis stream message ID is the raw event's MongoDB `_id`. Raw writes use upserts with `$setOnInsert`, so a delivery can create its event only once. The raw upserts and daily `$inc` updates run in the same MongoDB transaction. Only IDs newly inserted by that transaction contribute to counters.

If processing or acknowledgment is retried after a successful commit, all raw event IDs already exist, no event is considered new, and no counters are incremented again. Redis messages are acknowledged only after the transaction commits successfully.

When raw-event TTL is enabled, configure the retention period to exceed the maximum Redis stream and pending-message retry horizon. This preserves the raw `_id` deduplication record for every message Redis can still redeliver.

This strategy requires MongoDB to run as a replica set or sharded cluster because standalone MongoDB deployments do not support multi-document transactions.

## Analytics API

`GET /api/v1/links/{code}/analytics` requires a valid access token and is rate limited per authenticated user. Only the link owner may view the response.

The optional `from` and `to` query parameters use inclusive UTC dates in `YYYY-MM-DD` format. With neither parameter, the endpoint returns the most recent 30 UTC calendar days. A request may cover at most `ANALYTICS_MAX_QUERY_DAYS` days.

```json
{
  "code": "my-cool-project",
  "from": "2026-10-01",
  "to": "2026-10-08",
  "total_clicks": 1420,
  "daily": [{"date": "2026-10-02", "total_clicks": 1420}],
  "referrers": {"twitter": 800, "linkedin": 400, "direct": 220},
  "browsers": {"chrome": 1100, "safari": 320}
}
```

The endpoint returns `404 Not Found` for an unknown link and `403 Forbidden` when the authenticated user does not own the link.
