#!/usr/bin/env node
// GO-004 — host de expressoes/callbacks para o experimento de fronteira RPC Go<->Node.
//
// Protocolo (JSON, uma mensagem por linha):
//   Go -> Node  {id, kind: "expr"|"call"|"raw_fn_attempt"|"db", code?, name?, args?, context?}
//   Node -> Go  {id, type:"result", ok, result?, error?, evalMs}
//   Node -> Go  {type:"callback_request", corr, op, args}   (somente kind "db")
//   Go -> Node  {type:"callback_response", corr, result?, error?}
//
// Usa vm2 (a mesma biblioteca de packages/saltcorn-data/models/expression.ts) para
// avaliar "expr"/"db" com uma sandbox minima {row, user, Date} — nao reimplementa
// toda a superficie de contexto real (Table/File/View/User completos), que e
// justamente o que este experimento existe para caracterizar como incompativel
// por padrao (ver caso #6).
"use strict";

const path = require("path");
const readline = require("readline");
const { VM } = require(
  path.join(__dirname, "..", "..", "..", "..", "packages", "saltcorn-data", "node_modules", "vm2")
);

// Funcoes "registradas por um plugin" — simulam uma acao e um tipo customizado
// do jeito que o State real faz hoje (objetos/funcoes JS carregados no processo
// que hospeda o motor, nunca serializados a partir do lado Go).
const registeredFunctions = {
  sendToast: ({ msg }) => ({ toast: true, message: `Toast: ${msg}` }),
  customType_currency_read: ({ value, attrs }) => {
    const n = parseFloat(value);
    const currency = (attrs && attrs.currency) || "USD";
    return `${currency} ${n.toFixed(2)}`;
  },
};

let nextCorr = 1;
const pendingCallbacks = new Map();

function requestCallback(op, args) {
  return new Promise((resolve, reject) => {
    const corr = "c" + nextCorr++;
    pendingCallbacks.set(corr, { resolve, reject });
    process.stdout.write(JSON.stringify({ type: "callback_request", corr, op, args }) + "\n");
  });
}

const rl = readline.createInterface({ input: process.stdin, terminal: false });

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg;
  try {
    msg = JSON.parse(line);
  } catch (e) {
    process.stderr.write("host.cjs: linha invalida: " + line + "\n");
    return;
  }

  if (msg.type === "callback_response") {
    const p = pendingCallbacks.get(msg.corr);
    if (p) {
      pendingCallbacks.delete(msg.corr);
      if (msg.error) p.reject(new Error(msg.error));
      else p.resolve(msg.result);
    }
    return;
  }

  handleRequest(msg);
});

async function handleRequest(req) {
  const start = process.hrtime.bigint();
  let ok = true;
  let result;
  let error;
  try {
    if (req.kind === "call") {
      // Callback "por nome": o lado Go nunca envia codigo de funcao, apenas um
      // identificador — o padrao viavel que este experimento recomenda.
      const fn = registeredFunctions[req.name];
      if (!fn) throw new Error(`funcao registrada desconhecida: ${req.name}`);
      result = await fn(req.args || {});
    } else if (req.kind === "raw_fn_attempt") {
      // Caso negativo deliberado: o lado Go manda TEXTO de uma funcao que
      // referencia uma variavel do ambiente lexico original (que so existia
      // do lado Go). Isso NUNCA deveria funcionar — e nao funciona: a funcao
      // e recriada do zero aqui dentro, sem o closure original.
      const vm = new VM({ sandbox: {}, eval: false, wasm: false });
      const fn = vm.run(`(${req.code})`);
      result = fn();
    } else if (req.kind === "expr" || req.kind === "db") {
      const sandbox = {
        row: (req.context && req.context.row) || {},
        user: (req.context && req.context.user) || null,
        Date,
      };
      if (req.kind === "db") {
        // Nenhuma credencial de banco chega aqui — apenas uma funcao-ponte que
        // pede ao lado Go para resolver a consulta e devolve o resultado.
        sandbox.Table = {
          findOne: (q) => requestCallback("Table.findOne", q),
        };
      }
      const vm = new VM({ sandbox, eval: false, wasm: false });
      const wrapped = `(async () => { return (${req.code}); })()`;
      result = await vm.run(wrapped);
    } else {
      throw new Error(`kind desconhecido: ${req.kind}`);
    }
  } catch (e) {
    ok = false;
    error = (e && e.message) || String(e);
  }
  const end = process.hrtime.bigint();
  const evalMs = Number(end - start) / 1e6;

  // Mesma etapa de "deproxy" da producao (expression.ts): JSON.parse(JSON.stringify(...))
  // para tirar o valor do proxy do vm2 — e o que silenciosamente vira Date em string.
  let resultOut = result;
  if (ok) {
    try {
      resultOut = JSON.parse(JSON.stringify(result === undefined ? null : result));
    } catch (e) {
      resultOut = String(result);
    }
  }

  process.stdout.write(
    JSON.stringify({ id: req.id, type: "result", ok, result: resultOut, error, evalMs }) + "\n"
  );
}

process.stdin.on("end", () => process.exit(0));
