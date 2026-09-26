import { test } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtemp, writeFile, symlink, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import jwt from "jsonwebtoken";
import { selfHostedListener } from "../src/selfhost.js";
import { loadConfig } from "../src/config.js";
import { InMemorySessionStore } from "../src/session.js";
import { GoClient } from "../src/goClient.js";
import { buildRouter, createRequestListener } from "../src/app.js";

const secret="selfhost-test-secret-32-bytes-long";
test("self-hosted: ticket exige origem, tenant, audience, prazo e admin atuais; uso único", async () => {
  const root=await mkdtemp(path.join(tmpdir(),"go032-bff-"));
  let role=1;
  const go=createServer((req,res)=>{res.setHeader("Content-Type","application/json");res.end(JSON.stringify(req.url==="/readyz" ? {status:"ready"} : {id:"1",role_id:role}));});
  await new Promise<void>(r=>go.listen(0,"127.0.0.1",r));
  const goPort=(go.address() as {port:number}).port;
  const config=loadConfig({SALTCORN_BFF_SERVICE_IDENTITY_SECRET:secret,SALTCORN_BFF_GO_INTERNAL_API_URL:`http://127.0.0.1:${goPort}`});
  const deps={config,sessionStore:new InMemorySessionStore(),goClient:new GoClient({baseUrl:config.goInternalApiUrl,timeoutMs:1000})};
  const handler=selfHostedListener(deps,createRequestListener(buildRouter(deps),deps),{root,tenant:"app",installationId:"instance"});
  const server=createServer((req,res)=>void handler(req,res));await new Promise<void>(r=>server.listen(0,"127.0.0.1",r));
  const origin=`http://127.0.0.1:${(server.address() as {port:number}).port}`;
  let id=0;
  const ticket=(extra:Record<string,unknown>={})=>jwt.sign({sub:"1",tenant:"app",jti:String(++id),...extra},secret,{algorithm:"HS256",audience:"saltcorn-cli-login",issuer:"instance",expiresIn:60});
  const exchange=(token:string,originHeader=origin)=>fetch(origin+"/api/bff/operator-session",{method:"POST",headers:{"Content-Type":"application/json",Origin:originHeader},body:JSON.stringify({ticket:token})});
  try {
    await writeFile(path.join(root,"index.html"),"<h1>frontend</h1>");await symlink("/etc/passwd",path.join(root,"escape"));
    assert.equal((await fetch(origin+"/")).status,200);assert.equal((await fetch(origin+"/escape")).status,404);
    assert.equal((await exchange(ticket(),"https://other.example")).status,403);
    assert.equal((await exchange(ticket({tenant:"other"}))).status,401);
    assert.equal((await exchange(jwt.sign({sub:"1",tenant:"app",jti:"service"},secret,{audience:"backend",expiresIn:60}))).status,401);
    assert.equal((await exchange(ticket({iat:Math.floor(Date.now()/1000)-120}))).status,401);
    role=80;assert.equal((await exchange(ticket())).status,401);role=1;
    const token=ticket();const accepted=await exchange(token);assert.equal(accepted.status,200);
    assert.match(accepted.headers.get("set-cookie")!,/HttpOnly; Secure; SameSite=Lax/);
    assert.equal((await exchange(token)).status,401);
    assert.equal((await fetch(origin+"/readyz")).status,200);
    await new Promise<void>(r=>go.close(()=>r()));
    assert.equal((await fetch(origin+"/readyz")).status,503);
  } finally {server.closeAllConnections();server.close();go.closeAllConnections();go.close();await rm(root,{recursive:true,force:true});}
});

// GO-053: /manifest.json é servido no modo self-hosted usando o tenant
// FIXO da instalação (options.tenant) — nunca uma sessão, nunca
// identidade delegada (o Go real também não exige nenhuma para esta
// rota, ver cmd/server/pwa.go).
test("self-hosted: GET /manifest.json relaia o manifesto do tenant fixo, sem exigir sessão", async () => {
  const root=await mkdtemp(path.join(tmpdir(),"go053-bff-"));
  const manifest={name:"Guitars Shop",start_url:"/",display:"browser"};
  const go=createServer((req,res)=>{
    res.setHeader("Content-Type","application/json");
    if (req.url==="/v1/tenants/app/manifest") { res.end(JSON.stringify(manifest)); return; }
    res.end(JSON.stringify({status:"ready"}));
  });
  await new Promise<void>(r=>go.listen(0,"127.0.0.1",r));
  const goPort=(go.address() as {port:number}).port;
  const config=loadConfig({SALTCORN_BFF_SERVICE_IDENTITY_SECRET:secret,SALTCORN_BFF_GO_INTERNAL_API_URL:`http://127.0.0.1:${goPort}`});
  const deps={config,sessionStore:new InMemorySessionStore(),goClient:new GoClient({baseUrl:config.goInternalApiUrl,timeoutMs:1000})};
  const handler=selfHostedListener(deps,createRequestListener(buildRouter(deps),deps),{root,tenant:"app",installationId:"instance"});
  const server=createServer((req,res)=>void handler(req,res));await new Promise<void>(r=>server.listen(0,"127.0.0.1",r));
  const origin=`http://127.0.0.1:${(server.address() as {port:number}).port}`;
  try {
    const res=await fetch(origin+"/manifest.json");
    assert.equal(res.status,200);
    assert.deepEqual(await res.json(),manifest);
  } finally {server.closeAllConnections();server.close();go.closeAllConnections();go.close();await rm(root,{recursive:true,force:true});}
});
