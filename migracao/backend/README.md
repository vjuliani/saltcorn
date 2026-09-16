# saltcorn-go — backend

Módulo Go da migração ([docs/migracao-go/](../../docs/migracao-go/)). Implementa a estrutura definida em [ADR-0001](../../docs/migracao-go/adr/0001-backend-go-cqrs.md): monólito modular com CQRS lógico, três executáveis (`server`, `worker`, `cli`) reutilizando os mesmos serviços internos.

**Estado atual:** fundação (GO-005) + tenancy e contexto transacional (GO-007). Ainda sem tabelas de domínio, autenticação real ou API pública completa — metadados/schema (GO-011), identidade com verificação criptográfica (GO-008/GO-009) e o restante do domínio entram nas tarefas seguintes, sobre esta mesma base.

## Estrutura

```
cmd/
  server/   processo HTTP — health/readiness (GO-005) + rota de exemplo com tenancy (GO-007)
  worker/   processo de background — loop periódico (GO-005) + jobs por tenant (GO-007)
  cli/      linha de comando (paridade com o saltcorn-cli atual, gradual)
internal/
  platform/
    config/    carregamento de configuração por variável de ambiente
    health/    handlers de liveness/readiness reutilizáveis
    shutdown/  rastreador de trabalho em curso para encerramento gracioso
    tenancy/   resolução/propagação de tenant+ator via context.Context (GO-007) —
               substitui o AsyncLocalStorage da produção Node (matriz GO-001 §2.1)
    database/  pool Postgres (pgx) com isolamento de schema por tenant, seguro
               sob reuso de conexão (GO-007)
```

Dependências externas mínimas e pinadas a versões compatíveis com Go 1.22 (a mais recente de cada uma frequentemente já exige Go 1.24+): `github.com/jackc/pgx/v5` (driver Postgres) e `github.com/golang-jwt/jwt/v5` (leitura de claims do token de identidade delegada). `go.mod` fixa a toolchain em `go 1.22`.

## Build, testes e execução

A partir deste diretório (`migracao/backend/`):

```bash
go build ./...
go vet ./...
gofmt -l .                          # deve não imprimir nada
go test -race -count=1 ./...
```

Os testes de `internal/platform/database` exigem Postgres real e fazem `t.Skip()` automaticamente se `SALTCORN_GO_TEST_DATABASE_URL` não estiver definida. Para rodá-los localmente, com um Postgres **isolado e descartável** (nunca aponte isto para um banco compartilhado — os testes fazem `TRUNCATE`/inserts/drops de schema):

```bash
docker run -d --name saltcorn-backend-testdb -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=saltcorn_test -e POSTGRES_USER=postgres -p 127.0.0.1:55556:5432 postgres:16

PGPASSWORD=postgres psql -h 127.0.0.1 -p 55556 -U postgres -d saltcorn_test <<'SQL'
CREATE SCHEMA IF NOT EXISTS acme;
CREATE SCHEMA IF NOT EXISTS beta;
CREATE TABLE IF NOT EXISTS acme.probe (id serial primary key, marker text not null);
CREATE TABLE IF NOT EXISTS beta.probe (id serial primary key, marker text not null);
INSERT INTO acme.probe (marker) VALUES ('acme-secret');
INSERT INTO beta.probe (marker) VALUES ('beta-secret');
SQL

SALTCORN_GO_TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:55556/saltcorn_test" \
  go test -race -count=1 ./internal/platform/database/...

docker rm -f saltcorn-backend-testdb   # ao terminar
```

Rodar cada processo:

```bash
go run ./cmd/server         # escuta em :8090 por padrão
go run ./cmd/worker         # loop de background
go run ./cmd/cli version
go run ./cmd/cli healthcheck   # consulta /healthz do server, se estiver rodando
```

Com `SALTCORN_GO_DATABASE_URL` configurada (apontando para o Postgres de teste acima ou outro), `cmd/server` expõe `GET /v1/tenants/{tenant}/tables/{table}/records` (rota de exemplo — sem tabela de domínio real ainda, só prova a fronteira tenancy+banco) e `cmd/worker` processa um job por tenant listado em `SALTCORN_GO_WORKER_TENANTS`.

## Configuração

Variáveis de ambiente lidas por `internal/platform/config` (compartilhado pelos três executáveis):

| Variável | Padrão | Descrição |
| --- | --- | --- |
| `SALTCORN_GO_HTTP_ADDR` | `:8090` | Endereço em que `cmd/server` escuta |
| `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS` | `15` | Tempo máximo esperando trabalho em curso terminar antes de forçar a saída |
| `SALTCORN_GO_ENV` | `development` | Rótulo do ambiente (ainda não controla comportamento, só logging) |
| `SALTCORN_GO_DATABASE_URL` | (vazio) | DSN do Postgres (`postgres://user:pass@host/db`). Vazio = sem banco configurado; rotas/jobs que dependem dele respondem 503/pulam, em vez de falhar ao iniciar |
| `SALTCORN_GO_WORKER_TENANTS` | (vazio) | Lista de tenants, separados por vírgula, que `cmd/worker` processa a cada ciclo — fundação temporária até GO-024/GO-025 trazerem descoberta real de tenants e fila de jobs |

## Tenancy e contexto transacional (GO-007)

`internal/platform/tenancy.Middleware` resolve tenant a partir do path `{tenant}` (rota registrada com o roteamento por padrão do Go 1.22, `r.PathValue`) e do claim `tenant` do token de identidade delegada (`Authorization: Bearer <jwt>`, ADR-0003) — rejeita com 400 se a URL não tem tenant, 401 se o token estiver ausente/malformado/expirado, e 403 se os dois não baterem (`tenant_mismatch`), replicando exatamente a regra do contrato em [`migracao/contracts/openapi/internal-api.yaml`](../contracts/openapi/internal-api.yaml).

**Atenção:** `tenancy.ParseDelegatedIdentity` usa `jwt.ParseUnverified` — lê as claims mas **não verifica a assinatura** do token. A verificação criptográfica completa é escopo de GO-008/GO-009; até lá, nenhum caminho de escrita real deve confiar só nisto. Isso está documentado no código-fonte (`identity.go`), não é um descuido.

`internal/platform/database.DB.WithTenant` executa uma função dentro de uma transação Postgres escopada ao schema do tenant via `SET LOCAL search_path` — nunca `SET` sem `LOCAL`, que persistiria na conexão física depois de devolvida ao pool e vazaria para a próxima chamada de outro tenant que reaproveitasse essa mesma conexão. Os testes de `internal/platform/database` verificam isso sob reuso concorrente de conexão (pool pequeno, muitos workers, dois tenants) e sob cancelamento de contexto — ambos com um teste dedicado (`TestWithTenant_DoesNotLeakSearchPathToNextConnectionUse`) que inspeciona `SHOW search_path` diretamente na conexão pooled, não apenas o comportamento observável de `WithTenant`.

## Encerramento gracioso

`cmd/server` e `cmd/worker` capturam `SIGINT`/`SIGTERM`, param de aceitar trabalho novo, e esperam o trabalho já em curso terminar (via `internal/platform/shutdown.Tracker`) antes de sair — dentro do prazo de `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS`. Se o prazo estourar, o processo registra um aviso e sai mesmo assim; isso é uma decisão operacional explícita, não um bug — ver `shutdown.Tracker.Drain`.

## CI

`.github/workflows/migracao-backend-ci.yml` roda `gofmt`, `go vet`, `go build` e `go test -race` neste módulo a cada push/PR que toque `migracao/backend/`, com um serviço Postgres efêmero do próprio job (schemas `acme`/`beta` preparados antes dos testes) para que a suíte de `internal/platform/database` rode de verdade em CI, não só localmente.
