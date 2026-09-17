// Sobe o BFF real (mesmos módulos de produção de migracao/packages/bff/
// src/*.ts, compilados) com UMA sessão pré-semeada — o harness de E2E
// precisa de um navegador "já logado" para exercitar o fluxo criar/
// publicar/operar, mas o BFF não tem (por decisão explícita, GO-017) um
// endpoint de login usuário+senha ainda. Em vez de fabricar um formulário
// de login que não existe de verdade, este script faz a MESMA chamada
// direta a `SessionStore.create(...)` que os próprios testes do BFF já
// fazem (`editor.test.ts`, `withSession`) — só que aqui o resultado vira
// um cookie real injetado no navegador (ver run.sh/tests/*.spec.ts), não
// um cabeçalho de uma chamada fetch de teste. Nenhuma rota nova entra em
// migracao/packages/bff/src/ — esta semeadura vive inteiramente aqui, em
// migracao/e2e/, fora do runtime real do BFF.
import { createServer } from "node:http";
import { loadConfig } from "../../packages/bff/dist/src/config.js";
import { GoClient } from "../../packages/bff/dist/src/goClient.js";
import { InMemorySessionStore } from "../../packages/bff/dist/src/session.js";
import { buildRouter, createRequestListener } from "../../packages/bff/dist/src/app.js";
import { generateCsrfToken } from "../../packages/bff/dist/src/csrf.js";

const tenant = process.env.SALTCORN_E2E_TENANT;
const adminUserId = process.env.SALTCORN_E2E_ADMIN_USER_ID;
if (!tenant || !adminUserId) {
  console.error("SALTCORN_E2E_TENANT e SALTCORN_E2E_ADMIN_USER_ID são obrigatórios");
  process.exit(1);
}

const config = loadConfig();
const sessionStore = new InMemorySessionStore();
const goClient = new GoClient({ baseUrl: config.goInternalApiUrl, timeoutMs: config.goRequestTimeoutMs });
const router = buildRouter({ config, sessionStore, goClient });
const listener = createRequestListener(router, { config, sessionStore, goClient });
const server = createServer((req, res) => void listener(req, res));

const sessionId = await sessionStore.create({ userId: adminUserId, tenant });
const csrfToken = generateCsrfToken();

const [host, portStr] = config.httpAddr.split(":");
const port = Number(portStr || config.httpAddr);
server.listen(port, host || undefined, () => {
  // Uma linha de JSON em stdout — run.sh a captura para montar o cookie
  // que o Playwright injeta no navegador antes de navegar.
  console.log(JSON.stringify({ sessionId, csrfToken, port }));
});

process.on("SIGTERM", () => process.exit(0));
process.on("SIGINT", () => process.exit(0));
