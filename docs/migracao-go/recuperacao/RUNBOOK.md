# GO-034 — Pausa, reconciliação e recuperação

Este procedimento atende ao caminho de **pausa/reconciliação** de GO-034 e ADR-0006. Reverter uma rota não desfaz escritas. A evidência é um laboratório PostgreSQL 16 com cópia sintética; não autoriza corte de um tenant real. O [runner reproduzível](../../../migracao/rehearsal/README.md) e o [histórico](../execucoes/GO-034.md) registram versões e resultados.

## Contrato do ensaio e pré-condições da onda

| Item | Ensaio executável | Antes de uma onda real |
| --- | --- | --- |
| Origem | 2 autores/2 livros, IDs não sequenciais e FK; sem dados pessoais | Cópia sanitizada, inventário de tenants/capacidades/arquivos/plugins e mapeamento explícito por versão |
| Migração | Pack v1 cria catálogo; mapeamento allowlist copia registros/IDs e reposiciona sequences | Adaptador revisado para o schema legado exato; não usar `setup`/`migrate` sobre banco não gerenciado |
| RPO | Zero das 3 transações Go confirmadas; origem disponível no momento da pausa | Meta aprovada com operação; para perda da origem, definir e ensaiar WAL/PITR/replicação separadamente |
| RTO | ≤60s da pausa até candidato Go restaurado e reconciliado; inclui drenagem, backup final, restore e validação | Medir novamente com volume, recursos, arquivos, rede, processos e reabertura de tráfego reais |
| Leitores | Schema Go 1→2, pack1, release 0.1.0-go032; retorno Go validado | Prova de leitura/escrita Node sobre os dados novos antes de considerar retorno legado |
| Escritor único | Guard, dreno, registro persistido, flock e lease PG exclusivo; concorrente recusado | Todas as réplicas Go/Node, workers, cron, hosts JS e escritores externos identificados e cercados |

**Não há meta operacional de produção definida neste checkout.** Os valores acima são critérios deste ensaio. GO-033 continua bloqueando promoção do piloto/integral; GO-035/036 ainda são necessárias. Falta de compatibilidade Node não é um resultado verde: a saída mantém `legacy_traffic_allowed=false`.

## Antes de migrar

1. Identificar banco/cluster, tenant, capacidade, versão de origem/destino/contratos, owner atual, processos, filas e último checkpoint. Registrar quem controla admissão na borda e quem pode escrever diretamente no banco. Não inferir parada por sessão expirada.
2. Reservar um único operador/executor. No perfil self-hosted, usar a CLI e seu lock local + lease PG; um lease livre **não prova** que um processo legado ou `cmd/server` iniciado diretamente parou, pois eles podem não cooperar com o lock.
3. Manter a origem disponível e imutável após congelamento. Criar backup original com manifesto; confirmar restore em destino novo antes do corte. Guardar credenciais e arquivos de instância em diretório privado, fora de logs/artefatos públicos.
4. Capturar snapshot consistente (repeatable read) de IDs, contagens, valores canônicos e checksums por tabela; incluir relações, sequences, metadados, configurações, arquivos, inventário de plugins, chaves de idempotência e estados de outbox. Definir normalizações permitidas, sem excluir diferenças arbitrariamente para obter igualdade.
5. Migrar por expansão: novos objetos/colunas compatíveis; nenhuma remoção, renomeação ou mudança incompatível de tipo na janela de retorno. No ensaio, uma event trigger rejeita DROP de objetos/colunas e prova rollback do comando; essa barreira de teste não é um validador universal de DDL.
6. Comparar dados migrados com a origem antes de admitir Go. Pack não contém registros nem certifica todo o legado. Na fixture, IDs/FKs/contagens/checksums coincidem e o próximo ID inserido é 301, não 1.

## Após escritas Go: pausa obrigatória

1. Fechar admissão na borda para o escopo, mantendo **ambos** os destinos Go e Node fechados. Iniciar cronômetro de recuperação. Suspender novos jobs/efeitos externos, drenar trabalho em curso e encerrar todos os processos escritores confirmados. Não enviar e-mails/webhooks novamente para “corrigir” incerteza.
2. Esperar os processos saírem, conferir conexões/transações/locks e filas reais. No pacote self-hosted, TERM no supervisor inicia dreno dos filhos; aguardar sua saída. Em topologias com múltiplas réplicas, verificar cada uma. Nunca usar `fuser -k`, kill por nome genérico ou remover locks à força.
3. O teste mostra que timeout de `Guard.Drain` mantém admissão fechada e não troca ownership. Corrigir a causa, conferir unidades em voo e repetir a transição somente depois do dreno. Não atualizar `_sc_capability_ownership` com processos ativos: a guarda tem cache por processo, sem invalidação distribuída.
4. Com todos os escritores parados e a borda fechada, persistir fence para Go e checkpoint da pausa. A registry atual só tem `go`/`legacy`; `legacy` aqui significa **Go recusado**, não autorização de tráfego Node. A borda e o processo Node precisam continuar parados. Ao reiniciar a guarda, o fence deve permanecer efetivo. O ensaio comprova essa recarga; não implementa um gateway real.
5. Adquirir exclusividade para a administração, registrar último checkpoint confirmado e produzir **backup posterior às escritas**, preservando dados/arquivos/config/plugins e filas. Reconciliar operações de resultado desconhecido por chave de idempotência e provedor, não por repetição cega. A transação abortada não deixa linha/chave/evento, mas pode consumir sequence.

## Restaurar e decidir

Os comandos abaixo são para uma instalação **Go gerenciada**, parada e identificada; os caminhos são variáveis que o operador deve definir para sua onda. DSN fica no ambiente protegido, nunca no histórico de argv. `RECOVERY_DIR` e banco de destino devem ser novos/vazios.

```bash
"$RELEASE_DIR/bin/cli" backup --dir "$INSTANCE_DIR" --output "$FINAL_BACKUP_DIR"
# SALTCORN_GO_DATABASE_URL aponta agora para o NOVO banco vazio de recuperação.
"$RELEASE_DIR/bin/cli" restore --dir "$RECOVERY_DIR" --backup "$FINAL_BACKUP_DIR"
"$RELEASE_DIR/bin/cli" check --dir "$RECOVERY_DIR"
```

**`check`/health verde não basta:** o ensaio restaura o backup anterior ao corte; ele é compatível e saudável, porém seu checksum diverge porque perde insert/update/delete Go. Esse candidato deve ser recusado. Conferir também:

- IDs, FKs, contagens, hashes completos, metadados/config, arquivos e plugins comparados ao checkpoint final congelado, não ao baseline anterior às escritas;
- chaves/resultados idempotentes e outbox: no ensaio, três eventos ficam pendentes e retry da escrita confirmada não repete efeito/evento;
- high-water mark de sequence preservado inclusive para ID abortado (302), evitando reuso indevido;
- schema/contratos aceitos pelo leitor escolhido; versão futura e backup corrompido recusados antes de promoção;
- nenhum outro escritor autorizado no primário antigo ou no destino restaurado.

Se qualquer comparação falhar ou ultrapassar RPO/RTO: manter pausa, preservar os dois bancos e evidências, investigar delta/versão e reconstruir em **outro destino vazio**. Nunca restaurar por cima da origem para esconder divergência. Um reset/novo backup não apaga a falha anterior.

O candidato Go só pode voltar a admitir após as verificações, autorização operacional da onda e troca controlada de owner, com primário antigo ainda cercado. O teste exercita a guarda, não reabre tráfego real. **Volta a Node exige prova adicional do leitor/contrato/plugin legado contra esse mesmo checkpoint.** Sem essa prova, a alternativa ensaiada é permanecer pausado/reconciliar e recuperar Go; não há “botão de rollback” seguro para Node.

## Encerrar janela e retomar interrupções

Contração só depois de declarar a janela encerrada, retirar a opção de retorno incompatível e preservar backups pelo prazo da onda. No teste, DROP da coluna de compatibilidade é recusado enquanto a janela está aberta e permitido apenas no candidato recuperado após fechamento explícito; a origem e primário congelado permanecem intactos.

| Checkpoint persistido | Retomada segura |
| --- | --- |
| `source_captured` / `expanded_and_reconciled` | Confirmar origem ainda congelada e hashes/versões; não copiar novamente por cima de destino alterado |
| `go_writes_confirmed` | Identificar escritor, commits e eventos reais; iniciar pausa/dreno antes de qualquer volta |
| `paused_fenced` | Conferir borda/processos/locks de todos os lados; manter fence após reinício; capturar checkpoint final |
| `recovery_backup_complete` | Validar manifesto e último checkpoint; restaurar em destino vazio, preservando tentativa anterior |
| `recovered_go_validated_legacy_blocked` | Revalidar que nenhuma escrita ocorreu após a captura; Node segue bloqueado; decidir retomada Go conforme onda |
| `complete` | Conferir resultado/versão/artefatos; não inferir deploy autorizado |

O runner escreve checkpoint por rename atômico. Se o teste for interrompido, conferir bancos/conexões remanescentes de seu container antes de limpar; usar nova saída de evidência para repetir. Os bancos sintéticos são descartáveis e removidos pelo cleanup normal; um checkpoint de teste não é um backup operacional restaurável. Em uma onda real, guardar separadamente os backups privados e os IDs de bancos/instâncias/processos, sem publicar credenciais.
