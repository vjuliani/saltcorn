# Matriz de paridade — GO-033

`matrix.json` cobre **cada uma das 70 linhas** do inventário GO-001, na mesma ordem. IDs CAP são estáveis; alterar o inventário exige reconciliar a matriz. Cada linha explicita cobertura, suítes/pacotes de prova, participação no piloto e lacuna residual. Os testes verificam as capacidades implementadas; não substituem uma comparação completa com aplicações legadas reais.

O piloto é o `guitars` proposto em GO-001: web PostgreSQL, dois tenants/papéis, relação, List/filtro, formulário e automação. A seleção do piloto inclui conservadoramente infraestrutura web/admin e extensões necessárias à operação; capacidades específicas de mobile, SQLite web, ML, discovery e autenticação externa ficam no integral. Não é aceite operacional de um tenant de produção, cujo inventário ainda falta. O perfil integral acrescenta todo o inventário, SQLite, mobile e extensões. **Ambos permanecem bloqueados**: em especial, faltam integração HTTP da automação e templates/formulários usados pelo piloto. Substituições visuais propostas em GO-029 ainda precisam de validação na aplicação completa. Não reduzir o piloto a uma demonstração List para obter um gate verde.

## Executar em Linux

Requer Go 1.22, Node 22, Python 3.10+, PostgreSQL e ferramentas 16, `oapi-codegen` 2.8.0, Chromium/Firefox do Playwright 1.63.0. O runner instala dependências com os lockfiles. Instalar os navegadores previamente com `npm ci --prefix migracao/e2e` e `(cd migracao/e2e && npx playwright install chromium firefox)`; a CI instala também bibliotecas do sistema.

**Use cluster exclusivo descartável**, nunca a prévia nem produção. Os testes criam/removem schemas/bancos e o harness recria o tenant `go033_web`. A fixture SQL falha se já inicializada; para repetir apenas a matriz, reutilize o cluster inicializado, sem aplicar novamente o SQL. Verifique que nenhuma execução anterior continua ativa. O nome obrigatório do banco é uma barreira contra engano, não prova que o destino pode ser apagado.

```bash
docker --context default run -d --name saltcorn-go033-pg \
  -p 127.0.0.1:55574:5432 \
  -e POSTGRES_PASSWORD=go033_test_only -e POSTGRES_DB=go033_parity postgres:16
# Aguarde pg_isready antes de inicializar.
PGHOST=127.0.0.1 PGPORT=55574 PGUSER=postgres PGDATABASE=go033_parity \
  PGPASSWORD=go033_test_only psql -X -v ON_ERROR_STOP=1 -f migracao/parity/fixture.sql
export SALTCORN_PARITY_DISPOSABLE=1
export SALTCORN_GO_TEST_DATABASE_URL='postgres://postgres:go033_test_only@127.0.0.1:55574/go033_parity'
export SALTCORN_GO_TEST_DATABASE_URL_RLS='postgres://parity_user:parity_test_only@127.0.0.1:55574/go033_parity'
export SALTCORN_GO_TEST_INSTALLATION_DATABASE_URL="$SALTCORN_GO_TEST_DATABASE_URL"
export SALTCORN_GO_TEST_MOBILE_E2E=1
python3 -m unittest discover -s migracao/parity -p 'test_*.py' -v
python3 migracao/parity/run.py --output /tmp/go033-parity-01 --gate audit
```

Saída obrigatoriamente em diretório novo fora do checkout. `report.json` contém comando/cwd/exit/duração/hash de cada log, testes Go aprovados por pacote, contagens e skips, versões das ferramentas, manifesto completo do release e resultados por capacidade/perfil. `inputs.json` registra hashes do código, contratos, lockfiles, workflow e inventário, incluindo mudanças ainda não commitadas. O runner verifica que esses inputs não mudaram durante a execução. `RESULTS.md` é a leitura humana. Não versionar logs de sessão nem arquivos privados de instalação.

- `--gate audit` exige todas as oito suítes aprovadas e evidências presentes. Lacunas documentadas são resultados legítimos da auditoria, não testes aprovados da capacidade ausente.
- `--gate pilot` / `--gate integral` executam a mesma coleta e retornam **1** se houver qualquer lacuna aplicável. Não autorizam deploy: GO-034/035/036 exigem ensaios operacionais próprios.
- Skip de dependência/configuração é erro. As únicas dispensas Go são o benchmark GO-016 (carga pertence a GO-035) e o helper de crash SQLite, cujo teste pai deve passar. TAP/Vitest/Playwright rejeitam skips, todo, zero testes e falhas; os dois navegadores devem constar sem retries mascarando falha.

## Eixos e limites da evidência

| Eixo | Evidência executada | Limite |
| --- | --- | --- |
| HTTP/domínio/autorização | Backend `-race`, PG, RLS com NOSUPERUSER/NOBYPASSRLS; BFF sessão/CSRF/realtime | Sem paridade de todas as rotas públicas/admin legadas |
| Contratos | Lint, geração TS e Go, drift, build/vet e cliente; versões incompatíveis também nos testes sync/instalação | API OpenAPI 0.1.0, sync v1, compatibilidade do release 1/schema 2 |
| UI | Vitest e E2E real Go+BFF+React Chromium/Firefox | Sessão semeada; fluxo List/editor, não aplicação guitars completa; WebKit sem validação |
| Plugins | Host JS real e testes Go de expressões/callback autorizado, ações nativas e pack | Host temporário necessário; extensões de produção não inventariadas |
| PG/SQLite | Corpus records/outbox/sync/instalação, rollback/crash/upgrade/restore | SQLite administrativo/offline; web SQLite recusado |
| Mobile | SQLite real no cliente JS e HTTP cliente→BFF→Go→PG, repetição/conflitos/checkpoint | Android/iOS/driver Capacitor/push não executados; runtime preservado |
| Distribuição | Construção isolada, processos reais, setup, versão, upgrade, backup/restore e restart | Linux; CLI administrativa parcial, não upgrade automático do legado |

## Retomada e limpeza

Não há opção para transformar relatórios antigos em evidência atual nem pular suítes. Após correção relevante, gerar nova tentativa, preservando a anterior e seu erro. Relatórios em execução são gravados após cada comando. Se houver interrupção, conferir processos/portas e leases antes de recomeçar; encerrar somente processos comprovadamente pertencentes ao harness. Portas E2E padrão 8091/3101/4173, configuráveis via `SALTCORN_E2E_*_PORT`; portas ocupadas são recusadas antes do seed, sem `fuser -k`.

Remover somente o container/volume GO-033 após conferir que seus testes acabaram (`docker --context default rm -fv saltcorn-go033-pg`). Não apagar `/tmp/saltcorn-preview`, filas locais do usuário ou volumes de outros containers. O pacote gerado tem diretório próprio por tentativa e não altera a release que serve a prévia.
