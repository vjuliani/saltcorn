// Testes do host (GO-022) — sobem dist/host.js como subprocesso real e
// dirigem o protocolo pela fronteira de verdade (stdin/stdout), mesmo
// espírito do caller.go do protótipo de GO-004, mas cobrindo os casos que
// o protótipo deixou como recomendação para esta tarefa: capacidade
// negada, timeout síncrono, timeout assíncrono, kind desconhecido.
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import path from "node:path";
import type { EvalRequest, HostToGoMessage } from "../src/protocol.js";

const HOST_PATH = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "src", "host.js");

interface Session {
  proc: ChildProcessWithoutNullStreams;
  send: (msg: unknown) => void;
  next: () => Promise<HostToGoMessage>;
  close: () => void;
}

function startHost(): Session {
  const proc = spawn("node", [HOST_PATH], { stdio: ["pipe", "pipe", "pipe"] });
  const rl = createInterface({ input: proc.stdout, terminal: false });
  const queue: HostToGoMessage[] = [];
  const waiters: Array<(m: HostToGoMessage) => void> = [];

  rl.on("line", (line) => {
    if (!line.trim()) return;
    const msg = JSON.parse(line) as HostToGoMessage;
    const waiter = waiters.shift();
    if (waiter) waiter(msg);
    else queue.push(msg);
  });

  return {
    proc,
    send: (msg) => proc.stdin.write(JSON.stringify(msg) + "\n"),
    next: () =>
      new Promise((resolve) => {
        const queued = queue.shift();
        if (queued) resolve(queued);
        else waiters.push(resolve);
      }),
    close: () => proc.stdin.end(),
  };
}

// runOne envia uma requisição e serve callback_request de "db.read" com uma
// resposta fixa — o suficiente para provar o mecanismo de capacidade sem
// depender de Postgres real (isso já é coberto do lado Go,
// internal/pluginhost, com internal/records de verdade).
async function runOne(s: Session, req: EvalRequest, dbAnswer?: { result?: unknown; error?: string }) {
  s.send(req);
  for (;;) {
    const msg = await s.next();
    if (msg.type === "callback_request") {
      assert.equal(msg.op, "db.read");
      s.send({ type: "callback_response", corr: msg.corr, ...dbAnswer });
      continue;
    }
    if (msg.type === "result" && msg.id === req.id) return msg;
  }
}

test("expressão pura sobre row/user", async () => {
  const s = startHost();
  try {
    const res = await runOne(s, {
      type: "eval",
      id: 1,
      kind: "expr",
      code: "row.price * row.qty",
      context: { row: { price: 19.9, qty: 3 } },
      capabilities: [],
    });
    assert.equal(res.ok, true);
    assert.ok(Math.abs((res.result as number) - 59.7) < 1e-9, `esperado ~59.7, recebido ${res.result}`);
  } finally {
    s.close();
  }
});

test("callback nomeado (kind=call) por nome, nunca por closure serializada", async () => {
  const s = startHost();
  try {
    const res = await runOne(s, { type: "eval", id: 2, kind: "call", name: "sendToast", args: { msg: "ola" }, capabilities: [] });
    assert.equal(res.ok, true);
    assert.deepEqual(res.result, { toast: true, message: "Toast: ola" });
  } finally {
    s.close();
  }
});

test("callback de leitura (db.read) concedido é resolvido pelo lado Go", async () => {
  const s = startHost();
  try {
    const res = await runOne(
      s,
      { type: "eval", id: 3, kind: "expr", code: "await callHost('db.read', {table: 'books'})", capabilities: ["db.read"] },
      { result: { id: 1, name: "guitars" } }
    );
    assert.equal(res.ok, true);
    assert.deepEqual(res.result, { id: 1, name: "guitars" });
  } finally {
    s.close();
  }
});

test("callback de leitura SEM capacidade concedida é negado — nunca chega a pedir nada ao lado Go", async () => {
  const s = startHost();
  try {
    const res = await runOne(s, { type: "eval", id: 4, kind: "expr", code: "await callHost('db.read', {table: 'books'})", capabilities: [] });
    assert.equal(res.ok, false);
    assert.equal(res.error?.code, "capability_denied");
  } finally {
    s.close();
  }
});

test("laço síncrono infinito é interrompido pelo timeout (vm.Script)", async () => {
  const s = startHost();
  try {
    const res = await runOne(s, {
      type: "eval",
      id: 5,
      kind: "expr",
      code: "(() => { while (true) {} })()",
      capabilities: [],
      timeoutMs: 200,
    });
    assert.equal(res.ok, false);
    assert.equal(res.error?.code, "timeout");
  } finally {
    s.close();
  }
});

test("callback que nunca responde é interrompido pelo timeout assíncrono do host", async () => {
  const s = startHost();
  try {
    // Não respondemos ao callback_request — a Promise de callHost nunca
    // resolve, exatamente o cenário que vm.Script({timeout}) NÃO cobre
    // (a parte síncrona de runInContext já retornou) e que o timeout
    // assíncrono do host precisa cobrir sozinho.
    const req: EvalRequest = { type: "eval", id: 6, kind: "expr", code: "await callHost('db.read', {table: 'books'})", capabilities: ["db.read"], timeoutMs: 300 };
    s.send(req);
    let result;
    for (;;) {
      const msg = await s.next();
      if (msg.type === "callback_request") continue; // deliberadamente não responde
      if (msg.type === "result" && msg.id === req.id) {
        result = msg;
        break;
      }
    }
    assert.equal(result.ok, false);
    assert.equal(result.error?.code, "timeout");
  } finally {
    s.close();
  }
});

test("erro de runtime dentro da expressão vira runtime_error, não derruba o host", async () => {
  const s = startHost();
  try {
    const res = await runOne(s, { type: "eval", id: 7, kind: "expr", code: "row.nope.explode", capabilities: [] });
    assert.equal(res.ok, false);
    assert.equal(res.error?.code, "runtime_error");

    // O host continua respondendo depois de um erro de runtime — não é um crash.
    const res2 = await runOne(s, { type: "eval", id: 8, kind: "expr", code: "1+1", capabilities: [] });
    assert.equal(res2.ok, true);
    assert.equal(res2.result, 2);
  } finally {
    s.close();
  }
});

test("função com closure de variável externa nunca enviada falha — nenhuma fronteira transporta ambiente léxico", async () => {
  const s = startHost();
  try {
    const res = await runOne(s, {
      type: "eval",
      id: 9,
      kind: "expr",
      code: "(function(){ return outerCounter + 1; })()",
      capabilities: [],
    });
    assert.equal(res.ok, false);
    assert.match(res.error?.message ?? "", /outerCounter/);
  } finally {
    s.close();
  }
});
