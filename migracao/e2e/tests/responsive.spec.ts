// GO-021 — responsividade real (viewport móvel), não uma suposição sobre
// classes Bootstrap: confirma que o shell não produz overflow horizontal
// e que o botão de colapsar a sidebar (escondido em telas médias/grandes
// via `d-md-none`, Topbar.tsx) fica visível numa tela pequena.
import { test, expect } from "./fixtures";

test("shell não tem overflow horizontal em viewport móvel (375px)", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 667 });
  await page.goto("/");

  const hasHorizontalOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1
  );
  expect(hasHorizontalOverflow).toBe(false);

  await expect(page.locator("#sidebarToggleTop")).toBeVisible();
});

test("shell não tem overflow horizontal em viewport desktop (1280px)", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/");

  const hasHorizontalOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1
  );
  expect(hasHorizontalOverflow).toBe(false);
});
