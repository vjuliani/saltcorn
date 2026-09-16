import { test } from "node:test";
import assert from "node:assert/strict";
import { generateCsrfToken, verifyCsrf, csrfCookieHeader, readCsrfCookie } from "../src/csrf.js";

test("verifyCsrf: cookie e header batem", () => {
  const token = generateCsrfToken();
  assert.equal(verifyCsrf(token, token), true);
});

test("verifyCsrf: header ausente é rejeitado", () => {
  assert.equal(verifyCsrf(generateCsrfToken(), undefined), false);
  assert.equal(verifyCsrf(generateCsrfToken(), null), false);
});

test("verifyCsrf: cookie ausente é rejeitado", () => {
  assert.equal(verifyCsrf(null, generateCsrfToken()), false);
});

test("verifyCsrf: valores diferentes são rejeitados", () => {
  assert.equal(verifyCsrf(generateCsrfToken(), generateCsrfToken()), false);
});

test("csrfCookieHeader não é HttpOnly (front-end precisa ler para ecoar no header)", () => {
  const header = csrfCookieHeader("abc");
  assert.doesNotMatch(header, /HttpOnly/);
  assert.match(header, /Secure/);
});

test("readCsrfCookie extrai o valor certo", () => {
  assert.equal(readCsrfCookie("sc_session=x; sc_csrf=tok123"), "tok123");
  assert.equal(readCsrfCookie("sc_session=x"), null);
});

test("generateCsrfToken produz valores diferentes a cada chamada", () => {
  const a = generateCsrfToken();
  const b = generateCsrfToken();
  assert.notEqual(a, b);
});
