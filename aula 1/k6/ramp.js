// Rampa: sobe de degrau em degrau até MAX_RPS para achar o "joelho" —
// o ponto em que a latência dispara e os erros começam.
import http from 'k6/http';
import { check } from 'k6';
import { TARGET, pickCode, redirectParams } from './lib.js';

const MAX = Number(__ENV.MAX_RPS || 3000);
const STEPS = Number(__ENV.STEPS || 6);
const STEP = __ENV.STEP_DURATION || '30s';

const stages = [];
for (let i = 1; i <= STEPS; i++) {
  const target = Math.round((MAX * i) / STEPS);
  stages.push({ target, duration: '5s' }); // sobe
  stages.push({ target, duration: STEP }); // sustenta
}

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: Math.round(MAX / STEPS),
      timeUnit: '1s',
      stages,
      preAllocatedVUs: 300,
      maxVUs: 4000,
    },
  },
};

export default function () {
  const res = http.get(`${TARGET}/${pickCode()}`, redirectParams);
  check(res, { 'redirect 302': (r) => r.status === 302 });
}
