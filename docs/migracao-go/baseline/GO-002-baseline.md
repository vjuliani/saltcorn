# GO-002 — Baseline de comportamento e desempenho

Relaciona-se à tarefa [GO-002](../TASKS.md#go-002--criar-baseline-de-comportamento-e-desempenho) e ao [plano de arquitetura](../README.md). Histórico de execução em [execucoes/GO-002.md](../execucoes/GO-002.md). Depende de [GO-001](../inventario/GO-001-matriz-capacidades.md) (DONE) para orientar quais suítes/pacotes priorizar.

**Commit de referência:** `6557f7624104bb7d688b40a33f88ccbe2df5d153` (inclui GO-001 integrado).
**Ambiente:** Node `v22.23.2`, npm `12.0.2`, Docker `25.0.2`. Postgres de teste **isolado e descartável** em container Docker próprio desta execução (`postgres:16`, porta local `55555`, banco `saltcorn_test`) — não usa o serviço Postgres do host nem containers de outros projetos em execução na máquina. Container removido ao final da tarefa.
**Datasets:** fixtures sintéticas de `packages/saltcorn-data/db/fixtures.ts` (usadas pelas suítes `data`/`server`) e dados de UI gerados pelos próprios testes Playwright; nenhum dado real ou de produção foi usado — não há inventário de produção neste checkout (ver README, premissas de fase 0), então a exigência de "dataset sanitizado" é satisfeita trivialmente por não existir dado real disponível. O pack `guitars` (recomendado como piloto em GO-001) não foi usado nesta rodada porque as suítes atuais não o carregam; fica registrado como candidato a dataset de carga quando houver um ambiente de piloto real (F2/F3).

## 1. Reprodutibilidade — achado de ambiente

`npm ci` falha neste commit: `package.json`/`package-lock.json` estão fora de sincronia (`EUSAGE`, ~15 pacotes ausentes do lockfile). O próprio `deploy/docker-compose-tests/*/Dockerfile.test` já usa `npm install --legacy-peer-deps` em vez de `npm ci` — não é uma regressão desta tarefa, mas vale registrar como observação para GO-005 (pipeline Go/CI) e para quem for reproduzir manualmente.

`npm install --legacy-peer-deps` em npm 12 bloqueia por padrão scripts de instalação de dependências não listadas em `allowScripts` no `package.json` (proteção padrão do npm contra supply-chain, não específica deste ambiente). Isso deixa `sqlite3` sem o binding nativo compilado, o que quebra a **descoberta de comandos do `saltcorn-cli`** (o oclif tenta carregar todos os comandos para montar o manifesto; um deles carrega `@saltcorn/data`, que tenta resolver o driver SQLite por padrão quando não há config, falhando ao exigir o binding nativo) — o comando `run-tests` some silenciosamente do `--help` e falha com "command not found" ao ser chamado diretamente, sem indicar a causa real. Correção aplicada nesta tarefa: `npm install-scripts approve sqlite3` (grava `allowScripts.sqlite3: true` em `package.json`) seguido de novo `npm install`. Esse ajuste está incluído nesta PR porque é necessário para qualquer pessoa reproduzir a suíte com um npm moderno, não é específico deste sandbox.

`deploy/playwright/` **não é um workspace do npm** (root `package.json` só lista `packages/*`), então suas dependências (`@playwright/test`) precisam de `npm install` próprio dentro da pasta — não documentado em nenhum README do diretório. O binário do Chromium também precisa ser instalado à parte (`npx playwright install chromium`); `--with-deps` exige `sudo` (indisponível neste ambiente) mas o binário sozinho funcionou sem instalar dependências de SO adicionais.

## 2. Suíte de dados (`packages/saltcorn-data`)

Comando: `PGHOST=127.0.0.1 PGPORT=55555 PGUSER=postgres PGPASSWORD=postgres PGDATABASE=saltcorn_test TZ=UTC packages/saltcorn-cli/bin/saltcorn run-tests saltcorn-data`

| Métrica | Resultado |
| --- | --- |
| Testes / suítes | 846 testes em 219 suítes |
| Aprovados / reprovados | **846 / 0** (com `TZ=UTC`) |
| Duração | 55,6 s |

**Achado de ambiente (não é defeito do produto):** na primeira execução, **sem** forçar `TZ=UTC` (timezone do host: `America/Sao_Paulo`, UTC-3), 2 testes falharam por causa de fronteira de data: `get by date error / should run view on date` (esperava `'Carl Rogers'`, obteve `'No row selected'`) e `localized dates in csv import` (esperava dia `15`, obteve dia `14`). Repetindo com `TZ=UTC` os mesmos 846 testes passam. Hipótese confirmada por reprodução: os testes assumem UTC (consistente com o que um runner de CI normalmente usa) e o processo Node herda o timezone do sistema quando `TZ` não é definido. **Recomendação:** fixar `TZ=UTC` explicitamente no comando de teste (CLI ou script), não deixar implícito no ambiente — relevante para a matriz de compatibilidade de datas/timezone já sinalizada em GO-001.

Nenhum outro defeito observado nesta suíte. Cobertura inclui automação (`Workflow run actions`, `Workflow run userform`, `Workflow mutex` com `AcquireLock`/`ReleaseLock` e timeout de lock) — serve de caracterização inicial de "jobs" sem precisar de um ambiente de produção.

## 3. Suíte de servidor (`packages/server`)

Comando: mesmo ambiente acima, `saltcorn run-tests server`.

| Métrica | Resultado |
| --- | --- |
| Testes / suítes | 599 testes em 142 suítes |
| Aprovados / reprovados | **599 / 0** |
| Duração | 105,5 s |

Nenhum defeito observado.

## 4. Benchmark de renderização (`npm run bench:render`)

Mede o custo de renderizar uma view List (25 linhas × 18 colunas = 450 células) de 4 formas, isolando o custo de avaliar expressões sobre o controle `as_text` (que não avalia nada).

| Estratégia | ms/render (20 iterações) | custo por célula sobre o controle |
| --- | --- | --- |
| `as_text` (controle) | 4,12 | — |
| `show_with_html` | 17,54 | 29,8 µs/célula |
| `cell_css_formula` | 24,82 | 46,0 µs/célula |
| `FormulaValue` | 21,77 | 39,2 µs/célula |

Este é o único benchmark de desempenho já instrumentado no repositório; não há ferramenta de carga HTTP (autocannon/k6/artillery) configurada em nenhum `package.json` do monorepo. **Lacuna registrada:** medir p95/p99 de latência HTTP sob concorrência exigiria introduzir essa ferramenta — não foi feito nesta tarefa por não fazer parte das suítes existentes (escopo de GO-002 é usar os casos das suítes atuais); fica como próximo passo explícito, não como número inventado.

## 5. Playwright E2E (`deploy/playwright`)

Comando: `deploy/playwright/run.sh` (reset de schema, cria usuário admin, sobe `saltcorn serve` na porta 3014 sobre o mesmo Postgres isolado, roda `npx playwright test`, encerra o servidor). Apenas o projeto `chromium` está habilitado no `playwright.config.js` (firefox/webkit comentados); `workers: 1`, `retries: 0` fora de CI.

| Métrica | Resultado |
| --- | --- |
| Testes | 271 (42 arquivos de spec) |
| Aprovados / reprovados | **260 / 11** |
| Duração | 16,1 min |

**Falhas observadas (execução única, sem retry — protocolo exige não apagar falha anterior com um retry verde; portanto tratadas como pendência a confirmar, não como defeito fechado):**

| # | Spec | Asserção que falhou | Padrão |
| --- | --- | --- | --- |
| 1 | `TC_02_Left_Panel_validation` | Visibilidade do botão "Discover tables" | Independente |
| 2 | `TC_08_About_Application` | Estado do checkbox na aba "Mobile App" | Independente (raiz) |
| 3, 4 | `TC_08_About_Application` (abas "Development", "Notification") | `page/context` fechado | Cascata da falha 2 (mesmo `describe`, página compartilhada) |
| 5 | `TC_14_Join` | Visibilidade de campo de join | Independente |
| 6 | `TC_15_Aggregation` | `page.click`: página/contexto fechado | Possível cascata (contexto encerrado antes da asserção) |
| 7 | `TC_16_badges` | `page.click`: página/contexto fechado | Possível cascata |
| 8 | `TC_43_Library_Sharing` | Página A não reflete texto atualizado a partir da página B | Independente (raiz) |
| 9 | `TC_43_Library_Sharing` | Slot de campo compartilhado não preenchido na segunda colocação | Independente (raiz) |
| 10 | `TC_43_Library_Sharing` | Página A não renderiza conteúdo do slot de container | Independente (raiz) |
| 11 | `TC_43_Library_Sharing` | `scrollIntoViewIfNeeded`: página/contexto fechado | Cascata das falhas 8–10 (mesmo `describe`) |

Portanto: **~6 causas-raiz independentes** (não 11), sendo 4 delas (#2, 8, 9, 10) potencialmente relevantes à migração — as 3 primeiras (#8, 9, 10) testam exatamente o comportamento de **compartilhamento de componentes de biblioteca entre páginas** (`library.ts` / `resolveSegment`, documentado em GO-001 §2.3) e podem indicar um defeito real de sincronização/atualização de conteúdo compartilhado, não uma flakiness de teste — merece triagem antes de portar essa capacidade em GO-020/GO-027. **Não foi feita uma segunda rodada de confirmação** (o rerun completo leva ~16 min e um subconjunto correria risco de falhar por dependência de estado de specs anteriores, já que os testes são numerados e presumem execução em sequência) — registrar como próximo passo antes de tratar qualquer um destes como "defeito conhecido" fechado.

Achado de reprodutibilidade adicional: a primeira tentativa falhou 100% dos casos por `Executable doesn't exist at .../chromium_headless_shell-1234` — a versão de `@playwright/test` fixada em `deploy/playwright/package.json` (1.62.0) espera uma revisão de Chromium diferente da que já estava em cache neste ambiente (de outro uso da máquina, revisão 1243). `npx playwright install chromium` resolveu.

## 6. Consumo de recursos

Não foi instrumentada uma medição formal de CPU/memória (ex.: `/usr/bin/time -v` sobre o processo pai não contabiliza os processos filhos que `node --test --test-concurrency=4` cria, o que tornaria o número enganoso). Como proxy qualitativo: as 3 suítes (846 + 599 + 271 casos) rodaram sem OOM ou timeout inesperado em uma máquina de desenvolvimento comum, usando o container Postgres isolado sem limites de CPU/memória configurados. **Lacuna registrada:** uma medição confiável de consumo por processo exigiria instrumentar `run-tests`/CI com `cgroups` ou uma ferramenta de profiling dedicada — não foi feito nesta tarefa; próximo passo explícito, não número inventado.

## 7. Limites provisórios (hipótese, não SLO comprovado)

Conforme README §5 ("Ajustar esse limite em F0; não é desempenho comprovado"), os números abaixo são um ponto de partida a refinar, não um compromisso:

- **Casos críticos:** os 846 casos de `saltcorn-data` e 599 de `server` cobrem CRUD dinâmico, autorização, RLS, packs, triggers/workflow/scheduler e views — tratar como o corpus de regressão mínimo antes de qualquer corte de capacidade para Go. Os 271 casos Playwright cobrem os fluxos de builder/UI ponta-a-ponta listados em GO-001 §3 (piloto).
- **p95/p99 de latência:** sem ferramenta de carga instrumentada (§4), não há p95/p99 sob concorrência medido. Como proxy de latência de renderização single-threaded: `show_with_html` ~17,5 ms/render para uma List de 450 células — ordem de grandeza a validar sob carga real em GO-016 (projeções) e no piloto (F3).
- **RPO/RTO:** não medido nesta tarefa (exige cenário de restore/backup real, fora do escopo de "rodar as suítes existentes"); README já trata isso como pendente de definição em F0 junto à operação.
- **Defeitos conhecidos vs. compatibilidade desejada:** nenhum defeito confirmado nas suítes `data`/`server` (0 falhas reais, apenas timezone de ambiente). Playwright tem 4 causas-raiz a triar (ver §5) antes de classificá-las.

## 8. Lacunas e hipóteses (rotina de validação)

- Medição de p95/p99 sob carga HTTP concorrente: não instrumentada (nenhuma ferramenta de load test no repo) — próximo passo explícito, possivelmente introduzido apenas quando houver um ambiente de piloto real.
- Consumo de CPU/memória por processo: não medido com precisão (limitação de `/usr/bin/time` com processos filhos concorrentes) — próximo passo explícito.
- RPO/RTO: não exercitado (exigiria cenário de backup/restore real).
- 4 causas-raiz de falha no Playwright (`TC_08`, `TC_43` ×3) carecem de uma segunda execução isolada para confirmar se são defeito consistente ou específico da ordem/estado desta rodada — não tratadas como "defeito conhecido" fechado nesta entrega.
- Dataset do piloto (`guitars`) não foi exercitado nesta rodada — as suítes atuais não o referenciam; falta uma integração dedicada quando houver ambiente de piloto (F2/F3).
