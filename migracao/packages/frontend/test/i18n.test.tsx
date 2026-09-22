// Testa o mecanismo de tradução isoladamente (GO-047) — a cobertura de
// "os componentes de produto usam t() corretamente" já é responsabilidade
// dos testes de cada componente (ListView/EditView/ViewsListPage/
// Topbar); aqui o foco é só "o motor de tradução resolve chaves, cai no
// fallback certo, e interpola parâmetros do jeito esperado".
import { renderHook } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { I18nProvider, useT, useI18n } from "../src/i18n/I18nContext";
import { translations, SUPPORTED_LOCALES } from "../src/i18n/translations";
import type { ReactNode } from "react";

function wrapper(locale: string) {
  return ({ children }: { children: ReactNode }) => <I18nProvider locale={locale}>{children}</I18nProvider>;
}

describe("i18n", () => {
  it("resolve uma chave conhecida em pt", () => {
    const { result } = renderHook(() => useT(), { wrapper: wrapper("pt") });
    expect(result.current("list.noRecords")).toEqual("Nenhum registro.");
  });

  it("resolve a MESMA chave em en, com texto diferente", () => {
    const { result } = renderHook(() => useT(), { wrapper: wrapper("en") });
    expect(result.current("list.noRecords")).toEqual("No records.");
  });

  it("locale desconhecido cai no fallback pt (nunca uma exceção)", () => {
    const { result } = renderHook(() => useI18n(), { wrapper: wrapper("fr") });
    expect(result.current.locale).toEqual("pt");
    expect(result.current.t("edit.save")).toEqual("Salvar");
  });

  it("chave desconhecida devolve a própria chave (nunca undefined/exceção)", () => {
    const { result } = renderHook(() => useT(), { wrapper: wrapper("pt") });
    expect(result.current("chave.que.nao.existe")).toEqual("chave.que.nao.existe");
  });

  it("interpola {param} no template", () => {
    const { result } = renderHook(() => useT(), { wrapper: wrapper("pt") });
    expect(result.current("views.listError", { error: "timeout" })).toEqual("Erro ao listar views: timeout");
  });

  it("interpola em en também, com o template certo do idioma", () => {
    const { result } = renderHook(() => useT(), { wrapper: wrapper("en") });
    expect(result.current("views.navigateView", { viewName: "books_list" })).toEqual('Saved — would go to view "books_list".');
  });

  it("useT sem I18nProvider usa o fallback pt do Context (nunca lança)", () => {
    const { result } = renderHook(() => useT());
    expect(result.current("edit.save")).toEqual("Salvar");
  });

  // Achado real de escopo (GO-047, decisão de preflight): só 2 idiomas —
  // este teste falharia (com uma mensagem clara) se alguém adicionasse um
  // locale a SUPPORTED_LOCALES sem também adicionar seu catálogo completo
  // em translations.ts, ou vice-versa.
  it("todo locale suportado tem exatamente o mesmo conjunto de chaves que pt", () => {
    const ptKeys = Object.keys(translations.pt).sort();
    for (const locale of SUPPORTED_LOCALES) {
      const keys = Object.keys(translations[locale]).sort();
      expect(keys, `locale ${locale} tem chaves divergentes de pt`).toEqual(ptKeys);
    }
  });
});
