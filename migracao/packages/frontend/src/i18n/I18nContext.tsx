// Contexto de i18n (GO-047) — o locale efetivo já vem RESOLVIDO do BFF
// (bootstrap, GO-047: User.Language > cookie `lang` > default_locale do
// tenant > "pt") — este componente nunca reimplementa essa cadeia de
// prioridade, só consome o resultado. `t(key, params?)` interpola
// `{nome}` no template, mesmo espírito minimalista de
// `EditView.coerceForSubmit`: sem uma dependência de i18n externa para
// um catálogo de ~20 chaves.
import { createContext, useContext, useMemo, type ReactNode } from "react";
import { translations, type Locale } from "./translations";

export interface I18nContextValue {
  locale: Locale;
  t: (key: string, params?: Record<string, string>) => string;
}

function normalizeLocale(locale: string): Locale {
  return locale === "en" ? "en" : "pt";
}

function translate(locale: Locale, key: string, params?: Record<string, string>): string {
  let template = translations[locale][key] ?? translations.pt[key] ?? key;
  if (params) {
    for (const [name, value] of Object.entries(params)) {
      template = template.split(`{${name}}`).join(value);
    }
  }
  return template;
}

const I18nContext = createContext<I18nContextValue>({
  locale: "pt",
  t: (key, params) => translate("pt", key, params),
});

export function I18nProvider({ locale, children }: { locale: string; children: ReactNode }) {
  const value = useMemo<I18nContextValue>(() => {
    const resolved = normalizeLocale(locale);
    return { locale: resolved, t: (key, params) => translate(resolved, key, params) };
  }, [locale]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  return useContext(I18nContext);
}

export function useT(): I18nContextValue["t"] {
  return useContext(I18nContext).t;
}
