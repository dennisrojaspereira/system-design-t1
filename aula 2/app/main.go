// Plataforma de cursos e vídeo sob demanda — System Design na Prática, Aula 02.
//
// Um único binário, três papéis. O primeiro argumento escolhe qual:
//
//	vod api      catálogo, busca, upload multipart, playback e progresso  (api.go)
//	vod worker   consome a fila: probe → encode → package → valida → READY (worker.go)
//	vod edge     a "CDN" do laboratório: cache, acesso assinado, rede simulada (edge.go)
//
// Os bytes pesados nunca passam pela API: o upload vai direto ao storage por
// URLs pré-assinadas, e o vídeo sai pela borda.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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

const (
	bucketRaw = "raw" // originais privados
	bucketVOD = "vod" // pacotes HLS versionados
)

func main() {
	role := "api"
	if len(os.Args) > 1 {
		role = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch role {
	case "api":
		runAPI(ctx)
	case "worker":
		runWorker(ctx)
	case "edge":
		runEdge(ctx)
	case "health": // usado pelo healthcheck do Compose: vod health <porta>
		res, err := http.Get("http://127.0.0.1:" + os.Args[2] + "/healthz")
		if err != nil || res.StatusCode != 200 {
			os.Exit(1)
		}
	case "burst": // usado por `make burst N=18`: enfileira N publicações de uma vez
		res, err := http.Post("http://127.0.0.1:8080/api/admin/burst?n="+os.Args[2], "", nil)
		if err != nil {
			log.Fatal(err)
		}
		io.Copy(os.Stdout, res.Body)
	default:
		log.Fatalf("papel desconhecido %q (use api, worker ou edge)", role)
	}
}

// ---------- dependências ----------

func mustDB(ctx context.Context) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(env("DATABASE_URL", "postgres://vod:vod@pg:5432/vod"))
	if err != nil {
		log.Fatalf("DATABASE_URL: %v", err)
	}
	cfg.MaxConns = int32(envInt("DB_POOL_MAX", 10))
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	for i := 0; ; i++ {
		if err = db.Ping(ctx); err == nil {
			return db
		}
		if i == 30 || ctx.Err() != nil {
			log.Fatalf("postgres não respondeu: %v", err)
		}
		time.Sleep(time.Second)
	}
}

// mustS3 fala com o storage compatível com S3. Região fixa: assinar uma URL
// vira conta local, sem ida ao storage.
func mustS3(ctx context.Context) *minio.Client {
	c, err := minio.New(env("S3_ENDPOINT", "s3:7070"), &minio.Options{
		Creds:        credentials.NewStaticV4(env("S3_ACCESS_KEY", "lab"), env("S3_SECRET_KEY", "labsecret"), ""),
		Region:       "us-east-1",
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		log.Fatalf("s3: %v", err)
	}
	for i := 0; ; i++ {
		if _, err = c.BucketExists(ctx, bucketRaw); err == nil {
			return c
		}
		if i == 30 || ctx.Err() != nil {
			log.Fatalf("storage não respondeu: %v", err)
		}
		time.Sleep(time.Second)
	}
}

func ensureBuckets(ctx context.Context, s3 *minio.Client) {
	for _, b := range []string{bucketRaw, bucketVOD} {
		if ok, _ := s3.BucketExists(ctx, b); !ok {
			if err := s3.MakeBucket(ctx, b, minio.MakeBucketOptions{}); err != nil {
				if ok, _ := s3.BucketExists(ctx, b); !ok { // outra instância pode ter criado
					log.Fatalf("criar bucket %s: %v", b, err)
				}
			}
		}
	}
}

// ---------- acesso temporário ao vídeo ----------
//
// A API de playback confere matrícula e emite um token curto para um asset.
// A borda só confere a assinatura: não consulta banco no caminho do vídeo.
// Formato: "<expira-unix>.<sessão>.<hmac(asset|expira|sessão)>", entregue no
// cookie vod_access. Cada play recebe um token diferente.

func playbackSecret() []byte { return []byte(env("PLAYBACK_SECRET", "troque-este-segredo")) }

func accessMAC(asset, exp, session string) string {
	m := hmac.New(sha256.New, playbackSecret())
	m.Write([]byte(asset + "|" + exp + "|" + session))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func signAccess(asset string, exp time.Time) string {
	nonce := make([]byte, 6)
	rand.Read(nonce)
	e, session := strconv.FormatInt(exp.Unix(), 10), hex.EncodeToString(nonce)
	return e + "." + session + "." + accessMAC(asset, e, session)
}

func verifyAccess(asset, token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("acesso ausente ou malformado")
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return fmt.Errorf("acesso malformado")
	}
	if !hmac.Equal([]byte(accessMAC(asset, parts[0], parts[1])), []byte(parts[2])) {
		return fmt.Errorf("assinatura não confere para %s", asset)
	}
	if time.Now().Unix() > exp {
		return fmt.Errorf("acesso expirado")
	}
	return nil
}

// ---------- HTTP ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func serve(ctx context.Context, addr string, h http.Handler) {
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Printf("ouvindo em %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
