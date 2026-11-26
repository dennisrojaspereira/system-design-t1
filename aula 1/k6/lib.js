// Configuração comum dos testes de carga. Tudo vem de variável de ambiente:
//   make load SCRIPT=read RPS=800 DURATION=2m SKEW=1
export const TARGET = __ENV.TARGET || 'http://app1:8080';

// O banco já nasce com 100.000 links: s-1 ... s-100000 (db/01-schema.sql).
const LINKS = Number(__ENV.LINKS || 100000);

// SKEW controla a popularidade: 1 = todos os links igualmente acessados;
// 8 (padrão) = poucos links concentram quase todo o tráfego, como na vida real.
const SKEW = Number(__ENV.SKEW || 8);

export function pickCode() {
  return 's-' + (Math.floor(LINKS * Math.pow(Math.random(), SKEW)) + 1);
}

// `name` fixo evita uma série de métrica por URL diferente.
export const redirectParams = { redirects: 0, timeout: '3s', tags: { name: 'redirect' } };
export const createParams = {
  headers: { 'Content-Type': 'application/json' },
  timeout: '3s',
  tags: { name: 'create' },
};
