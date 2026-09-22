// Testes do ViewsListPage (GO-020): listar, pré-visualizar uma view
// compatível, e apresentar o motivo de uma view incompatível (nunca uma
// tabela quebrada) — contra um BffClient mockado (a classificação real já
// tem cobertura em internal/views/render_test.go e
// cmd/server/render_test.go do lado Go).
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ViewsListPage } from "../src/render/ViewsListPage";
import { BffClient, ViewUnsupportedError } from "../src/bffClient";
import { I18nProvider } from "../src/i18n/I18nContext";

function makeClient() {
  return {
    listViews: vi.fn(),
    renderView: vi.fn(),
    deleteViewRow: vi.fn(),
    submitView: vi.fn(),
  } as unknown as BffClient & {
    listViews: ReturnType<typeof vi.fn>;
    renderView: ReturnType<typeof vi.fn>;
    deleteViewRow: ReturnType<typeof vi.fn>;
    submitView: ReturnType<typeof vi.fn>;
  };
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

  // GO-047: mesma página, mesmos dados, só o I18nProvider muda — prova
  // que ViewsListPage reage de verdade ao locale efetivo (não só que o
  // motor de tradução isolado funciona, ver i18n.test.tsx).
  it("com I18nProvider locale=\"en\", desenha os textos fixos em inglês (dados do usuário continuam intactos)", async () => {
    client.listViews.mockResolvedValue([{ id: 1, name: "booklist", template: "List", min_role: 100 }]);
    render(
      <I18nProvider locale="en">
        <ViewsListPage bffClient={client} />
      </I18nProvider>
    );

    expect(await screen.findByText("booklist")).toBeTruthy(); // dado do usuário, nunca traduzido
    expect(screen.getByText("published")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Preview" })).toBeTruthy();
    expect(screen.getByText("Name")).toBeTruthy();
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

  // GO-039: Show/Edit — o mesmo botão "Visualizar" agora pode desenhar
  // três shapes diferentes; estes testes provam que a página despacha
  // corretamente para cada um.
  it("pré-visualiza uma view Show, desenhando o ShowView com os valores reais", async () => {
    client.listViews.mockResolvedValue([{ id: 2, name: "showbook", template: "Show", min_role: 100 }]);
    client.renderView.mockResolvedValue({
      view_id: 2,
      table: "books",
      record_id: 1,
      columns: [{ field_name: "title", header_label: "Título" }],
      values: { title: "Dune" },
    });
    render(<ViewsListPage bffClient={client} />);

    fireEvent.click(await screen.findByText("Visualizar"));
    expect(await screen.findByTestId("show-view")).toBeTruthy();
    expect(screen.getByText("Dune")).toBeTruthy();
  });

  it("pré-visualiza uma view Edit em branco (criação), desenhando o EditView", async () => {
    client.listViews.mockResolvedValue([{ id: 3, name: "createbook", template: "Edit", min_role: 100 }]);
    client.renderView.mockResolvedValue({
      view_id: 3,
      table: "books",
      record_id: 0,
      fields: [{ field_name: "title", label: "title", field_type: "text", fieldview: "edit", required: true, value: null }],
      action_name: "Save",
    });
    render(<ViewsListPage bffClient={client} />);

    fireEvent.click(await screen.findByText("Visualizar"));
    expect(await screen.findByTestId("edit-view")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Salvar" })).toBeTruthy();
  });

  it("clicar em 'Excluir' numa coluna de ação chama deleteViewRow com id/versão da linha e recarrega", async () => {
    client.listViews.mockResolvedValue([{ id: 1, name: "booklist", template: "List", min_role: 100 }]);
    client.renderView.mockResolvedValue({
      view_id: 1,
      columns: [
        { kind: "field", field_name: "title", header_label: "Título" },
        { kind: "action", action_name: "Delete" },
      ],
      rows: [{ id: 7, _version: "3", title: "Dune" }],
      order_by: "id",
      descending: false,
      next_cursor: null,
    });
    client.deleteViewRow.mockResolvedValue(undefined);
    render(<ViewsListPage bffClient={client} />);

    fireEvent.click(await screen.findByText("Visualizar"));
    fireEvent.click(await screen.findByText("Excluir"));
    await waitFor(() => expect(client.deleteViewRow).toHaveBeenCalledWith(1, 7, "3"));
    // Recarrega a mesma view depois de excluir.
    await waitFor(() => expect(client.renderView).toHaveBeenCalledTimes(2));
  });

  it("submeter o EditView chama submitView e mostra a decisão de navegação", async () => {
    client.listViews.mockResolvedValue([{ id: 3, name: "createbook", template: "Edit", min_role: 100 }]);
    client.renderView.mockResolvedValue({
      view_id: 3,
      table: "books",
      record_id: 0,
      fields: [{ field_name: "title", label: "title", field_type: "text", fieldview: "edit", required: true, value: null }],
      action_name: "Save",
    });
    client.submitView.mockResolvedValue({ record: { id: 9, title: "Neuromancer" }, navigate: { type: "reload" } });
    render(<ViewsListPage bffClient={client} />);

    fireEvent.click(await screen.findByText("Visualizar"));
    const input = (await screen.findByLabelText("title *")) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Neuromancer" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar" }));

    await waitFor(() => expect(client.submitView).toHaveBeenCalledWith(3, { record_id: undefined, _version: undefined, values: { title: "Neuromancer" } }));
    expect(await screen.findByTestId("views-navigate-message")).toBeTruthy();
  });
});
