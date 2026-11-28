// Worker: consome a fila e transforma um original em um pacote HLS.
//
//	claim (lease) → download → probe → encode + package → upload em prefixo da
//	tentativa → validação → publicação atômica (READY + ponteiro da aula)
//
// A fila entrega pelo menos uma vez. O que mantém o resultado correto:
//
//   - cada tentativa escreve no próprio prefixo (nada é sobrescrito);
//
//   - a lease é renovada durante o encode; se vencer, outro worker assume;
//
//   - a publicação é um UPDATE condicional ao attempt_id: a tentativa antiga perde.
//
//     LEASE_S          duração da lease                                   (Lab 2)
//     MAX_ATTEMPTS     tentativas antes de FAILED (a "DLQ" do lab)        (Lab 2)
//     ENCODE_EXTRA_S   espera extra depois do encode, para abrir a janela (Lab 2)
//     FAULT            none | drop_segment  apaga um segmento do pacote   (Lab 6)
//     VALIDATE         true | false  valida o pacote antes do READY       (Lab 6)
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// profileVersion identifica a ladder. Mudar a ladder = subir este número:
// o mesmo original ganha um job novo, sem colidir com o anterior.
const profileVersion = 1

// A ladder da aula: 480p / 1 Mb/s, 720p / 2,5 Mb/s, 1080p / 5 Mb/s (vídeo + áudio).
var ladder = []struct {
	name    string
	height  int
	videoKb int
}{
	{"480p", 480, 900},
	{"720p", 720, 2400},
	{"1080p", 1080, 4900},
}

var (
	jobAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "worker_attempts_total", Help: "Tentativas por resultado.",
	}, []string{"result"}) // published | error | lost_lease | failed
	encodeSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "worker_encode_seconds", Help: "Duração do encode + package.",
		Buckets: []float64{2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300},
	})
	publishSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "publish_latency_seconds", Help: "Do enfileiramento ao READY (o SLO de publicação).",
		Buckets: []float64{5, 10, 20, 30, 45, 60, 90, 120, 180, 300, 600, 900},
	})
	workerBusy = promauto.NewGauge(prometheus.GaugeOpts{Name: "worker_busy", Help: "1 enquanto o worker processa um job."})
)

type worker struct {
	db       *pgxpool.Pool
	s3       *minio.Client
	name     string
	lease    time.Duration
	maxTries int
}

type claimed struct {
	JobID     int64
	AssetID   int64
	Attempt   int
	AttemptID string
	RawKey    string
	LessonID  int
}

var errLostLease = errors.New("lease perdida: outra tentativa assumiu o job")

func runWorker(ctx context.Context) {
	host, _ := os.Hostname()
	w := &worker{
		db: mustDB(ctx), s3: mustS3(ctx), name: host,
		lease:    time.Duration(envInt("LEASE_S", 20)) * time.Second,
		maxTries: envInt("MAX_ATTEMPTS", 3),
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	go serve(ctx, ":8080", mux)

	log.Printf("worker %s pronto (lease %s, até %d tentativas)", w.name, w.lease, w.maxTries)
	for ctx.Err() == nil {
		job, err := w.claim(ctx)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
				log.Printf("claim: %v", err)
			}
			select { // fila vazia: espera um pouco e pergunta de novo
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
			continue
		}
		w.process(ctx, job)
	}
}

// claim pega o job pendente mais antigo. Dois casos entram: o que nunca rodou
// (QUEUED) e o que está em PROCESSING com a lease vencida — o worker anterior
// morreu ou travou. SKIP LOCKED faz workers concorrentes pegarem jobs diferentes.
func (w *worker) claim(ctx context.Context) (*claimed, error) {
	// Quem estourou o limite de tentativas sai da fila: FAILED é a "DLQ" do lab.
	w.db.Exec(ctx, `
		WITH dead AS (
			UPDATE jobs SET state = 'FAILED', finished_at = now(),
			       last_error = COALESCE(last_error, 'lease venceu em todas as tentativas')
			WHERE state = 'PROCESSING' AND lease_until < now() AND attempt >= $1 RETURNING asset_id)
		UPDATE assets SET status = 'FAILED' WHERE id IN (SELECT asset_id FROM dead)`, w.maxTries)

	var c claimed
	err := w.db.QueryRow(ctx, `
		WITH next AS (
			SELECT id FROM jobs
			WHERE (state = 'QUEUED' AND run_after <= now())
			   OR (state = 'PROCESSING' AND lease_until < now())
			ORDER BY enqueued_at
			FOR UPDATE SKIP LOCKED LIMIT 1),
		j AS (
			UPDATE jobs SET state = 'PROCESSING', attempt = attempt + 1, attempt_id = gen_random_uuid(),
			       worker = $1, lease_until = now() + make_interval(secs => $2),
			       started_at = COALESCE(started_at, now())
			WHERE id = (SELECT id FROM next)
			RETURNING id, asset_id, attempt, attempt_id::text),
		a AS (
			UPDATE assets SET status = 'PROCESSING' WHERE id = (SELECT asset_id FROM j)
			RETURNING id, raw_key, lesson_id)
		SELECT j.id, j.asset_id, j.attempt, j.attempt_id, a.raw_key, a.lesson_id FROM j JOIN a ON a.id = j.asset_id`,
		w.name, w.lease.Seconds()).Scan(&c.JobID, &c.AssetID, &c.Attempt, &c.AttemptID, &c.RawKey, &c.LessonID)
	return &c, err
}

func (w *worker) process(ctx context.Context, job *claimed) {
	workerBusy.Set(1)
	defer workerBusy.Set(0)
	log.Printf("job %d (asset %d) tentativa %d: início", job.JobID, job.AssetID, job.Attempt)

	// Heartbeat: renova a lease enquanto trabalha. Se o UPDATE não achar a linha
	// com o nosso attempt_id, outra tentativa assumiu — cancela tudo.
	jctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	go func() {
		t := time.NewTicker(w.lease / 3)
		defer t.Stop()
		for {
			select {
			case <-jctx.Done():
				return
			case <-t.C:
				tag, err := w.db.Exec(jctx, `
					UPDATE jobs SET lease_until = now() + make_interval(secs => $3)
					WHERE id = $1 AND attempt_id::text = $2 AND state = 'PROCESSING'`, job.JobID, job.AttemptID, w.lease.Seconds())
				if err == nil && tag.RowsAffected() == 0 {
					cancel(errLostLease)
					return
				}
			}
		}
	}()

	err := w.run(jctx, job)
	if cause := context.Cause(jctx); errors.Is(cause, errLostLease) {
		err = errLostLease
	}
	switch {
	case err == nil:
		jobAttempts.WithLabelValues("published").Inc()
		log.Printf("job %d tentativa %d: READY", job.JobID, job.Attempt)
	case errors.Is(err, errLostLease):
		jobAttempts.WithLabelValues("lost_lease").Inc()
		log.Printf("job %d tentativa %d: %v — nada foi publicado por esta tentativa", job.JobID, job.Attempt, err)
	case ctx.Err() != nil:
		log.Printf("job %d tentativa %d: worker encerrando; a lease vence e outro assume", job.JobID, job.Attempt)
	default:
		w.fail(job, err)
	}
}

func (w *worker) run(ctx context.Context, job *claimed) error {
	dir, err := os.MkdirTemp("", fmt.Sprintf("job-%d-", job.JobID))
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "source.mp4")
	if err := w.s3.FGetObject(ctx, bucketRaw, job.RawKey, src, minio.GetObjectOptions{}); err != nil {
		return fmt.Errorf("baixar original: %w", err)
	}
	// Probe: recusa cedo o que não é vídeo, antes de gastar CPU de encode.
	if out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "csv=p=0", src).Output(); err != nil || strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("probe: arquivo sem faixa de vídeo legível")
	}

	out := filepath.Join(dir, "out")
	os.MkdirAll(out, 0o755)
	start := time.Now()
	if err := encode(ctx, src, out); err != nil {
		return err
	}
	encodeSeconds.Observe(time.Since(start).Seconds())
	if extra := envInt("ENCODE_EXTRA_S", 0); extra > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(extra) * time.Second):
		}
	}

	// Staging: cada tentativa grava no próprio prefixo. Uma tentativa que morre
	// no meio deixa lixo isolado, nunca um pacote publicado pela metade.
	prefix := fmt.Sprintf("a%d/t%d/", job.AssetID, job.Attempt)
	err = filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		_, err = w.s3.FPutObject(ctx, bucketVOD, prefix+filepath.ToSlash(rel), p, minio.PutObjectOptions{ContentType: contentType(p)})
		return err
	})
	if err != nil {
		return fmt.Errorf("enviar pacote: %w", err)
	}
	if env("FAULT", "none") == "drop_segment" {
		w.s3.RemoveObject(ctx, bucketVOD, prefix+"720p/seg_001.m4s", minio.RemoveObjectOptions{})
		log.Printf("job %d: FAULT=drop_segment apagou %s720p/seg_001.m4s", job.JobID, prefix)
	}
	if env("VALIDATE", "true") == "true" {
		if err := w.validate(ctx, prefix); err != nil {
			return fmt.Errorf("validação: %w", err)
		}
	}
	return w.publish(ctx, job, prefix)
}

// encode gera as três variantes numa passada só. Keyframes a cada 2 s em todas
// (-g 60 a 30 fps, sem corte de cena): os segmentos de 6 s começam no mesmo
// instante em todas as variantes, e o player troca de qualidade na emenda.
func encode(ctx context.Context, src, out string) error {
	var filter, vmap strings.Builder
	fmt.Fprintf(&filter, "[0:v]split=%d", len(ladder))
	for i := range ladder {
		fmt.Fprintf(&filter, "[s%d]", i)
	}
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", src}
	var maps, rates []string
	for i, r := range ladder {
		fmt.Fprintf(&filter, ";[s%d]scale=-2:%d[v%d]", i, r.height, i)
		maps = append(maps, "-map", fmt.Sprintf("[v%d]", i), "-map", "0:a:0?")
		kb := fmt.Sprintf("%dk", r.videoKb)
		rates = append(rates,
			fmt.Sprintf("-b:v:%d", i), kb, fmt.Sprintf("-minrate:v:%d", i), kb,
			fmt.Sprintf("-maxrate:v:%d", i), kb, fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", 2*r.videoKb))
		fmt.Fprintf(&vmap, "v:%d,a:%d,name:%s ", i, i, r.name)
	}
	args = append(args, "-filter_complex", filter.String())
	args = append(args, maps...)
	args = append(args, "-c:v", "libx264", "-preset", env("ENCODE_PRESET", "ultrafast"), "-pix_fmt", "yuv420p",
		"-r", "30", "-g", "60", "-keyint_min", "60", "-sc_threshold", "0", "-x264-params", "nal-hrd=cbr")
	args = append(args, rates...)
	args = append(args, "-c:a", "aac", "-b:a", "96k", "-ac", "2",
		"-f", "hls", "-hls_time", "6", "-hls_playlist_type", "vod", "-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments", "-master_pl_name", "master.m3u8",
		"-var_stream_map", strings.TrimSpace(vmap.String()),
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(out, "%v", "seg_%03d.m4s"), filepath.Join(out, "%v", "index.m3u8"))
	if b, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("encode: %v: %s", err, lastLine(string(b)))
	}
	return nil
}

// validate lê as playlists publicadas no storage e confere que todo arquivo
// referenciado existe. Uma versão só fica visível se o pacote estiver completo.
func (w *worker) validate(ctx context.Context, prefix string) error {
	refs := func(key string) ([]string, error) {
		obj, err := w.s3.GetObject(ctx, bucketVOD, key, minio.GetObjectOptions{})
		if err != nil {
			return nil, err
		}
		defer obj.Close()
		var out []string
		sc := bufio.NewScanner(obj)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if _, uri, ok := strings.Cut(line, `#EXT-X-MAP:URI="`); ok {
				out = append(out, strings.TrimSuffix(uri, `"`))
			} else if line != "" && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
		if sc.Err() != nil || len(out) == 0 {
			return nil, fmt.Errorf("%s ausente ou vazia", key)
		}
		return out, nil
	}
	variants, err := refs(prefix + "master.m3u8")
	if err != nil {
		return err
	}
	if len(variants) != len(ladder) {
		return fmt.Errorf("master com %d variantes, esperava %d", len(variants), len(ladder))
	}
	segments := -1
	for _, v := range variants {
		files, err := refs(prefix + v)
		if err != nil {
			return err
		}
		for _, f := range files {
			key := prefix + path.Join(path.Dir(v), f)
			if st, err := w.s3.StatObject(ctx, bucketVOD, key, minio.StatObjectOptions{}); err != nil || st.Size == 0 {
				return fmt.Errorf("%s referenciado na playlist e ausente no storage", key)
			}
		}
		if segments >= 0 && len(files) != segments {
			return fmt.Errorf("variantes com números de segmentos diferentes")
		}
		segments = len(files)
	}
	return nil
}

// publish é o commit: numa transação, fecha o job, marca o asset READY e aponta
// a aula para a versão nova. O WHERE com attempt_id é a proteção contra o worker
// antigo: se outra tentativa assumiu, nenhuma linha muda e nada é publicado.
func (w *worker) publish(ctx context.Context, job *claimed, prefix string) error {
	tx, err := w.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var waited float64
	err = tx.QueryRow(ctx, `
		UPDATE jobs SET state = 'DONE', finished_at = now(), last_error = NULL
		WHERE id = $1 AND attempt_id::text = $2 AND state = 'PROCESSING'
		RETURNING extract(epoch FROM now() - enqueued_at)::float8`, job.JobID, job.AttemptID).Scan(&waited)
	if errors.Is(err, pgx.ErrNoRows) {
		return errLostLease
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'READY', hls_prefix = $2, ready_at = now() WHERE id = $1`, job.AssetID, prefix); err != nil {
		return err
	}
	// A versão mais nova vira a ativa; a anterior continua no storage para rollback.
	if _, err := tx.Exec(ctx, `
		UPDATE lessons l SET active_asset_id = $2
		WHERE l.id = $1 AND NOT EXISTS (
			SELECT 1 FROM assets cur, assets new
			WHERE cur.id = l.active_asset_id AND new.id = $2 AND cur.version > new.version)`, job.LessonID, job.AssetID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	publishSeconds.Observe(waited)
	return nil
}

// fail devolve o job à fila com backoff, ou encerra em FAILED depois de MAX_ATTEMPTS.
// Roda com contexto próprio: o do job pode já ter sido cancelado.
func (w *worker) fail(job *claimed, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := cause.Error()
	if job.Attempt >= w.maxTries {
		jobAttempts.WithLabelValues("failed").Inc()
		log.Printf("job %d tentativa %d: %v — FAILED, sem novas tentativas", job.JobID, job.Attempt, msg)
		w.db.Exec(ctx, `
			WITH j AS (UPDATE jobs SET state = 'FAILED', finished_at = now(), last_error = $3
			           WHERE id = $1 AND attempt_id::text = $2 RETURNING asset_id)
			UPDATE assets SET status = 'FAILED' WHERE id IN (SELECT asset_id FROM j)`, job.JobID, job.AttemptID, msg)
		return
	}
	jobAttempts.WithLabelValues("error").Inc()
	backoff := time.Duration(job.Attempt*5) * time.Second
	log.Printf("job %d tentativa %d: %v — nova tentativa em %s", job.JobID, job.Attempt, msg, backoff)
	w.db.Exec(ctx, `
		WITH j AS (UPDATE jobs SET state = 'QUEUED', last_error = $3, run_after = now() + make_interval(secs => $4)
		           WHERE id = $1 AND attempt_id::text = $2 RETURNING asset_id)
		UPDATE assets SET status = 'QUEUED' WHERE id IN (SELECT asset_id FROM j)`, job.JobID, job.AttemptID, msg, backoff.Seconds())
}

func contentType(p string) string {
	switch filepath.Ext(p) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".m4s":
		return "video/iso.segment"
	default:
		return "video/mp4"
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
