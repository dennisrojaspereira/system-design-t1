-- Plataforma de cursos — Aula 02. O Postgres é a fonte autoritativa:
-- catálogo, matrícula, versões de vídeo, fila de processamento e progresso.

-- ---------- catálogo e busca ----------
CREATE TABLE courses (
  id          serial PRIMARY KEY,
  title       text NOT NULL,
  description text NOT NULL,
  -- A busca começa aqui: tsvector + GIN. Motor separado só quando relevância,
  -- facetas ou carga justificarem o custo de manter um índice eventual.
  search      tsvector GENERATED ALWAYS AS
              (to_tsvector('portuguese', title || ' ' || description)) STORED
);
CREATE INDEX courses_search ON courses USING gin (search);

CREATE TABLE lessons (
  id              serial PRIMARY KEY,
  course_id       int  NOT NULL REFERENCES courses(id),
  position        int  NOT NULL,
  title           text NOT NULL,
  active_asset_id bigint,           -- ponteiro para a versão publicada
  search          tsvector GENERATED ALWAYS AS (to_tsvector('portuguese', title)) STORED,
  UNIQUE (course_id, position)
);
CREATE INDEX lessons_search ON lessons USING gin (search);

CREATE TABLE enrollments (
  user_id   text NOT NULL,
  course_id int  NOT NULL REFERENCES courses(id),
  PRIMARY KEY (user_id, course_id)
);

-- ---------- vídeo: cada upload vira uma versão (asset) da aula ----------
CREATE TABLE assets (
  id         bigserial PRIMARY KEY,
  lesson_id  int  NOT NULL REFERENCES lessons(id),
  version    int  NOT NULL,
  status     text NOT NULL DEFAULT 'UPLOADING',  -- UPLOADING → QUEUED → PROCESSING → READY | FAILED
  raw_key    text NOT NULL,                      -- original privado no bucket raw
  hls_prefix text,                               -- pacote publicado no bucket vod
  size_bytes bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  ready_at   timestamptz,
  UNIQUE (lesson_id, version)                    -- duas versões nunca têm a mesma identidade
);

CREATE TABLE uploads (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_id     bigint NOT NULL REFERENCES assets(id),
  s3_upload_id text   NOT NULL,
  part_size    bigint NOT NULL,
  size_bytes   bigint NOT NULL,
  status       text   NOT NULL DEFAULT 'OPEN',   -- OPEN → COMPLETED
  created_at   timestamptz NOT NULL DEFAULT now()
);

-- ---------- fila de processamento ----------
-- Uma tabela faz o papel da fila: entrega pelo menos uma vez, com lease.
-- A chave (asset, versão da fonte, versão do perfil) torna o enfileiramento idempotente.
CREATE TABLE jobs (
  id              bigserial PRIMARY KEY,
  asset_id        bigint NOT NULL REFERENCES assets(id),
  source_version  int    NOT NULL DEFAULT 1,
  profile_version int    NOT NULL DEFAULT 1,
  state           text   NOT NULL DEFAULT 'QUEUED',  -- QUEUED → PROCESSING → DONE | FAILED
  attempt         int    NOT NULL DEFAULT 0,
  attempt_id      uuid,                              -- identidade da tentativa ativa (fencing)
  worker          text,
  lease_until     timestamptz,
  run_after       timestamptz NOT NULL DEFAULT now(), -- backoff entre tentativas
  enqueued_at     timestamptz NOT NULL DEFAULT now(),
  started_at      timestamptz,
  finished_at     timestamptz,
  last_error      text,
  UNIQUE (asset_id, source_version, profile_version)
);
CREATE INDEX jobs_pending ON jobs (enqueued_at) WHERE state IN ('QUEUED', 'PROCESSING');

-- ---------- progresso: uma linha por aluno e aula ----------
CREATE TABLE progress (
  user_id    text NOT NULL,
  lesson_id  int  NOT NULL REFERENCES lessons(id),
  generation int  NOT NULL DEFAULT 0,   -- sobe a cada nova sessão de play
  seq        int  NOT NULL DEFAULT 0,   -- cresce a cada checkpoint da sessão
  pos_ms     int  NOT NULL DEFAULT 0,
  completed  boolean NOT NULL DEFAULT false,  -- conclusão é estado próprio, não posição
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, lesson_id)
);

-- ---------- dados iniciais ----------
INSERT INTO courses (title, description) VALUES
  ('System Design na Prática', 'Do MVP à escala: cache, filas, bancos de dados, CDN e streaming de vídeo.'),
  ('Go para APIs', 'Construindo serviços HTTP em Go com Postgres, testes e observabilidade.'),
  ('Postgres do zero ao índice', 'Modelagem, transações, índices GIN, busca textual e replicação.'),
  ('Carga do laboratório', 'Curso usado pelo botão de rajada para encher a fila de processamento.');

INSERT INTO lessons (course_id, position, title) VALUES
  (1, 1, 'Encurtador de links'),
  (1, 2, 'Plataforma de vídeo sob demanda'),
  (1, 3, 'Feed e notificações'),
  (2, 1, 'Roteamento e middlewares'),
  (2, 2, 'Pool de conexões e contexto'),
  (3, 1, 'Índices e planos de execução'),
  (3, 2, 'Busca textual com tsvector'),
  (4, 1, 'Aula de carga');

-- ana está matriculada em tudo; bia não comprou o curso 1.
INSERT INTO enrollments (user_id, course_id) VALUES
  ('ana', 1), ('ana', 2), ('ana', 3), ('ana', 4),
  ('bia', 2), ('bia', 3);
