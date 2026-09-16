// Sessão de navegador — implementa exatamente ADR-0007 ("GO-017 implementa
// exatamente este contrato"): cookie `sc_session`, HttpOnly, Secure,
// SameSite=Lax, 24h com renovação deslizante em atividade; a sessão
// guarda só {user_id, tenant} — nunca papel/permissões, que o BFF sempre
// reconsulta no backend Go (getActor) a cada requisição que precisar.
//
// O ID de sessão em si é opaco e aleatório (crypto.randomBytes) — os
// dados ficam no SessionStore, nunca no cookie. Isso é o que ADR-0007
// chama de "um store dedicado do BFF... nunca o banco de domínio".

import { randomBytes, timingSafeEqual } from "node:crypto";

export const SESSION_COOKIE_NAME = "sc_session";
export const SESSION_MAX_AGE_MS = 24 * 60 * 60 * 1000; // 24h, ADR-0007

export interface SessionData {
  readonly userId: string;
  readonly tenant: string;
}

interface StoredSession {
  readonly data: SessionData;
  expiresAt: number;
}

/**
 * SessionStore é a interface que ADR-0007 deixa em aberto quanto à
 * tecnologia exata ("Redis, ou Postgres separado do banco de domínio...
 * a escolha exata fica para GO-017"). InMemorySessionStore (abaixo) é a
 * implementação desta entrega — documentada como decisão de estágio, não
 * definitiva: trocar por Redis/Postgres-de-sessão é uma troca de
 * implementação atrás desta mesma interface, não uma reescrita dos
 * pontos que a consomem.
 */
export interface SessionStore {
  create(data: SessionData): Promise<string>;
  get(sessionId: string): Promise<SessionData | null>;
  /** Renovação deslizante: estende expiresAt sem trocar o ID nem os dados. */
  touch(sessionId: string): Promise<void>;
  destroy(sessionId: string): Promise<void>;
}

/**
 * Store em memória — não sobrevive a um restart do processo nem escala
 * além de uma única instância do BFF. Decisão de estágio explícita (ver
 * docs/migracao-go/execucoes/GO-017.md, nota de escopo #5): sem
 * infraestrutura Redis disponível neste ambiente de execução, e sem
 * consumidor de produção real ainda para justificar operar um store
 * externo. Nunca o banco de domínio (ADR-0003/ADR-0007) — este store não
 * importa nenhum driver de banco.
 */
export class InMemorySessionStore implements SessionStore {
  private readonly sessions = new Map<string, StoredSession>();

  async create(data: SessionData): Promise<string> {
    const id = randomBytes(32).toString("base64url");
    this.sessions.set(id, { data, expiresAt: Date.now() + SESSION_MAX_AGE_MS });
    return id;
  }

  async get(sessionId: string): Promise<SessionData | null> {
    const entry = this.sessions.get(sessionId);
    if (!entry) return null;
    if (entry.expiresAt < Date.now()) {
      this.sessions.delete(sessionId);
      return null;
    }
    return entry.data;
  }

  async touch(sessionId: string): Promise<void> {
    const entry = this.sessions.get(sessionId);
    if (entry) entry.expiresAt = Date.now() + SESSION_MAX_AGE_MS;
  }

  async destroy(sessionId: string): Promise<void> {
    this.sessions.delete(sessionId);
  }
}

/** Serializa o Set-Cookie de uma sessão nova — sempre os mesmos atributos fixados em ADR-0007. */
export function sessionCookieHeader(sessionId: string): string {
  const maxAgeSeconds = Math.floor(SESSION_MAX_AGE_MS / 1000);
  return `${SESSION_COOKIE_NAME}=${sessionId}; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=${maxAgeSeconds}`;
}

/** Set-Cookie que expira imediatamente — usado por logout (ADR-0007: "logout apaga a sessão do store imediatamente"). */
export function expiredSessionCookieHeader(): string {
  return `${SESSION_COOKIE_NAME}=; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=0`;
}

/** Extrai o valor do cookie de sessão de um header Cookie bruto, sem depender de nenhuma biblioteca de parsing. */
export function readSessionCookie(cookieHeader: string | undefined): string | null {
  if (!cookieHeader) return null;
  for (const part of cookieHeader.split(";")) {
    const eq = part.indexOf("=");
    if (eq === -1) continue;
    const name = part.slice(0, eq).trim();
    if (name === SESSION_COOKIE_NAME) {
      return decodeURIComponent(part.slice(eq + 1).trim());
    }
  }
  return null;
}

/**
 * constantTimeEqual compara dois valores opacos (IDs de sessão, tokens
 * CSRF) sem vazar informação por timing — mesmo cuidado de
 * identity.VerifyAPIToken no lado Go (crypto/subtle lá, crypto.
 * timingSafeEqual aqui).
 */
export function constantTimeEqual(a: string, b: string): boolean {
  const bufA = Buffer.from(a, "utf8");
  const bufB = Buffer.from(b, "utf8");
  if (bufA.length !== bufB.length) return false;
  return timingSafeEqual(bufA, bufB);
}
