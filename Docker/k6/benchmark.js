import http from 'k6/http';
import { check, fail } from 'k6';
import { SharedArray } from 'k6/data';
import execution from 'k6/execution';
import { Counter } from 'k6/metrics';

const baseURL = (__ENV.BASE_URL || 'http://host.docker.internal:8080').replace(/\/+$/, '');
const targetURL = __ENV.ORIGINAL_URL || 'https://example.com';
const peakRPS = positiveInteger('PEAK_RPS', 12000);
const preallocatedVUs = positiveInteger('PREALLOCATED_VUS', 512);
const maxVUs = positiveInteger('MAX_VUS', 4096);
if (peakRPS < 10000) throw new Error('PEAK_RPS must be at least 10,000');
if (maxVUs < preallocatedVUs) throw new Error('MAX_VUS must be at least PREALLOCATED_VUS');

// Constructed once per k6 instance, shared in memory across VUs; no per-VU
// setup-data copies of the 10,000-link fixture are needed.
const pool = new SharedArray('shorts-seeded-links', () => {
  const entries = JSON.parse(open('./seed-pool.json'));
  if (entries.length !== 10000 || new Set(entries.map((link) => link.code)).size !== 10000) {
    throw new Error('Expected 10,000 unique seeded links');
  }
  if (entries.slice(0, 5000).some((link) => link.type !== 'custom') ||
      entries.slice(5000).some((link) => link.type !== 'auto' || !/^[0-9A-Za-z]{7}$/.test(link.code))) {
    throw new Error('Seed pool must contain 5,000 custom codes followed by 5,000 Base62 codes');
  }
  return entries;
});

const operations = new Counter('shortener_load_operations');
const conflicts = new Counter('shortener_load_conflicts');
const unexpectedResponses = new Counter('shortener_load_unexpected_responses');

export const options = {
  discardResponseBodies: true,
  // Keep request URLs, dynamic slugs, VU IDs, and iteration IDs out of tags.
  systemTags: ['status', 'method', 'name', 'check', 'scenario', 'expected_response', 'error_code'],
  setupTimeout: '10m',
  batch: 50,
  batchPerHost: 50,
  scenarios: {
    traffic: {
      executor: 'ramping-arrival-rate',
      startRate: 0,
      timeUnit: '1s',
      preAllocatedVUs: preallocatedVUs,
      maxVUs,
      gracefulStop: '30s',
      stages: [
        { duration: '1m', target: 2000 },
        { duration: '3m', target: peakRPS },
        { duration: '5m', target: peakRPS },
        { duration: '1m', target: 0 },
      ],
    },
  },
  thresholds: {
    dropped_iterations: ['count==0'],
    'http_req_failed{scenario:traffic}': ['rate<0.01'],
    'checks{scenario:traffic}': ['rate>0.99'],
    'http_req_duration{scenario:traffic,operation:redirect}': ['p(95)<500'],
    shortener_load_unexpected_responses: ['count==0'],
    shortener_load_conflicts: ['count>0'],
  },
};

function positiveInteger(name, fallback) {
  const value = Number(__ENV[name] || fallback);
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${name} must be a positive integer`);
  return value;
}

export function setup() {
  const token = __ENV.ACCESS_TOKEN;
  if (!token) fail('Set K6_ACCESS_TOKEN to a valid access token before running the benchmark');
  const headers = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' };
  const identity = http.get(`${baseURL}/api/v1/auth/me`, {
    headers, timeout: '10s', tags: { name: 'GET /api/v1/auth/me', phase: 'setup' },
  });
  if (identity.status !== 200) fail(`Benchmark authentication failed (HTTP ${identity.status})`);

  const duplicate = http.post(`${baseURL}/api/v1/links`, JSON.stringify({
    original_url: targetURL, type: 'custom', code: pool[0].code,
  }), {
    headers, timeout: '10s',
    tags: { name: 'POST /api/v1/links', phase: 'setup' },
    responseCallback: http.expectedStatuses(409),
  });
  if (duplicate.status !== 409) fail(`Duplicate preflight expected 409, received ${duplicate.status}`);
  const limit = Number(duplicate.headers['X-Ratelimit-Limit']);
  const requiredLimit = Math.ceil(peakRPS * 0.05 * 60 * 2);
  if (!Number.isFinite(limit) || limit < requiredLimit) {
    fail(`Restart API with CREATE_LINK_RATE_LIMIT_PER_MINUTE >= ${requiredLimit} for this benchmark`);
  }

  // The Compose seed dependency inserts this fixture directly into MongoDB.
  // Verify every code and prewarm its Redis cache before measuring traffic.
  for (let offset = 0; offset < pool.length; offset += 50) {
    const requests = [];
    for (let index = offset; index < Math.min(offset + 50, pool.length); index += 1) {
      requests.push({
        method: 'GET', url: `${baseURL}/${pool[index].code}`,
        params: {
          redirects: 0, timeout: '10s',
          tags: { name: 'GET /{code}', phase: 'setup' },
          responseCallback: http.expectedStatuses(307),
        },
      });
    }
    const responses = http.batch(requests);
    if (responses.some((response) => response.status !== 307 || response.headers.Location !== targetURL)) {
      fail('Seed pool verification failed: check API database settings and stale Redis cache entries');
    }
  }

  console.log(`Verified and warmed ${pool.length} seeded links; starting 95/5 traffic at peak ${peakRPS} RPS`);
  return { token };
}

export default function (data) {
  // One HTTP operation per iteration: nineteen redirects followed by one
  // creation. Global iteration indices preserve the split across VUs.
  const iteration = execution.scenario.iterationInTest;
  unexpectedResponses.add(0);
  if (iteration % 20 < 19) {
    const ordinal = iteration - Math.floor(iteration / 20);
    const link = pool[(ordinal * 7919) % pool.length];
    const response = http.get(`${baseURL}/${link.code}`, {
      redirects: 0, timeout: '10s',
      tags: { name: 'GET /{code}', operation: 'redirect', kind: link.type },
      responseCallback: http.expectedStatuses(307),
    });
    operations.add(1, { operation: 'redirect', kind: link.type });
    const valid = check(response, {
      'redirect returns 307': (result) => result.status === 307,
      'redirect has expected Location': (result) => result.headers.Location === targetURL,
    });
    if (!valid) unexpectedResponses.add(1);
    return;
  }

  // Half of POSTs create fresh automatic links; half deliberately conflict
  // with existing custom codes. Expected 409s count as successful requests.
  const createOrdinal = Math.floor(iteration / 20);
  const duplicate = createOrdinal % 2 === 1;
  const payload = { original_url: targetURL, type: duplicate ? 'custom' : 'auto' };
  if (duplicate) payload.code = pool[Math.floor(createOrdinal / 2) % 5000].code;
  const expectedStatus = duplicate ? 409 : 201;
  const response = http.post(`${baseURL}/api/v1/links`, JSON.stringify(payload), {
    headers: { Authorization: `Bearer ${data.token}`, 'Content-Type': 'application/json' },
    timeout: '10s',
    tags: { name: 'POST /api/v1/links', operation: 'create', kind: duplicate ? 'duplicate' : 'auto' },
    responseCallback: http.expectedStatuses(expectedStatus),
  });
  operations.add(1, { operation: 'create', kind: duplicate ? 'duplicate' : 'auto' });
  conflicts.add(duplicate && response.status === 409 ? 1 : 0);
  if (!check(response, { 'create returns expected status': (result) => result.status === expectedStatus })) {
    unexpectedResponses.add(1);
  }
}
