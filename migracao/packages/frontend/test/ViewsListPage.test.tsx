// Testes do ViewsListPage (GO-020): listar, pré-visualizar uma view
// compatível, e apresentar o motivo de uma view incompatível (nunca uma
// tabela quebrada) — contra um BffClient mockado (a classificação real já
// tem cobertura em internal/views/render_test.go e
// cmd/server/render_test.go do lado Go).
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ViewsListPage } from "../src/render/ViewsListPage";
import { BffClient, ViewUnsupportedError } from "../src/bffClient";

function makeClient() {
  return {
    listViews: vi.fn(),
    renderView: vi.fn(),
  } as unknown as BffClient & { listViews: ReturnType<typeof vi.fn>; renderView: ReturnType<typeof vi.fn> };
}

describe("ViewsListPage", () => {
  let client: ReturnType<typeof makeClient>;

  beforeEach(() => {
    client = makeClient();
  });

  it("lista as views com nome, template e status (publicada/rascunho)", async () => {
    client.listViews.mockResolvedValue([
      { id: 1, name: "booklist", template: "List", min_role: 100 },
      { id: 2, name: "showbook", template: "Show", min_role: 1 },
    ]);
    render(<ViewsListPage bffClient={client} />);

    expect(await screen.findByText("booklist")).toBeTruthy();
    expect(screen.getByText("showbook")).toBeTruthy();
    expect(screen.getByText("publicada")).toBeTruthy();
    expect(screen.getByText("rascunho")).toBeTruthy();
  });

  it("pré-visualiza uma view compatível, desenhando o ListView", async () => {
    client.listViews.mockResolvedValue([{ id: 1, name: "booklist", template: "List", min_role: 100 }]);
    client.renderView.mockResolvedValue({
      view_id: 1,
      columns: [{ field_name: "title", header_label: "Título" }],
      rows: [{ title: "Dune" }],
      order_by: "id",
      descending: false,
      next_cursor: null,
    });
    render(<ViewsListPage bffClient={client} />);

    fireEvent.click(await screen.findByText("Visualizar"));
    expect(await screen.findByText("Dune")).toBeTruthy();
    expect(client.renderView).toHaveBeenCalledWith(1, {});
  });

  it("apresenta o motivo de uma view incompatível, sem tentar desenhar uma tabela", async () => {
    client.listViews.mockResolvedValue([{ id: 2, name: "showbook", template: "Show", min_role: 1 }]);
    client.renderView.mockRejectedValue(new ViewUnsupportedError('template "Show" não suportado neste runtime (só "List")'));
    render(<ViewsListPage bffClient={client} />);

    fireEvent.click(await screen.findByText("Visualizar"));
    const message = await screen.findByTestId("views-unsupported");
    expect(message.textContent).toMatch(/não suportado pelo novo runtime/);
    expect(message.textContent).toMatch(/template "Show" não suportado/);
    expect(screen.queryByRole("table", { name: "" })).toBeTruthy(); // a tabela de listagem continua, só não há ListView
  });

  it("exibe erro ao falhar em listar views", async () => {
    client.listViews.mockRejectedValue(Object.assign(new Error("indisponível"), { status: 502, code: "domain_unavailable" }));
    render(<ViewsListPage bffClient={client} />);
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(screen.getByRole("alert").textContent).toMatch(/indisponível/);
  });
});
