// Optional integration check: requires migracao/e2e dependencies and Chromium.
import {createRequire} from 'node:module';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {writeFile,readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
const local=path.dirname(fileURLToPath(import.meta.url));
const root=path.resolve(local,'../..');
const config=JSON.parse(await readFile(path.join(local,'.state/settings.json'),'utf8'));
const mode=process.argv[2] || 'create';
if(!['create','recheck'].includes(mode))throw Error('Uso: smoke.mjs create|recheck');
const tableFile=path.join(local,'.state/smoke-table.txt');
const {chromium,expect}=createRequire(root+'/migracao/e2e/package.json')('@playwright/test');
const login=(await promisify(execFile)(root+'/migracao/local/local.sh',['login'])).stdout.trim();
const browser=await chromium.launch({headless:true});
try {
 const page=await browser.newPage();
 await page.goto(login);await page.waitForURL(`http://localhost:${config.bffPort}/`);
 await page.goto(`http://localhost:${config.frontendPort}`);
 const response=await page.request.get(`http://localhost:${config.frontendPort}/@vite/client`);if(!response.ok())throw Error('Vite indisponível');
 let table;
 if(mode==='recheck') table=(await readFile(tableFile,'utf8')).trim();
 else {
  table='local_howto_'+Date.now();
  await page.getByRole('button',{name:/Abrir editor \(conectado ao BFF\)/}).click();
  await page.getByLabel('Nome da tabela').fill(table);
  await page.getByRole('button',{name:'Criar tabela',exact:true}).click();
  await page.getByRole('button',{name:`Criar view em "${table}"`}).click();
  await page.getByRole('button',{name:'Publicar',exact:true}).click();
  await expect(page.getByTestId('status-message')).toHaveText('Publicada.');
  const status=await page.evaluate(async(table)=>{
   const csrf=document.cookie.match(/(?:^|;\s*)sc_csrf=([^;]+)/)[1];
   const r=await fetch('/api/bff/tables/'+table+'/records',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':decodeURIComponent(csrf)},body:JSON.stringify({titulo:'Persistência local validada'})});return r.status;
  },table);
  if(status!==201)throw Error('Falha de escrita: '+status);
  await writeFile(tableFile,table,{mode:0o600});
 }
 await page.getByRole('button',{name:/Ver views/}).click();
 await page.getByRole('row',{name:new RegExp(table+'_view')}).getByRole('button',{name:'Visualizar'}).click();
 await expect(page.getByTestId('list-view')).toContainText('Persistência local validada');
 console.log(JSON.stringify({login:true,vite:true,reactBffGo:true,persisted:true,mode}));
}finally{await browser.close();}
