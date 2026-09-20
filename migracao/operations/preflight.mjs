import { mkdir, writeFile } from "node:fs/promises";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";
const url = new URL(process.env.SALTCORN_GO_TEST_DATABASE_URL);
assert.equal(
  process.env.GO035_DISPOSABLE,
  "1",
  "Confirmação de banco descartável ausente"
);
assert(["postgres:", "postgresql:"].includes(url.protocol));
assert(
  ["127.0.0.1", "localhost"].includes(url.hostname),
  "Somente banco loopback"
);
assert.equal(
  url.pathname,
  "/go035_load",
  "Nome de banco exclusivo obrigatório"
);
assert.equal(url.search, "", "DSN deve ser sem opções adicionais");
assert.equal(process.platform, "linux", "Métricas de recursos exigem /proc");
await mkdir(process.argv[2], { mode: 0o700 }); // Refuse reuse: previous evidence remains immutable.
await writeFile(
  `${process.argv[2]}/preflight.json`,
  JSON.stringify(
    {
      at: new Date().toISOString(),
      database: {
        host: url.hostname,
        port: url.port || "5432",
        name: "go035_load",
      },
      node: process.version,
      base: execFileSync("git", ["rev-parse", "HEAD"], {
        encoding: "utf8",
      }).trim(),
      profile: "laboratorio-provisorio",
      productionPromotion: false,
    },
    null,
    2
  )
);

const paths = execFileSync(
  "git",
  [
    "ls-files",
    "--cached",
    "--others",
    "--exclude-standard",
    "-z",
    "migracao",
    "docs/migracao-go",
    ".github/workflows",
  ],
  { encoding: "utf8" }
)
  .split("\0")
  .filter(Boolean)
  .sort();
const hashes = {};
for (const path of paths)
  hashes[path] = createHash("sha256")
    .update(await readFile(path))
    .digest("hex");
await writeFile(
  `${process.argv[2]}/source-manifest.json`,
  JSON.stringify(hashes, null, 2)
);
