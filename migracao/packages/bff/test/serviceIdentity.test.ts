import { test } from "node:test";
import assert from "node:assert/strict";
import jwt from "jsonwebtoken";
import { mintServiceIdentity } from "../src/serviceIdentity.js";

const secret = "01234567890123456789012345678901"; // 32 bytes

test("mintServiceIdentity produz claims exatas do contrato (sub, tenant, iat, exp) — nunca papel", () => {
  const token = mintServiceIdentity(secret, { sub: "42", tenant: "acme" }, 30);
  const decoded = jwt.verify(token, secret, { algorithms: ["HS256"] }) as jwt.JwtPayload;

  assert.equal(decoded.sub, "42");
  assert.equal((decoded as any).tenant, "acme");
  assert.ok(decoded.iat);
  assert.ok(decoded.exp);
  assert.equal((decoded as any).role_id, undefined);
  assert.equal((decoded as any).role, undefined);
});

test("mintServiceIdentity: exp respeita o ttl configurado", () => {
  const before = Math.floor(Date.now() / 1000);
  const token = mintServiceIdentity(secret, { sub: "1", tenant: "acme" }, 60);
  const decoded = jwt.verify(token, secret) as jwt.JwtPayload;
  assert.ok(decoded.exp! >= before + 59 && decoded.exp! <= before + 62);
});

test("token assinado com segredo diferente é rejeitado na verificação (prova que a assinatura importa)", () => {
  const token = mintServiceIdentity(secret, { sub: "1", tenant: "acme" }, 30);
  assert.throws(() => jwt.verify(token, "outro-segredo-de-32-bytes-aqui!!"));
});

test("mintServiceIdentity fixa o algoritmo HS256 explicitamente (defesa contra confusão de algoritmo)", () => {
  const token = mintServiceIdentity(secret, { sub: "1", tenant: "acme" }, 30);
  const header = JSON.parse(Buffer.from(token.split(".")[0]!, "base64url").toString("utf8"));
  assert.equal(header.alg, "HS256");
});
