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

- [ ] **Status:** IN_REVIEW — PR [#11](https://github.com/vjuliani/saltcorn/pull/11) aberto; histórico em [execucoes/GO-008.md](execucoes/GO-008.md)
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

- [ ] **Status:** IN_REVIEW — PR [#12](https://github.com/vjuliani/saltcorn/pull/12) aberto; histórico em [execucoes/GO-009.md](execucoes/GO-009.md)
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-012, GO-013, GO-018
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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

- [ ] **Status:** TODO
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
