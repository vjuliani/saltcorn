# GO-035 — Resultados de laboratório

Execução final `/tmp/go035-run-04`; início UTC `2026-09-20T18:11:21.469Z`. **Laboratório aprovado; promoção de produção bloqueada.** O [runbook](RUNBOOK.md) define o perfil provisório, reprodução e limites de interpretação. O [relatório JSON](report.json) contém resultados, nomes dos 75 testes, hashes dos artefatos e fontes usados.

| Clientes | Requisições | req/s | p95 (ms) | p99 (ms) | Erros |
| --- | --- | --- | --- | --- | --- |
| 1 | 214 | 42.6 | 35.9 | 53.2 | 0 |
| 8 | 1044 | 208.0 | 64.5 | 86.6 | 0 |
| 32 | 872 | 169.2 | 270.5 | 307.8 | 0 |

Durante as fases, Chromium realizou 18 renderizações React distribuídas pelos dois tenants, verificando os marcadores de dados. Reconciliação final compara todos os títulos com o conjunto exato esperado, incluindo escritas e retry de resultado incerto. Não houve linhas ausentes, duplicadas nem vazamento entre tenants.

| Serviço | RSS máximo (MiB) | CPU acumulada (s) | Janela (s) |
| --- | --- | --- | --- |
| bff | 122.1 | 8.40 | 41.8 |
| go | 28.1 | 7.40 | 41.8 |

209 amostras a cada 200 ms. Recursos medidos nos processos BFF e Go; navegador, banco, worker e gerador não entram nesses números. Não é teste prolongado de vazamento de memória.

| Falha | Resultado |
| --- | --- |
| lost-response-after-commit | Uma linha antes e depois do replay; zero duplicatas |
| bff-go-timeout-saturation | 192 erros 502 controlados; pico 64; 128 rejeições; recuperação incluída em 1766 ms |
| db-network-reset | Reconexão e leitura em 96 ms |
| go-db-pool-saturation | 256 chamadas; 128 rejeições 503; 128 handlers e quatro conexões |
| worker-sigkill-restart | Rollback após SIGKILL; 80 eventos preservados e drenados em 15397 ms desde a interrupção |

Host JS, arquivos, outbox, leases, scheduler e admissão: **75 testes com race detector, zero skips/falhas**. Host real executa timeout, crash e limite de heap; filesystem real falha com path indisponível e recupera após restauração. Essas provas são de componentes, sem alegação de integração HTTP ausente.

Verificações complementares: BFF 51/51; `go vet ./...` e `go build ./...`; geração/lint/typecheck de contratos, testes dos clientes Go; actionlint do novo workflow e consistência das 38 tarefas.

## Falhas de desenvolvimento preservadas

- `go035-run-01`: compilação interrompida por import `time` ausente; corrigido antes dos ensaios HTTP.
- `go035-run-02`: carga normal passou, mas a observação instantânea do pool ocorreu antes de quatro conexões estarem adquiridas. A asserção interrompeu o ensaio; corrigida a espera pela condição conjunta de admissão/pool e o tratamento da promise pendente durante limpeza.
- `go035-run-03`: carga/falhas e 75 testes passaram, mas o gate leu a versão nova de um campo de relatório enquanto o processo ainda executava a versão anterior. Essa execução não vale como gate final. A tentativa 04 foi iniciada com fontes estáveis e manifesto; também passou a medir recuperação do worker desde o SIGKILL, incluindo rollback e retirada da injeção.

## Decisão

Os limites provisórios do laboratório foram atendidos. A promoção permanece impedida pelos SLOs/volumes de produção não acordados, ausência de baseline HTTP comparável e lacunas de paridade de GO-033. Não houve corte de tráfego, mudança de escritor em produção ou merge automático.
