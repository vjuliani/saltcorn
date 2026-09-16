# saltcorn-go — contratos (GO-006)

Contratos OpenAPI da migração ([docs/migracao-go/](../../docs/migracao-go/)), versionados e validados por geração de clientes reais — não apenas lint estático. Relaciona-se a [ADR-0001](../../docs/migracao-go/adr/0001-backend-go-cqrs.md) e [ADR-0003](../../docs/migracao-go/adr/0003-bff-nodejs-permanente.md).

**Recorte desta versão:** registros dinâmicos via CQRS (`CreateRecord`/`ListRecords`/`GetRecord`/`UpdateRecord`/`DeleteRecord`, o exemplo do ADR-0001) e a fronteira de identidade delegada. Não é a superfície completa do domínio — ver `mapeamento-apis-legadas.md` para o que ainda não tem contrato e por quê.

## Estrutura

```
openapi/
  common.yaml        convenções compartilhadas (erro, paginação, Id, DateTime, Decimal, Tenant)
  internal-api.yaml  contrato BFF → Go
  bff-api.yaml       contrato React → BFF
redocly.yaml         config de lint + geração (define as duas APIs e onde cada .d.ts sai)
oapi-codegen.*.yaml  config de geração do cliente Go, um por contrato
fixtures/
  identidade-delegada.md   3 exemplos worked (1 positivo, 2 negativos) de identidade delegada
mapeamento-apis-legadas.md  rotas Node atuais → contrato novo, com lacunas explícitas
gen/                 saída gerada, commitada como evidência de validação (ver abaixo)
  bundled/           specs com $ref resolvido, uma única árvore por contrato
  ts/                tipos TypeScript gerados + um cliente de exemplo que os usa de verdade
  go/                módulo Go próprio com o cliente gerado + um pacote de exemplo com testes
```

## Por que `gen/` é commitado

O critério de aceite de GO-006 é "clientes Go/TS validam ambos os contratos" — a evidência de que isso é verdade é o código gerado existindo, compilando/typechecando, e (do lado Go) tendo testes que passam contra um servidor fake. Não há nenhum consumidor real ainda (BFF e frontend só existem a partir de GO-017/GO-018) — quando existirem, cada um roda sua própria geração a partir destes mesmos contratos, não necessariamente reaproveitando estes arquivos `gen/` literalmente.

## Comandos

A partir deste diretório (`migracao/contracts/`):

```bash
npm install
npm run lint        # redocly lint — é o que a CI usa para "detectar incompatibilidade"
npm run gen:ts       # gera gen/ts/*.d.ts a partir dos dois contratos
npm run typecheck    # tsc --noEmit sobre gen/ts/ (tipos gerados + client-example.ts)
npm run validate     # lint + gen:ts + typecheck, em sequência
```

Geração do cliente Go (requer `oapi-codegen` instalado — `go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest`):

```bash
npm run bundle:internal && npm run bundle:bff   # resolve $ref para um único arquivo por contrato
oapi-codegen -config oapi-codegen.internal-api.yaml gen/bundled/internal-api.json
oapi-codegen -config oapi-codegen.bff-api.yaml gen/bundled/bff-api.json
cd gen/go && go build ./... && go test ./...
```

## Identidade delegada — exemplos positivos e negativos

`fixtures/identidade-delegada.md` documenta os três casos (válido; assinatura inválida; tenant divergente) como requisição/resposta HTTP; `gen/go/example/client_example_test.go` implementa os mesmos três como testes executáveis contra um servidor fake (`httptest`), não apenas como texto.

## Limitação conhecida de geração (registrada, não corrigida nesta tarefa)

O schema `Page` em `common.yaml` é composto via `allOf` nos endpoints que o usam (`internal-api.yaml`). O `oapi-codegen` gera, para esse padrão, uma struct anônima em vez de reaproveitar o tipo nomeado `Page` — `gen/go/example/client_example.go` contorna isso retornando itens e cursor separadamente em vez do tipo de página inteiro. Se essa composição genérica de paginação for usada em mais endpoints, vale revisar se `allOf` é a modelagem certa ou se cada endpoint deveria declarar sua própria resposta de página nomeada.

## CI

`.github/workflows/migracao-contracts-ci.yml` roda `npm run validate` (lint + geração TS + typecheck) e a geração + build + test do cliente Go, escopado a `migracao/contracts/**`.
