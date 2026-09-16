# ADR-0009 — Observabilidade com padrões abertos, sem SDK externo

- **Status:** Aceito
- **Data:** 2026-09-16
- **Relaciona-se com:** [ADR-0001 (backend Go)](0001-backend-go-cqrs.md), [GO-010](../TASKS.md#go-010--instrumentar-observabilidade)

## Contexto

ADR-0001 já previa este trabalho: "Observabilidade adicional dedicada (logs estruturados, métricas, tracing HTTP/SQL/jobs — GO-010) para que uma operação seja rastreável entre os dois runtimes" e reservava o pacote `internal/platform/telemetry` na convenção de diretórios (§"Decisão", item "Todo código novo é criado sob..."). O que faltava decidir era COM QUE tecnologia — um SDK de tracing/métricas completo (OpenTelemetry, cliente Prometheus) ou uma implementação mínima própria.

## Decisão

`internal/platform/telemetry` implementa logs estruturados, métricas e correlação de trace **sem depender de um SDK externo** (nem OpenTelemetry, nem um cliente Prometheus), usando só a biblioteca padrão do Go (`log/slog`, `net/http`) e dois formatos abertos e amplamente suportados:

1. **Trace/correlação:** o header padrão [W3C Trace Context](https://www.w3.org/TR/trace-context/) (`traceparent: 00-{trace-id}-{span-id}-{flags}`), gerado/analisado à mão em `trace.go`. Qualquer coletor real (Jaeger, Grafana Tempo, o futuro BFF Node.js) entende esse formato nativamente — não é um formato proprietário deste backend.
2. **Métricas:** o [formato de exposição de texto do Prometheus](https://prometheus.io/docs/instrumenting/exposition_formats/), escrito à mão em `metrics.go` (`Counter`, `Histogram`, `GaugeFunc`, `Registry.WriteText`), exposto em `GET /metrics`. Qualquer Prometheus real faz scrape sem exigir um cliente Go específico.
3. **Logs:** `log/slog` (biblioteca padrão desde Go 1.21) em JSON, com um `ReplaceAttr` que redige por nome de atributo (ver "Redação por nome de atributo" abaixo).

## Por que não um SDK completo

- **Custo de dependência:** o mesmo cuidado já tomado com `pgx`/`jwt`/`otp` (GO-005/007/008) — a versão mais recente de `go.opentelemetry.io/otel` e do cliente oficial `github.com/prometheus/client_golang` tende a exigir uma toolchain Go mais nova do que a `1.22` fixada neste módulo, ou traz uma árvore de dependências transitivas desproporcional ao que esta fundação precisa agora.
- **Sem tráfego de produção real ainda:** a fundação não tem nenhum consumidor real de métricas/traces (nenhum Prometheus, Jaeger ou Grafana configurado neste ambiente) — introduzir a complexidade operacional de um SDK completo (exporters, samplers, contextos de propagação mais ricos) sem um coletor real para validar contra é comprar complexidade sem poder comprová-la.
- **Formatos abertos preservam a saída:** como trace e métricas usam formatos padrão, adotar um SDK completo depois (quando houver tráfego real e um coletor definido) é uma troca de implementação por trás da mesma interface (`telemetry.LoggerFor`, `telemetry.Middleware`, `Registry.Handler()`), não uma reescrita dos pontos de instrumentação espalhados pelo código.

## Redação por nome de atributo, não por conteúdo

O critério de aceite "sem registrar tokens ou dados sensíveis" é garantido de duas formas complementares, nenhuma delas uma convenção que depende de quem escreve cada linha de log lembrar:

1. **`NewHandler`** (log): todo atributo cujo NOME contenha um trecho de uma lista fixa (`token`, `password`, `senha`, `secret`, `authorization`, `cookie`, `totp_secret`) tem o VALOR substituído por `"REDACTED"` no handler — não é possível logar um campo `api_token` acidentalmente sem que ele seja redigido, não importa o call site.
2. **`internal/platform/database`** (SQL): nunca loga o texto da consulta nem os parâmetros, e nunca loga a mensagem crua do driver — só um resultado CLASSIFICADO (`"ok"`, `"error"`, `"canceled"`, `"deadline_exceeded"`). Isso existe porque uma mensagem de erro do Postgres pode ecoar de volta um valor de linha (ex.: `duplicate key value violates unique constraint ... Detail: Key (token_hash)=(...) already exists`) — redação por nome de atributo não pega isso, porque o valor sensível está dentro do TEXTO de uma mensagem de erro, não isolado num atributo com nome reconhecível.

## Controle de cardinalidade por desenho, não por convenção

Toda métrica declara seu conjunto de labels UMA VEZ, na criação (`NewCounter`/`NewHistogram`) — não é possível anexar uma label nova em tempo de execução. Nenhuma métrica usa tenant, ator, ou qualquer valor de cardinalidade não-limitada como label:

- `http_requests_total{method, route, status_class}` — `route` é um nome lógico curado (ex.: `"tenant_records"`), nunca `r.URL.Path` bruto (que contém o tenant).
- `sql_transactions_total{result}`, `job_runs_total{result}` — `result` é um conjunto fixo e pequeno (`"ok"`, `"error"`, `"skipped_not_owner"`, ...).
- Granularidade por tenant/ator fica nos **logs estruturados** (via `LoggerFor`, que inclui tenant/ator quando presentes no contexto) — logs não agregam em séries temporais, então não sofrem o mesmo problema de explosão de cardinalidade que uma métrica com uma label de alta cardinalidade causaria num backend de séries temporais real.

## "RPC" do critério de aceite

O escopo da tarefa lista "HTTP/SQL/jobs/RPC". Não existe hoje uma camada de RPC binária separada no backend Go — a comunicação entre BFF (GO-017, ainda não implementado) e o backend Go é HTTP/JSON (ADR-0003), e o host de plugins JS com sua própria fronteira RPC é GO-022 (também não implementado). `telemetry.Middleware`, aplicado à API interna HTTP do backend Go, é o que efetivamente cobre essa comunicação entre serviços hoje — quando GO-022 (host de plugins) existir, deve reusar `telemetry.LoggerFor`/um novo bundle de métricas análogo a `HTTPMetrics`, não inventar um mecanismo de correlação próprio.

## Alternativas consideradas e rejeitadas

| Alternativa | Por que rejeitada |
| --- | --- |
| SDK OpenTelemetry completo | Custo de dependência desproporcional sem coletor real para validar contra (ver acima); pode ser adotado depois atrás da mesma interface, já que o formato de trace (W3C) é o mesmo que o OTel usa por padrão |
| Cliente Prometheus oficial (`client_golang`) | Mesma justificativa de custo/dependência; o formato de exposição de texto que ele produziria é o mesmo que `Registry.WriteText` escreve à mão |
| ID de correlação em formato proprietário (ex.: um UUID simples num header `X-Request-Id`) | Rejeitado a favor do padrão W3C Trace Context: mesmo formato de trace-id/span-id que ferramentas reais de tracing já entendem, sem exigir tradução quando um coletor real for adotado |
| Redação por varredura de conteúdo (regex sobre o valor, não o nome do atributo) | Mais custosa e menos confiável (falsos negativos para segredos sem um padrão reconhecível, falsos positivos para texto legítimo que pareça um segredo); a redação por nome cobre o caso dominante (alguém loga um atributo cujo nome já entrega que é sensível) com custo previsível |

## Consequências

- Se/quando um coletor real (Prometheus, Jaeger) entrar em operação, a migração para um SDK completo, se decidida, troca a implementação interna de `telemetry` mantendo as mesmas assinaturas públicas usadas pelos call sites (`cmd/server`, `cmd/worker`, `internal/platform/database`) — não é um trabalho de instrumentar tudo de novo.
- Novas métricas devem seguir o mesmo padrão de labels curadas e documentadas na criação — adicionar uma métrica com uma label de tenant/ID livre é um erro de revisão a pegar, não algo que o tipo `Counter` impede sozinho (ele impede *mudar* o conjunto de labels em runtime, não impede alguém declarar uma label de alta cardinalidade de propósito).
- `/healthz` e `/readyz` permanecem deliberadamente fora de `telemetry.Middleware` — probes de alta frequência não devem virar ruído de log nem inflar as métricas de requisição HTTP "reais".
