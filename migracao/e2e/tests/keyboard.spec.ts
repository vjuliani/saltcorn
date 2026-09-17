// GO-021 — cobertura mínima de teclado/acessibilidade real (navegador de
// verdade, não jsdom): o botão de colapsar a sidebar (GO-018,
// Topbar.tsx) é alcançável via foco programático e operável via teclado,
// sem depender de clique de mouse.
import { test, expect } from "./fixtures";

test("botão de colapsar a sidebar é operável por teclado (Enter) e reflete aria-pressed", async ({ page }) => {
  // O botão só é visível em telas pequenas (`d-md-none`, Topbar.tsx) — um
  // elemento display:none nunca recebe foco, então este teste precisa de
  // um viewport móvel para exercitar exatamente o cenário em que o botão
  // existe de verdade (achado deste teste: falhava em silêncio com
  // "toBeFocused() = inactive" no viewport desktop padrão do Playwright).
  await page.setViewportSize({ width: 375, height: 667 });
  await page.goto("/");

  const toggle = page.locator("#sidebarToggleTop");
  const sidebar = page.locator("#accordionSidebar");

  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  await expect(sidebar).not.toHaveClass(/toggled/);

  await toggle.focus();
  await expect(toggle).toBeFocused();
  await page.keyboard.press("Enter");

  await expect(sidebar).toHaveClass(/toggled/);
  await expect(toggle).toHaveAttribute("aria-pressed", "true");

  // Repetir alterna de volta — o estado do teclado é o mesmo estado do clique (useState do React, GO-018), não um caminho separado.
  await page.keyboard.press("Enter");
  await expect(sidebar).not.toHaveClass(/toggled/);
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
});
