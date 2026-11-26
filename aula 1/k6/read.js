// Leitura constante: RPS redirects por segundo durante DURATION.
import http from 'k6/http';
import { check } from 'k6';
import { TARGET, pickCode, redirectParams } from './lib.js';

export const options = {
  scenarios: {
    redirects: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RPS || 200),
      timeUnit: '1s',
      duration: __ENV.DURATION || '2m',
      preAllocatedVUs: 200,
      maxVUs: 3000,
    },
  },
};

export default function () {
  const res = http.get(`${TARGET}/${pickCode()}`, redirectParams);
  check(res, { 'redirect 302': (r) => r.status === 302 });
}
