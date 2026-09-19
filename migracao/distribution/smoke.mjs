// Runs only against a disposable administrative PostgreSQL DSN. Creates and
// drops its own databases; all installation files are under a new temp dir.
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, writeFile, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { createServer } from 'node:net';
const exec = promisify(execFile);
const release = path.resolve(process.argv[2]);
const baseDSN = process.env.SALTCORN_GO_TEST_INSTALLATION_DATABASE_URL;
if (!baseDSN) throw Error('SALTCORN_GO_TEST_INSTALLATION_DATABASE_URL obrigatório');
const root = await mkdtemp(path.join(tmpdir(),'go032-smoke-'));
const dbName = 'go032_smoke_' + Date.now();
const restoreName = dbName + '_restore';
const dbURL = new URL(baseDSN); dbURL.pathname = '/' + dbName;
const restoreURL = new URL(baseDSN); restoreURL.pathname = '/' + restoreName;
const env = {...process.env, SALTCORN_GO_DATABASE_URL:dbURL.href};
const cli = path.join(release,'bin/cli');
const instance = path.join(root,'instance');
let child;
let exited;
let logs = '';
async function port() {const s=createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p;}
const webPort=await port(),backendPort=await port();
const origin='http://127.0.0.1:'+webPort;
async function psql(sql) {const u=new URL(baseDSN);return exec('psql',['-X','-v','ON_ERROR_STOP=1','-c',sql],{env:{...process.env,PGHOST:u.hostname,PGPORT:u.port||'5432',PGUSER:decodeURIComponent(u.username),PGPASSWORD:decodeURIComponent(u.password),PGDATABASE:u.pathname.slice(1),PGSSLMODE:u.searchParams.get('sslmode')||'prefer'}});}
async function run(command,args=[],options={}) {return exec(cli,[command,'--dir',instance,...args],{env,...options});}
async function stop() {if(child){child.kill('SIGTERM');await exited;child=undefined;}}
async function start(dir=instance) {
 child=spawn(cli,['serve','--dir',dir,'--release',release],{env,stdio:['ignore','pipe','pipe']});
 exited=new Promise((resolve,reject)=>{child.once('error',reject);child.once('exit',(code,signal)=>resolve({code,signal}));});
 child.stdout.on('data',b=>logs+=b);child.stderr.on('data',b=>logs+=b);
 for(let i=0;i<100;i++){try{if((await fetch(origin+'/readyz')).ok)return;}catch{}await new Promise(r=>setTimeout(r,100));}
 throw Error('serve indisponível: '+logs);
}
try {
 await psql(`CREATE DATABASE ${dbName}`);await psql(`CREATE DATABASE ${restoreName}`);
 const password=path.join(root,'password');await writeFile(password,'strong-test-password-12345\n',{mode:0o600});
 await run('setup',['--email','admin@example.com','--password-file',password,'--http',`127.0.0.1:${webPort}`,'--backend-http',`127.0.0.1:${backendPort}`]);
 await assert.rejects(run('setup',['--email','admin@example.com','--password-file',password]));
 // Simulate interruption after DB commit but before publishing instance.json.
 const configPath=path.join(instance,'instance.json');const initial=JSON.parse(await readFile(configPath,'utf8'));
 await writeFile(path.join(instance,'setup.pending.json'),JSON.stringify({...initial,admin_id:0}),{mode:0o600});await rm(configPath);
 await run('setup',['--email','admin@example.com','--password-file',password]);
 const resumed=JSON.parse(await readFile(configPath,'utf8'));assert.equal(resumed.id,initial.id);assert.equal(resumed.admin_id,initial.admin_id);
 await run('check');await run('migrate');await run('set-cfg',['--key','site_name','--value','"smoke-site"']);
 await start();
 // The BFF must not inherit the supervisor's PostgreSQL/SMTP credentials.
 const threads=await readdir(`/proc/${child.pid}/task`);
 const descendants=new Set((await Promise.all(threads.map(t=>readFile(`/proc/${child.pid}/task/${t}/children`,'utf8').catch(()=>'')))).join(' ').trim().split(/\s+/).filter(Boolean));
 let checkedBff=false;
 for(const pid of descendants){const command=await readFile(`/proc/${pid}/cmdline`,'utf8');if(command.includes('bff/dist/src/server.js')){const childEnv=await readFile(`/proc/${pid}/environ`,'utf8');assert.ok(!childEnv.includes('SALTCORN_GO_DATABASE_URL='));assert.ok(!childEnv.includes('SALTCORN_GO_SMTP_PASSWORD='));assert.ok(!childEnv.includes('PGPASSWORD='));checkedBff=true;}}
 assert.ok(checkedBff,'BFF process identified');
 assert.equal((await fetch(origin+'/')).status,200);
 assert.match(await (await fetch(origin+'/vendor/sbadmin2/sb-admin-2.min.css')).text(),/Bootstrap|bootstrap/);
 assert.equal((await fetch(origin+'/vendor/builder_bundle.js')).status,200);
 assert.equal((await fetch(origin+'/instance.json')).status,404);
 await assert.rejects(run('backup',['--output',path.join(root,'busy-backup')]));
 const login=(await run('login')).stdout.trim();const ticket=new URL(login).hash.slice(1);
 const headers={'Content-Type':'application/json',Origin:origin};
 assert.equal((await fetch(origin+'/api/bff/operator-session',{method:'POST',headers:{...headers,Origin:'https://wrong.example'},body:JSON.stringify({ticket})})).status,403);
 const response=await fetch(origin+'/api/bff/operator-session',{method:'POST',headers,body:JSON.stringify({ticket})});assert.equal(response.status,200);
 const cookies=response.headers.getSetCookie().map(c=>c.split(';')[0]).join('; ');
 assert.match(cookies,/sc_session=/);const csrf=/sc_csrf=([^;]+)/.exec(cookies)[1];
 assert.equal((await fetch(origin+'/api/bff/operator-session',{method:'POST',headers,body:JSON.stringify({ticket})})).status,401);
 const bootstrap=await fetch(origin+'/api/bff/bootstrap',{headers:{Cookie:cookies}});assert.equal(bootstrap.status,200);assert.equal((await bootstrap.json()).actor.role_id,1);
 const created=await fetch(origin+'/api/bff/tables',{method:'POST',headers:{...headers,Cookie:cookies,'X-CSRF-Token':csrf},body:JSON.stringify({name:'smoke_records'})});assert.equal(created.status,201);
 await stop();
 await writeFile(path.join(instance,'files','sample.txt'),'preserved file');
 await run('backup',['--output',path.join(root,'backup')]);
 const restored=path.join(root,'restored');
 await exec(cli,['restore','--dir',restored,'--backup',path.join(root,'backup')],{env:{...env,SALTCORN_GO_DATABASE_URL:restoreURL.href}});
 assert.equal(await readFile(path.join(restored,'files','sample.txt'),'utf8'),'preserved file');
 const config=await exec(cli,['get-cfg','--dir',restored,'--key','site_name'],{env});assert.equal(config.stdout.trim(),'"smoke-site"');
 await start(restored);
 assert.equal((await fetch(origin+'/api/bff/bootstrap',{headers:{Cookie:cookies}})).status,401,'restart requires reauthentication');
 await stop();
 await assert.rejects(fetch(origin+'/healthz'));
 const manifestPath=path.join(release,'release.json');const originalManifest=await readFile(manifestPath,'utf8');
 try {await writeFile(manifestPath,JSON.stringify({...JSON.parse(originalManifest),schema:999}));await assert.rejects(run('serve',['--release',release]));}
 finally {await writeFile(manifestPath,originalManifest);}
 // SQLite administration uses the same packaged binary and rejects web boot.
 const desktop=path.join(root,'desktop');await exec(cli,['setup','--driver','sqlite','--dir',desktop],{env:{...env,SALTCORN_GO_DATABASE_URL:''}});
 await exec(cli,['check','--dir',desktop],{env});await assert.rejects(exec(cli,['serve','--dir',desktop,'--release',release],{env}));
 console.log('PASS release: setup, upgrade, assets, sessão/CSRF/replay, lock, backup/restore, restart e perfil SQLite');
} finally {
 await stop();
 await psql(`DROP DATABASE IF EXISTS ${dbName} WITH (FORCE)`);await psql(`DROP DATABASE IF EXISTS ${restoreName} WITH (FORCE)`);
 await rm(root,{recursive:true,force:true});
}
