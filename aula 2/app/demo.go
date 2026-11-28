// Demo: mantém o laboratório "vivo" para a aula. Em vez de a turma esperar um
// encode ou uma rampa de carga, este papel gera atividade contínua e plausível:
// alunos assistindo pela borda, checkpoints, logins, um play negado de vez em
// quando e pequenas rajadas de publicação. O Grafana fica com dados em todos os
// painéis desde o primeiro minuto.
//
//	make demo        liga      (DEMO_VIEWERS=8 por padrão)
//	make demo-stop   desliga
//
// É tráfego de verdade contra a API e a borda — só quem assiste é que é simulado.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

type demo struct {
	api, edge string
	http      *http.Client
}

func runDemo(ctx context.Context) {
	d := &demo{api: env("API", "http://api:8080"), edge: env("EDGE", "http://edge:8081"), http: &http.Client{Timeout: 30 * time.Second}}
	n := envInt("DEMO_VIEWERS", 8)
	log.Printf("demo: %d espectadores simulados, com logins, checkpoints e rajadas", n)
	for i := 0; i < n; i++ {
		go d.viewer(ctx, i)
	}
	logins, bursts := time.NewTicker(7*time.Second), time.NewTicker(50*time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-logins.C:
			method := []string{"senha+2fa", "senha+2fa", "passkey"}[rand.IntN(3)]
			d.call(ctx, "POST", d.api+"/idp/token", "", map[string]any{"user": "ana", "method": method}, nil)
			if rand.IntN(3) == 0 { // bia se autentica, mas não tem matrícula no curso 1
				d.call(ctx, "POST", d.api+"/api/lessons/1/playback", "bia", nil, nil)
			}
		case <-bursts.C:
			d.call(ctx, "POST", fmt.Sprintf("%s/api/admin/burst?n=%d", d.api, 2+rand.IntN(3)), "", nil, nil)
		}
	}
}

// viewer assiste à aula 1 em laço: play, playlists, segmentos e checkpoints.
func (d *demo) viewer(ctx context.Context, id int) {
	user := fmt.Sprintf("demo-%d", id)
	d.sleep(ctx, time.Duration(rand.IntN(8000))*time.Millisecond)
	for ctx.Err() == nil {
		var pb struct{ ManifestURL, Access string }
		t0 := time.Now()
		if d.call(ctx, "POST", d.api+"/api/lessons/1/playback", "ana", nil, &pb) != 200 {
			d.sleep(ctx, 5*time.Second) // a aula ainda não está READY
			continue
		}
		var sess struct{ Generation int }
		d.call(ctx, "POST", d.api+"/api/lessons/1/progress/session", user, nil, &sess)

		base := d.edge + strings.TrimSuffix(pb.ManifestURL, "master.m3u8")
		d.get(ctx, base+"master.m3u8", pb.Access)
		variant := []string{"480p", "720p", "720p", "1080p", "1080p"}[rand.IntN(5)]
		bitrate := map[string]float64{"480p": 1e6, "720p": 2.5e6, "1080p": 5e6}[variant]
		index := d.get(ctx, base+variant+"/index.m3u8", pb.Access)
		seq := 0
		for i, line := range strings.Split(index, "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			d.get(ctx, base+variant+"/"+line, pb.Access)
			tel := map[string]any{"watchedMs": 6000, "bitrateBps": bitrate}
			if seq == 0 { // primeiro quadro = tudo o que veio antes do primeiro segmento tocar
				tel["startupMs"] = float64(time.Since(t0).Milliseconds()) + 150 + rand.Float64()*250
			}
			if rand.IntN(40) == 0 { // um travamento ocasional, como numa rede móvel
				tel["stallMs"] = 400 + rand.Float64()*1800
			}
			d.call(ctx, "POST", d.api+"/api/telemetry", user, tel, nil)
			seq++
			d.call(ctx, "PUT", d.api+"/api/lessons/1/progress", user,
				map[string]any{"generation": sess.Generation, "seq": seq, "positionMs": i * 3000}, nil)
			d.sleep(ctx, time.Duration(1500+rand.IntN(1500))*time.Millisecond)
		}
		d.sleep(ctx, time.Duration(2+rand.IntN(6))*time.Second)
	}
}

func (d *demo) sleep(ctx context.Context, t time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(t):
	}
}

func (d *demo) call(ctx context.Context, method, url, user string, body, out any) int {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, url, rd)
	if user != "" {
		req.Header.Set("X-User", user)
	}
	res, err := d.http.Do(req)
	if err != nil {
		return 0
	}
	defer res.Body.Close()
	if out != nil && res.StatusCode == 200 {
		json.NewDecoder(res.Body).Decode(out)
	}
	io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func (d *demo) get(ctx context.Context, url, access string) string {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Cookie", "vod_access="+access)
	res, err := d.http.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if strings.HasSuffix(url, ".m3u8") {
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	io.Copy(io.Discard, res.Body)
	return ""
}
