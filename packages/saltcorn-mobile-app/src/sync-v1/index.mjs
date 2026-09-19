import { OfflineSyncClient, createSQLiteStore } from "./client.mjs";

// Opt-in runtime API: legacy /sync remains unchanged. Scope comes from a
// bootstrap authenticated with the BFF session, never a user-supplied role.
export async function open({ table, baseURL, tenant, actor, csrfToken, query, fetchImpl = globalThis.fetch, timeoutMs = 15000 }) {
  const url = new URL(baseURL);
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new Error("invalid_base_url");
  const origin = url.href.replace(/\/$/, "");
  const scope = { tenant, actor: String(actor), table };
  const key = JSON.stringify([origin, tenant, scope.actor, table]);
  // A legacy offline queue belongs to a different protocol. Never abandon it
  // by silently opening a fresh v1 cache during an app upgrade.
  const legacyName = `${table}_sync_info`;
  const existing = await query("SELECT name FROM sqlite_master WHERE type='table' AND name=?", [legacyName]);
  if (existing.rows.length) {
    const quoted = '"' + legacyName.replaceAll('"', '""') + '"';
    const pending = await query(`SELECT 1 FROM ${quoted} WHERE modified_local = true LIMIT 1`, []);
    if (pending.rows.length) throw Object.assign(new Error("legacy_pending"), { code: "legacy_pending" });
  }
  const store = createSQLiteStore(query, key);
  const client = new OfflineSyncClient({ scope, store, exchange: async (request) => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), timeoutMs);
    try {
    const response = await fetchImpl(`${origin}/api/bff/sync/${encodeURIComponent(table)}/exchange`, {
      method: "POST", credentials: "include", signal: controller.signal,
      headers: { "Content-Type": "application/json", "X-CSRF-Token": typeof csrfToken === "function" ? csrfToken() : csrfToken },
      body: JSON.stringify(request),
    });
    const body = await response.json();
    if (!response.ok) throw Object.assign(new Error(body.error?.message ?? "sync_failed"), { status: response.status, code: body.error?.code });
    return body;
    } finally { clearTimeout(timeout); }
  } });
  await client.load();
  return client;
}

export { OfflineSyncClient, createSQLiteStore };
