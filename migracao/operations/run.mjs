// Isolated Linux/Postgres lab: real React, real BFF, Go and worker processes.
import assert from "node:assert/strict";
import { spawn, fork, execFile, execFileSync } from "node:child_process";
import { promisify } from "node:util";
import { createServer as httpServer, request as httpRequest } from "node:http";
import { createServer as tcpServer, connect } from "node:net";
import { once } from "node:events";
import { randomBytes } from "node:crypto";
import { readFile, writeFile, open } from "node:fs/promises";
import { createRequire } from "node:module";
import { mintServiceIdentity } from "../packages/bff/dist/src/serviceIdentity.js";
const { chromium, expect } = createRequire(
  new URL("../e2e/package.json", import.meta.url)
)("@playwright/test");
const exec = promisify(execFile),
  dir = process.argv[2],
  dsn = new URL(process.env.SALTCORN_GO_TEST_DATABASE_URL);
const secret = randomBytes(32).toString("hex"),
  children = [],
  servers = [],
  handles = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const report = {
  profile: {
    clients: [1, 8, 32],
    phaseSeconds: 5,
    seedRowsPerTenant: 1000,
    readWriteRatio: "80/20",
    p95ms: 500,
    p99ms: 1000,
    recoveryMs: 30000,
    bffLimit: 64,
    goLimit: 128,
    poolLimit: 4,
    scope: "laboratorio-provisorio",
  },
  phases: [],
  faults: [],
  resources: {},
  labPassed: false,
  productionPromotion: false,
};
const pgEnv = {
  ...process.env,
  PGHOST: dsn.hostname,
  PGPORT: dsn.port || "5432",
  PGUSER: decodeURIComponent(dsn.username),
  PGPASSWORD: decodeURIComponent(dsn.password),
  PGDATABASE: "go035_load",
};
async function sql(query) {
  return (
    await exec("psql", ["-X", "-At", "-v", "ON_ERROR_STOP=1", "-c", query], {
      env: pgEnv,
      maxBuffer: 8e6,
    })
  ).stdout.trim();
}
async function listen(server) {
  servers.push(server);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return server.address().port;
}
async function start(name, args = [], extra = {}) {
  const log = await open(`${dir}/${name}-${children.length}.log`, "w", 0o600);
  handles.push(log);
  const child = spawn(`${dir}/${name}`, args, {
    env: { ...process.env, ...extra },
    stdio: ["ignore", log.fd, log.fd],
  });
  children.push(child);
  return child;
}
async function stop(child, signal = "SIGTERM") {
  if (child.exitCode !== null || child.signalCode) return;
  const done = once(child, "exit");
  child.kill(signal);
  const timer = setTimeout(() => child.kill("SIGKILL"), 3000);
  await done;
  clearTimeout(timer);
}
async function until(fn, ms = 30000) {
  const end = Date.now() + ms;
  let last;
  while (Date.now() < end) {
    try {
      if (await fn()) return;
    } catch (e) {
      last = e;
    }
    await sleep(100);
  }
  throw Error(
    `recovery deadline exceeded: ${last?.message || "condition false"}`
  );
}
let browser,
  sampler,
  sampling = false,
  goUrl,
  bffUrl,
  sessions,
  bff,
  goProcess,
  dbDown = false,
  network = "up";
const dbSockets = new Set(),
  httpSockets = new Set();
const resourceSamples = [];
let upstreamActive = 0,
  upstreamPeak = 0;
try {
  // DB proxy owns only its own connections. No service/container is stopped.
  const dbProxy = tcpServer((socket) => {
    if (dbDown) {
      socket.destroy();
      return;
    }
    const upstream = connect({
      host: dsn.hostname,
      port: Number(dsn.port || 5432),
    });
    for (const s of [socket, upstream]) {
      dbSockets.add(s);
      s.on("close", () => dbSockets.delete(s));
      s.on("error", () => {
        socket.destroy();
        upstream.destroy();
      });
    }
    socket.on("close", () => upstream.destroy());
    upstream.on("close", () => socket.destroy());
    socket.pipe(upstream);
    upstream.pipe(socket);
  });
  const dbPort = await listen(dbProxy);
  for (const tenant of ["load_a", "load_b"])
    await exec(`${dir}/cli`, [
      "e2e-seed",
      "--dsn",
      dsn.href,
      "--tenant",
      tenant,
    ]);
  await sql(
    "INSERT INTO public._sc_capability_ownership(tenant,capability,owner) VALUES ('load_a','worker.outbox_processor','go'),('load_b','worker.outbox_processor','go') ON CONFLICT(tenant,capability) DO UPDATE SET owner='go'"
  );
  // Reserve/check before launching; a port race fails startup, never kills its owner.
  const reservation = tcpServer();
  const goPort = await listen(reservation);
  await new Promise((r) => reservation.close(r));
  const proxiedDSN = new URL(dsn);
  proxiedDSN.port = String(dbPort);
  proxiedDSN.search = "?sslmode=disable&pool_max_conns=4";
  goProcess = await start("server", [], {
    SALTCORN_GO_HTTP_ADDR: `127.0.0.1:${goPort}`,
    SALTCORN_GO_DATABASE_URL: proxiedDSN.href,
    SALTCORN_GO_SERVICE_IDENTITY_SECRET: secret,
    SALTCORN_GO_LOG_LEVEL: "warn",
  });
  goUrl = `http://127.0.0.1:${goPort}`;
  await until(async () => {
    assert(goProcess.exitCode === null);
    return (await fetch(goUrl + "/healthz")).ok;
  });
  const proxy = httpServer((req, res) => {
    upstreamActive++;
    upstreamPeak = Math.max(upstreamPeak, upstreamActive);
    let released = false;
    const release = () => {
      if (!released) {
        released = true;
        upstreamActive--;
      }
    };
    res.on("close", release);
    if (network === "hang") return;
    const lose = network === "lose-response" && req.method === "POST";
    const upstream = httpRequest(
      goUrl + req.url,
      { method: req.method, headers: req.headers },
      (incoming) => {
        if (lose) {
          incoming.resume();
          incoming.on("end", () => res.destroy());
        } else {
          res.writeHead(incoming.statusCode, incoming.headers);
          incoming.pipe(res);
        }
      }
    );
    upstream.on("error", () => res.destroy());
    res.on("close", () => upstream.destroy());
    req.pipe(upstream);
  });
  proxy.on("connection", (s) => {
    httpSockets.add(s);
    s.on("close", () => httpSockets.delete(s));
  });
  const proxyPort = await listen(proxy);
  const bffLog = await open(`${dir}/bff.log`, "w", 0o600);
  handles.push(bffLog);
  bff = fork(new URL("./bff.mjs", import.meta.url), [], {
    env: {
      ...process.env,
      SALTCORN_BFF_GO_INTERNAL_API_URL: `http://127.0.0.1:${proxyPort}`,
      SALTCORN_BFF_SERVICE_IDENTITY_SECRET: secret,
      SALTCORN_BFF_GO_REQUEST_TIMEOUT_MS: "1500",
    },
    stdio: ["ignore", bffLog.fd, bffLog.fd, "ipc"],
  });
  children.push(bff);
  const ready = await Promise.race([
    once(bff, "message").then(([m]) => m),
    once(bff, "exit").then(() => {
      throw Error("BFF startup failed");
    }),
  ]);
  sessions = ready.sessions;
  bffUrl = `http://127.0.0.1:${ready.port}`;
  async function stats() {
    const message = once(bff, "message");
    bff.send("stats");
    return (await message)[0].stats;
  }
  async function api(session, path, body, expected) {
    const response = await fetch(bffUrl + "/api/bff" + path, {
      method: body === undefined ? "GET" : "POST",
      headers: {
        Cookie: `sc_session=${session.sessionId}; sc_csrf=${session.csrfToken}`,
        "X-CSRF-Token": session.csrfToken,
        "Content-Type": "application/json",
      },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(12000),
    });
    const data = await response.json();
    if (expected !== undefined)
      assert.equal(
        response.status,
        expected,
        `${path}: ${JSON.stringify(data)}`
      );
    return { status: response.status, data };
  }
  for (const session of sessions) {
    await api(session, "/tables", { name: "load_items" }, 201);
    await api(
      session,
      "/tables/load_items/fields",
      { name: "titulo", type: "text" },
      201
    );
    session.view = (
      await api(
        session,
        "/views",
        {
          name: "load_items_view",
          table: "load_items",
          template: "List",
          configuration: {
            layout: {
              besides: [
                {
                  header_label: "Título",
                  contents: { type: "Field", field_name: "titulo" },
                },
              ],
            },
          },
        },
        201
      )
    ).data;
    await sql(
      `INSERT INTO ${session.tenant}.load_items(titulo) SELECT '${session.tenant}-seed-' || n FROM generate_series(1,1000) n`
    );
  }
  const ticks = Number(
    execFileSync("getconf", ["CLK_TCK"], { encoding: "utf8" })
  );
  async function sample() {
    if (sampling) return;
    sampling = true;
    try {
      const entry = { at: Date.now() };
      for (const [name, child] of [
        ["go", goProcess],
        ["bff", bff],
      ]) {
        const stat = await readFile(`/proc/${child.pid}/stat`, "utf8");
        const fields = stat.slice(stat.lastIndexOf(")") + 2).split(" ");
        const status = await readFile(`/proc/${child.pid}/status`, "utf8");
        entry[name] = {
          cpuSeconds: (Number(fields[11]) + Number(fields[12])) / ticks,
          rssMiB: Number(status.match(/VmRSS:\s+(\d+)/)[1]) / 1024,
        };
      }
      const metrics = await (
        await fetch(goUrl + "/metrics", { signal: AbortSignal.timeout(1000) })
      ).text();
      entry.poolAcquired = Number(
        metrics.match(/^sql_pool_acquired_connections (\d+)/m)?.[1]
      );
      entry.goActive = Number(
        metrics.match(/^http_admission_active (\d+)/m)?.[1]
      );
      resourceSamples.push(entry);
    } finally {
      sampling = false;
    }
  }
  await sample();
  sampler = setInterval(
    () =>
      sample().catch((e) => {
        report.sampleError = e.message;
      }),
    200
  );
  browser = await chromium.launch({ headless: true });
  async function ui() {
    return Promise.all(
      sessions.map(async (session) => {
        const context = await browser.newContext();
        try {
          await context.addCookies([
            { name: "sc_session", value: session.sessionId, url: bffUrl },
            { name: "sc_csrf", value: session.csrfToken, url: bffUrl },
          ]);
          const page = await context.newPage();
          const errors = [];
          page.on("pageerror", (e) => errors.push(e.message));
          for (let i = 0; i < 3; i++) {
            await page.goto(bffUrl);
            await page.getByRole("button", { name: /Ver views/ }).click();
            await page
              .getByRole("row", { name: /load_items_view/ })
              .getByRole("button", { name: "Visualizar" })
              .click();
            const table = page.getByTestId("list-view");
            await expect(table).toContainText(session.tenant + "-seed-");
            await expect(table).not.toContainText(
              session.tenant === "load_a" ? "load_b-" : "load_a-"
            );
          }
          assert.deepEqual(errors, []);
          return { tenant: session.tenant, renderedPages: 3 };
        } finally {
          await context.close();
        }
      })
    );
  }
  const expectedWrites = new Map(sessions.map((s) => [s.tenant, new Set()]));
  let sequence = 0;
  for (const clients of report.profile.clients) {
    const samples = [],
      end = Date.now() + 5000,
      start = performance.now();
    const browsers = ui();
    // Attach a rejection handler immediately while HTTP clients are running.
    browsers.catch(() => {});
    await Promise.all(
      Array.from({ length: clients }, (_, client) =>
        (async () => {
          let n = 0;
          const session = sessions[client % 2];
          while (Date.now() < end) {
            const write = n++ % 5 === 4,
              begin = performance.now(),
              marker = `${session.tenant}-write-${sequence++}`;
            const result = await api(
              session,
              write
                ? "/tables/load_items/records"
                : `/views/${session.view.id}/render`,
              write ? { titulo: marker } : undefined,
              write ? 201 : 200
            );
            if (write) expectedWrites.get(session.tenant).add(marker);
            else {
              const body = JSON.stringify(result.data);
              assert(
                body.includes(session.tenant + "-seed-"),
                "missing own tenant"
              );
              assert(
                !body.includes(
                  session.tenant === "load_a" ? "load_b-" : "load_a-"
                ),
                "TENANT LEAK"
              );
            }
            samples.push({
              kind: write ? "write" : "read",
              ms: performance.now() - begin,
              status: result.status,
            });
          }
        })()
      )
    );
    const uiEvidence = await browsers,
      elapsed = (performance.now() - start) / 1000;
    const percentile = (values, p) =>
      values.slice().sort((a, b) => a - b)[
        Math.max(0, Math.ceil(values.length * p) - 1)
      ];
    const summarize = (values) => ({
      count: values.length,
      p50ms: percentile(values, 0.5),
      p95ms: percentile(values, 0.95),
      p99ms: percentile(values, 0.99),
    });
    const phase = {
      clients,
      elapsedSeconds: elapsed,
      rps: samples.length / elapsed,
      ...summarize(samples.map((s) => s.ms)),
      reads: summarize(
        samples.filter((s) => s.kind === "read").map((s) => s.ms)
      ),
      writes: summarize(
        samples.filter((s) => s.kind === "write").map((s) => s.ms)
      ),
      errors: 0,
      ui: uiEvidence,
    };
    report.phases.push(phase);
    await writeFile(`${dir}/latency-${clients}.json`, JSON.stringify(samples));
    assert(phase.p95ms <= 500 && phase.p99ms <= 1000, "Lab latency SLO failed");
  }
  // Lost acknowledgement after committed mutation: reconcile DB before replay.
  network = "lose-response";
  const lost = { titulo: "load_a-uncertain-write" };
  assert.equal(
    (await api(sessions[0], "/tables/load_items/records", lost)).status,
    502
  );
  assert.equal(
    await sql(
      "SELECT count(*) FROM load_a.load_items WHERE titulo='load_a-uncertain-write'"
    ),
    "1"
  );
  network = "up";
  await api(sessions[0], "/tables/load_items/records", lost, 201);
  assert.equal(
    await sql(
      "SELECT count(*) FROM load_a.load_items WHERE titulo='load_a-uncertain-write'"
    ),
    "1"
  );
  expectedWrites.get("load_a").add(lost.titulo);
  report.faults.push({
    name: "lost-response-after-commit",
    reconciled: 1,
    replayed: 1,
    duplicates: 0,
  });
  // BFF saturation: blocked upstream, real burst, no queue beyond 64.
  network = "hang";
  upstreamPeak = 0;
  const burstStart = performance.now();
  const burst = await Promise.all(
    Array.from({ length: 192 }, (_, i) =>
      api(sessions[i % 2], "/tables/load_items/records")
    )
  );
  assert(burst.every((r) => r.status === 502));
  const bffStats = await stats();
  assert(bffStats.rejected > 0 && bffStats.peak <= 64);
  assert(upstreamPeak <= 64);
  network = "up";
  for (const s of httpSockets) s.destroy();
  await until(
    async () =>
      (await api(sessions[0], "/tables/load_items/records")).status === 200
  );
  report.faults.push({
    name: "bff-go-timeout-saturation",
    requests: 192,
    errorsControlled: 192,
    elapsedMs: performance.now() - burstStart,
    upstreamPeak,
    ...bffStats,
  });
  // DB outage resets existing connections and rejects reconnects.
  const dbStart = performance.now();
  dbDown = true;
  for (const s of dbSockets) s.destroy();
  const dbErrors = await Promise.all(
    sessions.map((s) => api(s, "/tables/load_items/records"))
  );
  assert(dbErrors.every((r) => r.status === 502));
  dbDown = false;
  await until(
    async () =>
      (await api(sessions[0], "/tables/load_items/records")).status === 200
  );
  const dbRecovery = performance.now() - dbStart;
  assert(dbRecovery <= 30000);
  report.faults.push({
    name: "db-network-reset",
    errorsControlled: 2,
    recoveryMs: dbRecovery,
  });
  // Direct Go overload while DB requests cannot complete. The BFF limit alone
  // cannot protect other internal clients, so exercise the Go admission gate.
  const lock = spawn(
    "psql",
    [
      "-X",
      "-v",
      "ON_ERROR_STOP=1",
      "-c",
      "BEGIN; LOCK TABLE load_a.load_items IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(20); ROLLBACK;",
    ],
    { env: { ...pgEnv, PGAPPNAME: "go035-lock" }, stdio: "ignore" }
  );
  children.push(lock);
  await until(
    async () =>
      Number(
        await sql(
          "SELECT count(*) FROM pg_stat_activity WHERE application_name='go035-lock' AND wait_event='PgSleep'"
        )
      ) === 1
  );
  const token = mintServiceIdentity(secret, { sub: "1", tenant: "load_a" }, 30);
  const direct = () =>
    fetch(goUrl + "/v1/tenants/load_a/tables/load_items/records", {
      headers: { Authorization: `Bearer ${token}` },
      signal: AbortSignal.timeout(15000),
    }).then(async (r) => {
      await r.text();
      return r.status;
    });
  const saturation = Promise.all(Array.from({ length: 256 }, direct));
  saturation.catch(() => {});
  await until(async () => {
    const m = await (await fetch(goUrl + "/metrics")).text();
    return (
      /http_admission_active 128\n/.test(m) &&
      /sql_pool_acquired_connections 4\n/.test(m)
    );
  });
  const saturationMetrics = await (await fetch(goUrl + "/metrics")).text();
  await writeFile(`${dir}/saturation.prom`, saturationMetrics);
  assert(/sql_pool_acquired_connections 4\n/.test(saturationMetrics));
  await sql(
    "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name='go035-lock'"
  );
  await stop(lock);
  const codes = await saturation;
  assert(codes.includes(503));
  assert(codes.every((s) => s === 200 || s === 503));
  report.faults.push({
    name: "go-db-pool-saturation",
    requests: 256,
    rejected: codes.filter((s) => s === 503).length,
    activeLimit: 128,
    poolLimit: 4,
  });
  await writeFile(`${dir}/saturation.prom`, saturationMetrics);
  // Crash during an outbox transaction; event IDs survive and drain on restart.
  for (const s of sessions)
    await sql(
      `INSERT INTO ${s.tenant}._sc_outbox(idempotency_key,event_type,payload_json) SELECT 'go035-' || n,'go035.synthetic','{}'::jsonb FROM generate_series(1,40) n`
    );
  const eventSnapshot = await sql(
    "SELECT string_agg(tenant || ':' || id || ':' || idempotency_key,',' ORDER BY tenant,id) FROM (SELECT 'a' tenant,id,idempotency_key FROM load_a._sc_outbox UNION ALL SELECT 'b',id,idempotency_key FROM load_b._sc_outbox) x"
  );
  const workerEnv = {
    SALTCORN_GO_DATABASE_URL: dsn.href,
    SALTCORN_GO_WORKER_TENANTS: "load_a,load_b",
    SALTCORN_GO_LOG_LEVEL: "info",
  };
  // Stall an UPDATE inside the worker transaction, then kill that process.
  // PostgreSQL must roll back both status and attempts before retry.
  await sql(
    "CREATE OR REPLACE FUNCTION load_a.go035_stall() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(5); RETURN NEW; END $$; CREATE TRIGGER go035_stall BEFORE UPDATE ON load_a._sc_outbox FOR EACH ROW EXECUTE FUNCTION load_a.go035_stall()"
  );
  let worker = await start("worker", [], {
    ...workerEnv,
    PGAPPNAME: "go035-worker-fault",
  });
  await until(
    async () =>
      Number(
        await sql(
          "SELECT count(*) FROM pg_stat_activity WHERE query LIKE 'UPDATE _sc_outbox%' AND wait_event='PgSleep'"
        )
      ) > 0
  );
  const workerStart = performance.now();
  await stop(worker, "SIGKILL");
  await sql(
    "DROP TRIGGER go035_stall ON load_a._sc_outbox; DROP FUNCTION load_a.go035_stall()"
  );
  assert.equal(
    await sql(
      "SELECT count(*) FROM load_a._sc_outbox WHERE status='pending' AND attempts=0"
    ),
    "40"
  );
  worker = await start("worker", [], workerEnv);
  await until(
    async () =>
      (await sql(
        "SELECT (SELECT count(*) FROM load_a._sc_outbox WHERE status='done') + (SELECT count(*) FROM load_b._sc_outbox WHERE status='done')"
      )) === "80"
  );
  const workerRecovery = performance.now() - workerStart;
  assert(workerRecovery <= 30000);
  assert.equal(
    await sql(
      "SELECT string_agg(tenant || ':' || id || ':' || idempotency_key,',' ORDER BY tenant,id) FROM (SELECT 'a' tenant,id,idempotency_key FROM load_a._sc_outbox UNION ALL SELECT 'b',id,idempotency_key FROM load_b._sc_outbox) x"
    ),
    eventSnapshot
  );
  await stop(worker);
  report.faults.push({
    name: "worker-sigkill-restart",
    events: 80,
    lost: 0,
    recoveryMs: workerRecovery,
    consumer: "synthetic-log-only",
  });
  for (const session of sessions) {
    const rows = JSON.parse(
      await sql(
        `SELECT json_agg(titulo ORDER BY titulo) FROM ${session.tenant}.load_items`
      )
    );
    const expected = [
      ...Array.from(
        { length: 1000 },
        (_, i) => `${session.tenant}-seed-${i + 1}`
      ),
      ...expectedWrites.get(session.tenant),
    ].sort();
    assert.deepEqual(
      rows,
      expected,
      "Reconciliation: missing/duplicate/cross-tenant rows"
    );
  }
  report.reconciliation = {
    tenants: sessions.map((s) => ({
      tenant: s.tenant,
      rows: 1000 + expectedWrites.get(s.tenant).size,
    })),
    missing: 0,
    duplicates: 0,
    crossTenant: 0,
  };
  clearInterval(sampler);
  await until(() => !sampling);
  await sample();
  for (const name of ["go", "bff"]) {
    const first = resourceSamples[0],
      last = resourceSamples.at(-1);
    report.resources[name] = {
      peakRssMiB: Math.max(...resourceSamples.map((s) => s[name].rssMiB)),
      cpuSeconds: last[name].cpuSeconds - first[name].cpuSeconds,
      elapsedSeconds: (last.at - first.at) / 1000,
    };
    assert(
      report.resources[name].peakRssMiB < 512,
      `${name} exceeded 512 MiB lab budget`
    );
  }
  assert(resourceSamples.length > 10);
  assert(!report.sampleError);
  assert(
    resourceSamples.every(
      (s) =>
        Number.isFinite(s.poolAcquired) &&
        s.poolAcquired <= 4 &&
        s.goActive <= 128
    )
  );
  report.resources.samples = resourceSamples.length;
  report.resources.intervalMs = 200;
  await writeFile(`${dir}/resources.json`, JSON.stringify(resourceSamples));
  await writeFile(
    `${dir}/metrics.prom`,
    await (await fetch(goUrl + "/metrics")).text()
  );
  report.workloadPassed = true;
} catch (error) {
  report.failure = error.message
    .replaceAll(dsn.href, "<database>")
    .replaceAll(secret, "<secret>")
    .replaceAll(decodeURIComponent(dsn.password), "<password>");
  process.exitCode = 1;
  console.error(report.failure);
} finally {
  await writeFile(`${dir}/resources.json`, JSON.stringify(resourceSamples));
  await writeFile(`${dir}/report.json`, JSON.stringify(report, null, 2));
  clearInterval(sampler);
  if (browser) await browser.close();
  for (const child of children.reverse()) await stop(child);
  for (const socket of [...dbSockets, ...httpSockets]) socket.destroy();
  for (const server of servers) server.close();
  for (const handle of handles) await handle.close();
  await writeFile(`${dir}/report.json`, JSON.stringify(report, null, 2));
}
