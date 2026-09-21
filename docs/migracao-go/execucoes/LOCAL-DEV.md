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
