// Testes do EditorPage (GO-019): ciclo criar tabela → criar view →
// reabrir → salvar → conflito → publicar, contra um BffClient mockado
// (não é integração real — isso já tem cobertura em bffClient.test.ts,
// packages/bff/test/editor.test.ts e cmd/server/views_test.go do lado
// Go). O BuilderPanel é mockado aqui: ele já tem sua própria suíte
// (BuilderPanel.test.tsx) e depende de um bundle UMD externo que não
// precisa ser exercitado de novo só para provar que o EditorPage
// encaminha `view.configuration` para ele.
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { EditorPage } from "../src/editor/EditorPage";
import { BffClient, ViewConflictError } from "../src/bffClient";

vi.mock("../src/builder/BuilderPanel", () => ({
  BuilderPanel: () => <div data-testid="builder-container-mock" />,
}));

function makeClient() {
  return {
    createTable: vi.fn(),
    addField: vi.fn(),
    createView: vi.fn(),
    getView: vi.fn(),
    updateView: vi.fn(),
  } as unknown as BffClient & {
    createTable: ReturnType<typeof vi.fn>;
    addField: ReturnType<typeof vi.fn>;
    createView: ReturnType<typeof vi.fn>;
    getView: ReturnType<typeof vi.fn>;
    updateView: ReturnType<typeof vi.fn>;
  };
}

describe("EditorPage", () => {
  let client: ReturnType<typeof makeClient>;

  beforeEach(() => {
    client = makeClient();
  });

  it("cria tabela, cria view e exibe o builder com a configuration recebida", async () => {
    client.createTable.mockResolvedValue({ id: 1, name: "books" });
    client.createView.mockResolvedValue({
      id: 5,
      name: "books_view",
      table_id: 1,
      template: "List",
      min_role: 1,
      configuration: { above: [] },
      _version: "1",
    });

    render(<EditorPage bffClient={client} />);

    fireEvent.change(screen.getByLabelText("Nome da tabela"), { target: { value: "books" } });
    fireEvent.click(screen.getByText("Criar tabela"));
    await waitFor(() => expect(client.createTable).toHaveBeenCalledWith({ name: "books" }));

    fireEvent.click(await screen.findByText('Criar view em "books"'));
    await waitFor(() => expect(client.createView).toHaveBeenCalled());

    expect(await screen.findByTestId("builder-container-mock")).toBeTruthy();
    expect(screen.getByText(/books_view/)).toBeTruthy();
    expect(screen.getByText(/admin \(não publicada\)/)).toBeTruthy();
  });

  it("salva a view e reflete a nova _version, sem exibir conflito", async () => {
    client.createTable.mockResolvedValue({ id: 1, name: "books" });
    client.createView.mockResolvedValue({
      id: 5,
      name: "books_view",
      table_id: 1,
      template: "List",
      min_role: 1,
      configuration: { above: [] },
      _version: "1",
    });
    client.updateView.mockResolvedValue({
      id: 5,
      name: "books_view",
      table_id: 1,
      template: "List",
      min_role: 1,
      configuration: { above: [] },
      _version: "2",
    });

    render(<EditorPage bffClient={client} />);
    fireEvent.change(screen.getByLabelText("Nome da tabela"), { target: { value: "books" } });
    fireEvent.click(screen.getByText("Criar tabela"));
    fireEvent.click(await screen.findByText('Criar view em "books"'));
    await screen.findByTestId("builder-container-mock");

    fireEvent.click(screen.getByText("Salvar"));
    await waitFor(() =>
      expect(client.updateView).toHaveBeenCalledWith(5, { _version: "1", configuration: { above: [] } })
    );
    expect(await screen.findByTestId("status-message")).toHaveTextContent("Salvo.");
    expect(screen.queryByTestId("conflict-message")).toBeNull();
  });

  it("em conflito de edição (ViewConflictError), apresenta o conflito sem sobrescrever o estado local", async () => {
    client.createTable.mockResolvedValue({ id: 1, name: "books" });
    client.createView.mockResolvedValue({
      id: 5,
      name: "books_view",
      table_id: 1,
      template: "List",
      min_role: 1,
      configuration: { above: [] },
      _version: "1",
    });
    client.updateView.mockRejectedValue(new ViewConflictError());

    render(<EditorPage bffClient={client} />);
    fireEvent.change(screen.getByLabelText("Nome da tabela"), { target: { value: "books" } });
    fireEvent.click(screen.getByText("Criar tabela"));
    fireEvent.click(await screen.findByText('Criar view em "books"'));
    await screen.findByTestId("builder-container-mock");

    fireEvent.click(screen.getByText("Salvar"));
    const alert = await screen.findByTestId("conflict-message");
    expect(alert.textContent).toMatch(/Conflito de edição/);
    // A versão local não avançou (continua "1"): uma tentativa seguinte
    // reenviaria a mesma _version, não uma versão fabricada localmente.
    expect(screen.getByText(/admin \(não publicada\)/)).toBeTruthy();
  });

  it("publica a view (min_role público) e reabre para confirmar o estado do servidor", async () => {
    client.createTable.mockResolvedValue({ id: 1, name: "books" });
    client.createView.mockResolvedValue({
      id: 5,
      name: "books_view",
      table_id: 1,
      template: "List",
      min_role: 1,
      configuration: { above: [] },
      _version: "1",
    });
    client.updateView.mockResolvedValue({
      id: 5,
      name: "books_view",
      table_id: 1,
      template: "List",
      min_role: 100,
      configuration: { above: [] },
      _version: "2",
    });
    client.getView.mockResolvedValue({
      id: 5,
      name: "books_view",
      table_id: 1,
      template: "List",
      min_role: 100,
      configuration: { above: [] },
      _version: "2",
    });

    render(<EditorPage bffClient={client} />);
    fireEvent.change(screen.getByLabelText("Nome da tabela"), { target: { value: "books" } });
    fireEvent.click(screen.getByText("Criar tabela"));
    fireEvent.click(await screen.findByText('Criar view em "books"'));
    await screen.findByTestId("builder-container-mock");

    fireEvent.click(screen.getByText("Publicar"));
    await waitFor(() => expect(client.updateView).toHaveBeenCalledWith(5, { _version: "1", min_role: 100 }));
    expect(await screen.findByText(/público \(publicada\)/)).toBeTruthy();

    fireEvent.click(screen.getByText("Reabrir"));
    await waitFor(() => expect(client.getView).toHaveBeenCalledWith(5));
    expect(await screen.findByTestId("status-message")).toHaveTextContent("View recarregada do servidor.");
  });
});
