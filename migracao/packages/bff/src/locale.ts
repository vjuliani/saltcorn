// Resolução de idioma efetivo (GO-047, `routes/utils.ts` do legado —
// `setLanguage`/`applyUserLocale`): mesma ordem de prioridade do
// legado — preferência EXPLÍCITA do usuário (`identity.User.Language`,
// via Go) > cookie `lang` > `default_locale` do tenant (via Go,
// `internal/config`) > "pt" (fallback final, o idioma "nativo" deste
// port desde GO-018). O BFF resolve isto UMA vez, no bootstrap — o
// frontend nunca reimplementa esta cadeia.
export const LANG_COOKIE_NAME = "lang";

/** Extrai o valor de um cookie por nome, sem depender de nenhuma biblioteca de parsing — mesmo padrão de readSessionCookie (session.ts). */
export function readCookie(cookieHeader: string | undefined, name: string): string | null {
  if (!cookieHeader) return null;
  for (const part of cookieHeader.split(";")) {
    const eq = part.indexOf("=");
    if (eq === -1) continue;
    const cookieName = part.slice(0, eq).trim();
    if (cookieName === name) {
      return decodeURIComponent(part.slice(eq + 1).trim());
    }
  }
  return null;
}

/** Set-Cookie para persistir a preferência de idioma quando não há usuário logado (ver nota de escopo em app.ts) — Path=/ e sem HttpOnly (o valor não é sensível, só uma preferência de exibição). */
export function langCookieHeader(language: string): string {
  if (!language) return `${LANG_COOKIE_NAME}=; SameSite=Lax; Path=/; Max-Age=0`;
  return `${LANG_COOKIE_NAME}=${encodeURIComponent(language)}; SameSite=Lax; Path=/; Max-Age=31536000`;
}

/**
 * resolveLocale aplica a cadeia de prioridade do legado —
 * actorLanguage (User.Language, "" = sem preferência) > cookie `lang` >
 * defaultLocale (default_locale do tenant, sempre não-vazio, ver
 * goClient.getActor) — sempre devolve um idioma não-vazio.
 */
export function resolveLocale(actorLanguage: string, cookieHeader: string | undefined, defaultLocale: string): string {
  if (actorLanguage) return actorLanguage;
  const cookieLang = readCookie(cookieHeader, LANG_COOKIE_NAME);
  if (cookieLang) return cookieLang;
  return defaultLocale || "pt";
}

// RTL_LANGUAGES — mesma lista do legado (routes/utils.ts) — nenhum dos
// dois idiomas desta entrega (pt/en) é RTL, mas o mecanismo é portado
// (não hardcoded para "sempre LTR") para não exigir retrabalho quando
// um idioma RTL for adicionado ao catálogo do frontend.
const RTL_LANGUAGES = ["ar", "he", "fa", "ur", "yi"];

export function isRTL(locale: string): boolean {
  return RTL_LANGUAGES.some((lang) => locale.startsWith(lang));
}
