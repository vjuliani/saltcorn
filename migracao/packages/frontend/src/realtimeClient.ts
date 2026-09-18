// Cliente de comunicação em tempo real (GO-028) — conecta ao Socket.IO
// real hospedado pelo BFF (migracao/packages/bff/src/realtime.ts), nunca
// ao backend Go diretamente (ADR-0003: o Go não fala Socket.IO). Mesmo
// padrão de bffClient.ts: `credentials`/cookie de sessão, sem token no
// código do frontend — a autenticação do handshake é o MESMO cookie
// `sc_session` que toda chamada HTTP já usa.
//
// Retomada sem perda entre reconexões (critério de aceite "reconexão"):
// cada evento chega com `id` (ver realtime.ts do BFF); este módulo lembra
// o maior `id` já recebido e o reenvia como `?since=` na query da conexão
// SEGUINTE que o socket.io-client abrir sozinho (evento "reconnect_attempt"
// do Manager) — o BFF então retoma exatamente dali, nunca reentrega nem
// perde eventos publicados durante a queda.
import { io, type Socket } from "socket.io-client";

export interface RealtimeEvent<TPayload = Record<string, unknown>> {
  readonly id: number;
  readonly payload: TPayload;
}

export interface RealtimeClientOptions {
  /** Mesmo padrão de BffClient: "" (padrão) = same-origin, atrás do mesmo proxy reverso de produção. */
  readonly baseUrl?: string;
  /** Chamado para cada evento recebido, já desembrulhado do envelope {id, payload}. */
  readonly onEvent: (payload: Record<string, unknown>) => void;
}

/**
 * connectRealtime abre a conexão e devolve o Socket já configurado —
 * quem chama decide quando fechar (`socket.close()`), tipicamente no
 * cleanup de um efeito React.
 */
export function connectRealtime(opts: RealtimeClientOptions): Socket {
  let lastSeenId = 0;

  const socket = io(opts.baseUrl ?? "", {
    path: "/socket.io/",
    withCredentials: true,
  });

  socket.io.on("reconnect_attempt", () => {
    socket.io.opts.query = { since: String(lastSeenId) };
  });

  socket.on("dynamic_update", (event: RealtimeEvent) => {
    lastSeenId = Math.max(lastSeenId, event.id);
    opts.onEvent(event.payload);
  });

  return socket;
}
