// Fixture compartilhada: injeta os cookies de sessão/CSRF pré-semeados
// por scripts/start-bff.mjs (via run.sh) no navegador ANTES de qualquer
// navegação — simula "já logado" sem fabricar um formulário de login que
// não existe de verdade (ver nota de escopo 3 em
// docs/migracao-go/execucoes/GO-021.md). `secure: false` é uma
// simplificação deste harness (HTTP local, não HTTPS) — o atributo
// `Secure` em si já tem teste unitário dedicado do lado do BFF.
import { test as base, expect } from "@playwright/test";

interface SeededSession {
  sessionId: string;
  csrfToken: string;
}

function readSession(): SeededSession {
  const raw = process.env.SALTCORN_E2E_SESSION_JSON;
  if (!raw) {
    throw new Error(
      "SALTCORN_E2E_SESSION_JSON não definida — rode via ./run.sh (que sobe Go+BFF+frontend reais e semeia a sessão), não `npx playwright test` diretamente"
    );
  }
  return JSON.parse(raw);
}

export const test = base.extend({
  page: async ({ page, context }, use) => {
    const { sessionId, csrfToken } = readSession();
    await context.addCookies([
      { name: "sc_session", value: sessionId, domain: "localhost", path: "/", httpOnly: true, secure: false, sameSite: "Lax" },
      { name: "sc_csrf", value: csrfToken, domain: "localhost", path: "/", httpOnly: false, secure: false, sameSite: "Lax" },
    ]);
    await use(page);
  },
});

export { expect };
