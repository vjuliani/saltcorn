# Backlog de migração para Go

Relaciona-se ao [plano de arquitetura](README.md). Stack definida: **frontend React + SB Admin 2; BFF Node.js; backend Go com CQRS lógico**. O BFF é permanente; o backend legado e o host de compatibilidade são temporários.

São tarefas locais para refinamento e execução; nenhuma issue foi publicada em sistema externo.

Todas começam em **TODO**. IDs são estáveis; dependências são IDs de tarefas. P0 = necessária para o escopo final proposto; P1 = otimização ou recurso a confirmar no inventário (torna-se bloqueador se usado por aplicação migrada). Tamanho relativo: M = médio; L = grande e deve ser dividido em subtarefas ao iniciar. Responsável indica papel sugerido, não pessoa atribuída. Não há estimativa em dias nem datas comprometidas.

Protótipos podem preceder dependências apenas em escopo isolado, registrado como exploração; não habilitam integração ou conclusão da tarefa dependente. Concluir requer artefato revisável, testes pertinentes e evidência dos critérios de aceite. Alterações de comportamento exigem atualização da matriz de compatibilidade.

## Sequência inicial

1. GO-001: inventário real e escolha do piloto.
2. GO-002, GO-003 e GO-004: baseline, decisões e risco de plugins.
3. GO-005 a GO-010: fundação, contratos, identidade e controle da transição.
4. GO-011 a GO-015 e GO-017 a GO-021: fluxo vertical de dados e editor.
5. Completar capacidades usadas pelo piloto e executar GO-034/GO-036; depois ampliar a paridade e as ondas.

O canário do piloto tem validação própria de carga e paridade; não exige mobile/SQLite quando ausentes do piloto. A conversão integral exige GO-033 e GO-035. GO-016 pode concluir com decisão fundamentada de adiar projeções.

## Validação, retomada e controle

Aplicar o [protocolo de execução](EXECUCAO.md) a cada tarefa. O CSV contém a linha atual de controle; o Markdown apresenta o backlog e suas rotinas. O histórico de tentativas e checkpoints fica em `execucoes/GO-NNN.md`, criado somente ao iniciar a tarefa usando o modelo do protocolo.

Fluxo: `TODO → IN_PROGRESS → VALIDATING → IN_REVIEW → DONE`. Interrupções usam `PAUSED` ou `BLOCKED`, com motivo e próximo passo. Somente evidência válida de todos os critérios, revisão e merge permitem `DONE`. A execução da implementação termina com PR aberto em `IN_REVIEW`. As caixas de seleção ficam marcadas apenas em `DONE`.

Todas as tarefas abaixo continuam pendentes. As rotinas são requisitos para execução futura, não verificações já realizadas na migração. Os campos de executor, execução, checkpoint e evidências permanecem vazios até existir trabalho real registrado.

## Como executar uma task

Cada task contém um **comando de execução para o agente**, em linguagem natural: copie o bloco e envie ao agente no checkout deste repositório. Não é um comando de shell nem uma promessa de execução automática. O comando inclui o escopo e a validação específicos da task. Os comandos Git/GitHub CLI estão no [protocolo](EXECUCAO.md#8-branch-commit-e-pull-request).

A branch prevista é `task/go-NNN`. Na primeira execução, descobrir e registrar a branch base do repositório; na retomada, reutilizar a branch e o PR existentes. Não executar as 38 tasks em lote: respeitar dependências integradas e o controle de concorrência.

## Tarefas

### GO-001 — Inventariar capacidades e aplicações

- [x] **Status:** DONE — integrada via PR [#1](https://github.com/vjuliani/saltcorn/pull/1) (merge `6557f7624104bb7d688b40a33f88ccbe2df5d153`); matriz em [inventario/GO-001-matriz-capacidades.md](inventario/GO-001-matriz-capacidades.md); histórico em [execucoes/GO-001.md](execucoes/GO-001.md)
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Arquitetura
- **Depende de:** Nenhuma
- **Branch:** `task/go-001`
- **Escopo:** Catalogar rotas, modelos, plugins por versão, autenticação, bancos, layouts, packs e mobile; escolher aplicações representativas.
- **Aceite:** Matriz liga cada capacidade a código, aplicação, fixture, destino Go/bridge e bloqueador; escopo piloto e escopo final registrados.
- **Rotina de validação:** Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses.
- **Regra de retomada:** Retomar do último artefato revisado; comparar o commit atual com o analisado e atualizar apenas as evidências afetadas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-001; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-001 — Inventariar capacidades e aplicações, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-001 a partir da base registrada. Escopo: Catalogar rotas, modelos, plugins por versão, autenticação, bancos, layouts, packs e mobile; escolher aplicações representativas. Valide: Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-002 — Criar baseline de comportamento e desempenho

- [x] **Status:** DONE — integrada via PR [#4](https://github.com/vjuliani/saltcorn/pull/4) (merge `5745f5225ac4b69ac41af89798c96866066e78aa`); baseline em [baseline/GO-002-baseline.md](baseline/GO-002-baseline.md); histórico em [execucoes/GO-002.md](execucoes/GO-002.md)
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** QA
- **Depende de:** GO-001
- **Branch:** `task/go-002`
- **Escopo:** Preparar datasets sanitizados e casos das suítes data/server/Playwright; medir latência, erros, consumo e jobs.
- **Aceite:** Execução reproduzível no commit de referência; limites p95/p99, RPO/RTO e casos críticos definidos; defeitos conhecidos separados da compatibilidade desejada.
- **Rotina de validação:** Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses.
- **Regra de retomada:** Retomar do último artefato revisado; comparar o commit atual com o analisado e atualizar apenas as evidências afetadas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-002; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-002 — Criar baseline de comportamento e desempenho, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-002 a partir da base registrada. Escopo: Preparar datasets sanitizados e casos das suítes data/server/Playwright; medir latência, erros, consumo e jobs. Valide: Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-003 — Registrar decisões de arquitetura

- [x] **Status:** DONE — integrada via PR [#5](https://github.com/vjuliani/saltcorn/pull/5) (merge `6f027146d0c9b4220488dd4a8205c945f085c3c6`); ADRs em [adr/](adr/README.md); histórico em [execucoes/GO-003.md](execucoes/GO-003.md)
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Arquitetura
- **Depende de:** GO-001
- **Branch:** `task/go-003`
- **Escopo:** Registrar ADRs de backend Go modular com CQRS lógico, frontend React + SB Admin 2, BFF Node.js separado, bancos e política de extensões.
- **Aceite:** Limites das três camadas, custos operacionais e estratégia SQLite/mobile explícitos; Node.js permanece no BFF e a retirada do legado de domínio tem critérios próprios.
- **Rotina de validação:** Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses.
- **Regra de retomada:** Retomar do último artefato revisado; comparar o commit atual com o analisado e atualizar apenas as evidências afetadas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-003; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-003 — Registrar decisões de arquitetura, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-003 a partir da base registrada. Escopo: Registrar ADRs de backend Go modular com CQRS lógico, frontend React + SB Admin 2, BFF Node.js separado, bancos e política de extensões. Valide: Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-004 — Prototipar compatibilidade de extensões e expressões

- [x] **Status:** DONE — integrada via PR [#7](https://github.com/vjuliani/saltcorn/pull/7) (merge `afd356d00d95f58c33f856987a9b49a8c6bdd3b2`); relatório em [prototipos/GO-004-relatorio-fronteira-rpc.md](prototipos/GO-004-relatorio-fronteira-rpc.md); histórico em [execucoes/GO-004.md](execucoes/GO-004.md)
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-001
- **Branch:** `task/go-004`
- **Escopo:** Executar amostra de fórmulas, callbacks, custom types e plugins com dependências de banco; experimentar fronteira RPC.
- **Aceite:** Relatório demonstra suportados e incompatíveis, sem supor serialização de funções; operações transacionais classificadas e custo da bridge medido.
- **Rotina de validação:** Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses.
- **Regra de retomada:** Retomar do último artefato revisado; comparar o commit atual com o analisado e atualizar apenas as evidências afetadas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-004; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-004 — Prototipar compatibilidade de extensões e expressões, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-004 a partir da base registrada. Escopo: Executar amostra de fórmulas, callbacks, custom types e plugins com dependências de banco; experimentar fronteira RPC. Valide: Conferir evidências no commit analisado, cobertura do inventário e rastreabilidade dos critérios; registrar lacunas e hipóteses. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-005 — Criar fundação Go e pipeline

- [x] **Status:** DONE — integrada via PR [#8](https://github.com/vjuliani/saltcorn/pull/8) (merge `dffc38df342759bc4f00d064200b187141dfe5a5`); código em [migracao/backend/](../../migracao/backend/); histórico em [execucoes/GO-005.md](execucoes/GO-005.md)
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-003
- **Branch:** `task/go-005`
- **Escopo:** Criar módulo, server/worker/CLI, configuração, shutdown, health/readiness e CI; fixar toolchain e dependências.
- **Aceite:** Build reproduzível, go test e race detector nos módulos concorrentes passam; processo encerra sem perder transações em curso.
- **Rotina de validação:** Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis.
- **Regra de retomada:** Conferir toolchains, configuração e versões de contratos; repetir checks afetados por mudanças desde o checkpoint antes de integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-005; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-005 — Criar fundação Go e pipeline, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-005 a partir da base registrada. Escopo: Criar módulo, server/worker/CLI, configuração, shutdown, health/readiness e CI; fixar toolchain e dependências. Valide: Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-006 — Definir contratos e fixtures HTTP

- [x] **Status:** DONE — integrada via PR [#9](https://github.com/vjuliani/saltcorn/pull/9) (merge `ee9fccaaa1e6dbcc942060b0a709dbc8f8d6b325`); contratos em [migracao/contracts/](../../migracao/contracts/); histórico em [execucoes/GO-006.md](execucoes/GO-006.md)
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-001, GO-003
- **Branch:** `task/go-006`
- **Escopo:** Versionar contratos React–BFF Node.js e BFF–Go em OpenAPI: DTOs, erros, paginação, identidade delegada, IDs, datas, decimais, null e layouts; mapear APIs legadas.
- **Aceite:** Clientes Go/TS validam ambos os contratos; CI detecta incompatibilidade; autenticação entre serviços e delegação de ator/tenant têm exemplos positivos e negativos.
- **Rotina de validação:** Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis.
- **Regra de retomada:** Conferir toolchains, configuração e versões de contratos; repetir checks afetados por mudanças desde o checkpoint antes de integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-006; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-006 — Definir contratos e fixtures HTTP, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-006 a partir da base registrada. Escopo: Versionar contratos React–BFF Node.js e BFF–Go em OpenAPI: DTOs, erros, paginação, identidade delegada, IDs, datas, decimais, null e layouts; mapear APIs legadas. Valide: Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-007 — Implementar tenancy e contexto transacional

- [x] **Status:** DONE — integrada via PR [#10](https://github.com/vjuliani/saltcorn/pull/10) (merge `9b240d31a697f9c6ac13d9bc63df5c89ad8fcd33`); código em [migracao/backend/internal/platform/{tenancy,database}](../../migracao/backend/internal/platform/); histórico em [execucoes/GO-007.md](execucoes/GO-007.md)
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-005
- **Branch:** `task/go-007`
- **Escopo:** Resolver tenant por mapeamento confiável; propagar ator, tenant e transação em context.Context, SQL e jobs.
- **Aceite:** Testes concorrentes com dois tenants, reuso de conexão, cancelamento e rollback não vazam schema, usuário ou dados.
- **Rotina de validação:** Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis.
- **Regra de retomada:** Conferir toolchains, configuração e versões de contratos; repetir checks afetados por mudanças desde o checkpoint antes de integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-007; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-007 — Implementar tenancy e contexto transacional, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-007 a partir da base registrada. Escopo: Resolver tenant por mapeamento confiável; propagar ator, tenant e transação em context.Context, SQL e jobs. Valide: Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-008 — Migrar identidade e autorização

- [x] **Status:** DONE — PR [#11](https://github.com/vjuliani/saltcorn/pull/11) mergeado; histórico em [execucoes/GO-008.md](execucoes/GO-008.md)
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-006, GO-007
- **Branch:** `task/go-008`
- **Escopo:** Implementar identidade, hashes, roles, ownership, RLS, tokens e MFA no backend Go; definir sessão/cookies no BFF e identidade delegada verificável entre serviços.
- **Aceite:** Matriz positiva/negativa cobre APIs, arquivos, sessão, revogação e acesso cruzado; estratégias de plugins sem suporte bloqueiam o corte.
- **Rotina de validação:** Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis.
- **Regra de retomada:** Conferir toolchains, configuração e versões de contratos; repetir checks afetados por mudanças desde o checkpoint antes de integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-008; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-008 — Migrar identidade e autorização, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-008 a partir da base registrada. Escopo: Implementar identidade, hashes, roles, ownership, RLS, tokens e MFA no backend Go; definir sessão/cookies no BFF e identidade delegada verificável entre serviços. Valide: Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-009 — Criar entrada de migração e ownership de escrita

- [x] **Status:** DONE — PR [#12](https://github.com/vjuliani/saltcorn/pull/12) mergeado; histórico em [execucoes/GO-009.md](execucoes/GO-009.md)
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend + Plataforma
- **Depende de:** GO-006, GO-008
- **Branch:** `task/go-009`
- **Escopo:** Rotear tenant/capacidade entre backend legado e Go, mantendo BFF Node.js como camada permanente; registrar proprietário e bloquear caminhos alternativos, inclusive jobs.
- **Aceite:** Teste comprova escritor único e rollback de rota; identidade não pode ser forjada por headers; requisições em andamento são drenadas.
- **Rotina de validação:** Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis.
- **Regra de retomada:** Conferir toolchains, configuração e versões de contratos; repetir checks afetados por mudanças desde o checkpoint antes de integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-009; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-009 — Criar entrada de migração e ownership de escrita, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-009 a partir da base registrada. Escopo: Rotear tenant/capacidade entre backend legado e Go, mantendo BFF Node.js como camada permanente; registrar proprietário e bloquear caminhos alternativos, inclusive jobs. Valide: Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-010 — Instrumentar observabilidade

- [x] **Status:** DONE — PR [#13](https://github.com/vjuliani/saltcorn/pull/13) mergeado; histórico em [execucoes/GO-010.md](execucoes/GO-010.md)
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-005
- **Branch:** `task/go-010`
- **Escopo:** Adicionar logs estruturados, métricas e tracing HTTP/SQL/jobs/RPC com correlação e controle de cardinalidade.
- **Aceite:** Uma operação é rastreável entre runtimes sem registrar tokens ou dados sensíveis; dashboards distinguem falhas e saturação.
- **Rotina de validação:** Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis.
- **Regra de retomada:** Conferir toolchains, configuração e versões de contratos; repetir checks afetados por mudanças desde o checkpoint antes de integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-010; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-010 — Instrumentar observabilidade, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-010 a partir da base registrada. Escopo: Adicionar logs estruturados, métricas e tracing HTTP/SQL/jobs/RPC com correlação e controle de cardinalidade. Valide: Executar build e checks dos componentes alterados; validar contratos e cenários negativos de identidade/tenant, cancelamento e falhas de integração quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-011 — Portar catálogo e evolução de schema

- [x] **Status:** DONE — PR [#14](https://github.com/vjuliani/saltcorn/pull/14) mergeado; histórico em [execucoes/GO-011.md](execucoes/GO-011.md)
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-007, GO-008
- **Branch:** `task/go-011`
- **Escopo:** Portar tabelas/campos/relações/constraints e migrations PG com locks, versão de metadados e invalidação de cache.
- **Aceite:** Criar/alterar schema mantém catálogo e DDL consistentes; falha intermediária é recuperável; nomes maliciosos e concorrência são cobertos.
- **Rotina de validação:** Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados.
- **Regra de retomada:** Conferir versão de schema, proprietário da escrita e estado da transação/outbox; reconciliar operações pendentes antes de repetir mutações.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-011; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-011 — Portar catálogo e evolução de schema, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-011 a partir da base registrada. Escopo: Portar tabelas/campos/relações/constraints e migrations PG com locks, versão de metadados e invalidação de cache. Valide: Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-012 — Implementar compilador de consultas dinâmicas

- [x] **Status:** DONE — PR [#15](https://github.com/vjuliani/saltcorn/pull/15) mergeado; histórico em [execucoes/GO-012.md](execucoes/GO-012.md)
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011
- **Branch:** `task/go-012`
- **Escopo:** Portar filtros, joins, agregações, ordenação e paginação; parametrizar valores e resolver identificadores pelo catálogo.
- **Aceite:** Corpus compara resultados, tipos, NULL, datas, chaves compostas e permissões com legado; consultas inválidas não permitem injeção SQL.
- **Rotina de validação:** Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados.
- **Regra de retomada:** Conferir versão de schema, proprietário da escrita e estado da transação/outbox; reconciliar operações pendentes antes de repetir mutações.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-012; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-012 — Implementar compilador de consultas dinâmicas, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-012 a partir da base registrada. Escopo: Portar filtros, joins, agregações, ordenação e paginação; parametrizar valores e resolver identificadores pelo catálogo. Valide: Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-013 — Implementar comandos de registros

- [x] **Status:** DONE — PR [#16](https://github.com/vjuliani/saltcorn/pull/16) mergeado; histórico em [execucoes/GO-013.md](execucoes/GO-013.md)
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011, GO-012
- **Branch:** `task/go-013`
- **Escopo:** Portar insert/update/delete, validações e transações; definir pontos de extensão de triggers e controle de versão.
- **Aceite:** Fixtures isoladas mostram equivalência de dados/erros; conflito concorrente retorna erro definido e falha desfaz toda a operação.
- **Rotina de validação:** Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados.
- **Regra de retomada:** Conferir versão de schema, proprietário da escrita e estado da transação/outbox; reconciliar operações pendentes antes de repetir mutações.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-013; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-013 — Implementar comandos de registros, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-013 a partir da base registrada. Escopo: Portar insert/update/delete, validações e transações; definir pontos de extensão de triggers e controle de versão. Valide: Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-014 — Adicionar idempotência e outbox

- [x] **Status:** DONE — PR [#17](https://github.com/vjuliani/saltcorn/pull/17) mergeado; histórico em [execucoes/GO-014.md](execucoes/GO-014.md)
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-013
- **Branch:** `task/go-014`
- **Escopo:** Persistir chave/payload/resultado e eventos junto à escrita; worker com retries, deduplicação e falhas inspecionáveis.
- **Aceite:** Crash antes e depois do commit não perde evento confirmado; redelivery não repete efeito interno; mesma chave com payload diferente é rejeitada.
- **Rotina de validação:** Simular crash antes/depois do commit e confirmação perdida; verificar deduplicação e rejeição de mesma chave com payload distinto.
- **Regra de retomada:** Conferir versão de schema, proprietário da escrita e estado da transação/outbox; reconciliar operações pendentes antes de repetir mutações.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-014; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-014 — Adicionar idempotência e outbox, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-014 a partir da base registrada. Escopo: Persistir chave/payload/resultado e eventos junto à escrita; worker com retries, deduplicação e falhas inspecionáveis. Valide: Simular crash antes/depois do commit e confirmação perdida; verificar deduplicação e rejeição de mesma chave com payload distinto. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-015 — Integrar políticas em todos os caminhos de leitura

- [x] **Status:** DONE — PR [#18](https://github.com/vjuliani/saltcorn/pull/18) mergeado; histórico em [execucoes/GO-015.md](execucoes/GO-015.md)
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend + QA
- **Depende de:** GO-008, GO-012
- **Branch:** `task/go-015`
- **Escopo:** Aplicar autorização a busca, exportação, contagem, agregações e caches; usar banco primário após escrita.
- **Aceite:** Revogação de acesso é imediata nos caminhos protegidos; contagens e agregados não revelam registros proibidos.
- **Rotina de validação:** Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados.
- **Regra de retomada:** Conferir versão de schema, proprietário da escrita e estado da transação/outbox; reconciliar operações pendentes antes de repetir mutações.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-015; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-015 — Integrar políticas em todos os caminhos de leitura, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-015 a partir da base registrada. Escopo: Aplicar autorização a busca, exportação, contagem, agregações e caches; usar banco primário após escrita. Valide: Executar fixtures de domínio em banco isolado, paridade com o legado, autorização, rollback, concorrência e idempotência dos caminhos alterados. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-016 — Medir necessidade de projeções CQRS

- [x] **Status:** DONE — PR [#19](https://github.com/vjuliani/saltcorn/pull/19) mergeado; histórico em [execucoes/GO-016.md](execucoes/GO-016.md)
- **Fase:** F2 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend
- **Depende de:** GO-002, GO-010, GO-014, GO-015
- **Branch:** `task/go-016`
- **Escopo:** Comparar queries diretas e projeção de uma consulta custosa; definir watermark, reconstrução e fallback.
- **Aceite:** ADR decide adotar ou adiar com benchmark; se adotada, rebuild, atraso, ordem e mudança de schema são testados antes do uso.
- **Rotina de validação:** Reproduzir benchmark com dataset e ambiente identificados; validar rebuild/watermark se a projeção for adotada ou registrar decisão fundamentada de adiar.
- **Regra de retomada:** Conferir versão de schema, proprietário da escrita e estado da transação/outbox; reconciliar operações pendentes antes de repetir mutações.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-016; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-016 — Medir necessidade de projeções CQRS, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-016 a partir da base registrada. Escopo: Comparar queries diretas e projeção de uma consulta custosa; definir watermark, reconstrução e fallback. Valide: Reproduzir benchmark com dataset e ambiente identificados; validar rebuild/watermark se a projeção for adotada ou registrar decisão fundamentada de adiar. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-017 — Implementar BFF web em Node.js

- [x] **Status:** DONE — PR [#20](https://github.com/vjuliani/saltcorn/pull/20) mergeado; histórico em [execucoes/GO-017.md](execucoes/GO-017.md)
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** BFF / Node.js
- **Depende de:** GO-006, GO-008, GO-012, GO-013
- **Branch:** `task/go-017`
- **Escopo:** Criar migracao/packages/bff em Node.js/TypeScript com build, CI, health e shutdown; compor bootstrap, metadados e dados via cliente HTTP tipado Go; implementar sessão/CSRF, autenticação entre serviços, timeouts, cancelamento e idempotência.
- **Aceite:** Contratos React–BFF–Go passam integração; BFF não acessa banco de domínio nem executa suas regras; identidade forjada é rejeitada; falhas/timeouts Go geram erros controlados e retries não duplicam comandos.
- **Rotina de validação:** Validar autenticação entre serviços, identidade delegada, CSRF e falhas BFF–Go; provar que retry de comando não duplica escrita e que o BFF não acessa o banco de domínio.
- **Regra de retomada:** Conferir versões do frontend, BFF, Go e layouts; preservar alterações locais e retomar com fixture identificada; repetir o fluxo afetado após integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-017; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-017 — Implementar BFF web em Node.js, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-017 a partir da base registrada. Escopo: Criar migracao/packages/bff em Node.js/TypeScript com build, CI, health e shutdown; compor bootstrap, metadados e dados via cliente HTTP tipado Go; implementar sessão/CSRF, autenticação entre serviços, timeouts, cancelamento e idempotência. Valide: Validar autenticação entre serviços, identidade delegada, CSRF e falhas BFF–Go; provar que retry de comando não duplica escrita e que o BFF não acessa o banco de domínio. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-018 — Integrar React + SB Admin 2 e desacoplar builder

- [x] **Status:** DONE — PR [#21](https://github.com/vjuliani/saltcorn/pull/21) mergeado; histórico em [execucoes/GO-018.md](execucoes/GO-018.md)
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Frontend
- **Depende de:** GO-006
- **Branch:** `task/go-018`
- **Escopo:** Criar shell React + SB Admin 2 reaproveitando assets e tema existentes; adaptar templates para componentes e isolar widgets imperativos; conectar React/Craft.js ao cliente tipado do BFF e versionar layouts.
- **Aceite:** Sidebar, topbar e navegação funcionam sem disputa de DOM entre React e scripts legados; builder funciona com mocks; round-trip preserva propriedades, referências e extensões.
- **Rotina de validação:** Validar shell React + SB Admin 2, ausência de disputa de DOM, mocks do BFF e round-trip de documentos do builder.
- **Regra de retomada:** Conferir versões do frontend, BFF, Go e layouts; preservar alterações locais e retomar com fixture identificada; repetir o fluxo afetado após integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-018; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-018 — Integrar React + SB Admin 2 e desacoplar builder, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-018 a partir da base registrada. Escopo: Criar shell React + SB Admin 2 reaproveitando assets e tema existentes; adaptar templates para componentes e isolar widgets imperativos; conectar React/Craft.js ao cliente tipado do BFF e versionar layouts. Valide: Validar shell React + SB Admin 2, ausência de disputa de DOM, mocks do BFF e round-trip de documentos do builder. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-019 — Integrar editor ao BFF

- [x] **Status:** DONE — integrada via PR [#22](https://github.com/vjuliani/saltcorn/pull/22) (merge `8f268ba369b416c8adf138b3d8270bd60588754e`); histórico em [execucoes/GO-019.md](execucoes/GO-019.md)
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Frontend
- **Depende de:** GO-017, GO-018
- **Branch:** `task/go-019`
- **Escopo:** Conectar tabelas, campos, views, preview e publicação; tratar erro de validação e edição concorrente.
- **Aceite:** E2E cria tabela e view, salva, reabre e publica com dois papéis; conflito de edição é apresentado sem sobrescrever silenciosamente.
- **Rotina de validação:** Executar build dos componentes alterados, contratos React–BFF–Go e E2E do fluxo afetado; conferir navegação, acessibilidade e paridade visual quando aplicáveis.
- **Regra de retomada:** Conferir versões do frontend, BFF, Go e layouts; preservar alterações locais e retomar com fixture identificada; repetir o fluxo afetado após integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-019; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-019 — Integrar editor ao BFF, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-019 a partir da base registrada. Escopo: Conectar tabelas, campos, views, preview e publicação; tratar erro de validação e edição concorrente. Valide: Executar build dos componentes alterados, contratos React–BFF–Go e E2E do fluxo afetado; conferir navegação, acessibilidade e paridade visual quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-020 — Portar runtime de views e páginas

- [x] **Status:** DONE — integrada via PR [#23](https://github.com/vjuliani/saltcorn/pull/23) (merge `dc4c555ddfaee6330e8e45a0a77b065031b68444`); histórico em [execucoes/GO-020.md](execucoes/GO-020.md)
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-012, GO-013, GO-018, GO-019 (corrigido nesta entrega — o escopo real depende de `internal/views`, GO-019; lacuna do backlog original, ver [execucoes/GO-020.md](execucoes/GO-020.md) nota 1)
- **Branch:** `task/go-020`
- **Escopo:** Separar metadados e regras de views no Go, composição no BFF Node.js e renderização React; aplicar SB Admin 2 à interface administrativa e preservar temas das aplicações, URLs, forms e widgets legados.
- **Aceite:** Aplicações fixture renderizam e operam com paridade funcional/visual; layouts incompatíveis bloqueiam publicação ou seguem rota legada explícita.
- **Rotina de validação:** Executar build dos componentes alterados, contratos React–BFF–Go e E2E do fluxo afetado; conferir navegação, acessibilidade e paridade visual quando aplicáveis.
- **Regra de retomada:** Conferir versões do frontend, BFF, Go e layouts; preservar alterações locais e retomar com fixture identificada; repetir o fluxo afetado após integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-020; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-020 — Portar runtime de views e páginas, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-020 a partir da base registrada. Escopo: Separar metadados e regras de views no Go, composição no BFF Node.js e renderização React; aplicar SB Admin 2 à interface administrativa e preservar temas das aplicações, URLs, forms e widgets legados. Valide: Executar build dos componentes alterados, contratos React–BFF–Go e E2E do fluxo afetado; conferir navegação, acessibilidade e paridade visual quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-021 — Validar experiência e compatibilidade web

- [x] **Status:** DONE — integrada via PR [#24](https://github.com/vjuliani/saltcorn/pull/24) (merge `45020830524ecc52c9b1d3297711298f327d779c`); histórico em [execucoes/GO-021.md](execucoes/GO-021.md)
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** QA + Frontend
- **Depende de:** GO-019, GO-020
- **Branch:** `task/go-021`
- **Escopo:** Cobrir React + SB Admin 2, i18n, responsividade, teclado, temas das aplicações, assets, sanitização HTML e componentes customizados do piloto.
- **Aceite:** Fluxo criar/publicar/operar passa E2E nos navegadores acordados; nenhum script não autorizado executa em cenários de teste.
- **Rotina de validação:** Executar build dos componentes alterados, contratos React–BFF–Go e E2E do fluxo afetado; conferir navegação, acessibilidade e paridade visual quando aplicáveis.
- **Regra de retomada:** Conferir versões do frontend, BFF, Go e layouts; preservar alterações locais e retomar com fixture identificada; repetir o fluxo afetado após integrar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-021; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-021 — Validar experiência e compatibilidade web, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-021 a partir da base registrada. Escopo: Cobrir React + SB Admin 2, i18n, responsividade, teclado, temas das aplicações, assets, sanitização HTML e componentes customizados do piloto. Valide: Executar build dos componentes alterados, contratos React–BFF–Go e E2E do fluxo afetado; conferir navegação, acessibilidade e paridade visual quando aplicáveis. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-022 — Implementar host temporário de extensões JS

- [x] **Status:** DONE — integrada via PR [#25](https://github.com/vjuliani/saltcorn/pull/25) (merge `2a125c1a9b3527d5d0661f5bb55f460c56477950`); histórico em [execucoes/GO-022.md](execucoes/GO-022.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Plataforma
- **Depende de:** GO-004, GO-008, GO-009
- **Branch:** `task/go-022`
- **Escopo:** Criar host JS temporário separado do BFF Node.js, com RPC versionado, capacidades explícitas, isolamento OS/processo, limites e protocolo de erros; evitar credenciais amplas.
- **Aceite:** Timeout/crash e acesso proibido são contidos; plugin transacional incompatível mantém operação integral no legado; matriz de plugins atualizada.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-022; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-022 — Implementar host temporário de extensões JS, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-022 a partir da base registrada. Escopo: Criar host JS temporário separado do BFF Node.js, com RPC versionado, capacidades explícitas, isolamento OS/processo, limites e protocolo de erros; evitar credenciais amplas. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-023 — Portar tipos e expressões prioritários

- [x] **Status:** DONE — integrada via PR [#26](https://github.com/vjuliani/saltcorn/pull/26) (merge `a3372755ca858ad3e8da5c74f9c1eceee39e2f7d`); histórico em [execucoes/GO-023.md](execucoes/GO-023.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-004, GO-011, GO-022
- **Branch:** `task/go-023`
- **Escopo:** Portar tipos básicos e subconjunto de expressões do piloto; fallback explícito quando semântica divergir.
- **Aceite:** Corpus cobre coerção, null, datas, decimal, erros e async; expressão desconhecida nunca muda resultado silenciosamente.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-023; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-023 — Portar tipos e expressões prioritários, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-023 a partir da base registrada. Escopo: Portar tipos básicos e subconjunto de expressões do piloto; fallback explícito quando semântica divergir. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-024 — Portar triggers, ações e workflows

- [x] **Status:** DONE — integrada via PR [#27](https://github.com/vjuliani/saltcorn/pull/27) (merge `5bc11c72fa100ad134a7881f6997e60b94de2b5f`); histórico em [execucoes/GO-024.md](execucoes/GO-024.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-013, GO-014, GO-023
- **Branch:** `task/go-024`
- **Escopo:** Separar hooks transacionais e efeitos após commit; portar estado de workflow, condições e rastreamento.
- **Aceite:** Ordem e rollback equivalem às fixtures; falhas/repetições não duplicam efeitos internos; workflows interrompidos retomam de forma documentada.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-024; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-024 — Portar triggers, ações e workflows, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-024 a partir da base registrada. Escopo: Separar hooks transacionais e efeitos após commit; portar estado de workflow, condições e rastreamento. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-025 — Portar scheduler e coordenação de workers

- [x] **Status:** DONE — integrada via PR [#28](https://github.com/vjuliani/saltcorn/pull/28) (merge `77f512e11c539d18dd58ba6f3b55242468cc8126`); histórico em [execucoes/GO-025.md](execucoes/GO-025.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend
- **Depende de:** GO-014, GO-024
- **Branch:** `task/go-025`
- **Escopo:** Implementar agendamento, timezone, leases/locks, retomada e drenagem ao transferir ownership.
- **Aceite:** Dois workers não executam simultaneamente job exclusivo; reinício e expiração de lease têm comportamento testado; scheduler antigo é desativado por escopo.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-025; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-025 — Portar scheduler e coordenação de workers, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-025 a partir da base registrada. Escopo: Implementar agendamento, timezone, leases/locks, retomada e drenagem ao transferir ownership. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-026 — Portar arquivos e notificações

- [x] **Status:** DONE — integrada via PR [#29](https://github.com/vjuliani/saltcorn/pull/29) (merge `2e178aee7d157799a82a833b26d6a0b688dff9bd`); histórico em [execucoes/GO-026.md](execucoes/GO-026.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-008, GO-014
- **Branch:** `task/go-026`
- **Escopo:** Portar upload/download, armazenamento local/S3 conforme escopo, autorização, e-mail, webhooks e notificações.
- **Aceite:** Usuário sem acesso não baixa arquivo; uploads interrompidos são limpos; falha do provedor entra em retry e duplicatas externas têm política explícita.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-026; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-026 — Portar arquivos e notificações, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-026 a partir da base registrada. Escopo: Portar upload/download, armazenamento local/S3 conforme escopo, autorização, e-mail, webhooks e notificações. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-027 — Portar configuração, packs e biblioteca

- [x] **Status:** DONE — integrada via PR [#30](https://github.com/vjuliani/saltcorn/pull/30) (merge `514dfb4de27c14c72d77df925810d274bf0aa4a2`); histórico em [execucoes/GO-027.md](execucoes/GO-027.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011, GO-020, GO-023
- **Branch:** `task/go-027`
- **Escopo:** Migrar import/export de aplicações, dependências, assets, configurações e referências entre entidades.
- **Aceite:** Export legado importa no Go e reexporta sem perda no corpus; faltas de plugin/versão são reportadas antes de aplicar alterações.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-027; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-027 — Portar configuração, packs e biblioteca, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-027 a partir da base registrada. Escopo: Migrar import/export de aplicações, dependências, assets, configurações e referências entre entidades. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-028 — Migrar comunicação em tempo real

- [x] **Status:** DONE — integrada via PR [#31](https://github.com/vjuliani/saltcorn/pull/31) (merge `6c8ea05e353d580c99dbeb074bf35389df6013fb`); histórico em [execucoes/GO-028.md](execucoes/GO-028.md)
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-017, GO-024
- **Branch:** `task/go-028`
- **Escopo:** Inventariar eventos Socket.IO; manter protocolo via adapter ou migrar cliente e servidor de forma coordenada.
- **Aceite:** Reconexão, sessão expirada, ordenação e isolamento por tenant passam testes; não tratar WebSocket puro como substituto compatível de Socket.IO.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-028; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-028 — Migrar comunicação em tempo real, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-028 a partir da base registrada. Escopo: Inventariar eventos Socket.IO; manter protocolo via adapter ou migrar cliente e servidor de forma coordenada. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-029 — Definir SDK e portar plugins exigidos

- [x] **Status:** DONE — integrada via PR [#32](https://github.com/vjuliani/saltcorn/pull/32) (merge `40a3cb5dbb264b27bd5de424de950e95a9ed3b48`); histórico em [execucoes/GO-029.md](execucoes/GO-029.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-022, GO-023, GO-024, GO-027
- **Branch:** `task/go-029`
- **Escopo:** Definir contratos de tipos/views/ações/auth; portar plugins requeridos por aplicação e documentar diferenças.
- **Aceite:** Cada plugin de domínio necessário tem versão Go testada ou substituição funcional; dependências do host temporário bloqueiam sua retirada; plugins de apresentação seguem os contratos React/BFF e não acessam persistência.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-029; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-029 — Definir SDK e portar plugins exigidos, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-029 a partir da base registrada. Escopo: Definir contratos de tipos/views/ações/auth; portar plugins requeridos por aplicação e documentar diferenças. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-030 — Implementar adapter SQLite

- [x] **Status:** DONE — PR [#33](https://github.com/vjuliani/saltcorn/pull/33) integrado; histórico em [execucoes/GO-030.md](execucoes/GO-030.md)
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011, GO-012, GO-013, GO-014
- **Branch:** `task/go-030`
- **Escopo:** Cobrir DDL, tipos, transações, locking e outbox no SQLite; distinguir tenancy disponível e modo desktop.
- **Aceite:** Mesmas fixtures de domínio passam PG/SQLite com divergências justificadas; concorrência e recuperação são verificadas sem sintaxe exclusiva de PG.
- **Rotina de validação:** Executar fixtures nos dois adapters; testar locking/rollback SQLite sem depender de sintaxe PG.
- **Regra de retomada:** Identificar versões e checkpoints de banco, cliente e artefatos; retomar a partir de estado consistente comprovado, sem apagar dados locais pendentes.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-030; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-030 — Implementar adapter SQLite, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-030 a partir da base registrada. Escopo: Cobrir DDL, tipos, transações, locking e outbox no SQLite; distinguir tenancy disponível e modo desktop. Valide: Executar fixtures nos dois adapters; testar locking/rollback SQLite sem depender de sintaxe PG. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-031 — Migrar contratos de sync e mobile offline

- [x] **Status:** DONE — histórico em [execucoes/GO-031.md](execucoes/GO-031.md)
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Mobile
- **Depende de:** GO-006, GO-008, GO-027, GO-030
- **Branch:** `task/go-031`
- **Escopo:** Versionar sync, conflitos, exclusões, migrações locais e retomada; preservar runtime mobile e mapear dependências JS.
- **Aceite:** Cliente fica offline, edita, reconecta e converge com conflitos explícitos; upgrade preserva dados locais e autorização; escopo Go no dispositivo definido.
- **Rotina de validação:** Testar interrupção offline/reconexão, conflito, exclusão e upgrade com dados locais pendentes; verificar convergência e autorização.
- **Regra de retomada:** Identificar versões e checkpoints de banco, cliente e artefatos; retomar a partir de estado consistente comprovado, sem apagar dados locais pendentes.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-031; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-031 — Migrar contratos de sync e mobile offline, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-031 a partir da base registrada. Escopo: Versionar sync, conflitos, exclusões, migrações locais e retomada; preservar runtime mobile e mapear dependências JS. Valide: Testar interrupção offline/reconexão, conflito, exclusão e upgrade com dados locais pendentes; verificar convergência e autorização. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-032 — Portar CLI e distribuição self-hosted

- [x] **Status:** DONE — histórico em [execucoes/GO-032.md](execucoes/GO-032.md)
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-005, GO-027, GO-030
- **Branch:** `task/go-032`
- **Escopo:** Portar setup, migrations, backup/restore, configuração e serve; empacotar backend Go, BFF Node.js e assets React + SB Admin 2 com health checks, versões compatíveis e documentação operacional.
- **Aceite:** Instalação limpa e upgrade de fixture funcionam; backup restaura dados, arquivos, configuração e versões de plugins; dependências runtime declaradas.
- **Rotina de validação:** Executar a matriz aplicável de PG/SQLite, web/mobile, versões de contratos e instalação/upgrade; conferir persistência e recuperação no perfil da tarefa.
- **Regra de retomada:** Identificar versões e checkpoints de banco, cliente e artefatos; retomar a partir de estado consistente comprovado, sem apagar dados locais pendentes.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-032; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-032 — Portar CLI e distribuição self-hosted, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-032 a partir da base registrada. Escopo: Portar setup, migrations, backup/restore, configuração e serve; empacotar backend Go, BFF Node.js e assets React + SB Admin 2 com health checks, versões compatíveis e documentação operacional. Valide: Executar a matriz aplicável de PG/SQLite, web/mobile, versões de contratos e instalação/upgrade; conferir persistência e recuperação no perfil da tarefa. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-033 — Executar matriz completa de paridade

- [x] **Status:** DONE — histórico em [execucoes/GO-033.md](execucoes/GO-033.md)
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** QA
- **Depende de:** GO-021, GO-025, GO-026, GO-027, GO-028, GO-029, GO-030, GO-031, GO-032
- **Branch:** `task/go-033`
- **Escopo:** Consolidar testes HTTP, domínio, UI, plugins, bancos e mobile; separar perfil piloto do perfil integral.
- **Aceite:** Todas as capacidades contratadas têm evidência e resultado; lacunas impedem declarar migração completa.
- **Rotina de validação:** Executar a matriz aplicável de PG/SQLite, web/mobile, versões de contratos e instalação/upgrade; conferir persistência e recuperação no perfil da tarefa.
- **Regra de retomada:** Identificar versões e checkpoints de banco, cliente e artefatos; retomar a partir de estado consistente comprovado, sem apagar dados locais pendentes.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-033; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-033 — Executar matriz completa de paridade, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-033 a partir da base registrada. Escopo: Consolidar testes HTTP, domínio, UI, plugins, bancos e mobile; separar perfil piloto do perfil integral. Valide: Executar a matriz aplicável de PG/SQLite, web/mobile, versões de contratos e instalação/upgrade; conferir persistência e recuperação no perfil da tarefa. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-034 — Ensaiar migração de dados e rollback

- [x] **Status:** DONE — PR [#38](https://github.com/vjuliani/saltcorn/pull/38); [runbook](recuperacao/RUNBOOK.md) e [histórico](execucoes/GO-034.md)
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Plataforma
- **Depende de:** GO-009, GO-027, GO-032
- **Branch:** `task/go-034`
- **Escopo:** Ensaiar expansão/contração, ownership, locks, reconciliação de IDs/contagens/checksums e recuperação em cópia sanitizada.
- **Aceite:** Runbook demonstra retorno seguro com escritas Go ou procedimento de pausa/reconciliação; RPO/RTO atendidos; nenhum DDL destrutivo durante janela de retorno.
- **Rotina de validação:** Ensaiar restauração e reconciliação após escritas Go; comprovar RPO/RTO e compatibilidade de schema antes de permitir retorno de tráfego.
- **Regra de retomada:** Verificar estado real de tráfego, escritor ativo, schema e jobs; reconciliar a onda interrompida antes de avançar ou executar rollback; não repetir corte às cegas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-034; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-034 — Ensaiar migração de dados e rollback, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-034 a partir da base registrada. Escopo: Ensaiar expansão/contração, ownership, locks, reconciliação de IDs/contagens/checksums e recuperação em cópia sanitizada. Valide: Ensaiar restauração e reconciliação após escritas Go; comprovar RPO/RTO e compatibilidade de schema antes de permitir retorno de tráfego. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-035 — Validar carga e falhas operacionais

- [x] **Status:** DONE — PR [#39](https://github.com/vjuliani/saltcorn/pull/39); [resultados](operacao/RESULTADOS.md) e [histórico](execucoes/GO-035.md)
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** QA + Plataforma
- **Depende de:** GO-002, GO-010, GO-014, GO-025, GO-033
- **Branch:** `task/go-035`
- **Escopo:** Executar carga realista ponta a ponta React/BFF/Go; injetar falhas de rede BFF–Go, DB, worker, host JS e armazenamento; medir latências, recursos e saturação de ambos os serviços.
- **Aceite:** SLOs acordados são atendidos; vazamento entre tenants, perda de eventos e saturação sem limites impedem promoção.
- **Rotina de validação:** Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar.
- **Regra de retomada:** Verificar estado real de tráfego, escritor ativo, schema e jobs; reconciliar a onda interrompida antes de avançar ou executar rollback; não repetir corte às cegas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-035; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-035 — Validar carga e falhas operacionais, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-035 a partir da base registrada. Escopo: Executar carga realista ponta a ponta React/BFF/Go; injetar falhas de rede BFF–Go, DB, worker, host JS e armazenamento; medir latências, recursos e saturação de ambos os serviços. Valide: Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-036 — Liberar canário do piloto

- [ ] **Status:** BLOCKED — PR preparatório [#40](https://github.com/vjuliani/saltcorn/pull/40); liberação impedida; [preflight](canario/RESULTADOS.md) e [histórico](execucoes/GO-036.md)
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma + QA
- **Depende de:** GO-021, GO-024, GO-025, GO-026, GO-027, GO-034
- **Branch:** `task/go-036`
- **Escopo:** Validar subconjunto da matriz, carga e dependências do piloto; rotear poucos tenants com observação e critérios de abortar.
- **Aceite:** Todos os recursos usados pelo piloto têm paridade; janela acordada sem incidentes críticos; rollback ensaiado e métricas comparadas ao baseline.
- **Rotina de validação:** Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar.
- **Regra de retomada:** Verificar estado real de tráfego, escritor ativo, schema e jobs; reconciliar a onda interrompida antes de avançar ou executar rollback; não repetir corte às cegas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-036; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-036 — Liberar canário do piloto, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-036 a partir da base registrada. Escopo: Validar subconjunto da matriz, carga e dependências do piloto; rotear poucos tenants com observação e critérios de abortar. Valide: Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-037 — Expandir migração por ondas

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-033, GO-034, GO-035, GO-036
- **Branch:** `task/go-037`
- **Escopo:** Migrar grupos compatíveis, reconciliar dados por onda e acompanhar acessos residuais ao backend legado, distinguindo-os do tráfego normal do BFF Node.js.
- **Aceite:** Cada onda possui checklist e evidências de SLO/recuperação; não há escritores legados ativos para capacidades migradas.
- **Rotina de validação:** Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar.
- **Regra de retomada:** Verificar estado real de tráfego, escritor ativo, schema e jobs; reconciliar a onda interrompida antes de avançar ou executar rollback; não repetir corte às cegas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-037; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-037 — Expandir migração por ondas, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-037 a partir da base registrada. Escopo: Migrar grupos compatíveis, reconciliar dados por onda e acompanhar acessos residuais ao backend legado, distinguindo-os do tráfego normal do BFF Node.js. Valide: Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-038 — Retirar legado e encerrar migração

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Arquitetura + Plataforma
- **Depende de:** GO-029, GO-037
- **Branch:** `task/go-038`
- **Escopo:** Remover rotas de domínio, jobs, credenciais e bridge do backend legado; manter BFF Node.js e frontend React + SB Admin 2; atualizar distribuição e runbooks dos serviços finais.
- **Aceite:** Janela acordada sem chamadas ao backend legado/host temporário; domínio e persistência no Go; frontend e BFF permanentes funcionam; instalação, upgrades compatíveis entre serviços e restore testados.
- **Rotina de validação:** Comprovar ausência de chamadas ao backend legado/host temporário na janela acordada; testar instalação e restore mantendo BFF Node.js e frontend React + SB Admin 2.
- **Regra de retomada:** Verificar estado real de tráfego, escritor ativo, schema e jobs; reconciliar a onda interrompida antes de avançar ou executar rollback; não repetir corte às cegas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-038; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-038 — Retirar legado e encerrar migração, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-038 a partir da base registrada. Escopo: Remover rotas de domínio, jobs, credenciais e bridge do backend legado; manter BFF Node.js e frontend React + SB Admin 2; atualizar distribuição e runbooks dos serviços finais. Valide: Comprovar ausência de chamadas ao backend legado/host temporário na janela acordada; testar instalação e restore mantendo BFF Node.js e frontend React + SB Admin 2. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

## Tarefas de fechamento de lacunas (pós-auditoria de 2026-09-21)

GO-036 permanece **BLOCKED** (ver [canario/RESULTADOS.md](canario/RESULTADOS.md) e [canario/RUNBOOK.md](canario/RUNBOOK.md)): a matriz de paridade da GO-033 ([paridade/RESULTADOS.md](paridade/RESULTADOS.md)) mostra 39 das 48 capacidades do piloto `guitars` como PARTIAL/BLOCKED, apesar de todas as tasks GO-001–GO-038 estarem DONE/mescladas. As 5 tasks abaixo fecham os bloqueios de ENGENHARIA identificados no runbook do canário — a retomada da GO-036 exige que estas estejam DONE, mais uma decisão operacional que nenhuma delas cobre: **alvo do canário (ambiente, dois tenants, janela, SLOs)**, que só o proprietário do piloto pode definir (não é uma tarefa de código, ver runbook).

### GO-039 — Portar viewtemplates Edit/Show/Feed e formulário do piloto guitars

- [x] **Status:** DONE — integrada via PR [#45](https://github.com/vjuliani/saltcorn/pull/45) (merge `010280ae81adda6f2ddb774c777a1cb0db4d51fd`); pendência isolada (bug em consumidores fora do pacote, achado de CI) corrigida via PR [#46](https://github.com/vjuliani/saltcorn/pull/46); histórico em [execucoes/GO-039.md](execucoes/GO-039.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-019, GO-020, GO-029
- **Branch:** `task/go-039`
- **Escopo:** Estender o pipeline classify/compile/render de `internal/views` (hoje só "List", `render.go:37`) e os componentes React correspondentes para os viewtemplates Edit, Show e Feed — os três templates que o pack piloto `guitars` usa além de List (9 views ao todo: 2 List, 5 Edit, 1 Feed, 1 Show, conforme GO-033/GO-036); incluir o formulário de escrita real (create/update) que Edit exige. **Emendado por auditoria de 2026-09-21 (CAP-073/074/085, ver GO-001-matriz-capacidades.md §6):** "formulário de escrita real" significa especificamente portar a ação `form_action` (`base-plugin/actions.ts` — o mecanismo real do botão "Salvar": `tryInsertRow`/`tryUpdateRow` + upload de arquivo) e a ação `navigate` (redirecionamento pós-ação) — sem as duas, Edit não salva nem redireciona, mesmo "portado" visualmente. Cobrir também, dentro dos templates Edit/Show/Feed, os nós de coluna que o pack `guitars` de fato usa além de `field` simples (verificar `join_field`/`view_link`/`action`/`aggregation` antes de declarar pronto — `internal/views/render.go` hoje rejeita qualquer `colType != "Field"`). **Emendado no preflight de execução (2026-09-21, extração real de `pack.json` do pack `guitars`):** o layout real de `create_guitar` embute uma view inteira (`layout` node `type: "view"`, `view: "edit_processed_embed"`, `relation: ".guitars.processed$guitar"`) — renderizar uma view DENTRO de outra, num relacionamento de linha, é uma capacidade recursiva nova (contexto de segurança/role próprio, resolução de relação 1:N) que não cabe com o rigor devido dentro desta task; e o template `Feed` (`guitar_feed`) depende exatamente do MESMO mecanismo (`show_view` renderizado por linha). Ambos ficam FORA de escopo aqui, portados em **GO-051**. `upload_photo` usa fieldview `upload`, que exige um tipo de campo `FieldFile` que `internal/metadata` (GO-011) ainda não tem — também fica fora, mesma task GO-051.
- **Aceite:** Das 9 views do pack `guitars`, as 6 que não dependem de view aninhada, Feed ou campo de arquivo (`guitar_list`, `list_processed`, `show_guitar`, `edit_guitar_process`, `edit_processed_embed` — como view standalone, `add_process_type`) renderizam e operam (leitura E escrita, incluindo salvar via `form_action` e redirecionar via `navigate`) através do runtime Go/React; `create_guitar` opera para seus próprios campos (sem o sub-formulário embutido, documentado como lacuna); `guitar_feed` e `upload_photo` ficam explicitamente BLOQUEADAS nesta entrega, com a task de continuação (GO-051) registrada — nunca em silêncio. GO-033 (CAP-034 "Viewtemplates nativos", CAP-040 "Formulários", CAP-073, CAP-074, CAP-085) reclassificado de PARTIAL/NÃO LISTADA para PASS PARCIAL para este pack, com a lacuna explícita; comportamento comparado ao legado e divergências documentadas.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-039; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-039 — Portar viewtemplates Edit/Show/Feed e formulário do piloto guitars, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-039 a partir da base registrada. Escopo: Estender o pipeline classify/compile/render de internal/views e os componentes React para os viewtemplates Edit, Show e Feed usados pelo pack piloto guitars (9 views: 2 List, 5 Edit, 1 Feed, 1 Show), incluindo formulário de escrita real. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-040 — Ligar automação (triggers/ações) ao caminho HTTP real

- [x] **Status:** DONE — integrada via PR [#47](https://github.com/vjuliani/saltcorn/pull/47) (merge `54417823caaefce73a96c95ed40d84231cc7de34`); histórico em [execucoes/GO-040.md](execucoes/GO-040.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-022, GO-024, GO-025, GO-029
- **Branch:** `task/go-040`
- **Escopo:** Ligar `internal/triggers.Dispatcher.HooksFor` em `cmd/server` (`createRecordHandler` chama `records.CreateRecord` com `hooks=nil` hoje; `updateRecordHandler`/`deleteRecordHandler` nem existem ainda — achado de preflight, GO-017 só entregou list/create) e decidir o ciclo de vida do processo host de plugins (`internal/pluginhost`, GO-022) dentro de `cmd/server`; portar a ação `run_js_code` (deliberadamente deferida em GO-029) roteando para `internal/pluginhost`; ligar o consumidor de outbox de triggers `AfterCommit` no `cmd/worker` (hoje só loga "sem consumidor real ainda", `enqueueAfterCommit` grava o evento mas nada o executa). **Emendado no preflight de execução (2026-09-21):** o trigger `receive_share_trigger` do pack `guitars` usa `when_trigger: "ReceiveMobileShareData"` — um evento NOMEADO genérico (`Trigger.emitEvent(eventname, channel, user, payload)` do legado, disparado por `POST /api/emit-event`), arquiteturalmente DESACOPLADO de qualquer escrita de registro — `internal/triggers.WhenTrigger` só cobre Validate/Insert/Update/Delete (fechado, atado aos hooks de `internal/records`). Além disso, o código JS real do trigger (`Table.findOne(...).insertRow(...)`) faz ESCRITA via singleton de domínio — `internal/pluginhost` (GO-022) deliberadamente só expõe leitura (`CapDBRead`) e nunca singletons como `Table` (decisão de escopo registrada em `docs/migracao-go/execucoes/GO-022.md`, nota 5: "escrita a partir de expressão/plugin... precisa de decisão própria de idempotência"). Fazer o trigger real do pack disparar de ponta a ponta exigiria DUAS capacidades novas e maiores (mecanismo de evento nomeado + capacidade de escrita no host) — fora de escopo aqui, registrado como **GO-052**. Esta task entrega o mecanismo real de disparo síncrono (hooks de CRUD) e assíncrono (outbox `AfterCommit`) para os triggers Insert/Update/Delete/Validate já suportados, incluindo `run_js_code` com o mesmo contrato de leitura já existente (`CapDBRead`).
- **Aceite:** Uma requisição HTTP real de criação/atualização/remoção de registro dispara triggers/ações síncronas (incluindo `run_js_code` via host de plugins, capacidade de leitura) na mesma transação; um trigger `AfterCommit` grava o evento de outbox e o worker o executa de fato (não só loga); GO-033 (CAP-047 "Triggers", CAP-048 "Ações multi-etapa") reclassificado de PARTIAL para PASS para os quatro `WhenTrigger` suportados. O trigger `receive_share_trigger` do pack `guitars` (evento nomeado `ReceiveMobileShareData` + escrita via `Table` no host) permanece expressamente fora de escopo, com GO-052 registrada — não é um silêncio, é uma decisão documentada.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-040; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-040 — Ligar automação (triggers/ações) ao caminho HTTP real, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-040 a partir da base registrada. Escopo: Ligar internal/triggers.Dispatcher.HooksFor em cmd/server (hoje hooks=nil) e decidir o ciclo de vida do processo host de plugins (internal/pluginhost) dentro de cmd/server, garantindo que o trigger run_js_code/ReceiveMobileShareData do pack guitars dispare a partir de escrita HTTP real. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-041 — Completar adapter SQLite (identidade, views, worker e caminho web)

- [~] **Status:** IN_REVIEW — PR aberto; escopo reduzido em relação ao Aceite original com decisão explícita do usuário registrada em [execucoes/GO-041.md](execucoes/GO-041.md) (triggers/scheduler/notify/files/config seguem `pgx.Tx`-only, deferidos para GO-055)
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-030, GO-008, GO-019, GO-020, GO-025
- **Branch:** `task/go-041`
- **Escopo:** Estender a fronteira `internal/platform/database.Tx` (GO-030 — já usada por `internal/records`/`internal/platform/outbox` desde a extensão pós-GO-030) para `internal/identity` e `internal/views`; ligar `internal/platform/sqlite` em `cmd/server` e `cmd/worker`, que hoje não têm nenhuma referência a esse pacote.
- **Aceite:** `cmd/server` e `cmd/worker` sobem e servem tráfego real contra um tenant em arquivo SQLite (identidade, views, registros CRUD funcionando, testado via HTTP/httptest); GO-033 (CAP-021 "Facade de banco", CAP-023 "Adapter SQLite") reclassificado de PARTIAL para **PASS PARCIAL** — triggers/scheduler não convertidos nesta entrega (ver [execucoes/GO-041.md](execucoes/GO-041.md) e GO-055).
- **Rotina de validação:** Executar as mesmas fixtures de domínio (identidade, views) nos dois adapters; testar concorrência/rollback SQLite sem depender de sintaxe exclusiva de PG; prova HTTP real (httptest) do caminho SQLite completo em `cmd/server`.
- **Regra de retomada:** Identificar versões e checkpoints de banco, cliente e artefatos; retomar a partir de estado consistente comprovado, sem apagar dados locais pendentes.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-041; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-041 — Completar adapter SQLite (identidade, views, worker e caminho web), de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-041 a partir da base registrada. Escopo: Estender internal/platform/database.Tx para internal/identity e internal/views; ligar internal/platform/sqlite em cmd/server e cmd/worker. Valide: Executar as mesmas fixtures de domínio nos dois adapters; testar concorrência/rollback SQLite sem depender de sintaxe exclusiva de PG. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-055 — Portar internal/triggers, internal/scheduler e internal/notify para database.Tx (completar adapter SQLite)

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P1 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-041
- **Branch:** `task/go-055`
- **Escopo:** Follow-up do escopo deferido em GO-041 (decisão explícita do usuário, ver [execucoes/GO-041.md](execucoes/GO-041.md)) — `internal/triggers` (inclui `Dispatcher.HooksFor`, que hoje produz um `*records.Hooks` fechado em `pgx.Tx`), `internal/scheduler` e `internal/notify` permanecem inteiramente `pgx.Tx`-only e não têm nenhum caminho contra o adapter SQLite (`internal/platform/sqlite`). Estender a fronteira `database.Tx` (GO-030) a esses três pacotes, preservando os wrappers `pgx.Tx` existentes (mesmo padrão `postgres.go` de GO-041/GO-030) para não quebrar nenhum chamador Postgres já testado. Avaliar também `internal/files`/`internal/config`, identificados na mesma auditoria como igualmente `pgx.Tx`-only, e decidir se entram nesta task ou em follow-up próprio.
- **Aceite:** `cmd/server` em modo SQLite dispara triggers reais (`Dispatcher.HooksFor`/`EmitEvent`) a partir de uma escrita HTTP de registro, sem `hooks=nil`; `cmd/worker` em modo SQLite executa pelo menos um job real (outbox/scheduler) contra um tenant em arquivo SQLite; GO-033 (CAP-021, CAP-023) reclassificado de PASS PARCIAL para PASS completo.
- **Rotina de validação:** Mesmas fixtures de domínio (triggers, scheduler, notify) nos dois adapters via `internal/platform/sqlite/parity_test.go`; testar concorrência/rollback SQLite sem depender de sintaxe exclusiva de PG; prova HTTP/job real (não só unitária) do disparo de trigger e da execução de job contra SQLite.
- **Regra de retomada:** Identificar versões e checkpoints de banco, cliente e artefatos; retomar a partir de estado consistente comprovado, sem apagar dados locais pendentes.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-055; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-055 — Portar internal/triggers, internal/scheduler e internal/notify para database.Tx (completar adapter SQLite), de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas (GO-041 DONE/IN_REVIEW) e o checkpoint; crie ou retome a branch task/go-055 a partir da base registrada. Escopo: Estender internal/platform/database.Tx a internal/triggers (incluindo Dispatcher.HooksFor, hoje pgx.Tx-only), internal/scheduler e internal/notify, preservando os wrappers pgx.Tx existentes; avaliar internal/files/internal/config para a mesma conversão ou follow-up próprio. Valide: mesmas fixtures de domínio nos dois adapters; testar concorrência/rollback SQLite sem depender de sintaxe exclusiva de PG; prova HTTP/job real de trigger e job de worker contra SQLite. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-042 — Validar substituição de plugins de terceiro do piloto ponta a ponta

- [x] **Status:** DONE — integrada via PR [#48](https://github.com/vjuliani/saltcorn/pull/48) (merge `44f9aa19310`); histórico em [execucoes/GO-042.md](execucoes/GO-042.md)
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend + Frontend + QA
- **Depende de:** GO-018, GO-020, GO-029, GO-039
- **Branch:** `task/go-042`
- **Escopo:** Publicar e operar a aplicação `guitars` completa (após GO-039) com o tema SB Admin 2 padrão e um campo de data HTML5 nativo no lugar de `@saltcorn/any-bootstrap-theme` e `@saltcorn/flatpickr-date` (plugins de terceiro reais sem código-fonte neste checkout, ver GO-029); comparar visual/funcionalmente ao legado.
- **Aceite:** Aplicação `guitars` publicada e usável ponta a ponta no novo frontend, nos papéis previstos pelo piloto; divergências visuais/UX documentadas explicitamente; GO-033 (CAP-045 "Plugins de terceiro do pack piloto") reclassificado de PARTIAL para PASS ou divergência formalmente aceita.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-042; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-042 — Validar substituição de plugins de terceiro do piloto ponta a ponta, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-042 a partir da base registrada. Escopo: Publicar e operar a aplicação guitars completa com o tema SB Admin 2 padrão e campo de data HTML5 nativo no lugar de any-bootstrap-theme/flatpickr-date; comparar visual/funcionalmente ao legado. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-043 — Implementar coordenação real de corte entre processos (edge/ownership)

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-009, GO-025
- **Branch:** `task/go-043`
- **Escopo:** Mecanismo de bloqueio/drenagem de admissões no edge por capacidade/tenant; atualização coordenada de `cutover.Guard` em TODAS as réplicas de servidor/worker (hoje carregado só uma vez no boot de cada processo, ver ADR-0008); acknowledgment explícito de troca de ownership antes de liberar tráfego para o novo dono.
- **Aceite:** Um ensaio de corte real com múltiplos processos (≥2 `cmd/server` + ≥1 `cmd/worker`) demonstra troca de ownership sem dois escritores simultâneos em nenhum momento, sem depender de expiração de lease como prova de que o dono antigo parou; ADR-0008 atualizado com o mecanismo implementado.
- **Rotina de validação:** Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar.
- **Regra de retomada:** Verificar estado real de tráfego, escritor ativo, schema e jobs; reconciliar a onda interrompida antes de avançar ou executar rollback; não repetir corte às cegas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-043; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-043 — Implementar coordenação real de corte entre processos (edge/ownership), de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-043 a partir da base registrada. Escopo: Mecanismo de bloqueio/drenagem de admissões no edge por capacidade/tenant; atualização coordenada de cutover.Guard em todas as réplicas de servidor/worker; acknowledgment explícito de troca de ownership antes de liberar tráfego. Valide: Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

## Tarefas de fechamento de lacunas — auditoria independente de completude (2026-09-21)

Antes de prosseguir com GO-039, o usuário pediu garantia de que TODA funcionalidade do legado está refletida na migração. Uma auditoria com 4 agentes independentes (varredura de rotas HTTP, modelos de domínio, builder/viewtemplates/ações, e CLI/mobile/tenancy/i18n — cada um caçando o que NUNCA foi listado, não re-julgando o que já está PARTIAL/BLOCKED) confirmou que a resposta era **não**: pelo menos 15 capacidades reais do legado, incluindo uma superfície de segurança inteira (administração de usuário/servidor), nunca entraram em nenhuma das duas matrizes existentes. Detalhes completos e evidência de código em [GO-001-matriz-capacidades.md §6](inventario/GO-001-matriz-capacidades.md#6-achados-adicionais-de-auditoria-independente-2026-09-21) e no [adendo da GO-033](paridade/RESULTADOS.md) (CAP-071 a CAP-085). GO-039 já foi emendada acima (CAP-073/074/085). As 7 tasks abaixo cobrem o restante.

### GO-044 — Portar administração de usuário e segurança do servidor

- [x] **Status:** DONE — integrada via PR [#44](https://github.com/vjuliani/saltcorn/pull/44) (merge `af76741c3bf2a29c95960cad404f58dcd3307eb2`); histórico em [execucoes/GO-044.md](execucoes/GO-044.md)
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Plataforma
- **Depende de:** GO-008, GO-009
- **Branch:** `task/go-044`
- **Escopo:** Portar (ou decidir explicitamente bloquear, com justificativa de risco) as capacidades de `packages/server/auth/admin.ts` (CAP-071: impersonação de usuário "become-user", emissão/gestão de certificado SSL/Let's Encrypt, matriz de permissões por tabela, admin de API tokens, force-logout, reset de senha) e `packages/server/auth/roleadmin.ts` (CAP-072: CRUD de role customizada, restrição de métodos de auth/layout/push por role) — nenhum dos dois arquivos entrou na contagem original de "39 arquivos em `routes/`" porque vivem em `auth/`.
- **Aceite:** Cada sub-capacidade de CAP-071/072 tem uma decisão explícita e documentada — portada com equivalente Go testado, OU bloqueada com justificativa de risco de segurança registrada (nunca silêncio); se impersonação de usuário for portada, tem trilha de auditoria própria (quem virou quem, quando); emissão de certificado nunca aceita entrada não sanitizada de domínio.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-044; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-044 — Portar administração de usuário e segurança do servidor, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-044 a partir da base registrada. Escopo: Portar ou decidir explicitamente bloquear (com justificativa de risco) as capacidades de auth/admin.ts (impersonação de usuário, SSL/Let's Encrypt, permissões por tabela, tokens, reset de senha) e auth/roleadmin.ts (CRUD de role, restrições por role). Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-045 — Portar versionamento de linha (table history) e sistema de tags

- [x] **Status:** DONE — integrada via PR [#49](https://github.com/vjuliani/saltcorn/pull/49) (merge `534af3a21d7`); histórico em [execucoes/GO-045.md](execucoes/GO-045.md)
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend
- **Depende de:** GO-011, GO-013, GO-027
- **Branch:** `task/go-045`
- **Escopo:** Portar versionamento de linha (CAP-075: flag `versioned` por tabela, tabela `<nome>__history`, consulta/restauração de versão anterior de um registro, `models/table.ts`) e o sistema de tags (CAP-077: CRUD de tag, associação tag↔entidade, export de Pack filtrado por tag, `models/tag.ts`/`tag_entry.ts`). **Emendado na execução (2026-09-22, leitura real do legado):** `Table.deleteRows` nunca chama `insert_history_row` — o histórico só é gravado em insert/update, nunca em delete; o texto original deste escopo ("grava a cada update/delete") presumia o contrário sem ter confirmado contra o código real, corrigido para seguir o comportamento VERDADEIRO. Tags cobrem só Table/View — Page nunca foi portada para Go, e `internal/triggers.Trigger` (GO-024) nunca modelou um campo `Name`, então uma tag de trigger não seria portável entre tenants (toda referência de Pack é por nome); os dois carve-outs são divergências deliberadas e documentadas, não um esquecimento.
- **Aceite:** Uma tabela marcada `versioned` grava histórico a cada insert/update (nunca delete, ver achado acima) e permite consultar/restaurar uma versão anterior de um registro; tags podem ser criadas, associadas a tabelas/views, e usadas para filtrar um export de Pack (GO-027, com fechamento transitivo de referências FieldKey — o pack filtrado é, ele mesmo, importável); GO-033 (CAP-075, CAP-077) reclassificado de NÃO LISTADA para PASS PARCIAL com lacuna explícita (undo/redo explícito e exposição HTTP/CLI/UI de tags ficam de fora, documentado).
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-045; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-045 — Portar versionamento de linha (table history) e sistema de tags, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-045 a partir da base registrada. Escopo: Portar versionamento de linha (flag versioned, tabela de histórico, restaurar versão anterior) e sistema de tags (CRUD, associação a entidades, export de Pack filtrado por tag). Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-046 — Portar catálogo administrativo de instalação (metadata, plugins, backup completo)

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P1 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-026, GO-027, GO-032
- **Branch:** `task/go-046`
- **Escopo:** Portar `_sc_metadata` (CAP-078: key/value genérico, rastreamento de versão do core, payload de backup); catálogo/governança de plugins (CAP-079: descoberta via npm registry, versão/upgrade, compatibilidade de engine, checagem de views dependentes antes de remover — distinto do MECANISMO de execução já coberto por GO-022/029); backup de instalação completa distinto de Pack (CAP-080: Plugin/Role/Page/PageGroup/MetaData/File/Crash/Table/View/Field num zip) com agendamento/retenção GFS/criptografia/destinos S3/SFTP (CAP-081); e-mail HTML/MJML renderizado a partir de uma View (CAP-083, `viewToMjml`).
- **Aceite:** `_sc_config`/`_sc_metadata` cobrem os casos de uso reais do legado (versão do core, payload de backup); catálogo de plugins rastreia versão/compatibilidade; backup de instalação completa exporta/restaura as entidades listadas; ao menos um modo de e-mail HTML a partir de View funciona; cada sub-capacidade sem porte real tem decisão de escopo explícita registrada.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-046; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-046 — Portar catálogo administrativo de instalação (metadata, plugins, backup completo), de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-046 a partir da base registrada. Escopo: Portar _sc_metadata, catálogo/governança de plugins, backup de instalação completa (distinto de Pack) com agendamento/retenção/criptografia/destinos remotos, e e-mail HTML/MJML a partir de View. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-047 — Portar internacionalização (i18n) da interface

- [x] **Status:** DONE — integrada via PR [#50](https://github.com/vjuliani/saltcorn/pull/50) (merge `55b89deed55`); histórico em [execucoes/GO-047.md](execucoes/GO-047.md)
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-017, GO-018
- **Branch:** `task/go-047`
- **Escopo:** Portar internacionalização da interface (CAP-082): configuração de idiomas por tenant, seleção de idioma por usuário/cookie, mecanismo de tradução de strings da UI no frontend React + BFF. Decidir explicitamente se a tradução assistida por LLM (`saltcorn dev translate`) é portada, substituída, ou fica fora de escopo (com justificativa) — não é core, mas precisa de decisão registrada. **Emendado na execução (2026-09-22, leitura real do legado):** o catálogo de ~36 idiomas + a UI de administração `/localizer` (CRUD de idiomas/strings custom) são uma superfície de produto muito maior que o aceite exige — portado o MECANISMO (resolução de idioma efetivo, seleção por usuário persistida, catálogo de traduções, RTL) com 2 idiomas reais (pt/en); tradução assistida por LLM decidida explicitamente fora de escopo (nenhuma integração de LLM existe no backend Go hoje).
- **Aceite:** A interface (frontend React + páginas servidas pelo BFF) suporta pelo menos 2 idiomas configuráveis por tenant, com seleção por usuário persistida (confirmado ponta a ponta em navegador real: troca pelo seletor + persistência após reload); GO-033 (CAP-082) reclassificado de NÃO LISTADA para PASS PARCIAL com lacuna explícita (36 idiomas do legado, UI `/localizer`, e tradução assistida por LLM ficam de fora, documentado).
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-047; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-047 — Portar internacionalização (i18n) da interface, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-047 a partir da base registrada. Escopo: Portar configuração de idiomas por tenant, seleção de idioma por usuário, mecanismo de tradução de strings da UI; decidir escopo da tradução assistida por LLM. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-048 — Portar editor visual de Workflow

- [x] **Status:** DONE — integrada via PR [#51](https://github.com/vjuliani/saltcorn/pull/51) (merge `f3ecae87d4d`); histórico em [execucoes/GO-048.md](execucoes/GO-048.md)
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Frontend
- **Depende de:** GO-018, GO-024
- **Branch:** `task/go-048`
- **Escopo:** Portar a UI de autoria de grafos de workflow (CAP-076: nós/arestas/condições, hoje `packages/workflow-editor` em React Flow) para o frontend novo, produzindo/editando a mesma definição que `internal/workflow` (GO-024) já executa — sem esta UI, o motor de workflow existe mas ninguém consegue autorar um workflow novo. **Emendado na execução (2026-09-22, achado de preflight):** `internal/workflow` (GO-024) só tinha estado de EXECUÇÃO persistido, nunca a DEFINIÇÃO — sem isso o próprio Aceite desta task era impossível de cumprir. Absorvido: `_sc_workflows`/`_sc_workflow_steps` (schema novo), `Compile` (liga definição persistida a `Definition` executável), catálogo de ações restrito a `set_context`/`count_rows` (efeitos internos puros — `send_email`/`webhook` ficam fora, razão documentada em execucoes/GO-048.md), CRUD HTTP completo + endpoint de execução síncrona.
- **Aceite:** Um usuário cria/edita um workflow visualmente no frontend novo (React) e o resultado é executado de ponta a ponta por `internal/workflow` (confirmado ponta a ponta em navegador real: criar 2 passos, ligar por next_step, marcar inicial, rodar, contexto final observável, definição sobrevive a reload); GO-033 (CAP-076) reclassificado de NÃO LISTADA para PASS PARCIAL com lacuna explícita (ramificação binária não N-vias, catálogo de 2 ações, sem geração por IA/sub-workflow/tipos de passo que pausam execução/node type de ForLoop, documentado).
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-048; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-048 — Portar editor visual de Workflow, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-048 a partir da base registrada. Escopo: Portar a UI de autoria de grafos de workflow (nós/arestas/condições) para o frontend novo, produzindo a mesma definição que internal/workflow executa. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-049 — Portar administração de ciclo de vida de tenants

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-009, GO-032
- **Branch:** `task/go-049`
- **Escopo:** Portar administração de ciclo de vida de tenants (CAP-084: criar/listar/deletar/configurar tenant via UI/CLI administrativa, provisionamento a partir de um tenant-template) — distinto da RESOLUÇÃO de tenant em runtime (CAP-009, já coberta). Decidir explicitamente o escopo de emissão automática de certificado TLS por tenant (Let's Encrypt/Greenlock) — não é core, mas é uma superfície de segurança/operação que precisa de decisão registrada, não silêncio.
- **Aceite:** Um operador cria/lista/deleta um tenant e opcionalmente o provisiona a partir de um template, via CLI Go (`cmd/cli`) ou rota administrativa; decisão sobre TLS automático documentada (portado com biblioteca madura, ou explicitamente delegado a um proxy/edge externo); GO-033 (CAP-084) reclassificado de NÃO LISTADA para PASS/PARTIAL.
- **Rotina de validação:** Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar.
- **Regra de retomada:** Verificar estado real de tráfego, escritor ativo, schema e jobs; reconciliar a onda interrompida antes de avançar ou executar rollback; não repetir corte às cegas.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-049; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-049 — Portar administração de ciclo de vida de tenants, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-049 a partir da base registrada. Escopo: Portar criar/listar/deletar/configurar tenant via UI/CLI administrativa e provisionamento por template; decidir escopo de TLS automático por tenant. Valide: Executar preflight da onda em ambiente identificado, reconciliação de dados, SLOs e ensaio de recuperação; registrar critérios de promoção e abortar. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-050 — Decidir escopo dos achados de nicho da auditoria de completude

- [x] **Status:** DONE — integrada via PR [#54](https://github.com/vjuliani/saltcorn/pull/54) (merge `401ee584044c`); histórico em [execucoes/GO-050.md](execucoes/GO-050.md)
- **Fase:** F4 · **Prioridade:** P2 · **Tamanho:** S
- **Responsável sugerido:** Arquitetura
- **Depende de:** GO-001
- **Branch:** `task/go-050`
- **Escopo:** Para cada achado de nicho listado em `GO-001-matriz-capacidades.md §6.3` e no adendo de `paridade/RESULTADOS.md` (copiloto de IA para layout, Blockly/ação `blocks`, diagrama Cytoscape de tabelas/tags/roles, rotas HTTP registradas por plugin, PWA/Web Share Target, `robots.txt`/`sitemap.xml`, layout de emergência, câmera/geolocalização mobile, e as ~21 ações de `base-plugin/actions.ts` ainda sem menção nominal), registrar uma decisão EXPLÍCITA: portar (abrindo task própria), substituir funcionalmente, ou aceitar como fora de escopo — com justificativa em cada caso. Não é uma task de implementação; é uma task de DECISÃO documentada, para que nenhum desses itens fique esquecido por padrão.
- **Aceite:** Cada item da lista de nicho tem uma linha própria na matriz de capacidades com destino decidido (Go nativo/Frontend/Fora de escopo/Bloqueador) e justificativa — nenhum item permanece "não avaliado".
- **Rotina de validação:** Revisão de documentação — não há código para testar nesta task.
- **Regra de retomada:** Conferir quais itens já têm decisão registrada antes de reabrir os demais.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-050; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-050 — Decidir escopo dos achados de nicho da auditoria de completude, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas e o checkpoint; crie ou retome a branch task/go-050 a partir da base registrada. Escopo: Para cada achado de nicho da auditoria de 2026-09-21 (copiloto de IA, Blockly, diagrama, rotas de plugin, PWA, robots/sitemap, layout de emergência, camera/geolocalização mobile, ações não citadas), registrar decisão explícita de portar/substituir/aceitar fora de escopo com justificativa. Valide: Revisão de documentação. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-051 — Portar renderização de view aninhada, viewtemplate Feed e campo de arquivo em Edit

- [x] **Status:** DONE — integrada via PR [#52](https://github.com/vjuliani/saltcorn/pull/52) (merge `d179855884b3`); histórico em [execucoes/GO-051.md](execucoes/GO-051.md)
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-039
- **Branch:** `task/go-051`
- **Escopo:** Carve-out do preflight de GO-039 (2026-09-21, extração real de `pack.json` do pack `guitars`): (1) renderizar uma view inteira DENTRO de outra a partir de um nó de layout `type: "view"` com `relation` (ex.: `create_guitar` embute `edit_processed_embed` via `.guitars.processed$guitar` — relação 1:N, contexto de role/tenant próprio para a sub-view); (2) o viewtemplate `Feed` (`guitar_feed`), que depende do MESMO mecanismo (`show_view` renderizado por linha, com `view_to_create` para o botão de criação); (3) um tipo de campo `FieldFile` em `internal/metadata` (GO-011) ligado a `internal/files` (GO-026), necessário para a fieldview `upload` usada por `upload_photo`.
- **Aceite:** `create_guitar` renderiza e salva o sub-formulário `edit_processed_embed` embutido, filtrado pela relação da linha pai; `guitar_feed` renderiza os cards via `show_guitar` por linha e o botão de criação abre `create_guitar`; `upload_photo` faz upload real de arquivo e persiste a referência no campo; as 9 views do pack `guitars` (GO-039 + esta task) operam de ponta a ponta; GO-033 (CAP-034, CAP-040, CAP-073, CAP-074, CAP-085) reclassificado de PARCIAL para PASS completo.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-051; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-051 — Portar renderização de view aninhada, viewtemplate Feed e campo de arquivo em Edit, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas (GO-039 DONE) e o checkpoint; crie ou retome a branch task/go-051 a partir da base registrada. Escopo: Renderizar view aninhada via nó de layout type=view com relation (create_guitar embute edit_processed_embed); portar o viewtemplate Feed (guitar_feed, mesmo mecanismo de show_view por linha); adicionar FieldFile a internal/metadata ligado a internal/files para a fieldview upload (upload_photo). Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-052 — Portar mecanismo de evento nomeado (`emitEvent`) e capacidade de escrita no host de plugins

- [x] **Status:** DONE — integrada via PR [#53](https://github.com/vjuliani/saltcorn/pull/53) (merge `078c312cf19a`); histórico em [execucoes/GO-052.md](execucoes/GO-052.md)
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend
- **Depende de:** GO-040, GO-022
- **Branch:** `task/go-052`
- **Escopo:** Carve-out do preflight de GO-040 (2026-09-21): (1) portar `Trigger.emitEvent(eventname, channel, user, payload)` do legado — um mecanismo de evento NOMEADO genérico, desacoplado de qualquer escrita de registro, disparado hoje por `POST /api/emit-event`; exige estender `internal/triggers.WhenTrigger` (hoje fechado em Validate/Insert/Update/Delete) para aceitar nomes de evento arbitrários, e uma rota HTTP nova para emiti-los, com seu próprio modelo de autorização (o legado distingue `ReceiveMobileShareData`, sempre permitido para usuário autenticado, de outros nomes, que exigem `mobile_emit_allowed_events` configurado); (2) adicionar uma capacidade de ESCRITA a `internal/pluginhost` (hoje só `CapDBRead`) — decisão de escopo de GO-022 já registrava isto como pendente ("precisa de decisão própria de idempotência").
- **Aceite:** Um evento nomeado arbitrário emitido via HTTP dispara os triggers cujo `when_trigger` bate com o nome; o trigger `receive_share_trigger` do pack `guitars` (`ReceiveMobileShareData`, ação `run_js_code` com `Table.findOne(...).insertRow(...)`) dispara de ponta a ponta e persiste a linha real na tabela `photos`; a capacidade de escrita no host tem sua própria política de idempotência (nunca duas escritas para o mesmo evento) e autorização (mesmo papel do ator que originou a chamada, nunca elevado). GO-033 (CAP-047/048) reclassificado de PASS parcial (GO-040) para PASS completo.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-052; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-052 — Portar mecanismo de evento nomeado (emitEvent) e capacidade de escrita no host de plugins, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas (GO-040/GO-022 DONE) e o checkpoint; crie ou retome a branch task/go-052 a partir da base registrada. Escopo: Portar Trigger.emitEvent (evento nomeado genérico, desacoplado de escrita de registro, disparado por rota HTTP nova) estendendo internal/triggers.WhenTrigger; adicionar capacidade de escrita a internal/pluginhost com política própria de idempotência/autorização. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-053 — Portar Web Share Target (PWA) reaproveitando o mecanismo de evento nomeado

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P2 · **Tamanho:** S
- **Responsável sugerido:** Backend + BFF
- **Depende de:** GO-047, GO-052
- **Branch:** `task/go-053`
- **Escopo:** Achado de nicho de GO-050 (`inventario/GO-001-matriz-capacidades.md` §6.5, item 5) — único achado que reaproveita DIRETAMENTE um mecanismo já pronto: o legado (`routes/notifications.ts`) expõe `GET /manifest.json` (Web App Manifest dinâmico, derivado de config já existente — `site_name`, ícones, cores) e `POST /share-handler` (Web Share Target — recebe conteúdo compartilhado via "Compartilhar com..." do navegador e chama `Trigger.emitEvent("ReceiveMobileShareData", null, user, {row: body})`, exatamente `internal/triggers.Dispatcher.EmitEvent` que GO-052 já portou). Portar as duas rotas equivalentes em Go/BFF, incluindo `install_progressive_web_app` (ação client-side de instalação, achado 9d de GO-050) como extensão natural desta mesma task.
- **Aceite:** `GET .../manifest.json` devolve um manifesto PWA válido (name/icons/start_url/display, e um bloco `share_target` quando existir um trigger `ReceiveMobileShareData`); `POST .../share-handler` chama `Dispatcher.EmitEvent("ReceiveMobileShareData", ...)` de ponta a ponta (mesma prova de `receive_share_trigger` já validada em GO-052, agora acionável também por este caminho); validado em navegador real simulando o payload de compartilhamento.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-053; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-053 — Portar Web Share Target (PWA) reaproveitando o mecanismo de evento nomeado, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas (GO-047/GO-052 DONE) e o checkpoint; crie ou retome a branch task/go-053 a partir da base registrada. Escopo: Portar GET .../manifest.json (Web App Manifest dinâmico) e POST .../share-handler (Web Share Target, chamando Dispatcher.EmitEvent("ReceiveMobileShareData", ...) já existente de GO-052), mais a ação client-side install_progressive_web_app. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```

### GO-054 — Portar catálogo estendido de ações nativas de trigger

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P2 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-029, GO-040, GO-047, GO-051, GO-052
- **Branch:** `task/go-054`
- **Escopo:** Achado de nicho de GO-050 (`inventario/GO-001-matriz-capacidades.md` §6.5, item 9a) — portar, como `ActionFunc`s nativas em `internal/triggers.BuiltinActions()` (ADR-0005, mesmo padrão de `send_email`/`webhook`), o subconjunto de ações de `base-plugin/actions.ts` do legado com valor de produto real e baixo custo marginal (várias reaproveitam mecanismos já portados por tasks anteriores): `loop_rows`, `duplicate_row`, `duplicate_row_prefill_edit`, `recalculate_stored_fields`, `sleep`, `notify_user`, `toast`, `step_control_flow`, `emit_event` (cascata — chama `Dispatcher.EmitEvent` já existente), `download_file_to_browser` (reaproveita `GET .../files/{id}` de GO-051), `reload_embedded_view`, `progress_bar`, `copy_to_clipboard`, `set_user_language` (reaproveita `PATCH .../actor` de GO-047), `refresh_user_session`. Fora de escopo, decisão já registrada em GO-050: `find_or_create_dm_room`/`train_model_instance`/`sync_table_from_external`/`convert_session_to_user`/`insert_joined_row` (dependem de capacidades-mãe não portadas) e `run_js_code_in_field` (bloqueador de segurança, mesma classe de CAP-072).
- **Aceite:** Cada ação do subconjunto acima está registrada em `BuiltinActions()`, com pelo menos um teste de disparo real via trigger (Postgres real) provando o efeito colateral esperado; ações que reaproveitam um mecanismo já existente (arquivo, idioma, emitEvent) o fazem chamando o MESMO código já portado, nunca uma segunda implementação paralela.
- **Rotina de validação:** Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões.
- **Regra de retomada:** Inspecionar jobs, leases, eventos e efeitos externos; reconciliar resultados desconhecidos e preservar chaves de idempotência antes de reprocessar.
- **Controle de execução:** Aplicar EXECUCAO.md; criar/retomar task/go-054; validar e registrar evidências; fazer commit/push e abrir/atualizar PR; IN_REVIEW até revisão, checks e merge, então DONE.

**Comando de execução para o agente:**

```text
Execute a task GO-054 — Portar catálogo estendido de ações nativas de trigger, de docs/migracao-go/TASKS.md, seguindo docs/migracao-go/EXECUCAO.md. Verifique dependências integradas (GO-029/GO-040/GO-047/GO-051/GO-052 DONE) e o checkpoint; crie ou retome a branch task/go-054 a partir da base registrada. Escopo: Portar como ActionFunc nativas em internal/triggers.BuiltinActions() o subconjunto de ações do legado com valor real e baixo custo marginal (loop_rows, duplicate_row, duplicate_row_prefill_edit, recalculate_stored_fields, sleep, notify_user, toast, step_control_flow, emit_event, download_file_to_browser, reload_embedded_view, progress_bar, copy_to_clipboard, set_user_language, refresh_user_session) — reaproveitando mecanismos já portados (emitEvent de GO-052, download de arquivo de GO-051, idioma de usuário de GO-047) onde aplicável. Valide: Executar fixtures da capacidade, autorização e cenários de falha/timeout/repetição; conferir ordem e atomicidade dos efeitos e compatibilidade das extensões. Comprove todos os critérios de aceite, atualize o histórico e sincronize CSV/Markdown. Faça commit apenas dos arquivos da task, publique a branch em vjuliani/saltcorn e abra ou atualize o PR para a base registrada. Termine informando URL do PR, validações e pendências; mantenha IN_REVIEW até revisão, checks obrigatórios e merge. Se bloqueado, registre causa e próximo passo sem declarar conclusão.
```
