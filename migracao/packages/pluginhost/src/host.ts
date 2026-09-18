// Host de extensões (GO-022) — processo de vida longa, promovido do
// protótipo de GO-004 (docs/migracao-go/prototipos/GO-004-fronteira-rpc/).
// Lê requisições JSON (uma por linha) de stdin, avalia usando o módulo
// `vm` NATIVO do Node (nunca `vm2` — ver nota de escopo 2 em
// docs/migracao-go/execucoes/GO-022.md: `vm2` está descontinuado e o
// próprio relatório de GO-004 registra que usá-lo lá foi deliberado para
// medir a fronteira, não uma recomendação para o host final), e responde
// por stdout.
//
// A fronteira de segurança real deste host é o PROCESSO (isolamento de
// SO, limite de memória via --max-old-space-size na hora de spawnar,
// timeout/kill do lado Go) — ADR-0005: "simples uso de VM não constitui
// toda a fronteira de segurança". O timeout de `vm.Script` aqui dentro é
// só a primeira linha de defesa (contém a maioria dos laços síncronos
// sem precisar matar o processo inteiro); a garantia de verdade contra
// código que nunca resolve (síncrono ou assíncrono) é o watchdog do
// cliente Go, que mata e reinicia o processo do host — ver
// internal/pluginhost/client.go.
import { createContext, Script } from "node:vm";
import { createInterface } from "node:readline";
import type {
  Capability,
  CallbackResponse,
  EvalRequest,
  EvalResult,
  RpcError,
} from "./protocol.js";
import { isCallbackResponse } from "./protocol.js";

const DEFAULT_SYNC_TIMEOUT_MS = 2000;
const DEFAULT_ASYNC_TIMEOUT_MS = 5000;

// "Funções registradas por um plugin" — simula o padrão real (State real
// carrega objetos/funções JS em runtime, nunca serializa código do lado
// Go) — mesmo espírito do protótipo de GO-004. Nenhum inventário real de
// plugin de terceiro existe neste checkout (lacuna já registrada desde
// GO-001/GO-003/GO-004); isto demonstra o MECANISMO de chamada por nome,
// não um catálogo de plugins de produção.
const registeredFunctions: Record<string, (args: Record<string, unknown>) => unknown> = {
  sendToast: (args) => ({ toast: true, message: `Toast: ${String(args.msg ?? "")}` }),
};

/**
 * unsupportedSingleton (GO-023) fecha a lacuna concreta de GO-004 caso #6:
 * uma expressão que referencia um singleton de domínio (`Table`/`File`/
 * `View`) sem canal de callback explícito virava `undefined` do lado Node,
 * um resultado ERRADO SILENCIOSO (ex.: `Table.findOne(...)` vira
 * `undefined.findOne` só se a expressão tentar CHAMAR um método — mas só
 * referenciar `Table` numa condição booleana, por exemplo, passava batido
 * como falsy sem nenhum erro). Este proxy lança explicitamente em
 * qualquer leitura de propriedade, `in`, ou chamada — nunca deixa a
 * referência virar um valor manso; é exatamente o "fallback explícito
 * quando a semântica diverge" exigido pelo critério de aceite de GO-023.
 */
function unsupportedSingleton(name: string): unknown {
  const deny = () => {
    throw Object.assign(
      new Error(
        `referência a ${name} não suportada nesta fronteira — singleton de domínio sem canal de callback explícito (GO-004 caso #6, ver GO-023)`,
      ),
      { rpcCode: "unsupported_reference" },
    );
  };
  return new Proxy(function () {} as unknown as object, {
    get: deny,
    has: deny,
    apply: deny,
    construct: deny,
  });
}

let nextCorr = 1;
const pendingCallbacks = new Map<string, { resolve: (v: unknown) => void; reject: (e: Error) => void }>();

function writeMessage(msg: unknown): void {
  process.stdout.write(JSON.stringify(msg) + "\n");
}

/**
 * requestCallback é o ÚNICO canal de callback — genérico por `op`, não um
 * global por singleton (recomendação #2 do relatório de GO-004). A
 * checagem de capacidade acontece ANTES de qualquer callback_request sair
 * para o lado Go — o host nunca pede ao Go algo que a chamada não tinha
 * permissão de pedir, e o cliente Go (defesa em profundidade) valida a
 * MESMA coisa de novo do lado dele.
 */
function requestCallback(op: Capability, args: Record<string, unknown>, granted: readonly Capability[]): Promise<unknown> {
  if (!granted.includes(op)) {
    return Promise.reject(Object.assign(new Error(`capacidade não concedida: ${op}`), { rpcCode: "capability_denied" }));
  }
  return new Promise((resolve, reject) => {
    const corr = "c" + nextCorr++;
    pendingCallbacks.set(corr, { resolve, reject });
    writeMessage({ type: "callback_request", corr, op, args });
  });
}

function classifyError(e: unknown): RpcError {
  const err = e as { rpcCode?: string; message?: string; name?: string };
  if (err?.rpcCode === "capability_denied") {
    return { code: "capability_denied", message: err.message ?? "capacidade não concedida" };
  }
  if (err?.rpcCode === "unsupported_reference") {
    return { code: "unsupported_reference", message: err.message ?? "referência não suportada" };
  }
  // `rpcCode: "timeout"` cobre o timeout ASSÍNCRONO deste host (a corrida
  // com `asyncTimeout`); a checagem de nome/mensagem cobre o timeout
  // SÍNCRONO que o próprio `vm.Script.runInContext` lança (nunca marca
  // `rpcCode`, é um erro nativo do V8).
  if (err?.rpcCode === "timeout" || err?.name === "TimeoutError" || /Script execution timed out/.test(err?.message ?? "")) {
    return { code: "timeout", message: err.message ?? "tempo de execução excedido" };
  }
  return { code: "runtime_error", message: err?.message ?? String(e) };
}

async function handleEval(req: EvalRequest): Promise<void> {
  const start = process.hrtime.bigint();
  let ok = true;
  let result: unknown;
  let error: RpcError | undefined;

  const asyncTimeout = new Promise<never>((_, reject) => {
    setTimeout(() => reject(Object.assign(new Error("tempo de execução excedido"), { rpcCode: "timeout" })), req.timeoutMs ?? DEFAULT_ASYNC_TIMEOUT_MS);
  });

  try {
    if (req.kind === "call") {
      const fn = registeredFunctions[req.name ?? ""];
      if (!fn) throw new Error(`função registrada desconhecida: ${req.name}`);
      result = await Promise.race([Promise.resolve(fn(req.args ?? {})), asyncTimeout]);
    } else if (req.kind === "expr") {
      if (typeof req.code !== "string") throw Object.assign(new Error("expr requer code"), { rpcCode: "invalid_request" });
      // Achado desta tarefa: um EvalRequest.Capabilities nil do lado Go
      // serializa como `"capabilities": null` (Go não usa `omitempty` aqui
      // de propósito — nil e lista vazia significam a MESMA coisa,
      // "nenhuma capacidade concedida"), e `null.includes` derrubaria esta
      // chamada com um TypeError classificado erroneamente como
      // runtime_error em vez de capability_denied. `?? []` normaliza os
      // dois casos (ausente/null/vazio) para o mesmo comportamento.
      const granted = req.capabilities ?? [];
      const sandbox: Record<string, unknown> = {
        row: req.context?.row ?? {},
        user: req.context?.user ?? null,
        Date,
        callHost: (op: Capability, args: Record<string, unknown>) => requestCallback(op, args, granted),
        // GO-023: nunca undefined silencioso — ver unsupportedSingleton.
        Table: unsupportedSingleton("Table"),
        File: unsupportedSingleton("File"),
        View: unsupportedSingleton("View"),
      };
      const context = createContext(sandbox);
      const wrapped = `(async () => { return (${req.code}); })()`;
      const script = new Script(wrapped);
      const evalPromise = script.runInContext(context, { timeout: req.timeoutMs ?? DEFAULT_SYNC_TIMEOUT_MS }) as Promise<unknown>;
      result = await Promise.race([evalPromise, asyncTimeout]);
    } else {
      throw Object.assign(new Error(`kind desconhecido: ${String((req as { kind: unknown }).kind)}`), { rpcCode: "invalid_request" });
    }
  } catch (e) {
    ok = false;
    error = classifyError(e);
  }

  const evalMs = Number(process.hrtime.bigint() - start) / 1e6;

  // Mesma etapa de "deproxy" já usada em produção (models/expression.ts)
  // e no protótipo de GO-004: tira o valor do contexto da VM via JSON —
  // é o que faz Date virar string silenciosamente (achado já registrado,
  // não uma regressão desta tarefa).
  let resultOut = result;
  if (ok) {
    try {
      resultOut = JSON.parse(JSON.stringify(result === undefined ? null : result));
    } catch {
      resultOut = String(result);
    }
  }

  const response: EvalResult = { type: "result", id: req.id, ok, result: resultOut, error, evalMs };
  writeMessage(response);
}

const rl = createInterface({ input: process.stdin, terminal: false });

rl.on("line", (line) => {
  if (!line.trim()) return;
  let msg: unknown;
  try {
    msg = JSON.parse(line);
  } catch {
    process.stderr.write(`pluginhost: linha inválida: ${line}\n`);
    return;
  }

  if (isCallbackResponse(msg as CallbackResponse)) {
    const cb = msg as CallbackResponse;
    const pending = pendingCallbacks.get(cb.corr);
    if (pending) {
      pendingCallbacks.delete(cb.corr);
      if (cb.error) pending.reject(new Error(cb.error));
      else pending.resolve(cb.result);
    }
    return;
  }

  void handleEval(msg as EvalRequest);
});

process.stdin.on("end", () => process.exit(0));
