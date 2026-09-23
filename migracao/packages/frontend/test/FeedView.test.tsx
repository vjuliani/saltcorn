// Testa FeedView.tsx isoladamente — a classificação/consulta real já
// tem cobertura exaustiva do lado Go (internal/views/feed_test.go,
// cmd/server/feed_and_nested_test.go); aqui o foco é só "o React desenha
// os cards e o botão de criar do jeito certo".
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { FeedView, type FeedViewPlan } from "../src/render/FeedView";

function planWithCards(count: number): FeedViewPlan {
  return {
    view_id: 1,
    table: "guitars",
    cards: Array.from({ length: count }, (_, i) => ({
      record_id: i + 1,
      show: {
        view_id: 2,
        table: "guitars",
        record_id: i + 1,
        columns: [{ field_name: "name", header_label: "Nome" }],
        values: { name: `Guitar ${i + 1}` },
      },
    })),
  };
}

describe("FeedView", () => {
  it("desenha um card por linha, cada um via ShowView", () => {
    render(<FeedView plan={planWithCards(2)} />);
    expect(screen.getByTestId("feed-card-1")).toBeTruthy();
    expect(screen.getByTestId("feed-card-2")).toBeTruthy();
    expect(screen.getByText("Guitar 1")).toBeTruthy();
    expect(screen.getByText("Guitar 2")).toBeTruthy();
  });

  it("mostra uma mensagem quando não há nenhum card", () => {
    render(<FeedView plan={planWithCards(0)} />);
    expect(screen.getByTestId("feed-empty")).toBeTruthy();
  });

  it("desenha o botão de criar só quando view_to_create_id está presente, e chama onCreateClick", () => {
    const onCreateClick = vi.fn();
    const plan: FeedViewPlan = { ...planWithCards(1), view_to_create_id: 9, view_to_create_name: "create_guitar" };
    render(<FeedView plan={plan} onCreateClick={onCreateClick} />);
    fireEvent.click(screen.getByTestId("feed-create-button"));
    expect(onCreateClick).toHaveBeenCalled();
  });

  it("não desenha o botão de criar quando view_to_create_id está ausente", () => {
    render(<FeedView plan={planWithCards(1)} />);
    expect(screen.queryByTestId("feed-create-button")).toBeNull();
  });

  it("chama onNextPage com o cursor quando next_cursor está presente", () => {
    const onNextPage = vi.fn();
    const plan: FeedViewPlan = { ...planWithCards(1), next_cursor: "abc123" };
    render(<FeedView plan={plan} onNextPage={onNextPage} />);
    fireEvent.click(screen.getByRole("button", { name: "Próxima página" }));
    expect(onNextPage).toHaveBeenCalledWith("abc123");
  });

  it("não desenha o botão de próxima página quando next_cursor é null", () => {
    const plan: FeedViewPlan = { ...planWithCards(1), next_cursor: null };
    render(<FeedView plan={plan} />);
    expect(screen.queryByRole("button", { name: "Próxima página" })).toBeNull();
  });
});
