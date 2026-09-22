// Testa EditView.tsx isoladamente, com um EditViewPlan de exemplo — a
// classificação/form_action real já tem cobertura exaustiva do lado Go
// (internal/views/edit_test.go, cmd/server/edit_http_test.go); aqui o
// foco é só "o React desenha o formulário certo e coage os valores do
// jeito que internal/records espera antes de chamar onSubmit".
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { EditView } from "../src/render/EditView";

describe("EditView", () => {
  it("desenha um <input> por campo de texto, preenchido com o valor atual", () => {
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "books",
          record_id: 5,
          _version: "3",
          fields: [{ field_name: "title", label: "Título", field_type: "text", fieldview: "edit", required: true, value: "Dune" }],
          action_name: "Save",
        }}
      />
    );
    const input = screen.getByLabelText("Título *") as HTMLInputElement;
    expect(input.value).toEqual("Dune");
  });

  it("desenha um <select> com as opções reais para fieldview select (FieldKey)", () => {
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "processed",
          record_id: 0,
          fields: [
            {
              field_name: "guitar",
              label: "guitar",
              field_type: "key",
              fieldview: "select",
              required: false,
              value: null,
              options: [
                { id: 1, label: "Stratocaster" },
                { id: 2, label: "Les Paul" },
              ],
            },
          ],
          action_name: "Save",
        }}
      />
    );
    expect(screen.getByText("Stratocaster")).toBeTruthy();
    expect(screen.getByText("Les Paul")).toBeTruthy();
  });

  it("submete valores coagidos para número em campos integer/float/key, string em text", () => {
    const onSubmit = vi.fn();
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "books",
          record_id: 0,
          fields: [
            { field_name: "title", label: "title", field_type: "text", fieldview: "edit", required: true, value: null },
            { field_name: "pages", label: "pages", field_type: "integer", fieldview: "edit", required: false, value: null },
          ],
          action_name: "Save",
        }}
        onSubmit={onSubmit}
      />
    );
    fireEvent.change(screen.getByLabelText("title *"), { target: { value: "Neuromancer" } });
    fireEvent.change(screen.getByLabelText("pages"), { target: { value: "271" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar" }));
    expect(onSubmit).toHaveBeenCalledWith({ title: "Neuromancer", pages: 271 });
  });

  it("omite do envio um campo deixado em branco (nunca envia string vazia)", () => {
    const onSubmit = vi.fn();
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "books",
          record_id: 0,
          fields: [
            { field_name: "title", label: "title", field_type: "text", fieldview: "edit", required: true, value: "x" },
            { field_name: "pages", label: "pages", field_type: "integer", fieldview: "edit", required: false, value: null },
          ],
          action_name: "Save",
        }}
        onSubmit={onSubmit}
      />
    );
    fireEvent.click(screen.getByRole("button", { name: "Salvar" }));
    expect(onSubmit).toHaveBeenCalledWith({ title: "x" });
  });

  it("desenha um <input type=\"date\"> nativo para campo date, populado com AAAA-MM-DD a partir do RFC3339 do Go (GO-042, substitui flatpickr)", () => {
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "processed",
          record_id: 7,
          fields: [
            { field_name: "date_processed", label: "Data", field_type: "date", fieldview: "flatpickr", required: false, value: "2024-01-15T00:00:00Z" },
          ],
          action_name: "Save",
        }}
      />
    );
    const input = screen.getByLabelText("Data") as HTMLInputElement;
    expect(input.type).toEqual("date");
    expect(input.value).toEqual("2024-01-15");
  });

  it("submete um campo date como RFC3339 completo (GO-042 — o Go só aceita RFC3339, nunca AAAA-MM-DD isolado)", () => {
    const onSubmit = vi.fn();
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "processed",
          record_id: 0,
          fields: [{ field_name: "date_processed", label: "Data", field_type: "date", fieldview: "flatpickr", required: false, value: null }],
          action_name: "Save",
        }}
        onSubmit={onSubmit}
      />
    );
    fireEvent.change(screen.getByLabelText("Data"), { target: { value: "2024-03-20" } });
    fireEvent.click(screen.getByRole("button", { name: "Salvar" }));
    expect(onSubmit).toHaveBeenCalledWith({ date_processed: "2024-03-20T00:00:00Z" });
  });

  it("rotula o botão como SubmitWithAjax mapeia para 'Salvar' também", () => {
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "process_type",
          record_id: 0,
          fields: [{ field_name: "name", label: "name", field_type: "text", fieldview: "edit", required: true, value: null }],
          action_name: "SubmitWithAjax",
        }}
      />
    );
    expect(screen.getByRole("button", { name: "Salvar" })).toBeTruthy();
  });
});
