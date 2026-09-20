// Test-only session bootstrap, using the same runtime modules as cmd/server.
// Session credentials travel over IPC, never into public evidence/log files.
import { createServer } from "node:http";
import { loadConfig } from "../packages/bff/dist/src/config.js";
import { GoClient } from "../packages/bff/dist/src/goClient.js";
import { InMemorySessionStore } from "../packages/bff/dist/src/session.js";
import { generateCsrfToken } from "../packages/bff/dist/src/csrf.js";
import {
  buildRouter,
  createRequestListener,
} from "../packages/bff/dist/src/app.js";
import { selfHostedListener } from "../packages/bff/dist/src/selfhost.js";
const config = loadConfig();
const sessionStore = new InMemorySessionStore();
const goClient = new GoClient({
  baseUrl: config.goInternalApiUrl,
  timeoutMs: config.goRequestTimeoutMs,
});
const deps = { config, sessionStore, goClient };
const listener = selfHostedListener(
  deps,
  createRequestListener(buildRouter(deps), deps),
  {
    root: new URL("../packages/frontend/dist", import.meta.url).pathname,
    tenant: "load_a",
    installationId: "go035-lab",
  }
);
const sessions = [];
for (const tenant of ["load_a", "load_b"])
  sessions.push({
    tenant,
    sessionId: await sessionStore.create({ userId: "1", tenant }),
    csrfToken: generateCsrfToken(),
  });
const server = createServer((req, res) => void listener(req, res));
server.listen(0, "127.0.0.1", () =>
  process.send({ port: server.address().port, sessions })
);
process.on("message", () => process.send({ stats: goClient.concurrency }));
process.on("SIGTERM", () => {
  server.closeAllConnections();
  server.close();
  process.disconnect();
});
