# GO-004 — Protótipo de fronteira RPC Go↔Node

Código de suporte ao relatório [GO-004-relatorio-fronteira-rpc.md](../GO-004-relatorio-fronteira-rpc.md). **Não é código de produto** — é um experimento isolado e descartável (ver nota em [execucoes/GO-004.md](../../execucoes/GO-004.md) sobre por que fica fora de `migracao/`).

## O que é

- `host.cjs`: processo Node que avalia expressões/callbacks usando `vm2` (a mesma biblioteca de `packages/saltcorn-data/models/expression.ts`), lendo requisições em JSON (uma por linha) via stdin e respondendo via stdout. Tem um canal de callback bidirecional (`callback_request`/`callback_response`) para simular uma expressão que precisa consultar o "banco" do lado Go durante a avaliação.
- `caller.go`: processo Go que sobe `host.cjs` como subprocesso, envia uma amostra de 11 casos (fórmulas, callbacks nomeados, um caso negativo, e chamadas dependentes de banco) e dois benchmarks de latência (200 chamadas puras vs. 200 chamadas com 1 callback de banco, com aquecimento descartado). Serve um "banco" em memória (`fakeDB`) para responder aos callbacks.
- `results.json`: saída da última execução (dados brutos usados no relatório).

## Como rodar

Requer Go ≥1.22 e Node (o repositório principal precisa ter `npm install` rodado ao menos uma vez, para `packages/saltcorn-data/node_modules/vm2` existir — `host.cjs` referencia esse caminho diretamente em vez de ter sua própria cópia, para usar exatamente a mesma versão de `vm2` que a produção).

```bash
cd docs/migracao-go/prototipos/GO-004-fronteira-rpc
go run caller.go
```

Ou compilar antes (não versionar o binário resultante):

```bash
go build -o /tmp/go004-caller caller.go
/tmp/go004-caller
```

`host.cjs` não é chamado diretamente — `caller.go` o invoca como subprocesso (`node host.cjs`), assumindo que `node` está no `PATH`.
