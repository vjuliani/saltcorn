import { test } from "node:test";
import assert from "node:assert/strict";
import { InMemorySessionStore, readSessionCookie, sessionCookieHeader, constantTimeEqual, SESSION_COOKIE_NAME } from "../src/session.js";

test("InMemorySessionStore: create/get/touch/destroy", async () => {
  const store = new InMemorySessionStore();
  const id = await store.create({ userId: "42", tenant: "acme" });

  const data = await store.get(id);
  assert.deepEqual(data, { userId: "42", tenant: "acme" });

  await store.touch(id);
  assert.deepEqual(await store.get(id), { userId: "42", tenant: "acme" });

  await store.destroy(id);
  assert.equal(await store.get(id), null);
});

test("InMemorySessionStore: sessão inexistente retorna null, não lança", async () => {
  const store = new InMemorySessionStore();
  assert.equal(await store.get("nao-existe"), null);
});

test("readSessionCookie extrai o valor certo entre vários cookies", () => {
  const header = `outro=x; ${SESSION_COOKIE_NAME}=abc123; sc_csrf=def456`;
  assert.equal(readSessionCookie(header), "abc123");
});

test("readSessionCookie retorna null sem o cookie de sessão", () => {
  assert.equal(readSessionCookie("outro=x; sc_csrf=def456"), null);
  assert.equal(readSessionCookie(undefined), null);
});

test("sessionCookieHeader inclui os atributos fixados em ADR-0007", () => {
  const header = sessionCookieHeader("abc123");
  assert.match(header, /HttpOnly/);
  assert.match(header, /Secure/);
  assert.match(header, /SameSite=Lax/);
  assert.match(header, /Max-Age=86400/); // 24h
});

test("constantTimeEqual: igual e diferente", () => {
  assert.equal(constantTimeEqual("abc", "abc"), true);
  assert.equal(constantTimeEqual("abc", "abd"), false);
  assert.equal(constantTimeEqual("abc", "abcd"), false);
});
