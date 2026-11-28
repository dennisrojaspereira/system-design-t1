// Edge: a "CDN" do laboratório. Um ponto de presença só, cache em memória.
// Faz o que importa para a aula e deixa o resto de fora:
//
//   - confere o acesso assinado antes de servir (sem consultar o banco);
//
//   - guarda playlists e segmentos e responde X-Cache: HIT ou MISS;
//
//   - busca na origem privada (o bucket vod) quando não tem o objeto;
//
//   - simula a distância até a origem e a banda do aluno.
//
//     CACHE_KEY          path | path+cookie   o que identifica um objeto no cache (Lab 4)
//     CACHE_TTL_S        validade de uma entrada; os caminhos são versionados   (Lab 4)
//     CACHE_MAX_MB       tamanho do cache; cheio, sai o objeto mais antigo      (Lab 4)
//     ORIGIN_LATENCY_MS  atraso somado a cada ida à origem                      (Lab 4)
//     NET_KBPS           banda simulada até o player; 0 = sem limite            (Lab 3)
//
// Latência, banda e queda da origem também mudam em tempo de execução por
// /edge/admin, para a interface controlar sem reiniciar nada.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	edgeRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edge_requests_total", Help: "Requisições na borda por resultado de cache e tipo de arquivo.",
	}, []string{"cache", "kind"}) // cache: HIT | MISS | DENIED | ERROR ; kind: playlist | segment
	edgeBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edge_bytes_total", Help: "Bytes entregues ao player por resultado de cache.",
	}, []string{"cache"})
	edgeTTFB = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "edge_ttfb_seconds", Help: "Tempo até o primeiro byte, por resultado de cache.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .2, .3, .5, .75, 1, 1.5, 2.5},
	}, []string{"cache"})
	originRequests = promauto.NewCounter(prometheus.CounterOpts{Name: "origin_requests_total", Help: "Idas à origem."})
	originBytes    = promauto.NewCounter(prometheus.CounterOpts{Name: "origin_bytes_total", Help: "Bytes buscados na origem."})
	edgeEntries    = promauto.NewGauge(prometheus.GaugeOpts{Name: "edge_cache_entries", Help: "Objetos no cache da borda."})
	edgeEvictions  = promauto.NewCounter(prometheus.CounterOpts{Name: "edge_cache_evictions_total", Help: "Objetos expulsos por falta de espaço."})
	edgeCacheBytes = promauto.NewGauge(prometheus.GaugeOpts{Name: "edge_cache_bytes", Help: "Bytes no cache da borda."})
)

type cacheEntry struct {
	body    []byte
	ctype   string
	expires time.Time
}

type flight struct {
	done chan struct{}
	ent  *cacheEntry
	err  error
}

type edge struct {
	s3       *minio.Client
	cacheKey string
	ttl      time.Duration

	mu      sync.RWMutex
	cache   map[string]*cacheEntry
	order   []string // ordem de chegada: quando o cache enche, sai o mais antigo
	size    int64
	maxSize int64

	// Estado ajustável em tempo de execução.
	latencyMs  atomic.Int64
	netKbps    atomic.Int64
	originDown atomic.Bool

	// Buscas em andamento na origem, por chave de cache.
	flightMu sync.Mutex
	flights  map[string]*flight

	// Banda simulada: um balde só, compartilhado por todas as conexões.
	netMu   sync.Mutex
	netNext time.Time
}

func runEdge(ctx context.Context) {
	e := &edge{
		s3:       mustS3(ctx),
		cacheKey: env("CACHE_KEY", "path"),
		ttl:      time.Duration(envInt("CACHE_TTL_S", 3600)) * time.Second,
		cache:    map[string]*cacheEntry{},
		flights:  map[string]*flight{},
		maxSize:  int64(envInt("CACHE_MAX_MB", 512)) << 20,
	}
	e.latencyMs.Store(int64(envInt("ORIGIN_LATENCY_MS", 150)))
	e.netKbps.Store(int64(envInt("NET_KBPS", 0)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /vod/", e.serveVOD)
	mux.HandleFunc("GET /edge/admin/state", e.state)
	mux.HandleFunc("POST /edge/admin/state", e.setState)
	mux.HandleFunc("POST /edge/admin/purge", e.purge)
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	log.Printf("edge: chave de cache = %s, TTL = %s, origem a %d ms", e.cacheKey, e.ttl, e.latencyMs.Load())
	serve(ctx, ":8081", mux)
}

func (e *edge) serveVOD(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	key := strings.TrimPrefix(r.URL.Path, "/vod/") // a7/t1/720p/seg_003.m4s
	asset, _, _ := strings.Cut(key, "/")
	kind := "segment"
	if strings.HasSuffix(key, ".m3u8") {
		kind = "playlist"
	}

	// 1. Autorização na borda: o cookie assinado vale para o asset e tem prazo.
	token := ""
	if c, err := r.Cookie("vod_access"); err == nil {
		token = c.Value
	}
	if err := verifyAccess(asset, token); err != nil {
		edgeRequests.WithLabelValues("DENIED", kind).Inc()
		http.Error(w, "acesso negado: "+err.Error(), http.StatusForbidden)
		return
	}

	// 2. Identidade do objeto no cache. Com "path", todos os alunos compartilham
	// a mesma entrada. Com "path+cookie", cada sessão abre a sua: o hit ratio despenca.
	ck := key
	if e.cacheKey == "path+cookie" {
		ck = key + "|" + token
	}

	e.mu.RLock()
	ent := e.cache[ck]
	e.mu.RUnlock()
	status := "HIT"
	if ent == nil || time.Now().After(ent.expires) {
		status = "MISS"
		var err error
		if ent, err = e.fetchOnce(r.Context(), ck, key); err != nil {
			edgeRequests.WithLabelValues("ERROR", kind).Inc()
			code := http.StatusBadGateway
			if minio.ToErrorResponse(err).StatusCode == http.StatusNotFound {
				code = http.StatusNotFound
			}
			http.Error(w, "origem: "+err.Error(), code)
			return
		}
	}

	ttfb := time.Since(start)
	edgeRequests.WithLabelValues(status, kind).Inc()
	edgeTTFB.WithLabelValues(status).Observe(ttfb.Seconds())
	edgeBytes.WithLabelValues(status).Add(float64(len(ent.body)))

	h := w.Header()
	h.Set("Content-Type", ent.ctype)
	h.Set("Content-Length", strconv.Itoa(len(ent.body)))
	h.Set("X-Cache", status)
	// Em produção: "public, max-age=31536000, immutable" — o caminho leva a versão,
	// o conteúdo nunca muda. Aqui o navegador não guarda nada, para toda requisição
	// passar pela borda e aparecer nas métricas.
	h.Set("Cache-Control", "no-store")
	h.Set("Server-Timing", "edge;dur="+strconv.FormatFloat(float64(ttfb.Microseconds())/1000, 'f', 1, 64)+`;desc="`+status+`"`)
	h.Set("Timing-Allow-Origin", "*")
	h.Set("Access-Control-Expose-Headers", "X-Cache, Server-Timing")
	e.writeThrottled(w, r, ent.body)
}

func (e *edge) store(ck string, ent *cacheEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if old := e.cache[ck]; old != nil {
		e.size -= int64(len(old.body))
	} else {
		e.order = append(e.order, ck)
	}
	e.cache[ck] = ent
	e.size += int64(len(ent.body))
	for e.size > e.maxSize && len(e.order) > 1 {
		victim := e.order[0]
		e.order = e.order[1:]
		e.size -= int64(len(e.cache[victim].body))
		delete(e.cache, victim)
		edgeEvictions.Inc()
	}
	edgeEntries.Set(float64(len(e.cache)))
	edgeCacheBytes.Set(float64(e.size))
}

// fetchOnce junta requisições iguais: se cem alunos pedem o mesmo segmento
// frio ao mesmo tempo, só uma ida à origem acontece e os outros esperam por ela.
// (Nas CDNs isso se chama request collapsing.) Todos contam como MISS.
func (e *edge) fetchOnce(ctx context.Context, ck, key string) (*cacheEntry, error) {
	e.flightMu.Lock()
	if f := e.flights[ck]; f != nil {
		e.flightMu.Unlock()
		select {
		case <-f.done:
			return f.ent, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	e.flights[ck] = f
	e.flightMu.Unlock()

	// Contexto próprio: se quem iniciou a busca desistir, os que esperam não perdem a resposta.
	fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	f.ent, f.err = e.fetch(fctx, key)
	cancel()
	if f.err == nil {
		e.store(ck, f.ent)
	}
	e.flightMu.Lock()
	delete(e.flights, ck)
	e.flightMu.Unlock()
	close(f.done)
	return f.ent, f.err
}

// fetch vai à origem privada. Só a borda tem credencial para ler o bucket vod.
func (e *edge) fetch(ctx context.Context, key string) (*cacheEntry, error) {
	originRequests.Inc()
	if e.originDown.Load() {
		return nil, context.DeadlineExceeded
	}
	// A distância até a origem: em produção são centenas de ms entre continentes.
	if ms := e.latencyMs.Load(); ms > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(ms) * time.Millisecond):
		}
	}
	obj, err := e.s3.GetObject(ctx, bucketVOD, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	st, err := obj.Stat()
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(obj)
	if err != nil {
		return nil, err
	}
	originBytes.Add(float64(len(body)))
	return &cacheEntry{body: body, ctype: st.ContentType, expires: time.Now().Add(e.ttl)}, nil
}

// writeThrottled entrega o corpo respeitando a banda simulada (NET_KBPS).
func (e *edge) writeThrottled(w http.ResponseWriter, r *http.Request, body []byte) {
	const chunk = 16 << 10
	fl, _ := w.(http.Flusher)
	for len(body) > 0 {
		n := min(chunk, len(body))
		if kbps := e.netKbps.Load(); kbps > 0 {
			d := time.Duration(float64(n*8) / float64(kbps*1000) * float64(time.Second))
			e.netMu.Lock()
			now := time.Now()
			if e.netNext.Before(now) {
				e.netNext = now
			}
			e.netNext = e.netNext.Add(d)
			wait := e.netNext.Sub(now)
			e.netMu.Unlock()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(wait):
			}
		}
		if _, err := w.Write(body[:n]); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
		body = body[n:]
	}
}

// ---------- administração (só para o laboratório) ----------

type edgeState struct {
	CacheKey        string `json:"cacheKey"`
	TTLSeconds      int    `json:"ttlSeconds"`
	OriginLatencyMs *int64 `json:"originLatencyMs,omitempty"`
	NetKbps         *int64 `json:"netKbps,omitempty"`
	OriginDown      *bool  `json:"originDown,omitempty"`
	Entries         int    `json:"entries"`
	Bytes           int64  `json:"bytes"`
}

func (e *edge) state(w http.ResponseWriter, r *http.Request) {
	lat, net, down := e.latencyMs.Load(), e.netKbps.Load(), e.originDown.Load()
	e.mu.RLock()
	s := edgeState{e.cacheKey, int(e.ttl.Seconds()), &lat, &net, &down, len(e.cache), e.size}
	e.mu.RUnlock()
	writeJSON(w, 200, s)
}

func (e *edge) setState(w http.ResponseWriter, r *http.Request) {
	var in edgeState
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeErr(w, 400, "corpo inválido")
		return
	}
	if in.OriginLatencyMs != nil {
		e.latencyMs.Store(max(*in.OriginLatencyMs, 0))
	}
	if in.NetKbps != nil {
		e.netKbps.Store(max(*in.NetKbps, 0))
		e.netMu.Lock()
		e.netNext = time.Time{}
		e.netMu.Unlock()
	}
	if in.OriginDown != nil {
		e.originDown.Store(*in.OriginDown)
	}
	e.state(w, r)
}

// purge esvazia o cache: é assim que se simula uma região fria.
func (e *edge) purge(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	n := len(e.cache)
	e.cache = map[string]*cacheEntry{}
	e.order = nil
	e.size = 0
	edgeEntries.Set(0)
	edgeCacheBytes.Set(0)
	e.mu.Unlock()
	writeJSON(w, 200, map[string]int{"purged": n})
}
