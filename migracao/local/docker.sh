#!/usr/bin/env bash
set -euo pipefail
local_dir=$(cd "$(dirname "$0")" && pwd)
state="$local_dir/.docker-state"
command=${1:-help}
case "$command" in
  up|build|login|status|logs|stop|down) ;;
  help|--help|-h)
    echo 'Uso: migracao/local/docker.sh up|build|login|status|logs [serviço]|stop|down'
    exit 0 ;;
  *) echo 'Comando inválido; use docker.sh help' >&2; exit 1 ;;
esac
umask 077
if [[ ! -f "$state/compose.env" ]]; then
  if [[ "$command" != up ]]; then
    echo 'Execute primeiro: migracao/local/docker.sh up' >&2; exit 1
  fi
  # Never silently replace credentials belonging to an existing volume.
  volumes=$(docker --context "${SALTCORN_DOCKER_CONTEXT:-default}" volume ls -q --filter label=com.docker.compose.project=saltcorn-migracao-docker)
  if [[ -n "$volumes" ]]; then
    echo 'Volumes existentes sem compose.env; restaure o arquivo de credenciais.' >&2; exit 1
  fi
  port=${SALTCORN_DOCKER_PORT:-5180}
  if [[ ! "$port" =~ ^[1-9][0-9]{3,4}$ ]] || (( port > 65535 || port < 1024 )); then
    echo 'SALTCORN_DOCKER_PORT deve estar entre 1024 e 65535' >&2; exit 1
  fi
  mkdir -p "$state"
  password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
  (set -o noclobber; printf 'POSTGRES_PASSWORD=%s\nHTTP_PORT=%s\n' "$password" "$port" > "$state/compose.env")
fi
# Only this private env file configures Compose; avoid shell overrides or a root .env.
unset POSTGRES_PASSWORD HTTP_PORT COMPOSE_FILE COMPOSE_PROJECT_NAME COMPOSE_PROFILES
compose() {
  docker --context "${SALTCORN_DOCKER_CONTEXT:-default}" compose --env-file "$state/compose.env" -f "$local_dir/compose.app.yaml" "$@"
}
case "$command" in
  up)
    compose up --build -d --wait --wait-timeout 180
    echo 'Aplicação pronta. Execute migracao/local/docker.sh login e abra o link gerado.' ;;
  build) compose build ;;
  login)
    port=$(sed -n 's/^HTTP_PORT=//p' "$state/compose.env")
    compose exec -T app /release/bin/cli login --dir /data/instance | sed "s|http://localhost:3100/|http://localhost:$port/|" ;;
  status) compose ps ;;
  logs) compose logs --follow --tail 100 "${@:2}" ;;
  stop) compose stop ;;
  down) compose down ;;
esac
