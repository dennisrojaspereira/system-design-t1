// API: o plano de controle. Atende chamadas pequenas e rápidas — catálogo,
// busca, sessão de upload, autorização de play e checkpoints de progresso.
//
//	PROGRESS_MODE   conditional | lww   escrita condicional × "última escrita vence" (Lab 5)
//	PART_SIZE_MB    tamanho da parte do upload multipart                          (Lab 1)
//	PLAYBACK_TTL_S  validade do acesso temporário ao vídeo                        (Lab 4)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ---------- métricas ----------

var (
	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "Latência da API por rota e status.",
		Buckets: []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"route", "status"})

	uploadsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "uploads_total", Help: "Sessões de upload por evento.",
	}, []string{"event"}) // created | completed | complete_repeated | reconciled

	playbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "playback_requests_total", Help: "Pedidos de play por resultado.",
	}, []string{"result"}) // ok | forbidden | not_ready

	progressWrites = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "progress_writes_total", Help: "Checkpoints por resultado.",
	}, []string{"result"}) // accepted | stale

	playerStartup = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "player_startup_seconds", Help: "Clique no play até o primeiro quadro, medido no player.",
		Buckets: []float64{.1, .25, .5, .75, 1, 1.5, 2, 2.5, 3, 4, 5, 7.5, 10},
	})
	playerStall   = promauto.NewCounter(prometheus.CounterOpts{Name: "player_stall_seconds_total", Help: "Segundos em travamento (rebuffer)."})
	playerWatched = promauto.NewCounter(prometheus.CounterOpts{Name: "player_watched_seconds_total", Help: "Segundos reproduzidos."})
	playerBitrate = promauto.NewGauge(prometheus.GaugeOpts{Name: "player_bitrate_bps", Help: "Bitrate da variante em reprodução (último informado)."})
)

type api struct {
	db           *pgxpool.Pool
	s3           *minio.Client
	core         minio.Core
	partSize     int64
	progressMode string
	playbackTTL  time.Duration
}

func runAPI(ctx context.Context) {
	a := &api{
		db:           mustDB(ctx),
		s3:           mustS3(ctx),
		partSize:     int64(envInt("PART_SIZE_MB", 5)) << 20,
		progressMode: env("PROGRESS_MODE", "conditional"),
		playbackTTL:  time.Duration(envInt("PLAYBACK_TTL_S", 3600)) * time.Second,
	}
	a.core = minio.Core{Client: a.s3}
	ensureBuckets(ctx, a.s3)
	prometheus.MustRegister(&queueCollector{db: a.db})
	go a.seed(ctx)

	mux := http.NewServeMux()
	h := func(pattern, route string, fn http.HandlerFunc) { mux.Handle(pattern, instrument(route, fn)) }

	h("GET /api/courses", "courses", a.listCourses)
	h("GET /api/courses/{id}", "course", a.getCourse)

	h("POST /api/uploads", "upload_create", a.createUpload)
	h("GET /api/uploads/{id}", "upload_status", a.uploadStatus)
	h("POST /api/uploads/{id}/parts", "upload_parts", a.uploadParts)
	h("POST /api/uploads/{id}/complete", "upload_complete", a.completeUpload)

	h("POST /api/lessons/{id}/playback", "playback", a.playback)
	h("PUT /api/lessons/{id}/active", "set_active", a.setActive)
	h("POST /api/lessons/{id}/progress/session", "progress_session", a.openSession)
	h("PUT /api/lessons/{id}/progress", "progress_put", a.putProgress)
	h("GET /api/lessons/{id}/progress", "progress_get", a.getProgress)

	h("GET /api/pipeline", "pipeline", a.pipeline)
	h("POST /api/admin/burst", "burst", a.burst)
	h("POST /api/telemetry", "telemetry", a.telemetry)
	h("GET /api/sample", "sample", a.sample)
	h("GET /api/config", "config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"progressMode": a.progressMode, "partSize": a.partSize})
	})

	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.Ping(r.Context()); err != nil {
			writeErr(w, 503, err.Error())
			return
		}
		w.Write([]byte("ok"))
	})
	serve(ctx, ":8080", mux)
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

func instrument(route string, fn http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, code: 200}
		t := time.Now()
		fn(sw, r)
		httpDuration.WithLabelValues(route, strconv.Itoa(sw.code)).Observe(time.Since(t).Seconds())
	})
}

// O laboratório não tem login: o cabeçalho X-User diz quem é o aluno.
func user(r *http.Request) string {
	if u := r.Header.Get("X-User"); u != "" {
		return u
	}
	return "ana"
}

func pathInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.PathValue(name))
	return n
}

// ---------- catálogo e busca ----------

func (a *api) listCourses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	// websearch_to_tsquery aceita o que o aluno digita: aspas, "or", -palavra.
	// Sem termo, lista tudo. O índice GIN atende as duas colunas `search`.
	rows, err := a.db.Query(r.Context(), `
		WITH q AS (SELECT websearch_to_tsquery('portuguese', $1) AS tsq)
		SELECT c.id, c.title, c.description,
		       (SELECT count(*) FROM lessons l WHERE l.course_id = c.id),
		       EXISTS (SELECT 1 FROM enrollments e WHERE e.course_id = c.id AND e.user_id = $2),
		       ts_rank(c.search, q.tsq)
		FROM courses c, q
		WHERE $1 = '' OR c.search @@ q.tsq
		   OR EXISTS (SELECT 1 FROM lessons l WHERE l.course_id = c.id AND l.search @@ q.tsq)
		ORDER BY 6 DESC, c.id`, q, user(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type course struct {
		ID          int     `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Lessons     int     `json:"lessons"`
		Enrolled    bool    `json:"enrolled"`
		Rank        float32 `json:"rank"`
	}
	out := []course{}
	for rows.Next() {
		var c course
		if err := rows.Scan(&c.ID, &c.Title, &c.Description, &c.Lessons, &c.Enrolled, &c.Rank); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out = append(out, c)
	}
	writeJSON(w, 200, out)
}

type assetView struct {
	ID        int64   `json:"id"`
	Version   int     `json:"version"`
	Status    string  `json:"status"`
	HLSPrefix *string `json:"hlsPrefix"`
	Active    bool    `json:"active"`
}

func (a *api) getCourse(w http.ResponseWriter, r *http.Request) {
	id := pathInt(r, "id")
	var title string
	var enrolled bool
	err := a.db.QueryRow(r.Context(), `
		SELECT title, EXISTS (SELECT 1 FROM enrollments WHERE course_id = $1 AND user_id = $2)
		FROM courses WHERE id = $1`, id, user(r)).Scan(&title, &enrolled)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, 404, "curso não encontrado")
		return
	} else if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type lesson struct {
		ID       int         `json:"id"`
		Position int         `json:"position"`
		Title    string      `json:"title"`
		Assets   []assetView `json:"assets"`
	}
	lessons := []*lesson{}
	byID := map[int]*lesson{}
	rows, err := a.db.Query(r.Context(), `
		SELECT l.id, l.position, l.title, a.id, a.version, a.status, a.hls_prefix, a.id = l.active_asset_id
		FROM lessons l LEFT JOIN assets a ON a.lesson_id = l.id
		WHERE l.course_id = $1 ORDER BY l.position, a.version DESC`, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var l lesson
		var aid *int64
		var ver *int
		var status, prefix *string
		var active *bool
		if err := rows.Scan(&l.ID, &l.Position, &l.Title, &aid, &ver, &status, &prefix, &active); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		cur, ok := byID[l.ID]
		if !ok {
			l.Assets = []assetView{}
			cur = &l
			byID[l.ID] = cur
			lessons = append(lessons, cur)
		}
		if aid != nil && len(cur.Assets) < 5 {
			cur.Assets = append(cur.Assets, assetView{*aid, *ver, *status, prefix, active != nil && *active})
		}
	}
	writeJSON(w, 200, map[string]any{"id": id, "title": title, "enrolled": enrolled, "lessons": lessons})
}

// ---------- upload direto, retomável e privado ----------
//
// 1. POST /api/uploads            → reserva asset + chave e abre o multipart no storage
// 2. POST /api/uploads/{id}/parts → URLs pré-assinadas, uma por parte
// 3. o cliente faz PUT de cada parte direto no storage (a API não vê os bytes)
// 4. POST /api/uploads/{id}/complete → junta as partes e enfileira o processamento
//
// Caiu no meio? GET /api/uploads/{id} lista as partes já gravadas; o cliente
// pede URLs novas só para as que faltam.

type uploadRow struct {
	ID         string
	AssetID    int64
	S3UploadID string
	PartSize   int64
	Size       int64
	Status     string
	RawKey     string
	LessonID   int
}

func (a *api) loadUpload(ctx context.Context, id string) (*uploadRow, error) {
	var u uploadRow
	err := a.db.QueryRow(ctx, `
		SELECT u.id::text, u.asset_id, u.s3_upload_id, u.part_size, u.size_bytes, u.status, a.raw_key, a.lesson_id
		FROM uploads u JOIN assets a ON a.id = u.asset_id WHERE u.id::text = $1`, id).
		Scan(&u.ID, &u.AssetID, &u.S3UploadID, &u.PartSize, &u.Size, &u.Status, &u.RawKey, &u.LessonID)
	return &u, err
}

func (a *api) createUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LessonID int   `json:"lessonId"`
		Size     int64 `json:"size"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.LessonID == 0 || in.Size <= 0 {
		writeErr(w, 400, "informe lessonId e size")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer tx.Rollback(ctx)

	// Trava a aula para numerar a versão: dois uploads simultâneos não recebem o mesmo número.
	var exists int
	if err := tx.QueryRow(ctx, `SELECT id FROM lessons WHERE id = $1 FOR UPDATE`, in.LessonID).Scan(&exists); err != nil {
		writeErr(w, 404, "aula não encontrada")
		return
	}
	var assetID int64
	var version int
	err = tx.QueryRow(ctx, `
		INSERT INTO assets (lesson_id, version, raw_key, size_bytes)
		SELECT $1, COALESCE(max(version), 0) + 1, '', $2 FROM assets WHERE lesson_id = $1
		RETURNING id, version`, in.LessonID, in.Size).Scan(&assetID, &version)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// A chave é reservada pela API: a URL assinada só serve para este objeto.
	key := fmt.Sprintf("uploads/a%d/original.mp4", assetID)
	s3id, err := a.core.NewMultipartUpload(ctx, bucketRaw, key, minio.PutObjectOptions{ContentType: "video/mp4"})
	if err != nil {
		writeErr(w, 502, "storage: "+err.Error())
		return
	}
	var uploadID string
	err = tx.QueryRow(ctx, `
		WITH a AS (UPDATE assets SET raw_key = $2 WHERE id = $1)
		INSERT INTO uploads (asset_id, s3_upload_id, part_size, size_bytes) VALUES ($1, $3, $4, $5)
		RETURNING id::text`, assetID, key, s3id, a.partSize, in.Size).Scan(&uploadID)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uploadsTotal.WithLabelValues("created").Inc()
	writeJSON(w, 201, map[string]any{
		"uploadId": uploadID, "assetId": assetID, "version": version,
		"partSize": a.partSize, "parts": (in.Size + a.partSize - 1) / a.partSize,
	})
}

func (a *api) storedParts(ctx context.Context, u *uploadRow) ([]minio.ObjectPart, error) {
	res, err := a.core.ListObjectParts(ctx, bucketRaw, u.RawKey, u.S3UploadID, 0, 1000)
	return res.ObjectParts, err
}

func (a *api) uploadStatus(w http.ResponseWriter, r *http.Request) {
	u, err := a.loadUpload(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "upload não encontrado")
		return
	}
	type part struct {
		PartNumber int    `json:"partNumber"`
		ETag       string `json:"etag"`
		Size       int64  `json:"size"`
	}
	parts := []part{}
	if u.Status == "OPEN" {
		stored, err := a.storedParts(r.Context(), u)
		if err != nil {
			writeErr(w, 502, "storage: "+err.Error())
			return
		}
		for _, p := range stored {
			parts = append(parts, part{p.PartNumber, p.ETag, p.Size})
		}
	}
	writeJSON(w, 200, map[string]any{
		"uploadId": u.ID, "assetId": u.AssetID, "status": u.Status, "partSize": u.PartSize,
		"parts": (u.Size + u.PartSize - 1) / u.PartSize, "stored": parts,
	})
}

func (a *api) uploadParts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PartNumbers []int `json:"partNumbers"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.PartNumbers) == 0 {
		writeErr(w, 400, "informe partNumbers")
		return
	}
	u, err := a.loadUpload(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "upload não encontrado")
		return
	}
	if u.Status != "OPEN" {
		writeErr(w, 409, "upload já concluído")
		return
	}
	total := int((u.Size + u.PartSize - 1) / u.PartSize)
	urls := map[string]string{}
	for _, n := range in.PartNumbers {
		if n < 1 || n > total {
			writeErr(w, 400, fmt.Sprintf("parte %d fora do intervalo 1..%d", n, total))
			return
		}
		// Expiração curta: a URL autoriza uma parte de uma chave reservada, por 15 minutos.
		signed, err := a.s3.Presign(r.Context(), http.MethodPut, bucketRaw, u.RawKey, 15*time.Minute,
			url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {u.S3UploadID}})
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		// O navegador alcança o storage pelo caminho /s3 da interface (mesma origem, sem CORS).
		urls[strconv.Itoa(n)] = "/s3" + signed.RequestURI()
	}
	writeJSON(w, 200, map[string]any{"urls": urls, "expiresIn": 900})
}

func (a *api) completeUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u, err := a.loadUpload(ctx, r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "upload não encontrado")
		return
	}
	done := func(repeated bool) {
		writeJSON(w, 200, map[string]any{"uploadId": u.ID, "assetId": u.AssetID, "status": "COMPLETED", "repeated": repeated})
	}
	// Idempotência: repetir o complete (a resposta se perdeu, o cliente tentou de novo) não cria nada.
	if u.Status == "COMPLETED" {
		uploadsTotal.WithLabelValues("complete_repeated").Inc()
		done(true)
		return
	}
	// A API não confia na lista do cliente: lê do storage as partes realmente gravadas.
	stored, err := a.storedParts(ctx, u)
	event := "completed"
	if err == nil {
		total := int((u.Size + u.PartSize - 1) / u.PartSize)
		if len(stored) != total {
			writeErr(w, 409, fmt.Sprintf("faltam partes: %d de %d gravadas", len(stored), total))
			return
		}
		parts := make([]minio.CompletePart, len(stored))
		for i, p := range stored {
			parts[i] = minio.CompletePart{PartNumber: p.PartNumber, ETag: p.ETag}
		}
		_, err = a.core.CompleteMultipartUpload(ctx, bucketRaw, u.RawKey, u.S3UploadID, parts, minio.PutObjectOptions{})
	}
	if err != nil {
		// O storage pode ter concluído e a resposta ter se perdido: confira o objeto
		// na chave reservada e reconcilie a sessão em vez de criar outro asset.
		st, serr := a.s3.StatObject(ctx, bucketRaw, u.RawKey, minio.StatObjectOptions{})
		if serr != nil || st.Size != u.Size {
			writeErr(w, 502, "storage: "+err.Error())
			return
		}
		event = "reconciled"
	}
	if err := a.enqueue(ctx, u.ID, u.AssetID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uploadsTotal.WithLabelValues(event).Inc()
	done(false)
}

// enqueue fecha a sessão, move o asset para QUEUED e cria o job — tudo na mesma
// transação. A chave única do job faz da repetição um no-op.
func (a *api) enqueue(ctx context.Context, uploadID string, assetID int64) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if uploadID != "" {
		if _, err := tx.Exec(ctx, `UPDATE uploads SET status = 'COMPLETED' WHERE id::text = $1`, uploadID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'QUEUED' WHERE id = $1 AND status = 'UPLOADING'`, assetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO jobs (asset_id, profile_version) VALUES ($1, $2)
		ON CONFLICT (asset_id, source_version, profile_version) DO NOTHING`, assetID, profileVersion); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------- playback: quem decide o acesso é a fonte autoritativa ----------

func (a *api) playback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lessonID := pathInt(r, "id")
	var enrolled bool
	var assetID *int64
	var version *int
	var status, prefix *string
	err := a.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM enrollments e WHERE e.course_id = l.course_id AND e.user_id = $2),
		       a.id, a.version, a.status, a.hls_prefix
		FROM lessons l LEFT JOIN assets a ON a.id = l.active_asset_id
		WHERE l.id = $1`, lessonID, user(r)).Scan(&enrolled, &assetID, &version, &status, &prefix)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, 404, "aula não encontrada")
		return
	} else if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// A busca pode estar desatualizada; a matrícula é conferida aqui, a cada play.
	if !enrolled {
		playbackTotal.WithLabelValues("forbidden").Inc()
		writeErr(w, 403, "sem matrícula neste curso")
		return
	}
	if assetID == nil || status == nil || *status != "READY" || prefix == nil {
		playbackTotal.WithLabelValues("not_ready").Inc()
		writeErr(w, 409, "esta aula ainda não tem vídeo publicado")
		return
	}
	asset := fmt.Sprintf("a%d", *assetID)
	exp := time.Now().Add(a.playbackTTL)
	token := signAccess(asset, exp)
	// O cookie vale só para o caminho deste asset. A URL do vídeo não leva o token:
	// todos os alunos pedem exatamente a mesma URL, e a borda guarda uma cópia só.
	http.SetCookie(w, &http.Cookie{
		Name: "vod_access", Value: token, Path: "/vod/" + asset + "/",
		Expires: exp, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	playbackTotal.WithLabelValues("ok").Inc()
	writeJSON(w, 200, map[string]any{
		"assetId": *assetID, "version": *version,
		"manifestUrl": "/vod/" + *prefix + "master.m3u8",
		"access":      token, "expiresAt": exp.UTC().Format(time.RFC3339),
	})
}

// setActive troca o ponteiro da aula para outra versão já publicada (rollback).
// A URL versionada muda junto: nada é invalidado na CDN.
func (a *api) setActive(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AssetID int64 `json:"assetId"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.AssetID == 0 {
		writeErr(w, 400, "informe assetId")
		return
	}
	tag, err := a.db.Exec(r.Context(), `
		UPDATE lessons l SET active_asset_id = a.id FROM assets a
		WHERE l.id = $1 AND a.id = $2 AND a.lesson_id = l.id AND a.status = 'READY'`, pathInt(r, "id"), in.AssetID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, 409, "versão inexistente ou ainda não publicada")
		return
	}
	writeJSON(w, 200, map[string]any{"activeAssetId": in.AssetID})
}

// ---------- progresso e retomada entre dispositivos ----------

type progressState struct {
	Generation int  `json:"generation"`
	Seq        int  `json:"seq"`
	PositionMs int  `json:"positionMs"`
	Completed  bool `json:"completed"`
}

// openSession é chamada no play. Uma sessão nova incrementa generation de forma
// atômica: checkpoints atrasados da sessão anterior passam a ser recusados.
func (a *api) openSession(w http.ResponseWriter, r *http.Request) {
	var p progressState
	err := a.db.QueryRow(r.Context(), `
		INSERT INTO progress (user_id, lesson_id, generation) VALUES ($1, $2, 1)
		ON CONFLICT (user_id, lesson_id) DO UPDATE SET generation = progress.generation + 1, seq = 0
		RETURNING generation, seq, pos_ms, completed`, user(r), pathInt(r, "id")).
		Scan(&p.Generation, &p.Seq, &p.PositionMs, &p.Completed)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (a *api) getProgress(w http.ResponseWriter, r *http.Request) {
	var p progressState
	err := a.db.QueryRow(r.Context(), `
		SELECT generation, seq, pos_ms, completed FROM progress WHERE user_id = $1 AND lesson_id = $2`,
		user(r), pathInt(r, "id")).Scan(&p.Generation, &p.Seq, &p.PositionMs, &p.Completed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (a *api) putProgress(w http.ResponseWriter, r *http.Request) {
	var in progressState
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "corpo inválido")
		return
	}
	ctx, u, lesson := r.Context(), user(r), pathInt(r, "id")
	var tag interface{ RowsAffected() int64 }
	var err error
	if a.progressMode == "lww" {
		// "Última escrita vence": quem chegar por último grava, mesmo sendo um
		// checkpoint antigo de outro aparelho. É o bug que o Lab 5 mostra.
		tag, err = a.db.Exec(ctx, `
			UPDATE progress SET pos_ms = $3, seq = $4, completed = completed OR $5, updated_at = now()
			WHERE user_id = $1 AND lesson_id = $2`, u, lesson, in.PositionMs, in.Seq, in.Completed)
	} else {
		// Escrita condicional: só a sessão atual grava, e só para frente na sequência.
		// Voltar o vídeo funciona: o seek para trás chega com seq maior.
		tag, err = a.db.Exec(ctx, `
			UPDATE progress SET pos_ms = $3, seq = $5, completed = completed OR $6, updated_at = now()
			WHERE user_id = $1 AND lesson_id = $2 AND generation = $4 AND seq < $5`,
			u, lesson, in.PositionMs, in.Generation, in.Seq, in.Completed)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		progressWrites.WithLabelValues("stale").Inc()
		var cur progressState
		a.db.QueryRow(ctx, `SELECT generation, seq, pos_ms, completed FROM progress WHERE user_id = $1 AND lesson_id = $2`,
			u, lesson).Scan(&cur.Generation, &cur.Seq, &cur.PositionMs, &cur.Completed)
		writeJSON(w, 409, map[string]any{"error": "checkpoint antigo: outra sessão ou sequência mais nova já gravou", "current": cur})
		return
	}
	progressWrites.WithLabelValues("accepted").Inc()
	w.WriteHeader(204)
}

// ---------- telemetria do player ----------

func (a *api) telemetry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		StartupMs  float64 `json:"startupMs"`
		StallMs    float64 `json:"stallMs"`
		WatchedMs  float64 `json:"watchedMs"`
		BitrateBps float64 `json:"bitrateBps"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "corpo inválido")
		return
	}
	if in.StartupMs > 0 {
		playerStartup.Observe(in.StartupMs / 1000)
	}
	playerStall.Add(max(in.StallMs, 0) / 1000)
	playerWatched.Add(max(in.WatchedMs, 0) / 1000)
	if in.BitrateBps > 0 {
		playerBitrate.Set(in.BitrateBps)
	}
	w.WriteHeader(204)
}

// ---------- visão do pipeline ----------

func (a *api) pipeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type job struct {
		AssetID   int64   `json:"assetId"`
		Lesson    string  `json:"lesson"`
		Version   int     `json:"version"`
		Status    string  `json:"status"`
		JobState  *string `json:"jobState"`
		Attempt   *int    `json:"attempt"`
		Worker    *string `json:"worker"`
		LeaseS    *int    `json:"leaseS"`
		AgeS      int     `json:"ageS"`
		TotalS    *int    `json:"totalS"`
		LastError *string `json:"lastError"`
		Active    bool    `json:"active"`
	}
	rows, err := a.db.Query(ctx, `
		SELECT a.id, l.title, a.version, a.status, j.state, j.attempt, j.worker,
		       GREATEST(extract(epoch FROM j.lease_until - now()), 0)::int,
		       extract(epoch FROM now() - a.created_at)::int,
		       extract(epoch FROM j.finished_at - j.enqueued_at)::int,
		       j.last_error, a.id = l.active_asset_id
		FROM assets a JOIN lessons l ON l.id = a.lesson_id LEFT JOIN jobs j ON j.asset_id = a.id
		ORDER BY a.id DESC LIMIT 30`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	jobs := []job{}
	for rows.Next() {
		var j job
		var active *bool
		if err := rows.Scan(&j.AssetID, &j.Lesson, &j.Version, &j.Status, &j.JobState, &j.Attempt, &j.Worker,
			&j.LeaseS, &j.AgeS, &j.TotalS, &j.LastError, &active); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		j.Active = active != nil && *active
		jobs = append(jobs, j)
	}
	q := queueStats(ctx, a.db)
	writeJSON(w, 200, map[string]any{"jobs": jobs, "queue": q})
}

type qstats struct {
	Queued     int     `json:"queued"`
	Processing int     `json:"processing"`
	Done       int     `json:"done"`
	Failed     int     `json:"failed"`
	OldestAgeS float64 `json:"oldestAgeS"`
}

func queueStats(ctx context.Context, db *pgxpool.Pool) qstats {
	var q qstats
	db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE state = 'QUEUED'), count(*) FILTER (WHERE state = 'PROCESSING'),
		       count(*) FILTER (WHERE state = 'DONE'), count(*) FILTER (WHERE state = 'FAILED'),
		       COALESCE(extract(epoch FROM now() - min(enqueued_at) FILTER (WHERE state IN ('QUEUED', 'PROCESSING'))), 0)::float8
		FROM jobs`).Scan(&q.Queued, &q.Processing, &q.Done, &q.Failed, &q.OldestAgeS)
	return q
}

// queueCollector expõe a fila ao Prometheus lendo o banco a cada coleta.
// A idade do job mais antigo avisa do atraso antes do tamanho da fila.
type queueCollector struct{ db *pgxpool.Pool }

var (
	queueJobsDesc = prometheus.NewDesc("queue_jobs", "Jobs por estado.", []string{"state"}, nil)
	queueAgeDesc  = prometheus.NewDesc("queue_oldest_job_age_seconds", "Idade do job pendente mais antigo.", nil, nil)
)

func (c *queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- queueJobsDesc
	ch <- queueAgeDesc
}
func (c *queueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	q := queueStats(ctx, c.db)
	for state, n := range map[string]int{"QUEUED": q.Queued, "PROCESSING": q.Processing, "DONE": q.Done, "FAILED": q.Failed} {
		ch <- prometheus.MustNewConstMetric(queueJobsDesc, prometheus.GaugeValue, float64(n), state)
	}
	ch <- prometheus.MustNewConstMetric(queueAgeDesc, prometheus.GaugeValue, q.OldestAgeS)
}

// ---------- dados do laboratório ----------

const (
	sampleKey = "samples/sample.mp4" // vídeo de exemplo: 90 s, 1080p
	burstKey  = "samples/burst.mp4"  // clipe curto para encher a fila
)

// burst simula uma rajada de publicações: cria N versões da "Aula de carga"
// apontando para o clipe de exemplo e enfileira todas de uma vez.
func (a *api) burst(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n < 1 || n > 200 {
		writeErr(w, 400, "use ?n= entre 1 e 200")
		return
	}
	ctx := r.Context()
	created := 0
	for i := 0; i < n; i++ {
		id, err := a.newAssetFromSample(ctx, 8, burstKey)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err := a.enqueue(ctx, "", id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		created++
	}
	writeJSON(w, 201, map[string]any{"enqueued": created})
}

func (a *api) newAssetFromSample(ctx context.Context, lessonID int, key string) (int64, error) {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var id int64
	if _, err := tx.Exec(ctx, `SELECT 1 FROM lessons WHERE id = $1 FOR UPDATE`, lessonID); err != nil {
		return 0, err
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO assets (lesson_id, version, raw_key)
		SELECT $1, COALESCE(max(version), 0) + 1, $2 FROM assets WHERE lesson_id = $1 RETURNING id`, lessonID, key).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// sample entrega o vídeo de exemplo para o botão "usar vídeo de exemplo" da interface.
func (a *api) sample(w http.ResponseWriter, r *http.Request) {
	obj, err := a.s3.GetObject(r.Context(), bucketRaw, sampleKey, minio.GetObjectOptions{})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	defer obj.Close()
	st, err := obj.Stat()
	if err != nil {
		writeErr(w, 404, "vídeo de exemplo ainda não foi gerado")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size, 10))
	io.Copy(w, obj)
}

// seed roda na subida: gera os vídeos de exemplo (uma vez) e publica a
// primeira aula, para o player ter o que tocar sem depender do Lab 1.
func (a *api) seed(ctx context.Context) {
	gen := func(key string, seconds int) error {
		if _, err := a.s3.StatObject(ctx, bucketRaw, key, minio.StatObjectOptions{}); err == nil {
			return nil
		}
		log.Printf("seed: gerando %s (%d s)", key, seconds)
		tmp, err := os.CreateTemp("", "sample-*.mp4")
		if err != nil {
			return err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		// Padrão de teste com relógio na imagem e bitrate constante: o tamanho dos
		// arquivos bate com as contas da aula.
		cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30,drawtext=text='%{pts\\:hms}':fontsize=110:x=60:y=60:fontcolor=white:box=1:boxcolor=black@0.6",
			"-f", "lavfi", "-i", "sine=frequency=330:sample_rate=48000",
			"-t", strconv.Itoa(seconds),
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "60",
			"-b:v", "5M", "-minrate", "5M", "-maxrate", "5M", "-bufsize", "10M", "-x264-params", "nal-hrd=cbr",
			"-c:a", "aac", "-b:a", "128k", "-ac", "2", "-movflags", "+faststart", tmp.Name())
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg: %v: %s", err, out)
		}
		_, err = a.s3.FPutObject(ctx, bucketRaw, key, tmp.Name(), minio.PutObjectOptions{ContentType: "video/mp4"})
		return err
	}
	if err := gen(sampleKey, envInt("SAMPLE_SECONDS", 90)); err != nil {
		log.Printf("seed: %v", err)
		return
	}
	if err := gen(burstKey, envInt("BURST_SECONDS", 12)); err != nil {
		log.Printf("seed: %v", err)
		return
	}
	var n int
	a.db.QueryRow(ctx, `SELECT count(*) FROM assets`).Scan(&n)
	if n > 0 {
		return
	}
	id, err := a.newAssetFromSample(ctx, 1, sampleKey)
	if err == nil {
		err = a.enqueue(ctx, "", id)
	}
	if err != nil {
		log.Printf("seed: %v", err)
		return
	}
	log.Print("seed: aula 1 enfileirada para processamento")
}
