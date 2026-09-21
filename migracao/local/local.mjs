// Local development only. Persistent setup and process locks are owned by the
// existing installation CLI; no destructive E2E seed or custom session bypass.
import { spawn } from "node:child_process";
import { isDeepStrictEqual } from "node:util";
import { readFile, writeFile, mkdir, access, rename } from "node:fs/promises";
import { randomBytes } from "node:crypto";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";
import path from "node:path";

const local = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(local, "../..");
const state = path.join(local, ".state");
const instance = path.join(state, "instance");
const command = process.argv[2];
const exists = async (file) =>
  access(file).then(
    () => true,
    () => false
  );
const json = async (file) => JSON.parse(await readFile(file, "utf8"));
const config = await json(
  path.join(
    local,
    (await exists(path.join(local, "config.json")))
      ? "config.json"
      : "config.example.json"
  )
);
const ports = ["postgresPort", "backendPort", "bffPort", "frontendPort"].map(
  (k) => config[k]
);
if (
  new Set(ports).size !== 4 ||
  ports.some((p) => !Number.isInteger(p) || p < 1024 || p > 65535)
)
  throw Error("Configure quatro portas distintas entre 1024 e 65535");
if (
  !/^[a-z][a-z0-9_]{0,47}$/.test(config.tenant) ||
  ["public", "information_schema"].includes(config.tenant) ||
  config.tenant.startsWith("pg_")
)
  throw Error("Tenant local inválido");
if (!config.adminEmail || !/^[a-zA-Z0-9_.-]+$/.test(config.dockerContext))
  throw Error("E-mail/contexto Docker inválido");

function run(bin, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(bin, args, { cwd: repo, stdio: "inherit", ...options });
    child.once("error", reject);
    child.once("exit", (code, signal) =>
      code === 0
        ? resolve()
        : reject(Error(`${path.basename(bin)} terminou (${signal || code})`))
    );
  });
}
async function compose(...args) {
  await run(
    "docker",
    [
      "--context",
      config.dockerContext,
      "compose",
      "-f",
      path.join(local, "compose.yaml"),
      ...args,
    ],
    {
      env: {
        ...process.env,
        SALTCORN_LOCAL_PG_PORT: String(config.postgresPort),
      },
    }
  );
}
async function releasePath() {
  if (!(await exists(path.join(state, "release.txt"))))
    throw Error("Execute primeiro: migracao/local/local.sh setup");
  return (await readFile(path.join(state, "release.txt"), "utf8")).trim();
}
async function compatibleSettings() {
  if (
    (await exists(path.join(state, "settings.json"))) &&
    !isDeepStrictEqual(await json(path.join(state, "settings.json")), config)
  ) {
    throw Error(
      "Configuração diverge da instância existente. Restaure config.json; não altere portas/tenant após setup sem migrar a instância."
    );
  }
}
async function checkPort(port) {
  const server = createServer();
  await new Promise((resolve, reject) =>
    server
      .once("error", () =>
        reject(Error(`Porta ${port} ocupada; nenhum processo foi encerrado`))
      )
      .listen(port, "127.0.0.1", resolve)
  );
  await new Promise((resolve) => server.close(resolve));
}
async function build() {
  if (await exists(path.join(instance, "instance.json")))
    await run(path.join(await releasePath(), "bin/cli"), [
      "check",
      "--dir",
      instance,
    ]);
  const release = path.join(
    state,
    "releases",
    `${Date.now()}-${randomBytes(3).toString("hex")}`
  );
  await run("bash", [
    path.join(repo, "migracao/distribution/build.sh"),
    release,
  ], { env: { ...process.env, VITE_BFF_BASE_URL: "" } });
  await writeFile(path.join(state, "release.next"), release + "\n", {
    mode: 0o600,
  });
  await rename(
    path.join(state, "release.next"),
    path.join(state, "release.txt")
  );
}
async function setup() {
  if (!process.version.startsWith("v22."))
    throw Error("Use Node.js 22 para esta release");
  await compatibleSettings();
  if (await exists(path.join(state, "settings.json"))) {
    for (const file of ["postgres.env", "admin-password"])
      if (!(await exists(path.join(state, file))))
        throw Error(
          `Estado local incompleto: restaure ${file} do backup; credenciais não serão regeneradas`
        );
  }
  await mkdir(state, { recursive: true, mode: 0o700 });
  for (const [file, content] of [
    [
      "postgres.env",
      `POSTGRES_USER=saltcorn_local\nPOSTGRES_DB=saltcorn_local\nPOSTGRES_PASSWORD=${randomBytes(24).toString("hex")}\n`,
    ],
    ["admin-password", randomBytes(24).toString("hex") + "\n"],
  ])
    if (!(await exists(path.join(state, file))))
      await writeFile(path.join(state, file), content, {
        mode: 0o600,
        flag: "wx",
      });
  if (!(await exists(path.join(state, "settings.json"))))
    await writeFile(path.join(state, "settings.json"), JSON.stringify(config), {
      mode: 0o600,
      flag: "wx",
    });
  await compose("up", "-d", "--wait", "postgres");
  if (!(await exists(path.join(state, "release.txt")))) await build();
  const cli = path.join(await releasePath(), "bin/cli");
  if (!(await exists(path.join(instance, "instance.json")))) {
    const envFile = await readFile(path.join(state, "postgres.env"), "utf8");
    const password = envFile.match(/^POSTGRES_PASSWORD=(.+)$/m)[1];
    await run(
      cli,
      [
        "setup",
        "--dir",
        instance,
        "--tenant",
        config.tenant,
        "--email",
        config.adminEmail,
        "--password-file",
        path.join(state, "admin-password"),
        "--http",
        `127.0.0.1:${config.bffPort}`,
        "--backend-http",
        `127.0.0.1:${config.backendPort}`,
      ],
      {
        env: {
          ...process.env,
          SALTCORN_GO_DATABASE_URL: `postgres://saltcorn_local:${password}@127.0.0.1:${config.postgresPort}/saltcorn_local?sslmode=disable`,
        },
      }
    );
  }
  await run(cli, ["check", "--dir", instance]);
  await run("npm", ["ci"], {
    cwd: path.join(repo, "migracao/packages/frontend"),
  });
  console.log(
    "Preparação concluída. Execute local.sh up; em outro terminal, local.sh login."
  );
}
async function start(withFrontend) {
  const release = await releasePath();
  await checkPort(config.backendPort);
  await checkPort(config.bffPort);
  if (withFrontend) await checkPort(config.frontendPort);
  const children = [];
  let stopping = false;
  const stop = () => {
    if (stopping) return;
    stopping = true;
    for (const c of children)
      if (c.exitCode === null && !c.signalCode) c.kill("SIGTERM");
  };
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
  const launch = (bin, args, options = {}) => {
    const child = spawn(bin, args, { cwd: repo, stdio: "inherit", ...options });
    children.push(child);
    return new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("exit", (code, signal) => {
        if (stopping) resolve();
        else
          reject(Error(`${path.basename(bin)} encerrou (${signal || code})`));
      });
    });
  };
  const tasks = [];
  try {
    tasks.push(
      launch(path.join(release, "bin/cli"), [
        "serve",
        "--dir",
        instance,
        "--release",
        release,
      ])
    );
    tasks[0].catch(stop);
    if (withFrontend) tasks.push(launchFrontend(launch));
    console.log(`BFF + frontend compilado: http://localhost:${config.bffPort}`);
    console.log(`Backend Go: http://127.0.0.1:${config.backendPort}`);
    if (withFrontend)
      console.log(
        `Frontend com recarga automática: http://localhost:${config.frontendPort}`
      );
    console.log(
      "Login: execute local.sh login em outro terminal. Ctrl+C encerra os serviços; PostgreSQL/dados permanecem."
    );
    await Promise.race(tasks);
  } finally {
    stop();
    await Promise.allSettled(tasks);
    process.removeListener("SIGINT", stop);
    process.removeListener("SIGTERM", stop);
  }
}
function launchFrontend(launch) {
  const frontend = path.join(repo, "migracao/packages/frontend");
  return launch(
    process.execPath,
    [
      path.join(frontend, "node_modules/vite/bin/vite.js"),
      frontend,
      "--host",
      "127.0.0.1",
      "--port",
      String(config.frontendPort),
      "--strictPort",
    ],
    {
      env: {
        ...process.env,
        VITE_BFF_BASE_URL: "",
        SALTCORN_DEV_BFF_PROXY_TARGET: `http://127.0.0.1:${config.bffPort}`,
      },
    }
  );
}
try {
  await compatibleSettings();
  switch (command) {
    case "setup":
      await setup();
      break;
    case "build":
      await releasePath();
      await build();
      break;
    case "up":
      await start(true);
      break;
    case "backend-bff":
      await start(false);
      break;
    case "frontend":
      await checkPort(config.frontendPort);
      await launchFrontend((bin, args, options) => run(bin, args, options));
      break;
    case "login":
      await run(path.join(await releasePath(), "bin/cli"), [
        "login",
        "--dir",
        instance,
      ]);
      break;
    case "db-up":
      await compose("up", "-d", "--wait", "postgres");
      break;
    case "db-stop":
      await compose("stop", "postgres");
      break;
    case "status":
      await compose("ps");
      for (const [name, port, endpoint] of [
        ["Go", config.backendPort, "/readyz"],
        ["BFF", config.bffPort, "/readyz"],
        ["Frontend", config.frontendPort, "/"],
      ]) {
        try {
          const r = await fetch(`http://127.0.0.1:${port}${endpoint}`, {
            signal: AbortSignal.timeout(2000),
          });
          console.log(`${name}: HTTP ${r.status}`);
        } catch {
          console.log(`${name}: parado ou indisponível`);
        }
      }
      break;
    default:
      console.log(
        "Uso: migracao/local/local.sh setup|up|backend-bff|frontend|login|status|build|db-up|db-stop"
      );
      if (command) process.exitCode = 1;
  }
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
