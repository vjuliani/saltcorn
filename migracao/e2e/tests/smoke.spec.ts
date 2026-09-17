import { test, expect } from "./fixtures";

test("shell carrega com sessão pré-semeada", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Saltcorn")).toBeVisible();
  await expect(page.getByRole("button", { name: /Abrir editor \(conectado ao BFF\)/ })).toBeVisible();
});
