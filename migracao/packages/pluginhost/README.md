# saltcorn-go-pluginhost (GO-022)

Host JS temporário — separado do BFF (`migracao/packages/bff/`) — que avalia expressões/callbacks de plugin sob um processo isolado, com capacidades explícitas, limites reais de memória/tempo e um protocolo de erros tipado. Promove a protótipo `docs/migracao-go/prototipos/GO-004-fronteira-rpc/` a produto — ver [ADR-0005](../../../docs/migracao-go/adr/0005-politica-de-extensoes.md) e [`docs/migracao-go/execucoes/GO-022.md`](../../../docs/migracao-go/execucoes/GO-022.md) para as decisões de escopo completas.

## Por que não `vm2`

O próprio relatório de GO-004 usou `vm2` deliberadamente para medir a fronteira RPC com o mecanismo real de produção, mas registrou explicitamente: "não é recomendação de continuar usando vm2 no host final". `vm2` está descontinuado, com histórico de escape de sandbox. Este host usa o módulo `vm` **nativo** do Node (`vm.Script`/`vm.createContext`) — não porque `vm` seja um sandbox mais forte (não é — ADR-0005 cita `vm.runInNewContext` como "sandbox mais fraco ainda" no contexto mobile), mas porque a ADR é explícita: **"simples uso de VM não constitui toda a fronteira de segurança"**. A fronteira de segurança real deste host é o **PROCESSO** isolado — limite de heap via `--max-old-space-size` (na hora de spawnar, do lado do cliente Go, `migracao/backend/internal/pluginhost`), timeout do lado Go com kill+respawn. A VM é só a camada interna de conveniência (contém a maioria dos laços síncronos sem precisar matar o processo inteiro), nunca a fronteira de confiança.

## Protocolo (`src/protocol.ts`)

JSON, uma mensagem por linha, em stdin/stdout — evolução direta do protocolo prototipado em GO-004 (`docs/migracao-go/prototipos/GO-004-fronteira-rpc/host.cjs`):

- **`eval`** (Go → host): `{id, kind: "expr"|"call", code?, name?, args?, context?, capabilities: Capability[], timeoutMs?}`. `capabilities` é a lista de callbacks que ESTA chamada pode invocar — ADR-0005: "capacidades explícitas, não acesso irrestrito". `null`/ausente é tratado como lista vazia (achado desta tarefa: Go não usa `omitempty` em `Capabilities`, e um `null` sem essa normalização quebrava com um erro de runtime em vez de `capability_denied` — corrigido em `src/host.ts`).
- **`result`** (host → Go): `{id, ok, result?, error?: {code, message}, evalMs}` — `error.code` é um de `runtime_error | capability_denied | invalid_request | timeout | crashed`, nunca uma string solta.
- **`callback_request`** (host → Go) / **`callback_response`** (Go → host): canal de callback **genérico** por `op` (não um global por singleton — recomendação #2 do relatório de GO-004), correlacionado por `corr`. A checagem "esta chamada pode invocar este `op`?" acontece nos DOIS lados (host, em `requestCallback`; cliente Go, em `serveCallback`) — defesa em profundidade, nenhum lado confia cegamente no outro.

## Capacidades implementadas

- **`db.read`**: único callback desta entrega — uma leitura genérica, resolvida do lado Go reaproveitando `internal/records.Rows` com a MESMA autorização por papel já usada em todo o resto do backend (GO-008/012/015). Nenhuma credencial de banco cruza para o processo Node (ADR-0005).
- **Escrita a partir de uma expressão/plugin está FORA de escopo** — o relatório de GO-004 (§5) é explícito: não prototipada, precisa de decisão própria de idempotência/rollback antes de ser considerada segura. Uma expressão que precise escrever fica classificada como bloqueador (`internal/pluginhost.ExpressionCapability` permanece `OwnerLegacy`) até uma tarefa dedicada decidir isso.

## Isolamento testado por regressão deliberada

Três garantias, cada uma provada desabilitando o mecanismo e confirmando falha real antes de restaurar (ver `migracao/backend/internal/pluginhost/client_test.go` e `docs/migracao-go/execucoes/GO-022.md`):

1. **Timeout**: um laço síncrono infinito é interrompido; sem a contenção do lado Go, o processo preso é reaproveitado pela chamada seguinte e produz uma condição de corrida real (confirmado com `go test -race` durante esta tarefa).
2. **Crash**: o processo do host morto (por OOM real via `--max-old-space-size`, ou morto externamente) nunca deixa o cliente Go preso — a chamada em voo recebe `crashed`, e a PRÓXIMA chamada sobe um host novo sozinha.
3. **Acesso proibido**: um callback fora da lista de capacidades concedidas é negado nos dois lados, nunca chega a executar.

## Build, testes e execução

```bash
npm install
npm run typecheck   # tsc --noEmit
npm run build       # tsc -p tsconfig.json → dist/
npm test            # node --test dist/test/**/*.test.js
```

O cliente Go (`migracao/backend/internal/pluginhost`) espera o `dist/src/host.js` COMPILADO — rode `npm run build` aqui antes dos testes de integração do lado Go.

## Limitações desta entrega (deliberadas, não fabricadas)

- **Chamadas serializadas por `Client`**: um processo de host atende uma chamada por vez (mutex do lado Go) — correlacionar `callback_request` de chamadas CONCORRENTES pelo MESMO processo exigiria rastrear qual conjunto de capacidades pertence a qual chamada em voo, complexidade não justificada para o volume esperado (não é caminho quente). Uma pool de processos para mais throughput é uma extensão futura, não implementada aqui.
- **Sem inventário real de plugins de terceiro** (lacuna já registrada desde GO-001/GO-003/GO-004): `sendToast` em `src/host.ts` demonstra o MECANISMO de chamada por nome, não um catálogo de plugins de produção.
- **Sem wiring em `cmd/server`**: conectar este host a um caminho de validação real (ex.: `ownership_formula`, fórmulas calculadas em `CreateRecord`) é GO-023, que também decide quais expressões são prioritárias.
