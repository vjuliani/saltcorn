# saltcorn-go — backend

Módulo Go da migração ([docs/migracao-go/](../../docs/migracao-go/)). Implementa a estrutura definida em [ADR-0001](../../docs/migracao-go/adr/0001-backend-go-cqrs.md): monólito modular com CQRS lógico, três executáveis (`server`, `worker`, `cli`) reutilizando os mesmos serviços internos.

**Estado atual:** fundação (GO-005) + tenancy e contexto transacional (GO-007) + identidade, hashes, roles, ownership, RLS, tokens de API e MFA (GO-008) + registro de ownership de escrita e guarda de drenagem para o corte gradual (GO-009) + logs estruturados, métricas e correlação de trace (GO-010) + catálogo de tabelas/campos/relações dinâmicos e evolução de schema (GO-011) + compilador de consultas dinâmicas (GO-012) + comandos de registro (insert/update/delete) com controle de concorrência (GO-013) + idempotência e outbox transacional (GO-014) + autorização em joins/agregações de leitura (GO-015) + avaliação com benchmark de projeções CQRS, adiada (GO-016, ADR-0010) + rotas HTTP reais de registros/ator e BFF Node.js consumindo-as (GO-017). Ainda sem API pública completa — o restante do domínio entra nas tarefas seguintes, sobre esta mesma base.

## Estrutura

```
cmd/
  server/   processo HTTP — health/readiness (GO-005) + tenancy (GO-007)
            + verificação real de identidade delegada quando configurada (GO-008)
            + guarda de ownership de escrita nas rotas de registros (GO-009)
            + logs estruturados, GET /metrics e trace por requisição (GO-010)
            + rotas reais de internal-api.yaml — listRecords/createRecord/
            getActor — substituindo a rota de exemplo/placeholder (GO-017)
  worker/   processo de background — loop periódico (GO-005) + jobs por tenant (GO-007)
            + guarda de ownership de escrita no job placeholder (GO-009)
            + logs estruturados, métricas de job e trace por execução (GO-010)
            + job de processamento de outbox por tenant (GO-014)
  cli/      linha de comando (paridade com o saltcorn-cli atual, gradual)
internal/
  identity/  hash de senha (bcrypt), papéis, ownership por campo, tokens de
             API (gerados/só o hash é persistido), TOTP/MFA, guarda contra
             estratégias de autenticação não suportadas (GO-008);
             FindUserByID resolve o papel atual a partir do `sub` da
             identidade delegada, nunca confiado de fora (GO-017)
  metadata/  catálogo de tabelas/campos/relações dinâmicos e executor de
             evolução de schema — catálogo e DDL na mesma transação, lock
             de advisory por tenant, versão de metadados e cache com
             invalidação (GO-011)
  records/   compilador de consultas dinâmicas — DSL de filtros, joins de
             1 nível, agregações escalares, ordenação e paginação, tudo
             resolvido contra o catálogo e parametrizado (GO-012); e
             comandos de registro — insert/update/delete com controle de
             concorrência via xmin e pontos de extensão de trigger
             (GO-013) — no mesmo pacote ("registros/consultas" é um
             módulo só em ADR-0001)
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
               (GO-008); log/métrica por transação (GO-010)
    cutover/   registro de ownership de escrita por tenant/capacidade e guarda
               de admissão/drenagem para o corte gradual entre backend
               legado e Go (GO-009, ADR-0008)
    telemetry/ logs estruturados com redação automática, métricas em formato
               Prometheus e correlação de trace via W3C Trace Context
               (GO-010, ADR-0009)
    outbox/    idempotência de escrita e padrão outbox transacional — chave/
               payload/resultado e eventos gravados na mesma transação do
               efeito; worker com savepoint por evento, retries com corte e
               `FOR UPDATE SKIP LOCKED` entre workers concorrentes (GO-014)
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

`internal/metadata` (GO-011), `internal/records` (GO-012/013) e `internal/platform/outbox` (GO-014) só precisam de `SALTCORN_GO_TEST_DATABASE_URL` — nenhum fixture adicional: cada teste cria seu próprio schema de tenant isolado, como `internal/identity`.

Rodar cada processo:

```bash
go run ./cmd/server         # escuta em :8090 por padrão
go run ./cmd/worker         # loop de background
go run ./cmd/cli version
go run ./cmd/cli healthcheck   # consulta /healthz do server, se estiver rodando
```

Com `SALTCORN_GO_DATABASE_URL` e `SALTCORN_GO_SERVICE_IDENTITY_SECRET` configuradas (apontando para o Postgres de teste acima ou outro), `cmd/server` expõe as rotas reais de registros/ator (GO-017: `GET`/`POST .../records`, `GET .../actor`) e `cmd/worker` processa um job por tenant listado em `SALTCORN_GO_WORKER_TENANTS`. As rotas de registros exigem, além do registro de ownership (`_sc_capability_ownership`, ver "Ownership de escrita" abaixo), que os schemas de `internal/identity`/`internal/metadata`/`internal/platform/outbox` já tenham sido aplicados ao schema do tenant (`EnsureSchema` de cada pacote) e que exista um usuário e uma tabela dinâmica — aplicação desses schemas ainda é manual/externa ao binário, mesmo padrão de `_sc_capability_ownership`.

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
| `SALTCORN_GO_LOG_LEVEL` | `INFO` | Nível mínimo de log estruturado (GO-010): `DEBUG`, `INFO`, `WARN` ou `ERROR` (case-insensitive). `DEBUG` inclui a conclusão de toda transação SQL bem-sucedida; `INFO` só as de requisição HTTP/job e erros |

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

## Catálogo de metadados e evolução de schema (GO-011)

`internal/metadata` implementa o núcleo do produto (matriz GO-001 §2.2, `models/table.ts`/`field.ts`): o catálogo de tabelas/campos/relações dinâmicos e o executor que aplica essas mudanças como DDL Postgres de verdade — tabelas de framework `_sc_tables`, `_sc_fields`, `_sc_metadata_version` (schema por tenant, como `internal/identity`).

- **Catálogo e DDL na mesma transação:** `CreateTable`/`AddField`/`DropField`/`DropTable` gravam o metadado E executam a DDL correspondente dentro da mesma `db.WithTenant` — se qualquer etapa falhar, a transação inteira desfaz (Postgres trata DDL como transacional), nunca deixando um catálogo que descreve uma coluna inexistente ou uma coluna física sem registro.
- **Nomes maliciosos:** `SQLSanitize`/`SQLSanitizeAllowDots` portam byte-a-byte a semântica de `packages/db-common/internal.ts` (incl. `\p{Letter}` Unicode) — todo nome definido pelo usuário final passa por aqui antes de virar identificador SQL, e ainda é citado via `pgx.Identifier.Sanitize()` na DDL (defesa em profundidade).
- **Concorrência:** toda mutação adquire `pg_advisory_xact_lock` escopado ao tenant atual (libera sozinho no commit/rollback) — o "executor único de migrations" de ADR-0001, por tenant. Tentativas concorrentes de criar a mesma tabela serializam e só uma faz a criação real (as demais recebem a tabela já existente, idempotente).
- **Idempotência:** `CreateTable`/`AddField` com uma definição idêntica a uma já existente são um no-op bem-sucedido (retornam o registro existente, não incrementam a versão); `DropField`/`DropTable` de algo que não existe também são no-op (mesma convenção de `identity.RevokeAPIToken`).
- **Relações:** um campo `FieldKey` é uma FOREIGN KEY real do Postgres para outra tabela do catálogo — não um ponteiro solto em metadado.
- **Versão do catálogo e cache:** toda mutação incrementa `_sc_metadata_version` na mesma transação — a base de "invalidação de cache". `metadata.Cache` é o consumidor mínimo e testável dessa versão (nenhum código de produção o usa ainda; GO-012, o compilador de consultas, é quem consumiria isto de verdade).
- **Autorização:** só `identity.RoleAdmin` pode mutar o catálogo (`identity.CanWrite` reaproveitado, GO-008) — um ator sem esse papel é recusado antes de qualquer DDL rodar.

Tipos de campo suportados nesta tarefa: `text`, `integer`, `boolean`, `float`, `date`, `key` (relação). Deliberadamente fora de escopo: campos de arquivo (GO-026 não existe), tipos definidos por plugin, e constraints por fórmula JS (dependem do motor de expressões, bloqueado por ADR-0005/GO-004) — só constraint de unicidade por campo é suportada.

## Compilador de consultas dinâmicas (GO-012)

`internal/records` implementa o DSL de filtros/joins/agregações/ordenação/paginação sobre o catálogo de `internal/metadata` (matriz GO-001 §2.2, `packages/db-common/internal.ts`, `mkWhere`/`whereClause`) — a versão tipada em Go do DSL legado, não uma tradução literal do mapa flexível de chave-como-operador do JavaScript.

- **`Where`** é uma interface implementada por `Eq`, `In`, `NotIn`, `Like` (substring, `ILIKE`), `Gt`/`Gte`/`Lt`/`Lte`, `Between`, `And`, `Or`, `Not` — cada operador é um tipo próprio, montado em Go, não uma string livre. "Chaves compostas" do critério de aceite: várias condições combinadas com `And` (o catálogo de GO-011 só tem PK simples, `id serial`, então não existe PK composta a portar — isto é o equivalente prático).
- **Resolução pelo catálogo:** todo nome de tabela/campo em `Query` é resolvido contra `metadata.GetTable`/`ListFields` ANTES de qualquer SQL ser montado — um nome não catalogado é rejeitado (`ErrUnknownTable`/`ErrUnknownField`), nunca sanitizado-e-aceito. Mais estrito que o legado, que só sanitiza o nome sem validar contra um catálogo.
- **Sem injeção de SQL:** todo valor de filtro é parametrizado (`$1, $2, ...`) via `pgx`, nunca interpolado; identificadores resolvidos ainda passam por `pgx.Identifier.Sanitize()` na montagem da SQL (defesa em profundidade). Um teste dedicado confirma que um valor com sintaxe SQL (`"x'; DROP TABLE books; --"`) só falha em encontrar correspondência, nunca corrompe a consulta.
- **Tipos e `NULL`:** `Value == nil` sempre compila para `IS NULL` (nunca `= NULL`); todo valor passa por `validateValue`, que rejeita um tipo Go incompatível com o tipo de campo do catálogo (`ErrTypeMismatch`) antes de chegar ao Postgres.
- **Joins:** um nível de `LEFT JOIN` através de um campo `metadata.FieldKey`, trazendo colunas da tabela referenciada sob a chave `"<campo>__<coluna>"`.
- **Agregações:** subquery escalar (`COUNT`/`SUM`/`AVG`/`MIN`/`MAX`) sobre uma tabela filha que referencia a tabela consultada via campo `key` — o padrão "quantos registros relacionados" do Saltcorn legado.
- **Autorização:** `identity.CanRead(actorRole, table.MinRoleRead)` é checado antes de compilar qualquer SQL na tabela principal — reaproveitado de GO-008; GO-015 estende a mesma checagem a joins e agregações (ver seção própria abaixo).

Deliberadamente fora de escopo (documentado, não fabricado — ver `docs/migracao-go/execucoes/GO-012.md`): busca full-text, consultas de JSON path (GO-011 não tem tipo de campo JSON), sub-selects, slugify, operador de regex e geo — nenhum tem o tipo de campo ou a infraestrutura correspondente ainda.

## Autorização em todos os caminhos de leitura (GO-015)

`internal/records` (GO-012) já checava `identity.CanRead` na tabela principal de uma `Query` antes de montar qualquer SQL, mas essa era a ÚNICA checagem do pacote — um join ou uma agregação sobre uma tabela DIFERENTE da principal não checava o `MinRoleRead` dessa segunda tabela. Isso é uma porta lateral real: um ator com acesso só à tabela pública `books` podia trazer colunas de uma tabela `publisher` admin-only via join, ou obter `COUNT`/`SUM`/`AVG`/`MIN`/`MAX` sobre uma tabela filha admin-only via agregação, desde que a tabela consultada diretamente fosse de leitura pública — exatamente o cenário que "contagens e agregados não revelam registros proibidos" (critério de aceite) proíbe.

- **Join:** `Compile` agora chama `identity.CanRead(actorRole, refTable.MinRoleRead)` logo após resolver a tabela referenciada por um campo `metadata.FieldKey`, antes de listar suas colunas ou montar o `LEFT JOIN` — nenhuma coluna de uma tabela sem permissão de leitura chega a aparecer no SQL montado.
- **Agregação (inclui "contagem"):** `compileAggregation` recebe `actorRole` e aplica a mesma checagem sobre a tabela filha (`agg.ChildTable`) antes de montar a subquery escalar — cobre `Count` (a "contagem" do escopo, que no DSL de GO-012 é só mais um `AggFunc`, não uma função dedicada) e as demais funções.
- **Busca e exportação:** `Rows` é o único ponto de execução pública de uma `Query` (usado tanto para busca quanto para qualquer exportação em lote que venha a existir) — herda as duas correções acima automaticamente, por passarem pelo mesmo `Compile`. Não existe hoje uma função de exportação separada (CSV/JSON em lote) no pacote; quando existir, deve reusar `Rows`/`Compile`, nunca montar SQL paralelo.
- **Revogação imediata:** o papel do ator (`actorRole`) é um parâmetro explícito de toda chamada a `Compile`/`Rows`/`CreateRecord`/`UpdateRecord`/`DeleteRecord` — nunca cacheado dentro de `internal/records` ou `internal/identity` entre chamadas. Uma leitura bem-sucedida como admin não deixa nenhum resquício que beneficie a chamada seguinte com um papel insuficiente (`TestCompile_RevocationTakesEffectImmediately` prova a ordem admin→público, complementando `TestCompile_AuthorizationDenied`, que já provava público→admin).
- **Caches:** `metadata.Cache` (GO-011) guarda `MinRoleRead`/`MinRoleWrite` por tabela em memória, mas segue sem nenhum consumidor de produção (confirmado: nada em `internal/records` o referencia) — não há cache de resultado de consulta nem de decisão de autorização hoje. Como não existe hoje um mutador para alterar `MinRoleRead`/`MinRoleWrite` de uma tabela já criada (só são definidos em `CreateTable`), não há uma "política que muda" para esse cache servir desatualizada; se um mutador e a integração com `Compile` vierem a existir, a invalidação por versão de `metadata.Cache` (que já existe desde GO-011) precisa continuar cobrindo qualquer mutação de papel mínimo, não só de shape de schema.
- **"Usar banco primário após escrita":** não existe réplica de leitura no backend hoje — `internal/platform/database.DB` abre um único `*pgxpool.Pool` e todo `WithTenant`/`WithTenantAndActor` sempre o usa. O requisito é hoje vacuamente satisfeito (não há para onde uma leitura pós-escrita "vazar"); vira trabalho real só se/quando uma topologia de réplica for introduzida.

## Projeções CQRS — adiadas com benchmark (GO-016)

[ADR-0010](../../docs/migracao-go/adr/0010-projecoes-cqrs.md) mede o custo da consulta agregada de `internal/records` (a subquery correlacionada por linha de `Query.Aggregations`, GO-012/GO-015) e decide **adiar** a adoção de projeções CQRS assíncronas: um índice na coluna do campo `FieldKey` (que `internal/metadata`, GO-011, não cria hoje) já resolve 18× do gargalo sozinho, a custo zero de escrita; uma projeção completa ganharia mais 23× sobre isso, mas ao custo de ~1,7× de latência de escrita e da complexidade de atraso/reconstrução que o critério de aceite da tarefa pede para testar SE a projeção fosse adotada — desproporcional sem nenhum consumidor de leitura real ainda (`internal/records` não está exposto por nenhum caminho HTTP/CLI/worker hoje). O benchmark reproduzível fica em `internal/records/cqrs_bench_test.go`, atrás de `SALTCORN_GO_RUN_CQRS_BENCH=1` (não roda na suíte de correção rotineira — mede tempo, não corretude). A ADR também registra, como recomendação separada e de baixo custo (fora do escopo desta tarefa), que `AddField` deveria criar um índice para todo campo `FieldKey`.

## Comandos de registro (GO-013)

`internal/records` também implementa `CreateRecord`/`UpdateRecord`/`DeleteRecord` — insert/update/delete de registros dinâmicos, reaproveitando a mesma resolução de catálogo e `validateValue` de GO-012 (nenhuma checagem nova reinventada).

- **Controle de concorrência via `xmin`:** em vez de uma coluna `version` própria (que exigiria alterar o DDL já testado de GO-011), o token de versão é a coluna de sistema `xmin` do Postgres — o identificador de transação que gravou a versão atual da linha. `Compile` (GO-012) passa a incluir `_version` (`xmin::text`) em todo resultado de leitura; `UpdateRecord`/`DeleteRecord` exigem esse token (`expectedVersion`) e falham com `ErrVersionConflict` — o "erro definido" do critério de aceite — se a linha mudou desde a leitura. `ErrRecordNotFound` distingue "não existe" de "existe, mas mudou".
- **Pontos de extensão de trigger:** um `*Hooks` opcional com callbacks `Before{Insert,Update,Delete}`/`After{Insert,Update,Delete}`, chamados dentro da MESMA transação do comando — um hook que falha desfaz a operação inteira, não só o efeito do hook. Nenhuma automação real usa isto ainda (GO-024/GO-025 não existem); é o ponto de plugue testável, mesmo espírito do `metadata.Cache` sem consumidor real (GO-011).
- **Validações:** nome de campo desconhecido (`ErrUnknownField`), tipo incompatível (`ErrTypeMismatch`), campo obrigatório ausente (`ErrRequiredField`) — checados em Go antes de qualquer SQL rodar.
- **Classificação de erros do driver:** violação de unicidade → `ErrDuplicateValue`; violação de chave estrangeira → `ErrInvalidReference`; violação `NOT NULL` → `ErrRequiredField` — nunca a mensagem crua do driver Postgres, que pode ecoar de volta um valor de linha (ex.: `Key (email)=(x@y.com) already exists`).
- **Autorização:** só em nível de tabela (`identity.CanWrite(actorRole, table.MinRoleWrite)`) — autorização por ownership de linha (`identity.IsOwnerByField`, GO-008) fica de fora: exigiria o catálogo saber qual campo é o "campo de ownership" de uma tabela, o que GO-011 não modela hoje.

## Idempotência e outbox (GO-014)

`internal/platform/outbox` implementa `Do`, que executa uma operação e grava sua chave de idempotência e os eventos que ela produz na MESMA transação Postgres do efeito em si — nunca como uma escrita separada. Isso dá as duas metades do critério de aceite de graça, pela própria atomicidade do Postgres:

- **Crash antes do commit:** se a transação não commitar (a operação falha, o chamador aborta, o processo cai antes do commit), NADA persiste — nem o efeito, nem a chave, nem o evento. Nada foi "confirmado", então nada é "perdido": uma nova tentativa com a mesma chave/payload encontra o catálogo limpo e tenta de novo.
- **Crash depois do commit / confirmação perdida:** se a transação commitar, efeito + chave + evento ficam gravados juntos. Uma queda do processo chamador depois do commit (ou uma resposta perdida na rede) não perde o evento — ele já está no Postgres; uma nova chamada com a mesma chave/payload encontra o resultado já gravado e o devolve sem rodar o efeito de novo (`replayed=true`).
- **Mesma chave, payload diferente:** rejeitada com `ErrKeyConflict`, sem rodar a operação.
- **Concorrência real:** `pg_advisory_xact_lock` escopado a (tenant, chave) serializa tentativas concorrentes com a MESMA chave (mesma técnica de `internal/metadata.lockCatalog`, GO-011) — sem isso, duas chamadas concorrentes poderiam ambas ver "não existe" e rodar o efeito duas vezes.
- **Sem estado "failed" na própria chave:** se a operação falhar, `Do` não grava nada para aquela tentativa — a transação externa aborta (mesma disciplina de GO-013), e uma nova tentativa encontra o catálogo limpo. Mais simples e mais correto do que tentar persistir um "failed" durável dentro de uma transação que o próprio chamador vai reverter.

O worker (`ListPending`/`ProcessPending`) drena `_sc_outbox` com `FOR UPDATE SKIP LOCKED` — o mecanismo que garante que dois workers concorrentes nunca peguem o mesmo evento — processando cada evento numa savepoint própria (`tx.Begin()` sobre uma `pgx.Tx` já aberta simula uma transação aninhada via `SAVEPOINT`): se o handler de um evento falha, só o efeito DELE é desfeito, os demais eventos do lote continuam. Retries têm corte: `maxAttempts` esgotado marca o evento como `failed` (terminal, inspecionável via `ListFailed`/consulta direta a `_sc_outbox`), nunca fica pendente para sempre.

`cmd/worker` roda um job de outbox por tenant (mesma guarda de ownership de GO-009, mesma telemetria de GO-010) — hoje o handler só loga o evento (nenhuma automação real o consome ainda, GO-024/GO-025 não existem); é o ponto de plugue testável, mesmo espírito dos `Hooks` sem consumidor real de GO-013.

## Observabilidade (GO-010)

`internal/platform/telemetry` implementa logs estruturados, métricas e correlação de trace sem SDK externo — a decisão de design completa (por que não OpenTelemetry/cliente Prometheus, redação por nome de atributo, controle de cardinalidade por desenho) está em [ADR-0009](../../docs/migracao-go/adr/0009-observabilidade-sem-sdk-externo.md).

- **Trace/correlação:** cada requisição HTTP e cada execução de job ganha um `trace_id`/`span_id` (padrão [W3C Trace Context](https://www.w3.org/TR/trace-context/)) — `telemetry.Middleware` reaproveita o `trace_id` de um header `traceparent` de entrada (se válido) e sempre devolve um `traceparent` na resposta, para que um chamador (o futuro BFF, GO-017) e o backend Go correlacionem a mesma operação. `telemetry.LoggerFor(ctx)` anexa `trace_id`/`span_id`/`tenant`/`ator` (quando presentes) a toda linha de log.
- **Logs estruturados:** JSON via `log/slog`, com `telemetry.NewHandler` redigindo automaticamente (`"REDACTED"`) qualquer atributo cujo NOME contenha `token`, `password`/`senha`, `secret`, `authorization`, `cookie` ou `totp_secret` — não depende de quem escreve cada `logger.Info(...)` lembrar de omitir o campo certo. `internal/platform/database` nunca loga texto de SQL, parâmetros ou a mensagem crua do driver — só um resultado classificado (`ok`/`error`/`canceled`/`deadline_exceeded`) e a duração.
- **Métricas:** `GET /metrics` expõe `http_requests_total{method,route,status_class}`, `http_request_duration_seconds{method,route}`, `sql_transactions_total{result}`, `sql_transaction_duration_seconds`, `sql_pool_{acquired,idle,total,max}_connections` (saturação do pool) e `job_runs_total{result}`/`job_duration_seconds`, no formato de exposição de texto do Prometheus. Toda label é um conjunto fixo e pequeno declarado na criação da métrica — nunca tenant, ator ou path bruto (controle de cardinalidade); essa granularidade fica nos logs, que não sofrem o mesmo problema de explosão de séries temporais.
- `/healthz` e `/readyz` são deliberadamente **não** instrumentadas (probes de alta frequência não devem virar ruído de log/métrica).

Exemplo local (com o servidor rodando e uma requisição feita):

```bash
curl -s http://localhost:8090/metrics | grep http_requests_total
# http_requests_total{method="GET",route="tenant_records",status_class="2xx"} 1
```

## Rotas HTTP reais de registros e BFF Node.js (GO-017)

Até esta tarefa, `cmd/server` só tinha a rota de exemplo/placeholder de GO-009 (um `SELECT now()`, sem tabela de domínio real) — `internal-api.yaml` (GO-006) nunca tinha sido implementado, apesar de `internal/records` (GO-012/013) já ter toda a lógica de domínio pronta. GO-017 fecha essa lacuna, mas de forma minimalista: implementa em `cmd/server/records.go` só os três caminhos que o BFF (`migracao/packages/bff/`, também desta tarefa) de fato consome —

- **`GET /v1/tenants/{tenant}/actor`** — extensão nova de GO-017 a `internal-api.yaml`: resolve o papel atual do ator (`identity.FindUserByID` a partir do `sub` da identidade delegada) a cada requisição, nunca cacheado — o mecanismo que ADR-0007 exige para o bootstrap do BFF.
- **`GET .../tables/{table}/records`** — `records.Rows` com paginação por cursor opaco (implementado como offset codificado em base64 — decisão de implementação, não parte do contrato).
- **`POST .../tables/{table}/records`** — `records.CreateRecord` dentro de `outbox.Do` (GO-014): a mesma `Idempotency-Key` com o mesmo corpo retorna o registro já criado sem rodar `CreateRecord` de novo; corpo diferente com a mesma chave falha com 409.

`GET`/`PATCH`/`DELETE` por ID de `internal-api.yaml` **não são implementados**: `bff-api.yaml` (o contrato que o BFF realmente precisa satisfazer) não os expõe ao React ainda, e `records.UpdateRecord`/`DeleteRecord` exigem um `expectedVersion` que o contrato HTTP hoje não tem como veicular em `DELETE` (sem corpo) — fica para quando um consumidor real de edição existir (provavelmente GO-019).

O BFF Node.js/TypeScript (`migracao/packages/bff/`, [README próprio](../packages/bff/README.md)) é o primeiro consumidor real dessas rotas: sessão/cookies (ADR-0007), CSRF, cliente HTTP tipado (reaproveitando `migracao/contracts/gen/ts`), timeout/`AbortController`, e a mesma `Idempotency-Key` determinística reaproveitada em retry do navegador — nunca acessa banco de domínio, cada leitura/escrita chama a API interna do Go acima.

## Editor: tabelas, campos e views (GO-019)

Até esta tarefa, nem o catálogo de tabelas/campos (`internal/metadata`, GO-011) nem nenhuma persistência de view tinham QUALQUER exposição HTTP — o schema dinâmico só existia no nível de biblioteca Go. GO-019 fecha essa lacuna e introduz `internal/views`, o novo pacote que guarda o documento de layout do builder Craft.js (`packages/saltcorn-builder`) associado a uma tabela.

- **`internal/views` não tinha runtime de renderização nesta tarefa** — armazenava `template` como uma string de intenção não interpretada e `configuration` como o JSON de layout do builder, opacamente. Interpretar os dois e produzir um DTO de renderização é GO-020 ("Portar runtime de views e páginas" — ver seção própria abaixo), que reaproveita este pacote diretamente.
- **Concorrência via `xmin`, reaproveitada de `internal/records` (GO-013):** `UpdateView` usa exatamente o mesmo padrão de `_version` (coluna de sistema `xmin`) — nenhum mecanismo novo. `ErrVersionConflict` é o "conflito de edição apresentado, não sobrescrito silenciosamente" do critério de aceite.
- **"Publicar" não é um mecanismo separado:** uma view nasce com `min_role = identity.RoleAdmin` por padrão (nunca pública por omissão); publicar é um `UpdateView` comum que baixa `min_role` (ex.: para `RolePublic`). "Preview com dois papéis" reaproveita `identity.CanRead` (GO-008) igual a qualquer outra leitura com controle de papel do backend.
- **Idempotência — só onde faz sentido:** `metadata.CreateTable`/`AddField` (GO-011) já são idempotentes por definição própria (redefinição idêntica é um no-op que devolve o registro existente) — as rotas `POST .../tables` e `POST .../tables/{table}/fields` (`cmd/server/tables.go`) por isso **não** usam `outbox.Do`/`Idempotency-Key`, que seria maquinário redundante. Já `views.CreateView`/`UpdateView` NÃO são idempotentes por natureza (um retry de rede de uma criação bem-sucedida colidiria com o nome único; um retry de um PATCH bem-sucedido colidiria com o `_version` já avançado) — `cmd/server/views.go` por isso exige `Idempotency-Key` e usa `outbox.Do` (GO-014), o mesmo padrão de `createRecord` (GO-017).
- **Capacidades de corte gradual granulares** (GO-009): `tables.schema` (criação de tabela/campo) e `tables.views` (CRUD de view) são capacidades PRÓPRIAS, distintas de `tables.records` — cada uma pode ser cortada para o Go independentemente das demais.
- **Rotas novas:** `POST /v1/tenants/{tenant}/tables`, `POST /v1/tenants/{tenant}/tables/{table}/fields`, `GET /v1/tenants/{tenant}/views`, `POST /v1/tenants/{tenant}/views`, `GET /v1/tenants/{tenant}/views/{id}`, `PATCH /v1/tenants/{tenant}/views/{id}` — todas atrás de `cutover.RequireOwnership` com a capacidade correspondente (`GET .../views` e `GET .../views/{id}/render` são de GO-020, ver seção própria abaixo).

**Validação do critério "conflito de edição é apresentado sem sobrescrever silenciosamente"** foi verificada em TRÊS camadas, cada uma com regressão deliberada confirmada (remover a checagem, ver o teste falhar, restaurar, ver passar de novo): `internal/views/commands_test.go` (`TestUpdateView_ConcurrentEditConflict`, nível de comando Go), `cmd/server/views_test.go` (`TestEditorE2E_CreateTableViewSaveReopenPublish`, nível HTTP completo) e `migracao/packages/bff/test/mockGoServer.ts` (nível de mock do BFF, ver README do BFF).

`TestEditorE2E_CreateTableViewSaveReopenPublish` é a prova de ponta a ponta do critério "E2E cria tabela e view, salva, reabre e publica com dois papéis": um teste HTTP real (Postgres real, `httptest`, `ServiceIdentity` assinado/verificado de verdade) que percorre toda a cadeia — cria tabela, adiciona campo, cria view não publicada, confirma 403 para um ator público, salva, reabre, tenta salvar com `_version` obsoleto (409), publica (`min_role` → público), confirma 200 para o mesmo ator público, e confirma que o conteúdo do PATCH obsoleto nunca persistiu. Não há navegador disponível neste ambiente (confirmado em GO-018) — este é o "ponta a ponta" mais realista possível sem um, documentado como tal, não apresentado como um E2E de navegador que não aconteceu.

## Runtime de renderização de views (GO-020)

`internal/views/render.go` (novo) é o primeiro consumidor real dos campos `template`/`configuration` que GO-019 só persistia opacamente — decide se uma view é RENDERIZÁVEL pelo runtime novo e, se for, monta o plano de dados para desenhá-la, reaproveitando o compilador de consultas de `internal/records` (GO-012/013/015) por inteiro, sem SQL duplicado.

- **Subconjunto suportado, deliberadamente pequeno:** o legado tem 8 viewtemplates nativos e um viewtemplate `List` sozinho com ~40 opções de exibição (`base-plugin/viewtemplates/list.ts`, >2000 linhas) — portar tudo de uma vez não é um recorte executável. `ClassifyView` (nesta entrega) só aceita `template = "List"`, colunas `{type: "Field", field_name}` dentro de `configuration.layout.besides`, e um subconjunto fechado de `default_state` (`_order_field`, `_descending`). Qualquer recurso fora disso (join, agregação, ação, view embutida, qualquer outra opção de exibição, qualquer outro viewtemplate) é classificado como incompatível, com o motivo específico em `*UnsupportedLayoutError` — nunca uma tentativa de renderização parcial. **Corrigido em GO-039** (ver seção própria abaixo): a fonte de dados real do builder legado é `configuration.columns[]` (lista flat), não `configuration.layout.besides[]` (árvore de arranjo visual) — a leitura descrita aqui, embora correta contra os testes desta própria tarefa, nunca teria classificado corretamente uma view List real produzida em produção.
- **"Layouts incompatíveis bloqueiam publicação" (critério de aceite):** `CreateView`/`UpdateView` (GO-019, estendidos aqui) recusam qualquer transição em que o `min_role` resultante deixe de ser admin-only se `ClassifyView` falhar — 422 `view_unsupported` na camada HTTP. Editar mantendo a view admin-only nunca é bloqueado (um admin itera livremente antes de publicar).
- **Duas autorizações independentes:** `CompileListPlan` chama `GetView` (checa `identity.CanRead` sobre `view.MinRole`, GO-019) E `records.Rows` (checa `identity.CanRead` sobre `table.MinRoleRead`, GO-015) — publicar uma view nunca contorna o papel mínimo de leitura da tabela por baixo.
- **Rota nova:** `GET /v1/tenants/{tenant}/views/{id}/render` (paginação por cursor opaco, mesma convenção de `listRecordsHandler`) devolve o DTO (colunas resolvidas + linhas + paginação) — não HTML. `GET /v1/tenants/{tenant}/views` (listagem, também nova) enumera as views visíveis ao ator, filtro opcional por tabela.

**Achado de arquitetura, não bloqueador desta tarefa:** `internal/views` (GO-019) cria `_sc_views` com um shape PRÓPRIO — mas o legado (`packages/saltcorn-data/db/reset_schema.ts:112-122`) já usa esse MESMO nome de tabela, no MESMO schema por tenant (ADR-0004: Go e legado compartilham o Postgres, schema por tenant), com um shape DIFERENTE e incompatível (`viewtemplate` em vez de `template`, colunas extras). Nenhum teste hoje aciona essa colisão (todos usam schemas sintéticos, nunca um schema com as migrations reais do legado já aplicadas), mas um corte real da capacidade `tables.views` (ADR-0006/0008) para um tenant com histórico legado colidiria de verdade. Registrado como recomendação para antes de qualquer corte real — ver nota de escopo 5 em `docs/migracao-go/execucoes/GO-020.md` — não resolvido nesta entrega (fora do escopo, que é o runtime de renderização, não a reconciliação de schema).

**Verificação de regressão deliberada** (desabilitar → confirmar falha real → restaurar → confirmar passe), aplicada quatro vezes: bloqueio de publicação no domínio e via HTTP, classificação em `CompileListPlan` no domínio e via HTTP. O achado mais grave: sem a checagem de classificação, o endpoint de renderização devolvia 200 OK com as linhas reais da tabela para uma view do tipo "Show" — um vazamento de dados de uma view fora do subconjunto suportado, silencioso. Detalhes completos em `docs/migracao-go/execucoes/GO-020.md`.

## Show, Edit, escrita real e JoinField/Action (GO-039)

Estende o runtime de GO-020 para os viewtemplates Edit e Show, e para List com colunas `JoinField`/`Action` — o formulário de escrita real (`form_action`) e as duas capacidades de coluna que o pack piloto `guitars` exige. Escopo derivado da leitura de um pack REAL (`deploy/playwright_mobile/backups/guitars_backup.zip`), não de uma suposição de formato — ver `docs/migracao-go/execucoes/GO-039.md` para as 11 decisões de escopo.

- **Correção de fonte de verdade:** `configuration.columns[]` (lista flat, tipos capitalizados `"Field"`/`"JoinField"`/`"Action"` — o MESMO que `list.ts:1009` `get_state_fields` lê) substitui `configuration.layout.besides[]` como o que `classifyListColumns`/`classifyShowColumns`/`classifyEditColumns` interpretam. `layout` continua existindo no documento (é a árvore de arranjo visual que o builder legado reaproveitado, GO-018, também produz), mas este runtime nunca a interpreta — nem antes nem depois desta correção.
- **`internal/views/show.go` (novo):** `CompileShowPlan` — os valores de UM registro, resolvidos contra `configuration.columns[]` (só `Field`, sem ação nem join nesta entrega).
- **`internal/views/edit.go` (novo):** `CompileEditPlan` (leitura — registro existente ou em branco para criação, com opções reais carregadas de campos `select`/FieldKey via `loadEditFieldOptions`) e `SubmitEditView` (`form_action` — cria via `records.CreateRecord` ou atualiza via `records.UpdateRecord`, restringindo `values` aos campos que a própria view expõe). `resolveNavigate` calcula a decisão pós-submissão (`destination_type`): `"reload"` (padrão), `"referer"` (`Back to referer`), `"view"` (`View` + `view_when_done`).
- **`Save` e `SubmitWithAjax` mapeiam para o MESMO mecanismo backend** — só o estilo de requisição do frontend legado diferia entre as duas, nunca o contrato HTTP nem a validação.
- **Coluna `Action`/`Delete` em List** (`DeleteListRow`, `internal/views/render.go`): confere o `minRole` do PRÓPRIO nó de coluna (`ActionMinRole`) ALÉM do `min_role_write` da tabela (`records.DeleteRecordTx`) — dois níveis independentes de autorização, mesmo espírito do legado.
- **Coluna `JoinField` em List:** um nível (`campo_local.campo_remoto`), reaproveitando `records.Query.Joins` (GO-012) — `internal/records` já resolvia joins de um nível para consultas gerais, a extensão aqui foi só reconhecer esse tipo de coluna na classificação.
- **Achado real, corrigido, fora do escopo de views:** `internal/records.validateFieldValues` nunca aceitava um campo `FieldInteger`/`FieldKey`/`FieldDate` vindo de um corpo JSON HTTP — `encoding/json` decodifica número como `float64` e data como `string`, mas `validateValue` exigia `int64`/`time.Time` exatos. Nenhum teste HTTP anterior exercitava um campo não-texto (`TestCreateRecordHandler_Success` só usa `"label"`, texto) — escrever um registro com um campo inteiro/chave/data pela API HTTP real estava quebrado desde sempre. Corrigido com `coerceJSONValue` (novo, `internal/records/commands.go`).
- **Rotas novas:** `POST /v1/tenants/{tenant}/views/{id}/submit` (form_action, idempotente por `Idempotency-Key`) e `DELETE /v1/tenants/{tenant}/views/{id}/rows/{recordId}` (ação de coluna Delete, `?version=` para concorrência otimista). `GET .../views/{id}/render` (já existia) passa a despachar por template — `?record=` obrigatório para Show, opcional para Edit, ignorado por List.
- **Resultado no pack piloto guitars:** das 9 views, 6 operam leitura+escrita de ponta a ponta (`guitar_list`, `list_processed`, `show_guitar`, `edit_guitar_process`, `edit_processed_embed`, `add_process_type`); `create_guitar` opera para seus próprios campos (o sub-formulário embutido de `edit_processed_embed`, via um nó de layout `type: "view"` que nem aparece em `columns[]`, não é desenhado); `guitar_feed` (Feed) e `upload_photo` (fieldview `upload`, exige um `FieldFile` que `internal/metadata` ainda não tem) ficam explicitamente bloqueadas — GO-051 registrada para as duas, que compartilham a mesma capacidade de base (renderização de view aninhada).

**Verificação de regressão deliberada** (desabilitar → confirmar falha real → restaurar → confirmar passe), três vezes: `coerceJSONValue` (sem ela, o mesmo bug de escrita HTTP volta — status 400 real ao tentar salvar um campo `select`/FieldKey via `submit`), a guarda "campo fora da view" em `SubmitEditView` (sem ela, um cliente adulterado conseguiria submeter um campo que a view não expõe), e `ActionMinRole` em `DeleteListRow` (sem ela, um ator sem papel especial conseguiria excluir numa tabela com escrita pública liberada, mesmo com uma ação de coluna que deveria exigir mais). Um quarto achado real (não deliberado) apareceu no FRONTEND, pego pelo próprio teste novo: uma mensagem de "navegação pós-submissão" era limpa antes de aparecer, por ordem de chamadas errada em `ViewsListPage.tsx`. Detalhes completos em `docs/migracao-go/execucoes/GO-039.md`.

## `cli e2e-seed` — bootstrap de tenant para E2E de navegador (GO-021)

`cmd/cli` ganha o subcomando `e2e-seed` (`--dsn`, `--tenant`, `--email`): recria o schema do tenant do zero, aplica `EnsureSchema` de identidade/metadados/outbox/views, cria um usuário admin, e registra ownership de Go (`cutover.SetOwner`) para `tables.records`/`tables.schema`/`tables.views`. Não existe (nem deveria existir) uma rota HTTP pública para criar o primeiro usuário/conceder ownership — são ações anteriores a qualquer requisição autenticada. É o mínimo necessário para o harness de E2E de navegador real (`migracao/e2e/`, ver README próprio) preparar um tenant utilizável antes de subir `cmd/server`/BFF/frontend — paridade mínima com `saltcorn create-user`/`reset-schema` do CLI legado (matriz GO-001 §2.6), focada no que o harness precisa, não um comando de administração completo. Testado em `cmd/cli/e2eseed_test.go` (Postgres real, 2 testes: criação bem-sucedida e re-execução idempotente do ponto de vista do harness — recria do zero, não acumula).

```bash
go run ./cmd/cli e2e-seed --dsn "$SALTCORN_GO_TEST_DATABASE_URL" --tenant e2e_web
# {"tenant":"e2e_web","admin_user_id":1,"admin_role_id":1}
```

## E2E de navegador real (GO-021)

Até esta tarefa, "E2E" nas entregas anteriores (GO-018/019/020) significava HTTP real via `httptest`/Postgres real, documentado repetidamente como "sem ferramenta de browser disponível neste ambiente". **Essa afirmação estava desatualizada**: Playwright já está instalado neste ambiente (usado por `deploy/playwright` desde GO-002) e um Chromium/Firefox headless real funciona (confirmado nesta tarefa). `migracao/e2e/` (novo pacote, README próprio) é a correção — Playwright de verdade contra o stack completo (este backend Go real, compilado e rodando via `go build`; BFF real; frontend real, buildado e servido), dirigindo um navegador real: **6/6 testes em Chromium e 6/6 em Firefox** (fluxo criar/publicar/operar, teclado, responsividade, sanitização HTML). Ver `migracao/e2e/README.md` e `docs/migracao-go/execucoes/GO-021.md` para o design completo (WebKit não coberto por limitação real do ambiente — dependência de sistema ausente, sem acesso root para instalar) e os quatro achados reais corrigidos durante a implementação (CORS, limpeza de processos do harness, uma URL relativa quebrada em `bffClient.ts` desde GO-017, e um layout de view incompatível em `EditorPage.tsx`).

## Host temporário de extensões JS (GO-022)

`internal/pluginhost` é o cliente Go do host JS temporário (`migracao/packages/pluginhost`, README próprio) — um processo Node **separado do BFF** (ADR-0005: "o BFF Node.js não executa plugins de domínio... mesmo sendo ambos processos Node.js, host de plugins e BFF são sistemas com fronteiras de confiança diferentes") que avalia expressões/callbacks de plugin. Promove o protótipo de GO-004 (`docs/migracao-go/prototipos/GO-004-fronteira-rpc/`) a produto.

- **`Client`** gerencia um processo de host de VIDA LONGA (recomendação #1 do relatório de GO-004 — subprocesso por chamada custa 162-176ms de warm-up, proibitivo se repetido). Chamadas são serializadas por um mutex — simplificação deliberada, não fabricação: correlacionar callbacks de chamadas concorrentes pelo mesmo processo exigiria rastrear qual conjunto de capacidades pertence a qual chamada em voo, complexidade não justificada para o volume esperado (não é caminho quente).
- **Isolamento real de processo, não só de VM** (ADR-0005: "simples uso de VM não constitui toda a fronteira de segurança"): o host spawna com `--max-old-space-size` configurável (limite real de heap — um plugin que tenta esgotar memória derruba SÓ o processo do host, nunca o `cmd/server`); todo `Eval` recebe um `context.Context` — se o prazo expira, `Client` MATA o processo (pode estar preso num laço que nem o timeout interno do `vm.Script` conseguiu interromper) e a PRÓXIMA chamada sobe um host novo sozinha.
- **Capacidades explícitas, verificadas nos DOIS lados**: cada `Eval` declara `Capabilities []Capability` — um callback fora dessa lista é negado tanto no host (TypeScript) quanto no cliente Go (`serveCallback`), defesa em profundidade, nenhum lado confia cegamente no outro.
- **`CapDBRead` é a única capacidade implementada** — um callback de LEITURA genérico, resolvido reaproveitando `internal/records.Rows` com a MESMA autorização por papel de todo o resto do backend (GO-008/012/015) — nenhuma credencial de banco cruza para o processo Node. Escrita a partir de uma expressão fica fora de escopo (relatório de GO-004 §5: não prototipada, precisa de decisão própria de idempotência) — continua classificada como bloqueadora.
- **`ExpressionCapability = "plugins.expr"`** é o nome de capacidade usado com `internal/platform/cutover` — reaproveitando o MESMO mecanismo já em produção desde GO-009 (`tables.records`/`tables.schema`/`tables.views`), não um mecanismo por-plugin novo. Enquanto nenhuma chamada a `SwitchOwner` mencionar essa capacidade, `cutover.OwnerOf` devolve `OwnerLegacy` para qualquer tenant — o critério de aceite "plugin transacional incompatível mantém operação integral no legado" vale por construção, provado em `internal/pluginhost/cutover_test.go` e, desde GO-023, também aplicado de verdade por `internal/expression.Evaluator` (ver seção seguinte) via `cutover.Guard.Begin`.

**Verificação de regressão deliberada** (desabilitar → confirmar falha real → restaurar → confirmar passe), três vezes: contenção de timeout (sem ela, o processo preso é reaproveitado pela chamada seguinte — `go test -race` pegou uma condição de corrida REAL nessa configuração, não hipotética), contenção de crash (sem ela, a chamada seguinte tenta escrever no processo morto e falha com "broken pipe" em vez de subir um host novo), e um limite de memória real (`--max-old-space-size=32`, uma alocação deliberadamente grande derruba o processo por OOM de verdade — visível no stack trace nativo do V8 nos logs do teste — e a chamada seguinte ainda funciona). Detalhes completos em `docs/migracao-go/execucoes/GO-022.md`.

**Matriz de plugins atualizada** (critério de aceite): `docs/migracao-go/inventario/GO-001-matriz-capacidades.md` §2.4 — a linha "Motor de expressões JS" era classificada como "Bloqueador" simples antes de GO-004; agora reflete o que o protótipo e este host realmente provaram (fórmulas puras e callback de leitura são viáveis) e os três critérios concretos de incompatibilidade que continuam bloqueando (closure, escrita, singleton sem callback).

## Tipos e expressões prioritárias (GO-023)

Dois pacotes novos promovem o host de GO-022 a uma capacidade consumível pelo resto do backend, sem que nenhum chamador futuro precise reinventar coerção de tipo ou a checagem de cutover.

- **`internal/types`** — coerção explícita ("read") dos 5 tipos básicos de `metadata.FieldType` (`text`, `integer`, `boolean`, `float`, `date`), equivalente Go ao `read()`/`readFromDB()` de `base-plugin/types.ts` do legado, para valores BRUTOS (o que uma expressão do host devolve, ou o que viria de um formulário/CSV) — nunca substitui `internal/records.validateValue` (GO-013), que continua validando valores JÁ tipados em Go, sem coerção nenhuma. Onde a semântica diverge deliberadamente do legado (`Float.read` do legado "limpa" strings sujas como `"R$ 10,50"`; `Date.read` do legado devolve `null` silenciosamente para uma data que não parseia), este pacote NUNCA adivinha: devolve um erro explícito e tipado (`ErrCoercionInvalid`/`ErrCoercionUnsupportedFormat`). Não existe tipo Decimal dedicado — nem aqui, nem no legado — "decimal" é `Float`/`float64` (IEEE754) comparado com tolerância via `FloatEquals(a, b, decimalPlaces)`, que replica `Float.equals` do legado.
- **`internal/expression`** — a ÚNICA fachada Go para avaliar o subconjunto prioritário de expressões sobre o host de GO-022. `Evaluator.Eval(ctx, tenant, Request{...}, callbacks)`: (1) checa `cutover.Guard.Begin(tenant, pluginhost.ExpressionCapability)` ANTES de tocar no host — uma capacidade não trocada para `OwnerGo` devolve `ErrLegacyOwner` sem sequer iniciar o processo Node, o mecanismo de GO-009/GO-022 finalmente EM USO, não só reservado; (2) delega a `pluginhost.Client.Eval`; (3) coage o resultado bruto para `Request.ExpectedType` via `internal/types.Coerce` — um resultado de formato incompatível vira `ErrAmbiguousResult`, nunca um valor de tipo errado escapando silenciosamente.
- **Achado de GO-023 no próprio host (`migracao/packages/pluginhost`)**: o caso #6 de GO-004 (referenciar `Table`/`File`/`View` sem canal de callback explícito virava `undefined` silencioso) agora é um erro explícito — `Table`/`File`/`View` no sandbox são um `Proxy` que lança `unsupported_reference` em qualquer leitura/chamada. Confirmado por regressão deliberada: removendo o estojo, a mesma referência (`Table.findOne(...)`) passa a virar `runtime_error`, indistinguível de um bug comum da fórmula — exatamente a ambiguidade perigosa que o estojo elimina.
- **Corpus de expressões prioritárias** (`internal/expression/expression_test.go`, contra host real + Postgres real): aritmética/decimal síncrona, null-coalescing, boolean, data (documentando o round-trip JSON Date→string, não "corrigindo" silenciosamente), async via `callHost('db.read', ...)`, erro de runtime, closure não serializável, referência a singleton (`unsupported_reference`) e resultado ambíguo (`ErrAmbiguousResult`) — os seis primeiros itens do critério de aceite "corpus cobre coerção, null, datas, decimal, erros e async" mais os dois casos que provam "expressão desconhecida nunca muda resultado silenciosamente".
- **Fora de escopo, documentado, não esquecido**: wiring de `ownership_formula` em `internal/identity` (o sentinela `ErrOwnershipFormulaUnsupported` permanece sem nenhum call site — não existe ainda coluna de ownership por fórmula no catálogo Go, isso pertence a uma tarefa futura de autorização/RLS); escrita a partir de expressão (GO-004 §5, ainda sem decisão de idempotência); formatos de data locale-aware do legado (moment.js) — o subconjunto prioritário cobre RFC3339 e `AAAA-MM-DD`; wiring em `cmd/server`/rotas HTTP reais (GO-024 triggers/ações, GO-027 packs, GO-029 SDK são os consumidores planejados).

## Triggers, ações e workflows (GO-024)

Dois pacotes novos preenchem `internal/records.Hooks` (reservado desde GO-013 — "nenhuma automação real usa isto ainda") e são o primeiro consumidor real de `internal/expression.Evaluator` (GO-023) fora dos próprios testes.

- **`internal/triggers`** — catálogo `_sc_triggers` (When Validate/Insert/Update/Delete — o subconjunto ligado aos comandos de registro de GO-013) + `Dispatcher.HooksFor(tenant, user) *records.Hooks`. `WhenValidate` roda ANTES da escrita física (pode abortar a operação inteira, mesmo contrato de `Hooks` desde GO-013); `WhenInsert/Update/Delete` rodam DEPOIS, ainda na mesma transação — a menos que `Trigger.AfterCommit`. `Trigger.OnlyIf` (fórmula JS opcional) é avaliado via `internal/expression.Evaluator` — um erro ao avaliar (incluindo `ErrLegacyOwner`, capacidade de expressão ainda não é Go para o tenant) aborta a operação inteira de forma explícita, nunca decide silenciosamente "dispara" ou "não dispara" (a mesma disciplina de GO-023 aplicada à decisão de disparo). `ActionFunc` é o mecanismo de ação nativa em Go (ADR-0005) — só o MECANISMO de registro/despacho por nome é entregue aqui, não o catálogo completo de ações builtin do legado (GO-026/GO-029).
- **Efeitos pós-commit: divergência deliberada e mais forte que o legado.** O legado (`models/trigger.ts` + `db.afterCommit`, `packages/postgres/postgres.ts:775-839`) enfileira um trigger `_after_commit` numa lista EM MEMÓRIA do processo Node, fora da transação — se o processo cai entre o commit e a execução dessa fila, o efeito é PERDIDO SILENCIOSAMENTE. Aqui, `Dispatcher.enqueueAfterCommit` grava um evento outbox (GO-014) na MESMA transação da escrita: o evento já está durável no Postgres antes do commit terminar, e uma queda do processo depois do commit não perde nada — o worker (`outbox.ProcessPending`) processa depois. Confirmado por regressão deliberada: desabilitando a separação, a ação passa a rodar sincronamente dentro da transação da escrita (o teste que espera "nunca roda sincronamente" falha de verdade).
- **`internal/workflow`** — máquina de estados persistida e retomável (`_sc_workflow_runs`/`_sc_workflow_trace`, equivalente reduzido de `workflow_run.ts`/`workflow_step.ts`). `Advance` processa EXATAMENTE um passo por chamada, cada um em sua PRÓPRIA transação — nunca uma transação de longa duração cobrindo o workflow inteiro. Duas garantias, cada uma provada por regressão deliberada:
  - `SELECT ... FOR UPDATE` na linha do run serializa `Advance` concorrentes do MESMO run — o equivalente Postgres nativo ao `MultiNodeMutex` do legado (`models/multi_node_mutex.ts`), sem portar um mutex distribuído próprio. Removendo o `FOR UPDATE`: duas chamadas concorrentes processam REDUNDANTEMENTE o mesmo passo em vez de avançar coletivamente — o run trava sem nunca terminar (o outbox evita o efeito duplicado, mas não a estagnação).
  - O efeito de cada passo é protegido por `outbox.Do` com chave `(run, step_seq, nome do passo)` — o `step_seq` existe especificamente para que uma segunda VISITA ao mesmo passo (um loop no grafo, via `Step.Next`/`Step.Else`) nunca seja confundida com uma repetição da primeira visita. Removendo `step_seq` da chave: um workflow com loop real (`TestRun_LoopRevisitsSameStepName_EachVisitEffectRuns`) passa a colidir (`ErrKeyConflict`) na segunda visita ao mesmo passo, terminando em erro em vez de completar o loop — um bug real encontrado e corrigido nesta tarefa, não hipotético.
  - Retomada após queda: `TestAdvance_RolledBackAttempt_LeavesNoPartialEffect_ThenCleanRetry` prova que uma tentativa de `Advance` cuja transação nunca commita (queda simulada) não deixa nem o efeito do passo, nem o avanço de `current_step`, nem a chave de idempotência — uma nova tentativa encontra o estado exatamente como estava e roda de forma limpa, sem duplicar nem perder nada.
- **Corpus** (`internal/triggers/dispatch_test.go`, `internal/workflow/run_test.go`, contra host real + Postgres real): aborto por `Validate`, `OnlyIf` como portão de disparo, execução na mesma transação, enfileiramento pós-commit (com e sem rollback), retentativa sem duplicar evento, ação desconhecida, passo com `ErrorStep` (réplica da fixture do legado "exatamente 1 linha, nunca duplicada"), avanço concorrente, loop com revisita de passo, e capacidade de expressão nunca cortada para Go (`only_if` falha fechado, nunca decide silenciosamente).
- **Fora de escopo, documentado, não esquecido**: catálogo completo de ações builtin (I/O externo — e-mail/webhook/notificações são GO-026; SDK/plugins de terceiro são GO-029); agendamento/Schedule/Cron (portado por GO-025, ver seção seguinte); sub-workflows e formulário interativo ("Waiting"/`wait_info` do legado); wiring em `cmd/server`/rotas HTTP reais (nenhum consumidor HTTP constrói um `Dispatcher` ainda — a mesma lacuna documentada por GO-023 para `internal/expression`).

## Scheduler e coordenação de workers (GO-025)

Dois pacotes novos, mais wiring real em `cmd/worker` — ao contrário de GO-022/023/024, que deliberadamente não conectaram seus mecanismos a `cmd/server`/`cmd/worker`, GO-025 é especificamente sobre essa coordenação, então `runScheduledTriggersJob` conecta tudo de ponta a ponta.

- **`internal/scheduler`** — disparo de triggers por TEMPO (o subconjunto Weekly/Daily/Hourly/Often/Cron de `when_trigger` que GO-024 deixou fora de `internal/triggers`, que é ligado a comandos de registro, não a relógio). Unifica os CINCO mecanismos sem estado de última execução do legado (`models/scheduler.ts`: config global `next_{name}_event` para Hourly/Daily/Weekly sem hora fixa; regex `HH:MM`/`Weekday HH:MM` no campo `channel` para Daily/Weekly com hora fixa; janela de tick para Cron; "roda todo tick" para Often) num único mecanismo: uma expressão cron de 5 campos por trigger (`internal/scheduler.ParseCron`, mesma regra Vixie de OR entre dia-do-mês/dia-da-semana do legado) + uma coluna `next_run_at` persistida, avançada deterministicamente a cada execução via `CronExpr.NextAfter` (nunca o mesmo instante, mesmo quando `after` já satisfaz a expressão). `Timezone` é explícito por trigger (IANA, default UTC) — nunca a ambiguidade documentada do legado ("evaluated in the server's local timezone", com um bug conhecido de mistura local/UTC em `getWeeklyTriggersDueNow`). Um processo parado atravessando várias janelas PULA direto para a próxima ocorrência futura (nunca acumula/"catch up"), mesma política do legado, agora determinística em vez de depender de janelas de tick não sobrepostas.
- **`internal/platform/lease`** — exclusividade MULTI-PROCESSO por nome de job, com expiração explícita (TTL) em tabela (`_sc_leases`), reivindicada por um UPSERT atômico condicional (`Acquire`) e liberável só pelo dono corrente (`Release`). Divergência deliberada do legado (`pg_try_advisory_lock(11565)` em `packages/server/serve.js:450-494` — eleição de líder do PROCESSO INTEIRO, sem TTL, solta só quando a conexão morre, não testável deterministicamente): aqui a exclusividade é por TENANT (não pelo processo inteiro — dois tenants diferentes nunca esperam um pelo outro) e a expiração é um timestamp comparável, testável sem depender do ciclo de vida de uma conexão TCP. `cutover.Guard` (GO-009) sozinho NÃO resolve isto: é uma guarda em memória de UM processo, e duas instâncias do worker rodando ao mesmo tempo teriam cada uma sua própria Guard, ambas "Go owner" — nada impediria as duas de rodarem o MESMO job simultaneamente. `lease` é o que efetivamente serializa entre processos.
- **`cmd/worker.runScheduledTriggersJob`** — dois portões, nesta ordem: (1) `cutover.Acquire(scheduler.Capability)` — "scheduler antigo é desativado por escopo" (critério de aceite) vale por construção, mesmo mecanismo de GO-009/022/023/024: enquanto ninguém chamar `cutover.SwitchOwner` para esta capacidade e este tenant, o job nunca toca `_sc_scheduled_triggers`; (2) `lease.Acquire(nome="scheduler:"+tenant, owner=workerInstanceID, ttl=3×jobInterval)` — perder a disputa (`lease.ErrLeaseHeld`) é um resultado NORMAL com mais de um worker, não um erro: outro processo já está cuidando deste tenant agora. `workerInstanceID` é gerado uma vez por processo (hostname+PID+timestamp de início) — não precisa de aleatoriedade criptográfica, só precisa ser distinto de qualquer instância viva ao mesmo tempo.
- **Verificação de regressão deliberada**: (1) removida a condição `WHERE` do UPSERT de `lease.Acquire` — dois workers concorrentes passaram a conseguir o MESMO lease "exclusivo" ao mesmo tempo (`TestAcquire_ExclusiveWhileValid` falhou de verdade); (2) removido o avanço inicial de `CronExpr.NextAfter` (a busca passou a começar EM `after`, não depois) — a mesma expressão que já bate em `after` passou a devolver o PRÓPRIO `after` como "próxima" ocorrência (`TestCronExpr_NextAfter_NeverReturnsTheSameInstant` falhou de verdade) — um bug que, sem a correção, faria um trigger disparar repetidamente dentro do mesmo minuto até o relógio virar, em vez de uma vez por ocorrência.
- **Corpus** (`internal/platform/lease/lease_test.go`, `internal/scheduler/{cron,dispatch,cutover}_test.go`, contra Postgres real): exclusividade enquanto válido, reaquisição após expiração, renovação pelo mesmo dono, `Release` só pelo dono, disputa concorrente real (duas goroutines, exatamente uma sucede); parser cron válido/inválido (incluindo o subconjunto que NÃO suporta — intervalos e passos, erro explícito), regra OR dia-do-mês/dia-da-semana, 0 e 7 ambos domingo, `NextAfter` diário e por fuso horário, nenhuma ocorrência dentro do horizonte de busca; disparo de trigger devido, não-disparo do que ainda não é devido, falha de uma ação não aborta as demais do lote (savepoint), ação desconhecida registra erro mas avança a agenda mesmo assim, capacidade de scheduler nunca cortada para Go permanece `OwnerLegacy` por padrão.
- **Fora de escopo, documentado, não esquecido**: intervalos (`1-5`) e passos (`*/5`) na expressão cron — erro explícito, não interpretado; sub-segundo/"Often" exato do legado — granularidade de minuto; catálogo real de triggers agendados de produção (nenhum existe neste checkout, mesma lacuna já registrada desde GO-001/003/004 para plugins) — `internal/scheduler.Dispatcher.Actions` é o MECANISMO, uma única ação de demonstração (`"log"`) é registrada em `cmd/worker`.

## Arquivos e notificações (GO-026)

Dois pacotes novos, mais wiring real em `cmd/worker` (o primeiro consumidor REAL de `outbox.ProcessPending` desde GO-014 — antes, todo evento só era logado).

- **`internal/files`** — catálogo `_sc_files` + `Backend` (interface de armazenamento físico; só `LocalBackend` é implementado — S3 fica deliberadamente fora de escopo, o próprio legado marca S3 como "experimental" na UI e nenhum ADR exige S3 para o piloto). `Download` resolve o catálogo e confere `files.CanRead` (reusa `identity.CanRead`/`IsOwnerByField`, GO-008 — nenhuma checagem nova) ANTES de tocar o backend físico: "usuário sem acesso não baixa arquivo" é estrutural, `ErrNotAuthorized` nunca chega a abrir bytes.
- **"Uploads interrompidos são limpos" — garantia NOVA, não uma porta.** O legado (`express-fileupload` + `File.create()` em duas etapas sem transação entre elas) não tem nenhuma limpeza documentada de arquivo órfão. `LocalBackend.Save` escreve num nome de staging (`.uploading.<sufixo aleatório>`) e só promove por `rename` atômico se o upload terminar sem erro — qualquer interrupção (leitura falha, cliente desconectou) limpa o staging SINCRONAMENTE, dentro da própria chamada. `CleanupOrphans` é a rede de segurança para uma queda ABRUPTA do processo (kill -9, que nenhum `defer` intercepta) — varre por staging mais antigo que um limiar configurável, nunca toca um upload fresco em andamento; wired em `cmd/worker` (`runFileCleanupJob`). Confirmado por regressão deliberada: desabilitando a limpeza síncrona, um upload interrompido deixa o arquivo de staging para trás de verdade.
- **`internal/notify`** — e-mail (`SendEmail`, `net/smtp` da biblioteca padrão — sem dependência nova, sem OAuth2) e webhook (`SendWebhook`, `net/http` com timeout explícito de 10s — o legado não tem NENHUM timeout). "Falha do provedor entra em retry e duplicatas externas têm política explícita" (critério de aceite) é o contrato de `outbox.Do`/`outbox.ProcessPending` (GO-014) reaproveitado, não reinventado: `EnqueueEmail`/`EnqueueWebhook` gravam o evento na MESMA transação do chamador com uma chave de idempotência (mesma chave + mesmo conteúdo nunca duplica; mesma chave + conteúdo diferente falha com `outbox.ErrKeyConflict`); `Handler(smtpCfg, httpClient, fallback)` é o handler real que `cmd/worker.runOutboxJob` agora usa — uma falha do provedor propaga como erro, e `outbox.ProcessPending` decide o retry (mecanismo já testado desde GO-014). `fallback` preserva o comportamento de log de qualquer tipo de evento que não seja e-mail/webhook.
- **Correção de segurança deliberada: bloqueio de SSRF em `SendWebhook`.** Achado de preflight: a ação `webhook` do legado passa a URL de destino direto para `fetch`, sem NENHUMA checagem — nem timeout, nem proteção contra IPs privados/loopback/link-local. `guardHost` resolve o host via DNS de verdade (nunca confia só na sintaxe da URL) e recusa qualquer IP privado/loopback/link-local/não especificado/multicast — incluindo `169.254.169.254`, o endereço clássico de metadata de nuvem, o alvo mais comum de um ataque SSRF real. Confirmado por regressão deliberada: desabilitando o bloqueio, o cliente HTTP genuinamente tenta conectar a esses endereços (erro de conexão recusada/timeout observado nos testes, não uma simulação).
- **`internal/notify.Create` (notificações)** — grava a notificação e, se pedido, enfileira o e-mail correspondente NA MESMA transação. Divergência deliberada e mais forte que o legado: `Notification.create()` insere a linha e só DEPOIS, fora de qualquer transação, aciona `MailQueue` (fila em memória do processo, agendada via `setTimeout`) — se o processo cai com uma notificação "pending" agendada, o e-mail nunca sai (achado de preflight, não corrigido no legado). Canais push nativo (Web Push/FCM/APNS) e atualização dinâmica in-app ficam fora de escopo — exigem credenciais externas e bibliotecas pesadas sem exercício no piloto.
- **Corpus** (`internal/files/{storage,upload}_test.go`, `internal/notify/{webhook,email,outbox,notifications}_test.go`): upload/download com autorização por papel e por dono, upload interrompido, limpeza de órfãos (antigo removido, fresco preservado), falha ao criar catálogo limpa o arquivo físico; SSRF bloqueado (loopback/privado/link-local) e caminho de sucesso (com resolução de host controlada em teste); e-mail via um servidor SMTP falso real (protocolo de texto puro, sem dependência nova) — sucesso, sem autenticação, falha do provedor, conexão recusada; entrega de e-mail/webhook de ponta a ponta via `outbox.ProcessPending`; retry real após falha do provedor; duplicata de enfileiramento não duplica evento; tipo de evento desconhecido cai no fallback, nunca descartado.
- **Fora de escopo, documentado, não esquecido**: armazenamento S3 (interface pronta, sem implementação); miniaturas/redimensionamento de imagem; autenticação SMTP via OAuth2; corpo de e-mail HTML/MJML (só texto simples); canais de notificação push nativo e atualização dinâmica in-app; wiring de upload/download em rotas HTTP de `cmd/server` (testado no nível de pacote, mesma decisão de escopo já aplicada por GO-023/024 para seus próprios mecanismos); um diretório de armazenamento único e compartilhado entre tenants (namespacing por tenant fica para quando um consumidor HTTP real existir).

## Configuração, packs e biblioteca (GO-027)

Três pacotes novos — dois catálogos pequenos (`internal/library`, `internal/config`) que faltavam para o terceiro (`internal/pack`) fazer sentido.

- **`internal/library`** — catálogo `_sc_library` (name/icon/layout) para componentes reutilizáveis de builder, equivalente reduzido de `models/library.ts`. Layout é guardado como JSON opaco, sem nenhuma transformação — resolução de slots em runtime (`Library.resolveSegment` do legado) é responsabilidade de GO-020 (runtime de views), não deste pacote. `CreateOrReplace` é idempotente por nome (reinstalar o mesmo pack não duplica).
- **`internal/config`** — catálogo `_sc_config` chave/valor UNTYPED (JSON opaco), o subconjunto prioritário necessário para `Pack.Config` fazer round-trip sem perda. Deliberadamente NÃO o catálogo tipado completo do legado (`models/config.ts`, ~2140 linhas, ~40+ chaves com validação por tipo) — isso fica para uma tarefa futura dedicada; SMTP já tem seu próprio caminho parcial via `internal/notify` (GO-026), independente deste catálogo genérico. Não confundir com `internal/platform/config` — aquele é configuração do PROCESSO Go (variáveis de ambiente); este é configuração da APLICAÇÃO dentro de um tenant.
- **`internal/pack`** — o formato de export/import de aplicação inteira, equivalente reduzido do `Pack` do legado (`packages/saltcorn-admin-models/models/pack.ts`). Cobre só as entidades que JÁ existem como catálogo Go real: tabelas/campos (GO-011), views (GO-019/020), triggers (GO-024), triggers agendados (GO-025), biblioteca e config (esta tarefa). O Pack do legado também carrega pages/page_groups/roles/tags/models/model_instances/event_logs/code_pages — nenhuma dessas tem catálogo Go ainda (páginas nunca foram portadas; roles são papéis FIXOS por constante, não um catálogo dinâmico) — um Pack só pode conter o que o backend Go sabe recriar de verdade.
- **`Pack.Version` é um campo NOVO — o legado não tem nenhum.** Achado de preflight: compatibilidade entre versões de pack no legado é best-effort, campos ausentes viram zero-value silenciosamente. `Import` recusa explicitamente uma versão futura desconhecida (`ErrUnsupportedVersion`) em vez de tentar importar pela metade.
- **"Faltas de plugin/versão são reportadas antes de aplicar alterações" — garantia NOVA, não uma porta.** Achado de preflight: `can_install_pack` do legado só avisa sobre conflito de tipo de campo/nome de view — NUNCA checa plugin ou versão; `install_pack` continua instalando o resto do pack mesmo quando um plugin falha ao carregar (só `console.error`). Aqui, `Import` chama `Validate` PRIMEIRO — nenhum `INSERT` acontece se qualquer `PluginDependency` estiver ausente ou com versão incompatível (`*MissingDependenciesError`, lista COMPLETA de ausências de uma vez, não uma por vez). Como não existe inventário real de plugins de terceiro neste checkout (achado repetido desde GO-001/003/004), `availablePlugins` é sempre fornecido explicitamente por quem chama `Import`, nunca descoberto automaticamente.
- **Atomicidade "tudo ou nada" — herdada da convenção já estabelecida, não reinventada.** `Import` roda inteiro dentro da MESMA transação que o chamador controla (mesmo padrão de `CreateRecord`/`Advance`/`RunDue` desde GO-013): qualquer erro (referência a tabela desconhecida, conflito de nome) propaga imediatamente, e o `db.WithTenant` do chamador desfaz TUDO — nenhuma entidade parcialmente aplicada sobrevive a uma falha no meio do caminho.
- **Duas fases de criação de tabela: TODAS as tabelas (sem campos) primeiro, depois TODOS os campos de TODAS as tabelas.** Necessário para que um `FieldKey` referencie QUALQUER tabela do mesmo pack, mesmo uma declarada DEPOIS dela no array (ou uma referência circular entre duas tabelas) — um export real de uma aplicação com relações não garante nenhuma ordem topológica particular. Confirmado por regressão deliberada: colapsando para uma única fase (criar tabela e seus campos imediatamente, em um só laço), um pack com uma tabela referenciando outra declarada depois falha de verdade (`FieldDef.References não encontrada`).
- **Corpus de round-trip** (`internal/pack/pack_test.go`, contra Postgres real): constrói uma aplicação representativa (duas tabelas com relação `FieldKey`, uma view, um trigger, um trigger agendado, um item de biblioteca, configuração), exporta, importa numa tenant NOVA e vazia, reexporta, e compara byte a byte (JSON canônico) — nenhuma perda. Mais: dependência de plugin ausente/incompatível nunca cria nada (`ListTables` continua vazio depois); falha por referência a tabela desconhecida desfaz TUDO, inclusive a tabela válida do mesmo pack; versão de pack desconhecida é recusada sem tocar o catálogo.
- **Achado durante a implementação (não um bug de produção, um bug de fixture de teste)**: os testes originais deste pacote usavam sufixos de tenant com hífen (`"version-mismatch"`) combinados com nomes de função de teste longos, e criavam o schema de teste com `pgx.Identifier{...}.Sanitize()` diretamente sobre a string bruta — enquanto `database.WithTenant` resolve o schema via `tenancy.SchemaName` (que REMOVE caracteres não-identificador, não substitui por `_`). Para nomes curtos isso nunca divergia; para os nomes longos desta suíte, a truncagem de 63 bytes do Postgres cortava as duas versões (com e sem hífen) em pontos DIFERENTES, produzindo dois schemas diferentes e um erro real (`no schema has been selected to create in`). Corrigido aplicando `tenancy.SchemaName` na CONSTRUÇÃO do nome do tenant de teste, não só confiando na normalização interna de `WithTenant` — o helper de fixture agora usa exatamente a mesma string que `WithTenant` vai resolver.
- **Fora de escopo, documentado, não esquecido**: pages/page_groups/roles dinâmicos/tags/models/model_instances/event_logs/code_pages (sem catálogo Go ainda); loja remota de packs via HTTP (`packs_store_endpoint`, dependência de um serviço de terceiro inexistente neste checkout); catálogo de config TIPADO completo (~40+ chaves do legado); resolução de slots de biblioteca em runtime (GO-020); wiring de rotas HTTP de export/import em `cmd/server` (testado no nível de pacote, mesma decisão de escopo já aplicada por GO-023/024/026 para seus próprios mecanismos).

## Comunicação em tempo real (GO-028)

Um pacote novo pequeno — o Go só oferece o transporte de dados; o protocolo Socket.IO em si roda inteiramente no BFF Node.js (`migracao/packages/bff/src/realtime.ts`), nunca aqui (ADR-0003/ADR-0007: sessão de navegador e borda web são sempre BFF). Ver `docs/migracao-go/execucoes/GO-028.md` para o levantamento completo do legado (Socket.IO em `packages/server/serve.js`).

- **`internal/realtime`** — catálogo `_sc_realtime_events` (id bigserial, audience `broadcast`/`users`, payload JSONB opaco) + `Publish`/`ListSinceForActor`. `Publish` é chamado NA MESMA transação do efeito de domínio que origina o evento (ex.: `internal/notify.Create`) — mesma convenção de atomicidade "de graça" de todo o resto do backend. `ListSinceForActor` filtra por destinatário NO Postgres (`WHERE audience = 'broadcast' OR $ator = ANY(user_ids)`) — o BFF nunca recebe sequer a existência de um evento de outro usuário.
- **Isolamento por tenant — estrutural, não uma convenção de nome.** Achado do levantamento de GO-028: o legado isola tempo real inteiramente por nome de room (`_${tenant}_dynamic_update_room`, resolvido do header `Host` no handshake) — sem barreira própria do Socket.IO; se a resolução de tenant errasse, o vazamento seria silencioso, e não há teste automatizado disso no legado. Aqui, `_sc_realtime_events` vive no schema Postgres do PRÓPRIO tenant (`internal/platform/database.WithTenant`, mesma convenção de toda tabela do backend) — impossível uma consulta em um tenant enxergar a linha de outro, não uma questão de nomear certo.
- **Ordenação — `id` bigserial, nunca `created_at`.** `created_at` usa `now()` do Postgres, que é CONSTANTE dentro de uma transação (`transaction_timestamp()`) — dois eventos publicados na mesma transação teriam o MESMO timestamp, tornando a ordem entre eles indefinida se fosse esse o critério. `id` (bigserial, sempre estritamente crescente) é o único campo usado para ordenar e para o cursor de retomada (`after`/`next_after`).
- **`internal/notify.Create` (GO-026) agora também publica um evento em tempo real** para o usuário da notificação, na MESMA transação — fecha a lacuna que GO-026 tinha deixado explicitamente aberta ("atualização dinâmica in-app fica fora de escopo").
- **Rota HTTP nova**: `GET /v1/tenants/{tenant}/realtime/events` (`cmd/server/realtime.go`), atrás de `tenancy.Middleware` + `cutover.RequireOwnership(guard, "realtime.events", ...)` — mesmo padrão granular de corte Node/Go de toda rota de domínio desde GO-009/019. O BFF faz polling curto desta rota, uma vez por socket Socket.IO conectado, com o ServiceIdentity do PRÓPRIO ator daquele socket.
- **Achado real desta tarefa (bug de fixture de teste, mesma classe do achado de GO-027, causa diferente)**: um teste que precisava de DOIS tenants no mesmo `t.Name()` longo (`TestListSinceForActor_NeverLeaksAcrossTenantSchemas`) construía o segundo nome como `<nome-do-primeiro>_b` — como os dois nomes COMPARTILHAM o mesmo prefixo de mais de 63 bytes, o truncamento de identificador do Postgres produzia o MESMO nome de schema para os dois, mascarando o próprio teste de isolamento que deveria provar o contrário (`tenant B enxergou 1 evento publicado no tenant A`). Corrigido truncando o nome de teste sanitizado a um tamanho seguro (`shortSanitizedName`, 40 bytes) antes de compor qualquer sufixo — lição reaproveitável: dois tenants de teste derivados do MESMO nome longo de função nunca devem só concatenar um sufixo no fim.
- **Superfície coberta neste piloto**: só o equivalente ao `dynamic_update` de notificação. Ficam de fora, bloqueados (mesma disciplina de "sem inventário real" de GO-026 para push nativo): colaboração em tempo real por view (`collab_room` do legado), stream de logs de admin, progresso de restore de backup, e o namespace `/datastream` de upload — nenhum tem consumidor Go equivalente ainda, e nenhum é exigido pelos critérios de aceite desta tarefa (reconexão, sessão expirada, ordenação, isolamento por tenant).
- **Corpus** (`internal/realtime/events_test.go`, `cmd/server/realtime_test.go`, mais `migracao/packages/bff/test/realtime.test.ts` e `migracao/packages/frontend/test/realtimeClient.test.ts` do lado Node): ordenação estrita entre publicações concorrentes de audience diferente; `AudienceUsers` nunca visível a outro ator; `AudienceBroadcast` visível a qualquer ator; cursor `after` exclui eventos já entregues; isolamento estrutural entre dois schemas de tenant; rota HTTP recusa sem o corte de ownership; handshake Socket.IO recusa sem sessão; socket já conectado é desconectado à força quando a sessão expira; reconexão automática do protocolo real retoma sem perder nem repetir eventos via `?since=`.
- **Fora de escopo, documentado, não esquecido**: wiring de `internal/realtime.Publish` em `internal/triggers`/`internal/workflow` (só `internal/notify` foi conectado nesta entrega — o consumidor mais direto do achado de GO-026); áudience "público" (visitante anônimo, equivalente a `_${tenant}_public_dynamic_update_room` do legado); long-polling ou SSE como alternativa ao polling curto atual; qualquer wiring de UI real no frontend além do módulo `realtimeClient.ts` reutilizável (este pacote ainda não tem roteador nem páginas reais além do shell de demonstração, GO-018).

## SDK de extensão: tipos, views, ações e autenticação (GO-029)

GO-029 formaliza, como contratos Go documentados, os 4 pontos de extensão que um "plugin" do legado registra (`registerPlugin`, `packages/saltcorn-data/db/state.ts:1051-1218`) — e fecha a lacuna que GO-024 tinha deixado explicitamente reservada ("GO-026 e GO-029 são os consumidores planejados de um catálogo real" de ações). Ver `docs/migracao-go/execucoes/GO-029.md` para o levantamento completo do legado (anatomia de plugin, o que `base-plugin`/`sbadmin2` fornecem, ADR-0005/0006) e as decisões de escopo.

### Os 4 contratos

1. **Tipos** — `internal/types.Coerce(fieldType metadata.FieldType, raw any) (any, error)` (GO-023): uma função pura por `FieldType`, despachada centralmente por `Coerce`. Cobre 5 dos 6 tipos básicos do legado (text/integer/boolean/float/date) — `Color` fica de fora desta entrega (ver "Fora de escopo" abaixo).
2. **Ações** — `triggers.ActionFunc func(ctx, tx, table, row, config map[string]any) error` (GO-024, `config` acrescentado por GO-029): roda DENTRO da transação que disparou o trigger (ADR-0005 — ações do núcleo nunca vão ao host de plugins), registrada por nome em `Dispatcher.Actions`. `config` é `Trigger.Configuration` (coluna `jsonb` nova, GO-029) — os parâmetros PRÓPRIOS de cada instância de trigger (destinatário de um `send_email`, URL de um `webhook`), sem os quais o mesmo nome de ação sempre resolveria para a MESMA função sem dado próprio, impedindo ações reutilizáveis. `triggers.BuiltinActions()` entrega o catálogo nativo real: `send_email` e `webhook`, ligados a `internal/notify` (GO-026) via `EnqueueEmail`/`EnqueueWebhook` (idempotência por hash do conteúdo de `config` + id da linha — dois triggers diferentes sobre a mesma linha nunca colidem na mesma chave de idempotência, confirmado por regressão deliberada).
3. **Views** — `views.View{ID,Name,TableID,Template,MinRole,Configuration,Version}` + `ClassifyView`/`CompileListPlan` (GO-019/020): hoje só o viewtemplate "List" tem pipeline classify→plan→render real; um viewtemplate novo precisaria generalizar esse pipeline (não existe ainda uma interface `Viewtemplate` formal separada de "List").
4. **Autenticação** — `internal/identity` (GO-008): `HashPassword`/`CheckPassword`, `RoleID`/`CanRead`/`CanWrite`, `IsOwnerByField`. Deliberadamente **NÃO extensível por plugin** — ver "Bloqueadores" abaixo.

### Catálogo de ações nativas (`internal/triggers/actions.go`, novo)

```go
d := &triggers.Dispatcher{Expression: evaluator, Actions: triggers.BuiltinActions()}
```

`send_email` lê `config["to"]` (string ou lista), `config["subject"]`/`config["body"]` (com placeholders `{{campo}}` substituídos pelo valor do campo homônimo da linha — interpolação simples, sem motor de template completo); `webhook` lê `config["url"]` e envia a linha inteira como corpo JSON. Os dois validam a configuração ANTES de enfileirar (`ErrActionConfigInvalid` — nunca um envio silencioso com destinatário/URL vazio) e são idempotentes via `outbox.Do` (GO-014), com a chave incluindo um hash da própria `config` — achado de implementação confirmado por regressão deliberada: sem esse hash, dois triggers de `send_email` DIFERENTES (destinatários diferentes) sobre a MESMA linha colidem na mesma chave de idempotência e o Postgres rejeita a segunda inserção com `idempotency_key_conflict`, silenciosamente descartando o segundo e-mail.

### Contrato de apresentação (React/BFF) — confirmado, não uma porta

`sbadmin2` (`packages/saltcorn-sbadmin2/index.js`, investigado nesta tarefa) exporta só `{layout: {wrap, authWrap, renderBody}}` — zero `types`/`actions`/`viewtemplates`/`authentication`, e a única referência a `db` é uma leitura de uma string estática de versão (nenhuma query). Confirma o critério de aceite "plugins de apresentação seguem os contratos React/BFF e não acessam persistência": um componente de apresentação recebe dados JÁ prontos (menu, dados de view, alertas) e só produz marcação — o mesmo contrato que `migracao/packages/frontend` já segue desde GO-018 (consome o BFF via `bffClient.ts`, nunca acessa banco).

### O que ainda bloqueia a retirada do backend legado/host temporário (ADR-0006)

Lista explícita, exigida pelo critério de aceite "dependências do host temporário bloqueiam sua retirada":

1. **`run_js_code`/`run_js_code_in_field`** — por definição executa JS arbitrário do usuário; nunca terá versão "nativa Go", só adapter via host (ADR-0005). Não registrado em `BuiltinActions()` nesta entrega — precisaria de uma capacidade de host própria para "executar código", distinta de `internal/expression.Evaluator.Eval` (que avalia EXPRESSÕES, não blocos de código).
2. **Fórmulas/expressões de usuário** (`_only_if`, campos calculados) — já roteadas via `internal/expression.Evaluator` (GO-023), mas isso É o host, não uma saída dele.
3. **6 de 8 viewtemplates do legado** (Edit/Show/Feed/Filter/ListShowList/Room/WorkflowRoom) sem `internal/views` equivalente — enquanto uma aplicação usar essas views (o próprio pack piloto `guitars` usa Edit/Show/Feed), depende do caminho legado Node.
4. **`insert_any_row`/`modify_row`/`delete_rows`** — não portados nesta entrega: CRUD dirigido por trigger precisa de desenho próprio (evitar loop de trigger sobre a própria escrita, decidir contexto de autorização/papel do efeito) que não é incidental a "wiring de e-mail/webhook já prontos".
5. **`@saltcorn/any-bootstrap-theme`/`@saltcorn/flatpickr-date`** — plugins de terceiro reais usados pelo pack piloto `guitars`, sem código-fonte disponível neste checkout para portar ou auditar (substituídos funcionalmente no piloto — ver matriz de capacidades §2.4).
6. **Login social/OAuth/SAML** — zero plugins de auth de terceiro neste monorepo (confirmado por busca direta, não é lacuna de investigação); bloqueador permanente até existir inventário de produção.
7. **Catálogo completo de ~29 ações do legado** (`base-plugin/actions.ts`) — só `send_email`/`webhook` portados; o restante (`navigate`, `emit_event`, ações de terceiro, etc.) permanece no legado.

### Wiring em `cmd/server`/`cmd/worker` — deliberadamente fora desta entrega

Mesma decisão de escopo já aplicada por GO-023/024/026: o catálogo de ações e o `Dispatcher` são testados no NÍVEL DE PACOTE (via `records.CreateRecord` real, Postgres real), não conectados ao processo HTTP real. Achado desta tarefa: nenhum dos três (`internal/triggers.Dispatcher`, `internal/expression.Evaluator`/`pluginhost.Client`, `internal/workflow`) está de fato wireado em `cmd/server`/`cmd/worker` hoje — toda a cadeia de automação (GO-022→GO-025) segue testada e correta, mas inerte em produção. Conectar isso ao caminho real de `createRecordHandler`/`updateRecordHandler` (passar `Dispatcher.HooksFor(tenant, user)` como os `hooks` de `records.CreateRecord`, hoje sempre `nil`) exige também decidir o ciclo de vida do processo host (GO-022) dentro de `cmd/server` — trabalho de integração substancial, deixado para uma tarefa futura dedicada, não incidental ao "SDK de ações" desta entrega.

### Fora de escopo, documentado, não esquecido

Tipo `Color` (`internal/types`, o 6º tipo básico do legado) — sem uso pelo piloto `guitars` (que só exercita text/integer/boolean/float/date), adicioná-lo tocaria `internal/metadata.FieldType`, `internal/records.validateValue` e `internal/pack` simultaneamente; deixado para quando um consumidor real do tipo existir. Views Edit/Show/Feed/Filter/ListShowList/Room/WorkflowRoom (item 3 da lista de bloqueadores acima). CRUD dirigido por trigger (item 4). `run_js_code` como `ActionFunc` (item 1).

## Adapter SQLite (GO-030)

`internal/platform/sqlite` oferece um arquivo por tenant para uso desktop. O driver
`modernc.org/sqlite v1.36.0` é puro Go e compatível com a toolchain deste módulo.
`metadata`, consultas/comandos de `records` e idempotência/outbox usam a mesma
lógica de domínio nos dois bancos, através de `database.Tx`.

As entradas `records.{Compile,Rows,CreateRecord,UpdateRecord,DeleteRecord}Tx`
recebem essa interface e `TxHooks`. As entradas sem sufixo continuam compatíveis
com `pgx.Tx`/`Hooks`. A outbox segue o mesmo padrão: `EnsureSchemaTx`, `DoTx`,
`ListPendingTx`, `ListFailedTx`, `ProcessPendingTx` e `TxHandler`. Os callbacks devem
propagar erros para `WithTenant`, responsável pelo commit/rollback externo.

```go
err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx database.Tx) error {
    if err := metadata.EnsureSchema(ctx, tx); err != nil { return err }
    if err := outbox.EnsureSchemaTx(ctx, tx); err != nil { return err }
    _, _, err := outbox.DoTx(ctx, tx, requestKey, values,
        func(ctx context.Context, tx database.Tx) (any, []outbox.Event, error) {
            row, err := records.CreateRecordTx(ctx, tx, role, "items", values, nil)
            if err != nil { return nil, nil, err }
            return row, []outbox.Event{{Type: "created", Payload: row}}, nil
        })
    return err
})
```

| Comportamento | PostgreSQL | SQLite |
| --- | --- | --- |
| Tenant | Schema em pool compartilhado | Arquivo dedicado; uma conexão por instância/tenant |
| Escritor concorrente | Advisory locks e locks de linha | `BEGIN IMMEDIATE` reserva escritor; `busy_timeout(5000)` limita espera entre processos |
| Versão de registro | `xmin::text` | Coluna `_version`, incrementada pelos comandos de atualização |
| Unicidade de campo adicionado | Constraint na coluna | Índice único removido junto com o campo |
| Datas | `timestamptz` | `timestamp`, valores UTC com precisão de microssegundos |
| Outbox | `FOR UPDATE SKIP LOCKED` | Serialização do escritor na transação inteira |
| Tentativa de handler | Savepoint na transação | Savepoint na transação |

`metadata.EnsureSchema` acrescenta `_version` às tabelas de arquivos SQLite do
primeiro checkpoint, preservando dados existentes. Um campo de usuário chamado
`_version` impede a atualização e exige resolução explícita. Toda aplicação que
escreva registros deve usar os comandos para manter o token de concorrência.
Rollback em erro, cancelamento ou panic preserva atomicidade; o callback não deve
suprimir um erro de uma operação parcial e retornar sucesso.

Diferenças deliberadas: inteiros retornam como `int64` no SQLite e `int32` no
PostgreSQL para colunas integer (mesma representação JSON); datas representam o
mesmo instante, mas PostgreSQL pode retornar outro timezone; `Like` no SQLite usa
case folding ASCII, enquanto `ILIKE` no PostgreSQL segue a collation do banco.
Ordenação de NULL foi alinhada ao padrão PostgreSQL. SQLite não oferece RLS nativa:
a autorização de papel dos comandos/consultas e o isolamento por arquivo são
cobertos, mas o servidor, identidade, triggers e módulos posteriores continuam
com integração PostgreSQL. Esta entrega não habilita servidor HTTP ou mobile
SQLite; empacotamento e sync pertencem a GO-031/032.

As fixtures compartilhadas estão em `internal/platform/sqlite/parity_test.go`.
Com `SALTCORN_GO_TEST_DATABASE_URL` definido, executam nos dois adapters. Incluem
CRUD/tipos, erros de autorização/constraint, conflito de versão, injeção, joins,
paginação, idempotência concorrente entre handles, retries/savepoints e falha
terminal. Testes SQLite adicionais encerram um subprocesso antes/depois do commit
e verificam recuperação, replay e preservação de dados locais no upgrade.

## Sync e mobile offline (GO-031)

O protocolo 1 oferece `POST /v1/tenants/{tenant}/sync/{table}/exchange` por meio do
BFF em `/api/bff/sync/{table}/exchange`. `internal/sync` revalida escopo/papel,
executa mutações idempotentes e devolve conflitos explícitos e snapshot completo
autorizado. O HTTP resolve o papel atual do ator na mesma transação e configura
as GUCs de RLS; a rota exige ownership `tables.records`.

Prepare `sync.EnsureSchema` durante o provisionamento do tenant, junto de
`metadata.EnsureSchema` (a outbox já preparada pelas rotas de registros também
atende). A aplicação que atende HTTP não precisa executar DDL no sync. Erros HTTP
desfazem o lote; erros de registro classificados viram resultados por operação,
com savepoint, mantendo as demais operações válidas.

O cliente JS preserva o runtime Capacitor e mantém fila, conflitos e checkpoint
no SQLite. Ver `packages/saltcorn-mobile-app/src/sync-v1/README.md` e
[ADR-0011](../../docs/migracao-go/adr/0011-sync-versionado-offline.md). Limites
explícitos: 100 mutações, 1 MiB de entrada, 2000 linhas no snapshot; 413 reverte o
lote sem entregar snapshot parcial. Sync legado e UUIDs não são reinterpretados.

Validação integrada (BFF compilado, Node 22 e PostgreSQL de teste configurados):

```bash
SALTCORN_GO_TEST_MOBILE_E2E=1 go test -race -count=1 ./internal/sync ./cmd/server -run 'TestExchangeParity|TestScopeSchemaAndRollback|TestSnapshotLimitRollsBackBatch|TestPostgresOwnershipAndRevocation|TestSyncMobileE2E'
```

Da raiz, `node --test migracao/packages/mobile-sync-test/client.test.mjs` testa o
cliente com SQLite real. `.github/workflows/migracao-sync-ci.yml` executa ambos os
fluxos, incluindo cliente → BFF sessão/CSRF → Go → PostgreSQL. O binário Go não é
embutido no dispositivo.

## Encerramento gracioso

`cmd/server` e `cmd/worker` capturam `SIGINT`/`SIGTERM`, param de aceitar trabalho novo, e esperam o trabalho já em curso terminar (via `internal/platform/shutdown.Tracker`) antes de sair — dentro do prazo de `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS`. Se o prazo estourar, o processo registra um aviso e sai mesmo assim; isso é uma decisão operacional explícita, não um bug — ver `shutdown.Tracker.Drain`.

## CI

`.github/workflows/migracao-backend-ci.yml` roda `gofmt`, `go vet`, `go build` e `go test -race` neste módulo a cada push/PR que toque `migracao/backend/`, com um serviço Postgres efêmero do próprio job (schemas `acme`/`beta` preparados antes dos testes) para que a suíte de `internal/platform/database` rode de verdade em CI, não só localmente.

## Operação e distribuição (GO-032)

A [distribuição self-hosted](../distribution/README.md) empacota `cli`, `server`,
`worker`, BFF Node 22 e assets React/SB Admin 2 com manifesto de compatibilidade
(schema/contratos/hashes). `cli setup` substitui o provisionamento manual de
schemas para **instâncias Go novas e dedicadas**; não adota schemas legados.
`migrate`, `check`, `get-cfg`/`set-cfg`, `plugins`, `backup`/`restore` e `serve`
operam uma pasta privada separada da release. Migrations são versionadas e
atômicas; backup/restore exigem manutenção offline e destino vazio.

O perfil web permanece PostgreSQL. O mesmo CLI administra e recupera SQLite
no subconjunto portado em GO-030/031, recusando explicitamente `serve` web nesse
perfil. `login` gera acesso administrativo temporário para o operador local;
as regras de autorização continuam no Go. Consulte o runbook para instalação,
upgrade, recuperação, locks, dependências externas e limites de plugins/mobile.

## Administração de usuário e segurança do servidor (GO-044)

`internal/identity/admin.go` (novo) e `internal/identity/impersonation.go`
(novo) portam a superfície de `packages/server/auth/admin.ts` do legado que
faltava neste pacote — achado de uma auditoria independente de paridade (ver
`docs/migracao-go/inventario/GO-001-matriz-capacidades.md` §6.1): esses dois
arquivos vivem em `auth/`, não em `routes/`, e nunca tinham entrado em
nenhuma contagem/inventário anterior.

**Em escopo, portado e testado:**

- **Administração de usuário** (`admin.go`): listar/editar papel/deletar
  usuário (`ListUsers`/`UpdateUserRole`/`DeleteUser`), reset de senha
  (`SetPassword`/`GenerateRandomPassword` — texto plano só existe uma vez, na
  resposta da chamada), e listagem administrativa dos tokens de API de um
  usuário (`ListAPITokensForUser`, nunca reexibe hash/texto plano). Toda
  função é admin-only (`requireAdmin`).
- **Impersonação de usuário** (`impersonation.go`) — divergência deliberada e
  MAIS FORTE que o legado: `become-user` do legado só troca `req.user` na
  sessão, sem NENHUM registro de quem virou quem nem quando. Aqui, toda
  impersonação (`StartImpersonation`/`EndImpersonation`) grava uma linha em
  `_sc_impersonation_log` (schema novo) na MESMA transação da checagem de
  autorização — nunca é possível impersonar sem deixar rastro. Este pacote
  nunca cria a sessão de navegador do usuário impersonado — isso é sempre
  responsabilidade do BFF (ADR-0007); o Go só audita e autoriza.
- **Matriz de permissões por tabela** (`internal/metadata.UpdateTablePermissions`,
  novo) — `createTable` só define `min_role_read`/`min_role_write` na
  criação; esta é a única forma de mudá-los depois.
- **Force-logout** — inteiramente no BFF (`SessionStore.destroyAllForUser`),
  já que a sessão de navegador nunca é responsabilidade do Go.

Rotas HTTP novas em `cmd/server/users.go`/`cmd/server/tables.go`, registradas
em `cmd/server/main.go` sob `cutover.RequireOwnership` (exceto encerrar
impersonação, que exige só `tenancy.Middleware` — quem chama é o BFF
encerrando sua PRÓPRIA sessão de impersonação, nunca um usuário final
escolhendo um `log_id` alheio). Contrato em
`migracao/contracts/openapi/internal-api.yaml`.

**Deliberadamente fora de escopo, com justificativa registrada** (decisões
completas em `docs/migracao-go/execucoes/GO-044.md`):

- **Emissão/gestão de certificado SSL/Let's Encrypt** — o servidor Go não
  tem NENHUM código de TLS hoje (nem `http.ListenAndServeTLS`, nem gestão de
  certificado); fica para infraestrutura/borda, mesmo raciocínio já usado
  por ADR-0008 para o proxy reverso de corte.
- **CRUD de role customizada e restrições por role de auth/layout/push**
  (`auth/roleadmin.ts` do legado) — `identity.RoleID` é um conjunto fechado
  com ordem total sobre o qual toda a matriz `CanRead`/`CanWrite` (GO-008)
  foi desenhada; permitir role arbitrária sem redesenhar essa matriz
  arriscaria checagem de permissão inconsistente/contornável. Candidata a
  uma task própria de redesenho de autorização, não uma extensão incremental
  desta entrega.

## Automação ligada ao caminho HTTP real: triggers/ações e `run_js_code` (GO-040)

`internal/triggers.Dispatcher` (mecanismo desde GO-024) e
`internal/expression.Evaluator` (fachada desde GO-023) existiam só em
teste até esta tarefa — nenhum handler HTTP real construía um `Dispatcher`
nem passava hooks não-nulos a `internal/records`. GO-040 fecha essa
lacuna: uma requisição HTTP real de criação/atualização/remoção de
registro agora dispara triggers/ações de verdade, na mesma transação.

- **`updateRecordHandler`/`deleteRecordHandler` (novos, `cmd/server/records.go`)**
  — achado real de preflight: a descrição original da task presumia que os
  três handlers de escrita já existiam com `hooks=nil`; só `createRecordHandler`
  existia. As duas rotas (`PATCH`/`DELETE .../records/{id}`) já estavam no
  contrato desde GO-006, mas sem handler nem um jeito real de transmitir a
  versão esperada — corrigido junto com o handler (`_version` obrigatório no
  corpo do PATCH, `?version=` obrigatório no DELETE; `RecordUpdateInput`/
  `Record` do contrato também corrigidos, a versão antiga tinha campos
  fictícios `created_at`/`updated_at` que nunca foram implementados).
  Idempotência via `outbox.Do`, mesma convenção de `createRecordHandler`.
- **Wiring real do `Dispatcher` em `cmd/server/main.go`** — um único
  `*pluginhost.Client` por processo (início preguiçoso: só sobe o processo
  Node no primeiro `Eval` real, sem custo quando nenhum trigger usa
  `run_js_code`), fechado no shutdown gracioso. `dispatcher.HooksFor(tenant,
  user)` substitui o `nil` que os três handlers de escrita passavam a
  `internal/records` — `user` é `{"id", "role_id"}` do ator já resolvido por
  `resolveActorRole`, os dois únicos campos que fórmulas de trigger
  (`only_if`/`run_js_code`) precisam.
- **`run_js_code` (`internal/triggers/runjscode.go`, novo)** — ação nativa
  deliberadamente deferida em GO-029, portada aqui reaproveitando
  INTEGRALMENTE o mesmo contrato de avaliação que `only_if` (GO-023) já usa
  (`internal/expression.Evaluator.Eval`) — nunca uma segunda fronteira de
  execução de JS para o mesmo host. Herda os dois limites já documentados em
  GO-022/023: `code` precisa ser uma EXPRESSÃO, não um corpo de função com
  statements (`condição || (function(){ throw new Error(...) })()`, não
  `if`/`throw` soltos — divergência real e documentada do `function(row,
  user) { ... }` livre do legado); e uma referência a um singleton de
  domínio (`Table`/`File`/`View`) falha explicitamente
  (`ErrUnsupportedReference`), nunca um `undefined` silencioso — o trigger
  real do pack piloto guitars (`receive_share_trigger`) precisa de ESCRITA
  via `Table`, fora deste limite (ver GO-052).
- **Consumidor real do outbox de triggers `AfterCommit` (`cmd/worker/main.go`,
  `triggerOutboxHandler`)** — até esta tarefa, um trigger `AfterCommit`
  enfileirava um evento durável (`Dispatcher.enqueueAfterCommit`, GO-024) mas
  o fallback do `notify.Handler` só LOGAVA qualquer evento que não fosse
  e-mail/webhook ("demonstração, sem consumidor real ainda"). `triggerOutboxHandler`
  reconhece o prefixo `"trigger:"` e reexecuta a ação de verdade via
  `Dispatcher.RunOne` (exportado nesta tarefa, MESMO código usado pelo
  disparo síncrono — nenhum segundo caminho de despacho por nome de ação),
  dentro da transação própria do ciclo do worker; qualquer outro tipo de
  evento continua caindo no fallback de log, sem regressão.
- **Carve-outs para GO-052, achados de preflight ao ler o trigger real do
  pack guitars (`receive_share_trigger`)**: (1) mecanismo de evento NOMEADO
  (`Trigger.emitEvent`/`POST /api/emit-event` do legado) — `WhenTrigger` é um
  conjunto fechado (`Validate`/`Insert`/`Update`/`Delete`) atado aos hooks de
  `internal/records`, sem nenhum canal para um evento arbitrário disparar um
  trigger; (2) capacidade de ESCRITA em `internal/pluginhost` — o trigger
  real do pack faz `Table.findOne(...).insertRow(...)`, mas o host só expõe
  `CapDBRead` (decisão já registrada em GO-022). Nenhum dos dois é uma
  extensão mecânica do wiring desta task — cada um é uma capacidade nova e
  maior, com sua própria política de autorização/idempotência a decidir.

**Verificação de regressão deliberada** (desabilitar → confirmar falha real
→ restaurar → confirmar passe), três vezes: hooks de `createRecordHandler`
(sem eles, um trigger `Validate` configurado para abortar não impede mais a
escrita, e um trigger `Insert` não enfileira mais o e-mail — os dois
confirmados via HTTP real); hooks de `updateRecordHandler` (mesmo padrão,
via PATCH real); e o reconhecimento do prefixo `"trigger:"` em
`triggerOutboxHandler` no worker (invertendo a condição, o evento cai no
fallback de log em vez de executar `send_email` de verdade — confirmado via
`outbox.ProcessPending` real).

**Achado real de CI, corrigido**: `cmd/cli/e2eseed.go` (comando `cli
e2e-seed`, usado pelos harnesses de E2E/carga para provisionar um tenant
descartável, GO-021) nunca chamava `triggers.EnsureSchema` — irrelevante
enquanto hooks eram sempre `nil`, mas quebrou a criação de registro real
(`relation "_sc_triggers" does not exist"`) assim que esta task ligou
`Dispatcher.HooksFor` de verdade. O caminho de provisionamento de produção
(`internal/installation/migrate.go`) já incluía `triggers.EnsureSchema`
desde GO-024; só o atalho de bootstrap de E2E, anterior a GO-024, ficou
para trás. Corrigido, revalidado de ponta a ponta (não só `go test`): `cli
e2e-seed` → `cmd/server` real → HTTP create real com token de identidade
delegada assinado. Detalhes completos em
`docs/migracao-go/execucoes/GO-040.md`.

## Validação de plugins de terceiro do piloto ponta a ponta (GO-042)

Confirma, com evidência real (não só documentada), a substituição
funcional dos dois plugins de terceiro reais do pack piloto `guitars`
(`@saltcorn/any-bootstrap-theme`, `@saltcorn/flatpickr-date` — sem
código-fonte neste checkout, ver GO-001/GO-029).

- **Tema SB Admin 2**: já satisfeito por construção desde GO-018 — o
  frontend novo NUNCA ofereceu `any-bootstrap-theme` como opção; SB Admin
  2 é a ÚNICA folha de estilo/shell deste stack, em toda página. Nenhuma
  mudança de código nesta task.
- **Campo de data nativo** (`migracao/packages/frontend/src/render/
  EditView.tsx`): a fieldview `"flatpickr"` (a mesma que o pack `guitars`
  usa de verdade em `edit_processed_embed`/`upload_photo`, campo
  `date_processed`) agora vira um `<input type="date">` HTML5 NATIVO —
  antes (GO-039) virava um `<input type="text">` simples, sem seletor de
  calendário nem validação de formato pelo navegador. `internal/records.
  coerceJSONValue` (Go) só aceita RFC3339 completo, nunca a forma curta
  `AAAA-MM-DD` que `<input type="date">` produz/consome — o componente
  converte nas duas direções, o Go nunca precisa saber a diferença.
- **Prova ponta a ponta em navegador real** (`migracao/e2e/tests/
  date-field-edit.spec.ts`, novo): cria tabela/campos/views Edit e List
  via HTTP (mesmo shape de `configuration.columns[]` que o pack real
  usa — não existe hoje um tradutor do formato de `pack.json` do legado
  para `internal/pack.Pack`, formatos distintos, achado de preflight),
  abre a view Edit pelo navegador (Chromium+Firefox), confirma o
  `<input type="date">`, preenche e salva, reabre a view List sobre a
  MESMA tabela e confirma que o valor persistiu corretamente.

**Achado real, corrigido — `database.CollectMaps` devolvia `time.Time` no
fuso LOCAL DO PROCESSO, nunca UTC**: rodar o E2E acima pela primeira vez
contra um Postgres/processo real revelou que um campo `date` gravado como
meia-noite UTC (`"2024-03-20T00:00:00Z"`) voltava, num processo rodando
num fuso negativo (`America/Sao_Paulo`, UTC-3), como `"2024-03-19T21:00:
00-03:00"` — um dia ANTES do valor gravado. Causa raiz: `pgx` decodifica
`timestamptz` para `time.Time` no fuso `time.Local` do processo CLIENTE
(nunca UTC, nunca o fuso da sessão Postgres — confirmado `SHOW TimeZone`
= UTC no banco); `internal/metadata.FieldType.pgType()` mapeia `FieldDate`
para `timestamptz`, então todo campo de data saía deslocado dependendo só
de em que fuso o SERVIDOR estava rodando, nunca do valor realmente
gravado — nenhum teste anterior pegou isso porque nenhuma fixture Go
constrói um `time.Time` passando por um roundtrip real de Postgres com o
processo fora de UTC. Corrigido em `internal/platform/database.
CollectMaps` (o ponto genérico onde `internal/records.RowsTx`/
`CreateRecordTx`/`UpdateRecordTx` coletam linhas do driver): todo
`time.Time` é normalizado para UTC (`t.UTC()`) antes de sair deste
pacote — corrige o problema para QUALQUER consumidor, não só o caminho
HTTP desta task. Exatamente o tipo de divergência ambiental que "operar
ponta a ponta" (o aceite desta task) existe para revelar.

**Verificação de regressão deliberada** (desabilitar → confirmar falha
real → restaurar → confirmar passe), duas vezes: a conversão
`AAAA-MM-DD → RFC3339` em `EditView.coerceForSubmit` (sem ela, o Go
rejeitaria o valor com `ErrTypeMismatch`); e a normalização `.UTC()` em
`CollectMaps` (sem ela, `TestCollectMaps_NormalizesTimeToUTC` — que fixa
`time.Local` para um fuso negativo conhecido dentro do próprio teste,
determinístico independente de onde o CI realmente roda — reproduz
exatamente o mesmo bug encontrado no E2E: `Location()` no fuso local, dia
deslocado). Detalhes completos em `docs/migracao-go/execucoes/GO-042.md`.

## Versionamento de linha e sistema de tags (GO-045)

Duas capacidades distintas, ambas "NÃO LISTADA ANTERIORMENTE" na matriz
de capacidades (GO-001) antes desta task — nenhum catálogo Go existia
para nenhuma das duas.

**Versionamento de linha** (`models/table.ts`, flag `versioned`) —
`internal/metadata.Table.Versioned`: se true, `CreateTable` já cria
`<nome>__history` (uma coluna por campo, sem NOT NULL/UNIQUE — um
snapshot é um registro ponto-no-tempo, nunca sujeito às mesmas
constraints da tabela viva); `AddField`/`DropTable` mantêm a tabela de
histórico sincronizada; `SetTableVersioned` habilita/desabilita depois da
criação (desabilitar DESCARTA o histórico, mesmo comportamento do
legado). `internal/records` grava um snapshot COMPLETO
(`insertHistoryRow`, `_history_version` sempre `COALESCE(MAX(...),0)+1`
numa subquery — mesma técnica de `next_version_by_id` do legado, sem
race condition) a cada `CreateRecordTx`/`UpdateRecordTx` bem-sucedido.

**Achado real de leitura do legado**: `Table.deleteRows` NUNCA chama
`insert_history_row` — só insert/update gravam histórico; uma linha
deletada simplesmente some da tabela principal, seu último snapshot
continua sendo o do último insert/update. O texto do aceite original
desta task ("grava histórico a cada update/DELETE") presumia o
contrário, sem ter confirmado contra o código real — corrigido aqui para
seguir o comportamento VERDADEIRO do legado, não a paráfrase do aceite.

`GetHistoryTx`/`RestoreRowVersionTx` (leitura/restauração, expostas via
`GET .../records/{id}/history` e `POST .../records/{id}/restore`) —
restaurar é sempre ADITIVO: `RestoreRowVersionTx` delega ao MESMO
`updateRecordTx` de qualquer UPDATE real (validação, controle de
concorrência otimista, hooks de trigger), só que com um parâmetro interno
extra (`restoreOfVersion`) que grava, no NOVO snapshot resultante,
`_restore_of_version` apontando para a versão restaurada — nunca
sobrescreve nem apaga um snapshot existente.

**Sistema de tags** (`models/tag.ts`/`tag_entry.ts`, novo pacote
`internal/tags`) — CRUD de tag (idempotente por nome) e associação a
UMA entidade por vez (`TagEntryRef`, XOR entre `TableID`/`ViewID`,
`ErrInvalidEntry` se ambos ou nenhum). **Divergência deliberada e
documentada**: só Table/View, nunca Page (páginas nunca foram portadas
para Go) nem Trigger (`internal/triggers.Trigger`, GO-024, nunca modelou
um campo `Name` — toda referência de Pack é por NOME, nunca por ID
interno; sem nome, uma tag de trigger não seria portável entre
tenants/instalações, quebrando essa invariante). **Achado real,
corrigido**: uma `UNIQUE (tag_id, table_id, view_id)` composta NUNCA
detecta conflito quando uma das duas colunas é NULL (regra SQL padrão —
NULL não é igual a NULL), exatamente o caso comum aqui (`TagEntryRef`
sempre tem uma das duas nil) — `AddEntry` deixou de ser idempotente na
prática (duplicava a cada chamada repetida), pego pelo teste desta task.
Corrigido com dois ÍNDICES ÚNICOS PARCIAIS (`WHERE table_id IS NOT NULL`
/ `WHERE view_id IS NOT NULL`), cada um só enxergando as linhas onde a
coluna correspondente é preenchida.

**Integração com `internal/pack`** (GO-027) — `Pack.Tags []TagPack`
(sempre por nome, round-trip Export→Import→Export sem perda, mesmo
critério de todas as demais categorias). `pack.ExportFilteredByTag` (o
segundo critério de aceite desta task — "tags usadas para filtrar um
export") calcula o FECHAMENTO TRANSITIVO de tabelas referenciadas por
FieldKey: uma tag só marcando a tabela "books" (que referencia "authors")
produz um Pack incluindo `authors` também — sem isso, o próprio Import do
Pack filtrado falharia com "tabela desconhecida". Triggers/
ScheduledTriggers/Library/Config nunca entram num export filtrado por tag
(propriedades globais da aplicação, não de uma tabela/view específica).

**Deliberadamente fora de escopo, documentado**: exposição HTTP/CLI/UI de
tags (mesmo estágio que `internal/pack.Export`/`Import` em si, que também
nunca foram expostos em nenhuma task anterior); undo/redo explícito de
versionamento (navegação por cadeia de `_restore_of_version` — a
restauração pontual já está implementada e testada, só o atalho de
navegação fica de fora); mudar `versioned` de uma tabela que já tem dados
mantém o comportamento do legado (descarta o histórico ao desabilitar),
nunca uma migração retroativa.

**Verificação de regressão deliberada** (desabilitar → confirmar falha
real → restaurar → confirmar passe), quatro vezes: `insertHistoryRow` em
`CreateRecordTx` (sem ela, uma tabela versionada não grava histórico
algum); `_restore_of_version` em `RestoreRowVersionTx` (sem ele, um
snapshot restaurado não fica marcado como restauração); os dois índices
únicos parciais de `_sc_tag_entries` (sem eles, volta o bug de
`AddEntry` duplicando associações); e o fechamento transitivo de
`ExportFilteredByTag` (sem ele, o Pack filtrado tem um FieldKey
apontando para uma tabela ausente, e o próprio Import do pack filtrado
falha — confirmado end-to-end, não só a lista de tabelas). Detalhes
completos em `docs/migracao-go/execucoes/GO-045.md`.

## Internacionalização (i18n) da interface (GO-047)

Divergência DELIBERADA e documentada do mecanismo do legado
(`packages/server/app.js`, lib npm `i18n`, ~36 idiomas em
`models/config.ts:available_languages`, catálogo estático por locale,
mais `/localizer/*` — UI de administração de idiomas/strings e tradução
assistida por LLM): o "Aceite" desta task pede "pelo menos 2 idiomas
configuráveis por tenant, com seleção por usuário persistida", não o
catálogo de 36 idiomas nem a UI administrativa — portado o MECANISMO com
2 idiomas reais (pt/en — pt porque é o idioma em que todo este port foi
escrito desde GO-018).

- **`identity.User.Language`** (novo campo, mesmo nome/semântica do
  `User.language` do legado) — a preferência EXPLÍCITA do usuário, "" =
  sem preferência. `SetUserLanguage` (self-service, `PATCH
  /v1/tenants/{tenant}/actor`) — o ator só muda a PRÓPRIA preferência,
  nunca a de outro usuário (nunca há um userID em parâmetro).
- **`default_locale`** (tenant) — reaproveita `internal/config` (GO-027,
  chave-valor genérico), sem schema novo. Fallback final `"pt"` quando o
  tenant nunca configurou.
- **Ordem de resolução do idioma efetivo, mesma do legado**
  (`routes/utils.ts`, `setLanguage`/`applyUserLocale`): `User.Language`
  (se setado) → cookie `lang` (BFF) → `default_locale` do tenant → `"pt"`.
  Resolvida UMA vez no BFF (`migracao/packages/bff/src/locale.ts`,
  `resolveLocale`), exposta no bootstrap (`GET /api/bff/bootstrap`,
  campo `locale`) — o frontend nunca reimplementa essa cadeia.
- **RTL** — mesma lista do legado (`ar`, `he`, `fa`, `ur`, `yi`) portada
  em `locale.ts` (`isRTL`), mesmo que nenhum dos 2 idiomas desta entrega
  seja RTL — não hardcoded para "sempre LTR", sem retrabalho se um
  idioma RTL for adicionado ao catálogo do frontend depois.
- **Frontend** (`migracao/packages/frontend/src/i18n/`) — catálogo de
  traduções pt/en com chaves SEMÂNTICAS (nunca a própria string em
  português como chave — evita colisão de pontuação/espaço), aplicado às
  ~20 strings de produto real do frontend React (levantamento manual
  completo: `App.tsx`/`EditorPage.tsx` são páginas de demonstração/dev,
  documentado no próprio comentário de cada arquivo desde GO-018/019,
  não são alvo de tradução). Seletor de idioma na Topbar, com troca
  otimista local + persistência assíncrona via `PATCH /api/bff/actor/language`.

**Deliberadamente fora de escopo, documentado** (decisão explícita
exigida pelo enunciado da task): a UI de administração de idiomas/
strings custom (`/localizer/*` do legado) e a tradução assistida por LLM
(`saltcorn dev translate`) — nenhuma integração de LLM existe no backend
Go hoje, seria uma capacidade nova e maior, não uma extensão de i18n; o
catálogo desta entrega é pequeno o bastante para tradução manual direta.

**Verificação de regressão deliberada** (desabilitar → confirmar falha
real → restaurar → confirmar passe), três vezes, uma por camada:
`identity.SetUserLanguage` não persistindo (backend Go —
`TestSetActorLanguageHandler_PersistsAndReflectsInGetActor` falha de
verdade); a prioridade de `resolveLocale` ignorando `actorLanguage` (BFF
— o teste de bootstrap com preferência gravada falha de verdade); a
interpolação de `{param}` no motor de tradução (frontend — os testes com
parâmetro falham de verdade, mostrando o template cru). Além de um
achado real de ISOLAMENTO DE TESTE (não do produto): o harness E2E
compartilha o mesmo usuário/tenant entre Chromium e Firefox — a
preferência de idioma persistida por um navegador vazava para o
próximo, corrigido limpando a preferência antes/depois do teste
dedicado. Detalhes completos em `docs/migracao-go/execucoes/GO-047.md`.

## Editor visual de Workflow (GO-048)

**Achado central do preflight**: `internal/workflow` (GO-024) só tinha
estado de EXECUÇÃO persistido (`_sc_workflow_runs`/`_sc_workflow_trace`)
— a `Definition` (grafo de passos) era montada em código Go compilado,
nunca lida do banco (ver comentário de `definition.go`, que já apontava
isso como escopo de "uma tarefa futura"). Sem uma definição persistida,
o próprio critério de aceite de GO-048 ("um usuário cria/edita um
workflow visualmente... e o resultado é executado de ponta a ponta") era
impossível de cumprir — esta task teve que absorver esse trabalho de
backend antes de qualquer UI, uma expansão de escopo real, não tangencial.

- **`_sc_workflows`/`_sc_workflow_steps`** (novo) — a definição
  persistida, um passo por linha, com `only_if`/`next_step`/`else_step`/
  `error_step` espelhando exatamente os campos homônimos de `Step`
  (`definition.go`) e `position_x`/`position_y` só para o layout do
  canvas (nunca lido por `Compile`). `Workflow` é uma tabela PRÓPRIA, não
  uma reutilização de `_sc_triggers` (que exige `table_id NOT NULL` — o
  legado hospedava workflows num `Trigger` com `when_trigger` "API call"/
  "Never", um conceito que não existe no catálogo Go de triggers hoje).
- **`Compile`** (`internal/workflow/store.go`) — a ponte que faltava
  entre `_sc_workflow_steps` (dado) e `Definition` (código): resolve
  cada `action_name` num catálogo de `ActionBuilder`s, monta os `Step`s,
  devolve a `Definition` pronta para `Start`/`Advance`/`RunToCompletion`
  (todos de GO-024, INTOCADOS — nenhuma mudança no motor de execução).
- **Catálogo de ações** (`actions.go`) — deliberadamente restrito a
  efeitos INTERNOS: `set_context` (mescla um objeto JSON literal no
  contexto, puro) e `count_rows` (conta linhas de uma tabela do tenant
  via `internal/metadata.GetTable`, leitura pura). `send_email`/`webhook`
  (o catálogo de `internal/triggers`, GO-029) ficam FORA: `StepFunc`
  recebe só `(ctx, tx, wfContext)`, sem `runID` — a chave de idempotência
  do enfileiramento em `internal/notify` precisa ser única POR EXECUÇÃO,
  e derivar isso sem `runID` exigiria injetar um token sintético só para
  essa finalidade, uma complexidade não exigida pelo critério de aceite
  (que não depende de efeito externo observável).
- **Ramificação binária, nunca a expressão N-vias do legado** — o motor
  Go só executa `Next`/`Else` conforme `OnlyIf` (`run.go`, decisão já
  tomada em GO-024); o editor visual nunca oferece uma condição que o
  motor não executaria.
- **HTTP** (`cmd/server/workflow.go`) — CRUD completo + `POST
  .../workflows/{id}/run` (compila, inicia e roda até o fim numa única
  chamada síncrona, sem fila/polling — a prova ponta a ponta do critério
  de aceite). `run` é idempotente por `Idempotency-Key` só na etapa de
  `Start` (compilar+iniciar); a definição é compilada DUAS vezes (uma
  dentro da transação idempotente, outra fora para alimentar
  `RunToCompletion`, que abre suas próprias transações por passo) —
  `Compile` é leitura pura e barata, mais simples e correto do que tentar
  serializar uma `Definition` (que contém closures Go) através da
  fronteira do outbox.
- **Frontend** (`migracao/packages/frontend/src/workflow/
  WorkflowEditorPage.tsx`) — canvas `@xyflow/react` (o sucessor mantido
  do React Flow do editor legado) + painel lateral nativo (sem a
  infraestrutura de `showIf`/Monaco/HTML-do-servidor do legado — o
  catálogo de 2 ações desta entrega não precisa de formulário dinâmico
  genérico). Conectar `next_step`/`else_step` é feito pelos seletores do
  painel, não arrastando uma aresta (`nodesConnectable={false}`) — sem
  ambiguidade sobre qual handle uma conexão arrastada representaria.

**Deliberadamente fora de escopo, documentado**: geração de workflow por
IA (`copilot_generate_workflow`, plugin+LLM), workflow como ação de outro
workflow (sub-workflow — já excluído em GO-024), o catálogo de ~14 ações
builtin + ações de plugin do legado além de `set_context`/`count_rows`,
tipos de passo que pausam execução (`UserForm`, `WaitUntil` etc. — sem
suporte a status "Waiting" no motor Go), e um node type dedicado para
`ForLoop` (um loop é só uma aresta comum apontando para um passo anterior
— o motor já suporta isso nativamente).

**Achados reais corrigidos durante a execução** (não são bugs de
produto, são lacunas de bootstrap de teste/E2E — mesma classe já vista
em GO-040/045/047): (1) `cmd/cli/e2eseed.go` e as fixtures HTTP de
`cmd/server` não chamavam `workflow.EnsureSchema`; (2) `cmd/cli/
e2eseed.go` registrava ownership de Go só para
`["tables.records", "tables.schema", "tables.views"]` — a nova
capability `"workflows"` não estava na lista, e sem registro explícito
`cutover.RequireOwnership` falha FECHADO por padrão (nunca aberto):
toda rota de workflow devolvia 409 imediatamente no E2E até a correção.

**Verificação de regressão deliberada**: `UpdateStep` sem o `AND
xmin::text = $N` na cláusula `WHERE` (Go — o teste de conflito de versão
de passo falha de verdade, `<nil>` em vez de `ErrVersionConflict`); a
chave de idempotência de `runWorkflow` sem o escopo fixo (BFF — duas
chamadas com o mesmo corpo criam DOIS runs, `1 !== 2`); e um laço
infinito real encontrado no frontend (`workflow?.steps ?? []` criando uma
referência nova a cada render enquanto nenhum workflow está selecionado,
alimentando um ciclo `useMemo`→`useEffect`→`setState` sem fim — corrigido
com uma constante `EMPTY_STEPS` estável). Detalhes completos, com
comandos e evidência de falha genuína, em
`docs/migracao-go/execucoes/GO-048.md`.

## View aninhada, viewtemplate Feed e upload de arquivo (GO-051)

Carve-out do preflight de GO-039: as três capacidades que `create_guitar`/
`guitar_feed`/`upload_photo` (pack piloto `guitars`) exigiam e GO-039
deixou de fora por não caberem com o rigor devido naquela entrega.

- **View aninhada** (`internal/views/edit.go`) — um nó de layout
  `{"type": "view", "view": ..., "relation": ...}` (sintaxe do legado
  `.{tabelaPai}.{tabelaFilha}${campoFK}`, ex.:
  `.guitars.processed$guitar`) embute uma view Edit inteira DENTRO de
  outra, filtrada pelo id da linha pai. `configuration.layout` é a ÚNICA
  representação possível desse nó (confirmado lendo o pack.json real —
  `create_guitar` não lista `edit_processed_embed` em `columns[]`) — uma
  exceção deliberada, documentada no cabeçalho do pacote, à regra geral
  de GO-039 ("layout é só a árvore de arranjo visual, nunca a fonte de
  dados"). `resolveNestedEditPlans` só resolve quando o registro pai já
  existe (`recordID != 0`) — um pai ainda não salvo não tem como ter
  linhas filhas, então o sub-formulário embutido só aparece a PARTIR do
  primeiro salvamento, nunca antes (documentado, não um bug silencioso).
  `maxNestedViewDepth = 1` limita a recursão a um nível — uma view
  aninhada não pode, por sua vez, embutir outra (erro explícito, nunca
  recursão silenciosa).
- **Viewtemplate Feed** (`internal/views/feed.go`, novo) — renderiza um
  card por linha via `show_view` (o mesmo mecanismo de uma view Show
  standalone) mais `view_to_create`/`view_to_create_id` para o botão
  "Criar". `classifyFeedConfig` só valida a presença de `show_view` em
  `configuration` — a classificação, diferente de List/Show/Edit, não
  depende de `columns[]` nem de campos da tabela (o Feed não tem colunas
  próprias, delega tudo à view Show referenciada).
- **`metadata.FieldFile`** (`internal/metadata/fieldtype.go`) — reaproveita
  a mesma representação física/validação de `FieldKey` (inteiro,
  `coerceJSONValue`/`validateValue`), mas aponta para a tabela FIXA do
  sistema `_sc_files` (GO-026), nunca uma tabela dinâmica do tenant — por
  isso não precisou estender `FieldDef.References`; a DDL gerada é
  `REFERENCES _sc_files(id)`.
- **HTTP de upload/download** (`cmd/server/files.go`, novo) — GO-026
  entregou `internal/files.LocalBackend` deliberadamente sem consumidor
  HTTP ("um consumidor HTTP real fica para uma tarefa futura dedicada",
  comentário do próprio pacote) — esta é essa tarefa. `POST
  .../files` (multipart, um campo `file`) devolve o id do arquivo criado;
  `GET .../files/{id}` serve os bytes com o Content-Type original. O
  fluxo do formulário Edit é upload-depois-referencia, não multipart do
  formulário inteiro: o campo `file` é enviado primeiro (devolve um id),
  a submissão do Edit é uma chamada JSON separada com esse id como valor
  do campo — evita precisar de parsing multipart dentro de
  `SubmitEditView`. `downloadFileHandler` mapeia tanto
  `files.ErrNotFound` quanto `files.ErrNotAuthorized` para 404 (nunca
  revela a existência de um arquivo a quem não tem acesso — recomendação
  do próprio `internal/files.ErrNotAuthorized`). Simplificação de escala
  deliberada: o download carrega o arquivo inteiro em memória (sem
  streaming) — aceitável para o piloto (fotos, não arquivos grandes),
  mesmo espírito de `editFieldOptionsLimit`.

**Deliberadamente fora de escopo, documentado**: nenhuma UI para
"adicionar uma linha filha nova" dentro de uma view aninhada — o backend
só conhece o formato de campos de uma view filha a partir de linhas JÁ
EXISTENTES (`NestedEditPlan.Rows`); com zero linhas não há formato para
montar um formulário em branco. Editar/salvar linhas EXISTENTES já
satisfaz o critério de aceite literal ("renderiza e salva... embutido").
Nenhuma variante de relação além da sintaxe única confirmada no pack
piloto (ex.: many-to-many) é suportada.

**Achado real de bootstrap, mesma classe recorrente de GO-040/045/047/048**:
`cmd/cli/e2eseed.go` e as fixtures HTTP de `cmd/server`
(`records_test.go`, `views_test.go`) não chamavam `files.EnsureSchema`,
e `e2eseed.go` não registrava a capability `"files"` na lista de
ownership — corrigido proativamente por inspeção, aplicando a mesma
lição já documentada nas tasks anteriores.

**Achado real no harness de E2E**: `migracao/e2e/run.sh` não definia
`SALTCORN_GO_FILES_ROOT_DIR` ao lançar o backend Go — confirmado por
evidência de falha genuína (WARN no log do backend + `POST .../files`
devolvendo 503 `files_unavailable` durante uma execução real do E2E, e
o teste de upload falhando por receber um objeto de erro em vez de um
id numérico no campo do registro). Corrigido acrescentando
`FILES_ROOT_DIR=$(mktemp -d)` ao bloco de variáveis do backend, com
limpeza no `cleanup()` existente.

**Bug real de HTML encontrado e corrigido antes de rodar qualquer
teste** (ver README do frontend para o detalhe): compor `<EditView>`
recursivamente para as linhas filhas inicialmente as desenhava DENTRO do
`<form>` da view pai — HTML inválido (`<form>` aninhado). Corrigido
movendo o grupo de views aninhadas para fora do `<form>` pai, como
irmão dentro de um `<div>` envolvente.

**Verificação de regressão deliberada** (uma por camada, evidência
completa em `docs/migracao-go/execucoes/GO-051.md`): Go — desativar a
checagem de `relation` incompatível com a tabela pai em
`resolveNestedEditPlans` (o teste de mismatch falha de verdade); BFF —
quebrar o delimitador do parser multipart (`--${boundary}` →
`${boundary}`, o teste de upload/download passa a comparar bytes
diferentes dos enviados); frontend — reintroduzir o `<form>` aninhado
(o teste dedicado que conta `<form>` irmãos falha de verdade).
