# Cliente offline do protocolo Go v1

O bundle exporta `saltcorn.mobileApp.syncV1`. O fluxo antigo em `offlineMode`
continua usando o protocolo legado. Ative o módulo novo apenas para tabelas
migradas, com sessão do BFF estabelecida e a capacidade `tables.records` em Go.
O cliente usa seu próprio cache/fila; consumidores do protocolo novo devem ler e
editar por esta API, sem misturar escritas nos modelos offline legados.

```js
const bootstrap = await fetch(`${bffURL}/api/bff/bootstrap`, {
  credentials: "include",
}).then((response) => {
  if (!response.ok) throw new Error("Sessão do BFF necessária");
  return response.json();
});
const client = await saltcorn.mobileApp.syncV1.open({
  table: "widgets",
  baseURL: bffURL,
  tenant: bootstrap.tenant,
  actor: bootstrap.actor.id,
  csrfToken: () => csrfTokenAtualDoBff,
  query: (sql, args) => saltcorn.data.db.query(sql, args),
});
await client.sync(); // bootstrap online; depois é possível ler/editar offline
await client.edit("create", undefined, { label: "Criado offline" });
const local = await client.read();
await client.sync(); // repete a fila com os mesmos IDs em caso de queda
for (const conflict of (await client.read()).conflicts) {
  // Exibir o rascunho e obter a escolha do usuário antes de chamar resolve.
  // await client.resolve(conflict.mutation.id, "local" ou "server");
}
```

- `edit("update", id, values)` e `edit("delete", id)` usam a versão da última
  leitura. Uma criação não enviada aparece como `local:<mutation-id>` e pode ser
  editada/cancelada usando esse ID local.
- `sync()` envia até 100 operações por chamada. Consulte `read().pending` para
  decidir se há mais lotes. Um conflito permanece em `read().conflicts`; não é
  automaticamente reenviado com outro ID.
- `resolve(id, "server")` descarta explicitamente o rascunho conflitante;
  `"local"` cria uma nova tentativa sobre a versão atual. Registro removido pode
  ser recriado somente por essa decisão explícita. Campo removido exige adaptar
  a entrada ao novo schema; o rascunho original permanece disponível.
- `legacy_pending` impede iniciar o protocolo novo enquanto houver alterações
  não sincronizadas na tabela legada. Finalize/reconcilie essa fila no protocolo
  de origem. Não apague a base para contornar o erro.
- 401/403 bloqueiam leitura e edição do cache, sem apagar a fila. Após restaurar
  a sessão do mesmo ator, a primeira sincronização apenas atualiza o snapshot.
- Origem/base URL, tenant, ator e tabela isolam o cache. Não use o segredo de
  identidade do backend no app. A função de CSRF deve fornecer o token atual.
- `open` não instala plugins nativos. `query` deve oferecer SQLite com RETURNING
  e retornar `{rows: [...]}`. Uma única escrita CAS confirma toda a resposta;
  não envolva `sync()` em uma transação SQLite mantida aberta durante a rede.

Limites e decisões: `docs/migracao-go/adr/0011-sync-versionado-offline.md`.
