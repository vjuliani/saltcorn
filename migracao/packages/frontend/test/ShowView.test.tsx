// Testa ShowView.tsx isoladamente, com um ShowViewPlan de exemplo — a
// classificação/consulta real já tem cobertura exaustiva do lado Go
// (internal/views/show_test.go); aqui o foco é só "o React desenha o que
// o DTO manda".
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { ShowView } from "../src/render/ShowView";

describe("ShowView", () => {
  it("desenha rótulo e valor de cada coluna", () => {
    render(
      <ShowView
        plan={{
          view_id: 1,
          table: "books",
          record_id: 5,
          columns: [
            { field_name: "title", header_label: "Título" },
            { field_name: "pages", header_label: "Páginas" },
          ],
          values: { title: "Dune", pages: 412 },
        }}
      />
    );
    expect(screen.getByText("Título")).toBeTruthy();
    expect(screen.getByText("Dune")).toBeTruthy();
    expect(screen.getByText("Páginas")).toBeTruthy();
    expect(screen.getByText("412")).toBeTruthy();
  });

  it("desenha célula vazia para valor null/undefined, nunca 'null' literal", () => {
    render(
      <ShowView
        plan={{
          view_id: 1,
          table: "books",
          record_id: 5,
          columns: [{ field_name: "subtitle", header_label: "Subtítulo" }],
          values: { subtitle: null },
        }}
      />
    );
    expect(screen.queryByText("null")).toBeNull();
  });
});
