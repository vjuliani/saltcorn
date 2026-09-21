# Execução local — pedido complementar à GO-036

Pedido: criar arquivos de execução local para frontend, BFF e backend, com documentação how-to. O PR preparatório #40 foi integrado durante a execução; esta entrega usa `feat/migracao-local`, baseada em `503d85a193b`. O bloqueio operacional da GO-036 permanece.

## Implementação

- `migracao/local/local.sh` e `local.mjs`: setup persistente, build, stack conjunto, frontend separado, Go/BFF/worker sob o supervisor existente, login, status e operação do PostgreSQL.
- Compose com PostgreSQL 16 dedicado em loopback, porta 55436 e volume persistente. Aplicação em 8092/3102, Vite em 5173; configuração personalizada antes do setup.
- Configuração privada em `.state`, ignorada pelo Git; credenciais aleatórias, nenhum seed destrutivo. Backend/BFF usam o protocolo de locks da CLI da distribuição.
- [How-to](../EXECUCAO-LOCAL.md): configuração, inicialização, login, recompilação, parada/retomada, persistência, logs e problemas conhecidos.
- Smoke test real em Chromium e workflow `migracao-local-ci.yml`, incluindo repetição do setup e reinício do banco.

## Validação local

Ambiente: Linux, Go 1.22.2, Node 22.23.2, PostgreSQL 16 via contexto Docker `default`, Chromium da dependência Playwright de `migracao/e2e`.

| Procedimento | Resultado |
| --- | --- |
| `local.sh setup` | exit 0; instância criada, build e dependências preparados |
| `local.sh up` + Chromium | Login administrativo, Vite, criação/publicação de view, escrita/leitura via BFF/Go aprovados |
| Segunda chamada de `up` com aplicação ativa | exit 1 por porta ocupada; serviço anterior permanece HTTP 200 |
| Ctrl+C, repetir `setup`, `db-stop`, `db-up`, novo `up` | Dados preservados; nova sessão lê o registro anterior no navegador |
| `local.sh build` | exit 0; nova release, dados/instância preservados |
| `backend-bff` e `frontend` em terminais separados | Smoke `recheck` aprovado após recompilação |
| `local.sh status` | PostgreSQL healthy; endpoints de aplicação conferidos |
| `node --check`, `bash -n`, actionlint 1.7.7 | Aprovados |
| Controle do backlog | 38 IDs, status/checkbox e dependências consistentes; GO-036 BLOCKED |

Na preparação, um erro de sintaxe do executor foi corrigido antes de iniciar os serviços. A primeira tentativa de reiniciar para a leitura de persistência foi impedida pela revisão automática por limite de uso; após a janela informada e a solicitação de continuar, a revisão permitiu a retomada e o teste passou. Isso não foi tratado como aprovação implícita nem contornado.

Artefatos locais de diagnóstico: `/tmp/saltcorn-local-setup.log`, `saltcorn-local-setup-repeat.log`, `saltcorn-local-rebuild.log`, `saltcorn-local-restart.log` e logs do modo separado. Arquivos privados e releases permanecem em `migracao/local/.state`; não fazem parte do commit. O smoke deixa uma tabela de demonstração `local_howto_*`, identificada em `.state/smoke-table.txt`.

Ao finalizar os checks locais, parar os processos da aplicação e manter PostgreSQL/dados disponíveis. O usuário pode iniciar com `migracao/local/local.sh up` e emitir novo `login`. CI e URL do PR ficam registrados na descrição da entrega; nenhuma liberação de canário é inferida desses testes locais.

## Complemento — aplicação inteira em Docker Compose

Pedido adicional: executar também frontend, BFF e backend via Docker Compose. O PR #41 foi integrado em `f07e6ed28c2`; o complemento usa a branch `feat/migracao-docker-local`, em um único novo PR, sem alterar o estado da GO-036.

- `docker.sh`: build/inicialização com espera por saúde, login, status, logs, parada e remoção de containers sem remover volumes.
- `compose.app.yaml`: projeto isolado com PostgreSQL, CLI/supervisor Go+BFF+worker e frontend Nginx; somente a porta HTTP do frontend é publicada em loopback (5180 por padrão).
- Dockerfile em múltiplos estágios reutiliza o build da distribuição; frontend compartilha a rede de `app` para preservar os binds internos em loopback. Não é necessário Node ou Go no host.
- Credenciais aleatórias em `.docker-state/compose.env`, ignorado pelo Git; configuração/arquivos e banco em volumes persistentes separados. O contexto do build exclui estado local e dependências instaladas no host.
- How-to atualizado com preparação, acesso, contexto/porta, recompilação, persistência e solução de problemas. Smoke existente ganhou `--docker`; a CI ganhou `docker-howto`.

### Validação do complemento

Ambiente: Linux, Docker Engine no contexto `default`, imagens Go 1.22/Node 22/PostgreSQL 16/Nginx, Chromium do Playwright já instalado no host.

| Procedimento | Resultado |
| --- | --- |
| `docker.sh up` | Build e três serviços healthy; única publicação `127.0.0.1:5180` |
| Repetir `docker.sh up` | Exit 0; setup preserva a instância existente |
| `node migracao/local/smoke.mjs create --docker` | Login, React/Nginx/BFF/Go, criação/publicação de view, escrita e leitura aprovados |
| `docker.sh down`, `docker.sh up`, smoke `recheck --docker` | Exit 0; containers recriados e registro anterior lido com nova sessão |
| `docker.sh status` | PostgreSQL, app e frontend healthy |
| `bash -n`, `node --check`, `git diff --check`, actionlint 1.7.7 | Aprovados |

Falhas corrigidas durante a implementação: a imagem slim de build não continha certificados TLS; o Dockerfile passou a copiar o conjunto de certificados da imagem Go. Uma edição do wrapper enquanto o Bash ainda o executava causou erro de leitura ao terminar a primeira inicialização; a validação foi repetida com o arquivo estável e passou. Nenhum desses ensaios apagou volumes ou reinicializou o banco existente.

Logs locais em `/tmp/migracao-docker-{up,repeat,recreate}.log`, sem tickets de login. URL do PR e resultados da CI registrados na descrição da entrega. Ao finalizar a validação, os containers deste complemento são parados e os dados permanecem nos volumes para `docker.sh up`.

## Complemento documental — passo a passo Docker Compose

Pedido: detalhar a execução com Docker Compose. Base: `a8b4656a2a8`, integração do PR #42; branch `docs/migracao-docker-passo-a-passo`.

- Criado `docs/migracao-go/DOCKER-COMPOSE.md` com 13 etapas, comandos copiáveis, resultados esperados, distinção dos arquivos/configurações, contexto Docker, portas, autenticação, operação direta do Compose, persistência, diagnóstico e teste opcional.
- `EXECUCAO-LOCAL.md` preserva a âncora de Docker Compose e encaminha ao guia detalhado; índice da migração atualizado.
- Conferidos links/âncoras dos três documentos e sintaxe dos 22 blocos Bash do guia. `docker compose ... config --quiet` e `config --services` passaram no contexto `default`, sem exibir credenciais nem iniciar serviços; serviços retornados: postgres, app e frontend. `git diff --check` aprovado.
- Alteração apenas documental. Os testes funcionais do PR #42 não foram reexecutados, pois scripts, imagens e configuração Compose permanecem iguais. URL e checks da entrega ficam na descrição do PR. GO-036 permanece BLOCKED.
