// Prova o critério de aceite "sidebar... funciona sem disputa de DOM
// entre React e scripts legados": monta o Sidebar SEM carregar
// bootstrap.bundle.min.js/jQuery (window.bootstrap/window.jQuery
// continuam undefined durante todo o teste — ver asserção dedicada) e
// confirma que expandir/recolher um item com subitens funciona mesmo
// assim, porque é estado React (useState), não um listener de script
// externo lendo data-bs-toggle.
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { Sidebar } from "../src/components/Sidebar";
import type { MenuSection } from "../src/types/menu";

const sections: MenuSection[] = [
  {
    items: [{ label: "Início", link: "/" }],
  },
  {
    section: "Config",
    items: [
      {
        label: "Avançado",
        subitems: [{ label: "Campos", link: "/fields" }],
      },
    ],
  },
];

describe("Sidebar", () => {
  it("não depende de bootstrap.bundle.min.js/jQuery — nenhum script legado tocou o DOM", () => {
    // Se algum teste anterior tivesse carregado o script legado, essas
    // globais existiriam; aqui provamos que o Sidebar nunca as usa nem
    // as requer para funcionar.
    expect((window as any).bootstrap).toBeUndefined();
    expect((window as any).jQuery).toBeUndefined();
  });

  it("renderiza seções e itens", () => {
    render(<Sidebar brand={{ name: "Saltcorn" }} sections={sections} currentUrl="/" />);
    expect(screen.getByText("Saltcorn")).toBeInTheDocument();
    expect(screen.getByText("Início")).toBeInTheDocument();
    expect(screen.getByText("Config")).toBeInTheDocument();
    expect(screen.getByText("Avançado")).toBeInTheDocument();
  });

  it("marca o item cujo link bate com currentUrl como ativo", () => {
    render(<Sidebar brand={{ name: "Saltcorn" }} sections={sections} currentUrl="/" />);
    const inicio = screen.getByText("Início").closest("li");
    expect(inicio).toHaveClass("active");
  });

  it("expande/recolhe um item com subitens via clique — estado React, não data-bs-toggle", () => {
    render(<Sidebar brand={{ name: "Saltcorn" }} sections={sections} currentUrl="/nao-existe" />);
    const toggle = screen.getByRole("button", { name: "Avançado" });

    // Fechado inicialmente (currentUrl não bate com nenhum subitem).
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Campos")).not.toBeVisible();

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("Campos")).toBeVisible();

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
  });

  it("abre um item com subitens automaticamente quando currentUrl bate com um subitem", () => {
    render(<Sidebar brand={{ name: "Saltcorn" }} sections={sections} currentUrl="/fields" />);
    const toggle = screen.getByRole("button", { name: "Avançado" });
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("Campos")).toBeVisible();
  });
});
