#!/usr/bin/env bash
# Orquestra o stack completo NOVO (Postgres real + cmd/server Go real + BFF
# Node.js real + frontend React construído e servido) e roda a suíte
# Playwright contra ele — o mesmo espírito de deploy/playwright/run.sh
# (legado: reset-schema + create-user + saltcorn serve + playwright test),
# aplicado ao stack de migração (GO-021).
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)
cd "$SCRIPT_DIR"

: "${SALTCORN_GO_TEST_DATABASE_URL:?defina SALTCORN_GO_TEST_DATABASE_URL apontando para um Postgres de teste descartável (mesma convenção dos testes Go)}"

BACKEND_DIR="$SCRIPT_DIR/../backend"
BFF_DIR="$SCRIPT_DIR/../packages/bff"
FRONTEND_DIR="$SCRIPT_DIR/../packages/frontend"

E2E_TENANT="${SALTCORN_E2E_TENANT:-e2e_web}"
GO_PORT="${SALTCORN_E2E_GO_PORT:-8091}"
BFF_PORT="${SALTCORN_E2E_BFF_PORT:-3101}"
FRONTEND_PORT="${SALTCORN_E2E_FRONTEND_PORT:-4173}"
# Segredo só deste harness descartável — nunca um segredo real, nunca commitado com valor de produção.
SERVICE_IDENTITY_SECRET="e2e-harness-secret-32-bytes-min!"

GO_BIN=$(mktemp)
BFF_OUT=$(mktemp)
GO_PID=""
BFF_PID=""
FRONTEND_PID=""

# Achados desta tarefa sobre limpeza de processos de fundo — os dois
# juntos causavam uma falha muito difícil de diagnosticar (createTable
# nunca chegava ao Go, sem NENHUM erro do lado Go, porque o fetch() do
# navegador ia para um processo de uma execução ANTERIOR já morta, com um
# tenant/sessão que não existem mais):
# 1. `(cd DIR && cmd) &` cria uma SUBSHELL — `$!`/`jobs -p` capturam o PID
#    dela, não o do processo real (`npx`/`vite` ainda spawnam processos
#    FILHOS). Corrigido: cada processo de fundo roda via `setsid` (cria seu
#    próprio grupo de processos, com o PID inicial = PGID do grupo inteiro)
#    e caminho de binário direto (sem `npx`, sem `cd`+subshell — `vite
#    preview` aceita `[root]` como argumento posicional).
# 2. Como salvaguarda adicional, as portas usadas são liberadas ANTES de
#    começar (`fuser -k`), não só limpas no fim — cobre o caso de uma
#    execução anterior ter sido interrompida de um jeito que nem o cleanup
#    chegou a rodar.
for port in "$GO_PORT" "$BFF_PORT" "$FRONTEND_PORT"; do
  fuser -k -TERM "${port}/tcp" >/dev/null 2>&1 || true
done
sleep 0.3

cleanup() {
  rm -f "$GO_BIN" "$BFF_OUT"
  # `-PID` mata o GRUPO inteiro criado pelo setsid correspondente (server
  # Go, ou BFF, ou vite preview, incluindo qualquer processo filho que
  # cada um tenha spawnado) — não só o processo imediato.
  [ -n "$FRONTEND_PID" ] && kill -- "-$FRONTEND_PID" 2>/dev/null || true
  [ -n "$BFF_PID" ] && kill -- "-$BFF_PID" 2>/dev/null || true
  [ -n "$GO_PID" ] && kill -- "-$GO_PID" 2>/dev/null || true
}
trap cleanup EXIT

wait_for_port() {
  local port=$1
  local tries=0
  while ! (exec 3<>"/dev/tcp/localhost/$port") 2>/dev/null; do
    tries=$((tries + 1))
    if [ "$tries" -gt 100 ]; then
      echo "timeout esperando a porta $port abrir" >&2
      exit 1
    fi
    sleep 0.2
  done
  exec 3<&- 3>&- 2>/dev/null || true
}

echo "==> Semeando tenant/admin/ownership (cli e2e-seed)..."
SEED_JSON=$(cd "$BACKEND_DIR" && go run ./cmd/cli e2e-seed --dsn "$SALTCORN_GO_TEST_DATABASE_URL" --tenant "$E2E_TENANT")
echo "    $SEED_JSON"
ADMIN_USER_ID=$(node -e "console.log(JSON.parse(process.argv[1]).admin_user_id)" "$SEED_JSON")

echo "==> Compilando backend Go..."
(cd "$BACKEND_DIR" && go build -o "$GO_BIN" ./cmd/server)

echo "==> Iniciando backend Go na porta $GO_PORT..."
SALTCORN_GO_HTTP_ADDR=":$GO_PORT" \
  SALTCORN_GO_DATABASE_URL="$SALTCORN_GO_TEST_DATABASE_URL" \
  SALTCORN_GO_SERVICE_IDENTITY_SECRET="$SERVICE_IDENTITY_SECRET" \
  setsid "$GO_BIN" &
GO_PID=$!
wait_for_port "$GO_PORT"
echo "    backend Go pronto."

echo "==> Compilando BFF..."
(cd "$BFF_DIR" && npm run build --silent)

echo "==> Iniciando BFF na porta $BFF_PORT (sessão pré-semeada para o admin criado acima)..."
SALTCORN_BFF_HTTP_ADDR=":$BFF_PORT" \
  SALTCORN_BFF_GO_INTERNAL_API_URL="http://localhost:$GO_PORT" \
  SALTCORN_BFF_SERVICE_IDENTITY_SECRET="$SERVICE_IDENTITY_SECRET" \
  SALTCORN_E2E_TENANT="$E2E_TENANT" \
  SALTCORN_E2E_ADMIN_USER_ID="$ADMIN_USER_ID" \
  setsid node scripts/start-bff.mjs >"$BFF_OUT" 2>&1 &
BFF_PID=$!
wait_for_port "$BFF_PORT"
for _ in $(seq 1 50); do
  [ -s "$BFF_OUT" ] && break
  sleep 0.1
done
BFF_SESSION_JSON=$(cat "$BFF_OUT")
echo "    BFF pronto: $BFF_SESSION_JSON"

# VITE_BFF_BASE_URL vazio (mesma origem) + proxy do vite preview para o
# BFF real (vite.config.ts) — evita CORS entre a porta do frontend e a do
# BFF, a mesma topologia de "mesma origem via proxy reverso" documentada
# para produção (achado desta tarefa: sem isso, todo fetch() do navegador
# para o BFF é bloqueado por CORS — ver docs/migracao-go/execucoes/GO-021.md).
echo "==> Compilando frontend (VITE_BFF_BASE_URL vazio — proxy do vite preview cobre o cross-origin)..."
(cd "$FRONTEND_DIR" && VITE_BFF_BASE_URL="" npm run build --silent)

echo "==> Servindo frontend na porta $FRONTEND_PORT (proxy /api/bff -> localhost:$BFF_PORT)..."
SALTCORN_DEV_BFF_PROXY_TARGET="http://localhost:$BFF_PORT" \
  setsid "$FRONTEND_DIR/node_modules/.bin/vite" preview "$FRONTEND_DIR" --port "$FRONTEND_PORT" --strictPort &
FRONTEND_PID=$!
wait_for_port "$FRONTEND_PORT"
echo "    frontend pronto."

export SALTCORN_E2E_SESSION_JSON="$BFF_SESSION_JSON"
export SALTCORN_E2E_FRONTEND_PORT="$FRONTEND_PORT"
export SALTCORN_E2E_GO_BASE_URL="http://localhost:$GO_PORT"
export SALTCORN_E2E_BFF_BASE_URL="http://localhost:$BFF_PORT"
export SALTCORN_E2E_TENANT="$E2E_TENANT"

echo "==> Rodando Playwright..."
npx playwright test "$@"
