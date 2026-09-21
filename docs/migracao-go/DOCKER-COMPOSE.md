# Passo a passo: executar a migração com Docker Compose

Este guia inicia uma instância local persistente com frontend React, BFF Node.js, backend Go, worker e PostgreSQL. Ao final, você acessará a interface em **http://localhost:5180**, autenticado como administrador local.

O caminho principal usa `migracao/local/docker.sh`, que chama Docker Compose e prepara as credenciais. A seção [Compose diretamente](#10-usar-docker-compose-diretamente) mostra os comandos equivalentes após essa preparação.

## Antes de começar: qual arquivo usar?

| Arquivo | Finalidade neste repositório |
| --- | --- |
| [`migracao/local/compose.app.yaml`](../../migracao/local/compose.app.yaml) | **Aplicação inteira em containers. É o Compose deste guia.** |
| [`migracao/local/docker.sh`](../../migracao/local/docker.sh) | Prepara a configuração privada e opera `compose.app.yaml` |
| `migracao/local/.docker-state/compose.env` | Gerado no primeiro `docker.sh up`; contém senha do PostgreSQL e porta externa |
| [`migracao/local/compose.yaml`](../../migracao/local/compose.yaml) | Apenas PostgreSQL para o modo `local.sh`, em que Go/Node/Vite rodam no host |
| `migracao/local/config.json` | Configura somente `local.sh`; **não altera o modo Docker deste guia** |
| [`migracao/local/config.example.json`](../../migracao/local/config.example.json) | Modelo do `config.json` do modo `local.sh` |

Não é necessário copiar `config.example.json`, executar `local.sh setup` ou iniciar os dois arquivos Compose. O modo Docker tem seu próprio banco e seus próprios volumes. Os dados do modo `local.sh` não são importados automaticamente.

## 1. Preparar o terminal e verificar o Docker

Tenha Docker Engine com plugin Compose v2, ou Docker Desktop, instalado e em execução. Use um terminal Bash; no Windows, execute os comandos dentro do WSL2 com acesso ao Docker. O fluxo foi validado em Linux. A primeira compilação precisa de rede para baixar imagens e dependências.

Node.js, npm e Go **não precisam estar instalados no host** para iniciar a aplicação. Eles são utilizados dentro das imagens. O teste de navegador opcional, ao final, tem pré-requisitos adicionais.

Abra um terminal na raiz do seu checkout `saltcorn`. Confirme:

```bash
pwd
test -f migracao/local/compose.app.yaml && echo 'Raiz do repositório confirmada'
bash --version
docker --version
docker context ls
```

**Resultado esperado:** a mensagem `Raiz do repositório confirmada`, as versões do Bash/Docker e a lista de contextos Docker. Se o arquivo não existir, entre na pasta raiz do checkout antes de continuar.

Selecione o contexto que acessa o Docker local. O script usa `default` quando nenhuma variável é informada, mesmo que outro contexto esteja marcado como atual na CLI:

```bash
export SALTCORN_DOCKER_CONTEXT=default
docker --context "$SALTCORN_DOCKER_CONTEXT" info
docker --context "$SALTCORN_DOCKER_CONTEXT" compose version
```

**Resultado esperado:** informações do daemon e a versão do Compose, sem erro de conexão ou permissão. Se você usa Docker Desktop e `docker context ls` mostrar `desktop-linux` como o contexto funcional, substitua a primeira linha por:

```bash
export SALTCORN_DOCKER_CONTEXT=desktop-linux
```

Repita as verificações após mudar o contexto. O `export` vale para o terminal atual; em outro terminal, exporte novamente o mesmo valor. Use o mesmo contexto durante todo o procedimento, pois volumes e containers pertencem ao daemon selecionado.

## 2. Escolher a porta HTTP

O padrão é **5180**. Se essa porta estiver livre, siga para o passo 3 sem configurar nada.

Para escolher outra porta **antes da primeira inicialização**, por exemplo 5181:

```bash
export SALTCORN_DOCKER_PORT=5181
```

A porta deve estar entre 1024 e 65535. Essa variável só é usada ao criar `migracao/local/.docker-state/compose.env` pela primeira vez. Se o arquivo já existir, vale `HTTP_PORT` gravado nele. Para mudar uma instância existente, siga o [passo 9](#9-alterar-a-porta-de-uma-instância-existente).

Os exemplos de URL abaixo usam 5180; substitua pela porta escolhida. Não é necessário alterar as portas internas do YAML.

## 3. Construir as imagens e iniciar os serviços

Na raiz do repositório:

```bash
migracao/local/docker.sh up
```

Na primeira execução, o script:

1. Cria `.docker-state/compose.env` com senha aleatória do PostgreSQL e porta HTTP, com acesso restrito ao usuário local.
2. Compila Go, BFF e frontend usando o build da distribuição.
3. Cria a rede e os volumes do projeto `saltcorn-migracao-docker`.
4. Inicia PostgreSQL e aguarda sua verificação de saúde.
5. Prepara a instância, o tenant `local` e o administrador `admin@local.test`, quando ainda não existem.
6. Inicia Go, BFF e worker sob a CLI/supervisor, e depois o frontend Nginx.
7. Aguarda as verificações de saúde e retorna ao terminal.

O build inicial pode demorar alguns minutos. A espera de até 180 segundos configurada pelo script é para a inicialização dos serviços, além do tempo de compilação.

**Resultado esperado ao final:**

```text
Aplicação pronta. Execute migracao/local/docker.sh login e abra o link gerado.
```

Os serviços ficam em segundo plano. Fechar esse terminal não os encerra. Repetir `up` reutiliza o banco e a configuração existentes; não faz reset da instância nem aplica automaticamente upgrades futuros de schema.

Se o comando terminar com erro, consulte o [diagnóstico](#12-diagnosticar-problemas) antes de prosseguir. Um erro não implica que todos os containers tenham sido parados.

## 4. Confirmar que a aplicação está pronta

```bash
migracao/local/docker.sh status
```

**Resultado esperado:** três serviços em execução com status `healthy`:

| Serviço | O que executa | Acesso pelo host |
| --- | --- | --- |
| `postgres` | PostgreSQL 16 | Sem porta publicada |
| `app` | Backend Go, BFF Node.js e worker, gerenciados pela CLI | Publica `127.0.0.1:5180 → 8080`, usada pelo frontend |
| `frontend` | React compilado servido por Nginx | Compartilha a rede de `app` e atende na porta interna 8080 |

É normal a publicação da porta aparecer na linha **`app`**, embora a resposta HTTP seja servida pelo Nginx de **`frontend`**. Isso permite que Go e BFF continuem vinculados ao loopback interno, conforme exigido pelo supervisor.

Se tiver `curl` instalado, confira também:

```bash
curl --fail --show-error http://localhost:5180/readyz
```

**Resultado esperado:** HTTP 200 e JSON com `"status":"ready"`. Essa chamada atravessa Nginx/BFF e confere a prontidão do backend. Ela não autentica o navegador. Use a porta personalizada se não estiver usando 5180.

## 5. Gerar o login e abrir a interface

```bash
migracao/local/docker.sh login
```

**Resultado esperado:** uma URL no formato abaixo, com um ticket real no lugar do texto ilustrativo:

```text
http://localhost:5180/login#<ticket-temporario>
```

1. Copie **a URL completa emitida no terminal**, incluindo a parte depois de `#`.
2. Abra-a no navegador em até **60 segundos**.
3. Aguarde a validação e o redirecionamento para `http://localhost:5180/`.
4. Continue usando essa mesma origem no navegador.

O ticket é de uso único. Se expirar, já tiver sido consumido ou o BFF for reiniciado, execute `login` novamente. Mantenha o host `localhost`; alternar para `127.0.0.1` muda o host do cookie. O ticket concede acesso administrativo: não o publique em issues nem em logs compartilhados.

Esse fluxo usa o administrador criado pelo bootstrap, sem solicitar senha na interface. O login final de usuários por senha/plugins ainda não tem paridade completa. A senha inicial guardada no volume da instância não precisa ser copiada para executar este guia.

## 6. Conferir o fluxo pela interface

Para uma verificação manual simples:

1. Clique em **Abrir editor (conectado ao BFF)**.
2. No campo **Nome da tabela**, informe um nome novo, por exemplo `demo_docker_01`.
3. Clique em **Criar tabela**.
4. Clique em **Criar view em "demo_docker_01"**.
5. Clique em **Publicar** e aguarde a mensagem **Publicada.**
6. Abra **Ver views** e, na view criada, clique em **Visualizar**.

**Resultado esperado:** a view List abre; ela pode estar vazia porque esse procedimento criou a estrutura, sem inserir registros. O [teste opcional](#13-validar-escrita-e-persistência-com-o-teste-de-navegador-opcional) também grava um registro e confere sua leitura.

Este modo utiliza o frontend compilado. Alterações nos arquivos locais não aparecem automaticamente no navegador; veja o passo 8 para recompilar. Para desenvolver com Vite e recarga automática, use o [outro modo de execução local](EXECUCAO-LOCAL.md).

## 7. Consultar logs

Para acompanhar todos os serviços:

```bash
migracao/local/docker.sh logs
```

Para acompanhar somente um serviço, execute uma das opções:

```bash
migracao/local/docker.sh logs frontend
migracao/local/docker.sh logs app
migracao/local/docker.sh logs postgres
```

Cada comando mostra as últimas 100 linhas e continua acompanhando as novas. Use **Ctrl+C** para sair dos logs; isso não para os serviços. Backend, BFF e worker compartilham os logs de `app`.

Para consultar apenas as últimas linhas sem acompanhar continuamente, use `dc logs --tail 100 app`, após definir `dc` no passo 10.

## 8. Aplicar alterações de código

Depois de editar o código, execute:

```bash
migracao/local/docker.sh up
```

O comando recompila com cache, recria os containers cujas imagens/configurações mudaram e aguarda a prontidão. Os volumes são preservados. Depois, atualize a página no navegador e gere novo login se o BFF tiver sido reiniciado.

Para **apenas construir as imagens**, sem aplicar a nova versão aos containers em execução:

```bash
migracao/local/docker.sh build
```

`build` exige que o primeiro `up` já tenha preparado `compose.env`. Ele não atualiza a aplicação em execução; execute `up` quando quiser aplicar o resultado.

Mudanças futuras de schema exigem o procedimento de migração/backup da [distribuição](../../migracao/distribution/README.md). Recompilar as imagens não substitui esse procedimento.

## 9. Alterar a porta de uma instância existente

1. Remova os containers e a rede, preservando os volumes:

   ```bash
   migracao/local/docker.sh down
   ```

2. Abra `migracao/local/.docker-state/compose.env` no editor. Altere **somente** a linha da porta, por exemplo:

   ```dotenv
   HTTP_PORT=5181
   ```

   Preserve a linha `POSTGRES_PASSWORD` com seu valor original. Não substitua o arquivo inteiro pelo exemplo acima e não publique seu conteúdo.

3. Recrie os containers e gere o novo link:

   ```bash
   migracao/local/docker.sh up
   migracao/local/docker.sh login
   ```

**Resultado esperado:** o link usa `http://localhost:5181`, e os dados anteriores continuam disponíveis. Alterar `SALTCORN_DOCKER_PORT` ou `config.json` não muda a porta de um `compose.env` já existente.

## 10. Usar Docker Compose diretamente

Esta seção é opcional. Primeiro execute o passo 3 pelo menos uma vez: ele cria as credenciais que o YAML exige. Rodar apenas `docker compose up` na raiz não seleciona o arquivo correto nem fornece essa configuração privada.

Na raiz do checkout, com o mesmo contexto dos passos anteriores, defina a função abaixo no Bash:

```bash
# Evita que variáveis exportadas substituam o arquivo privado ou o projeto.
unset POSTGRES_PASSWORD HTTP_PORT COMPOSE_FILE COMPOSE_PROJECT_NAME COMPOSE_PROFILES

dc() {
  docker --context "${SALTCORN_DOCKER_CONTEXT:-default}" compose \
    --env-file migracao/local/.docker-state/compose.env \
    -f migracao/local/compose.app.yaml \
    "$@"
}
```

A função vale no terminal atual, e seus caminhos são relativos à raiz do checkout. Exemplos:

```bash
# Validar o YAML sem imprimir valores de credenciais:
dc config --quiet

# Listar os serviços definidos:
dc config --services

# Iniciar/recompilar e aguardar prontidão:
dc up --build -d --wait --wait-timeout 180

# Consultar estado e logs:
dc ps
dc logs --tail 100 app
```

**Resultado esperado em `config --services`:** `postgres`, `app` e `frontend`. `config --quiet` termina sem saída quando válido. Evite compartilhar a saída de `config` sem `--quiet`, pois a configuração resolvida contém a senha do banco.

Para login, continue usando:

```bash
migracao/local/docker.sh login
```

O wrapper ajusta a URL emitida pela CLI para a porta externa configurada. A CLI chamada diretamente dentro de `app` emite a porta interna do BFF, 3100, que não é publicada no host.

Use o projeto com uma única instância de `app`: o supervisor mantém locks da instalação e do banco. Não inicie processos Go/BFF adicionais contra esses mesmos volumes.

## 11. Parar, retomar e preservar os dados

Escolha a operação conforme a necessidade:

| Objetivo | Comando | O que permanece |
| --- | --- | --- |
| Parar todos os serviços | `migracao/local/docker.sh stop` | Containers, rede, volumes e configuração |
| Remover containers e rede | `migracao/local/docker.sh down` | Volumes e configuração privada |
| Retomar após qualquer uma das opções | `migracao/local/docker.sh up` | Reutiliza os dados e aguarda prontidão |
| Autenticar após retomar | `migracao/local/docker.sh login` | Cria novo ticket; não altera os dados |

Após reiniciar o computador, inicie o Docker, volte à raiz do checkout, selecione o mesmo contexto e execute:

```bash
export SALTCORN_DOCKER_CONTEXT=default
migracao/local/docker.sh up
migracao/local/docker.sh login
```

Se você usava outro contexto, substitua `default` pelo valor anterior. Os serviços não têm política de reinício automático definida neste Compose; use `up` para retomá-los.

A persistência está distribuída entre:

| Local | Conteúdo |
| --- | --- |
| Volume `saltcorn-migracao-docker_postgres-data` | Dados PostgreSQL |
| Volume `saltcorn-migracao-docker_instance-data` | Configuração/segredo da instância, senha inicial, arquivos e locks |
| `migracao/local/.docker-state/compose.env` | Senha original do PostgreSQL e porta externa |

**Preservar volumes não equivale a ter backup.** Para backup consistente, consulte a rotina da distribuição. Para uma simples parada/retomada, não remova esses volumes nem use `down -v`. O script não oferece reset automático.

Se os volumes existirem e `compose.env` tiver sido perdido, o script recusa gerar outra senha. Restaure o arquivo original; regenerar uma variável não troca a senha do PostgreSQL já inicializado. Os arquivos privados são ignorados pelo Git e não voltam com um novo clone.

## 12. Diagnosticar problemas

Comece pelos comandos abaixo, usando o mesmo contexto do ambiente:

```bash
migracao/local/docker.sh status
migracao/local/docker.sh logs app
# Ctrl+C para sair; depois, se necessário:
migracao/local/docker.sh logs postgres
```

| Sintoma | Verificação e próximo passo |
| --- | --- |
| `docker: command not found` ou Compose indisponível | Confirme instalação do Docker e do plugin Compose v2 no terminal usado. No WSL2, confira também o acesso ao Docker Desktop. |
| Não conecta ao daemon ou recebe permission denied | Inicie o Docker; confira `docker context ls` e `docker --context "$SALTCORN_DOCKER_CONTEXT" info`. Corrija o acesso ao daemon antes de repetir `up`. |
| Erro de `POSTGRES_PASSWORD` ao usar Compose direto | Execute o primeiro `docker.sh up` e use os argumentos `--env-file` e `-f` do passo 10. Não invente outra senha para os volumes existentes. |
| Porta 5180 já está em uso | Escolha uma porta livre; se `compose.env` já foi criado, use o passo 9. O script não encerra o processo que ocupa a porta. |
| Build falha ao baixar dependências/imagens | Verifique a rede e a mensagem do build. Corrija o acesso e repita `up`; os dados persistentes não dependem do cache de build. |
| `app` ou `frontend` fica unhealthy | Consulte `status` e os logs de `app`, `postgres` e `frontend`. Confirme primeiro a saúde do banco. Depois da correção, repita `up`. |
| `/readyz` retorna erro ou 502 | Confira se `app` está healthy; o Nginx depende do BFF e o BFF depende do Go. Use `logs app` para identificar a falha. |
| Ticket inválido/expirado ou `401 session_required` | Execute `login` novamente, abra o link inteiro em até 60 segundos e mantenha `localhost` no navegador. |
| Dados esperados não aparecem | Confira o contexto Docker e se os dados foram criados em `docker.sh` ou `local.sh`; são instâncias distintas. |
| Mudança em `config.json` não tem efeito | Esse arquivo configura `local.sh`. No modo Docker, a porta está em `.docker-state/compose.env`; veja o passo 9. |
| Código editado não aparece na interface | Execute `docker.sh up` para recompilar/aplicar a imagem e atualize o navegador. Não há HMR neste modo. |
| `instância em uso` / `banco em uso` | Verifique se há outra execução usando a mesma instância. Não remova arquivos de lock para contornar processos ativos. |
| `skipped_not_owner` nos logs do worker | O bootstrap concede somente as capacidades suportadas. Jobs sem ownership ficam desativados; não force ownership manualmente. |
| Volumes existentes sem `compose.env` | Restaure o arquivo privado original antes de retomar. Não apague os volumes como tentativa de corrigir o login. |

O tenant e o e-mail de bootstrap são definidos no entrypoint (`local` e `admin@local.test`). Não há opção no `config.json` para personalizá-los neste modo. O acesso local também não remove as lacunas funcionais da migração nem libera o [canário da GO-036](canario/RUNBOOK.md).

## 13. Validar escrita e persistência com o teste de navegador (opcional)

A aplicação deve estar pronta pelo passo 3. Para este teste adicional, tenha **Node.js 22 e npm no host**. Instale as dependências de teste e o Chromium:

```bash
npm ci --prefix migracao/e2e
(cd migracao/e2e && npx playwright install --with-deps chromium)
```

O instalador do navegador pode solicitar privilégios para instalar bibliotecas do sistema. Essa etapa é necessária somente para o teste automatizado, não para usar a interface.

Crie uma tabela/view e um registro pelo teste:

```bash
node migracao/local/smoke.mjs create --docker
```

**Resultado esperado:** JSON com `login: true`, `docker: true`, `reactBffGo: true`, `persisted: true` e `mode: "create"`. O campo `vite: false` é esperado neste modo.

Recrie os containers e confirme a leitura do registro:

```bash
migracao/local/docker.sh down
migracao/local/docker.sh up
node migracao/local/smoke.mjs recheck --docker
```

**Resultado esperado:** o mesmo sucesso, agora com `mode: "recheck"`. O teste gera novo login automaticamente e deixa os dados de demonstração na instância. O nome da tabela criada fica em `.docker-state/smoke-table.txt`; `recheck` precisa desse arquivo e do registro correspondente.

O job `docker-howto` de [`migracao-local-ci.yml`](../../.github/workflows/migracao-local-ci.yml) executa esse fluxo em CI. O teste confirma o recorte disponível de login, publicação e registros; não é uma comprovação de paridade completa com o legado.
