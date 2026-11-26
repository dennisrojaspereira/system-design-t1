// Tráfego misto (Lab 5): leituras de links antigos + "criei e já cliquei".
// Conta quantas vezes o link recém-criado ainda não existe na leitura
// (stale read): o sintoma clássico de lag de replicação.
import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';
import { TARGET, pickCode, redirectParams, createParams } from './lib.js';

const fresh = new Counter('read_after_write');
const stale = new Counter('stale_reads');

const duration = __ENV.DURATION || '2m';

export const options = {
  scenarios: {
    redirects: {
      executor: 'constant-arrival-rate',
      exec: 'read',
      rate: Number(__ENV.RPS || 300),
      timeUnit: '1s',
      duration,
      preAllocatedVUs: 100,
      maxVUs: 2000,
    },
    create_then_click: {
      executor: 'constant-arrival-rate',
      exec: 'createThenClick',
      rate: Number(__ENV.WRITE_RPS || 20),
      timeUnit: '1s',
      duration,
      preAllocatedVUs: 50,
      maxVUs: 500,
    },
  },
};

export function read() {
  const res = http.get(`${TARGET}/${pickCode()}`, redirectParams);
  check(res, { 'redirect 302': (r) => r.status === 302 });
}

export function createThenClick() {
  const body = JSON.stringify({ url: `https://example.com/novo/${__VU}-${__ITER}` });
  const created = http.post(`${TARGET}/api/links`, body, createParams);
  if (!check(created, { 'criado 201': (r) => r.status === 201 })) return;

  const res = http.get(`${TARGET}/${created.json('code')}`, {
    redirects: 0,
    timeout: '3s',
    tags: { name: 'redirect_fresh' },
  });
  fresh.add(1);
  if (res.status === 404) stale.add(1);
}
