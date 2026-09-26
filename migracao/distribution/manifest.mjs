import { readdir, readFile, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
const root = process.argv[2];
const sha256 = {};
async function walk(dir) {
  for (const entry of await readdir(dir, {withFileTypes: true})) {
    const p = path.join(dir,entry.name);
    if (p === path.join(root,"release.json")) continue;
    if (entry.isDirectory()) await walk(p);
    else if (entry.isFile()) sha256[path.relative(root,p).split(path.sep).join('/')] = createHash('sha256').update(await readFile(p)).digest('hex');
    // npm creates only .bin symlinks; they are not used to start runtime code.
    else if (!entry.isSymbolicLink()) throw Error(`Arquivo especial: ${p}`);
  }
}
await walk(root);
// version/schema devem bater exatamente com Release/SchemaVersion de
// migracao/backend/internal/installation/config.go — CheckRelease rejeita
// qualquer divergência. Sem fonte única compartilhada entre Go e este
// script; atualizar os dois manualmente sempre que qualquer um mudar.
await writeFile(path.join(root,'release.json'), JSON.stringify({format:1,version:'0.1.0-go046',schema:3,internal_api:1,bff_api:1,node_major:22,sha256},null,2)+'\n');
