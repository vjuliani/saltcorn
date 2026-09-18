// Ponto de entrada do BFF — carrega configuração, sobe o servidor HTTP, e
// encerra graciosamente em SIGINT/SIGTERM: para de aceitar conexões novas
// e espera as requisições em curso terminarem (dentro de um timeout),
// mesmo padrão de shutdown.Tracker no backend Go (cmd/server/main.go).
import { createServer } from "node:http";
import { loadConfig } from "./config.js";
import { GoClient } from "./goClient.js";
import { InMemorySessionStore } from "./session.js";
import { buildRouter, createRequestListener } from "./app.js";
import { attachRealtime } from "./realtime.js";

function parseAddr(addr: string): { host?: string; port: number } {
  const [maybeHost, maybePort] = addr.split(":");
  if (maybePort) return { host: maybeHost || undefined, port: Number.parseInt(maybePort, 10) };
  return { port: Number.parseInt(maybeHost ?? "3100", 10) };
}

export function main(): void {
  const config = loadConfig();
  const sessionStore = new InMemorySessionStore();
  const goClient = new GoClient({ baseUrl: config.goInternalApiUrl, timeoutMs: config.goRequestTimeoutMs });

  let inFlight = 0;
  let draining = false;
  const onRequestStart = () => {
    inFlight++;
    return () => {
      inFlight--;
    };
  };

  const router = buildRouter({ config, sessionStore, goClient, onRequestStart });
  const listener = createRequestListener(router, { config, sessionStore, goClient });
  const server = createServer((req, res) => {
    if (draining) {
      res.writeHead(503, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: { code: "shutting_down", message: "servidor encerrando" } }));
      return;
    }
    void listener(req, res);
  });

  attachRealtime(server, { config, sessionStore, goClient });

  const { host, port } = parseAddr(config.httpAddr);
  server.listen(port, host, () => {
    console.log(JSON.stringify({ level: "info", msg: "saltcorn-bff iniciado", addr: config.httpAddr, goInternalApiUrl: config.goInternalApiUrl }));
  });

  const shutdown = (signal: string) => {
    console.log(JSON.stringify({ level: "info", msg: "sinal de encerramento recebido, drenando", signal }));
    draining = true;
    server.close();

    const deadline = Date.now() + config.shutdownTimeoutMs;
    const waitForDrain = setInterval(() => {
      if (inFlight === 0 || Date.now() > deadline) {
        clearInterval(waitForDrain);
        console.log(JSON.stringify({ level: "info", msg: "encerrado", inFlight }));
        process.exit(0);
      }
    }, 50);
  };

  process.on("SIGINT", () => shutdown("SIGINT"));
  process.on("SIGTERM", () => shutdown("SIGTERM"));
}

// Só roda main() quando este arquivo é o entrypoint (não quando importado por testes).
if (process.argv[1] && import.meta.url === `file://${process.argv[1]}`) {
  main();
}
