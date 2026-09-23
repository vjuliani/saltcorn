// GO-052 — prova ponta a ponta, em navegador real, do critério de
// aceite central: "um evento nomeado arbitrário emitido via HTTP dispara
// os triggers cujo when_trigger bate com o nome; o trigger
// receive_share_trigger do pack guitars dispara de ponta a ponta e
// persiste a linha real na tabela photos".
//
// Achado real de escopo (não um bug, uma lacuna documentada): não existe
// nenhuma rota HTTP nesta entrega (nem em nenhuma anterior) para
// ADMINISTRAR triggers — só para DISPARÁ-los. Um harness de E2E dirigido
// pelo navegador não tem como criar o fixture do trigger sozinho; por
// isso `cli e2e-seed` (mesmo mecanismo já usado para o usuário
// admin/ownership) semeia diretamente, via Go, uma tabela
// "e2e_seed_photos" e um trigger `when_trigger="ReceiveMobileShareData"`
// que replica EXATAMENTE o código do `receive_share_trigger` real do
// pack piloto guitars (`Table.findOne({name}).insertRow(...)` por
// arquivo em `row.files`). Este teste dispara esse trigger de verdade
// via `page.request` contra `POST /api/bff/events/{eventname}` — o
// mesmo endpoint que o app mobile chamaria (não existe, nem no legado,
// uma UI web para emitir este evento; é um mecanismo API-only por
// natureza, mesmo espírito de nested-feed-upload.spec.ts para a view
// aninhada).
import { test, expect } from "./fixtures";

interface Session {
  csrfToken: string;
}

function readSession(): Session {
  return JSON.parse(process.env.SALTCORN_E2E_SESSION_JSON ?? "{}");
}

test("emitir ReceiveMobileShareData via HTTP dispara receive_share_trigger e persiste as linhas reais (GO-052)", async ({ page }) => {
  const { csrfToken } = readSession();
  const headers = { "x-csrf-token": csrfToken, "content-type": "application/json" };

  await page.goto("/");

  const idempotencyKey1 = `e2e-emit-${Date.now()}-1`;
  const payload = { files: [{ location: `/tmp/e2e-a-${Date.now()}.png` }, { location: `/tmp/e2e-b-${Date.now()}.png` }] };

  const res = await page.request.post("/api/bff/events/ReceiveMobileShareData", {
    headers, data: { payload },
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = (await res.json()) as { fired: number };
  expect(body.fired).toEqual(1);

  // Confirma as linhas REAIS gravadas pelo trigger, uma por arquivo em
  // row.files — não um resultado simulado.
  const recordsRes = await page.request.get("/api/bff/tables/e2e_seed_photos/records", { headers });
  expect(recordsRes.ok(), await recordsRes.text()).toBeTruthy();
  const records = (await recordsRes.json()) as { items: Array<{ photo: string }> };
  const photos = records.items.map((r) => r.photo);
  expect(photos).toContain(payload.files[0]!.location);
  expect(photos).toContain(payload.files[1]!.location);
});

test("um retry com o MESMO corpo nunca dispara o trigger duas vezes (idempotência real, GO-052)", async ({ page }) => {
  const { csrfToken } = readSession();
  const headers = { "x-csrf-token": csrfToken, "content-type": "application/json" };
  const payload = { files: [{ location: `/tmp/e2e-retry-${Date.now()}.png` }] };

  const before = await page.request.get("/api/bff/tables/e2e_seed_photos/records", { headers });
  const beforeCount = ((await before.json()) as { items: unknown[] }).items.length;

  const first = await page.request.post("/api/bff/events/ReceiveMobileShareData", { headers, data: { payload } });
  const second = await page.request.post("/api/bff/events/ReceiveMobileShareData", { headers, data: { payload } });
  expect(first.ok(), await first.text()).toBeTruthy();
  expect(second.ok(), await second.text()).toBeTruthy();

  const after = await page.request.get("/api/bff/tables/e2e_seed_photos/records", { headers });
  const afterCount = ((await after.json()) as { items: unknown[] }).items.length;
  // Exatamente 1 linha nova (a do arquivo único), nunca 2 — a segunda
  // chamada (mesmo corpo, mesma Idempotency-Key calculada pelo BFF) não
  // deveria disparar o trigger de novo.
  expect(afterCount - beforeCount).toEqual(1);
});

test("um nome de evento sem mobile_emit_allowed_events configurado é rejeitado com 403 (GO-052)", async ({ page }) => {
  const { csrfToken } = readSession();
  const headers = { "x-csrf-token": csrfToken, "content-type": "application/json" };

  const res = await page.request.post("/api/bff/events/EventoNuncaConfigurado", { headers, data: { payload: {} } });
  expect(res.status()).toEqual(403);
});
