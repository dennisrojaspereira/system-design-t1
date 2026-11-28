// Audiência assistindo a uma aula. Cada iteração é uma sessão de play:
//   POST /playback (API) → master.m3u8 → index.m3u8 → segmentos em sequência (borda)
//
//   make load SCRIPT=playback VIEWERS=50 DURATION=1m VARIANT=480p PACE=1
//
// PACE = segundos entre um segmento e o próximo. Um player real pede um
// segmento de 6 s a cada 6 s; o padrão acelera para o gráfico reagir logo.
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const API = __ENV.API || 'http://api:8080';
const EDGE = __ENV.EDGE || 'http://edge:8081';
const LESSON = __ENV.LESSON || '1';
const VARIANT = __ENV.VARIANT || '480p';
const PACE = Number(__ENV.PACE || 1);

const cacheHit = new Rate('cache_hit');
const ttfbHit = new Trend('segment_ttfb_hit', true);
const ttfbMiss = new Trend('segment_ttfb_miss', true);

export const options = {
  scenarios: {
    viewers: { executor: 'constant-vus', vus: Number(__ENV.VIEWERS || 20), duration: __ENV.DURATION || '1m' },
  },
};

let started = false;

export default function () {
  // Ninguém aperta o play no mesmo milissegundo: cada espectador entra num instante diferente.
  if (!started) {
    started = true;
    sleep(Math.random() * 5);
  }
  const pb = http.post(`${API}/api/lessons/${LESSON}/playback`, null, { headers: { 'X-User': 'ana' }, tags: { name: 'playback' } });
  if (!check(pb, { 'playback 200': (r) => r.status === 200 })) return sleep(1);
  const { manifestUrl, access } = pb.json();
  const base = EDGE + manifestUrl.replace('master.m3u8', '');
  // O cookie vai explícito: cada sessão tem o seu token, como no navegador.
  const params = (name) => ({
    headers: { Cookie: `vod_access=${access}` },
    tags: { name },
    responseType: name === 'segment' ? 'none' : 'text',
  });

  http.get(base + 'master.m3u8', params('playlist'));
  const index = http.get(`${base}${VARIANT}/index.m3u8`, params('playlist'));
  if (index.status !== 200) return sleep(1);
  const files = index.body.split('\n').filter((l) => l && !l.startsWith('#'));

  for (const f of files) {
    const res = http.get(`${base}${VARIANT}/${f}`, params('segment'));
    check(res, { 'segmento 200': (r) => r.status === 200 });
    const hit = res.headers['X-Cache'] === 'HIT';
    cacheHit.add(hit);
    (hit ? ttfbHit : ttfbMiss).add(res.timings.waiting);
    sleep(PACE);
  }
}
