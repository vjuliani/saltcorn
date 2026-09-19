import { test } from "node:test";
import assert from "node:assert/strict";
import { DatabaseSync } from "node:sqlite";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { OfflineSyncClient, createSQLiteStore } from "../../../packages/saltcorn-mobile-app/src/sync-v1/client.mjs";
import { open } from "../../../packages/saltcorn-mobile-app/src/sync-v1/index.mjs";

const scope = { tenant: "acme", actor: "1", table: "items" };
const fields = [{ name: "name", type: "text", required: true }];
function fixture(t, exchange) {
  const dir = mkdtempSync(join(tmpdir(), "go031-client-"));
  const db = new DatabaseSync(join(dir, "client.sqlite"));
  t.after(() => { db.close(); rmSync(dir, { recursive: true, force: true }); });
  const query = async (sql, args) => {
    const statement = db.prepare(sql);
    return { rows: statement.columns().length ? statement.all(...args) : (statement.run(...args), []) };
  };
  const store = createSQLiteStore(query, "acme/1/items");
  return { db, query, store, client: new OfflineSyncClient({ scope, store, exchange }) };
}
const snapshot = (rows = [], results = [], schema = 1) => ({ protocol: 1, scope, schema_version: schema, fields, checkpoint: `checkpoint-${schema}`, rows, results });

test("offline queue survives restart, response loss, then atomic acknowledgement", async (t) => {
  let fail = false;
  let firstID;
  let count = 0;
  const { store, client } = fixture(t, async (request) => {
    if (!request.mutations.length) return snapshot();
    const mutation = request.mutations[0];
    if (!firstID) { firstID = mutation.id; count++; }
    assert.equal(mutation.id, firstID);
    if (fail) throw new Error("response lost after commit");
    return snapshot([{ id: 1, _version: "1", ...mutation.values }], [{ id: mutation.id, status: "applied", row_id: 1 }]);
  });
  await client.sync();
  await client.edit("create", undefined, { name: "offline" });
  fail = true;
  await assert.rejects(client.sync(), /response lost/);
  const restart = new OfflineSyncClient({ scope, store, exchange: client.exchange });
  assert.equal((await restart.read()).pending.length, 1);
  assert.equal((await restart.read()).rows[0].name, "offline");
  fail = false;
  await restart.sync();
  assert.equal(count, 1);
  assert.deepEqual((await restart.read()).rows, [{ id: 1, _version: "1", name: "offline" }]);
  assert.equal((await restart.read()).pending.length, 0);
});

test("version conflicts preserve draft and require explicit resolution with a new ID", async (t) => {
  const { client } = fixture(t, async (request) => request.mutations.length
    ? snapshot([{ id: 1, name: "remote", _version: "2" }], [{ id: request.mutations[0].id, status: "conflict", code: "version_conflict" }])
    : snapshot([{ id: 1, name: "old", _version: "1" }]));
  await client.sync();
  const oldID = await client.edit("update", 1, { name: "my draft" });
  await client.sync();
  assert.equal((await client.read()).rows[0].name, "my draft");
  assert.equal((await client.read()).conflicts.length, 1);
  await client.resolve(oldID, "local");
  const pending = (await client.read()).pending[0];
  assert.notEqual(pending.id, oldID);
  assert.equal(pending.base_version, "2");
  assert.equal(pending.values.name, "my draft");
});

test("snapshot deletions remove clean data while preserving conflicting drafts", async (t) => {
  let deleted = false;
  const { client } = fixture(t, async (request) => deleted
    ? snapshot([], request.mutations.map((m) => ({ id: m.id, status: "conflict", code: "missing_or_inaccessible" })))
    : snapshot([{ id: 1, name: "first", _version: "1" }, { id: 2, name: "clean", _version: "1" }]));
  await client.sync();
  const id = await client.edit("update", 1, { name: "saved draft" });
  deleted = true;
  await client.sync();
  assert.equal((await client.read()).rows.length, 1);
  assert.equal((await client.read()).rows[0].name, "saved draft");
  await client.resolve(id, "server");
  assert.deepEqual((await client.read()).rows, []);
});

test("upgrade preserves pending IDs, scope and local values; future formats remain intact", async (t) => {
  const { client, store } = fixture(t, async () => snapshot());
  await client.sync();
  const id = await client.edit("create", undefined, { name: "not uploaded" });
  let old = await store.load();
  old.state.format = 1;
  delete old.state.conflicts;
  await store.save(old.revision, old.state);
  const restarted = new OfflineSyncClient({ scope, store, exchange: client.exchange });
  assert.equal((await restarted.read()).pending[0].id, id);
  assert.equal((await store.load()).state.format, 2);
  old = await store.load(); old.state.format = 99;
  await store.save(old.revision, old.state);
  await assert.rejects(restarted.read(), /local_version_unsupported/);
  assert.equal((await store.load()).state.pending[0].id, id);
});

test("revoked access locks cache without deleting pending changes; different actor cannot reopen", async (t) => {
  let forbidden = false;
  const { client, store } = fixture(t, async () => {
    if (forbidden) throw Object.assign(new Error("revoked"), { status: 403 });
    return snapshot();
  });
  await client.sync();
  const id = await client.edit("create", undefined, { name: "private draft" });
  forbidden = true;
  await assert.rejects(client.sync(), /revoked/);
  await assert.rejects(client.read(), /reauthentication_required/);
  await assert.rejects(client.edit("create", undefined, {}), /reauthentication_required/);
  assert.equal((await store.load()).state.pending[0].id, id);
  const other = new OfflineSyncClient({ scope: { ...scope, actor: "2" }, store, exchange: client.exchange });
  await assert.rejects(other.read(), /scope_mismatch/);
  forbidden = false;
  await client.sync(); // reauthentication pulls only, no unexpected queued write
  assert.equal((await client.read()).pending[0].id, id);
});

test("server schema refresh preserves pending data and exposes incompatible fields as conflicts", async (t) => {
  let upgraded = false;
  const { client } = fixture(t, async (request) => {
    if (!upgraded) return snapshot();
    if (request.mutations.length) throw Object.assign(new Error("schema changed"), { code: "schema_changed", status: 409 });
    return { ...snapshot([], [], 2), fields: [] };
  });
  await client.sync();
  const id = await client.edit("create", undefined, { name: "survive migration" });
  upgraded = true;
  await client.sync();
  const local = await client.read();
  assert.equal(local.schema_version, 2);
  assert.equal(local.conflicts[0].mutation.id, id);
  assert.equal(local.conflicts[0].mutation.values.name, "survive migration");
  await assert.rejects(client.resolve(id, "local"), /schema_changed/);
});

test("failure to save response leaves queue/checkpoint recoverable; CAS prevents lost updates", async (t) => {
  const { client, store } = fixture(t, async (request) => snapshot([], request.mutations.map((m) => ({ id: m.id, status: "applied" }))));
  await client.sync();
  const first = await store.load();
  await store.save(first.revision, first.state);
  await assert.rejects(store.save(first.revision, first.state), /local_state_changed/);
  const id = await client.edit("create", undefined, { name: "durable" });
  const originalSave = store.save;
  let calls = 0;
  store.save = async (...args) => { if (++calls === 2) throw new Error("disk full"); return originalSave(...args); };
  await assert.rejects(client.sync(), /disk full/);
  assert.equal((await store.load()).state.pending[0].id, id);
  store.save = originalSave;
  await client.sync();
  assert.equal((await client.read()).pending.length, 0);
});

test("partial acknowledgements or cross-scope responses never consume pending operations", async (t) => {
  let wrong = false;
  const { client, store } = fixture(t, async () => wrong ? { ...snapshot(), scope: { ...scope, actor: "2" } } : snapshot());
  await client.sync();
  const id = await client.edit("create", undefined, { name: "stay" });
  await assert.rejects(client.sync(), /missing_acknowledgement/);
  wrong = true;
  await assert.rejects(client.sync(), /response_scope_mismatch/);
  assert.equal((await store.load()).state.pending[0].id, id);
});

test("mobile transport sends only session credentials and CSRF, never service identity", async (t) => {
  const { query } = fixture(t, async () => snapshot());
  const mobile = await open({ table: "items", baseURL: "https://app.example", tenant: "acme", actor: 1, csrfToken: "csrf", query,
    fetchImpl: async (url, options) => {
      assert.equal(url, "https://app.example/api/bff/sync/items/exchange");
      assert.equal(options.credentials, "include");
      assert.equal(options.headers["X-CSRF-Token"], "csrf");
      assert.equal(options.headers.Authorization, undefined);
      assert.equal(JSON.parse(options.body).scope.actor, "1");
      return { ok: true, json: async () => snapshot() };
    },
  });
  await mobile.sync();
});

test("local create can be edited and deleted offline before its first send", async (t) => {
  const { client } = fixture(t, async () => snapshot());
  await client.sync();
  const id = await client.edit("create", undefined, { name: "initial" });
  await client.edit("update", `local:${id}`, { name: "edited offline" });
  assert.equal((await client.read()).pending[0].id, id);
  assert.equal((await client.read()).rows[0].name, "edited offline");
  await client.edit("delete", `local:${id}`);
  assert.deepEqual((await client.read()).rows, []);
  assert.deepEqual((await client.read()).pending, []);
});

test("authorization revoked during schema refresh also locks persisted cache", async (t) => {
  let changed = false;
  const { client, store } = fixture(t, async (request) => {
    if (!changed) return snapshot();
    if (request.mutations.length) throw Object.assign(new Error("upgrade"), { status: 409, code: "schema_changed" });
    throw Object.assign(new Error("revoked"), { status: 403 });
  });
  await client.sync();
  await client.edit("create", undefined, { name: "draft" });
  changed = true;
  await assert.rejects(client.sync(), /revoked/);
  await assert.rejects(client.read(), /reauthentication_required/);
  assert.equal((await store.load()).state.pending[0].values.name, "draft");
});

test("upgrading a legacy installation refuses cutover with unsynchronized local records", async (t) => {
  const { query } = fixture(t, async () => snapshot());
  await query("CREATE TABLE items_sync_info (ref TEXT, modified_local INTEGER)", []);
  await query("INSERT INTO items_sync_info VALUES ('offline-id', 1)", []);
  await assert.rejects(open({ table: "items", baseURL: "https://app.example", tenant: "acme", actor: 1, csrfToken: "csrf", query }), /legacy_pending/);
  assert.deepEqual((await query("SELECT * FROM items_sync_info", [])).rows.map((row) => ({ ...row })), [{ ref: "offline-id", modified_local: 1 }]);
});
