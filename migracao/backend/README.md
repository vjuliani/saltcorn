# saltcorn-go — backend (fundação)

Módulo Go da migração ([docs/migracao-go/](../../docs/migracao-go/)). Implementa a estrutura definida em [ADR-0001](../../docs/migracao-go/adr/0001-backend-go-cqrs.md): monólito modular com CQRS lógico, três executáveis (`server`, `worker`, `cli`) reutilizando os mesmos serviços internos.

**Estado atual (GO-005 — fundação):** sem regras de domínio. Existe apenas a estrutura de suporte — configuração, health/readiness, encerramento gracioso sem perder trabalho em curso, e CI. Identidade/tenancy, metadados/schema, registros/consultas e as demais capacidades entram nas tarefas seguintes (GO-007 em diante), sobre esta mesma fundação.

## Estrutura

```
cmd/
  server/   processo HTTP (rotas de domínio entram a partir de GO-007)
  worker/   processo de background (automação real entra em GO-024/GO-025)
  cli/      linha de comando (paridade com o saltcorn-cli atual, gradual)
internal/
  platform/
    config/    carregamento de configuração por variável de ambiente
    health/    handlers de liveness/readiness reutilizáveis
    shutdown/  rastreador de trabalho em curso para encerramento gracioso
```

Nenhuma dependência externa — apenas biblioteca padrão. `go.mod` fixa a toolchain em `go 1.22`.

## Build, testes e execução

A partir deste diretório (`migracao/backend/`):

```bash
go build ./...
go vet ./...
go test -race -count=1 ./...
```

Rodar cada processo:

```bash
go run ./cmd/server         # escuta em :8090 por padrão
go run ./cmd/worker         # loop de background
go run ./cmd/cli version
go run ./cmd/cli healthcheck   # consulta /healthz do server, se estiver rodando
```

## Configuração

Variáveis de ambiente lidas por `internal/platform/config` (compartilhado pelos três executáveis):

| Variável | Padrão | Descrição |
| --- | --- | --- |
| `SALTCORN_GO_HTTP_ADDR` | `:8090` | Endereço em que `cmd/server` escuta |
| `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS` | `15` | Tempo máximo esperando trabalho em curso terminar antes de forçar a saída |
| `SALTCORN_GO_ENV` | `development` | Rótulo do ambiente (ainda não controla comportamento, só logging) |

## Encerramento gracioso

`cmd/server` e `cmd/worker` capturam `SIGINT`/`SIGTERM`, param de aceitar trabalho novo, e esperam o trabalho já em curso terminar (via `internal/platform/shutdown.Tracker`) antes de sair — dentro do prazo de `SALTCORN_GO_SHUTDOWN_TIMEOUT_SECONDS`. Se o prazo estourar, o processo registra um aviso e sai mesmo assim; isso é uma decisão operacional explícita, não um bug — ver `shutdown.Tracker.Drain`.

## CI

`.github/workflows/migracao-backend-ci.yml` roda `go build`, `go vet` e `go test -race` neste módulo a cada push/PR que toque `migracao/backend/`.
