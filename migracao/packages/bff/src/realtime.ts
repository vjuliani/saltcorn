// Comunicação em tempo real (GO-028) — o protocolo Socket.IO real roda
// AQUI, no BFF, nunca no backend Go (ADR-0003: sessão/borda web é sempre
// BFF; ver docs/migracao-go/execucoes/GO-028.md para o levantamento
// completo do legado e a decisão de arquitetura). O Go só oferece
// `GET .../realtime/events` (goClient.listRealtimeEvents) — um poll
// autenticado e já filtrado por destinatário; este módulo é a ponte que
// busca esses eventos e os reemite pelo socket certo.
//
// Divergência deliberada do legado mais relevante (ver achado #1/#8 do
// levantamento): lá, isolamento por tenant depende de nomear rooms a
// partir do tenant resolvido do header `Host` no handshake — sem barreira
// estrutural própria do Socket.IO. Aqui não existem rooms: cada socket
// tem seu PRÓPRIO poll, escopado ao ator (userId+tenant) resolvido da
// sessão de navegador (nunca de um header/query do cliente) — o Go já
// filtra por esse ator antes mesmo do BFF receber a resposta, então um
// socket nunca sequer recebe a EXISTÊNCIA de um evento de outro tenant ou
// usuário, defesa em profundidade além do isolamento de schema do Go.
import type { Server as HTTPServer } from "node:http";
import { Server as SocketIOServer, type Socket } from "socket.io";
import type { Config } from "./config.js";
import type { GoClient } from "./goClient.js";
import { mintServiceIdentity } from "./serviceIdentity.js";
import { readSessionCookie } from "./session.js";
import type { SessionStore } from "./session.js";

export interface RealtimeDeps {
  readonly config: Config;
  readonly sessionStore: SessionStore;
  readonly goClient: GoClient;
}

interface SocketSession {
  readonly sessionId: string;
  readonly userId: string;
  readonly tenant: string;
}

const sessions = new WeakMap<Socket, SocketSession>();

/**
 * attachRealtime cria o servidor Socket.IO real (protocolo completo —
 * handshake com upgrade, reconexão com backoff, acks — nunca um
 * WebSocket puro reimplementado à mão, critério de aceite de GO-028) e o
 * anexa ao MESMO `http.Server` que já serve as rotas REST do BFF.
 */
export function attachRealtime(httpServer: HTTPServer, deps: RealtimeDeps): SocketIOServer {
  const io = new SocketIOServer(httpServer, {
    path: "/socket.io/",
  });

  // io.use roda no handshake — nega a conexão ANTES de qualquer
  // socket.on ser registrado se a sessão de navegador estiver ausente ou
  // expirada (mesmo padrão fail-closed de requireSession em app.ts).
  // Nunca aceita tenant/userId vindos do cliente (query/header) — só o
  // que a sessão do BFF já resolveu.
  io.use((socket, next) => {
    void (async () => {
      const sessionId = readSessionCookie(socket.handshake.headers.cookie);
      if (!sessionId) {
        next(new Error("session_required"));
        return;
      }
      const data = await deps.sessionStore.get(sessionId);
      if (!data) {
        next(new Error("session_required"));
        return;
      }
      sessions.set(socket, { sessionId, userId: data.userId, tenant: data.tenant });
      next();
    })();
  });

  io.on("connection", (socket) => {
    const session = sessions.get(socket);
    if (!session) {
      // Não deveria acontecer (io.use já rejeitou antes) — fail-closed
      // mesmo assim, nunca assumir uma sessão que não foi validada.
      socket.disconnect(true);
      return;
    }

    let cursor = parseSinceQuery(socket.handshake.query.since);

    const timer = setInterval(() => {
      void pollOnce(socket, session, deps, cursor).then((nextCursor) => {
        if (nextCursor !== undefined) cursor = nextCursor;
      });
    }, deps.config.realtimePollIntervalMs);

    socket.on("disconnect", () => clearInterval(timer));
  });

  return io;
}

/**
 * pollOnce faz UMA rodada: revalida a sessão (critério de aceite "sessão
 * expirada" — um socket aberto é desconectado à força assim que a sessão
 * por trás dele deixa de existir, não só recusado num handshake futuro),
 * e, se ainda válida, busca e reemite eventos em ordem (o array já vem
 * ordenado por id crescente do Go — reemitir na mesma ordem, nunca em
 * paralelo/Promise.all, preserva ordenação ponta a ponta).
 */
async function pollOnce(socket: Socket, session: SocketSession, deps: RealtimeDeps, cursor: number): Promise<number | undefined> {
  const stillValid = await deps.sessionStore.get(session.sessionId);
  if (!stillValid) {
    socket.disconnect(true);
    return undefined;
  }

  try {
    const token = mintServiceIdentity(deps.config.serviceIdentitySecret, { sub: session.userId, tenant: session.tenant }, deps.config.serviceIdentityTtlSeconds);
    const page = await deps.goClient.listRealtimeEvents(token, session.tenant, cursor);
    for (const event of page.items) {
      // `id` viaja junto do payload (não só o payload cru) para que um
      // cliente que reconecta (critério de aceite "reconexão") possa
      // lembrar o último id recebido e reenviá-lo como `?since=` na nova
      // conexão (ver frontend/src/realtimeClient.ts) — sem isso, o único
      // jeito de retomar sem perder eventos seria o BFF guardar estado
      // por sessão, o que faria duas abas do MESMO usuário compartilharem
      // cursor e uma roubar os eventos que a outra ainda não viu.
      socket.emit("dynamic_update", { id: event.id, payload: event.payload });
    }
    return page.next_after;
  } catch {
    // Indisponibilidade do Go é um erro controlado (mesmo espírito de
    // GoClient.request/domainUnavailableError): o próximo tick tenta de
    // novo, nunca derruba o socket por uma falha transitória de polling.
    return undefined;
  }
}

function parseSinceQuery(raw: unknown): number {
  const value = Array.isArray(raw) ? raw[0] : raw;
  if (typeof value !== "string") return 0;
  const n = Number.parseInt(value, 10);
  return Number.isFinite(n) && n >= 0 ? n : 0;
}
