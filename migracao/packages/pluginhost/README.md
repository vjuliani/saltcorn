# saltcorn-go-pluginhost (GO-022)

Host JS temporário — separado do BFF (`migracao/packages/bff/`) — que avalia expressões/callbacks de plugin sob um processo isolado, com capacidades explícitas, limites reais de memória/tempo e um protocolo de erros tipado. Promove a protótipo `docs/migracao-go/prototipos/GO-004-fronteira-rpc/` a produto — ver [ADR-0005](../../../docs/migracao-go/adr/0005-politica-de-extensoes.md) e [`docs/migracao-go/execucoes/GO-022.md`](../../../docs/migracao-go/execucoes/GO-022.md) para as decisões de escopo completas.

## Por que não `vm2`

O próprio relatório de GO-004 usou `vm2` deliberadamente para medir a fronteira RPC com o mecanismo real de produção, mas registrou explicitamente: "não é recomendação de continuar usando vm2 no host final". `vm2` está descontinuado, com histórico de escape de sandbox. Este host usa o módulo `vm` **nativo** do Node (`vm.Script`/`vm.createContext`) — não porque `vm` seja um sandbox mais forte (não é — ADR-0005 cita `vm.runInNewContext` como "sandbox mais fraco ainda" no contexto mobile), mas porque a ADR é explícita: **"simples uso de VM não constitui toda a fronteira de segurança"**. A fronteira de segurança real deste host é o **PROCESSO** isolado — limite de heap via `--max-old-space-size` (na hora de spawnar, do lado do cliente Go, `migracao/backend/internal/pluginhost`), timeout do lado Go com kill+respawn. A VM é só a camada interna de conveniência (contém a maioria dos laços síncronos sem precisar matar o processo inteiro), nunca a fronteira de confiança.

## Protocolo (`src/protocol.ts`)

JSON, uma mensagem por linha, em stdin/stdout — evolução direta do protocolo prototipado em GO-004 (`docs/migracao-go/prototipos/GO-004-fronteira-rpc/host.cjs`):

- **`eval`** (Go → host): `{id, kind: "expr"|"call", code?, name?, args?, context?, capabilities: Capability[], timeoutMs?}`. `capabilities` é a lista de callbacks que ESTA chamada pode invocar — ADR-0005: "capacidades explícitas, não acesso irrestrito". `null`/ausente é tratado como lista vazia (achado desta tarefa: Go não usa `omitempty` em `Capabilities`, e um `null` sem essa normalização quebrava com um erro de runtime em vez de `capability_denied` — corrigido em `src/host.ts`).
- **`result`** (host → Go): `{id, ok, result?, error?: {code, message}, evalMs}` — `error.code` é um de `runtime_error | capability_denied | invalid_request | timeout | crashed | unsupported_reference`, nunca uma string solta.
- **`callback_request`** (host → Go) / **`callback_response`** (Go → host): canal de callback **genérico** por `op` (não um global por singleton — recomendação #2 do relatório de GO-004), correlacionado por `corr`. A checagem "esta chamada pode invocar este `op`?" acontece nos DOIS lados (host, em `requestCallback`; cliente Go, em `serveCallback`) — defesa em profundidade, nenhum lado confia cegamente no outro.

## Capacidades implementadas

- **`db.read`** (GO-022): uma leitura genérica, resolvida do lado Go reaproveitando `internal/records.Rows` com a MESMA autorização por papel já usada em todo o resto do backend (GO-008/012/015). Nenhuma credencial de banco cruza para o processo Node (ADR-0005).
- **`db.write`** (GO-052): a capacidade de escrita que GO-022/GO-023 deixaram deliberadamente de fora ("precisa de decisão própria de idempotência" — ver nota histórica abaixo). Concedida SÓ à ação nativa `run_js_code` (`internal/triggers.NewRunJSCode`), nunca à avaliação de expressão genérica (`only_if`, campos calculados) — essas continuam estritamente somente-leitura, sem nenhuma capacidade declarada. `Table` no sandbox de `host.ts` deixou de ser um singleton totalmente bloqueado: `Table.findOne({name: string})` é SÍNCRONO (achado do preflight — o legado resolve contra um cache em-memória, nunca faz I/O nessa chamada), devolve um handle cujo ÚNICO método real é `insertRow(values)` (o que `receive_share_trigger` do pack piloto guitars usa) — chama `db.write` de verdade. Qualquer outro método (`updateRow`/`deleteRows`/`getRows`) ou uma forma de `findOne` diferente de `{name}` continua lançando `unsupported_reference`, nunca `undefined` silencioso. Sua política de idempotência não vive aqui: o CHAMADOR HTTP (`POST .../events/{eventname}`) já exige Idempotency-Key envolvendo a transação inteira.
- **`File`/`View` continuam totalmente bloqueados** — nenhum canal de callback para eles nesta entrega.

## Isolamento testado por regressão deliberada

Quatro garantias, cada uma provada desabilitando o mecanismo e confirmando falha real antes de restaurar (ver `migracao/backend/internal/pluginhost/client_test.go`, `test/host.test.ts` e `docs/migracao-go/execucoes/GO-022.md`/`GO-023.md`):

1. **Timeout**: um laço síncrono infinito é interrompido; sem a contenção do lado Go, o processo preso é reaproveitado pela chamada seguinte e produz uma condição de corrida real (confirmado com `go test -race` durante GO-022).
2. **Crash**: o processo do host morto (por OOM real via `--max-old-space-size`, ou morto externamente) nunca deixa o cliente Go preso — a chamada em voo recebe `crashed`, e a PRÓXIMA chamada sobe um host novo sozinha.
3. **Acesso proibido**: um callback fora da lista de capacidades concedidas é negado nos dois lados, nunca chega a executar.
4. **Referência a singleton de domínio** (GO-023): `File`/`View` no sandbox de `host.ts` são um `Proxy` que lança `unsupported_reference` em qualquer leitura/chamada — removendo esse estojo, a MESMA referência passa a virar `runtime_error`, indistinguível de um bug comum da fórmula do usuário. É exatamente essa ambiguidade que o estojo elimina. `Table` ganhou um canal real em GO-052 (ver acima), mas o MESMO princípio se aplica ao que continua fora dele: `Table.updateRow`/`Table.findOne({id: 1})` etc. lançam o mesmo `unsupported_reference`, nunca `undefined`.
5. **Capacidade concedida é checada em CADA chamada, nunca herdada** (GO-052): removendo a lista `granted` que `tableHandle.insertRow` de fato repassa a `requestCallback` (regressão deliberada, substituindo por uma lista fixa `["db.write"]`) faz o teste dedicado falhar — uma chamada SEM `db.write` nas capacidades declaradas ainda conseguia escrever, porque o handle "lembrava" uma concessão fixa em vez de checar a REAL lista desta chamada específica.

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
- **Sem wiring em `cmd/server`**: GO-023 entrega `internal/expression.Evaluator` (a fachada única de consumo, com checagem de cutover e coerção de tipo) mas não conecta nenhuma rota HTTP real a ela (ex.: campos calculados em `CreateRecord`, `ownership_formula`) — isso fica para os consumidores futuros (GO-024 triggers/ações, GO-027 packs, GO-029 SDK).
