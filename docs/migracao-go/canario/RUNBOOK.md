# GO-036 — Canário do piloto guitars

## Decisão corrente

**Liberação bloqueada. Nenhum tenant foi roteado.** Este PR prepara o diagnóstico reproduzível e o procedimento de retomada; não cumpre o aceite de liberar e observar um canário. A task permanece BLOCKED, mesmo que o PR preparatório seja integrado.

O escopo definido na GO-001 é `guitars`: web/PostgreSQL, dois tenants, papéis distintos, relações, listagem/filtro, formulário e automação. Não substituir esse piloto pela demonstração List/CRUD da GO-035 para declarar aceite. SQLite/mobile não são exigidos quando ausentes do escopo; o evento `ReceiveMobileShareData` do pack ainda exige definir e provar sua alternativa web ou preservar explicitamente a integração necessária.

| Impedimento | Evidência | Condição de desbloqueio / responsável |
| --- | --- | --- |
| 39/48 capacidades do piloto sem paridade completa | [GO-033](../paridade/RESULTADOS.md), matriz e pack original | Backend/frontend/QA: fechar lacunas usadas pelo piloto e produzir prova funcional da aplicação |
| Edit/Show/Feed e formulário indisponíveis no runtime Go | Pack contém nove views; só duas List; `internal/views/render.go` aceita somente List | Backend/frontend: implementar os fluxos e comparar seu comportamento ao legado |
| Automação do piloto sem caminho completo | Pack contém `run_js_code` em `ReceiveMobileShareData`; HTTP CRUD em `cmd/server/records.go` usa hooks nil | Backend/QA: ligar e validar a automação efetivamente usada, com idempotência/outbox |
| Ambiente, tenants, janela e SLO sem definição operacional | Plano exemplo intencionalmente vazio; solicitação de contexto ao usuário | Operação/proprietário do piloto: informar alvo e acordar limites/observação |
| Carga e baseline não representam o alvo | [GO-035](../operacao/RESULTADOS.md) comprova laboratório fechado, cinco segundos/fase | QA/Operação: medir mistura de tráfego, volume e baseline HTTP comparáveis no alvo |
| Recuperação não certifica aplicação/alvo | [GO-034](../recuperacao/RESULTADOS.md) usa authors/books sintéticos; retorno legado bloqueado | Backend/Operação: ensaiar recuperação do guitars com versões/dados do alvo e definir pausa ou retorno certificado |
| Corte coordenado não implementado | [ADR-0008](../adr/0008-roteamento-de-corte-e-ownership.md): edge/operação entre processos pendentes; caches de ownership carregados no boot | Plataforma: validar edge, bloqueio/drenagem de todos os escritores e atualização coordenada das guardas |

Merge de dependências significa integração dos artefatos entregues, não comprovação de paridade da aplicação operacional. Não reabrimos todas essas tasks automaticamente: o diagnóstico identifica o comportamento faltante e sua evidência, sem invalidar suas garantias transacionais já testadas.

## Executar o diagnóstico

Requer Python 3.10+ e Git, sem serviços ou credenciais. Na raiz:

```bash
python3 -m unittest discover -s migracao/canary -v
python3 migracao/canary/preflight.py --output /tmp/go036-preflight-novo.json
```

O resultado corrente é exit **2**, `decision=BLOCKED`, `canary_released=false`. Exit 2 é negação da liberação, não falha inesperada do teste. Não usar `|| true` para converter esse diagnóstico em sinal verde de deploy. O arquivo de saída deve ser novo; tentativas anteriores não são sobrescritas. A CI verifica separadamente que os testes passam e que o piloto atual continua bloqueado.

Para registrar o alvo proposto, copiar `migracao/canary/plan.example.json` para um arquivo privado e informar: alias do ambiente; dois tenants explícitos; janela em segundos; SLOs `p95_ms`, `p99_ms` e `recovery_seconds`. Usar `--plan caminho.json`. Não incluir tokens, URLs com senha, comandos nem dados pessoais. Curingas, duplicatas e campos desconhecidos são recusados. Preencher esse plano **não prova acordo, saúde do alvo, permissão ou execução**.

O programa lê somente metadados do `pack.json` dentro do ZIP e relatórios/matriz versionados. Registra hashes dos inputs para revisão. Recalcula o subconjunto piloto por capacidade/suíte/pacote; não confia no booleano agregado `eligible`. Evidências são snapshots históricos: o diagnóstico não consulta runtime nem verifica processos vivos, e um hipotético resultado sem bloqueios documentais ainda exige validação operacional. Não há `apply`, alteração de proxy, escrita de ownership ou acesso a banco.

## Plano de execução após desbloqueio

Esta sequência é um plano para revisão, não uma instrução para operar agora. Só preparar a execução com os impedimentos acima resolvidos e o alvo identificado.

1. **Fixar o escopo:** registrar release/hash, contratos, schema, pack/plugins, tenant inicial, segundo tenant de controle, papéis, capacidades HTTP e jobs envolvidos. Obter janela e limites de erro/latência/recuperação do proprietário. Mapear os processos reais de edge, legado, BFF, Go e workers. Provar que o plano não seleciona tenants fora da allowlist.
2. **Estabelecer baseline:** medir o mesmo workload/volume na origem e candidato; registrar equipamento, configuração, p95/p99, erros, throughput, CPU/RSS, pool, filas, idade e quantidade de eventos. Não comparar render benchmark de GO-002 com latência HTTP. Definir os limites antes da onda, sem ajustá-los para encobrir falha.
3. **Certificar dados e recuperação:** em cópia sanitizada do alvo, ensaiar expand/contract, sequência/IDs/relações, chaves de idempotência, arquivos e eventos; validar restauração após escritas Go. Escolher explicitamente entre retorno legado comprovadamente compatível e pausa/reconciliação. Com a evidência atual, retorno legado permanece proibido.
4. **Preparar coordenação real:** bloquear novas admissões no edge para o escopo; drenar requests e jobs em todas as réplicas. Conferir que não restam escritores/leases ativos e que eventos externos incertos foram reconciliados. Uma expiração de lease não prova que o processo antigo parou. Manter schema compatível durante toda a janela.
5. **Cortar o primeiro tenant:** somente com a coordenação implementada e testada, mudar ownership e carregar a mesma decisão em todas as guardas de servidor/worker antes de liberar admissões. Registrar geração/acknowledgments e escritor ativo. Alterar `_sc_capability_ownership` via SQL isolado ou chamar `SwitchOwner` numa única réplica não satisfaz essa condição.
6. **Rotear e observar:** rotear o tenant inicial por identidade validada, sem confiar em header escolhido pelo cliente; manter o segundo tenant como controle. Leituras e mutações da mesma capacidade seguem o proprietário único. Erro de Go em uma mutação não autoriza fallback para Node nem repetição com nova chave. Exercitar a aplicação completa nos papéis previstos e registrar cada intervalo de observação.
7. **Avançar apenas com evidência:** ao fim da janela acordada, reconciliar dados/eventos e comparar métricas ao baseline. Só então avaliar o segundo tenant, repetindo a mesma disciplina. GO-037 exige sua própria decisão de onda; não ampliar o escopo por porcentagem automática.

## Abortar e retomar

Abortar novas admissões do escopo ao detectar vazamento entre tenants, dois escritores, perda/duplicação de evento ou efeito, divergência de dados/schema, fila sem limite, incidente crítico ou SLO ultrapassado. Registrar último ponto consistente e manter o restante dos tenants sob a política já comprovada.

Na retomada, consultar estado **real** de rotas, ownership em cada processo, schema, jobs/leases, chaves, registros e provedor externo. Uma resposta perdida pode significar commit concluído. Reconciliar antes de reenviar e reutilizar a mesma identidade/payload/chave. Se o resultado de um efeito externo não puder ser determinado, manter o escopo pausado e escalar ao responsável.

Não repetir o corte às cegas. Não restaurar backup antigo sobre escritas Go confirmadas sem reconciliação. Não ligar Node ao banco escrito por Go sem certificação de compatibilidade. Seguir o [runbook GO-034](../recuperacao/RUNBOOK.md) para pausa, evidência e candidato recuperado; reabrir tráfego somente após conferir novamente escritor único e integridade.

## Evidência necessária para concluir a task

Registrar alvo/versões, acordo de janela/SLO, matriz do piloto sem lacunas, baseline comparável, ensaio de recuperação específico, snapshots de estado antes/depois, tenant/capacidade roteados, acknowledgments de coordenação, séries durante a janela, incidentes e reconciliação final. Revisão humana, checks e merge são necessários, mas não substituem essa operação. Até existir tal evidência, GO-036 continua **BLOCKED**, e nenhuma onda dependente pode tratá-la como liberada.
