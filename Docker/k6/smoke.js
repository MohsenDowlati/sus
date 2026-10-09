import http from 'k6/http';
import { check, sleep } from 'k6';

const baseURL = (__ENV.BASE_URL || 'http://host.docker.internal:8080').replace(/\/+$/, '');
const redirectCode = __ENV.REDIRECT_CODE || '';

export const options = {
  vus: 1,
  duration: '30s',
  thresholds: {
    checks: ['rate>0.99'],
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

export default function () {
  const health = http.get(`${baseURL}/healthz`, {
    tags: { name: 'GET /healthz' },
    timeout: '10s',
    responseCallback: http.expectedStatuses(200),
  });
  check(health, {
    'health returns 200': (response) => response.status === 200,
  });

  if (redirectCode) {
    const redirect = http.get(`${baseURL}/${encodeURIComponent(redirectCode)}`, {
      redirects: 0,
      tags: { name: 'GET /{code}' },
      timeout: '10s',
      responseCallback: http.expectedStatuses(307),
    });
    check(redirect, {
      'redirect returns 307': (response) => response.status === 307,
      'redirect includes Location': (response) => Boolean(response.headers.Location),
    });
  }

  sleep(1);
}
