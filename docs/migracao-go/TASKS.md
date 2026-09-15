# Backlog de migração para Go

Relaciona-se ao [plano de arquitetura](README.md). São tarefas locais para refinamento e execução; nenhuma issue foi publicada em sistema externo.

Todas começam em **TODO**. IDs são estáveis; dependências são IDs de tarefas. P0 = necessária para o escopo final proposto; P1 = otimização ou recurso a confirmar no inventário (torna-se bloqueador se usado por aplicação migrada). Tamanho relativo: M = médio; L = grande e deve ser dividido em subtarefas ao iniciar. Responsável indica papel sugerido, não pessoa atribuída. Não há estimativa em dias nem datas comprometidas.

Uma tarefa pode começar em protótipo antes das dependências, mas só pode ser concluída com elas satisfeitas. Concluir requer artefato revisável, testes pertinentes e evidência dos critérios de aceite. Alterações de comportamento exigem atualização da matriz de compatibilidade.

## Sequência inicial

1. GO-001: inventário real e escolha do piloto.
2. GO-002, GO-003 e GO-004: baseline, decisões e risco de plugins.
3. GO-005 a GO-010: fundação, contratos, identidade e controle da transição.
4. GO-011 a GO-015 e GO-017 a GO-021: fluxo vertical de dados e editor.
5. Completar capacidades usadas pelo piloto e executar GO-034/GO-036; depois ampliar a paridade e as ondas.

O canário do piloto tem validação própria de carga e paridade; não exige mobile/SQLite quando ausentes do piloto. A conversão integral exige GO-033 e GO-035. GO-016 pode concluir com decisão fundamentada de adiar projeções.

## Tarefas

### GO-001 — Inventariar capacidades e aplicações

- [ ] **Status:** TODO
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Arquitetura
- **Depende de:** Nenhuma
- **Escopo:** Catalogar rotas, modelos, plugins por versão, autenticação, bancos, layouts, packs e mobile; escolher aplicações representativas.
- **Aceite:** Matriz liga cada capacidade a código, aplicação, fixture, destino Go/bridge e bloqueador; escopo piloto e escopo final registrados.

### GO-002 — Criar baseline de comportamento e desempenho

- [ ] **Status:** TODO
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** QA
- **Depende de:** GO-001
- **Escopo:** Preparar datasets sanitizados e casos das suítes data/server/Playwright; medir latência, erros, consumo e jobs.
- **Aceite:** Execução reproduzível no commit de referência; limites p95/p99, RPO/RTO e casos críticos definidos; defeitos conhecidos separados da compatibilidade desejada.

### GO-003 — Registrar decisões de arquitetura

- [ ] **Status:** TODO
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Arquitetura
- **Depende de:** GO-001
- **Escopo:** Registrar ADRs de monólito modular, CQRS lógico, BFF, frontend, bancos e política de extensões.
- **Aceite:** Alternativas e custos documentados; requisito de servidor sem Node e estratégia SQLite/mobile explícitos.

### GO-004 — Prototipar compatibilidade de extensões e expressões

- [ ] **Status:** TODO
- **Fase:** F0 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-001
- **Escopo:** Executar amostra de fórmulas, callbacks, custom types e plugins com dependências de banco; experimentar fronteira RPC.
- **Aceite:** Relatório demonstra suportados e incompatíveis, sem supor serialização de funções; operações transacionais classificadas e custo da bridge medido.

### GO-005 — Criar fundação Go e pipeline

- [ ] **Status:** TODO
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-003
- **Escopo:** Criar módulo, server/worker/CLI, configuração, shutdown, health/readiness e CI; fixar toolchain e dependências.
- **Aceite:** Build reproduzível, go test e race detector nos módulos concorrentes passam; processo encerra sem perder transações em curso.

### GO-006 — Definir contratos e fixtures HTTP

- [ ] **Status:** TODO
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-001, GO-003
- **Escopo:** Versionar OpenAPI, DTOs, erros, paginação, IDs, datas, decimais, null e documento de layout; mapear APIs legadas.
- **Aceite:** Clientes Go/TS validam exemplos; compatibilidade e mudanças de contrato detectadas em CI.

### GO-007 — Implementar tenancy e contexto transacional

- [ ] **Status:** TODO
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-005
- **Escopo:** Resolver tenant por mapeamento confiável; propagar ator, tenant e transação em context.Context, SQL e jobs.
- **Aceite:** Testes concorrentes com dois tenants, reuso de conexão, cancelamento e rollback não vazam schema, usuário ou dados.

### GO-008 — Migrar identidade e autorização

- [ ] **Status:** TODO
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-006, GO-007
- **Escopo:** Implementar login, hashes, roles, ownership, RLS aplicável, tokens, MFA e compatibilidade ou renovação de sessão.
- **Aceite:** Matriz positiva/negativa cobre APIs, arquivos, sessão, revogação e acesso cruzado; estratégias de plugins sem suporte bloqueiam o corte.

### GO-009 — Criar entrada de migração e ownership de escrita

- [ ] **Status:** TODO
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend + Plataforma
- **Depende de:** GO-006, GO-008
- **Escopo:** Rotear tenant/capacidade para Node ou Go; registrar proprietário e bloquear caminhos alternativos, inclusive jobs.
- **Aceite:** Teste comprova escritor único e rollback de rota; identidade não pode ser forjada por headers; requisições em andamento são drenadas.

### GO-010 — Instrumentar observabilidade

- [ ] **Status:** TODO
- **Fase:** F1 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-005
- **Escopo:** Adicionar logs estruturados, métricas e tracing HTTP/SQL/jobs/RPC com correlação e controle de cardinalidade.
- **Aceite:** Uma operação é rastreável entre runtimes sem registrar tokens ou dados sensíveis; dashboards distinguem falhas e saturação.

### GO-011 — Portar catálogo e evolução de schema

- [ ] **Status:** TODO
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-007, GO-008
- **Escopo:** Portar tabelas/campos/relações/constraints e migrations PG com locks, versão de metadados e invalidação de cache.
- **Aceite:** Criar/alterar schema mantém catálogo e DDL consistentes; falha intermediária é recuperável; nomes maliciosos e concorrência são cobertos.

### GO-012 — Implementar compilador de consultas dinâmicas

- [ ] **Status:** TODO
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011
- **Escopo:** Portar filtros, joins, agregações, ordenação e paginação; parametrizar valores e resolver identificadores pelo catálogo.
- **Aceite:** Corpus compara resultados, tipos, NULL, datas, chaves compostas e permissões com legado; consultas inválidas não permitem injeção SQL.

### GO-013 — Implementar comandos de registros

- [ ] **Status:** TODO
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011, GO-012
- **Escopo:** Portar insert/update/delete, validações e transações; definir pontos de extensão de triggers e controle de versão.
- **Aceite:** Fixtures isoladas mostram equivalência de dados/erros; conflito concorrente retorna erro definido e falha desfaz toda a operação.

### GO-014 — Adicionar idempotência e outbox

- [ ] **Status:** TODO
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-013
- **Escopo:** Persistir chave/payload/resultado e eventos junto à escrita; worker com retries, deduplicação e falhas inspecionáveis.
- **Aceite:** Crash antes e depois do commit não perde evento confirmado; redelivery não repete efeito interno; mesma chave com payload diferente é rejeitada.

### GO-015 — Integrar políticas em todos os caminhos de leitura

- [ ] **Status:** TODO
- **Fase:** F2 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend + QA
- **Depende de:** GO-008, GO-012
- **Escopo:** Aplicar autorização a busca, exportação, contagem, agregações e caches; usar banco primário após escrita.
- **Aceite:** Revogação de acesso é imediata nos caminhos protegidos; contagens e agregados não revelam registros proibidos.

### GO-016 — Medir necessidade de projeções CQRS

- [ ] **Status:** TODO
- **Fase:** F2 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend
- **Depende de:** GO-002, GO-010, GO-014, GO-015
- **Escopo:** Comparar queries diretas e projeção de uma consulta custosa; definir watermark, reconstrução e fallback.
- **Aceite:** ADR decide adotar ou adiar com benchmark; se adotada, rebuild, atraso, ordem e mudança de schema são testados antes do uso.

### GO-017 — Implementar BFF web

- [ ] **Status:** TODO
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend
- **Depende de:** GO-006, GO-008, GO-012, GO-013
- **Escopo:** Compor bootstrap do editor, metadados, permissões de UI e dados; sessão/CSRF/erros e limites de payload.
- **Aceite:** Contrato de tela passa testes; BFF usa serviços de aplicação sem SQL direto; domínio rejeita acesso mesmo se UI/BFF omitir validação.

### GO-018 — Desacoplar builder de modelos do servidor

- [ ] **Status:** TODO
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Frontend
- **Depende de:** GO-006
- **Escopo:** Adicionar cliente tipado e adapters no React/Craft.js existente; versionar documento e manter import/export.
- **Aceite:** Builder funciona com mocks e contratos; round-trip de fixtures não perde propriedades, referências ou extensões reconhecidas.

### GO-019 — Integrar editor ao BFF

- [ ] **Status:** TODO
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Frontend
- **Depende de:** GO-017, GO-018
- **Escopo:** Conectar tabelas, campos, views, preview e publicação; tratar erro de validação e edição concorrente.
- **Aceite:** E2E cria tabela e view, salva, reabre e publica com dois papéis; conflito de edição é apresentado sem sobrescrever silenciosamente.

### GO-020 — Portar runtime de views e páginas

- [ ] **Status:** TODO
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-012, GO-013, GO-018
- **Escopo:** Interpretar layouts/metadados e widgets básicos; manter URLs, forms, navegação e fronteira de widgets legados.
- **Aceite:** Aplicações fixture renderizam e operam com paridade funcional/visual; layouts incompatíveis bloqueiam publicação ou seguem rota legada explícita.

### GO-021 — Validar experiência e compatibilidade web

- [ ] **Status:** TODO
- **Fase:** F3 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** QA + Frontend
- **Depende de:** GO-019, GO-020
- **Escopo:** Cobrir i18n, responsividade, teclado, temas, assets, sanitização HTML e componentes customizados do piloto.
- **Aceite:** Fluxo criar/publicar/operar passa E2E nos navegadores acordados; nenhum script não autorizado executa em cenários de teste.

### GO-022 — Implementar host temporário de extensões JS

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Plataforma
- **Depende de:** GO-004, GO-008, GO-009
- **Escopo:** Criar RPC versionado com capacidades explícitas, isolamento OS/processo, limites e protocolo de erros; evitar credenciais amplas.
- **Aceite:** Timeout/crash e acesso proibido são contidos; plugin transacional incompatível mantém operação integral no legado; matriz de plugins atualizada.

### GO-023 — Portar tipos e expressões prioritários

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-004, GO-011, GO-022
- **Escopo:** Portar tipos básicos e subconjunto de expressões do piloto; fallback explícito quando semântica divergir.
- **Aceite:** Corpus cobre coerção, null, datas, decimal, erros e async; expressão desconhecida nunca muda resultado silenciosamente.

### GO-024 — Portar triggers, ações e workflows

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-013, GO-014, GO-023
- **Escopo:** Separar hooks transacionais e efeitos após commit; portar estado de workflow, condições e rastreamento.
- **Aceite:** Ordem e rollback equivalem às fixtures; falhas/repetições não duplicam efeitos internos; workflows interrompidos retomam de forma documentada.

### GO-025 — Portar scheduler e coordenação de workers

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Backend
- **Depende de:** GO-014, GO-024
- **Escopo:** Implementar agendamento, timezone, leases/locks, retomada e drenagem ao transferir ownership.
- **Aceite:** Dois workers não executam simultaneamente job exclusivo; reinício e expiração de lease têm comportamento testado; scheduler antigo é desativado por escopo.

### GO-026 — Portar arquivos e notificações

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-008, GO-014
- **Escopo:** Portar upload/download, armazenamento local/S3 conforme escopo, autorização, e-mail, webhooks e notificações.
- **Aceite:** Usuário sem acesso não baixa arquivo; uploads interrompidos são limpos; falha do provedor entra em retry e duplicatas externas têm política explícita.

### GO-027 — Portar configuração, packs e biblioteca

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011, GO-020, GO-023
- **Escopo:** Migrar import/export de aplicações, dependências, assets, configurações e referências entre entidades.
- **Aceite:** Export legado importa no Go e reexporta sem perda no corpus; faltas de plugin/versão são reportadas antes de aplicar alterações.

### GO-028 — Migrar comunicação em tempo real

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P1 · **Tamanho:** M
- **Responsável sugerido:** Backend + Frontend
- **Depende de:** GO-017, GO-024
- **Escopo:** Inventariar eventos Socket.IO; manter protocolo via adapter ou migrar cliente e servidor de forma coordenada.
- **Aceite:** Reconexão, sessão expirada, ordenação e isolamento por tenant passam testes; não tratar WebSocket puro como substituto compatível de Socket.IO.

### GO-029 — Definir SDK e portar plugins exigidos

- [ ] **Status:** TODO
- **Fase:** F4 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-022, GO-023, GO-024, GO-027
- **Escopo:** Definir contratos de tipos/views/ações/auth; portar plugins requeridos por aplicação e documentar diferenças.
- **Aceite:** Cada plugin necessário tem versão testada nativa ou substituição funcional; os ainda dependentes de Node permanecem bloqueadores de servidor somente Go.

### GO-030 — Implementar adapter SQLite

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend
- **Depende de:** GO-011, GO-012, GO-013, GO-014
- **Escopo:** Cobrir DDL, tipos, transações, locking e outbox no SQLite; distinguir tenancy disponível e modo desktop.
- **Aceite:** Mesmas fixtures de domínio passam PG/SQLite com divergências justificadas; concorrência e recuperação são verificadas sem sintaxe exclusiva de PG.

### GO-031 — Migrar contratos de sync e mobile offline

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Mobile
- **Depende de:** GO-006, GO-008, GO-027, GO-030
- **Escopo:** Versionar sync, conflitos, exclusões, migrações locais e retomada; preservar runtime mobile e mapear dependências JS.
- **Aceite:** Cliente fica offline, edita, reconecta e converge com conflitos explícitos; upgrade preserva dados locais e autorização; escopo Go no dispositivo definido.

### GO-032 — Portar CLI e distribuição self-hosted

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-005, GO-027, GO-030
- **Escopo:** Portar setup, migrations, backup/restore, configuração e serve; preparar imagens e documentação operacional.
- **Aceite:** Instalação limpa e upgrade de fixture funcionam; backup restaura dados, arquivos, configuração e versões de plugins; dependências runtime declaradas.

### GO-033 — Executar matriz completa de paridade

- [ ] **Status:** TODO
- **Fase:** F5 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** QA
- **Depende de:** GO-021, GO-025, GO-026, GO-027, GO-028, GO-029, GO-030, GO-031, GO-032
- **Escopo:** Consolidar testes HTTP, domínio, UI, plugins, bancos e mobile; separar perfil piloto do perfil integral.
- **Aceite:** Todas as capacidades contratadas têm evidência e resultado; lacunas impedem declarar migração completa.

### GO-034 — Ensaiar migração de dados e rollback

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Backend + Plataforma
- **Depende de:** GO-009, GO-027, GO-032
- **Escopo:** Ensaiar expansão/contração, ownership, locks, reconciliação de IDs/contagens/checksums e recuperação em cópia sanitizada.
- **Aceite:** Runbook demonstra retorno seguro com escritas Go ou procedimento de pausa/reconciliação; RPO/RTO atendidos; nenhum DDL destrutivo durante janela de retorno.

### GO-035 — Validar carga e falhas operacionais

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** QA + Plataforma
- **Depende de:** GO-002, GO-010, GO-014, GO-025, GO-033
- **Escopo:** Executar carga realista e injetar falhas em DB, worker, host JS e armazenamento; medir recursos e latências.
- **Aceite:** SLOs acordados são atendidos; vazamento entre tenants, perda de eventos e saturação sem limites impedem promoção.

### GO-036 — Liberar canário do piloto

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Plataforma + QA
- **Depende de:** GO-021, GO-024, GO-025, GO-026, GO-027, GO-034
- **Escopo:** Validar subconjunto da matriz, carga e dependências do piloto; rotear poucos tenants com observação e critérios de abortar.
- **Aceite:** Todos os recursos usados pelo piloto têm paridade; janela acordada sem incidentes críticos; rollback ensaiado e métricas comparadas ao baseline.

### GO-037 — Expandir migração por ondas

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** L
- **Responsável sugerido:** Plataforma
- **Depende de:** GO-033, GO-034, GO-035, GO-036
- **Escopo:** Migrar grupos compatíveis, reconciliar dados por onda e acompanhar acessos residuais ao Node.
- **Aceite:** Cada onda possui checklist e evidências de SLO/recuperação; não há escritores legados ativos para capacidades migradas.

### GO-038 — Retirar legado e encerrar migração

- [ ] **Status:** TODO
- **Fase:** F6 · **Prioridade:** P0 · **Tamanho:** M
- **Responsável sugerido:** Arquitetura + Plataforma
- **Depende de:** GO-029, GO-037
- **Escopo:** Remover rotas, jobs, credenciais, bridge e dependências servidor Node resolvidas; arquivar runbooks e atualizar distribuição.
- **Aceite:** Janela de observação acordada sem chamadas ao legado; instalação final e restore testados; escopo final sem pendências ou declaração explícita de produto híbrido, sem marcar conversão integral como concluída.
