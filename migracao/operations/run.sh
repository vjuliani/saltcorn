#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${SALTCORN_GO_TEST_DATABASE_URL:?Banco exclusivo go035_load obrigatório}"
: "${GO035_DISPOSABLE:?Defina GO035_DISPOSABLE=1}"
OUTPUT="${1:?Diretório novo de evidências obrigatório}"
# Node preflight validates destination before any build or persistent effect.
node migracao/operations/preflight.mjs "$OUTPUT"
OUTPUT="$(realpath "$OUTPUT")"
for package in bff frontend pluginhost; do
  (cd "migracao/packages/$package" && npm ci && npm run build) > "$OUTPUT/build-$package.log" 2>&1
done
(cd migracao/e2e && npm ci && npx playwright install chromium) > "$OUTPUT/build-browser.log" 2>&1
(cd migracao/backend && go build -o "$OUTPUT/server" ./cmd/server && go build -o "$OUTPUT/cli" ./cmd/cli && go build -o "$OUTPUT/worker" ./cmd/worker)
node migracao/operations/run.mjs "$OUTPUT"
(cd migracao/backend && go test -race -count=1 -json ./internal/platform/telemetry ./internal/platform/outbox ./internal/platform/lease ./internal/scheduler ./internal/pluginhost ./internal/files) > "$OUTPUT/fault-tests.jsonl"
node migracao/operations/gate.mjs "$OUTPUT"
