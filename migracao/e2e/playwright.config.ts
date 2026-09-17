// Config do Playwright para o E2E do stack NOVO (GO-021) — distinto de
// deploy/playwright (legado, Node/Express). Só chromium/firefox: webkit
// falha por uma dependência de sistema ausente neste ambiente
// (libavif16) sem acesso root para instalar — ver nota de escopo 2 em
// docs/migracao-go/execucoes/GO-021.md, limitação verificada, não
// evitada por conveniência.
import { defineConfig, devices } from "@playwright/test";

const FRONTEND_PORT = Number(process.env.SALTCORN_E2E_FRONTEND_PORT ?? 4173);

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: `http://localhost:${FRONTEND_PORT}`,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] } },
  ],
});
