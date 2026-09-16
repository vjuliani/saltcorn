// CSRF — padrão de duplo envio ("double submit cookie"), exigido em toda
// mutação autenticada por sessão de navegador (ADR-0003/ADR-0007,
// contrato bff-api.yaml: header `X-CSRF-Token`). Implementado à mão
// (mecanismo simples e bem entendido, comparação em tempo constante) em
// vez de uma biblioteca — mesmo espírito de identity.VerifyAPIToken no
// Go: a parte que importa acertar (comparação sem vazar timing) é
// pequena e auditável aqui.

import { randomBytes } from "node:crypto";
import { constantTimeEqual } from "./session.js";

export const CSRF_COOKIE_NAME = "sc_csrf";
export const CSRF_HEADER_NAME = "x-csrf-token";

export function generateCsrfToken(): string {
  return randomBytes(32).toString("base64url");
}

/** Cookie legível por JS (sem HttpOnly) — o duplo-envio depende do front-end conseguir ler e ecoar no header. */
export function csrfCookieHeader(token: string): string {
  return `${CSRF_COOKIE_NAME}=${token}; Secure; SameSite=Lax; Path=/`;
}

export function readCsrfCookie(cookieHeader: string | undefined): string | null {
  if (!cookieHeader) return null;
  for (const part of cookieHeader.split(";")) {
    const eq = part.indexOf("=");
    if (eq === -1) continue;
    const name = part.slice(0, eq).trim();
    if (name === CSRF_COOKIE_NAME) {
      return decodeURIComponent(part.slice(eq + 1).trim());
    }
  }
  return null;
}

/** verifyCsrf confere que o cookie e o header existem, são não-vazios, e batem — em tempo constante. */
export function verifyCsrf(cookieToken: string | null, headerToken: string | null | undefined): boolean {
  if (!cookieToken || !headerToken) return false;
  return constantTimeEqual(cookieToken, headerToken);
}
