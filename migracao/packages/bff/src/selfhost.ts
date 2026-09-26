// Optional release entrypoint: serves the packaged UI and exchanges short-lived
// operator-issued login tickets. Domain access still goes through GoClient.
import type { IncomingMessage, ServerResponse } from "node:http";
import { createReadStream } from "node:fs";
import { realpath, stat } from "node:fs/promises";
import path from "node:path";
import jwt from "jsonwebtoken";
import type { AppDeps } from "./app.js";
import { readJSONBody, sendJSON } from "./httpHelpers.js";
import { sessionCookieHeader } from "./session.js";
import { csrfCookieHeader, generateCsrfToken } from "./csrf.js";
import { mintServiceIdentity } from "./serviceIdentity.js";

const loginPage = `<!doctype html><html lang="pt-BR"><meta charset="utf-8"><title>Entrar no Saltcorn</title><body><p id="status">Validando acesso…</p><script>
const ticket=location.hash.slice(1);history.replaceState(null,'','/login');
fetch('/api/bff/operator-session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ticket})}).then(r=>{if(!r.ok)throw Error();location.replace('/')}).catch(()=>{document.getElementById('status').textContent='Acesso expirado ou inválido. Gere outro link com o comando login.'});
</script></body></html>`;

export function selfHostedListener(deps: AppDeps, fallback: (req: IncomingMessage, res: ServerResponse) => Promise<void>, options: { root: string; tenant: string; installationId: string }) {
  const used = new Map<string, number>();
  return async (req: IncomingMessage, res: ServerResponse): Promise<void> => {
    try {
      const pathname = new URL(req.url ?? "/", "http://localhost").pathname;
      if (pathname === "/readyz") {
        const response = await fetch(`${deps.config.goInternalApiUrl}/readyz`, { signal: AbortSignal.timeout(2000) });
        sendJSON(res, response.ok ? 200 : 503, { status: response.ok ? "ready" : "unavailable" }); return;
      }
      if (pathname === "/manifest.json" && req.method === "GET") {
        // GO-053 — só existe um tenant conhecido SEM sessão no modo
        // self-hosted (options.tenant, fixo da instalação); fora desse
        // modo não há resolução de tenant anônima nesta topologia (ver
        // nota de escopo em bff-api.yaml).
        const manifest = await deps.goClient.getManifest(options.tenant);
        sendJSON(res, 200, manifest); return;
      }
      if (pathname === "/api/bff/operator-session" && req.method === "POST") {
        // JSON-only + Origin check prevents login CSRF. Browser tickets live in
        // fragments, never query strings, Referer headers or access logs.
        if (!req.headers["content-type"]?.startsWith("application/json") || !req.headers.origin || new URL(req.headers.origin).host !== req.headers.host) {
          sendJSON(res, 403, { error: "origin_required" }); return;
        }
        const body = await readJSONBody(req) as { ticket?: unknown };
        if (typeof body.ticket !== "string") throw new Error("ticket");
        const claims = jwt.verify(body.ticket, deps.config.serviceIdentitySecret, { algorithms: ["HS256"], audience: "saltcorn-cli-login", issuer: options.installationId }) as jwt.JwtPayload;
        const now = Math.floor(Date.now()/1000);
        for (const [id, exp] of used) if (exp <= now) used.delete(id);
        if (claims.tenant !== options.tenant || !claims.jti || !claims.sub || !/^\d+$/.test(claims.sub) || !claims.exp || !claims.iat || claims.exp - claims.iat > 60 || claims.iat > now + 5 || used.has(claims.jti)) throw new Error("ticket");
        used.set(claims.jti, claims.exp);
        const identity = mintServiceIdentity(deps.config.serviceIdentitySecret, { sub: claims.sub, tenant: options.tenant }, 30);
        const actor = await deps.goClient.getActor(identity, options.tenant);
        if (actor.role_id !== 1) throw new Error("admin required");
        const session = await deps.sessionStore.create({ userId: claims.sub, tenant: options.tenant });
        res.setHeader("Set-Cookie", [sessionCookieHeader(session), csrfCookieHeader(generateCsrfToken())]);
        res.setHeader("Cache-Control", "no-store");
        sendJSON(res, 200, { status: "ok" }); return;
      }
      if (pathname === "/login" && req.method === "GET") {
        res.writeHead(200, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff" });
        res.end(loginPage); return;
      }
      if (pathname.startsWith("/api/") || pathname === "/healthz" || pathname === "/readyz") { await fallback(req,res); return; }
      if (req.method !== "GET" && req.method !== "HEAD") { res.writeHead(405); res.end(); return; }
      const root = await realpath(options.root);
      let target = path.resolve(root, `.${decodeURIComponent(pathname)}`);
      if (target !== root && !target.startsWith(root + path.sep)) {res.writeHead(404);res.end();return;}
      if (pathname === "/") target = path.join(root,"index.html");
      target = await realpath(target);
      if (!target.startsWith(root + path.sep) || !(await stat(target)).isFile()) {res.writeHead(404);res.end();return;}
      const types: Record<string,string> = { ".html":"text/html; charset=utf-8", ".js":"text/javascript", ".css":"text/css", ".json":"application/json", ".svg":"image/svg+xml", ".woff2":"font/woff2", ".woff":"font/woff", ".ttf":"font/ttf", ".png":"image/png" };
      res.writeHead(200, { "Content-Type": types[path.extname(target)] ?? "application/octet-stream", "X-Content-Type-Options":"nosniff" });
      if (req.method === "HEAD") res.end(); else createReadStream(target).on("error",()=>res.destroy()).pipe(res);
    } catch {
      if (!res.headersSent) sendJSON(res, req.url === "/readyz" ? 503 : req.url === "/api/bff/operator-session" ? 401 : 404, { error: "unavailable" });
      else res.destroy();
    }
  };
}
