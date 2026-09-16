# ADR-0003 — BFF Node.js permanente e separado do domínio

- **Status:** Aceito (decisão permanente, não uma etapa transitória da migração)
- **Data:** 2026-09-16
- **Relaciona-se com:** [README §2 "BFF"](../README.md#2-arquitetura-proposta), [ADR-0001 (backend Go)](0001-backend-go-cqrs.md), [ADR-0002 (frontend)](0002-frontend-react-sbadmin2.md), [ADR-0005 (extensões)](0005-politica-de-extensoes.md), [ADR-0006 (retirada do legado)](0006-retirada-do-legado.md)

## Contexto

Hoje sessão, CSRF, autenticação e regras de domínio vivem no mesmo processo Express (GO-001 §2.1: `app.js` monta `passport`, `csrf`, `setTenant` e as rotas de domínio na mesma cadeia de middleware). Uma versão anterior deste conjunto de documentos propunha um **BFF web em Go**, no mesmo processo do backend; esta decisão substitui essa proposta por um **BFF Node.js/TypeScript separado**, registrado como a arquitetura final (não intermediária) em README §5 ("Conclusão da migração... O BFF Node.js faz parte da solução final").

## Decisão

O **BFF Node.js/TypeScript é permanente e roda em processo separado** do backend Go, consumindo APIs HTTP/JSON versionadas do domínio. Ele:

- Compõe bootstrap do editor, permissões de interface, metadados e dados para as telas React + SB Admin 2.
- Cuida de sessão/cookies, CSRF, paginação e erros voltados à UI.
- **Não acessa tabelas de domínio diretamente nem concentra regras de negócio** — todo comando/query revalida autorização no backend Go, mesmo que o BFF já tenha decidido mostrar ou esconder algo na interface.
- Usa cliente HTTP/JSON tipado para a API interna Go, com autenticação entre serviços e identidade delegada verificável (ator e tenant); o backend valida credenciais e autorização de forma independente do que o BFF afirma.
- Integrações externas usam a API pública versionada do Go diretamente, não passam pelo BFF; mobile pode ganhar um BFF próprio quando contratos e uso justificarem (decisão a parte, não coberta por este ADR).

Todo código novo do BFF é criado sob `migracao/packages/bff/` (convenção de diretórios do README §2).

## Escopo desta camada

**Dentro:** sessão/cookies, CSRF, composição de bootstrap/telas, tradução de erros do domínio para a UI, timeouts/cancelamento/correlação de traces nas chamadas ao Go.

**Fora (limite explícito com as outras duas camadas):**
- Acesso a banco de domínio ou execução de regra de negócio — [backend Go](0001-backend-go-cqrs.md) é a única fonte de verdade.
- Execução de plugins de domínio — **o BFF Node.js não executa plugins de domínio**; isso é papel do host temporário separado ([ADR-0005](0005-politica-de-extensoes.md)). Apesar de ambos serem Node.js, são dois processos com responsabilidades e ciclos de vida distintos.
- Renderização de HTML — [frontend React](0002-frontend-react-sbadmin2.md) consome o BFF, o BFF não gera HTML de página.

## Custos operacionais

- Processo, build, health check e release **próprios**, distintos tanto do backend Go quanto do frontend — três pipelines de deploy coordenados em vez de um.
- Latência de rede adicional entre BFF e Go em toda requisição de UI — o custo entra explicitamente nos benchmarks (README §2 "BFF") e na distribuição self-hosted (mais um processo para quem hospeda o Saltcorn).
- Indisponibilidade do backend Go deve produzir erro controlado na UI (o BFF precisa de timeout/circuit-breaker, não pode travar esperando indefinidamente).
- Retries de comando exigem a mesma chave de idempotência usada no domínio — uma composição de chamadas no BFF **não constitui transação de domínio**; se o BFF cair no meio de uma composição, não há rollback automático do lado Go.
- Autenticação entre serviços (identidade delegada ator+tenant) é infraestrutura nova a operar e auditar — não é apenas "mais um serviço", é mais uma fronteira de confiança.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| BFF web em Go, no mesmo processo do backend (proposta inicial deste documento) | Substituída por esta decisão; acopla sessão/CSRF de navegador ao processo de domínio, o que esta ADR evita deliberadamente |
| Manter tudo no processo de domínio atual, sem separar um BFF | Mistura autenticação de sessão de navegador com regras de domínio — exatamente o problema que a separação de camadas pretende resolver (ADR-0001) |
| BFF sem identidade delegada verificável (confiar cegamente no BFF) | Rejeitado: backend Go sempre revalida autorização; identidade forjada por header deve ser rejeitada (critério de aceite de GO-009) |

## Consequências e riscos

- O BFF Node.js **não é retirado** quando o domínio terminar de migrar para Go — só o backend legado e o host de plugins têm plano de retirada ([ADR-0006](0006-retirada-do-legado.md)). Tratar "concluir a migração" como "eliminar Node.js" é um erro de leitura desta arquitetura.
- Falha de rede entre BFF e Go é um modo de falha novo que não existia quando tudo rodava em um processo — precisa de estratégia explícita de timeout, retry idempotente e mensagem de erro para o usuário final.
