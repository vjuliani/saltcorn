# GO-004 — Relatório: prototipagem de compatibilidade de extensões e expressões

Relaciona-se à tarefa [GO-004](../TASKS.md#go-004--prototipar-compatibilidade-de-extensões-e-expressões), à [matriz de capacidades GO-001 §2.4](../inventario/GO-001-matriz-capacidades.md#24-extensões-expressões-e-automação) e ao [ADR-0005 (política de extensões)](../adr/0005-politica-de-extensoes.md). Histórico de execução em [execucoes/GO-004.md](../execucoes/GO-004.md). Código do protótipo em [prototipos/GO-004-fronteira-rpc/](GO-004-fronteira-rpc/).

**Commit de referência:** inclui GO-001/GO-002/GO-003 integrados (ADR-0005 já define a forma-alvo do host temporário; este relatório testa essa forma na prática, em escala reduzida).

## 1. Metodologia

Construímos um protótipo mínimo da fronteira RPC entre um "lado Go" e um "host Node" (não o backend Go real — GO-005 ainda não existe; ver nota de escopo abaixo):

- **Host Node** (`host.cjs`): avalia expressões e callbacks usando `vm2` — a mesma biblioteca de `packages/saltcorn-data/models/expression.ts` — recebendo requisições em JSON por stdin e respondendo por stdout. Tem um canal de callback bidirecional para simular uma expressão que precisa consultar dados durante a avaliação (`Table.findOne`), resolvido pelo lado Go, sem que o host receba credenciais de banco (coerente com ADR-0005).
- **Caller Go** (`caller.go`): sobe o host como subprocesso, envia 11 casos representativos (fórmulas, callbacks nomeados, um caso negativo, chamadas dependentes de "banco") e dois benchmarks de 200 chamadas cada, com 30 chamadas de aquecimento descartadas antes de cada medição (sem isso, o primeiro benchmark absorve o custo de warm-up do V8/vm2 e distorce a comparação — descoberto na primeira rodada, corrigido antes do resultado final).
- **"Banco"**: um mapa em memória do lado Go (`fakeDB`), não Postgres real — ver limitação na seção 7.

Nada disso é código de produto. Fica em `docs/migracao-go/prototipos/`, fora de `migracao/`, porque é uma exploração isolada (protocolo de execução: "Protótipos podem preceder dependências apenas em escopo isolado... não habilitam integração ou conclusão da tarefa dependente").

## 2. Casos executados

| # | Caso | Categoria | Resultado | Observação |
| --- | --- | --- | --- | --- |
| 1 | `row.price * row.qty` | Fórmula aritmética | ✅ OK — `59.7` | — |
| 2 | `row.first_name + ' ' + row.last_name` | Fórmula de string | ✅ OK — `"Ada Lovelace"` | — |
| 3 | `user.role_id === 1 ? 'admin' : 'nao-admin'` | Fórmula com contexto de usuário | ✅ OK — `"admin"` | — |
| 4 | `new Date('2026-01-15T00:00:00Z')` | Fórmula retornando `Date` | ✅ OK, mas com perda — resultado veio como `"2026-01-15T00:00:00.000Z"` (string) | Confirma na prática o achado de GO-001: a etapa de "deproxy" (`JSON.parse(JSON.stringify(...))`) usada tanto em produção quanto neste protótipo transforma `Date` em string silenciosamente |
| 5 | `new Date(row.created_at).getFullYear()` | Fórmula de data derivada | ✅ OK — `2024` | Funciona porque o resultado final já é um número, não um `Date` |
| 6 | `typeof Table` | Referência a singleton de domínio | ✅ "suportado" no sentido de não travar — retorna `"undefined"` | **Achado central:** sem o canal de callback explícito, singletons como `Table` simplesmente não existem do lado Node — nenhum erro chamativo, apenas `undefined`, o que é um risco de correção silenciosa se o autor da fórmula original contava com esse acesso |
| 7 | Callback nomeado `sendToast({msg})` | Ação de plugin | ✅ OK — `{"toast":true,"message":"Toast: ola"}` | Chamada **por nome**, não por função serializada (ver seção 4) |
| 8 | Callback nomeado `customType_currency_read({value, attrs})` | Tipo customizado | ✅ OK — `"USD 10.50"` | Mesmo padrão de #7 |
| 9 | Função com closure de variável externa nunca enviada | Caso negativo deliberado | ❌ Falha esperada — `outerCounter is not defined` | Ver seção 4 |
| 10 | `await Table.findOne({name: 'guitars'})` | Plugin com dependência de banco | ✅ OK, via callback — `{"id":1,"name":"guitars","brand":"Fender"}` | Ver seção 5 |
| 11 | `await Table.findOne({name: 'nao-existe'})` | Idem, registro ausente | ❌ Falha correta — `"not found"` propagada do lado Go | O erro do "banco" atravessa a fronteira como erro JS normal, sem tratamento especial necessário |

Dados brutos desta execução: [`GO-004-fronteira-rpc/results.json`](GO-004-fronteira-rpc/results.json).

## 3. Classificação suportado vs. incompatível

| Categoria | Suportado pela fronteira RPC prototipada? | Condição |
| --- | --- | --- |
| Fórmulas puras (aritmética, string, condicional) sobre `row`/`user` serializáveis em JSON | **Sim** | Contexto inteiro cabe em JSON |
| Fórmulas que retornam `Date` | **Sim, com perda silenciosa** | Vira string; qualquer código que espere um objeto `Date` do outro lado quebra sem erro explícito — mesmo comportamento já existente em produção (`vm2`), não uma regressão introduzida pela fronteira |
| Callback de ação/tipo de plugin **por nome** | **Sim** | O nome/identificador é enviado, nunca o código-fonte da função original; a função precisa já estar registrada no host |
| Função serializada com closure do ambiente de origem | **Não, por definição** | Ver seção 4 — nenhuma fronteira de processo consegue transportar o ambiente léxico de uma função, isso não é uma limitação de implementação a corrigir depois |
| Singleton de domínio (`Table`, `File`, `View`, `User`) sem canal de callback explícito | **Não, silenciosamente** | Vira `undefined`; precisa de um canal de callback dedicado por singleton (como o implementado para `Table.findOne`) ou falha de forma imprevisível |
| Plugin com dependência de banco, com canal de callback dedicado | **Sim, funcionalmente** | Testado com uma operação (`findOne`); replicar para cada operação real (`select`, `insert`, `update`, `delete`, `count`...) é trabalho adicional não coberto por este protótipo |

## 4. Caso negativo: por que não se supõe serialização de funções

O caso #9 envia o texto `"function(){ return outerCounter + 1; }"` para o host — uma função cujo comportamento pretendido dependia de uma variável (`outerCounter`) que só existia no ambiente onde a função foi originalmente escrita. O host recria a função a partir do texto (`vm.run("(" + code + ")")`) e a executa; o resultado é `ReferenceError: outerCounter is not defined`, **exatamente o esperado**.

Isso demonstra na prática a premissa de design already registrada em ADR-0005 e na matriz GO-001: uma fronteira de processo (RPC, IPC, o que for) só pode transportar **código autocontido + dados explícitos**, nunca o ambiente léxico/closure de uma função tal como ela existia do lado que a criou. Qualquer plugin cujo comportamento dependa de capturar variáveis externas por closure — em vez de receber tudo via parâmetros/contexto explícito — **não é portável para este modelo de host temporário sem reescrita**. Isso deve entrar na classificação por plugin (README §3 item 1: nativo Go / compatibilidade Node temporária / bloqueador) como um critério de triagem concreto, não apenas teórico.

## 5. Operações transacionais classificadas

| Tipo de operação | Atravessa a fronteira? | Implicação transacional |
| --- | --- | --- |
| Computação pura (sem tocar dados externos) | Sim, uma única chamada, sem callback | Nenhuma — não participa de transação alguma |
| Leitura de dados (`Table.findOne`-like) | Sim, via 1 callback síncrono (do ponto de vista da expressão, que usa `await`) | Se a expressão original roda **dentro** de uma transação Go (ex.: validação de `CreateRecord`, ADR-0001), essa transação precisa ficar aberta enquanto espera a resposta do host — cada callback é uma pausa da transação para uma chamada de processo externo. Isso é aceitável para 1 callback ocasional; **não é gratuito** se uma fórmula fizer múltiplos callbacks encadeados, e cada um soma a latência medida na seção 6 mais qualquer latência real de rede/IPC do host verdadeiro (este protótipo usa um pipe local, não mede rede) |
| Escrita de dados a partir de uma expressão (não testada neste protótipo) | Não testada | Consequência de design, não resultado experimental: uma escrita disparada por uma expressão avaliada no host, sob uma transação aberta do lado Go, precisa do mesmo tratamento de idempotência/outbox já decidido em ADR-0001 para qualquer escrita — não deve ser um caminho de escrita paralelo e não coordenado. Recomenda-se que GO-022/GO-023 tratem "expressão que escreve" como caso à parte, com sua própria validação, não como extensão trivial do callback de leitura aqui prototipado |

**Conclusão prática:** callbacks de **leitura** simples são viáveis dentro de uma transação Go, mediante o custo medido na seção 6. Callbacks de **escrita** não foram prototipados e não devem ser presumidos seguros pelo mesmo mecanismo sem uma decisão própria (idempotência, ordem, rollback do lado Node se a transação Go abortar depois do callback ter efeito colateral observável).

## 6. Custo da bridge medido

Cada benchmark: 200 chamadas cronometradas do lado Go (round-trip completo), após 30 chamadas de aquecimento descartadas (necessário — a primeira versão deste benchmark, sem aquecimento, mediu erroneamente o callback de banco como "mais rápido" que a chamada pura, um artefato de warm-up do V8/vm2, não um resultado real).

Três execuções independentes, mesma máquina, pipe local (sem rede):

| Execução | Pura, média (min–max) | Com 1 callback de banco, média (min–max) | Overhead do callback |
| --- | --- | --- | --- |
| 1 | 2,733 ms (2,138–8,733) | 2,966 ms (2,485–8,306) | +0,233 ms |
| 2 | 2,736 ms (2,160–9,223) | 3,015 ms (2,489–12,714) | +0,279 ms |
| 3 | 2,700 ms (2,059–9,704) | 3,078 ms (2,424–10,750) | +0,378 ms |
| 4 (execução final, dados em `results.json`) | 2,645 ms (2,122–9,229) | 3,216 ms (2,490–12,785) | +0,571 ms |

**Leitura:** o custo dominante por chamada (~2,6–2,7 ms no piso) é a **instantiação de uma `VM` do vm2 por requisição**, não o transporte JSON em si nem o callback — o overhead do callback de banco, quando existe, fica na casa de **0,2–0,6 ms**, pequeno frente ao piso fixo. Isso é uma característica deste protótipo (host local, mesmo processo pai, pipe em memória), **não** uma medição do custo de um host real separado por rede/container, que somaria latência de rede real a cada callback — o protótipo mede a mecânica e a ordem de grandeza do overhead de coordenação, não substitui um benchmark do host de produção (GO-022).

Uma chamada isolada, fora de qualquer aquecimento (caso #1 na tabela da seção 2), levou 162–176 ms — o custo de o processo Node compilar/otimizar o código pela primeira vez. Isso importa operacionalmente: um host de plugins que fica ocioso e recebe uma chamada esporádica paga esse custo a cada vez, a menos que seja mantido "quente" (processo de vida longa, não um subprocesso por chamada) — recomendação concreta para o desenho de GO-022.

## 7. Limitações e lacunas

- **"Banco" em memória, não Postgres real.** O canal de callback foi testado com uma função de busca simples (`fakeDB[name]`), não com uma conexão Postgres de verdade, transação real, nem os tipos de dado que a matriz GO-001 já sinaliza como sensíveis (decimal, datas com timezone, JSON, chaves compostas). O que este protótipo comprova é a **viabilidade mecânica** do callback bidirecional (o protocolo funciona, a latência é mensurável), não a paridade de dados de uma consulta real.
- **Apenas uma operação de banco testada** (`findOne` equivalente). Produção precisa de `select`, `insert`/`update`/`delete` (com as implicações de escrita da seção 5), agregações e transações — nenhuma delas foi prototipada.
- **Sem inventário real de plugins de terceiros** (mesma lacuna registrada em GO-001/GO-003): os "callbacks nomeados" (#7, #8) são simulações escritas para este experimento, não plugins reais extraídos de uma instalação em produção. A classificação da seção 3 é válida para os *padrões* testados, não é uma certificação de que todo plugin real se encaixa neles.
- **Host local (mesmo processo pai, pipe em memória).** Não mede latência de rede real entre processos em máquinas/containers diferentes, nem o custo de isolamento de SO/processo que ADR-0005 exige (sandboxing real, limites de CPU/memória) — este protótipo não tem nenhum isolamento além do que o `vm2` já oferece.
- **`vm2` está descontinuado** (já registrado em GO-001/ADR-0005) — usá-lo aqui foi deliberado, para medir a fronteira RPC com o mecanismo real de produção, não uma recomendação de continuar usando `vm2` no host final.

## 8. Recomendações para as próximas tarefas

1. **GO-022 (host temporário):** manter o host como processo de vida longa, não subprocesso por chamada — o custo de warm-up (162–176 ms na primeira chamada) é proibitivo se pago repetidamente.
2. **GO-022/GO-023:** desenhar o canal de callback de leitura como um mecanismo genérico (não um método por singleton), já que o padrão provou-se simples de implementar e com overhead pequeno para leitura; tratar escrita como um problema separado, com sua própria decisão de idempotência (não coberta aqui).
3. **Classificação de plugins (GO-004 em diante):** usar o critério concreto da seção 4 (depende de closure do ambiente de origem → não portável sem reescrita) como parte do checklist de triagem por plugin, quando o inventário real de produção existir.
4. **Alerta de correção silenciosa:** qualquer expressão que hoje referencia `Table`/`File`/`View`/`User` diretamente (não greppado neste protótipo, mas confirmado como padrão existente em `models/expression.ts` por GO-001) precisa ser identificada antes do corte — o caso #6 mostra que a falta desses singletons não produz um erro ruidoso, produz `undefined`, o que pode mascarar um bug até produção.
