# GO-036 — Resultado do preflight

**BLOCKED — canário não liberado.** Onze testes de diagnóstico passaram, mas os critérios operacionais da task não foram satisfeitos. O [relatório](report.json) registra hashes dos inputs e do programa; o [runbook](RUNBOOK.md) define responsáveis, sequência de corte e retomada.

| Verificação | Resultado |
| --- | --- |
| Dependências formais GO-021/024/025/026/027/034 | DONE; merges presentes na base `337d5c648697259310c05e8177d9a5be8476387c` |
| GO-035 | PR #39 integrado; oito checks SUCCESS; promoção de produção continua bloqueada |
| Subconjunto guitars | 48 capacidades; 39 bloqueadas por lacunas/evidência insuficiente |
| Pack real | Nove views: duas List, cinco Edit, uma Feed e uma Show; trigger run_js_code / ReceiveMobileShareData |
| Carga e baseline | GO-035 valida laboratório; não cobre workload/SLO/volume do alvo |
| Recuperação | GO-034 valida authors/books sintéticos; não certifica guitars/ambiente ou retorno legado |
| Plano alvo | Ambiente, dois tenants, janela e SLO ainda não informados/acordados |
| Operação de corte | Edge, coordenação de processos/guardas, escritor e jobs do alvo não verificados |
| Diagnóstico executado | 45 impedimentos documentais: 39 de paridade, carga, recuperação e quatro campos operacionais ausentes |
| Tráfego/escritas/leases/jobs | Nenhuma alteração pela GO-036; `runtime_verified=false`, `canary_released=false` |

Comandos executados na raiz, Python 3.12.3:

```bash
python3 -m unittest discover -s migracao/canary -v
python3 migracao/canary/preflight.py --output /tmp/go036-preflight-final.json
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 .github/workflows/migracao-canary-ci.yml
git diff --check
```

Resultados: testes exit 0 (11/11); preflight exit **2 esperado**, preservando o bloqueio; actionlint exit 0. A CI distingue o sucesso dos testes da negação da liberação. Rodar com `python -O` também mantém a negação. Casos negativos cobrem evidência ausente, booleano agregado forjado, redução/duplicação de escopo, falha web, allowlist inválida, SLO/janela inválidos e tentativa de sobrescrever evidência.

Não repetimos carga, instalação ou navegador: o runtime não foi alterado e as evidências existentes já demonstram que esses ensaios não aprovam o piloto. PASS de uma suíte repetida não resolveria os impedimentos. A revisão operacional deve produzir novas evidências específicas após implementar as lacunas e identificar o alvo.

A GO-036 não será marcada IN_REVIEW/DONE apenas pela abertura/merge do PR preparatório. Conforme EXECUCAO.md §§1/5, pendências essenciais mantêm a task **BLOCKED**. A solicitação de contexto do alvo ao usuário não recebeu resposta nesta execução; nenhum tenant, janela ou acordo de produção foi presumido.
