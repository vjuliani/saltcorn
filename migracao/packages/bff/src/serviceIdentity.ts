// ServiceIdentity — o JWT de vida curta que o BFF assina para chamar o
// backend Go (ADR-0003, contrato `ServiceIdentity` em internal-api.yaml,
// verificado do lado Go por internal/platform/tenancy.Verifier). Claims
// exatas do contrato: {sub, tenant, iat, exp} — nunca papel/permissões
// (ADR-0007: o Go sempre resolve o papel atual, nunca confia num valor
// vindo do BFF).
//
// Usa a biblioteca `jsonwebtoken` (madura) em vez de assinar HMAC à mão —
// ao contrário do padrão "sem SDK externo" de ADR-0009 (que se aplicava
// especificamente a observabilidade, sem coletor real para validar
// contra): aqui a superfície é pequena, mas verificação de JWT é uma
// classe de bug com histórico conhecido (confusão de algoritmo, etc.) —
// o próprio backend Go usa uma biblioteca madura para o mesmo motivo
// (golang-jwt/jwt/v5, GO-008), não hand-rolled.

import jwt from "jsonwebtoken";

export interface ServiceIdentityClaims {
  readonly sub: string;
  readonly tenant: string;
}

/**
 * mintServiceIdentity assina um token HS256 de vida curta — `algorithm`
 * fixado explicitamente (nunca deixado para o token decidir) para não
 * abrir a porta ao ataque clássico de confusão de algoritmo
 * ("alg": "none" ou trocar para um algoritmo assimétrico usando a chave
 * pública como segredo HMAC).
 */
export function mintServiceIdentity(secret: string, claims: ServiceIdentityClaims, ttlSeconds: number): string {
  return jwt.sign({ sub: claims.sub, tenant: claims.tenant }, secret, {
    algorithm: "HS256",
    expiresIn: ttlSeconds,
  });
}
