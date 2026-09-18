// Testes dos 4 critérios de aceite de GO-028 (reconexão, sessão expirada,
// ordenação, isolamento por tenant) contra um servidor Socket.IO REAL
// (não simulado) — mesmo rigor de test/session.test.ts, test/app.test.ts:
// HTTP/socket real via httptest-equivalente do Node (`createServer` +
// porta efêmera), nunca um mock do protocolo em si.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { io as ioClient, type Socket as ClientSocket } from "socket.io-client";
import { Server as SocketIOServer } from "socket.io";

import { attachRealtime, type RealtimeDeps } from "../src/realtime.js";
import { InMemorySessionStore, sessionCookieHeader } from "../src/session.js";
import type { Config } from "../src/config.js";
import type { GoClient } from "../src/goClient.js";

function testConfig(overrides: Partial<Config> = {}): Config {
  return {
    httpAddr: ":0",
    goInternalApiUrl: "http://unused.invalid",
    serviceIdentitySecret: "0".repeat(32),
    serviceIdentityTtlSeconds: 30,
    goRequestTimeoutMs: 5000,
    shutdownTimeoutMs: 1000,
    realtimePollIntervalMs: 30,
    ...overrides,
  };
}

interface FakeCall {
  readonly token: string;
  readonly tenant: string;
  readonly after: number;
}

/** fakeGoClient deixa o teste controlar exatamente o que cada poll devolve, e registra toda chamada recebida (para provar isolamento/ordenação). */
function fakeGoClient(
  respond: (call: FakeCall) => { items: { id: number; audience: string; payload: Record<string, unknown> }[]; next_after: number },
  calls: FakeCall[] = []
): { client: GoClient; calls: FakeCall[] } {
  const client = {
    listRealtimeEvents: async (token: string, tenant: string, after: number) => {
      const call = { token, tenant, after };
      calls.push(call);
      return respond(call);
    },
  } as unknown as GoClient;
  return { client, calls };
}

interface TestServer {
  readonly url: string;
  readonly io: SocketIOServer;
  close(): Promise<void>;
}

async function startServer(deps: RealtimeDeps): Promise<TestServer> {
  const httpServer = createServer();
  const io = attachRealtime(httpServer, deps);
  await new Promise<void>((resolve) => httpServer.listen(0, "127.0.0.1", resolve));
  const { port } = httpServer.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}`,
    io,
    close: () =>
      new Promise<void>((resolve) => {
        io.close(() => httpServer.close(() => resolve()));
      }),
  };
}

function connectClient(url: string, sessionId: string | undefined, extraQuery: Record<string, string> = {}): ClientSocket {
  return ioClient(url, {
    path: "/socket.io/",
    transports: ["websocket"],
    reconnectionDelay: 20,
    reconnectionDelayMax: 50,
    extraHeaders: sessionId ? { cookie: sessionCookieHeader(sessionId).split(";")[0]! } : {},
    query: extraQuery,
    forceNew: true,
  });
}

interface OnceEmitter {
  once(event: string, listener: (...args: never[]) => void): unknown;
}

function waitForEvent<T = unknown>(emitter: OnceEmitter, event: string, timeoutMs = 2000): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timeout esperando evento "${event}"`)), timeoutMs);
    emitter.once(event, ((payload: T) => {
      clearTimeout(timer);
      resolve(payload);
    }) as (...args: never[]) => void);
  });
}

test("handshake sem cookie de sessão é recusado (fail-closed)", async () => {
  const sessionStore = new InMemorySessionStore();
  const { client: goClient } = fakeGoClient(() => ({ items: [], next_after: 0 }));
  const server = await startServer({ config: testConfig(), sessionStore, goClient });
  try {
    const client = connectClient(server.url, undefined);
    const err = await waitForEvent<Error>(client, "connect_error");
    assert.match(err.message, /session_required/);
    client.close();
  } finally {
    await server.close();
  }
});

// Critério de aceite "isolamento por tenant": dois sockets, sessões de
// tenants diferentes — cada um só deveria ver o que o Go devolveu PARA O
// TENANT DELE. Isso prova que o BFF nunca cruza qual token pertence a
// qual socket, mesmo com pollings concorrentes.
test("isolamento por tenant: cada socket só recebe eventos do próprio tenant/sessão", async () => {
  const sessionStore = new InMemorySessionStore();
  const sessionAcme = await sessionStore.create({ userId: "1", tenant: "acme" });
  const sessionBeta = await sessionStore.create({ userId: "2", tenant: "beta" });

  const { client: goClient, calls } = fakeGoClient((call) => {
    if (call.after > 0) return { items: [], next_after: call.after };
    return { items: [{ id: 1, audience: "broadcast", payload: { marker: `${call.tenant}-only` } }], next_after: 1 };
  });

  const server = await startServer({ config: testConfig(), sessionStore, goClient });
  try {
    const clientAcme = connectClient(server.url, sessionAcme);
    const clientBeta = connectClient(server.url, sessionBeta);

    const [eventAcme, eventBeta] = await Promise.all([
      waitForEvent<{ id: number; payload: { marker: string } }>(clientAcme, "dynamic_update"),
      waitForEvent<{ id: number; payload: { marker: string } }>(clientBeta, "dynamic_update"),
    ]);

    assert.equal(eventAcme.payload.marker, "acme-only");
    assert.equal(eventBeta.payload.marker, "beta-only");
    assert.ok(calls.some((c) => c.tenant === "acme"));
    assert.ok(calls.some((c) => c.tenant === "beta"));

    clientAcme.close();
    clientBeta.close();
  } finally {
    await server.close();
  }
});

// Critério de aceite "ordenação": eventos chegam ao cliente na MESMA
// ordem de publicação — o array de cada página já vem ordenado do Go
// (internal/realtime.ListSinceForActor), e o cursor `after` avança
// estritamente entre polls, nunca revisitando ids já entregues.
test("ordenação: eventos chegam em ordem estrita, sem repetição entre polls", async () => {
  const sessionStore = new InMemorySessionStore();
  const sessionId = await sessionStore.create({ userId: "1", tenant: "acme" });

  // Cada entrada é acionada por um valor exato de `after` — simula o Go
  // devolvendo a próxima fatia da linha do tempo a cada poll, nunca tudo
  // de uma vez.
  const timeline = [
    { after: 0, items: [{ id: 1, audience: "broadcast", payload: { seq: 0 } }, { id: 2, audience: "broadcast", payload: { seq: 1 } }], next_after: 2 },
    { after: 2, items: [{ id: 3, audience: "broadcast", payload: { seq: 2 } }], next_after: 3 },
  ];
  const { client: goClient, calls } = fakeGoClient((call) => {
    const entry = timeline.find((e) => e.after === call.after);
    if (entry) return { items: entry.items, next_after: entry.next_after };
    return { items: [], next_after: call.after };
  });

  const server = await startServer({ config: testConfig(), sessionStore, goClient });
  try {
    const client = connectClient(server.url, sessionId);
    const received: number[] = [];
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("timeout esperando 3 eventos")), 3000);
      client.on("dynamic_update", (ev: { payload: { seq: number } }) => {
        received.push(ev.payload.seq);
        if (received.length >= 3) {
          clearTimeout(timer);
          resolve();
        }
      });
    });

    assert.deepEqual(received, [0, 1, 2]);
    // O cursor enviado nunca deveria repetir um valor já superado.
    const afters = calls.map((c) => c.after);
    for (let i = 1; i < afters.length; i++) {
      assert.ok(afters[i]! >= afters[i - 1]!, `after regrediu: ${afters[i - 1]} -> ${afters[i]}`);
    }
    client.close();
  } finally {
    await server.close();
  }
});

// Critério de aceite "sessão expirada": um socket JÁ CONECTADO é
// desconectado à força assim que a sessão por trás dele deixa de
// existir no store — não só recusado num handshake futuro.
test("sessão expirada: socket já conectado é desconectado à força no próximo poll", async () => {
  const sessionStore = new InMemorySessionStore();
  const sessionId = await sessionStore.create({ userId: "1", tenant: "acme" });
  const { client: goClient } = fakeGoClient(() => ({ items: [], next_after: 0 }));

  const server = await startServer({ config: testConfig({ realtimePollIntervalMs: 20 }), sessionStore, goClient });
  try {
    const client = connectClient(server.url, sessionId);
    await waitForEvent(client, "connect");

    await sessionStore.destroy(sessionId);

    const reason = await waitForEvent<string>(client, "disconnect");
    assert.equal(reason, "io server disconnect");
    client.close();
  } finally {
    await server.close();
  }
});

// Critério de aceite "reconexão": simula uma queda de rede (fechar o
// transporte por baixo, não um disconnect() intencional do servidor) e
// confirma que o cliente Socket.IO reconecta sozinho (protocolo real,
// nunca WebSocket puro) e retoma exatamente de onde parou via `?since=`
// — nenhum evento perdido, nenhum repetido.
test("reconexão: cliente reconecta sozinho e retoma sem perder nem repetir eventos", async () => {
  const sessionStore = new InMemorySessionStore();
  const sessionId = await sessionStore.create({ userId: "1", tenant: "acme" });

  // Um evento pendente por poll (nunca os dois de uma vez) — só assim o
  // teste consegue provar que o SEGUNDO evento só chega DEPOIS da
  // reconexão, não porque os dois já tinham sido entregues no primeiro poll.
  const allEvents = [
    { id: 1, audience: "broadcast", payload: { seq: 0 } },
    { id: 2, audience: "broadcast", payload: { seq: 1 } },
  ];
  const { client: goClient } = fakeGoClient((call) => {
    const next = allEvents.find((e) => e.id > call.after);
    if (!next) return { items: [], next_after: call.after };
    return { items: [next], next_after: next.id };
  });

  const server = await startServer({ config: testConfig(), sessionStore, goClient });
  try {
    let lastSeenId = 0;
    const client = connectClient(server.url, sessionId, { since: "0" });
    client.io.on("reconnect_attempt", () => {
      client.io.opts.query = { since: String(lastSeenId) };
    });

    const first = await waitForEvent<{ id: number; payload: { seq: number } }>(client, "dynamic_update");
    lastSeenId = first.id;
    assert.equal(first.payload.seq, 0);

    // Simula queda de rede: fecha o transporte de baixo nível (não um
    // disconnect() do servidor) — o cliente deveria reconectar sozinho.
    const serverSockets = [...server.io.sockets.sockets.values()];
    assert.equal(serverSockets.length, 1);
    serverSockets[0]!.conn.close();

    // "reconnect" é emitido pelo Manager (client.io), não pelo Socket de
    // namespace (client) — só o Manager sabe sobre o ciclo de
    // desconexão/reconexão do transporte por baixo.
    await waitForEvent(client.io, "reconnect", 3000);

    const second = await waitForEvent<{ id: number; payload: { seq: number } }>(client, "dynamic_update");
    assert.equal(second.payload.seq, 1, "após reconectar, deveria retomar do evento seguinte (seq=1), sem repetir seq=0");

    client.close();
  } finally {
    await server.close();
  }
});
