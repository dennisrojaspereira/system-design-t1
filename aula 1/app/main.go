// Encurtador de links — System Design na Prática, Aula 01.
//
// Um único binário, vários "estágios" de arquitetura ligados por variável
// de ambiente. Cada laboratório liga uma peça e o Grafana mostra o efeito.
//
//	ID_STRATEGY     counter | random | range        (Lab 2)
//	CACHE_ENABLED   true | false                    (Lab 3)
//	DATABASE_REPLICA_URL  leituras vão para a réplica (Lab 5)
//	ANALYTICS_MODE  off | sync | async              (Lab 6)
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// ---------- configuração ----------

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return def
}

// ---------- métricas ----------

var (
	buckets = []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}

	httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "Latência por rota e status.", Buckets: buckets,
	}, []string{"route", "status"})

	dbDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "shortener_db_query_duration_seconds", Help: "Latência das queries (inclui espera no pool).", Buckets: buckets,
	}, []string{"op", "db"})

	cacheRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "shortener_cache_requests_total", Help: "Leituras no cache: hit, miss ou error.",
	}, []string{"result"})

	idCollisions = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shortener_id_collisions_total", Help: "Códigos gerados que já existiam (exigiu nova tentativa).",
	})
	idBlocks = promauto.NewCounter(prometheus.CounterOpts{
		Name: "shortener_id_blocks_allocated_total", Help: "Blocos de IDs reservados (estratégia range).",
	})

	analyticsQueue = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "shortener_analytics_queue_depth", Help: "Cliques aguardando gravação (modo async).",
	})
	analyticsEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "shortener_analytics_events_total", Help: "Cliques: written, dropped ou error.",
	}, []string{"result"})
)

func registerPoolMetrics(name string, pool *pgxpool.Pool) {
	labels := prometheus.Labels{"db": name}
	promauto.NewGaugeFunc(prometheus.GaugeOpts{Name: "shortener_db_pool_acquired_conns", Help: "Conexões em uso.", ConstLabels: labels},
		func() float64 { return float64(pool.Stat().AcquiredConns()) })
	promauto.NewGaugeFunc(prometheus.GaugeOpts{Name: "shortener_db_pool_max_conns", Help: "Tamanho máximo do pool.", ConstLabels: labels},
		func() float64 { return float64(pool.Stat().MaxConns()) })
	promauto.NewCounterFunc(prometheus.CounterOpts{Name: "shortener_db_pool_wait_seconds_total", Help: "Tempo total esperando conexão livre.", ConstLabels: labels},
		func() float64 { return pool.Stat().AcquireDuration().Seconds() })
	promauto.NewCounterFunc(prometheus.CounterOpts{Name: "shortener_db_pool_empty_acquire_total", Help: "Vezes em que o pool estava vazio e a requisição teve de esperar.", ConstLabels: labels},
		func() float64 { return float64(pool.Stat().EmptyAcquireCount()) })
}

// ---------- servidor ----------

type server struct {
	primary *pgxpool.Pool // escritas
	reader  *pgxpool.Pool // leituras (primário ou réplica)
	readerN string        // "primary" | "replica"
	cache   *redis.Client // nil quando CACHE_ENABLED=false
	ttl     time.Duration

	idStrategy string
	codeLen    int
	baseURL    string

	analyticsMode string
	clicks        chan string

	mu        sync.Mutex // protege o bloco de IDs (estratégia range)
	blockNext int64
	blockEnd  int64
}

const dbTimeout = 2 * time.Second

func (s *server) observeDB(op, db string, start time.Time) {
	dbDuration.WithLabelValues(op, db).Observe(time.Since(start).Seconds())
}

// --- geração de IDs ---

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func base62(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 62 {
		b = append([]byte{alphabet[n%62]}, b...)
	}
	return string(b)
}

func randomCode(n int) string {
	b := make([]byte, n)
	max := big.NewInt(62)
	for i := range b {
		k, _ := rand.Int(rand.Reader, max)
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}

const blockSize = 1000

func (s *server) nextCode(ctx context.Context) (string, error) {
	switch s.idStrategy {
	case "random":
		// Sem coordenação: sorteia e torce para não colidir.
		return randomCode(s.codeLen), nil

	case "range":
		// Cada instância reserva um bloco de 1000 IDs e gasta em memória:
		// 1 ida ao banco a cada 1000 links, em vez de 1 por link.
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.blockNext >= s.blockEnd {
			start := time.Now()
			var block int64
			err := s.primary.QueryRow(ctx, `SELECT nextval('link_block_seq')`).Scan(&block)
			s.observeDB("id_block", "primary", start)
			if err != nil {
				return "", err
			}
			idBlocks.Inc()
			s.blockNext, s.blockEnd = block*blockSize, (block+1)*blockSize
		}
		id := s.blockNext
		s.blockNext++
		return "r" + base62(id), nil // prefixo evita choque com os códigos do contador

	default: // counter
		// Um contador central no banco: simples, sem colisão, mas é uma ida
		// extra ao primário por link e os códigos são previsíveis (1, 2, 3...).
		start := time.Now()
		var id int64
		err := s.primary.QueryRow(ctx, `SELECT nextval('link_id_seq')`).Scan(&id)
		s.observeDB("id_next", "primary", start)
		return base62(id), err
	}
}

// --- handlers ---

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, `{"error":"informe {\"url\": \"...\"}"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dbTimeout)
	defer cancel()

	for attempt := 0; attempt < 10; attempt++ {
		code, err := s.nextCode(ctx)
		if err != nil {
			http.Error(w, `{"error":"falha ao gerar id"}`, http.StatusServiceUnavailable)
			return
		}
		start := time.Now()
		tag, err := s.primary.Exec(ctx,
			`INSERT INTO links (code, url) VALUES ($1, $2) ON CONFLICT (code) DO NOTHING`, code, req.URL)
		s.observeDB("insert", "primary", start)
		if err != nil {
			http.Error(w, `{"error":"banco indisponível"}`, http.StatusServiceUnavailable)
			return
		}
		if tag.RowsAffected() == 0 { // o código já existia: colisão
			idCollisions.Inc()
			continue
		}
		if s.cache != nil {
			// Write-through: quem acabou de criar já encontra o link no cache,
			// mesmo que a réplica de leitura ainda não o tenha recebido (Lab 5).
			s.cache.Set(ctx, "link:"+code, req.URL, s.ttl)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"code": code, "short_url": s.baseURL + "/" + code})
		return
	}
	http.Error(w, `{"error":"colisões demais ao gerar o código"}`, http.StatusServiceUnavailable)
}

var errNotFound = errors.New("not found")

// resolve é o caminho quente: 99% do tráfego passa por aqui.
func (s *server) resolve(ctx context.Context, code string) (string, error) {
	if s.cache != nil {
		url, err := s.cache.Get(ctx, "link:"+code).Result()
		switch {
		case err == nil:
			cacheRequests.WithLabelValues("hit").Inc()
			return url, nil
		case errors.Is(err, redis.Nil):
			cacheRequests.WithLabelValues("miss").Inc()
		default:
			// Cache fora do ar não pode derrubar o redirect: segue para o banco.
			cacheRequests.WithLabelValues("error").Inc()
		}
	}

	dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
	defer cancel()
	start := time.Now()
	var url string
	err := s.reader.QueryRow(dbCtx, `SELECT url FROM links WHERE code = $1`, code).Scan(&url)
	s.observeDB("select", s.readerN, start)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errNotFound
	}
	if err != nil {
		return "", err
	}
	if s.cache != nil {
		s.cache.Set(ctx, "link:"+code, url, s.ttl) // cache-aside: popula na volta
	}
	return url, nil
}

func (s *server) redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	url, err := s.resolve(r.Context(), code)
	if errors.Is(err, errNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "indisponível", http.StatusServiceUnavailable)
		return
	}
	s.countClick(r.Context(), code)
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), dbTimeout)
	defer cancel()
	var url string
	var clicks int64
	// Cliques = contador da linha (modo sync) + eventos em lote (modo async).
	// O count(*) sem índice serve para o lab; em produção, um agregado pronto.
	err := s.primary.QueryRow(ctx, `
		SELECT url, clicks + (SELECT count(*) FROM click_events e WHERE e.code = l.code)
		FROM links l WHERE code = $1`, r.PathValue("code")).Scan(&url, &clicks)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "indisponível", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"url": url, "clicks": clicks})
}

// --- analytics ---

func (s *server) countClick(ctx context.Context, code string) {
	switch s.analyticsMode {
	case "sync":
		// Ingênuo: todo redirect vira uma ESCRITA no primário, dentro da requisição.
		dbCtx, cancel := context.WithTimeout(ctx, dbTimeout)
		defer cancel()
		start := time.Now()
		_, err := s.primary.Exec(dbCtx, `UPDATE links SET clicks = clicks + 1 WHERE code = $1`, code)
		s.observeDB("click_update", "primary", start)
		if err != nil {
			analyticsEvents.WithLabelValues("error").Inc()
			return
		}
		analyticsEvents.WithLabelValues("written").Inc()
	case "async":
		// O redirect só enfileira; se a fila encher, descarta o clique —
		// nunca atrasa o usuário por causa de estatística.
		select {
		case s.clicks <- code:
			analyticsQueue.Set(float64(len(s.clicks)))
		default:
			analyticsEvents.WithLabelValues("dropped").Inc()
		}
	}
}

// flusher tira os cliques da fila e grava um lote por segundo numa tabela
// só de eventos (append-only, via COPY). Não toca na tabela de links: sem
// disputa de lock nas linhas "quentes" e uma única ida ao banco por segundo.
// Em produção essa fila seria um log durável (Kafka, Kinesis) e a agregação
// viveria fora do banco transacional.
func (s *server) flusher(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var pending [][]any
	flush := func() {
		analyticsQueue.Set(float64(len(s.clicks)))
		if len(pending) == 0 {
			return
		}
		rows := pending
		pending = nil
		dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start := time.Now()
		_, err := s.primary.CopyFrom(dbCtx, pgx.Identifier{"click_events"}, []string{"code", "at"}, pgx.CopyFromRows(rows))
		s.observeDB("click_batch", "primary", start)
		if err != nil {
			analyticsEvents.WithLabelValues("error").Add(float64(len(rows)))
			return
		}
		analyticsEvents.WithLabelValues("written").Add(float64(len(rows)))
	}
	for {
		select {
		case code := <-s.clicks:
			pending = append(pending, []any{code, time.Now()})
		case <-tick.C:
			flush()
		case <-ctx.Done():
			for len(s.clicks) > 0 {
				pending = append(pending, []any{<-s.clicks, time.Now()})
			}
			flush() // desligamento limpo não perde cliques; um kill -9 perde até 1s
			return
		}
	}
}

// --- infraestrutura ---

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

func instrument(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		h(sw, r)
		httpDuration.WithLabelValues(route, strconv.Itoa(sw.status)).Observe(time.Since(start).Seconds())
	}
}

func connect(ctx context.Context, url string, maxConns int) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		log.Fatalf("DATABASE_URL inválida: %v", err)
	}
	cfg.MaxConns = int32(maxConns)
	for {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool
			}
			pool.Close()
		}
		log.Printf("aguardando banco (%s): %v", cfg.ConnConfig.Host, err)
		time.Sleep(time.Second)
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	poolMax := envInt("DB_POOL_MAX", 10)
	s := &server{
		idStrategy:    env("ID_STRATEGY", "counter"),
		codeLen:       envInt("CODE_LENGTH", 7),
		baseURL:       env("BASE_URL", "http://localhost:8080"),
		analyticsMode: env("ANALYTICS_MODE", "off"),
		ttl:           time.Duration(envInt("CACHE_TTL_SECONDS", 300)) * time.Second,
		clicks:        make(chan string, envInt("ANALYTICS_QUEUE_SIZE", 10000)),
		readerN:       "primary",
	}

	s.primary = connect(ctx, env("DATABASE_URL", "postgres://shortener:shortener@pg:5432/shortener"), poolMax)
	registerPoolMetrics("primary", s.primary)
	s.reader = s.primary
	if replica := os.Getenv("DATABASE_REPLICA_URL"); replica != "" {
		s.reader, s.readerN = connect(ctx, replica, poolMax), "replica"
		registerPoolMetrics("replica", s.reader)
	}

	if env("CACHE_ENABLED", "false") == "true" {
		timeout := time.Duration(envInt("REDIS_TIMEOUT_MS", 1000)) * time.Millisecond
		s.cache = redis.NewClient(&redis.Options{
			Addr:        env("REDIS_ADDR", "redis:6379"),
			DialTimeout: timeout, ReadTimeout: timeout, WriteTimeout: timeout,
			MaxRetries: -1, // sem retry: queremos ver o efeito cru do timeout
		})
	}

	var wg sync.WaitGroup
	if s.analyticsMode == "async" {
		wg.Add(1)
		go func() { defer wg.Done(); s.flusher(ctx) }()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/links", instrument("create", s.create))
	mux.HandleFunc("GET /api/links/{code}", instrument("stats", s.stats))
	mux.HandleFunc("GET /{code}", instrument("redirect", s.redirect))
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	srv := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	log.Printf("encurtador no ar | ids=%s cache=%v leituras=%s analytics=%s pool=%d",
		s.idStrategy, s.cache != nil, s.readerN, s.analyticsMode, poolMax)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	wg.Wait()
}
