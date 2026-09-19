# ADR-0011 — Sync versionado e runtime mobile preservado

- Data: 2026-09-19.
- Estado: implementado na GO-031, sujeito à revisão do PR.
- Complementa [ADR-0004](0004-estrategia-de-bancos.md), [GO-031](../TASKS.md#go-031--migrar-contratos-de-sync-e-mobile-offline) e [contrato](../../../migracao/contracts/openapi/sync.yaml).

## Decisão

O protocolo 1 usa `POST /api/bff/sync/{table}/exchange`, encaminhado pelo BFF para
`POST /v1/tenants/{tenant}/sync/{table}/exchange`. Sessão e CSRF permanecem no BFF;
Go verifica identidade delegada, tenant, ator atual, papel, ownership/RLS e guarda
de corte `tables.records`. O cliente nunca recebe o segredo de identidade interna.

O runtime no dispositivo permanece **JavaScript/Capacitor com SQLite**. Nenhum
binário Go, runtime WASM ou novo plugin nativo é distribuído. O pacote Go de sync
executa no servidor; suas regras também são testadas sobre o adapter SQLite da
GO-030. O cliente usa o driver SQLite do runtime por uma função `query` injetada.

O protocolo legado (`/sync/sync_timestamp`, `load_changes`, `deletes` e upload)
permanece disponível para os clientes antigos. O novo módulo é opt-in, exportado
como `saltcorn.mobileApp.syncV1`. Não se renomeiam rotas antigas nem se enviam filas
legadas ao protocolo novo. A abertura do cliente novo recusa uma tabela cujo
`<tabela>_sync_info` ainda tenha `modified_local=true`; é preciso sincronizar ou
reconciliar essa fila pelo protocolo de origem antes do corte. Nenhum registro
local legado é apagado por essa verificação.

## Concorrência, exclusão e retomada

Cada mutação tem ID imutável, device/client ID, tipo, ID de registro e versão-base.
A chave de idempotência inclui ator, tabela e dispositivo, dentro do schema do
tenant. Registro, resultado e evento de outbox são gravados na mesma transação.
Uma resposta perdida permite repetir exatamente o mesmo ID/payload. Payload
alterado com ID reutilizado é rejeitado. Após conflito, uma nova decisão do usuário
gera outro ID e usa a versão atual; não há sobrescrita silenciosa.

O cliente recebe um **snapshot completo autorizado** da tabela, ordenado por ID.
A ausência de um registro significa exclusão ou perda de acesso; não é preciso
expor tombstones de registros invisíveis. Rascunhos e conflitos ficam separados
do snapshot: uma exclusão remota não apaga silenciosamente uma edição pendente.
O checkpoint é um hash do snapshot e schema, não um cursor incremental temporal.
Assim, não há janela de timestamp que possa pular uma transação confirmada tarde.

A escolha é deliberadamente diferente do cursor temporal e merge por campo do
legado: a primeira versão troca eficiência de transferência por um protocolo de
retomada simples, conflitos explícitos e revogação verificável. Limites: 100
mutações por requisição, 1 MiB de corpo e 2000 linhas visíveis por tabela. Exceder o
snapshot devolve 413 e desfaz o lote inteiro; nunca se devolve uma página incompleta
como se fosse o conjunto completo. Tabelas maiores exigem evolução negociada do
protocolo antes do corte. Isto não declara paridade global de todas as aplicações.

## Persistência e upgrade local

`_sc_go_sync_v1` armazena, por servidor/tenant/ator/tabela, o estado JSON e uma
revisão para compare-and-swap. Antes da rede, a fila é marcada como enviada e
persistida. Snapshot, confirmações, conflitos e checkpoint são substituídos em
uma única escrita condicional. Falha local ou concorrência entre handles mantém a
fila anterior recuperável. Edições durante uma requisição não alteram payloads já
enviados. Criações ainda não enviadas podem ser editadas ou canceladas offline.

A migração local de formato 1 para 2 é aditiva: mantém IDs, fila, dados e escopo;
formato futuro/desconhecido é recusado sem escrita. Mudança no schema do servidor
requer pull sem mutações; campos removidos/tipos alterados viram conflitos
preservados. A operação continua com novo schema quando compatível. O retorno SQL
de comandos usa colunas explícitas para que o cache de statements do PostgreSQL
não mantenha o formato de `RETURNING *` anterior ao DDL.

Uma resposta 401/403 bloqueia leitura/edição do cache pelo cliente, preservando os
rascunhos no SQLite. Reautenticar o mesmo escopo primeiro faz pull sem enviar a
fila. Troca de conta não reusa cache nem confirmações de outro ator. O servidor
confere o scope do corpo antes de qualquer efeito, além da identidade autenticada.

## Compatibilidade e limites

- O schema da outbox é preparado pelo proprietário durante provisionamento
  (`sync.EnsureSchema`/`outbox.EnsureSchema`); requisições de sync não executam DDL.
- Tipos/PKs são os implementados nas GO-011/012/013/030: IDs inteiros e os tipos
  metadata atuais. Não se converte silenciosamente UUID legado para inteiro.
- FKs usam IDs já confirmados no servidor. Criações relacionadas devem ser
  sincronizadas em ordem de dependência e usar o ID confirmado antes de enviar a
  referência; não há tradução implícita de IDs temporários entre tabelas.
- Conflitos são por registro. A UI consumidora usa `read().conflicts` e
  `resolve(id, "local" | "server")`; não há merge automático por campo.
- A instalação nativa, assinatura Android/iOS e distribuição são GO-032. Os testes
  desta entrega validam o bundle JS, SQLite real e a cadeia HTTP real; não afirmam
  execução em aparelho/emulador nativo.

## Dependências JS preservadas

| Camada | Dependências existentes | Papel após GO-031 |
| --- | --- | --- |
| Shell mobile | Capacitor core/app/network/camera/filesystem/file-transfer/screen-orientation; send-intent | Ciclo de vida, conectividade, arquivos e recursos do dispositivo |
| Banco local | `@saltcorn/sqlite-mobile`, `@capacitor-community/sqlite`, `jeep-sqlite` no perfil web | Executar SQL local; o adapter novo recebe `saltcorn.data.db.query` |
| Runtime/rotas | `@saltcorn/data`, universal-router, markup e views/plugins empacotados | Continuam em JS; filas/rotas legadas não são reinterpretadas |
| Expressões/plugins | vm-browserify, crypto-browserify, plugins-code e demais polyfills do bundle | Preservados; portar sync não porta plugins de domínio para o dispositivo |
| Sync v1 | Fetch, AbortController, Web Crypto UUID e função SQLite `query` | Nenhuma dependência npm/runtime adicional |
| BFF | Sessão/CSRF, GoClient e JWT de serviço já existentes | Autenticar cliente e delegar identidade ao Go |

Versões Capacitor para o build vêm de
`packages/saltcorn-mobile-builder/utils/common-build-utils.ts`; não foram
atualizadas nesta tarefa. SQLite precisa suportar `RETURNING`, usado na escrita
condicional do estado local. Evidências em [GO-031](../execucoes/GO-031.md).
