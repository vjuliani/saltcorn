// computeIdempotencyKey — bff-api.yaml exige que "o BFF gera e propaga a
// Idempotency-Key para a chamada interna correspondente; um retry do
// navegador reaproveita a mesma chave, não gera uma nova". Uma chave
// aleatória por requisição não cumpriria isso (um retry geraria uma chave
// NOVA, perdendo a proteção de GO-014 contra duplicação) — em vez disso a
// chave é determinística: um hash de (ator, tenant, tabela, corpo
// canonicalizado). Duas requisições idênticas do mesmo ator produzem a
// MESMA chave sem nenhum estado adicional no BFF (nenhuma tabela de
// "requisições em voo" a manter); duas requisições com corpos diferentes
// produzem chaves diferentes, então não colidem no outbox do Go.
import { createHash } from "node:crypto";

export function computeIdempotencyKey(userId: string, tenant: string, table: string, body: Record<string, unknown>): string {
  const canonical = canonicalize(body);
  const hash = createHash("sha256");
  hash.update(userId);
  hash.update(":");
  hash.update(tenant);
  hash.update(":");
  hash.update(table);
  hash.update(":");
  hash.update(canonical);
  return hash.digest("hex");
}

/** canonicalize serializa um objeto com as chaves ordenadas — o mesmo conteúdo lógico produz sempre o mesmo texto, independente da ordem de inserção original. */
function canonicalize(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map(canonicalize).join(",")}]`;
  }
  const keys = Object.keys(value as Record<string, unknown>).sort();
  const entries = keys.map((k) => `${JSON.stringify(k)}:${canonicalize((value as Record<string, unknown>)[k])}`);
  return `{${entries.join(",")}}`;
}
