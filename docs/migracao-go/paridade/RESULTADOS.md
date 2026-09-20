# GO-033 — Resultado da matriz

Execução UTC: 2026-09-20T11:14:42.223757+00:00; commit `5eeca039f163fa55563ab32e28dffa2d88e76fdc`.
SHA-256 dos inputs: `3fd8c410627de59d2a944fd7103240538830aa9e7b10028c8113f5ddd99ecf60`. Logs e hashes em `report.json`.

**PASS de suíte não significa paridade integral nem autorização de canário.**

| Suíte | Resultado | Testes aprovados |
| --- | --- | --- |
| contracts | PASS | ver log |
| pluginhost | PASS | 10 |
| bff | PASS | 50 |
| frontend | PASS | 40 |
| backend | PASS | 471 |
| mobile | PASS | 12 |
| web | PASS | 12 |
| distribution | PASS | ver log |

Perfil **pilot**: BLOQUEADO; 39/48 capacidades com lacuna/evidência ausente.

Perfil **integral**: BLOQUEADO; 60/70 capacidades com lacuna/evidência ausente.

| ID / capacidade | Piloto | Resultado | Evidência executada | Cobertura / lacuna |
| --- | --- | --- | --- | --- |
| CAP-001 — Autenticação local (usuário/senha, bcrypt) | sim | PARTIAL | backend, bff: internal/identity | Hash bcrypt, identidade delegada e sessão de operador. **Lacuna:** Login/senha de usuário final não integrado ao BFF; recuperação e políticas de conta pendentes |
| CAP-002 — Autenticação via token de API (Bearer) | não | PARTIAL | backend: internal/identity | Primitivas de identidade/token. **Lacuna:** Compatibilidade da API pública e tokens legados sem fluxo HTTP completo |
| CAP-003 — TOTP / MFA | não | PARTIAL | backend: internal/identity | Geração e validação TOTP. **Lacuna:** Cadastro, desafio e políticas MFA não integrados à interface |
| CAP-004 — Login social / OAuth via plugin | não | BLOCKED | levantamento no inventário GO-001 | Levantamento GO-029 de métodos externos. **Lacuna:** Inventário/código de plugins OAuth/SAML de produção ausentes |
| CAP-005 — Handoff de sessão para app móvel (JWT curto) | não | PARTIAL | backend, mobile: internal/identity, internal/sync | Identidade delegada e sync autenticado. **Lacuna:** Handoff JWT do app Capacitor não exercitado em dispositivo |
| CAP-006 — Sessão HTTP (cookie + store) | sim | PARTIAL | bff, distribution | Cookie, CSRF, expiração e sessão de operador. **Lacuna:** Store de sessão em memória; login final e durabilidade multi-instância pendentes |
| CAP-007 — CSRF | sim | PASS | bff | CSRF, origem, cookie e negação de mutação sem token |
| CAP-008 — Rate limiting de login | sim | BLOCKED | levantamento no inventário GO-001 | Inventário de limitadores legados. **Lacuna:** Fluxo de login de usuário final e limitadores sem prova de paridade |
| CAP-009 — Resolução de tenant (subdomínio) | sim | PARTIAL | backend, bff: internal/platform/tenancy | Resolução/validação de tenant e contexto. **Lacuna:** DNS/proxy de subdomínios reais não ensaiados |
| CAP-010 — Propagação de contexto de tenant/transação | sim | PASS | backend: internal/platform/database | Contexto transacional, reutilização de conexão e isolamento entre tenants |
| CAP-011 — Defesa contra "tenant drift" (sessão de um tenant usada em outro) | sim | PASS | backend, bff: internal/identity, internal/platform/database | Negativos de identidade delegada, papel e tenant |
| CAP-012 — Autorização por papel (role_id) | sim | PASS | backend, bff: internal/identity, internal/records, internal/views | Negativos de leitura/escrita e publicação por papel |
| CAP-013 — Ownership por campo | sim | PASS | backend: internal/identity, internal/records | Ownership por campo e autorização de registros |
| CAP-014 — Ownership por fórmula JS | sim | PARTIAL | backend, pluginhost: internal/expression, internal/identity | Expressões com leitura autorizada e primitivas de ownership. **Lacuna:** Fórmulas arbitrárias de ownership não ligadas ao HTTP |
| CAP-015 — RLS nativa Postgres (políticas `CREATE POLICY`, GUC por conexão) | sim | PASS | backend: internal/platform/database | RLS real com conexão NOSUPERUSER/NOBYPASSRLS e usuários concorrentes |
| CAP-016 — Filtragem de leitura em JS (fallback SQLite / RLS desabilitada) | não | PARTIAL | backend: internal/platform/sqlite | Isolamento de arquivos SQLite por tenant. **Lacuna:** Fallback JS/ownership SQLite web não portado |
| CAP-017 — Tabelas/campos/relações dinâmicos | sim | PARTIAL | backend: internal/metadata, internal/records | DDL, campos básicos, concorrência e consulta. **Lacuna:** Relações/constraints arbitrárias do legado não cobertas integralmente |
| CAP-018 — Constraints de tabela definidas pelo usuário | sim | PARTIAL | backend: internal/metadata | Validação e constraints básicas. **Lacuna:** Catálogo completo de constraints definidas pelo usuário não portado |
| CAP-019 — Config key/value (global e por tenant) | sim | PARTIAL | backend, distribution: internal/config, internal/installation | Config versionada por tenant, CLI, backup e restore. **Lacuna:** Configuração global legada completa sem equivalência comprovada |
| CAP-020 — Discovery de tabelas existentes (reverse engineering) | não | BLOCKED | levantamento no inventário GO-001 | Inventário GO-001 de discovery. **Lacuna:** Reverse engineering de bancos existentes não implementado |
| CAP-021 — Facade de banco (`db/index.ts`) | sim | PARTIAL | backend: internal/platform/database, internal/platform/sqlite | Contrato transacional compartilhado. **Lacuna:** Domínios ainda acoplados a pgx impedem facade integral PG/SQLite |
| CAP-022 — Adapter Postgres | sim | PASS | backend: internal/platform/database | Adapter PostgreSQL e transações |
| CAP-023 — Adapter SQLite | não | PARTIAL | backend: internal/platform/sqlite, internal/installation, internal/sync | Registros/outbox/sync e recuperação SQLite. **Lacuna:** Identidade/views/worker/web SQLite não disponíveis |
| CAP-024 — Sanitização de identificadores (`sqlsanitize`/`sqlsanitizeAllowDots`) | sim | PASS | backend: internal/platform/database, internal/metadata | Identificadores escapados e ataques de nomes nas fixtures |
| CAP-025 — DSL de filtros parametrizados (`mkWhere`) | sim | PARTIAL | backend: internal/records | Filtros parametrizados e autorização. **Lacuna:** DSL relacional completa do legado não demonstrada |
| CAP-026 — Migrations do framework (tabelas `_sc_*`) | sim | PARTIAL | backend, distribution: internal/installation | Instalação limpa, schema 1→2, journal e recuperação PG/SQLite. **Lacuna:** Adoção/upgrade de schema legado existente recusado; GO-034 pendente |
| CAP-027 — Pack (import/export de aplicação) | sim | PARTIAL | backend: internal/pack | Export/import versionado, rollback, remapeamento e repetição. **Lacuna:** Formato não importa diretamente backup legado guitars completo |
| CAP-028 — Loja remota de packs (fetch HTTP) | não | PARTIAL | backend: internal/pack | Cliente remoto e validações de pack. **Lacuna:** Catálogo remoto real e packs de terceiros não ensaiados |
| CAP-029 — Componentes de biblioteca (snippets reutilizáveis) | sim | PARTIAL | backend: internal/library | Componentes versionados, referências e round-trip. **Lacuna:** Integração completa no editor e snippets legados pendente |
| CAP-030 — Log de eventos / auditoria | sim | PARTIAL | backend: internal/platform/outbox, internal/platform/telemetry | Eventos transacionais, idempotência e observabilidade. **Lacuna:** Histórico/auditoria administrativa equivalente ao legado sem prova |
| CAP-031 — Log de erros/crash | sim | PARTIAL | backend: internal/platform/telemetry | Logs/erros estruturados e HTTP. **Lacuna:** Catálogo de crashes e interface administrativa não portados integralmente |
| CAP-032 — Modelo de ML (`Model`/`ModelInstance`) | não | BLOCKED | levantamento no inventário GO-001 | Inventário GO-001 identifica lacuna de ML. **Lacuna:** Model/ModelInstance sem levantamento e implementação equivalentes |
| CAP-033 — Modelo de View (config JSON, execução) | sim | PARTIAL | backend, frontend, web: internal/views | Catálogo, edição, publicação e execução List. **Lacuna:** Execução dos demais templates e páginas não implementada |
| CAP-034 — Viewtemplates nativos (List/Show/Edit/Feed/Filter/ListShowList/Room/WorkflowRoom) | sim | PARTIAL | backend, web: internal/views | Template List com filtros e paginação. **Lacuna:** Show/Edit/Feed/Filter/ListShowList/Room/WorkflowRoom pendentes; formulário P0 do piloto bloqueado |
| CAP-035 — Árvore de layout (`layout.ts`) | sim | PARTIAL | backend, frontend, web: internal/views | Validação de árvore e round-trip. **Lacuna:** Catálogo completo de nós legados e canvas persistente não integrado |
| CAP-036 — Geração de HTML server-side (`@saltcorn/markup`) | sim | PARTIAL | frontend, web | Renderização React, sanitização e contrato de dados. **Lacuna:** Todos os elementos/HTML legado não comparados |
| CAP-037 — Tema SB Admin 2 (server-side hoje) | sim | PARTIAL | frontend, web | Shell SB Admin 2, teclado e responsividade. **Lacuna:** Substituição visual do tema do piloto sem validação da aplicação completa |
| CAP-038 — Builder de views/páginas (React + Craft.js) | sim | PARTIAL | frontend, web | Editor de configuração com save/publish e widget isolado. **Lacuna:** Alterações do canvas Craft.js não persistem por ponte completa ao editor conectado |
| CAP-039 — Páginas e grupos de páginas | não | BLOCKED | levantamento no inventário GO-001 | Inventário GO-001 de páginas/grupos. **Lacuna:** Domínio e UI completos de páginas/grupos não portados |
| CAP-040 — Formulários (`Form`) | sim | PARTIAL | frontend, web | Campos de configuração do editor. **Lacuna:** Formulários finais Edit e validação equivalentes ao legado pendentes |
| CAP-041 — Arquivos: upload/download/miniaturas | sim | PARTIAL | backend: internal/files | Upload/download, limites, permissões e limpeza. **Lacuna:** Miniaturas e todas as variantes de armazenamento legadas não demonstradas |
| CAP-042 — Carregador de plugins (npm install em runtime, `import()` dinâmico) | sim | PARTIAL | backend, pluginhost: internal/pluginhost | Host isolado, capacidades e limites. **Lacuna:** npm dinâmico e todos os plugins legados não portados; host temporário permanece |
| CAP-043 — Contrato de plugin (types/viewtemplates/actions/fieldviews/auth) | sim | PARTIAL | backend, pluginhost: internal/pluginhost, internal/triggers | SDK de ações/tipos e callback autorizado. **Lacuna:** Extensões arbitrárias types/views/fieldviews/auth não cobertas |
| CAP-044 — Plugins fixos (base-plugin, sbadmin2) | sim | PARTIAL | backend, frontend: internal/types, internal/triggers | Tipos básicos, send_email/webhook e shell SB Admin 2. **Lacuna:** Catálogo completo base-plugin e templates não portado |
| CAP-045 — Plugins de terceiro do pack piloto (`@saltcorn/any-bootstrap-theme`, `@saltcorn/flatpickr-date`) | sim | PARTIAL | frontend | Substituição proposta por SB Admin 2 e data HTML5. **Lacuna:** any-bootstrap-theme/flatpickr-date ausentes; aplicação guitars substituída não validada ponta a ponta |
| CAP-046 — Motor de expressões JS (fórmulas de usuário) | sim | PARTIAL | backend, pluginhost: internal/expression, internal/pluginhost | Avaliação JS em processo isolado, timeout e leitura autorizada. **Lacuna:** Host temporário requerido; fórmulas legadas com acesso direto a modelos não equivalentes |
| CAP-047 — Triggers (Insert/Update/Delete/Weekly/Daily/Hourly/Often/Cron/API call/PageLoad/Login/…) | sim | PARTIAL | backend: internal/triggers | Dispatch transacional em testes de domínio. **Lacuna:** HTTP CreateRecord recebe hooks nil; eventos reais não disparam cadeia de automação do piloto |
| CAP-048 — Ações multi-etapa | sim | PARTIAL | backend: internal/triggers | Ações compostas e idempotência no domínio. **Lacuna:** Catálogo de ~29 ações e CRUD por trigger incompleto; sem wiring HTTP |
| CAP-049 — E-mail (ação `send_email`, notificações) | não | PARTIAL | backend: internal/notify, internal/triggers | Fila transacional, interpolação e entrega SMTP de teste. **Lacuna:** Provedor SMTP real e automação originada por HTTP não ensaiados |
| CAP-050 — Webhook (ação `webhook`) | não | PARTIAL | backend: internal/notify, internal/triggers | Entrega HTTP local, idempotência e retries. **Lacuna:** Automação originada por HTTP não ligada ao dispatcher |
| CAP-051 — Motor de workflow (estado, retomada) | sim | PARTIAL | backend: internal/workflow | Estado durável, concorrência e retomada do workflow. **Lacuna:** Waiting/formulário, subworkflow e wiring no processo real pendentes |
| CAP-052 — Mutex distribuído entre nós (workflow) | sim | PASS | backend: internal/workflow | Serialização de avanço concorrente por linha do workflow |
| CAP-053 — Scheduler (tick loop + eleição de líder) | sim | PARTIAL | backend: internal/scheduler, internal/platform/lease | Agendamento persistido, timezone e lease exclusivo. **Lacuna:** Often tem granularidade de minuto; catálogo de ações do worker limitado |
| CAP-054 — Ações registradas por plugin (`state.actions`) | sim | PARTIAL | backend, pluginhost: internal/triggers, internal/pluginhost | Chamadas nomeadas com capacidades explícitas. **Lacuna:** Closures/efeitos e inventário completo de ações de terceiros ausentes |
| CAP-055 — App móvel (shell Capacitor) | não | PARTIAL | mobile, backend: internal/sync | Cliente JS real SQLite → BFF → Go → PG. **Lacuna:** Build/instalação Android/iOS, dispositivos e plugins Capacitor não executados |
| CAP-056 — Builder de app móvel nativo | não | BLOCKED | levantamento no inventário GO-001 | Shell/builder legados preservados. **Lacuna:** Build e assinatura nativos não executados; pipeline externo |
| CAP-057 — Driver SQLite embarcado no mobile | não | PARTIAL | mobile, backend: internal/sync, internal/platform/sqlite | SQLite real no cliente e recuperação de fila/checkpoint. **Lacuna:** Driver Capacitor nativo não executado em Android/iOS |
| CAP-058 — Protocolo de sincronização (cursor, paginação) | não | PARTIAL | mobile, backend: internal/sync | Protocolo 1, snapshot, limite de lote e negação de versão. **Lacuna:** Sem paginação incremental equivalente ao legado; migração de filas legadas bloqueia corte |
| CAP-059 — Resolução de conflitos (ordenação topológica, last-write-wins por campo) | não | PARTIAL | mobile, backend: internal/sync | Conflitos explícitos por versão, draft preservado, IDs repetidos. **Lacuna:** Mudança deliberada frente ao LWW; filas/FKs legadas exigem reconciliação |
| CAP-060 — Execução de upload de sync como processo CLI separado | não | PASS | mobile, backend: internal/sync | Upload transacional HTTP sem subprocesso e replay |
| CAP-061 — Notificações (catálogo + e-mail) | não | PARTIAL | backend, bff, frontend: internal/notify, internal/realtime | Notificações por usuário, e-mail enfileirado e dynamic_update. **Lacuna:** Canais push e integração completa de UI não cobertos |
| CAP-062 — Comunicação em tempo real (Socket.IO: dynamic_update, colaboração por view, stream de logs, progresso de restore, `/datastream`) | não | PARTIAL | backend, bff, frontend: internal/realtime | Socket.IO, reconexão, replay, tenant/destinatário. **Lacuna:** Colaboração, logs, restore e datastream pendentes |
| CAP-063 — Notificações push (registro de dispositivo) | não | BLOCKED | levantamento no inventário GO-001 | Inventário de canais push. **Lacuna:** FCM/APNS/WebPush sem credenciais/implementação equivalente |
| CAP-064 — Roteamento HTTP builder/admin (39 arquivos em `routes/`) | sim | PARTIAL | backend, bff, web: cmd/server | Rotas de editor/registros/views e autenticação delegada. **Lacuna:** 39 grupos de rotas administrativas não substituídos integralmente |
| CAP-065 — API REST genérica sobre tabelas | sim | PARTIAL | backend, bff: cmd/server | HTTP de registros tipado, idempotência e negativos. **Lacuna:** Compatibilidade da API pública legada e integrações externas não comprovada |
| CAP-066 — API interna de introspecção (`scapi.ts`) | não | BLOCKED | levantamento no inventário GO-001 | Inventário de scapi. **Lacuna:** API de introspecção/cloud sem substituição equivalente comprovada |
| CAP-067 — Segurança de cabeçalhos (Helmet/CSP) | sim | PARTIAL | bff, web | Borda HTTP, sanitização de conteúdo e sessão. **Lacuna:** Paridade de Helmet/CSP e política para todas as extensões não demonstrada |
| CAP-068 — CORS | sim | PARTIAL | bff, web | Topologia same-origin no BFF. **Lacuna:** Origens mobile reais e todos os cenários CORS não validados |
| CAP-069 — Mitigação de poluição de protótipo | sim | PARTIAL | backend, bff | Go elimina prototype JS no domínio; Node permanece na borda. **Lacuna:** Não há teste específico de prototype pollution no BFF/host |
| CAP-070 — CLI (`saltcorn`, oclif) | sim | PARTIAL | backend, distribution: internal/installation, cmd/cli | Setup/upgrade/backup/restore/config/serve/login e pacote Linux real. **Lacuna:** CLI geral tenant/user/trigger/mobile-build e outras plataformas pendentes |
