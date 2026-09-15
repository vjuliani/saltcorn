# Protocolo de execução da migração

Aplica-se às 38 tarefas de [TASKS.md](TASKS.md). Stack: React + SB Admin 2, BFF Node.js e backend Go. Este protocolo organiza execução futura; não inicia tarefas nem declara validações de implementação realizadas.

## 1. Registro e estados

[tasks.csv](tasks.csv) é a fonte do estado corrente e dos campos operacionais. Manter status, checkbox e conteúdo correspondente no Markdown sincronizados na mesma alteração. `responsavel_sugerido` é um papel; `executor` identifica quem assumiu a execução. `execucao_id` identifica a tentativa, por exemplo `GO-017-20260915T140000Z-01`.

| Estado | Condição | Próxima transição |
| --- | --- | --- |
| TODO | Ainda não iniciado | IN_PROGRESS após preflight |
| IN_PROGRESS | Executor e escopo registrados | VALIDATING, PAUSED ou BLOCKED |
| VALIDATING | Artefato pronto, checks em execução | IN_REVIEW após validação e abertura do PR; IN_PROGRESS se precisar corrigir; PAUSED/BLOCKED se interrompido |
| IN_REVIEW | PR aberto; aguardando revisão, CI ou merge | DONE após revisão, checks e merge; IN_PROGRESS se precisar corrigir; BLOCKED se houver impedimento |
| PAUSED | Interrupção com checkpoint e próximo passo | IN_PROGRESS após rotina de retomada |
| BLOCKED | Impedimento identificado e condição objetiva de desbloqueio | IN_PROGRESS após comprovar resolução |
| DONE | Critérios satisfeitos, revisão e checks aprovados, PR integrado à base | IN_PROGRESS se reaberta por regressão ou evidência invalidada |

Não concluir por tempo gasto, compilação isolada ou término de sessão. Decisão de adiar projeções em GO-016 pode cumprir seu aceite, pois essa tarefa é uma avaliação. Pular uma capacidade requerida não equivale a concluir sua implementação.

Se uma dependência concluída for reaberta, avaliar seus dependentes: bloquear integração dos que dependem do comportamento invalidado e revalidar evidências afetadas. Registrar a análise; não reabrir automaticamente tarefas sem impacto.

## 2. Preflight e controle de concorrência

1. Ler tarefa, aceite, rotina específica e dependências. Confirmar dependências em DONE, PRs integrados à base e evidências ainda aplicáveis. Explorações antecipadas devem ser isoladas e não habilitam integração.
2. Inspecionar branch, commit, `git status`, alterações existentes e executor ativo. Preservar trabalho local; não usar reset/clean para obter ambiente aparentemente limpo.
3. Registrar executor, tentativa, ambiente, versões, arquivos/capacidades previstos, risco e checks planejados. Dividir tarefas grandes em subtarefas identificadas no histórico da tarefa.
4. Garantir um executor ativo por tarefa e um proprietário de escrita por capacidade/tenant. Reservar alterações de schema, contratos e arquivos compartilhados; trabalhos paralelos devem ter fronteiras explícitas e ordem de integração.
5. Criar ou retomar a branch da task conforme a seção 8, registrar base e atualizar estado para IN_PROGRESS antes de alterar implementação. Mudança de executor exige handoff com checkpoint e verificação de que o anterior não continua executando.

O CSV é controle de coordenação, não lock distribuído. Antes de ações em banco/produção, usar os mecanismos reais de lock/lease e ownership previstos na implementação. Uma expiração de sessão ou ausência de resposta não comprova que processos/jobs pararam.

## 3. Rotina de validação

1. **Antes:** reproduzir o comportamento ou registrar baseline, fixture e resultado esperado. Identificar o ambiente para não executar ensaios destrutivos na origem errada.
2. **Durante:** rodar os checks necessários ao caminho alterado. Registrar comando exato, diretório, versões, configuração não sensível, exit code e resultado. Não inventar comandos de módulos ainda inexistentes: defini-los no preflight quando a implementação existir.
3. **Integração:** validar contratos e consumidores afetados. Para mudanças em autenticação/dados, incluir negativos por tenant, papel e ownership. Para mutações, incluir falha, rollback e repetição; para UI, fluxo funcional e acessibilidade pertinentes.
4. **Conclusão:** ligar cada critério de aceite a uma evidência e ao commit validado. Evidência de árvore com mudanças locais deve registrar também o diff/artefato correspondente; só o hash HEAD não descreve código não commitado.
5. **Revisão:** registrar responsável e resultado da revisão técnica. Evidência ausente, teste obrigatório falhando ou não executado mantém a tarefa fora de DONE. Justificar checks não aplicáveis; não tratar falha como não aplicabilidade.

Evitar reexecutar suites sem motivo: repetir checks após mudanças relevantes, falhas, alteração de ambiente ou evidência vencida por novo código. Resultados intermitentes exigem investigação; um retry verde não apaga a falha anterior. Evidências não devem conter credenciais, tokens ou dados pessoais de produção.

## 4. Checkpoints e retomada

Criar checkpoint ao concluir subetapa, antes de pausa/handoff, após resultado de validação e antes/depois de ação com efeito persistente. Atualizar `checkpoint`, `evidencias`, `bloqueio` e `proximo_passo` no CSV. Registrar data/hora em UTC, tentativa, commit/diff, trabalho concluído, pendente e processos ainda ativos.

Para retomar:

1. Ler último checkpoint e histórico; conferir branch, base e PR existentes (aberto, fechado ou integrado); comparar estado documentado com Git, ambiente, versões, banco, filas e processos reais. Não criar PR duplicado nem sobrescrever uma branch existente.
2. Conferir se dependências, contratos ou ownership mudaram. Preservar alterações existentes e resolver divergências antes de continuar.
3. Marcar como inválidas as evidências afetadas pelas mudanças e definir os checks que precisam ser repetidos.
4. Reconciliar operações cujo resultado ficou desconhecido: consultar idempotency key, registro, evento/outbox ou provedor externo. Não repetir cegamente escrita, upload, migration, publicação ou corte.
5. Registrar nova tentativa com referência à anterior, executor e próximo passo concreto; mudar para IN_PROGRESS somente após resolver o bloqueio/preflight.

Retomar do último estado consistente comprovado, não apenas da última linha do log. Se houve falha parcial de migração ou corte, congelar novas etapas desse escopo, conferir escritor ativo e executar recuperação/reconciliação prevista. Mudança de rota não desfaz dados.

## 5. Falhas, bloqueios e encerramento

- Falha corrigível: manter IN_PROGRESS, registrar diagnóstico e correção; voltar a VALIDATING com novos resultados.
- Dependência externa ou decisão pendente: BLOCKED com causa, impacto, responsável pela resolução e condição verificável de liberação. Continuar outras tarefas independentes quando possível.
- Interrupção planejada: PAUSED com checkpoint e próximo passo; registrar como encerrar ou retomar processos ativos.
- Risco confirmado de perda de dados, acesso entre tenants ou dois escritores: interromper a operação afetada e aplicar procedimento de contenção/recuperação antes de avançar.
- IN_REVIEW: implementação validada, commit/push realizados e PR aberto; registrar URL, SHA e resultados de CI. A entrega da execução termina aqui, sem merge automático.
- DONE: confirmar aceite, revisão, checks obrigatórios, merge na base, artefatos, compatibilidade e pendências resolvidas; sincronizar CSV/Markdown e encerrar reservas de execução. Pendências essenciais exigem manter tarefa aberta.

Ao executar uma task, criar sua branch e finalizar a implementação com commit, push e abertura/atualização de PR faz parte do fluxo solicitado. Merge e deploy não são automáticos. Se autenticação ou permissões impedirem push/PR, registrar BLOCKED, preservar commits locais e informar o impedimento; não declarar PR aberto.

## 6. Modelo de histórico por tarefa

Criar `docs/migracao-go/execucoes/GO-NNN.md` quando a execução começar. Acrescentar tentativas em sequência, preservando falhas e decisões anteriores.

```markdown
# GO-NNN — Título

## Tentativa GO-NNN-AAAAMMDDTHHMMSSZ-01

- Executor / revisor:
- Início / última atualização UTC:
- Estado / motivo:
- Branch / base / commit / identificação do diff ou artefato:
- URL e estado do PR / SHA validado / merge commit quando integrado:
- Ambiente / versões / fixture:
- Dependências verificadas e evidências:
- Escopo reservado / proprietário de escrita:
- Subtarefas concluídas / pendentes:

### Validações

| Critério de aceite | Comando/procedimento e diretório | Resultado / exit code | Evidência / versão validada |
| --- | --- | --- | --- |
| Preencher | Preencher | Pendente | Preencher |

### Checkpoint

- Data/hora UTC e estado consistente comprovado:
- Operações persistentes / chaves de idempotência não sensíveis:
- Jobs/processos/leases ainda ativos:
- Divergências, falhas e evidências invalidadas:
- Bloqueio e condição de desbloqueio:
- Próximo passo exato e checks para retomada:
- Recuperação/rollback aplicável:
- Revisão / decisão de encerramento:
```

## 7. Consistência do backlog

Ao editar os documentos, conferir: IDs únicos; dependências existentes e sem ciclos; estados permitidos; correspondência dos critérios/rotinas entre Markdown e CSV; checkbox marcado somente para DONE; links existentes. Para tarefas iniciadas, exigir executor, tentativa, checkpoint e próximo passo ou evidência de encerramento. Essa conferência valida o controle documental, não substitui os testes de produto.


## 8. Branch, commit e pull request

Uma branch por task, no padrão `task/go-NNN`. A base deve conter as dependências já integradas. A execução termina entregando PR aberto; o acompanhamento passa para IN_REVIEW. DONE exige merge comprovado. Registrar DONE no controle central após a integração (em atualização de acompanhamento), sem antecipar esse estado no PR ainda aberto.

### Preflight e criação (exemplo GO-017)

Executar na raiz do checkout. Substituir o ID nos comandos ao trabalhar em outra task. Pré-requisitos: Git, GitHub CLI autenticado e permissão no fork. A descoberta da base é feita uma vez e registrada; não presumir `main` ou `master`.

```bash
git status --short
git remote -v
gh auth status
gh repo view vjuliani/saltcorn --json defaultBranchRef --jq '.defaultBranchRef.name'
git fetch origin
```

Depois de conferir as saídas, atribuir à variável abaixo o nome retornado. Os blocos são procedimentos condicionais, não um script para colar inteiro. Não prosseguir se houver alterações alheias que possam ser carregadas para a task: preservá-las no checkout original e usar worktree isolada, ou integrar somente mudanças autorizadas com origem documentada.

```bash
TASK_BASE='nome-da-base-retornado'
TASK_BRANCH='task/go-017'
git branch --list "$TASK_BRANCH"
git branch --remotes --list "origin/$TASK_BRANCH"
gh pr list --repo vjuliani/saltcorn --head "$TASK_BRANCH" --state all
```

Se branch e PR não existirem e o checkout estiver apropriado para a task:

```bash
git switch --create "$TASK_BRANCH" "origin/$TASK_BASE"
```

Como alternativa para preservar trabalho alheio, criar worktree em caminho livre e executar a task dentro dela:

```bash
git worktree add -b "$TASK_BRANCH" ../saltcorn-go-017 "origin/$TASK_BASE"
```

Se a branch local já existir, usar `git switch "$TASK_BRANCH"` após conferir o checkout. Se existir somente no remoto, usar `git switch --track "origin/$TASK_BRANCH"`. Antes de retomar, conferir histórico, divergências, PR e checkpoint; não usar `switch -C`, force push ou rebase destrutivo para simplificar o fluxo. PR fechado sem merge exige registrar motivo e decidir reabertura ou nova tentativa rastreável.

### Validação e commit

Executar os checks específicos da task e registrar comandos reais no histórico. Revisar o diff; selecionar apenas arquivos da task. Os caminhos abaixo são exemplos e precisam corresponder aos arquivos efetivamente alterados.

```bash
git diff --check
git diff --stat
git add -- docs/migracao-go/execucoes/GO-017.md
# Adicionar explicitamente os demais arquivos de implementação e controle da task.
git diff --cached --check
git diff --cached
git commit -m "GO-017: implementar BFF web em Node.js"
git push --set-upstream origin "$TASK_BRANCH"
```

Não usar `git add .` indiscriminadamente. Não publicar segredos, fixtures de produção ou alterações de outras tasks. Se o push falhar, registrar a falha e retomar a publicação a partir do commit existente.

### Abrir ou atualizar PR

Preparar a descrição em um arquivo temporário fora do repositório, informando problema, resultado, task, dependências, mudanças, validações com SHA/ambiente, limitações e retomada/rollback quando aplicável. Exemplo, após criar o arquivo de descrição:

```bash
TASK_PR_BODY='/tmp/go-017-pr.md'
gh pr list --repo vjuliani/saltcorn --head "$TASK_BRANCH" --base "$TASK_BASE" --state open
gh pr create --repo vjuliani/saltcorn --base "$TASK_BASE" --head "$TASK_BRANCH" --title "GO-017: implementar BFF web em Node.js" --body-file "$TASK_PR_BODY"
```

Executar `gh pr create` apenas se a consulta não encontrar PR aberto. Havendo PR, usar `gh pr edit` com sua URL e `--body-file` para atualizar a descrição. Após publicação, registrar `pr_url`, base, SHA e IN_REVIEW no CSV/Markdown e histórico; fazer commit/push desse registro na mesma branch. Se o código mudar depois da validação, repetir os checks afetados.

```bash
gh pr view --repo vjuliani/saltcorn "$TASK_BRANCH" --json url,state,baseRefName,headRefName,headRefOid,mergeCommit
gh pr checks --repo vjuliani/saltcorn "$TASK_BRANCH"
```

Checks pendentes ou ausência de CI não comprovam sucesso. Informar ao usuário URL, validações executadas e pendências. Correções de revisão usam a mesma branch/PR. Não executar merge automático; quando o merge ocorrer, verificar integração e registrar DONE. Branch por task não elimina a necessidade de resolver conflitos dos arquivos compartilhados de acompanhamento.
