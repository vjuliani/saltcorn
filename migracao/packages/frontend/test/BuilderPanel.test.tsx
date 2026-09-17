// Prova o critério de aceite "builder funciona com mocks": o BuilderPanel
// é testado aqui com um `window.builder.renderBuilder` MOCKADO (simulando
// o bundle Craft.js já carregado) — isolando exatamente a responsabilidade
// deste componente (montar o container, codificar layout/options/mode
// corretamente, chamar a função certa) da responsabilidade do builder em
// si (que já tem sua própria suíte real, incluindo o round-trip novo de
// GO-018 em packages/saltcorn-builder/tests/storage_roundtrip.test.js —
// não duplicado aqui).
import { render, screen, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { BuilderPanel, MOCK_BUILDER_LAYOUT, MOCK_BUILDER_OPTIONS } from "../src/builder/BuilderPanel";

describe("BuilderPanel", () => {
  const renderBuilderMock = vi.fn();

  beforeEach(() => {
    renderBuilderMock.mockClear();
    (window as any).builder = { renderBuilder: renderBuilderMock };
  });

  afterEach(() => {
    delete (window as any).builder;
  });

  it("chama window.builder.renderBuilder com o container, layout e options de mock corretamente codificados", async () => {
    render(<BuilderPanel layout={MOCK_BUILDER_LAYOUT} options={MOCK_BUILDER_OPTIONS} mode="page" />);

    const container = screen.getByTestId("builder-container");
    await waitFor(() => expect(renderBuilderMock).toHaveBeenCalledTimes(1));

    const call = renderBuilderMock.mock.calls[0];
    if (!call) throw new Error("renderBuilder não foi chamado");
    const [containerId, encodedOptions, encodedLayout, mode] = call;
    expect(containerId).toEqual(container.id);
    expect(JSON.parse(decodeURIComponent(encodedLayout))).toEqual(MOCK_BUILDER_LAYOUT);
    expect(JSON.parse(decodeURIComponent(encodedOptions))).toEqual(MOCK_BUILDER_OPTIONS);
    expect(mode).toEqual("page");
  });

  it("não faz nenhuma chamada de rede para montar com dados de mock (sem referência de biblioteca no layout)", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    render(<BuilderPanel layout={MOCK_BUILDER_LAYOUT} options={MOCK_BUILDER_OPTIONS} />);
    await waitFor(() => expect(renderBuilderMock).toHaveBeenCalled());
    expect(fetchSpy).not.toHaveBeenCalled();
    fetchSpy.mockRestore();
  });
});
