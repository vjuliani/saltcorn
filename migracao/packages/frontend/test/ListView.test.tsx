// Testa ListView.tsx isoladamente, com um ListViewPlan de exemplo — a
// classificação/consulta real já tem cobertura exaustiva do lado Go
// (internal/views/render_test.go, cmd/server/render_test.go); aqui o foco
// é só "o React desenha o que o DTO manda", nada mais.
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { ListView } from "../src/render/ListView";

describe("ListView", () => {
  it("desenha cabeçalhos e linhas do plano", () => {
    render(
      <ListView
        plan={{
          view_id: 1,
          columns: [
            { field_name: "title", header_label: "Título" },
            { field_name: "pages", header_label: "pages" },
          ],
          rows: [
            { title: "Dune", pages: 412 },
            { title: "Foundation", pages: 255 },
          ],
          order_by: "id",
          descending: false,
          next_cursor: null,
        }}
      />
    );
    expect(screen.getByText("Título")).toBeTruthy();
    expect(screen.getByText("Dune")).toBeTruthy();
    expect(screen.getByText("412")).toBeTruthy();
  });

  it("mostra 'Nenhum registro.' quando rows está vazio", () => {
    render(<ListView plan={{ view_id: 1, columns: [{ field_name: "title", header_label: "Título" }], rows: [], order_by: "id", descending: false }} />);
    expect(screen.getByText("Nenhum registro.")).toBeTruthy();
  });

  it("exibe o botão de próxima página só quando há next_cursor, e chama onNextPage com o cursor", () => {
    const onNextPage = vi.fn();
    render(
      <ListView
        plan={{ view_id: 1, columns: [{ field_name: "title", header_label: "Título" }], rows: [{ title: "Dune" }], order_by: "id", descending: false, next_cursor: "2" }}
        onNextPage={onNextPage}
      />
    );
    fireEvent.click(screen.getByText("Próxima página"));
    expect(onNextPage).toHaveBeenCalledWith("2");
  });

  it("não mostra botão de próxima página quando next_cursor é null", () => {
    render(<ListView plan={{ view_id: 1, columns: [{ field_name: "title", header_label: "Título" }], rows: [], order_by: "id", descending: false, next_cursor: null }} />);
    expect(screen.queryByText("Próxima página")).toBeNull();
  });
});
