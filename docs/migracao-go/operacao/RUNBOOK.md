# GO-035 — Carga e falhas operacionais

## Limites do resultado

Este ensaio valida o recorte implementado de List/CRUD, duas sessões de tenants distintos, BFF Node, backend Go e PostgreSQL 16. O navegador Chromium executa React durante cada fase HTTP. Sessões são semeadas pelo harness usando os módulos reais de sessão; não existe login de usuário completo nesse recorte.

O perfil é **provisório de laboratório**, sem acordo de SLO de produção: 1/8/32 clientes concorrentes, cinco segundos por fase, leituras de List e 20% de escritas, mil registros iniciais por tenant. É um modelo fechado, sem pausas entre requisições: não estima chegada aberta, filas de usuários nem capacidade máxima de produção. Os dois tenants usam os mesmos IDs e nomes de tabela, com marcadores distintos. p95 ≤500 ms, p99 ≤1 s, zero erros normais, recuperação ≤30 s e RSS <512 MiB por serviço são limites de regressão do laboratório. CPU é medida, sem orçamento de produção inferido.

GO-002 não contém latência HTTP comparável; seu benchmark de renderização não permite calcular regressão HTTP de 10%. Volumes reais, mistura de views/plugins, concorrência entre tenants, duração de estabilidade e orçamento de recursos precisam ser acordados antes de qualquer promoção. GO-033 mantém bloqueios de paridade para piloto e integral. Um resultado verde **não libera produção**.

## Execução isolada

Requisitos: Linux com `/proc`, Node 22, Go 1.22, psql 16, PostgreSQL 16 e dependências de sistema do Chromium. O runner instala pacotes usando os lockfiles e o navegador fixado em `migracao/e2e`.

Criar um banco **exclusivo e descartável** `go035_load`, acessível somente em loopback. Obter suas credenciais por ambiente privado, sem colocá-las em logs/PR. Não usar a prévia nem o banco de origem. Depois, na raiz:

```bash
export GO035_DISPOSABLE=1
# SALTCORN_GO_TEST_DATABASE_URL deve apontar para o banco descartável go035_load.
migracao/operations/run.sh /tmp/go035-evidencia-nova
```

O diretório de evidência deve ser novo. O preflight recusa outro banco, endereço externo, opções adicionais de DSN ou ausência da confirmação. A fixture recria somente `load_a`/`load_b` nesse banco; testes de componente usam seus schemas isolados. Portas HTTP/proxy são alocadas pelo sistema. O harness encerra somente os processos e conexões que iniciou; não mata ocupantes de portas nem altera o container da prévia. Os dados do ensaio ficam disponíveis para inspeção após a execução.

## Evidências e portões

| Cenário | Prova exigida | Condição de abortar |
| --- | --- | --- |
| Carga React/BFF/Go | p50/p95/p99, throughput, status, três renderizações por tenant/fase, marcadores de tenant | Erro normal, SLO ultrapassado ou marcador alheio |
| Resposta perdida após commit | banco consultado antes do replay; uma linha antes/depois | Escrita ausente, duplicada ou replay divergente |
| Rede BFF–Go suspensa | burst de 192 chamadas, 502 controlados, limite de 64 chamadas Go, recuperação | Fila ilimitada, chamada pendurada, excesso do limite |
| Banco desconectado | reset de conexões pelo proxy, 502 e reconexão | Recuperação >30 s ou dado divergente |
| Banco lento/saturado | lock real de tabela, 256 chamadas diretas, Go admite ≤128 e pool ≤4, rejeita 503 | Ausência de rejeição, limite excedido, falta de recuperação |
| Worker interrompido na transação | trigger de teste pausa UPDATE, SIGKILL, rollback de status/tentativas, reinício drena 80 eventos | Evento perdido, status incorreto ou recuperação >30 s |
| Host JS | testes com processo real: timeout, crash, limite de memória e retomada | Teste falha ou é pulado |
| Armazenamento | path indisponível e recuperação, upload interrompido, limpeza e catálogo | Objeto parcial/catálogo inconsistente, teste falha ou é pulado |
| Recursos | amostras de RSS/CPU BFF e Go e métricas de pool/admissão a cada 200 ms | Amostra inválida, RSS ou limite excedido |

`report.json` só recebe `labPassed: true` após carga, falhas e testes de componentes sem skips. `latency-*.json`, `resources.json`, `*.prom`, `fault-tests.jsonl` e logs sustentam o resumo; `source-manifest.json` identifica os arquivos usados, inclusive alterações ainda sem commit. A CI guarda essas evidências como artefato.

Os 80 eventos são explicitamente semeados: CRUD HTTP ainda não emite eventos de domínio nesse caminho. O consumidor usado no ensaio registra eventos sintéticos em log; a prova é de durabilidade/reprocessamento, não de exactly-once em efeitos externos. Os testes transacionais de outbox complementam a interrupção do processo. Host JS e arquivos são ensaios de componente porque não estão conectados ao fluxo HTTP testado. Não extrapolar esses resultados para plugins, uploads HTTP, SMTP ou integrações externas em produção.

## Limites implementados e retomada

O BFF limita a 64 chamadas Go simultâneas por instância de `GoClient`, sem fila; timeout engloba também a leitura do corpo. Capacidade esgotada preserva o erro contratual 502 `domain_unavailable`. O backend limita a 128 handlers simultâneos, aplica contexto de dez segundos e devolve 503 `service_unavailable` com `Retry-After: 1`. Health/readiness/metrics continuam acessíveis. O pool do ensaio usa `pool_max_conns=4`; configurar um orçamento de conexões por instância antes de escalar. Esses limites não substituem limites de conexões/corpos no ingresso, dimensionamento de sessões/WebSockets nem proteção global entre réplicas.

Em falha, interromper promoção. Conferir processo, escritor/ownership, schema, contagens, chaves de idempotência e eventos. Não reenviar uma mutação de resultado incerto até consultar o efeito e reutilizar a mesma identidade/payload/chave. Não mudar rota para o legado presumindo rollback dos dados. Se uma execução foi interrompida, inspecionar os logs e o banco descartável, encerrar somente seus processos identificados e registrar a evidência inválida antes de iniciar nova tentativa em outro diretório. Para produção, aplicar também o [runbook de reconciliação GO-034](../recuperacao/RUNBOOK.md).
