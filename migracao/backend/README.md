# saltcorn-go — backend

Módulo Go da migração ([docs/migracao-go/](../../docs/migracao-go/)). Implementa a estrutura definida em [ADR-0001](../../docs/migracao-go/adr/0001-backend-go-cqrs.md): monólito modular com CQRS lógico, três executáveis (`server`, `worker`, `cli`) reutilizando os mesmos serviços internos.

**Estado atual:** fundação (GO-005) + tenancy e contexto transacional (GO-007) + identidade, hashes, roles, ownership, RLS, tokens de API e MFA (GO-008) + registro de ownership de escrita e guarda de drenagem para o corte gradual (GO-009) + logs estruturados, métricas e correlação de trace (GO-010) + catálogo de tabelas/campos/relações dinâmicos e evolução de schema (GO-011) + compilador de consultas dinâmicas (GO-012) + comandos de registro (insert/update/delete) com controle de concorrência (GO-013) + idempotência e outbox transacional (GO-014) + autorização em joins/agregações de leitura (GO-015) + avaliação com benchmark de projeções CQRS, adiada (GO-016, ADR-0010). Ainda sem API pública completa — o restante do domínio entra nas tarefas seguintes, sobre esta mesma base.

## Estrutura

```
cmd/
  server/   processo HTTP — health/readiness (GO-005) + rota de exemplo com tenancy (GO-007)
            + verificação real de identidade delegada quando configurada (GO-008)
            + guarda de ownership de escrita na rota de exemplo (GO-009)
            + logs estruturados, GET /metrics e trace por requisição (GO-010)
  worker/   processo de background — loop periódico (GO-005) + jobs por tenant (GO-007)
            + guarda de ownership de escrita no job placeholder (GO-009)
            + logs estruturados, métricas de job e trace por execução (GO-010)
            + job de processamento de outbox por tenant (GO-014)
  cli/      linha de comando (paridade com o saltcorn-cli atual, gradual)
internal/
  identity/  hash de senha (bcrypt), papéis, ownership por campo, tokens de
             API (gerados/só o hash é persistido), TOTP/MFA, guarda contra
             estratégias de autenticação não suportadas (GO-008)
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

## Encerramento gracioso

`cmd/server` e `cmd/worker` capturam `SIGINT`/`SIGTERM`, param de aceitar trabalho novo, e esperam o trabalho já em curso terminar (via `internal/platform/shutdown.Tracker`) antes de sair — dentro do prazo de `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS`. Se o prazo estourar, o processo registra um aviso e sai mesmo assim; isso é uma decisão operacional explícita, não um bug — ver `shutdown.Tracker.Drain`.

## CI

`.github/workflows/migracao-backend-ci.yml` roda `gofmt`, `go vet`, `go build` e `go test -race` neste módulo a cada push/PR que toque `migracao/backend/`, com um serviço Postgres efêmero do próprio job (schemas `acme`/`beta` preparados antes dos testes) para que a suíte de `internal/platform/database` rode de verdade em CI, não só localmente.
