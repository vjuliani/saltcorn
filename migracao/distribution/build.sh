#!/usr/bin/env bash
set -euo pipefail
# Build in an isolated tree; never prune the developer's node_modules.
repo=$(cd "$(dirname "$0")/../.." && pwd)
out=${1:?uso: build.sh /caminho/novo/da-release}
out=$(realpath -m "$out")
if [[ -e "$out" ]]; then echo 'destino já existe' >&2; exit 1; fi
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/source" "$stage/release/bin" "$stage/release/bff"
tar -C "$repo" --exclude=node_modules --exclude=dist -cf - migracao/packages/bff migracao/packages/frontend migracao/contracts | tar -C "$stage/source" -xf -
(cd "$repo/migracao/backend"; for cmd in cli server worker; do CGO_ENABLED=0 go build -trimpath -o "$stage/release/bin/$cmd" "./cmd/$cmd"; done)
npm ci --prefix "$stage/source/migracao/packages/bff"
npm run build --prefix "$stage/source/migracao/packages/bff"
npm ci --prefix "$stage/source/migracao/packages/frontend"
npm run build --prefix "$stage/source/migracao/packages/frontend"
cp -R "$stage/source/migracao/packages/bff/dist" "$stage/release/bff/dist"
cp "$repo/migracao/packages/bff/"{package.json,package-lock.json} "$stage/release/bff/"
npm ci --omit=dev --ignore-scripts --prefix "$stage/release/bff"
cp -R "$stage/source/migracao/packages/frontend/dist" "$stage/release/web"
cp "$repo/migracao/distribution/README.md" "$stage/release/README.md"
node "$repo/migracao/distribution/manifest.mjs" "$stage/release"
mkdir -p "$(dirname "$out")"
mv "$stage/release" "$out"
printf 'Release criada: %s\n' "$out"
