// GO-053 — prova ponta a ponta, em navegador real: POST .../notifications/
// share-handler dispara Dispatcher.EmitEvent("ReceiveMobileShareData",
// ...) de ponta a ponta — mesmo mecanismo já provado em emit-event.
// spec.ts, agora acionável também por este caminho HTTP dedicado, sem
// exigir o cabeçalho X-CSRF-Token (o POST nativo do Web Share Target do
// navegador não tem como anexá-lo).
//
// GET /manifest.json fica FORA deste harness de propósito: esta suíte
// sobe o BFF via buildRouter puro (scripts/start-bff.mjs, o mesmo modo
// "hospedado" que toda outra rota autenticada usa) — manifest.json só é
// servido em modo SELF-HOSTED (selfHostedListener, tenant fixo da
// instalação), que este harness não exercita. Cobertura real de
// manifest.json vive em cmd/server/pwa_test.go (Go) e
// migracao/packages/bff/test/selfhost.test.ts (BFF) — ver
// docs/migracao-go/execucoes/GO-053.md.
//
// O fixture receive_share_trigger lê `row.files` (compartilhamento de
// ARQUIVO) — fora do escopo desta task (ver
// docs/migracao-go/execucoes/GO-053.md: share-handler aqui só cobre
// título/texto/URL, `application/x-www-form-urlencoded`). Por isso este
// teste prova o DESPACHO (fired=1) e a idempotência, não um novo efeito
// colateral em `e2e_seed_photos` — esse efeito já está provado à
// exaustão em emit-event.spec.ts.
import { test, expect } from "./fixtures";

test("POST .../notifications/share-handler dispara ReceiveMobileShareData sem exigir CSRF (GO-053)", async ({ page }) => {
  await page.goto("/");
  const body = new URLSearchParams({ title: "olhem isso", url: `https://example.com/${Date.now()}` }).toString();

  // Deliberadamente SEM x-csrf-token — a mesma requisição que um POST
  // nativo do Web Share Target faria.
  const res = await page.request.post("/api/bff/notifications/share-handler", {
    headers: { "content-type": "application/x-www-form-urlencoded" },
    data: body,
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const result = (await res.json()) as { fired: number };
  expect(result.fired).toEqual(1);
});

test("um retry com o MESMO corpo nunca dispara o trigger duas vezes (GO-053)", async ({ page }) => {
  await page.goto("/");
  const body = new URLSearchParams({ title: "compartilhado de novo", url: `https://example.com/${Date.now()}` }).toString();
  const headers = { "content-type": "application/x-www-form-urlencoded" };

  const first = await page.request.post("/api/bff/notifications/share-handler", { headers, data: body });
  const second = await page.request.post("/api/bff/notifications/share-handler", { headers, data: body });
  expect(first.ok(), await first.text()).toBeTruthy();
  expect(second.ok(), await second.text()).toBeTruthy();
  const firstBody = (await first.json()) as { fired: number };
  const secondBody = (await second.json()) as { fired: number };
  expect(secondBody).toEqual(firstBody);
});

test("share-handler sem sessão retorna 401 (GO-053)", async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  const res = await page.request.post("/api/bff/notifications/share-handler", {
    headers: { "content-type": "application/x-www-form-urlencoded" },
    data: new URLSearchParams({ title: "anonimo" }).toString(),
  });
  expect(res.status()).toEqual(401);
  await context.close();
});
