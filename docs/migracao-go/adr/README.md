# Decisões de arquitetura (ADRs) — GO-003

Relaciona-se à tarefa [GO-003](../TASKS.md#go-003--registrar-decisões-de-arquitetura). Histórico de execução em [execucoes/GO-003.md](../execucoes/GO-003.md). Cada ADR cita evidência de código (via [matriz de capacidades GO-001](../inventario/GO-001-matriz-capacidades.md)) e do [plano de arquitetura](../README.md); nenhuma decisão aqui contradiz o README — este conjunto formaliza e detalha o que o README já propõe, com limites, custos e critérios de saída explícitos.

## Índice

1. [ADR-0001 — Backend Go modular com CQRS lógico](0001-backend-go-cqrs.md)
2. [ADR-0002 — Frontend React + SB Admin 2](0002-frontend-react-sbadmin2.md)
3. [ADR-0003 — BFF Node.js permanente e separado do domínio](0003-bff-nodejs-permanente.md)
4. [ADR-0004 — Estratégia de bancos: Postgres primeiro; SQLite e mobile em etapa posterior](0004-estrategia-de-bancos.md)
5. [ADR-0005 — Política de extensões: classificação de plugins e host JS temporário](0005-politica-de-extensoes.md)
6. [ADR-0006 — Critérios de retirada do backend legado Node e do host de plugins](0006-retirada-do-legado.md)
7. [ADR-0007 — Sessão/cookies do BFF](0007-sessao-cookies-bff.md)
8. [ADR-0008 — Roteamento de corte e ownership de escrita](0008-roteamento-de-corte-e-ownership.md)
9. [ADR-0009 — Observabilidade com padrões abertos, sem SDK externo](0009-observabilidade-sem-sdk-externo.md)
10. [ADR-0010 — Adiar projeções CQRS assíncronas](0010-projecoes-cqrs.md)
11. [ADR-0011 — Sync versionado e runtime mobile preservado](0011-sync-versionado-offline.md)

## Matriz de limites entre as três camadas

As "três camadas" da arquitetura final são **backend Go**, **BFF Node.js** e **frontend React**. A tabela abaixo consolida, lado a lado, o que cada uma faz e explicitamente não faz — cada célula é rastreável ao ADR correspondente.

| Responsabilidade | Backend Go ([ADR-0001](0001-backend-go-cqrs.md)) | BFF Node.js ([ADR-0003](0003-bff-nodejs-permanente.md)) | Frontend React ([ADR-0002](0002-frontend-react-sbadmin2.md)) |
| --- | --- | --- | --- |
| Regras de negócio e validação | **Sim** — única fonte de verdade | Não — nunca decide, só exibe o que o Go retorna | Não |
| Autorização (papel/ownership/RLS) | **Sim**, revalidada em todo comando/query | Não decide sozinho; identidade delegada verificada pelo Go | Não |
| Persistência / acesso a banco de domínio | **Sim** | **Não** — proibido explicitamente | Não |
| Sessão de navegador / cookies / CSRF | Não | **Sim** | Não (delega ao BFF) |
| Renderização de HTML/UI | Não | Não (compõe dados, não HTML de página) | **Sim** |
| Execução de plugins de domínio (JS arbitrário) | Não (delega ao host temporário, [ADR-0005](0005-politica-de-extensoes.md)) | **Não — proibido explicitamente**, mesmo sendo Node.js | Não |
| API pública versionada para integrações externas | **Sim** | Não (integrações não passam pelo BFF) | Não |
| Processo/build/deploy | Próprio (Go) | Próprio (Node.js) | Próprio (bundler frontend) |
| É retirado ao final da migração? | Sim, por capacidade/tenant ([ADR-0006](0006-retirada-do-legado.md)) — refere-se ao **backend legado Node**, não ao Go novo | **Não — é permanente**, parte da solução final | Não é "retirado"; substitui a renderização legada progressivamente |

## Custos operacionais consolidados

Cada ADR detalha seu próprio custo; o ponto consolidado é que a arquitetura final opera **três processos/pipelines independentes** (backend Go, BFF Node.js, frontend) mais um **quarto processo temporário** (host de plugins JS, [ADR-0005](0005-politica-de-extensoes.md)) durante a transição — contra o processo único de hoje. Isso não é um efeito colateral aceitável a ignorar: observabilidade (GO-010), autenticação entre serviços, timeouts/circuit-breakers e um executor único de migrations são pré-requisitos, não itens opcionais, exatamente porque a superfície operacional cresce.

## Estratégia de bancos e política de extensões

Tratadas em ADRs dedicados porque são decisões técnicas com critérios de aceite próprios, não apenas escopo do backend:

- [ADR-0004](0004-estrategia-de-bancos.md): Postgres primeiro; SQLite/mobile em F5, sem presumir equivalência SQL entre os dois adapters.
- [ADR-0005](0005-politica-de-extensoes.md): classificação de plugins (nativo Go / compatibilidade Node temporária / bloqueador) e host JS temporário isolado, separado do BFF.

## Retirada do legado

[ADR-0006](0006-retirada-do-legado.md) define critérios de saída **separados e verificáveis** para o backend legado Node e para o host de plugins — e deixa explícito que o BFF Node.js **não** tem critério de retirada, por ser parte permanente da arquitetura.

- [ADR-0012 — Instalação e recuperação self-hosted](0012-distribuicao-self-hosted.md)
