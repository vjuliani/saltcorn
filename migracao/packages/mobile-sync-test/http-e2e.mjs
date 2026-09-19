// Invoked by the Go HTTP integration test against an isolated tenant/database.
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { DatabaseSync } from "node:sqlite";
import { join } from "node:path";
import { open } from "../../../packages/saltcorn-mobile-app/src/sync-v1/index.mjs";
import { loadConfig } from "../bff/dist/src/config.js";
import { GoClient } from "../bff/dist/src/goClient.js";
import { InMemorySessionStore } from "../bff/dist/src/session.js";
import { buildRouter, createRequestListener, createTestSession } from "../bff/dist/src/app.js";

const fixture = JSON.parse(process.env.GO031_FIXTURE);
const config = loadConfig({
  SALTCORN_BFF_GO_INTERNAL_API_URL: fixture.url,
  SALTCORN_BFF_SERVICE_IDENTITY_SECRET: fixture.secret,
});
const sessions = new InMemorySessionStore();
const goClient = new GoClient({ baseUrl: fixture.url, timeoutMs: 5000 });
const deps = { config, sessionStore: sessions, goClient };
const server = createServer(createRequestListener(buildRouter(deps), deps));
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const baseURL = `http://127.0.0.1:${server.address().port}`;
const session = await createTestSession(sessions, { userId: fixture.actor, tenant: fixture.tenant });
const dbs = [];
let offline = false;
let loseResponse = false;
async function device(name) {
  const db = new DatabaseSync(join(fixture.dir, `${name}.sqlite`)); dbs.push(db);
  const query = async (sql, params) => {
    const statement = db.prepare(sql);
    return { rows: statement.columns().length ? statement.all(...params) : (statement.run(...params), []) };
  };
  return open({ baseURL, table: "widgets", tenant: fixture.tenant, actor: fixture.actor, csrfToken: session.csrfToken, query,
    fetchImpl: async (url, options) => {
      if (offline) throw new Error("offline");
      const response = await fetch(url, { ...options, headers: { ...options.headers, Cookie: session.cookies } });
      if (loseResponse && JSON.parse(options.body).mutations.length) {
        await response.arrayBuffer(); // server committed; client never sees acknowledgement
        loseResponse = false;
        throw new Error("response lost");
      }
      return response;
    },
  });
}
try {
  // Real BFF rejects absent session and absent CSRF before talking to Go.
  assert.equal((await fetch(`${baseURL}/api/bff/sync/widgets/exchange`, { method: "POST" })).status, 401);
  assert.equal((await fetch(`${baseURL}/api/bff/sync/widgets/exchange`, { method: "POST", headers: { Cookie: session.cookies } })).status, 403);
  let a = await device("a");
  const b = await device("b");
  await a.sync(); await b.sync();
  offline = true;
  const pendingID = await a.edit("create", undefined, { label: "offline edit" });
  await assert.rejects(a.sync(), /offline/);
  assert.equal((await a.read()).rows[0].label, "offline edit");
  offline = false; loseResponse = true;
  await assert.rejects(a.sync(), /response lost/);
  a = await device("a"); // process-equivalent reopen, same physical SQLite file
  assert.equal((await a.read()).pending[0].id, pendingID);
  await a.sync(); await b.sync();
  assert.equal((await a.read()).rows.length, 1);
  assert.equal((await b.read()).rows.length, 1);
  const id = (await a.read()).rows[0].id;
  await a.edit("update", id, { label: "draft A" });
  await b.edit("update", id, { label: "remote B" });
  await b.sync(); await a.sync();
  let local = await a.read();
  assert.equal(local.conflicts[0].code, "version_conflict");
  assert.equal(local.rows[0].label, "draft A");
  await a.resolve(local.conflicts[0].mutation.id, "local");
  await a.sync(); await b.sync();
  assert.equal((await b.read()).rows[0].label, "draft A");
  // Remote deletion versus a local edit is explicit, never resurrection.
  await a.edit("update", id, { label: "draft after delete" });
  await b.edit("delete", id); await b.sync(); await a.sync();
  local = await a.read();
  assert.equal(local.conflicts[0].code, "missing_or_inaccessible");
  assert.equal(local.rows[0].label, "draft after delete");
  await a.resolve(local.conflicts[0].mutation.id, "server");
  assert.equal((await a.read()).rows.length, 0);
  // Server schema changes while a draft is pending: refresh, retain ID, retry.
  const upgradeID = await a.edit("create", undefined, { label: "upgrade pending" });
  assert.equal((await fetch(`${fixture.url}/fixture/schema`, { method: "POST" })).status, 204);
  await a.sync();
  assert.equal((await a.read()).pending[0].id, upgradeID);
  await a.sync(); await b.sync();
  assert.equal((await b.read()).rows[0].label, "upgrade pending");
  // A different active login cannot send this actor's queue.
  const otherSession = await createTestSession(sessions, { userId: fixture.otherActor, tenant: fixture.tenant });
  const request = { protocol: 1, scope: { tenant: fixture.tenant, actor: fixture.actor, table: "widgets" }, client_id: "wrong_login", schema_version: 0, checkpoint: "", mutations: [] };
  assert.equal((await fetch(`${baseURL}/api/bff/sync/widgets/exchange`, { method: "POST", headers: { Cookie: otherSession.cookies, "X-CSRF-Token": otherSession.csrfToken, "Content-Type": "application/json" }, body: JSON.stringify(request) })).status, 403);
  await a.edit("create", undefined, { label: "preserve after revoke" });
  assert.equal((await fetch(`${fixture.url}/fixture/revoke`, { method: "POST" })).status, 204);
  await assert.rejects(a.sync(), (error) => error.status === 403);
  await assert.rejects(a.read(), /reauthentication_required/);
  const stored = await a.store.load();
  assert.equal(stored.state.pending[0].values.label, "preserve after revoke");
  console.log("PASS: mobile JS + SQLite -> BFF sessão/CSRF -> Go/PG; offline, replay, conflito, exclusão, upgrade, troca de ator e revogação");
} finally {
  for (const db of dbs) db.close();
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
}
