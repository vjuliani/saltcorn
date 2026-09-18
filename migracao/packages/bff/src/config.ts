// Configuração por variável de ambiente — mesmo espírito de
// internal/platform/config no backend Go: um único lugar que lê o
// ambiente, com padrões seguros e falha explícita quando um segredo
// obrigatório está ausente, nunca um valor inseguro por omissão.

export interface Config {
  /** Endereço em que o BFF escuta (ex.: ":3100"). */
  readonly httpAddr: string;
  /** URL base da API interna do backend Go (ex.: "http://localhost:8090"). */
  readonly goInternalApiUrl: string;
  /**
   * Segredo compartilhado HMAC para assinar o ServiceIdentity (JWT HS256,
   * ADR-0003/ADR-0007) — o MESMO segredo que `internal/platform/tenancy.
   * Verifier` usa para verificar do lado Go. Sem ele, o BFF não sobe: um
   * BFF que não consegue assinar identidade delegada verificável não pode
   * chamar o domínio com segurança nenhuma.
   */
  readonly serviceIdentitySecret: string;
  /** Tempo máximo de vida do ServiceIdentity assinado pelo BFF. */
  readonly serviceIdentityTtlSeconds: number;
  /** Timeout de toda chamada ao backend Go (ADR-0003: nunca travar esperando indefinidamente). */
  readonly goRequestTimeoutMs: number;
  /** Tempo máximo esperando requisições em curso antes de forçar a saída. */
  readonly shutdownTimeoutMs: number;
  /**
   * Intervalo de polling de src/realtime.ts (GO-028) a .../realtime/events,
   * por socket conectado — a cada tick também revalida a sessão de
   * navegador (ver realtime.ts), então este valor também limita o atraso
   * máximo para desconectar um socket cuja sessão expirou.
   */
  readonly realtimePollIntervalMs: number;
}

export class ConfigError extends Error {}

const DEFAULT_HTTP_ADDR = ":3100";
const DEFAULT_GO_INTERNAL_API_URL = "http://localhost:8090";
const DEFAULT_SERVICE_IDENTITY_TTL_SECONDS = 30;
const DEFAULT_GO_REQUEST_TIMEOUT_MS = 5000;
const DEFAULT_SHUTDOWN_TIMEOUT_MS = 15000;
const DEFAULT_REALTIME_POLL_INTERVAL_MS = 500;

/** Piso de tamanho do segredo — mesmo valor de tenancy.NewVerifier no Go, os dois lados precisam concordar. */
const MIN_SECRET_BYTES = 32;

export function loadConfig(env: NodeJS.ProcessEnv = process.env): Config {
  const serviceIdentitySecret = env.SALTCORN_BFF_SERVICE_IDENTITY_SECRET ?? "";
  if (Buffer.byteLength(serviceIdentitySecret, "utf8") < MIN_SECRET_BYTES) {
    throw new ConfigError(
      `SALTCORN_BFF_SERVICE_IDENTITY_SECRET precisa ter pelo menos ${MIN_SECRET_BYTES} bytes (mesmo piso de tenancy.NewVerifier no Go) — recebeu ${Buffer.byteLength(serviceIdentitySecret, "utf8")}`
    );
  }

  return {
    httpAddr: env.SALTCORN_BFF_HTTP_ADDR ?? DEFAULT_HTTP_ADDR,
    goInternalApiUrl: env.SALTCORN_BFF_GO_INTERNAL_API_URL ?? DEFAULT_GO_INTERNAL_API_URL,
    serviceIdentitySecret,
    serviceIdentityTtlSeconds: parsePositiveInt(env.SALTCORN_BFF_SERVICE_IDENTITY_TTL_SECONDS, DEFAULT_SERVICE_IDENTITY_TTL_SECONDS),
    goRequestTimeoutMs: parsePositiveInt(env.SALTCORN_BFF_GO_REQUEST_TIMEOUT_MS, DEFAULT_GO_REQUEST_TIMEOUT_MS),
    shutdownTimeoutMs: parsePositiveInt(env.SALTCORN_BFF_SHUTDOWN_TIMEOUT_MS, DEFAULT_SHUTDOWN_TIMEOUT_MS),
    realtimePollIntervalMs: parsePositiveInt(env.SALTCORN_BFF_REALTIME_POLL_INTERVAL_MS, DEFAULT_REALTIME_POLL_INTERVAL_MS),
  };
}

function parsePositiveInt(raw: string | undefined, fallback: number): number {
  if (!raw) return fallback;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}
