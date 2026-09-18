// Protocolo do host de extensões (GO-022) — evolução do protocolo
// prototipado em docs/migracao-go/prototipos/GO-004-fronteira-rpc/host.cjs
// (JSON, uma mensagem por linha em stdin/stdout). Diferenças deliberadas
// em relação ao protótipo: `capabilities` explícitas por chamada (ADR-0005:
// "capacidades explícitas, não acesso irrestrito"), um único canal de
// callback genérico (`op`, não um global por singleton — recomendação #2
// do relatório de GO-004) e um envelope de erro tipado (`code` +
// `message`), nunca uma string de erro solta.
export const PROTOCOL_VERSION = 1;

/** Nomes de capacidade — cada um autoriza exatamente uma classe de callback. */
export type Capability = "db.read";

export interface EvalRequest {
  readonly type: "eval";
  readonly id: number;
  /** "expr": código de expressão livre. "call": função já registrada no host, chamada por NOME — nunca por closure serializada (achado de GO-004 §4: closures não atravessam a fronteira de processo). */
  readonly kind: "expr" | "call";
  readonly code?: string;
  readonly name?: string;
  readonly args?: Record<string, unknown>;
  readonly context?: { row?: Record<string, unknown>; user?: Record<string, unknown> | null };
  /** Capacidades concedidas para ESTA chamada — um callback fora desta lista é negado (ver ErrorCode.capability_denied). */
  readonly capabilities: readonly Capability[];
  /** Limite de execução síncrona desta chamada (ms) — aplicado via vm.Script({timeout}). Omitido usa o padrão do host. */
  readonly timeoutMs?: number;
}

/**
 * `unsupported_reference` (GO-023): lançado quando a expressão referencia um
 * singleton de domínio (`Table`/`File`/`View`) sem canal de callback
 * explícito — achado de GO-004 caso #6, onde essa referência virava
 * `undefined` SILENCIOSAMENTE. Este host nunca deixa isso passar em
 * silêncio: os estojos em `src/host.ts` lançam este código explicitamente
 * ao serem referenciados.
 */
export type ErrorCode =
  | "runtime_error"
  | "capability_denied"
  | "invalid_request"
  | "timeout"
  | "crashed"
  | "unsupported_reference";

export interface RpcError {
  readonly code: ErrorCode;
  readonly message: string;
}

export interface EvalResult {
  readonly type: "result";
  readonly id: number;
  readonly ok: boolean;
  readonly result?: unknown;
  readonly error?: RpcError;
  readonly evalMs: number;
}

export interface CallbackRequest {
  readonly type: "callback_request";
  readonly corr: string;
  readonly op: Capability;
  readonly args: Record<string, unknown>;
}

export interface CallbackResponse {
  readonly type: "callback_response";
  readonly corr: string;
  readonly result?: unknown;
  readonly error?: string;
}

export type HostToGoMessage = EvalResult | CallbackRequest;
export type GoToHostMessage = EvalRequest | CallbackResponse;

export function isCallbackResponse(msg: { type?: string }): msg is CallbackResponse {
  return msg.type === "callback_response";
}
