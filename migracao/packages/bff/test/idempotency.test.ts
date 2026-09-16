import { test } from "node:test";
import assert from "node:assert/strict";
import { computeIdempotencyKey } from "../src/idempotency.js";

test("mesmo ator/tenant/tabela/corpo produz a MESMA chave — retry reaproveita, não gera nova (bff-api.yaml)", () => {
  const k1 = computeIdempotencyKey("42", "acme", "widgets", { label: "x" });
  const k2 = computeIdempotencyKey("42", "acme", "widgets", { label: "x" });
  assert.equal(k1, k2);
});

test("ordem de inserção das chaves do corpo não muda a chave (canonicalização)", () => {
  const k1 = computeIdempotencyKey("42", "acme", "widgets", { a: 1, b: 2 });
  const k2 = computeIdempotencyKey("42", "acme", "widgets", { b: 2, a: 1 });
  assert.equal(k1, k2);
});

test("corpo diferente produz chave diferente", () => {
  const k1 = computeIdempotencyKey("42", "acme", "widgets", { label: "x" });
  const k2 = computeIdempotencyKey("42", "acme", "widgets", { label: "y" });
  assert.notEqual(k1, k2);
});

test("ator diferente produz chave diferente (mesmo corpo)", () => {
  const k1 = computeIdempotencyKey("42", "acme", "widgets", { label: "x" });
  const k2 = computeIdempotencyKey("43", "acme", "widgets", { label: "x" });
  assert.notEqual(k1, k2);
});

test("tenant diferente produz chave diferente (mesmo corpo)", () => {
  const k1 = computeIdempotencyKey("42", "acme", "widgets", { label: "x" });
  const k2 = computeIdempotencyKey("42", "beta", "widgets", { label: "x" });
  assert.notEqual(k1, k2);
});

test("tabela diferente produz chave diferente (mesmo corpo)", () => {
  const k1 = computeIdempotencyKey("42", "acme", "widgets", { label: "x" });
  const k2 = computeIdempotencyKey("42", "acme", "gadgets", { label: "x" });
  assert.notEqual(k1, k2);
});
