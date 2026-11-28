// Aba "Arquitetura": diagramas C4 (contexto, containers, componentes, código) e
// fluxo de negócio, animados. Um seletor de estágio mostra como o desenho muda,
// do MVP à CDN, e o C2 exibe o estado ao vivo do laboratório.
(() => {
  const host = document.getElementById('tab-arq');
  const NS = 'http://www.w3.org/2000/svg';
  const C = { line: '#3A5068', ink: '#F2F5F7', ink2: '#A9B8C6', ink3: '#7F92A5', accent: '#FF8A3D', blue: '#5BB8FF', ok: '#7FDBA0', bad: '#FF6B6B', panel: '#16212E', field: '#0A1118' };
  const el = (tag, attrs = {}, parent) => {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
    if (parent) parent.appendChild(e);
    return e;
  };
  const state = { view: 'c2', stage: 6, auto: null, c3: 'worker', flow: 'entrar', step: 0, flowTimer: null, live: null };

  // ---------- estágios: como a arquitetura muda ----------
  const STAGES = [
    ['MVP: tudo pela API', 'Funciona com dez alunos. Um upload de 2 GB prende uma conexão da API por minutos, e cada play também.'],
    ['Upload direto', 'A API só assina URLs. O navegador envia as partes ao storage; se cair, retoma o que faltou. (Lab 1)'],
    ['Processamento assíncrono', 'Fila com lease e workers. O original vira um pacote pronto para tocar sem prender ninguém. (Lab 2)'],
    ['HLS + bitrate adaptativo', 'Três variantes em segmentos de 6 s. A rede piora e o player desce de qualidade em vez de travar. (Lab 3)'],
    ['CDN e acesso assinado', 'A borda guarda os segmentos perto do aluno e confere o cookie. A origem só vê os MISS. (Lab 4)'],
    ['Progresso entre dispositivos', 'Checkpoints com generation e seq: o aparelho antigo não sobrescreve o novo. (Lab 5)'],
  ];

  // Nó: [id, x, y, w, h, título, subtítulo, tipo, estágio em que aparece]
  // Aresta: { p: pontos, l: rótulo, heavy: bytes de vídeo, bad: gargalo, s: [de, até] }
  const C1 = {
    nodes: [
      ['prof', 60, 60, 190, 84, 'Professora', 'publica aulas', 'person', 1],
      ['aluno', 60, 400, 190, 84, 'Aluno', 'busca, assiste, retoma', 'person', 1],
      ['sys', 470, 215, 260, 120, 'Plataforma de cursos', 'catálogo, vídeo sob demanda, progresso', 'system', 1],
      ['idp', 495, 30, 210, 84, 'Provedor de identidade', 'login · 2FA · passkey', 'ext', 1],
      ['sto', 930, 60, 210, 84, 'Object storage', 'originais e pacotes de vídeo', 'ext', 2],
      ['cdn', 930, 400, 210, 84, 'CDN', 'entrega na borda', 'ext', 5],
    ],
    edges: [
      { p: [[250, 102], [470, 250]], l: 'envia vídeo de 2 GB', s: [1, 6] },
      { p: [[600, 215], [600, 114]], l: 'quem é esta pessoa?', s: [1, 6] },
      { p: [[250, 442], [470, 300]], l: 'busca · play · checkpoint', s: [1, 6] },
      { p: [[470, 320], [250, 460]], l: 'vídeo', heavy: 1, bad: 1, s: [1, 4] },
      { p: [[730, 250], [930, 102]], l: 'grava e lê objetos', heavy: 1, s: [2, 6] },
      { p: [[730, 300], [930, 442]], l: 'autoriza · origem no MISS', s: [5, 6] },
      { p: [[930, 462], [250, 462]], l: 'segmentos de vídeo', heavy: 1, s: [5, 6] },
    ],
  };
  const C2 = {
    nodes: [
      ['nav', 30, 235, 180, 78, 'Navegador / Player', 'web e mobile', 'person', 1],
      ['idp', 30, 50, 180, 78, 'IdP', 'login · 2FA · passkey', 'ext', 1],
      ['api', 400, 50, 180, 78, 'API', 'Go · plano de controle', 'box', 1],
      ['pg', 660, 50, 180, 78, 'Postgres', 'catálogo · fila · progresso', 'db', 1],
      ['raw', 400, 235, 180, 78, 'Storage raw', 'originais privados', 'db', 1],
      ['fila', 640, 235, 140, 78, 'Fila', 'tabela jobs · lease', 'box', 3],
      ['wk', 830, 235, 160, 78, 'Workers', 'ffmpeg · 1 CPU cada', 'box', 3],
      ['vod', 1030, 235, 150, 78, 'Storage VOD', 'pacotes versionados', 'db', 3],
      ['edge', 560, 430, 180, 78, 'Borda (CDN)', 'cache + cookie assinado', 'box', 5],
    ],
    edges: [
      { p: [[120, 235], [120, 128]], l: 'login', s: [1, 6] },
      { p: [[210, 89], [400, 89]], l: 'token: quem é', s: [1, 6] },
      { p: [[210, 255], [300, 255], [300, 110], [400, 110]], l: 'REST', s: [1, 5] },
      { p: [[210, 255], [300, 255], [300, 110], [400, 110]], l: 'REST + checkpoints', s: [6, 6] },
      { p: [[580, 89], [660, 89]], l: '', s: [1, 6] },
      { p: [[210, 295], [340, 295], [340, 150], [445, 150], [445, 128]], l: 'upload e MP4 pela API', heavy: 1, bad: 1, s: [1, 1] },
      { p: [[490, 128], [490, 235]], l: 'grava / lê', heavy: 1, bad: 1, s: [1, 1] },
      { p: [[210, 290], [400, 290]], l: 'partes · URL assinada', heavy: 1, s: [2, 6] },
      { p: [[490, 313], [490, 380], [120, 380], [120, 313]], l: 'MP4 direto da origem', heavy: 1, bad: 1, s: [2, 2] },
      { p: [[580, 274], [640, 274]], l: '', s: [3, 6] },
      { p: [[780, 274], [830, 274]], l: 'claim', s: [3, 6] },
      { p: [[990, 274], [1030, 274]], l: '', heavy: 1, s: [3, 6] },
      { p: [[910, 235], [910, 89], [840, 89]], l: 'READY condicional', s: [3, 6] },
      { p: [[1105, 313], [1105, 400], [120, 400], [120, 313]], l: 'MP4 direto da origem', heavy: 1, bad: 1, s: [3, 3] },
      { p: [[1105, 313], [1105, 400], [120, 400], [120, 313]], l: 'HLS direto da origem', heavy: 1, bad: 1, s: [4, 4] },
      { p: [[1105, 313], [1105, 469], [740, 469]], l: 'só no MISS', heavy: 1, s: [5, 6] },
      { p: [[560, 469], [120, 469], [120, 313]], l: 'segmentos · HIT', heavy: 1, s: [5, 6] },
    ],
  };

  // ---------- desenho ----------
  function drawGraph(svg, g, stage) {
    const labels = [];
    for (const e of g.edges) {
      if (stage < e.s[0] || stage > e.s[1]) continue;
      const fresh = e.s[0] === stage;
      const color = e.bad ? C.bad : e.heavy ? C.blue : C.ink3;
      const d = 'M' + e.p.map((q) => q.join(' ')).join(' L');
      el('path', { d, fill: 'none', stroke: color, 'stroke-width': e.heavy ? 4 : 2, 'stroke-dasharray': e.bad ? '10 7' : '', opacity: 0.85 }, svg);
      const [a, b] = e.p.slice(-2), ang = Math.atan2(b[1] - a[1], b[0] - a[0]);
      el('path', { d: `M${b[0]} ${b[1]} l${-12 * Math.cos(ang - 0.4)} ${-12 * Math.sin(ang - 0.4)} M${b[0]} ${b[1]} l${-12 * Math.cos(ang + 0.4)} ${-12 * Math.sin(ang + 0.4)}`, stroke: color, 'stroke-width': e.heavy ? 4 : 2, fill: 'none', 'stroke-linecap': 'round' }, svg);
      // bolinhas: bytes pesados andam devagar e em fila; chamadas pequenas passam rápido
      const len = e.p.slice(1).reduce((s, q, i) => s + Math.hypot(q[0] - e.p[i][0], q[1] - e.p[i][1]), 0);
      const n = e.heavy ? 3 : 1, dur = (e.heavy ? len / 90 : len / 260).toFixed(2);
      for (let i = 0; i < n; i++) {
        const c = el('circle', { r: e.heavy ? 7 : 4, fill: color }, svg);
        el('animateMotion', { path: d, dur: dur + 's', begin: (-(dur * i) / n).toFixed(2) + 's', repeatCount: 'indefinite' }, c);
      }
      if (e.l) {
        let best = 0, bi = 0; // rótulo no trecho mais longo
        e.p.slice(1).forEach((q, i) => { const l = Math.hypot(q[0] - e.p[i][0], q[1] - e.p[i][1]); if (l > best) { best = l; bi = i; } });
        const [p0, p1] = [e.p[bi], e.p[bi + 1]], horiz = Math.abs(p1[1] - p0[1]) < Math.abs(p1[0] - p0[0]);
        labels.push(() => { const t = el('text', { x: (p0[0] + p1[0]) / 2 + (horiz ? 0 : 10), y: (p0[1] + p1[1]) / 2 - (horiz ? 9 : 0), 'text-anchor': horiz ? 'middle' : 'start', 'font-size': 13, fill: fresh ? C.accent : color, 'font-family': 'ui-monospace, Consolas, monospace', 'paint-order': 'stroke', stroke: '#0F1720', 'stroke-width': 5 }, svg);
        t.textContent = e.l; });
      }
    }
    for (const [id, x, y, w, h, title, sub, kind, since] of g.nodes) {
      const on = stage >= since, fresh = stage === since && since > 1;
      const grp = el('g', { opacity: on ? 1 : 0.13, 'data-node': id }, svg);
      const stroke = fresh ? C.accent : kind === 'system' ? C.blue : kind === 'ext' ? C.ink3 : C.line;
      el('rect', { x, y, width: w, height: h, rx: kind === 'person' ? h / 2 : kind === 'db' ? 6 : 14, fill: kind === 'system' ? '#12304A' : C.panel, stroke, 'stroke-width': fresh ? 3 : 2, 'stroke-dasharray': kind === 'ext' ? '6 5' : '' }, grp);
      if (fresh) el('animate', { attributeName: 'stroke-opacity', values: '1;0.35;1', dur: '1.4s', repeatCount: 'indefinite' }, grp.lastChild);
      const t1 = el('text', { x: x + w / 2, y: y + h / 2 - 5, 'text-anchor': 'middle', 'font-size': 16, 'font-weight': 600, fill: C.ink }, grp); t1.textContent = title;
      const t2 = el('text', { x: x + w / 2, y: y + h / 2 + 16, 'text-anchor': 'middle', 'font-size': 12.5, fill: C.ink2 }, grp); t2.textContent = sub;
      if (fresh) { const n = el('text', { x: x + w - 8, y: y - 7, 'text-anchor': 'end', 'font-size': 12, fill: C.accent, 'font-family': 'ui-monospace, monospace' }, grp); n.textContent = 'NOVO'; }
      const live = el('text', { x: x + w / 2, y: y + h + 18, 'text-anchor': 'middle', 'font-size': 12.5, fill: C.ok, 'font-family': 'ui-monospace, monospace', 'data-live': id }, grp);
      live.textContent = '';
    }
    labels.forEach((f) => f());
  }

  // ---------- C3: componentes ----------
  const C3 = {
    api: { title: 'API: cada rota fala com quem precisa, e só', nodes: [
      ['a1', 60, 20, 250, 64, 'Catálogo e busca', 'GET /courses?q=', 'box', 1], ['a2', 60, 110, 250, 64, 'Uploads', 'sessão · partes · complete', 'box', 1],
      ['a3', 60, 200, 250, 64, 'Playback', 'matrícula → cookie assinado', 'box', 1], ['a4', 60, 290, 250, 64, 'Progresso', 'sessão · checkpoint', 'box', 1],
      ['a5', 60, 380, 250, 64, 'Fila (enfileirar)', 'job idempotente', 'box', 1],
      ['pg', 760, 120, 220, 78, 'Postgres', 'fonte autoritativa', 'db', 1], ['s3', 760, 330, 220, 78, 'Storage raw', 'multipart', 'db', 1]],
      edges: [
        { p: [[310, 52], [870, 52], [870, 120]], l: 'tsvector @@ websearch_to_tsquery', s: [1, 1] },
        { p: [[310, 130], [760, 140]], l: 'asset + sessão', s: [1, 1] },
        { p: [[310, 155], [690, 155], [690, 369], [760, 369]], l: 'assina URL · lista partes · completa', heavy: 1, s: [1, 1] },
        { p: [[310, 232], [560, 232], [560, 165], [760, 165]], l: 'matrícula + versão ativa', s: [1, 1] },
        { p: [[310, 322], [620, 322], [620, 188], [760, 188]], l: 'UPDATE … WHERE generation, seq', s: [1, 1] },
        { p: [[310, 412], [1040, 412], [1040, 160], [980, 160]], l: 'INSERT job ON CONFLICT DO NOTHING', s: [1, 1] }] },
    worker: { title: 'Worker: do claim à publicação, com a lease renovada por fora', nodes: [
      ['w1', 30, 60, 150, 70, '1 · Claim', 'SKIP LOCKED + lease', 'box', 1], ['w2', 225, 60, 150, 70, '2 · Download', 'original do raw', 'box', 1],
      ['w3', 420, 60, 150, 70, '3 · Probe', 'é vídeo mesmo?', 'box', 1], ['w4', 615, 60, 150, 70, '4 · Encode', '3 variantes · 6 s', 'box', 1],
      ['w5', 810, 60, 150, 70, '5 · Staging', 'prefixo da tentativa', 'box', 1], ['w6', 1005, 60, 150, 70, '6 · Validar', 'playlist × objetos', 'box', 1],
      ['w7', 1005, 250, 150, 70, '7 · Publicar', 'UPDATE condicional', 'box', 1], ['hb', 420, 250, 345, 70, 'Heartbeat', 'renova a lease; se perder, cancela tudo', 'box', 1],
      ['fail', 30, 250, 250, 70, 'Falhou?', 'backoff → nova tentativa → FAILED', 'ext', 1]],
      edges: [
        { p: [[180, 95], [225, 95]], l: '', s: [1, 1] }, { p: [[375, 95], [420, 95]], l: '', heavy: 1, s: [1, 1] }, { p: [[570, 95], [615, 95]], l: '', s: [1, 1] },
        { p: [[765, 95], [810, 95]], l: '', heavy: 1, s: [1, 1] }, { p: [[960, 95], [1005, 95]], l: '', s: [1, 1] }, { p: [[1080, 130], [1080, 250]], l: 'íntegro', s: [1, 1] },
        { p: [[590, 250], [590, 130]], l: 'a cada lease ÷ 3', s: [1, 1] }, { p: [[1005, 300], [765, 300]], l: 'attempt_id confere?', s: [1, 1] },
        { p: [[420, 300], [280, 300]], l: 'erro', bad: 1, s: [1, 1] }] },
    edge: { title: 'Borda: autoriza pelo cookie, identifica pelo caminho', nodes: [
      ['e1', 30, 150, 160, 70, '1 · Cookie', 'assinatura + prazo', 'box', 1], ['e2', 240, 150, 170, 70, '2 · Chave', 'o caminho, sem o token', 'box', 1],
      ['e3', 460, 150, 150, 70, '3 · Cache', 'HIT ou MISS?', 'box', 1], ['e4', 660, 290, 190, 70, '4 · Junta pedidos', 'request collapsing', 'box', 1],
      ['e5', 900, 290, 170, 70, '5 · Origem', 'bucket privado', 'db', 1], ['e6', 900, 150, 170, 70, '6 · Entrega', 'X-Cache · banda', 'box', 1],
      ['no', 30, 290, 160, 70, '403', 'sem acesso válido', 'ext', 1]],
      edges: [
        { p: [[190, 185], [240, 185]], l: '', s: [1, 1] }, { p: [[410, 185], [460, 185]], l: '', s: [1, 1] }, { p: [[610, 185], [900, 185]], l: 'HIT · 1 ms', heavy: 1, s: [1, 1] },
        { p: [[535, 220], [535, 325], [660, 325]], l: 'MISS', s: [1, 1] }, { p: [[850, 325], [900, 325]], l: '', s: [1, 1] },
        { p: [[985, 290], [985, 220]], l: 'guarda e entrega', heavy: 1, s: [1, 1] }, { p: [[110, 220], [110, 290]], l: 'inválido', bad: 1, s: [1, 1] }] },
  };

  // ---------- C4: o código que sustenta cada decisão ----------
  const CODE = [
    ['Claim com lease', 'app/worker.go', ['SELECT id FROM jobs', "WHERE (state = 'QUEUED' AND run_after <= now())", "   OR (state = 'PROCESSING' AND lease_until < now())  ◀ lease venceu", 'ORDER BY enqueued_at', 'FOR UPDATE SKIP LOCKED LIMIT 1   ◀ um job por worker']],
    ['Publicação condicional', 'app/worker.go', ["UPDATE jobs SET state = 'DONE', finished_at = now()", 'WHERE id = $1', "  AND attempt_id = $2   ◀ a tentativa antiga perde", "  AND state = 'PROCESSING'", '-- na mesma transação: asset READY + ponteiro da aula']],
    ['Checkpoint de progresso', 'app/api.go', ['UPDATE progress SET pos_ms = $3, seq = $5', 'WHERE user_id = $1 AND lesson_id = $2', '  AND generation = $4   ◀ só a sessão atual', '  AND seq < $5          ◀ só para frente']],
    ['Chave de cache', 'app/edge.go', ['ck := key                    ◀ igual para todos', 'if e.cacheKey == "path+cookie" {', '    ck = key + "|" + token   ◀ o erro: uma por sessão', '}', 'ent := e.cache[ck]']],
  ];

  // ---------- fluxo de negócio: as quatro jornadas ----------
  const FLOWS = {
    entrar: { name: 'Entrar', actors: ['Aluno', 'Navegador', 'IdP', 'API', 'Postgres'], steps: [
      [0, 1, 'abre a plataforma'], [1, 2, 'redireciona para o login'], [0, 2, 'senha + segundo fator, ou passkey (biometria)'],
      [2, 1, 'token assinado: "esta é a ana", com prazo'], [1, 3, 'chamadas com o token'], [3, 3, 'confere a assinatura; não vê senha nem biometria'],
      [3, 4, 'ana pode ver este curso? (matrícula)'], [3, 1, 'catálogo da ana']] },
    publicar: { name: 'Publicar', actors: ['Professora', 'Navegador', 'API', 'Postgres', 'Storage', 'Workers'], steps: [
      [0, 1, 'escolhe o vídeo de 2 GB'], [1, 2, 'POST /uploads'], [2, 3, 'cria a versão: asset UPLOADING'], [2, 1, 'uploadId + URLs assinadas'],
      [1, 4, 'PUT parte × N, direto ao storage', 1], [1, 2, 'POST /complete'], [2, 3, 'asset QUEUED + job, na mesma transação'],
      [5, 3, 'claim do job, com lease'], [5, 4, 'baixa o original, grava o pacote HLS', 1], [5, 3, 'READY + ponteiro da aula, se a tentativa ainda for a dona'], [2, 0, 'aula publicada']] },
    encontrar: { name: 'Encontrar', actors: ['Aluno', 'Navegador', 'API', 'Postgres'], steps: [
      [0, 1, 'digita "streaming de vídeo"'], [1, 2, 'GET /courses?q=…'], [2, 3, 'search @@ websearch_to_tsquery(q)'], [3, 2, 'cursos, por ts_rank'], [2, 1, 'lista + matrícula do aluno'], [1, 0, 'resultado na tela']] },
    assistir: { name: 'Assistir', actors: ['Aluno', 'Player', 'API', 'Postgres', 'Borda', 'Storage VOD'], steps: [
      [0, 1, 'clica em play'], [1, 2, 'POST /lessons/1/playback'], [2, 3, 'tem matrícula? qual é a versão ativa?'], [2, 1, 'manifest + cookie assinado'],
      [1, 4, 'GET master.m3u8, index.m3u8'], [4, 5, 'MISS: busca na origem', 1], [4, 1, 'segmento de 6 s; os próximos alunos recebem HIT', 1], [1, 1, 'mede a banda e escolhe a próxima variante'], [1, 0, 'primeiro quadro em menos de 2,5 s']] },
    retomar: { name: 'Retomar', actors: ['Celular', 'Notebook', 'API', 'Postgres'], steps: [
      [0, 2, 'play: abre sessão'], [2, 3, 'generation = 1'], [0, 2, 'checkpoint 11:00 (seq 1)'], [1, 2, 'play no notebook: abre sessão'], [2, 3, 'generation = 2'], [2, 1, 'retoma em 11:00'],
      [1, 2, 'voltou para 4:20 (gen 2, seq 1)'], [0, 2, 'checkpoint atrasado: 11:00 (gen 1)', 0, 1], [2, 0, '409: sua sessão venceu', 0, 1], [3, 3, 'posição continua em 4:20']] },
  };

  function drawFlow(svg) {
    const f = FLOWS[state.flow], n = f.actors.length, x = (i) => 110 + (i * 980) / (n - 1);
    f.actors.forEach((a, i) => {
      el('line', { x1: x(i), y1: 52, x2: x(i), y2: 548, stroke: C.line, 'stroke-width': 2, 'stroke-dasharray': '4 6' }, svg);
      el('rect', { x: x(i) - 80, y: 10, width: 160, height: 40, rx: 10, fill: C.panel, stroke: C.line, 'stroke-width': 2 }, svg);
      const t = el('text', { x: x(i), y: 36, 'text-anchor': 'middle', 'font-size': 15, 'font-weight': 600, fill: C.ink }, svg); t.textContent = a;
    });
    f.steps.forEach(([from, to, label, heavy, bad], i) => {
      if (i > state.step) return;
      const y = 86 + i * 43, cur = i === state.step, color = bad ? C.bad : heavy ? C.blue : cur ? C.accent : C.ink3;
      const g = el('g', { opacity: cur ? 1 : 0.55 }, svg);
      const x1 = x(from), x2 = from === to ? x(from) + 46 : x(to), dir = x2 > x1 ? 1 : -1;
      const d = from === to ? `M${x1} ${y - 8} h46 v16 h-40` : `M${x1} ${y} L${x2 - dir * 4} ${y}`;
      el('path', { d, fill: 'none', stroke: color, 'stroke-width': heavy ? 4 : 2 }, g);
      const tip = from === to ? [x1 + 6, y + 8, -1] : [x2 - dir * 4, y, dir];
      el('path', { d: `M${tip[0]} ${tip[1]} l${-10 * tip[2]} -6 M${tip[0]} ${tip[1]} l${-10 * tip[2]} 6`, stroke: color, 'stroke-width': heavy ? 4 : 2, fill: 'none', 'stroke-linecap': 'round' }, g);
      const t = el('text', { x: from === to ? x1 + 58 : (x1 + x2) / 2, y: y - (from === to ? -4 : 8), 'text-anchor': from === to ? 'start' : 'middle', 'font-size': 13.5, fill: cur ? C.ink : C.ink2, 'paint-order': 'stroke', stroke: '#0F1720', 'stroke-width': 5 }, g);
      t.textContent = `${i + 1}. ${label}`;
      if (cur) { const c = el('circle', { r: heavy ? 8 : 5, fill: color }, g); el('animateMotion', { path: d, dur: heavy ? '1.6s' : '0.8s', repeatCount: 'indefinite' }, c); }
    });
  }

  // ---------- montagem ----------
  const seg = (id, items, cur) => `<div class="seg" id="${id}">${items.map(([k, l]) => `<button data-k="${k}" aria-pressed="${k === cur}">${l}</button>`).join('')}</div>`;
  function render() {
    const v = state.view, stageful = v === 'c1' || v === 'c2';
    let controls = '', caption = '';
    if (stageful) {
      controls = `<div class="row"><button class="btn sm" id="arqAuto">${state.auto ? '❚❚ pausar' : '▶ ver evoluir'}</button>${seg('arqStage', STAGES.map((s, i) => [i + 1, i + 1]), state.stage)}</div>`;
      caption = `<p class="hint"><b>Estágio ${state.stage}: ${STAGES[state.stage - 1][0]}.</b> ${STAGES[state.stage - 1][1]}</p>`;
    } else if (v === 'c3') {
      controls = seg('arqC3', [['api', 'API'], ['worker', 'Worker'], ['edge', 'Borda']], state.c3);
      caption = `<p class="hint"><b>${C3[state.c3].title}.</b> Cada caixa é um trecho de <span class="m">app/${state.c3 === 'api' ? 'api' : state.c3}.go</span>.</p>`;
    } else if (v === 'fluxo') {
      const f = FLOWS[state.flow];
      controls = `<div class="row">${seg('arqFlow', Object.entries(FLOWS).map(([k, f]) => [k, f.name]), state.flow)}<button class="btn sm" id="flowPlay">${state.flowTimer ? '❚❚ pausar' : '▶ reproduzir'}</button><button class="btn ghost sm" id="flowNext">próximo passo</button><span class="muted">passo ${state.step + 1} de ${f.steps.length}</span></div>`;
      caption = `<p class="hint">Linha grossa azul = bytes de vídeo. Repare por onde eles <b>não</b> passam.</p>`;
    }
    host.innerHTML = `
      <div class="row"><span class="tag">Arquitetura</span>
        ${seg('arqView', [['c1', 'C1 · Contexto'], ['c2', 'C2 · Containers'], ['c3', 'C3 · Componentes'], ['c4', 'C4 · Código'], ['fluxo', 'Fluxo de negócio']], v)}
        <span class="grow"></span>${controls}</div>
      ${caption}
      ${v === 'c4' ? `<div class="grid2">${CODE.map(([t, f, lines], i) => `<div class="course"><div class="row"><h2 class="grow">${t}</h2><span class="pill">${f}</span></div><div class="log" style="max-height:none;white-space:pre;overflow-x:auto">${lines.map((l, j) => `<div class="cl" data-c="${i}" data-l="${j}" style="${l.includes('◀') ? '' : 'color:var(--ink3)'}">${l.replace(/&/g, '&amp;').replace(/</g, '&lt;')}</div>`).join('')}</div></div>`).join('')}</div>` : '<div id="arqSvg"></div>'}
      ${v === 'c2' ? '<p class="muted">Linha azul = bytes de vídeo · tracejado vermelho = gargalo que o próximo estágio resolve · números em verde = estado do laboratório agora.</p>' : ''}`;
    if (v !== 'c4') {
      const svg = el('svg', { viewBox: v === 'c3' && state.c3 !== 'api' ? '0 0 1200 400' : '0 0 1200 560', width: '100%', style: 'background:#0A1118;border:1px solid #263648;border-radius:12px' });
      host.querySelector('#arqSvg').appendChild(svg);
      if (v === 'c1') drawGraph(svg, C1, state.stage);
      if (v === 'c2') { drawGraph(svg, C2, state.stage); paintLive(); }
      if (v === 'c3') drawGraph(svg, C3[state.c3], 1);
      if (v === 'fluxo') drawFlow(svg);
    }
  }

  // Estado ao vivo sobre o C2: a fila, os workers e a borda de verdade.
  function paintLive() {
    if (state.view !== 'c2' || !state.live) return;
    const { q, e, busy } = state.live, set = (id, txt) => { const t = host.querySelector(`[data-live="${id}"]`); if (t) t.textContent = txt; };
    set('fila', `${q.queued} na fila · ${Math.round(q.oldestAgeS)} s`);
    set('wk', `${busy} processando`);
    set('vod', `${q.done} pacotes prontos`);
    set('edge', `${e.entries} objetos · chave ${e.cacheKey}`);
    set('pg', q.failed ? `${q.failed} job(s) FAILED` : '');
  }
  async function poll() {
    if (host.hidden || state.view !== 'c2') return;
    try {
      const [p, e] = await Promise.all([fetch('/api/pipeline').then((r) => r.json()), fetch('/edge/admin/state').then((r) => r.json())]);
      state.live = { q: p.queue, e, busy: p.queue.processing };
      paintLive();
    } catch {}
  }
  setInterval(poll, 2000);

  const stopTimers = () => { clearInterval(state.auto); clearInterval(state.flowTimer); state.auto = state.flowTimer = null; };
  host.addEventListener('click', (ev) => {
    const b = ev.target.closest('button'); if (!b) return;
    const k = b.dataset.k, group = b.parentElement.id;
    if (group === 'arqView') { stopTimers(); state.view = k; }
    if (group === 'arqStage') { stopTimers(); state.stage = Number(k); }
    if (group === 'arqC3') state.c3 = k;
    if (group === 'arqFlow') { stopTimers(); state.flow = k; state.step = 0; }
    if (b.id === 'arqAuto') {
      if (state.auto) stopTimers();
      else { state.stage = 1; state.auto = setInterval(() => { if (state.stage >= 6) stopTimers(); else state.stage++; render(); poll(); }, 3500); }
    }
    if (b.id === 'flowNext') { stopTimers(); state.step = (state.step + 1) % FLOWS[state.flow].steps.length; }
    if (b.id === 'flowPlay') {
      if (state.flowTimer) stopTimers();
      else { state.step = 0; state.flowTimer = setInterval(() => { if (state.step >= FLOWS[state.flow].steps.length - 1) stopTimers(); else state.step++; render(); }, 1700); }
    }
    render(); poll();
  });
  // C4: o destaque passeia pelas linhas que carregam a decisão.
  let tick = 0;
  setInterval(() => {
    if (host.hidden || state.view !== 'c4') return;
    tick++;
    host.querySelectorAll('.cl').forEach((l) => {
      const keys = [...host.querySelectorAll(`.cl[data-c="${l.dataset.c}"]`)].filter((x) => x.textContent.includes('◀'));
      const on = keys[tick % keys.length] === l;
      l.style.background = on ? 'rgba(255,138,61,.16)' : ''; l.style.color = on ? '#F2F5F7' : l.textContent.includes('◀') ? '#A9B8C6' : '#7F92A5';
    });
  }, 1400);

  window.renderArq = () => { render(); poll(); };
})();
