// Checkpoints de progresso: VIEWERS alunos, um checkpoint a cada INTERVAL segundos.
//
//   make load SCRIPT=progress VIEWERS=600 INTERVAL=10 JITTER=false
//   make load SCRIPT=progress VIEWERS=600 INTERVAL=10 JITTER=true
//
// JITTER=false: todos os players disparam no mesmo instante (relógio de parede).
// JITTER=true: cada player sorteia o seu deslocamento dentro do intervalo.
// A carga média é a mesma; o pico, não.
import http from 'k6/http';
import { check, sleep } from 'k6';

const API = __ENV.API || 'http://api:8080';
const LESSON = __ENV.LESSON || '1';
const INTERVAL = Number(__ENV.INTERVAL || 10);
const JITTER = (__ENV.JITTER || 'false') === 'true';

export const options = {
  scenarios: {
    heartbeats: { executor: 'constant-vus', vus: Number(__ENV.VIEWERS || 300), duration: __ENV.DURATION || '1m' },
  },
};

let session = null; // estado de cada VU: a sessão aberta no "play"
let offset = 0;

export default function () {
  const headers = { 'X-User': `k6-${__VU}`, 'Content-Type': 'application/json' };
  if (!session) {
    const res = http.post(`${API}/api/lessons/${LESSON}/progress/session`, null, { headers, tags: { name: 'session' } });
    session = { generation: res.json('generation'), seq: 0, pos: 0 };
    offset = JITTER ? Math.random() * INTERVAL : 0;
  }
  // Dorme até o próximo "tique" do intervalo, mais o deslocamento deste player.
  const now = Date.now() / 1000;
  sleep(INTERVAL - (now % INTERVAL) + offset);

  session.seq++;
  session.pos += INTERVAL * 1000;
  const res = http.put(
    `${API}/api/lessons/${LESSON}/progress`,
    JSON.stringify({ generation: session.generation, seq: session.seq, positionMs: session.pos }),
    { headers, tags: { name: 'checkpoint' } },
  );
  check(res, { 'checkpoint 204': (r) => r.status === 204 });
}
