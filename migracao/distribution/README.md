# Distribuição self-hosted — GO-032

Release Linux com `bin/cli`, `bin/server`, `bin/worker`, BFF Node.js e frontend React + SB Admin 2/builder. Código fonte e toolchain não são necessários no host após o build. O diretório da release é imutável; dados e segredos ficam em outro diretório privado.

## Dependências e perfis

- Build: Go 1.22, Node 22/npm, bash, tar. `migracao/distribution/build.sh /tmp/saltcorn-release` instala pelos lockfiles em árvore temporária, compila os três binários e copia o bundle frontend e dependências de produção do BFF. Pode-se transportar a pasta com `tar`; extrair apenas releases confiáveis.
- Runtime web: Linux, Node 22, PostgreSQL 16 dedicado à instância; `pg_dump`/`pg_restore` 16 para backup/restore. Proxy HTTPS externo para acesso remoto, com suporte a `/socket.io` WebSocket e encaminhamento de Host/Origin. Backend e BFF escutam somente em loopback. Sessões BFF em memória: um processo BFF, reautenticação após restart.
- Runtime SQLite: a CLI usa driver Go puro, sem libsqlite ou servidor externo. Suporta administração/recuperação de metadata, records, outbox e sync (GO-030/031); `serve` web recusa esse perfil, pois identidade, views e demais domínios HTTP ainda dependem de PostgreSQL. Não apresenta uma UI parcialmente funcional como suportada.
- Mobile: o runtime permanece JS/Capacitor e sua fila SQLite local não pertence ao backup do servidor. O cliente sync v1 consome o BFF desta distribuição; não há binário Go no dispositivo nem nova assinatura/build nativo Android/iOS nesta tarefa.
- SMTP é opcional e usa as variáveis `SALTCORN_GO_SMTP_*` existentes. Plugins JS de domínio não são ativados automaticamente pelo BFF. O host temporário/legado continua requisito externo para capacidades ainda não nativas; este pacote só habilita as capacidades HTTP Go já portadas.

## Instalação e operação

Crie previamente um **banco PostgreSQL vazio e dedicado**, com usuário proprietário autorizado a criar schemas. Informe o DSN por ambiente, nunca por argumento de processo. Prepare um arquivo privado com senha inicial de pelo menos 12 caracteres.

```bash
export SALTCORN_GO_DATABASE_URL='postgres://usuario:senha@localhost/saltcorn?sslmode=disable'
/tmp/saltcorn-release/bin/cli setup --dir /srv/saltcorn-instance --tenant app --email admin@example.com --password-file /caminho/privado/senha
/tmp/saltcorn-release/bin/cli check --dir /srv/saltcorn-instance
/tmp/saltcorn-release/bin/cli serve --dir /srv/saltcorn-instance
```

`setup` cria configuração privada com segredo aleatório, usuário admin, catálogo e registro de migrations. Não reutiliza banco legado, não apaga tabelas e não troca ownership durante upgrades. Se interrompido, `setup.pending.json` permite retomar com os mesmos argumentos. `serve` mantém os três processos e o lock da instância, encerra o conjunto se um processo/dependência falhar e drena em SIGTERM/SIGINT. Não execute server/worker diretamente em paralelo com a administração.

Em outro terminal, `bin/cli login --dir /srv/saltcorn-instance` gera um link de acesso administrativo válido por 60 segundos. Abra no mesmo host; para proxy HTTPS substitua apenas a origem pelo domínio configurado. O ticket fica no fragmento da URL, é consumido uma vez pelo BFF e nunca entra em logs de acesso. Esse comando exige acesso ao arquivo privado da instância e equivale à autoridade administrativa local. Não substitui o login de usuários finais/estratégias de plugins. Cookies mantêm Secure/HttpOnly/SameSite; use HTTPS ou localhost seguro no navegador. O BFF revalida o papel atual do administrador no Go antes de criar sessão.

Health: Go `http://127.0.0.1:8090/healthz` e `/readyz`; BFF `http://127.0.0.1:3100/healthz` e `/readyz` (consulta o Go). `serve` também verifica a conexão PostgreSQL e encerra se perder a sessão que mantém o lock do banco. `release.json` declara schema/contratos/Node e hashes dos componentes; releases incompatíveis são recusadas antes do boot.

Com `serve` parado:

```bash
bin/cli set-cfg --dir /srv/saltcorn-instance --key site_name --value '"Minha aplicação"'
bin/cli get-cfg --dir /srv/saltcorn-instance --key site_name
bin/cli migrate --dir /srv/saltcorn-instance
```

Configuração de processo fica em `instance.json` (0600); endereços são definidos por `setup --http 127.0.0.1:3100 --backend-http 127.0.0.1:8090`. Não publique esse arquivo. `get-cfg`/`set-cfg` administram o catálogo de configuração da aplicação como JSON, reutilizado por packs. Diretório `files/` contém arquivos locais; `plugins/<pasta>/package.json` e seus arquivos preservam nome/versão exatos. `cli plugins --dir ...` valida e registra o inventário sem executar instalação ou hooks. Dependências vendorizadas devem ser arquivos regulares; symlinks são recusados. Preservar um plugin não significa torná-lo compatível/nativo.

## Backup, restore e upgrade

Pare `serve` e quaisquer escritores externos, incluindo processos legados/host JS. O lock local e o advisory lock PostgreSQL impedem concorrência entre comandos/processos gerenciados; não podem bloquear ferramentas externas que desrespeitem esse protocolo.

```bash
bin/cli backup --dir /srv/saltcorn-instance --output /backups/saltcorn-001
# Novo diretório e NOVO banco vazio; o restore nunca usa implicitamente a origem.
export SALTCORN_GO_DATABASE_URL='postgres://usuario:senha@localhost/saltcorn_restored?sslmode=disable'
bin/cli restore --dir /srv/saltcorn-restored --backup /backups/saltcorn-001
bin/cli check --dir /srv/saltcorn-restored
```

Backup é um diretório privado com dump completo PostgreSQL ou arquivos SQLite fechados, arquivos locais, configuração, fontes/dependências e versões dos plugins, manifesto e SHA-256 por arquivo. Contém segredos: proteger armazenamento/transporte. Hashes detectam corrupção, não autenticam um produtor malicioso; restaure apenas backups confiáveis. O destino existente nunca é sobrescrito. Integridade, formato, schema e plugins são conferidos em staging privado antes de tocar no banco. `pg_restore` usa transação única; restauração interrompida conserva o backup original. Se houver falha após o commit PostgreSQL/antes de terminar os arquivos, preserve o destino para diagnóstico e repita em outro destino vazio; não reinicie sobre estado parcial.

Para upgrade: backup offline, instalar nova release em outra pasta, executar seu `cli migrate --dir ...`, `check` e `serve`. Migrations são aditivas, transacionais e registradas; versão futura bloqueia downgrade. Rollback após alteração de schema é restore do backup em novo destino com release compatível, nunca exclusão de linhas do journal. O teste de upgrade usa fixture v1 com registros/configuração pendentes e confirma a preservação na v2.

SQLite: `bin/cli setup --driver sqlite --dir /srv/saltcorn-desktop --tenant desktop`, depois os mesmos comandos administrativos (sem DSN). Só iniciar escritores SQLite após terminar a operação; os arquivos ficam em `sqlite/<tenant>.sqlite`. Não copiar uma base aberta manualmente. Filas outbox/sync persistidas são parte da cópia e não recebem IDs novos durante restauração.
