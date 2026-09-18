// Testes de src/realtimeClient.ts (GO-028) — contra um servidor Socket.IO
// real (não um mock do protocolo), mesmo rigor de
// migracao/packages/bff/test/realtime.test.ts: aqui o foco é a metade
// CLIENTE do contrato de retomada (lembrar o último id e reenviar como
// `?since=` na reconexão), que é o que este módulo de fato implementa.
import { describe, it, expect, afterEach } from "vitest";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { Server as SocketIOServer } from "socket.io";
import { connectRealtime } from "../src/realtimeClient";

let cleanup: (() => Promise<void>) | undefined;

afterEach(async () => {
  await cleanup?.();
  cleanup = undefined;
});

async function startFakeBff(onSince: (since: number) => { id: number; payload: Record<string, unknown> }[]): Promise<string> {
  const httpServer = createServer();
  const io = new SocketIOServer(httpServer, { path: "/socket.io/" });
  io.on("connection", (socket) => {
    const since = Number.parseInt(String(socket.handshake.query.since ?? "0"), 10) || 0;
    for (const event of onSince(since)) {
      socket.emit("dynamic_update", event);
    }
  });
  await new Promise<void>((resolve) => httpServer.listen(0, "127.0.0.1", resolve));
  const { port } = httpServer.address() as AddressInfo;
  cleanup = () => new Promise<void>((resolve) => io.close(() => httpServer.close(() => resolve())));
  return `http://127.0.0.1:${port}`;
}

describe("connectRealtime", () => {
  it("entrega o payload desembrulhado (sem o envelope {id, payload}) ao onEvent", async () => {
    const url = await startFakeBff((since) => (since === 0 ? [{ id: 1, payload: { title: "Olá" } }] : []));

    const received = await new Promise<Record<string, unknown>>((resolve) => {
      const socket = connectRealtime({ baseUrl: url, onEvent: resolve });
      cleanup = async () => {
        socket.close();
      };
    });

    expect(received).toEqual({ title: "Olá" });
  });

  it("reconexão: envia ?since= com o maior id já recebido, nunca 0 de novo", async () => {
    // Resolve a partir do lado do SERVIDOR (não do evento "reconnect" do
    // cliente) — o cliente pode considerar a reconexão concluída um
    // instante antes do servidor terminar de processar a nova conexão,
    // então esperar "reconnect" e checar o valor logo em seguida é uma
    // corrida; esperar o PRÓPRIO servidor observar `since=5` é
    // determinístico.
    let resolveSecondConnection: (since: number) => void;
    const secondConnectionSince = new Promise<number>((resolve) => {
      resolveSecondConnection = resolve;
    });
    const url = await startFakeBff((since) => {
      if (since === 0) return [{ id: 5, payload: { seq: 0 } }];
      resolveSecondConnection(since);
      return [];
    });

    const socket = connectRealtime({ baseUrl: url, onEvent: () => {} });
    cleanup = async () => {
      socket.close();
    };

    await new Promise<void>((resolve) => socket.on("dynamic_update", () => resolve()));

    // Força uma reconexão (mesma técnica do teste do BFF: fecha o
    // transporte por baixo, não um close() intencional do lado do
    // cliente) — o Manager reconecta sozinho.
    socket.io.engine.close();

    await expect(secondConnectionSince).resolves.toBe(5);
  });
});
