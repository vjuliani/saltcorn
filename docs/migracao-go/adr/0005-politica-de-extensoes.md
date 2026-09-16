# ADR-0005 — Política de extensões: classificação de plugins e host JS temporário

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [README §3 "Compatibilidade e segurança da transição"](../README.md#3-compatibilidade-e-segurança-da-transição), [ADR-0001 (backend Go)](0001-backend-go-cqrs.md), [ADR-0003 (BFF)](0003-bff-nodejs-permanente.md), [ADR-0006 (retirada do legado)](0006-retirada-do-legado.md), [matriz de capacidades GO-001 §2.4](../inventario/GO-001-matriz-capacidades.md)

## Contexto

GO-001 documenta duas capacidades que dependem de execução de JavaScript arbitrário e não têm equivalente Go direto:

1. **Carregador de plugins** (`plugins-loader/plugin_installer.ts`): executa `npm install` real em runtime (`child_process.spawn`), com dependências nativas e scripts de `postinstall` arbitrários; o contrato de plugin (`types`/`viewtemplates`/`actions`/`fieldviews`/`authentication`) pressupõe objetos e funções JS carregados em runtime V8.
2. **Motor de expressões** (`models/expression.ts`): usa `vm2` no servidor — projeto **descontinuado, com histórico de escape de sandbox** — e `vm.runInNewContext`/`vm-browserify` (sandbox mais fraco ainda) no bundle mobile. O contexto exposto às fórmulas de usuário tem acesso total a `Table`, `File`, `View`, `User` e `db`, não é um sandbox de funções puras.

Sem inventário de plugins de terceiros realmente instalados em produção (não existe neste checkout — premissa em aberto da fase 0), não é possível fechar hoje a lista completa de bloqueadores por plugin.

## Decisão

1. **Classificar toda capacidade dependente de JS** em uma de três categorias, sem aceitar perda silenciosa (README §3 item 1): **nativo Go**, **compatibilidade Node temporária** ou **bloqueador de migração**.
2. **Plugins fixos do núcleo** (`@saltcorn/base-plugin`, `@saltcorn/sbadmin2`) são tratados como **parte do produto**, não como plugins de terceiro — são portados nativamente para Go/React, não hospedados no host temporário.
3. **Plugins de terceiro e o motor de expressões** que dependem de execução JS ficam atrás de um **host temporário separado do BFF Node.js**, com:
   - RPC versionado e capacidades explícitas (não acesso irrestrito).
   - Isolamento de processo/sistema operacional — **"simples uso de VM não constitui toda a fronteira de segurança"** (README, citação direta — rejeita explicitamente continuar confiando só em `vm2`/`vm`).
   - Limites de memória, CPU e tempo por execução.
   - Sem credenciais irrestritas de banco (o host JS não recebe acesso amplo ao banco de domínio).
4. Plugins que dependem de transações ou objetos internos do domínio ficam no **caminho legado Node** até serem adaptados — não entram no host temporário "quebrados", ficam onde já funcionam até terem um plano de adaptação.
5. O **BFF Node.js não executa plugins de domínio** (reafirmação de [ADR-0003](0003-bff-nodejs-permanente.md)) — mesmo sendo ambos processos Node.js, host de plugins e BFF são sistemas com fronteiras de confiança diferentes e não devem ser fundidos por conveniência de linguagem.

## Escopo desta camada

**Dentro do host temporário:** execução de JS de plugins de terceiro não portados; avaliação de expressões/fórmulas de usuário até que uma alternativa nativa (ou motor JS embutido em Go) seja validada.

**Fora do host temporário (fica em Go):** plugins fixos do núcleo; qualquer plugin cuja capacidade tenha sido classificada e portada como "nativo Go".

**Fora de escopo desta ADR:** a escolha técnica final entre "motor JS embutido em Go" (ex.: um interpretador JS em Go) e "redesenho da linguagem de fórmulas" — fica para o ADR/decisão a produzir depois da prototipagem de GO-004, citada aqui como dependência, não resolvida por este documento.

## Custos operacionais

- Infraestrutura extra: processo isolado por capacidade/tenant, sandboxing de SO, quotas de memória/CPU/tempo — isso é operação nova, não uma biblioteca a mais.
- Overhead de RPC entre o backend Go e o host JS precisa ser medido (custo da bridge) antes de comprometer qualquer prazo — é exatamente o objetivo de GO-004.
- Inventário de plugins por versão precisa ser mantido vivo (não é um levantamento único) enquanto o host temporário existir, porque a classificação de cada plugin pode mudar conforme mais capacidades são portadas.
- `vm2` descontinuado é um risco de segurança **ativo hoje**, não apenas um obstáculo de migração — qualquer decisão de manter o motor de expressões como está por mais tempo do que o necessário carrega esse custo, independente da migração para Go.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| Portar todos os plugins nativamente antes do piloto | Inviável sem inventário de plugins realmente usados em produção (não existe neste checkout) |
| Continuar usando apenas `vm2`/sandboxing simples como fronteira de segurança | Rejeitado explicitamente no README: "simples uso de VM não constitui toda a fronteira de segurança" |
| Fundir o host de plugins com o BFF Node.js (ambos são Node, reduz um processo) | Rejeitado: mistura fronteiras de confiança distintas — o BFF nunca deve executar código de plugin de domínio, mesmo por conveniência |

## Consequências e riscos

- Divergência de expressões JS, datas e valores nulos é risco tabelado no README §5 — mitigação: corpus de fixtures e comparação por expressão, host temporário para os casos incompatíveis.
- Plugins dependentes de internals do domínio são risco tabelado no README §5 — mitigação: inventário por plugin/versão, decidindo entre porta nativa, adapter ou bloqueio de corte por aplicação.
- O host temporário é, por definição, temporário: seus critérios de retirada estão em [ADR-0006](0006-retirada-do-legado.md), não neste documento.
