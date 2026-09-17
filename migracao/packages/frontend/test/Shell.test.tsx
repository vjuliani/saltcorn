// Prova a outra metade do critério de aceite "topbar e navegação
// funcionam sem disputa de DOM": o botão de colapsar a sidebar
// (Topbar) muda o estado do Shell via onClick — nenhum listener global
// nem atributo data-sidebar-toggler é necessário.
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { Shell } from "../src/components/Shell";
import type { MenuSection } from "../src/types/menu";

const sections: MenuSection[] = [{ items: [{ label: "Início", link: "/" }] }];

describe("Shell", () => {
  it("renderiza sidebar, topbar e o conteúdo filho", () => {
    render(
      <Shell brand={{ name: "Saltcorn" }} sections={sections} currentUrl="/" title="Painel">
        <p>conteúdo da página</p>
      </Shell>
    );
    expect(screen.getByTestId("sidebar")).toBeInTheDocument();
    expect(screen.getByTestId("topbar")).toBeInTheDocument();
    expect(screen.getByText("conteúdo da página")).toBeInTheDocument();
    expect(screen.getByText("Painel")).toBeInTheDocument();
  });

  it("o botão da topbar alterna a classe 'toggled' da sidebar via estado React", () => {
    render(
      <Shell brand={{ name: "Saltcorn" }} sections={sections} currentUrl="/">
        <p>conteúdo</p>
      </Shell>
    );
    const sidebar = screen.getByTestId("sidebar");
    const toggleBtn = screen.getByRole("button", { name: /menu lateral/i });

    expect(sidebar).not.toHaveClass("toggled");
    fireEvent.click(toggleBtn);
    expect(sidebar).toHaveClass("toggled");
    fireEvent.click(toggleBtn);
    expect(sidebar).not.toHaveClass("toggled");
  });
});
