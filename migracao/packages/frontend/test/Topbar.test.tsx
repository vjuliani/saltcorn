// Testa o seletor de idioma da Topbar (GO-047) — o resto do componente
// (toggle da sidebar) já é coberto por Shell.test.tsx.
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { Topbar } from "../src/components/Topbar";

describe("Topbar — seletor de idioma (GO-047)", () => {
  it("não desenha o seletor quando onChangeLocale não é passado", () => {
    render(<Topbar collapsed={false} onToggleSidebar={() => {}} />);
    expect(screen.queryByTestId("locale-select")).not.toBeInTheDocument();
  });

  it("desenha o seletor com pt/en quando onChangeLocale é passado, valor = locale atual", () => {
    render(<Topbar collapsed={false} onToggleSidebar={() => {}} locale="en" onChangeLocale={() => {}} />);
    const select = screen.getByTestId("locale-select") as HTMLSelectElement;
    expect(select.value).toEqual("en");
    expect(screen.getByRole("option", { name: "PT" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "EN" })).toBeInTheDocument();
  });

  it("chama onChangeLocale com o novo valor ao trocar a seleção", () => {
    const onChangeLocale = vi.fn();
    render(<Topbar collapsed={false} onToggleSidebar={() => {}} locale="pt" onChangeLocale={onChangeLocale} />);
    fireEvent.change(screen.getByTestId("locale-select"), { target: { value: "en" } });
    expect(onChangeLocale).toHaveBeenCalledWith("en");
  });

  it("aria-label do botão de colapsar é traduzido pelo locale atual do I18nContext", () => {
    // Sem I18nProvider explícito: cai no fallback "pt" do Context
    // (mesmo comportamento documentado em i18n.test.tsx).
    render(<Topbar collapsed={false} onToggleSidebar={() => {}} />);
    expect(screen.getByRole("button", { name: "Recolher menu lateral" })).toBeInTheDocument();
  });
});
