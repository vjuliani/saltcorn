import { readFile, writeFile } from "node:fs/promises";
import assert from "node:assert/strict";
const dir = process.argv[2];
const events = (await readFile(`${dir}/fault-tests.jsonl`, "utf8"))
  .trim()
  .split("\n")
  .map(JSON.parse);
assert(
  !events.some((e) => e.Action === "skip" || e.Action === "fail"),
  "Falha/skip invalida evidência"
);
const tests = events
  .filter((e) => e.Action === "pass" && e.Test)
  .map((e) => `${e.Package}/${e.Test}`);
for (const name of [
  "TestEval_Timeout_IsContained",
  "TestEval_Crash_IsContained",
  "TestEval_MemoryLimit_CrashIsContained",
  "TestLocalBackend_UnavailablePathRecovers",
  "TestUpload_InterruptedUpload_NoCatalogEntryCreated",
])
  assert(
    tests.some((t) => t.endsWith("/" + name)),
    `Evidência obrigatória ausente: ${name}`
  );
const report = JSON.parse(await readFile(`${dir}/report.json`, "utf8"));
assert.equal(report.workloadPassed, true);
report.labPassed = true;
report.componentFaultTests = { passed: tests.length, skipped: 0, tests };
report.productionPromotion = false;
report.productionBlockers = [
  "SLOs e volume de produção não acordados",
  "GO-002 sem baseline HTTP comparável",
  "Paridade GO-033 bloqueia piloto e conversão integral",
  "Host JS e arquivos validados no componente; não conectados ao caminho HTTP ensaiado",
];
await writeFile(`${dir}/report.json`, JSON.stringify(report, null, 2));
console.log(
  JSON.stringify({
    labPassed: true,
    componentTests: tests.length,
    productionPromotion: false,
  })
);
