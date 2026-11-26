-- Modelo de dados do MVP: uma tabela, chave = o próprio código curto.
CREATE TABLE links (
    code       TEXT PRIMARY KEY,
    url        TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    clicks     BIGINT      NOT NULL DEFAULT 0
);

-- Lab 2: contador central (1 nextval por link) e contador de blocos (1 nextval a cada 1000 links).
CREATE SEQUENCE link_id_seq;
CREATE SEQUENCE link_block_seq;

-- Lab 6 (modo async): log de cliques, só inserção em lote. Sem índice de
-- propósito — cada índice é custo a mais em toda escrita.
CREATE TABLE click_events (
    code TEXT        NOT NULL,
    at   TIMESTAMPTZ NOT NULL
);

-- Carga inicial: 100.000 links (s-1 ... s-100000) para os testes de leitura.
-- O hífen não existe no alfabeto base62, então nunca colidem com códigos gerados.
INSERT INTO links (code, url)
SELECT 's-' || i, 'https://example.com/pagina/' || i
FROM generate_series(1, 100000) AS i;
