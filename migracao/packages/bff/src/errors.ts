// Formato de erro de common.yaml#/components/schemas/Error, reaproveitado
// por bff-api.yaml — {error: {code, message}}. BffError é o tipo interno
// que todo handler usa para sinalizar um erro de resposta; nunca deixamos
// uma exceção genérica virar corpo de resposta (poderia ecoar detalhe
// interno, ex.: stack trace ou mensagem crua do fetch).

export class BffError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string
  ) {
    super(message);
  }
}

export function sessionRequiredError(): BffError {
  return new BffError(401, "session_required", "sessão de navegador ausente ou expirada");
}

export function csrfInvalidError(): BffError {
  return new BffError(403, "csrf_invalid", "token CSRF ausente ou inválido");
}

/**
 * forbiddenError (GO-044) — usado só pela rota force-logout, que NUNCA
 * chama o Go (destrói sessões no store do próprio BFF, ADR-0007), então
 * não existe um 403 do Go para propagar; toda outra rota administrativa
 * deixa o Go (`identity.requireAdmin`) decidir e só repassa o erro dele.
 */
export function forbiddenError(): BffError {
  return new BffError(403, "not_authorized", "ator não tem papel suficiente para esta operação");
}

/** impersonationNotActiveError (GO-044) — /admin/impersonation/end chamado numa sessão que não é de impersonação. */
export function impersonationNotActiveError(): BffError {
  return new BffError(409, "impersonation_not_active", "esta sessão não é uma impersonação ativa");
}

/**
 * domainUnavailableError cobre o critério de aceite "falhas/timeouts Go
 * geram erros controlados" (ADR-0003: "indisponibilidade do backend Go
 * deve produzir erro controlado na UI, o BFF precisa de timeout, não pode
 * travar esperando indefinidamente").
 */
export function domainUnavailableError(): BffError {
  return new BffError(502, "domain_unavailable", "não foi possível completar a operação — tente novamente");
}

export interface ErrorBody {
  readonly error: {
    readonly code: string;
    readonly message: string;
  };
}

export function toErrorBody(err: BffError): ErrorBody {
  return { error: { code: err.code, message: err.message } };
}
