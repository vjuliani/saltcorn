# saltcorn-go — backend

Módulo Go da migração ([docs/migracao-go/](../../docs/migracao-go/)). Implementa a estrutura definida em [ADR-0001](../../docs/migracao-go/adr/0001-backend-go-cqrs.md): monólito modular com CQRS lógico, três executáveis (`server`, `worker`, `cli`) reutilizando os mesmos serviços internos.

**Estado atual:** fundação (GO-005) + tenancy e contexto transacional (GO-007) + identidade, hashes, roles, ownership, RLS, tokens de API e MFA (GO-008) + registro de ownership de escrita e guarda de drenagem para o corte gradual (GO-009). Ainda sem tabelas de domínio dinâmicas nem API pública completa — metadados/schema (GO-011) e o restante do domínio entram nas tarefas seguintes, sobre esta mesma base.

## Estrutura

```
cmd/
  server/   processo HTTP — health/readiness (GO-005) + rota de exemplo com tenancy (GO-007)
            + verificação real de identidade delegada quando configurada (GO-008)
            + guarda de ownership de escrita na rota de exemplo (GO-009)
  worker/   processo de background — loop periódico (GO-005) + jobs por tenant (GO-007)
            + guarda de ownership de escrita no job placeholder (GO-009)
  cli/      linha de comando (paridade com o saltcorn-cli atual, gradual)
internal/
  identity/  hash de senha (bcrypt), papéis, ownership por campo, tokens de
             API (gerados/só o hash é persistido), TOTP/MFA, guarda contra
             estratégias de autenticação não suportadas (GO-008)
  platform/
    config/    carregamento de configuração por variável de ambiente
    health/    handlers de liveness/readiness reutilizáveis
    shutdown/  rastreador de trabalho em curso para encerramento gracioso
    tenancy/   resolução/propagação de tenant+ator via context.Context (GO-007),
               com verificação real de assinatura JWT (GO-008) — substitui o
               AsyncLocalStorage da produção Node (matriz GO-001 §2.1)
    database/  pool Postgres (pgx) com isolamento de schema por tenant, seguro
               sob reuso de conexão (GO-007); WithTenantAndActor também define
               os GUCs de ator/papel que as políticas de RLS nativa consultam
               (GO-008)
    cutover/   registro de ownership de escrita por tenant/capacidade e guarda
               de admissão/drenagem para o corte gradual entre backend
               legado e Go (GO-009, ADR-0008)
```

Dependências externas mínimas e pinadas a versões compatíveis com Go 1.22 (a mais recente de cada uma frequentemente já exige Go 1.24+): `github.com/jackc/pgx/v5` (driver Postgres), `github.com/golang-jwt/jwt/v5` (verificação de assinatura do token de identidade delegada), `golang.org/x/crypto` (bcrypt) e `github.com/pquerna/otp` (TOTP/MFA, RFC 6238). `go.mod` fixa a toolchain em `go 1.22`.

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

### Testes de RLS (GO-008)

`internal/platform/database/rls_test.go` exige, além do Postgres acima, uma tabela `acme.rls_probe` com Row-Level Security nativa E uma conexão que **não seja superusuário** — Postgres sempre deixa superusuários ignorarem RLS, mesmo com `FORCE ROW LEVEL SECURITY`, então testar com a conexão padrão (tipicamente `postgres`) passaria mesmo com a política quebrada, um falso positivo silencioso. Por isso esses testes usam uma DSN separada, `SALTCORN_GO_TEST_DATABASE_URL_RLS`, e pulam (`t.Skip`) se ela não estiver definida:

```bash
PGPASSWORD=postgres psql -h 127.0.0.1 -p 55556 -U postgres -d saltcorn_test <<'SQL'
CREATE ROLE app_user LOGIN PASSWORD 'app_user' NOSUPERUSER NOBYPASSRLS;
GRANT USAGE ON SCHEMA acme TO app_user;

CREATE TABLE IF NOT EXISTS acme.rls_probe (
    id serial PRIMARY KEY,
    owner_id text NOT NULL,
    min_role_read int NOT NULL DEFAULT 100
);
ALTER TABLE acme.rls_probe ENABLE ROW LEVEL SECURITY;
ALTER TABLE acme.rls_probe FORCE ROW LEVEL SECURITY;

-- Ownership: o ator só vê linhas cujo owner_id bate com o GUC de ator.
CREATE POLICY sc_rls_owner ON acme.rls_probe
    USING (owner_id = current_setting('app.current_user_id', true));
-- Papel elevado: combina com a política acima (OR implícito entre políticas
-- permissivas) — replica sc_rls_elevated da produção Node (matriz GO-001 §2.1).
CREATE POLICY sc_rls_elevated ON acme.rls_probe
    USING (current_setting('app.current_user_role', true)::int <= min_role_read);

GRANT SELECT, INSERT, UPDATE, DELETE ON acme.rls_probe TO app_user;

-- min_role_read=1 (só admin) é proposital: se fosse igual ao papel do ator
-- de teste (80), a política elevada por si só já daria visibilidade mútua
-- entre user-1/user-2, mascarando se a política de ownership funciona de
-- verdade.
INSERT INTO acme.rls_probe (owner_id, min_role_read) VALUES ('user-1', 1), ('user-2', 1);
SQL

SALTCORN_GO_TEST_DATABASE_URL="postgres://postgres:postgres@127.0.0.1:55556/saltcorn_test" \
SALTCORN_GO_TEST_DATABASE_URL_RLS="postgres://app_user:app_user@127.0.0.1:55556/saltcorn_test" \
  go test -race -count=1 ./internal/platform/database/... ./internal/identity/...
```

Rodar cada processo:

```bash
go run ./cmd/server         # escuta em :8090 por padrão
go run ./cmd/worker         # loop de background
go run ./cmd/cli version
go run ./cmd/cli healthcheck   # consulta /healthz do server, se estiver rodando
```

Com `SALTCORN_GO_DATABASE_URL` e `SALTCORN_GO_SERVICE_IDENTITY_SECRET` configuradas (apontando para o Postgres de teste acima ou outro), `cmd/server` expõe `GET /v1/tenants/{tenant}/tables/{table}/records` (rota de exemplo — sem tabela de domínio real ainda, só prova a fronteira tenancy+banco+identidade delegada verificada) e `cmd/worker` processa um job por tenant listado em `SALTCORN_GO_WORKER_TENANTS`.

## Configuração

Variáveis de ambiente lidas por `internal/platform/config` (compartilhado pelos três executáveis):

| Variável | Padrão | Descrição |
| --- | --- | --- |
| `SALTCORN_GO_HTTP_ADDR` | `:8090` | Endereço em que `cmd/server` escuta |
| `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS` | `15` | Tempo máximo esperando trabalho em curso terminar antes de forçar a saída |
| `SALTCORN_GO_ENV` | `development` | Rótulo do ambiente (ainda não controla comportamento, só logging) |
| `SALTCORN_GO_DATABASE_URL` | (vazio) | DSN do Postgres (`postgres://user:pass@host/db`). Vazio = sem banco configurado; rotas/jobs que dependem dele respondem 503/pulam, em vez de falhar ao iniciar |
| `SALTCORN_GO_WORKER_TENANTS` | (vazio) | Lista de tenants, separados por vírgula, que `cmd/worker` processa a cada ciclo — fundação temporária até GO-024/GO-025 trazerem descoberta real de tenants e fila de jobs |
| `SALTCORN_GO_SERVICE_IDENTITY_SECRET` | (vazio) | Segredo HMAC (mínimo 32 bytes) usado para verificar a assinatura do token de identidade delegada (GO-008). Vazio = a rota que exige tenancy não é registrada (404), em vez de aceitar tokens sem verificação de assinatura — falha fechada, não insegura por omissão. Deve ser o mesmo segredo usado por quem assina o token (o futuro BFF, GO-017) |

## Tenancy e contexto transacional (GO-007)

`internal/platform/tenancy.Middleware` resolve tenant a partir do path `{tenant}` (rota registrada com o roteamento por padrão do Go 1.22, `r.PathValue`) e do claim `tenant` do token de identidade delegada (`Authorization: Bearer <jwt>`, ADR-0003) — rejeita com 400 se a URL não tem tenant, 401 se o token estiver ausente/malformado/expirado/com assinatura inválida, e 403 se tenant do token e da URL não baterem (`tenant_mismatch`), replicando exatamente a regra do contrato em [`migracao/contracts/openapi/internal-api.yaml`](../contracts/openapi/internal-api.yaml).

`internal/platform/database.DB.WithTenant` executa uma função dentro de uma transação Postgres escopada ao schema do tenant via `SET LOCAL search_path` — nunca `SET` sem `LOCAL`, que persistiria na conexão física depois de devolvida ao pool e vazaria para a próxima chamada de outro tenant que reaproveitasse essa mesma conexão. Os testes de `internal/platform/database` verificam isso sob reuso concorrente de conexão (pool pequeno, muitos workers, dois tenants) e sob cancelamento de contexto — ambos com um teste dedicado (`TestWithTenant_DoesNotLeakSearchPathToNextConnectionUse`) que inspeciona `SHOW search_path` diretamente na conexão pooled, não apenas o comportamento observável de `WithTenant`.

## Identidade e autorização (GO-008)

`internal/identity` implementa, no lado do domínio (não de `internal/platform`), o que a matriz GO-001 §2.1 descreve para a produção Node — adaptado, não copiado bit-a-bit:

- **Senhas:** `HashPassword`/`CheckPassword` usam bcrypt (compatível com o `bcryptjs` do Node legado para fins de migração de dados).
- **Papéis:** IDs bem conhecidos (`RoleAdmin=1`, `RolePublic=100`) e `CanRead`/`CanWrite` comparando papel do ator contra o mínimo exigido.
- **Ownership:** `IsOwnerByField` cobre ownership por campo. Ownership por **fórmula JavaScript continua não suportado** (`ErrOwnershipFormulaUnsupported`) — decisão herdada de GO-004/ADR-0005, não resolvida por esta tarefa.
- **Tokens de API:** `GenerateAPIToken` retorna o texto plano uma única vez; só o hash SHA-256 é persistido (`_sc_api_tokens.token_hash`) — uma melhoria deliberada sobre o sistema Node legado, que grava tokens em texto plano. `VerifyAPIToken` compara em tempo constante (`crypto/subtle`) para não vazar informação por timing.
- **TOTP/MFA:** `GenerateTOTPSecret`/`ValidateTOTPCode` (RFC 6238, `github.com/pquerna/otp`).
- **Estratégias de autenticação:** `RequireNativeStrategy` aceita só `password`/`api_token`/`totp` — qualquer estratégia de plugin de terceiro (ex.: OAuth social) ou valor desconhecido retorna `ErrUnsupportedAuthStrategy`, um erro explícito e classificado como bloqueador de corte, nunca sucesso silencioso ou pânico (ADR-0005/ADR-0006).
- **`Authenticate`** retorna o mesmo erro (`ErrInvalidCredentials`) tanto para e-mail inexistente quanto para senha errada — evita enumeração de contas.

Tabelas de framework (`_sc_users`, `_sc_api_tokens`) são criadas por `identity.EnsureSchema` — deliberadamente separadas do catálogo de tabelas dinâmicas definidas pelo usuário final (GO-011), que ainda não existe.

`internal/platform/database.DB.WithTenantAndActor` estende `WithTenant`: além do `search_path` do tenant, define via `set_config` (nunca `SET` com interpolação de string) os GUCs `app.current_user_id` e `app.current_user_role`, que políticas de Row-Level Security nativa do Postgres podem consultar com `current_setting(...)`. Isso viabiliza RLS real por dono/papel dentro do mesmo schema — a fronteira que `WithTenant` sozinho (GO-007) não cobre, porque isola por schema, não por usuário dentro do schema. Ver "Testes de RLS" acima para o SQL de política de exemplo, e a matriz completa em [`docs/migracao-go/identidade/GO-008-matriz-autorizacao.md`](../../docs/migracao-go/identidade/GO-008-matriz-autorizacao.md).

`internal/platform/tenancy.Verifier` substitui o `jwt.ParseUnverified` de GO-007 por verificação real de assinatura HS256 (`jwt.ParseWithClaims` + `jwt.WithValidMethods([]string{"HS256"})`, que bloqueia ataques de confusão de algoritmo como `"alg": "none"`) e exige a claim `exp` (`jwt.WithExpirationRequired()`). O segredo vem de `SALTCORN_GO_SERVICE_IDENTITY_SECRET` (ver tabela de configuração acima); sem ele, a rota que depende de tenancy simplesmente não é registrada.

Sessão/cookies do BFF (Node.js, ainda não implementado — GO-017) são **definidos**, não implementados, em [ADR-0007](../../docs/migracao-go/adr/0007-sessao-cookies-bff.md): o backend Go nunca recebe cookie de sessão, só o token de identidade delegada de vida curta descrito acima (ADR-0003).

## Ownership de escrita e corte gradual (GO-009)

`internal/platform/cutover` implementa o registro de ownership de escrita por tenant/capacidade e a guarda de admissão/drenagem que qualquer caminho de escrita do backend Go deve consultar antes de agir — a decisão de design completa (o que é implementado aqui vs. o que é infraestrutura de borda fora de escopo) está em [ADR-0008](../../docs/migracao-go/adr/0008-roteamento-de-corte-e-ownership.md).

- **Registro persistido** (`_sc_capability_ownership`, schema `public` — metadado de controle, não dado de um tenant): `(tenant, capability) → owner ∈ {go, legacy}`. Sem linha registrada, o owner é `legacy` — padrão seguro (ADR-0006): nada é servido por Go sem uma decisão explícita de corte.
- **`cutover.Guard`**: cache em memória do owner corrente por chave, resetável — diferente de `shutdown.Tracker` (que drena uma única vez, de forma permanente, para encerrar o processo), `Guard` precisa drenar e voltar a aceitar trabalho repetidamente, uma vez por troca de rota.
- **`cutover.LoadFromRegistry(ctx, db, guard)`**: carrega o registro persistido no cache no boot — chamado por `cmd/server` e `cmd/worker` antes de aceitar requisições/jobs. Sem isso (ou sem a tabela existir ainda), a Guard fica vazia e trata tudo como "não é Go" (aviso, não erro fatal).
- **`cutover.SwitchOwner(ctx, db, guard, tenant, capability, novoOwner, drainTimeout)`**: a única forma sancionada de trocar ownership — drena trabalho em curso, só então persiste o novo owner, só então libera admissão nova. Usada tanto para cortar uma capacidade para Go quanto para o rollback (voltar para legacy).
- **`cutover.Acquire(guard, tenant, capability)`**: o ponto de entrada único que qualquer rota HTTP (via `cutover.RequireOwnership`, aplicado depois de `tenancy.Middleware` — nunca lê headers, só o tenant já verificado do contexto) ou job de background deve chamar antes de agir. "Bloquear caminhos alternativos, inclusive jobs" (critério de aceite de GO-009) é a mesma checagem reutilizada nos dois lugares, não duas implementações que podem divergir — ver o wiring em `cmd/server/main.go` e `cmd/worker/main.go`.

Para exercitar a rota de exemplo localmente (`GET /v1/tenants/{tenant}/tables/{table}/records`, que agora também exige ownership), crie a tabela e registre um corte antes de iniciar `cmd/server`:

```bash
PGPASSWORD=postgres psql -h 127.0.0.1 -p 55556 -U postgres -d saltcorn_test <<'SQL'
CREATE TABLE IF NOT EXISTS _sc_capability_ownership (
    tenant text NOT NULL,
    capability text NOT NULL,
    owner text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant, capability)
);
INSERT INTO _sc_capability_ownership (tenant, capability, owner)
VALUES ('acme', 'tables.records', 'go')
ON CONFLICT (tenant, capability) DO UPDATE SET owner = EXCLUDED.owner;
SQL
```

Sem essa linha, a rota responde `409 route_mismatch` mesmo com um token de identidade delegada válido — o comportamento correto, não um bug: nenhuma tenant/capacidade é servida por Go sem registro explícito.

## Encerramento gracioso

`cmd/server` e `cmd/worker` capturam `SIGINT`/`SIGTERM`, param de aceitar trabalho novo, e esperam o trabalho já em curso terminar (via `internal/platform/shutdown.Tracker`) antes de sair — dentro do prazo de `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS`. Se o prazo estourar, o processo registra um aviso e sai mesmo assim; isso é uma decisão operacional explícita, não um bug — ver `shutdown.Tracker.Drain`.

## CI

`.github/workflows/migracao-backend-ci.yml` roda `gofmt`, `go vet`, `go build` e `go test -race` neste módulo a cada push/PR que toque `migracao/backend/`, com um serviço Postgres efêmero do próprio job (schemas `acme`/`beta` preparados antes dos testes) para que a suíte de `internal/platform/database` rode de verdade em CI, não só localmente.
