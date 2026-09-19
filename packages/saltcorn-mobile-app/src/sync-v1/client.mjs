// GO-031: platform-neutral client. Runtime dependencies are injected; this file
// runs unchanged in Capacitor/WebView and in the SQLite/HTTP integration tests.
const clone = (value) => JSON.parse(JSON.stringify(value));
const sameScope = (a, b) => a?.tenant === b.tenant && a?.actor === b.actor && a?.table === b.table;
const fail = (code) => Object.assign(new Error(code), { code });

export function createSQLiteStore(query, key) {
  return {
    async load() {
      await query("CREATE TABLE IF NOT EXISTS _sc_go_sync_v1 (scope TEXT PRIMARY KEY, revision INTEGER NOT NULL, state TEXT NOT NULL)", []);
      const { rows } = await query("SELECT revision, state FROM _sc_go_sync_v1 WHERE scope = ?", [key]);
      return rows.length ? { revision: rows[0].revision, state: JSON.parse(rows[0].state) } : { revision: 0, state: null };
    },
    async save(revision, state) {
      const result = revision === 0
        ? await query("INSERT INTO _sc_go_sync_v1 (scope, revision, state) VALUES (?, 1, ?) ON CONFLICT(scope) DO NOTHING RETURNING revision", [key, JSON.stringify(state)])
        : await query("UPDATE _sc_go_sync_v1 SET state = ?, revision = revision + 1 WHERE scope = ? AND revision = ? RETURNING revision", [JSON.stringify(state), key, revision]);
      if (result.rows.length !== 1) throw fail("local_state_changed");
      return result.rows[0].revision;
    },
  };
}

export class OfflineSyncClient {
  constructor({ scope, store, exchange, uuid = () => globalThis.crypto.randomUUID() }) {
    if (!scope?.tenant || !scope?.actor || !scope?.table) throw fail("invalid_scope");
    this.scope = clone(scope);
    this.store = store;
    this.exchange = exchange;
    this.uuid = uuid;
    this.tail = Promise.resolve();
  }

  exclusive(fn) {
    const work = this.tail.then(fn);
    this.tail = work.catch(() => {});
    return work;
  }

  async load() {
    const stored = await this.store.load();
    let state = stored.state;
    if (!state) {
      state = { format: 2, protocol: 1, scope: this.scope, client_id: this.uuid(), schema_version: 0, fields: [], checkpoint: "", rows: [], pending: [], conflicts: [], locked: false };
      stored.revision = await this.store.save(stored.revision, state);
    } else {
      if (!sameScope(state.scope, this.scope)) throw fail("scope_mismatch");
      if (state.protocol !== 1 || ![1, 2].includes(state.format)) throw fail("local_version_unsupported");
      if (!Array.isArray(state.rows) || !Array.isArray(state.pending) || !Array.isArray(state.fields) || !state.client_id) throw fail("local_state_invalid");
      if (state.format === 1) {
        // Additive migration: no DROP/DELETE and no regeneration of mutation IDs.
        state = { ...state, format: 2, conflicts: state.conflicts ?? [], locked: state.locked ?? false };
        stored.revision = await this.store.save(stored.revision, state);
      }
      if (!Array.isArray(state.conflicts)) throw fail("local_state_invalid");
    }
    return { revision: stored.revision, state };
  }

  read() {
    return this.exclusive(async () => {
      const { state } = await this.load();
      if (state.locked) throw fail("reauthentication_required");
      const rows = new Map(state.rows.map((row) => [row.id, clone(row)]));
      for (const item of [...state.pending, ...state.conflicts.map((c) => c.mutation)]) {
        const key = item.kind === "create" ? `local:${item.id}` : item.row_id;
        if (item.kind === "delete") rows.delete(key);
        else rows.set(key, { ...(rows.get(key) ?? item.original ?? {}), ...item.values, id: key, _pending: true });
      }
      return { rows: [...rows.values()], pending: clone(state.pending), conflicts: clone(state.conflicts), checkpoint: state.checkpoint, schema_version: state.schema_version };
    });
  }

  edit(kind, rowID, values = {}) {
    return this.exclusive(async () => {
      const { state, revision } = await this.load();
      if (state.locked) throw fail("reauthentication_required");
      if (!state.schema_version) throw fail("bootstrap_required");
      if (!["create", "update", "delete"].includes(kind)) throw fail("invalid_mutation");
      const localID = typeof rowID === "string" && rowID.startsWith("local:") ? rowID.slice(6) : null;
      if (localID) {
        const created = state.pending.find((m) => m.id === localID && m.kind === "create");
        if (!created) throw fail("record_missing");
        if (created.sent) throw fail("resolve_pending_first");
        if (kind === "delete") state.pending = state.pending.filter((m) => m !== created);
        else if (kind === "update") {
          const allowed = new Set(state.fields.map((f) => f.name));
          if (Object.keys(values).some((key) => !allowed.has(key))) throw fail("unknown_field");
          created.values = { ...created.values, ...clone(values) };
        } else throw fail("invalid_mutation");
        await this.store.save(revision, state);
        return created.id;
      }
      const old = state.pending.find((m) => m.row_id === rowID && rowID !== undefined);
      if (old?.sent || state.conflicts.some((c) => c.mutation.row_id === rowID && rowID !== undefined)) throw fail("resolve_pending_first");
      const row = state.rows.find((r) => r.id === rowID);
      if (kind !== "create" && !row) throw fail("record_missing");
      const fields = new Set(state.fields.map((f) => f.name));
      for (const field of Object.keys(values)) if (!fields.has(field) || field === "id" || field === "_version") throw fail("unknown_field");
      const mutation = old ?? { id: this.uuid(), kind, ...(kind === "create" ? {} : { row_id: rowID, base_version: row._version }), original: row ? clone(row) : undefined };
      mutation.kind = kind;
      if (kind === "delete") delete mutation.values;
      else mutation.values = { ...(mutation.values ?? {}), ...clone(values) };
      if (!old) state.pending.push(mutation);
      await this.store.save(revision, state);
      return mutation.id;
    });
  }

  resolve(id, choice) {
    return this.exclusive(async () => {
      if (!["server", "local"].includes(choice)) throw fail("invalid_resolution");
      const { state, revision } = await this.load();
      if (state.locked) throw fail("reauthentication_required");
      const conflict = state.conflicts.find((c) => c.mutation.id === id);
      if (!conflict) throw fail("conflict_missing");
      if (choice === "local") {
        const old = conflict.mutation;
        const row = state.rows.find((r) => r.id === old.row_id);
        const kind = old.kind === "delete" ? "delete" : row ? "update" : "create";
        if (kind === "delete" && !row) throw fail("record_missing");
        const values = kind === "create" ? { ...old.original, ...old.values } : old.values;
        if (values) { delete values.id; delete values._version; }
        const allowed = new Set(state.fields.map((f) => f.name));
        if (Object.keys(values ?? {}).some((k) => !allowed.has(k))) throw fail("schema_changed");
        state.pending.push({ id: this.uuid(), kind, ...(row ? { row_id: row.id, base_version: row._version, original: clone(row) } : {}), ...(kind !== "delete" ? { values: clone(values ?? {}) } : {}) });
      }
      state.conflicts = state.conflicts.filter((c) => c !== conflict);
      await this.store.save(revision, state);
    });
  }

  sync() {
    return this.exclusive(async () => {
      let { state, revision } = await this.load();
      const selected = state.locked ? [] : state.pending.slice(0, 100);
      for (const mutation of selected) mutation.sent = true;
      // Persist before network IO: response loss/restart must retry EXACT IDs.
      revision = await this.store.save(revision, state);
      const wire = (m) => {
        const { original, sent, ...request } = m;
        return request;
      };
      const request = { protocol: 1, scope: this.scope, client_id: state.client_id, schema_version: state.schema_version, checkpoint: state.checkpoint, mutations: selected.map(wire) };
      let response;
      let sent = selected;
      const call = async (input) => {
        try { return await this.exchange(input); }
        catch (error) {
          if ([401, 403].includes(error.status)) {
            state.locked = true;
            await this.store.save(revision, state);
          }
          throw error;
        }
      };
      try { response = await call(request); }
      catch (error) {
        if (error.code !== "schema_changed") throw error;
        // Refresh also revalidates authorization; drafts remain locked on 403.
        sent = [];
        response = await call({ ...request, mutations: [] });
      }
      if (response?.protocol !== 1 || !sameScope(response.scope, this.scope)) throw fail("response_scope_mismatch");
      if (!Array.isArray(response.rows) || !Array.isArray(response.results) || !Array.isArray(response.fields) || !Number.isInteger(response.schema_version) || response.schema_version < state.schema_version || typeof response.checkpoint !== "string") throw fail("invalid_response");
      const ids = new Set();
      for (const row of response.rows) {
        if (!Number.isSafeInteger(row.id) || row.id <= 0 || !row._version || ids.has(row.id)) throw fail("invalid_snapshot");
        ids.add(row.id);
      }
      const pendingIDs = new Set(sent.map((m) => m.id));
      const resultIDs = new Set();
      for (const result of response.results) {
        if (!pendingIDs.has(result.id) || resultIDs.has(result.id) || !["applied", "conflict", "rejected"].includes(result.status)) throw fail("invalid_acknowledgement");
        resultIDs.add(result.id);
      }
      if (resultIDs.size !== pendingIDs.size) throw fail("missing_acknowledgement");
      for (const result of response.results) {
        const mutation = state.pending.find((m) => m.id === result.id);
        if (result.status !== "applied") state.conflicts.push({ mutation, code: result.code ?? result.status });
        state.pending = state.pending.filter((m) => m.id !== result.id);
      }
      const fields = new Map(response.fields.map((f) => [f.name, f]));
      const previous = new Map(state.fields.map((f) => [f.name, f]));
      state.pending = state.pending.filter((mutation) => {
        const incompatible = Object.keys(mutation.values ?? {}).some((name) => !fields.has(name) || (previous.has(name) && fields.get(name).type !== previous.get(name).type));
        if (incompatible) state.conflicts.push({ mutation, code: "schema_changed" });
        return !incompatible;
      });
      state = { ...state, rows: response.rows, fields: response.fields, schema_version: response.schema_version, checkpoint: response.checkpoint, locked: false };
      // One CAS replaces snapshot, acknowledgements, conflicts and checkpoint.
      // Failure leaves the old queue intact; the server will replay next time.
      await this.store.save(revision, state);
      return clone(response);
    });
  }
}
