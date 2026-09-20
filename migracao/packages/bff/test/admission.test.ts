import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { GoClient } from "../src/goClient.js";

test("GoClient rejeita sem fila, mantém limite até terminar o corpo e libera após timeout", async () => {
  let hanging = true;
  let entered!: () => void;
  const received = new Promise<void>((resolve) => {
    entered = resolve;
  });
  const server = createServer((_req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    if (hanging) {
      res.write('{"actor":');
      entered();
    } else res.end("{}");
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const address = server.address() as { port: number };
  const client = new GoClient({
    baseUrl: `http://127.0.0.1:${address.port}`,
    timeoutMs: 200,
    maxInFlight: 1,
  });
  try {
    const first = assert.rejects(client.getActor("token", "tenant"), {
      status: 502,
    });
    await received;
    await assert.rejects(client.getActor("token", "tenant"), { status: 502 });
    assert.deepEqual(client.concurrency, {
      active: 1,
      peak: 1,
      rejected: 1,
      limit: 1,
    });
    await first;
    assert.equal(client.concurrency.active, 0);
    hanging = false;
    await client.getActor("token", "tenant");
    assert.equal(client.concurrency.active, 0);
  } finally {
    server.closeAllConnections();
    server.close();
  }
});
