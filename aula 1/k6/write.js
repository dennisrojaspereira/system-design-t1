// Escrita constante: RPS criações de link por segundo (Lab 2).
import http from 'k6/http';
import { check } from 'k6';
import { TARGET, createParams } from './lib.js';

export const options = {
  scenarios: {
    creates: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RPS || 200),
      timeUnit: '1s',
      duration: __ENV.DURATION || '2m',
      preAllocatedVUs: 100,
      maxVUs: 2000,
    },
  },
};

export default function () {
  const body = JSON.stringify({ url: `https://example.com/artigo/${__VU}-${__ITER}` });
  const res = http.post(`${TARGET}/api/links`, body, createParams);
  check(res, { 'criado 201': (r) => r.status === 201 });
}
