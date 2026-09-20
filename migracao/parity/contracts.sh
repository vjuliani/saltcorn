#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../contracts"
npm run validate
npm run bundle:internal
npm run bundle:bff
oapi-codegen -config oapi-codegen.internal-api.yaml gen/bundled/internal-api.json
oapi-codegen -config oapi-codegen.bff-api.yaml gen/bundled/bff-api.json
git diff --exit-code -- gen/go gen/ts gen/bundled
cd gen/go
go build ./...
go vet ./...
go test -race -count=1 ./...
