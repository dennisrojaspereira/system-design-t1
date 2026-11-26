#!/usr/bin/env python3
"""Painel do laboratório: uma página para subir os labs, aplicar carga,
derrubar serviços e consultar endereços e senhas.

    python3 painel/server.py        (ou: make painel)

Só usa a biblioteca padrão do Python. Escuta apenas em 127.0.0.1 e executa
exclusivamente os mesmos comandos `docker compose` do Makefile.
"""
import json
import os
import re
import shutil
import signal
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

ROOT = Path(__file__).resolve().parent.parent
PORT = int(os.environ.get("PAINEL_PORT", "7070"))
PROJECT = "sd-aula01"
ALL = ["--profile", "ha", "--profile", "replica", "--profile", "tools"]

SERVICES = ["app1", "app2", "app3", "lb", "pg", "pg-replica", "redis",
            "prometheus", "grafana", "pg-exporter", "pg-replica-exporter"]
SERVICE_ACTIONS = {"kill", "start", "pause", "unpause", "restart"}
K6_SCRIPTS = {"read", "ramp", "write", "mixed"}
K6_VARS = {"RPS", "DURATION", "MAX_RPS", "STEPS", "STEP_DURATION", "WRITE_RPS", "SKEW", "LINKS"}
K6_VALUE = re.compile(r"^[0-9]+(\.[0-9]+)?(ms|s|m|h)?$")
ANSI = re.compile(r"\x1b\[[0-9;?]*[A-Za-z]")


def detect_dc():
    """`docker compose` (plugin) ou `docker-compose` (binário antigo)."""
    try:
        if subprocess.run(["docker", "compose", "version"], capture_output=True).returncode == 0:
            return ["docker", "compose"]
    except OSError:
        pass
    return ["docker-compose"]


DC = detect_dc()

# ---------- log compartilhado ----------
_lock = threading.Lock()
_log = []          # linhas
_log_base = 0      # índice global da primeira linha guardada
LOG_MAX = 4000


def log(line):
    global _log_base
    line = ANSI.sub("", line.rstrip("\n")).split("\r")[-1]
    with _lock:
        _log.append(line)
        if len(_log) > LOG_MAX:
            drop = len(_log) - LOG_MAX
            del _log[:drop]
            _log_base += drop


def log_since(since):
    with _lock:
        start = max(since - _log_base, 0)
        return _log[start:], _log_base + len(_log)


def run_logged(argv, holder=None):
    """Executa um comando despejando a saída no log. Devolve o exit code."""
    log("$ " + " ".join(argv))
    kwargs = {}
    if os.name == "posix":
        kwargs["start_new_session"] = True
    try:
        proc = subprocess.Popen(argv, cwd=str(ROOT), stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, **kwargs)
    except OSError as e:
        log("erro: %s" % e)
        return 127
    if holder is not None:
        holder["proc"] = proc
    for raw in proc.stdout:
        log(raw.decode("utf-8", "replace"))
    return proc.wait()


# ---------- tarefa longa (uma por vez) ----------
job = {"name": None, "kind": None, "started": 0, "proc": None, "cancel": False}
last = {"name": None, "ok": None, "ended": 0}


def start_job(name, kind, steps, before=None):
    with _lock:
        if job["name"]:
            return "Já existe uma tarefa em andamento: %s" % job["name"]
        job.update(name=name, kind=kind, started=time.time(), proc=None, cancel=False)

    def work():
        ok = True
        log("")
        log("━━ %s" % name)
        try:
            if before:
                before()
            for argv in steps:
                if job["cancel"]:
                    ok = False
                    break
                code = run_logged(argv, job)
                if code != 0:
                    ok = False
                    if not job["cancel"]:
                        log("✗ comando terminou com código %d" % code)
                    break
        except Exception as e:  # noqa: BLE001 - o painel não pode morrer
            ok = False
            log("erro: %s" % e)
        cancelled = job["cancel"]
        log("■ interrompido" if cancelled else ("✓ concluído: %s" % name if ok else "✗ falhou: %s" % name))
        with _lock:
            last.update(name=name, ok=ok and not cancelled, ended=time.time())
            job.update(name=None, kind=None, proc=None, cancel=False)
        refresh_services()

    threading.Thread(target=work, daemon=True).start()
    return None


def stop_job():
    proc = job["proc"]
    if not job["name"]:
        return "Nenhuma tarefa em andamento."
    job["cancel"] = True
    if job["kind"] == "load":
        # O k6 roda num container próprio: mata o container, o `run` retorna.
        out = subprocess.run(
            ["docker", "ps", "-q", "--filter", "label=com.docker.compose.project=" + PROJECT,
             "--filter", "label=com.docker.compose.service=k6"],
            capture_output=True, text=True).stdout.split()
        if out:
            subprocess.run(["docker", "kill"] + out, capture_output=True)
    if proc and proc.poll() is None:
        try:
            if os.name == "posix":
                os.killpg(proc.pid, signal.SIGINT)
            else:
                proc.terminate()
        except OSError:
            pass

        def hard():
            time.sleep(8)
            if proc.poll() is None:
                try:
                    os.killpg(proc.pid, signal.SIGKILL) if os.name == "posix" else proc.kill()
                except OSError:
                    pass
        threading.Thread(target=hard, daemon=True).start()
    return None


# ---------- estado ----------
def read_env():
    try:
        return (ROOT / ".env").read_text(encoding="utf-8")
    except OSError:
        return ""


def env_vars(text):
    out = {}
    for line in text.splitlines():
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip()
    return out


def current_lab(text):
    m = re.match(r"\s*#\s*Lab\s*(\d)", text)
    return int(m.group(1)) if m else None


_services = {"at": 0, "list": [], "error": None}
_last_poll = 0


def refresh_services():
    try:
        r = subprocess.run(DC + ALL + ["ps", "-a", "--format", "json"], cwd=str(ROOT),
                           capture_output=True, text=True, timeout=20)
    except (OSError, subprocess.TimeoutExpired) as e:
        _services.update(at=time.time(), list=[], error=str(e))
        return
    if r.returncode != 0:
        _services.update(at=time.time(), list=[], error=(r.stderr or r.stdout).strip()[-400:])
        return
    rows = []
    text = r.stdout.strip()
    try:  # versões diferentes do compose: um array JSON ou um objeto por linha
        parsed = json.loads(text) if text.startswith("[") else [json.loads(l) for l in text.splitlines() if l.strip()]
    except ValueError:
        parsed = []
    for c in parsed:
        if c.get("Service") == "k6":
            continue
        rows.append({"service": c.get("Service"), "state": c.get("State"),
                     "health": c.get("Health") or "", "status": c.get("Status") or ""})
    order = {s: i for i, s in enumerate(SERVICES)}
    rows.sort(key=lambda x: order.get(x["service"], 99))
    _services.update(at=time.time(), list=rows, error=None)


def services_loop():
    while True:
        if time.time() - _last_poll < 15:
            refresh_services()
        time.sleep(2)


def state(since):
    global _last_poll
    _last_poll = time.time()
    env = read_env()
    v = env_vars(env)
    lines, nxt = log_since(since)
    return {
        "dc": " ".join(DC),
        "lab": current_lab(env),
        "env": env,
        # Mesma precedência do Compose: variável de ambiente, depois o .env, depois o padrão.
        "ports": {k: os.environ.get(name) or v.get(name) or default for k, name, default in (
            ("app", "APP_PORT", "8080"), ("lb", "LB_PORT", "8000"), ("grafana", "GRAFANA_PORT", "3000"),
            ("prom", "PROM_PORT", "9090"), ("ui", "UI_PORT", "8090"))},
        "job": {"name": job["name"], "kind": job["kind"], "started": job["started"]} if job["name"] else None,
        "last": last,
        "services": _services["list"],
        "servicesError": _services["error"],
        "log": lines,
        "next": nxt,
    }


# ---------- ações ----------
def act_lab(body):
    n = body.get("n")
    if n not in (1, 2, 3, 4, 5, 6):
        return "Lab inválido."
    src = ROOT / "labs" / ("lab%d.env" % n)
    return start_job("Subir o Lab %d" % n, "stack", [
        DC + ALL + ["down", "--remove-orphans"],
        DC + ["build", "app1"],
        DC + ["up", "-d", "--wait"],
    ], before=lambda: (shutil.copyfile(str(src), str(ROOT / ".env")), log("cp labs/lab%d.env .env" % n)))


def act_apply(body):
    content = body.get("env")
    if content is not None:
        if not isinstance(content, str) or len(content) > 20000:
            return ".env inválido."
        if job["name"]:
            return "Já existe uma tarefa em andamento: %s" % job["name"]
        (ROOT / ".env").write_text(content if content.endswith("\n") else content + "\n", encoding="utf-8")
        log("")
        log("✎ .env salvo")
    return start_job("Aplicar o .env", "stack", [DC + ["up", "-d", "--wait"]])


def act_load(body):
    script = body.get("script")
    if script not in K6_SCRIPTS:
        return "Script inválido."
    envs = []
    for k, val in (body.get("vars") or {}).items():
        val = str(val).strip()
        if not val:
            continue
        if k not in K6_VARS or not K6_VALUE.match(val):
            return "Valor inválido para %s: %s" % (k, val)
        envs += ["-e", "%s=%s" % (k, val)]
    return start_job("Carga: %s" % script, "load",
                     [DC + ["run", "--rm", "-T"] + envs + ["k6", "run", "/scripts/%s.js" % script]])


def act_service(body):
    svc, action = body.get("service"), body.get("action")
    if action == "flushall":
        argv = DC + ["exec", "-T", "redis", "redis-cli", "FLUSHALL"]
    elif svc in SERVICES and action in SERVICE_ACTIONS:
        argv = DC + [action] + (["-s", "KILL"] if action == "kill" else []) + [svc]
    else:
        return "Ação inválida."
    if job["kind"] == "stack":
        return "Aguarde a tarefa em andamento: %s" % job["name"]

    def work():
        log("")
        run_logged(argv)
        refresh_services()
    threading.Thread(target=work, daemon=True).start()
    return None


def act_down(body):
    return start_job("Parar tudo (mantém os dados)", "stack", [DC + ALL + ["down", "--remove-orphans"]])


def act_reset(body):
    return start_job("Apagar tudo (banco e métricas)", "stack", [DC + ALL + ["down", "-v", "--remove-orphans"]])


ROUTES = {"/api/lab": act_lab, "/api/apply": act_apply, "/api/load": act_load,
          "/api/service": act_service, "/api/down": act_down, "/api/reset": act_reset,
          "/api/stop": lambda body: stop_job()}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def _local(self):
        """Só aceita chamadas feitas pela própria página (bloqueia outros sites)."""
        allowed = {"localhost:%d" % PORT, "127.0.0.1:%d" % PORT}
        if self.headers.get("Host") not in allowed:
            return False
        origin = self.headers.get("Origin")
        return origin is None or origin in {"http://" + h for h in allowed}

    def _send(self, code, body, ctype="application/json; charset=utf-8"):
        data = body if isinstance(body, bytes) else json.dumps(body, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if not self._local():
            return self._send(403, {"error": "acesso negado"})
        url = urlparse(self.path)
        if url.path == "/":
            return self._send(200, (Path(__file__).parent / "index.html").read_bytes(), "text/html; charset=utf-8")
        if url.path == "/api/state":
            try:
                since = int(parse_qs(url.query).get("since", ["0"])[0])
            except ValueError:
                since = 0
            return self._send(200, state(since))
        self._send(404, {"error": "não encontrado"})

    def do_POST(self):
        if not self._local():
            return self._send(403, {"error": "acesso negado"})
        fn = ROUTES.get(urlparse(self.path).path)
        if not fn:
            return self._send(404, {"error": "não encontrado"})
        try:
            n = int(self.headers.get("Content-Length") or 0)
            body = json.loads(self.rfile.read(n) or b"{}") if n else {}
            if not isinstance(body, dict):
                raise ValueError
        except ValueError:
            return self._send(400, {"error": "JSON inválido"})
        err = fn(body)
        self._send(409 if err else 200, {"error": err} if err else {"ok": True})


def main():
    threading.Thread(target=services_loop, daemon=True).start()
    try:
        srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    except OSError as e:
        raise SystemExit("Não consegui abrir a porta %d (%s). Tente: PAINEL_PORT=7071 make painel" % (PORT, e))
    srv.daemon_threads = True
    log("Painel pronto. Comando do compose: %s" % " ".join(DC))
    print("\n  Painel do laboratório  http://localhost:%d\n\n  Ctrl+C para encerrar (os containers continuam rodando).\n" % PORT, flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        print()


if __name__ == "__main__":
    main()
