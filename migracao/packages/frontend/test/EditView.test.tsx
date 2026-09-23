// Testa EditView.tsx isoladamente, com um EditViewPlan de exemplo — a
// classificação/form_action real já tem cobertura exaustiva do lado Go
// (internal/views/edit_test.go, cmd/server/edit_http_test.go); aqui o
// foco é só "o React desenha o formulário certo e coage os valores do
// jeito que internal/records espera antes de chamar onSubmit".
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { EditView, type EditViewPlan } from "../src/render/EditView";

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

describe("EditView — fieldview upload (GO-051)", () => {
  it("desenha um <input type=\"file\"> para fieldview upload, sem link de arquivo atual quando value é null", () => {
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "photos",
          record_id: 0,
          fields: [{ field_name: "photo", label: "Foto", field_type: "file", fieldview: "upload", required: false, value: null }],
          action_name: "Save",
        }}
      />
    );
    const input = screen.getByLabelText("Foto") as HTMLInputElement;
    expect(input.type).toEqual("file");
    expect(screen.queryByTestId("edit-field-photo-current")).toBeNull();
  });

  it("mostra um link para o arquivo atual quando value já tem um id, via fileDownloadUrl", () => {
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "photos",
          record_id: 5,
          fields: [{ field_name: "photo", label: "Foto", field_type: "file", fieldview: "upload", required: false, value: 42 }],
          action_name: "Save",
        }}
        fileDownloadUrl={(id) => `/api/bff/files/${id}`}
      />
    );
    const link = screen.getByTestId("edit-field-photo-current") as HTMLAnchorElement;
    expect(link.href).toContain("/api/bff/files/42");
  });

  it("ao escolher um arquivo, chama onUploadFile e submete o id devolvido como valor do campo", async () => {
    const onUploadFile = vi.fn().mockResolvedValue(99);
    const onSubmit = vi.fn();
    render(
      <EditView
        plan={{
          view_id: 1,
          table: "photos",
          record_id: 0,
          fields: [{ field_name: "photo", label: "Foto", field_type: "file", fieldview: "upload", required: false, value: null }],
          action_name: "Save",
        }}
        onUploadFile={onUploadFile}
        onSubmit={onSubmit}
      />
    );
    const input = screen.getByLabelText("Foto") as HTMLInputElement;
    const file = new File(["conteudo"], "foto.png", { type: "image/png" });
    fireEvent.change(input, { target: { files: [file] } });
    await waitFor(() => expect(onUploadFile).toHaveBeenCalledWith(file));
    await waitFor(() => expect((screen.getByLabelText("Foto") as HTMLInputElement).disabled).toBe(false));

    fireEvent.click(screen.getByRole("button", { name: "Salvar" }));
    expect(onSubmit).toHaveBeenCalledWith({ photo: 99 });
  });
});

describe("EditView — views aninhadas (GO-051)", () => {
  const nestedPlan: EditViewPlan = {
    view_id: 1,
    table: "guitars",
    record_id: 10,
    _version: "1",
    fields: [{ field_name: "name", label: "Nome", field_type: "text", fieldview: "edit", required: true, value: "Strat" }],
    action_name: "Save",
    nested: [
      {
        view_id: 2,
        view_name: "editprocessed",
        child_table: "processed",
        fk_field: "guitar",
        parent_id: 10,
        rows: [
          {
            view_id: 2,
            table: "processed",
            record_id: 100,
            _version: "1",
            fields: [{ field_name: "heading", label: "Título", field_type: "text", fieldview: "edit", required: true, value: "Capítulo 1" }],
            action_name: "Save",
          },
        ],
      },
    ],
  };

  it("desenha um grupo aninhado com uma sub-EditView por linha filha existente", () => {
    render(<EditView plan={nestedPlan} />);
    expect(screen.getByTestId("nested-group-editprocessed")).toBeTruthy();
    expect(screen.getByTestId("nested-row-editprocessed-100")).toBeTruthy();
    const nestedInput = screen.getByLabelText("Título *") as HTMLInputElement;
    expect(nestedInput.value).toEqual("Capítulo 1");
  });

  it("mostra uma mensagem quando o grupo aninhado não tem nenhuma linha ainda", () => {
    const emptyGroupPlan: EditViewPlan = { ...nestedPlan, nested: [{ ...nestedPlan.nested![0]!, rows: [] }] };
    render(<EditView plan={emptyGroupPlan} />);
    expect(screen.getByTestId("nested-group-editprocessed-empty")).toBeTruthy();
  });

  it("submeter uma linha filha chama onSubmitNested com o plano da LINHA (view_id/record_id próprios), não o da view pai", () => {
    const onSubmitNested = vi.fn();
    render(<EditView plan={nestedPlan} onSubmitNested={onSubmitNested} />);
    fireEvent.change(screen.getByLabelText("Título *"), { target: { value: "Capítulo 1 revisado" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Salvar" })[1]!);
    expect(onSubmitNested).toHaveBeenCalledWith(
      expect.objectContaining({ view_id: 2, record_id: 100 }),
      { heading: "Capítulo 1 revisado" }
    );
  });

  it("nunca desenha <form> aninhado (HTML inválido) — a linha filha vive fora do <form> pai", () => {
    const { container } = render(<EditView plan={nestedPlan} />);
    const forms = container.querySelectorAll("form");
    expect(forms.length).toEqual(2); // um do pai, um da linha filha — irmãos, não aninhados
    for (const form of Array.from(forms)) {
      expect(form.querySelector("form")).toBeNull();
    }
  });
});
