# ADR-0010 — Adiar projeções CQRS assíncronas

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [ADR-0001 (backend Go, CQRS lógico)](0001-backend-go-cqrs.md), [GO-016](../TASKS.md#go-016--medir-necessidade-de-projeções-cqrs), [GO-012](../TASKS.md#go-012--implementar-compilador-de-consultas-dinâmicas), [GO-015](../TASKS.md#go-015--integrar-políticas-em-todos-os-caminhos-de-leitura), [GO-014 (outbox)](../TASKS.md#go-014--adicionar-idempotência-e-outbox)

## Contexto

ADR-0001 já deixava a porta aberta e a condição explícita: "CQRS com projeções assíncronas desde o início | Introduz atraso e reconstrução sem benefício comprovado; adotar **apenas** onde um benchmark justificar (ver GO-016, que pode concluir adiando)". GO-016 é essa medição.

O candidato natural a "consulta custosa" no backend hoje é a agregação de `internal/records` (GO-012, autorização estendida em GO-015): `Query.Aggregations` monta, para cada linha da tabela principal, uma subquery correlacionada (`SELECT COUNT(*)/SUM(...)/... FROM <filha> WHERE <fk> = t.id`) sobre uma tabela filha — o padrão "quantos registros relacionados" (ex.: quantos livros cada editora tem). Uma investigação de código (sem escrita, ver histórico de GO-015) já tinha confirmado que `internal/metadata` (GO-011) não cria índice para nenhum campo `FieldKey` — só a `FOREIGN KEY` em si, que no Postgres indexa o lado REFERENCIADO (a chave primária da tabela pai), nunca a coluna que referencia. Toda agregação/join por `FieldKey` no sistema roda hoje sem índice na coluna de junção.

Não existe ainda nenhum consumidor HTTP/CLI/worker real de `internal/records` (confirmado por busca no código) — nenhum dado de produção, nenhuma telemetria de latência real para calibrar a decisão. A medição precisa, portanto, ser um benchmark sintético, com dataset e ambiente registrados (rotina de validação da tarefa), não uma inferência a partir de tráfego que não existe.

## Benchmark

**Ambiente:** Go `1.22.2`; Postgres de teste **isolado e descartável** (container `saltcorn-go016-testdb`, `postgres:16-alpine`, sem tuning especial). **Dataset:** 300 linhas na tabela pai (`authors`), 200 linhas filhas por pai (`books`, chave estrangeira `author`) — 60.000 linhas filhas no total, geradas em massa via SQL (`generate_series`), não uma por uma em Go. **Consulta medida:** listar as 300 linhas pai com `COUNT(*)` das filhas cada (`Query.Aggregations` com `Function: Count`) — a consulta "custosa" do escopo da tarefa. Cada variante foi repetida 5 vezes; reportado min/mediana/max. Implementação do benchmark: `migracao/backend/internal/records/cqrs_bench_test.go` (`TestCQRSBenchmark_AggregationDirectVsIndexVsProjection`, atrás de `SALTCORN_GO_RUN_CQRS_BENCH=1` — não roda na suíte de correção rotineira porque mede tempo, não corretude).

| Variante | Mediana | Aceleração |
| --- | --- | --- |
| Direto, **sem** índice em `books.author` (comportamento de produção hoje) | 777 ms | — (baseline) |
| Direto, **com** índice manual em `books.author` | 43 ms | **18×** sobre o baseline |
| Projeção pré-computada (tabela resumo materializada, leitura simples) | 1,9 ms | **23×** adicional sobre "com índice"; ~410× sobre o baseline |

**Custo de escrita de uma projeção mantida em tempo real** (mesma transação, 1.000 inserções, mediana): sem manutenção de projeção, 914 µs/inserção; com um `UPDATE` de contador a cada inserção, 1,57 ms/inserção — **overhead de ~1,7×** na escrita.

## Decisão

**Adiar a adoção de projeções CQRS assíncronas.** O benchmark mostra que a maior parte do ganho (18× de 300) vem de um fix de schema muito mais barato — **um índice na coluna do campo `FieldKey`** —, não da arquitetura de projeção em si. A projeção completa (pré-computada, lida sem nenhuma agregação em tempo de leitura) ainda ganha mais 23× sobre a versão indexada, mas o valor ABSOLUTO já indexado (43 ms para 300 linhas agregadas) é uma latência aceitável para um endpoint de listagem interativo — não há hoje nenhum tráfego real, SLA ou critério de produto que exija espremer os 41 ms restantes ao custo de introduzir atraso, reconstrução, watermark e um caminho de leitura divergente do de escrita (exatamente os riscos que o critério de aceite desta tarefa pede para testar SE a projeção for adotada, e que confirmam ser desproporcionais ao benefício medido agora).

Isto cumpre o critério de aceite da tarefa pela via que EXECUCAO.md já previa: "Decisão de adiar projeções em GO-016 pode cumprir seu aceite, pois essa tarefa é uma avaliação."

## Recomendação de acompanhamento (fora do escopo desta tarefa)

Registrar como item de schema separado (não parte desta ADR nem desta tarefa): `internal/metadata.AddField` deveria criar um índice B-tree na coluna física sempre que `Type == FieldKey` — é a mudança de menor custo e maior retorno encontrada aqui (18× de aceleração, sem custo de escrita perceptível, sem complexidade operacional nova), e corrige uma lacuna que afeta TODO join/agregação do sistema, não só o cenário deste benchmark. Não implementado nesta ADR porque está fora do escopo declarado de GO-016 ("medir necessidade de projeções", não "otimizar o catálogo") — fica registrado aqui para não se perder, a ser tratado como subtarefa de uma futura revisão de `internal/metadata` ou como task própria do backlog.

## Quando revisitar esta decisão

Não é uma decisão permanente — revisitar quando qualquer uma destas condições se tornar verdadeira, com dados reais em mãos (não outra simulação):

1. **Existir tráfego de produção real** sobre um caminho de leitura agregado (depende de GO-017+ expor `internal/records` via HTTP) e a telemetria (GO-010, já instrumentada) mostrar p95/p99 de latência acima do aceitável mesmo com os índices recomendados aplicados.
2. **A escala real** (linhas por tabela filha, número de tabelas pai por consulta) for ordens de grandeza maior do que os 60.000 registros simulados aqui — o benchmark deveria ser reproduzido na escala real antes de decidir, não extrapolado.
3. **A proporção leitura:escrita** de um caminho específico for alta o suficiente para que o overhead de ~1,7× medido na escrita (se a projeção for mantida sincronamente, evitando complexidade de watermark) seja claramente compensado pelo volume de leituras — hoje essa proporção é desconhecida por não haver tráfego real.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada (por ora) |
| --- | --- |
| Adotar projeção assíncrona já nesta tarefa | O ganho incremental sobre "adicionar um índice" (23× vs. 18×, mas de uma base já rápida em termos absolutos) não justifica introduzir atraso/reconstrução/watermark sem tráfego real para validar contra — mesma lógica de custo/benefício já usada em ADR-0009 para adiar um SDK de observabilidade completo |
| Projeção mantida SINCRONAMENTE na mesma transação (sem ser "CQRS assíncrono" de fato — um contador desnormalizado, não uma projeção com watermark) | Evita staleness/rebuild, mas ainda assim não tem hoje um consumidor de leitura real que precise dela; o índice sozinho já resolve o gargalo medido a custo zero de escrita. Registrada como opção intermediária a considerar ANTES de uma projeção assíncrona completa, se a condição 3 acima se confirmar no futuro |
| Não medir nada e decidir por intuição | Contradiz diretamente o critério de aceite ("ADR decide adotar ou adiar **com benchmark**") e o padrão de rigor já estabelecido nas tarefas anteriores (nunca declarar uma conclusão de desempenho sem número reproduzido) |

## Consequências

- Nenhuma mudança de código de produção nesta tarefa — `internal/records`/`internal/metadata` permanecem como estavam depois de GO-015. O benchmark (`cqrs_bench_test.go`) fica no repositório, atrás de uma variável de ambiente, como o mecanismo reproduzível para revisitar esta decisão (mesmo espírito do `npm run bench:render` de GO-002: um benchmark permanente, não um número descartado).
- "Leitura após escrita usa o banco primário" (ADR-0001, GO-015) continua valendo sem ressalva: sem projeção adotada, não há caminho de leitura alternativo/assíncrono a se preocupar em manter consistente.
- Um índice em cada `FieldKey` (recomendação acima) é um candidato de baixo risco para uma tarefa futura pequena — não bloqueia nem depende de GO-016 continuar aberta.
