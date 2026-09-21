#!/usr/bin/env bash
set -euo pipefail
umask 077
mkdir -p /data/instance
if [[ ! -f /data/instance/instance.json ]]; then
  if [[ ! -f /data/admin-password ]]; then
    node -e 'require("node:fs").writeFileSync("/data/admin-password", require("node:crypto").randomBytes(24).toString("hex") + "\n", {mode:0o600,flag:"wx"})'
  fi
  export SALTCORN_GO_DATABASE_URL="postgres://saltcorn_local:${POSTGRES_PASSWORD:?}@postgres:5432/saltcorn_local?sslmode=disable"
  /release/bin/cli setup --dir /data/instance --tenant local --email admin@local.test --password-file /data/admin-password
fi
# Credentials already live in the private instance; do not inherit the bootstrap
# PostgreSQL password in the BFF child environment.
unset POSTGRES_PASSWORD SALTCORN_GO_DATABASE_URL
exec /release/bin/cli serve --dir /data/instance --release /release
