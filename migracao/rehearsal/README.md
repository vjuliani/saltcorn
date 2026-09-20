# GO-034 — Ensaio de migração e recuperação

O teste `TestMigrationPauseReconcileRecovery` executa uma cópia **sintética** de duas tabelas relacionadas para uma instalação Go, aplica escritas e ensaia a recuperação após pausa. Não é um importador genérico de backup legado nem uma ferramenta para cortar tráfego de produção. Procedimento operacional e limites no [runbook](../../docs/migracao-go/recuperacao/RUNBOOK.md).

Requer Linux, Go 1.22+, Python 3.10+, PostgreSQL 16 e `pg_dump`/`pg_restore` 16. A fixture usa autores 10/40 e livros 100/300 para detectar remapeamento acidental de IDs/FKs e sequences. O catálogo de destino vem de pack v1; usuários, páginas, plugins arbitrários e outros formatos legados não são importados.

```bash
docker --context default run -d --name saltcorn-go034-pg \
  -p 127.0.0.1:55575:5432 -e POSTGRES_PASSWORD=go034_test_only \
  -e POSTGRES_DB=go034_rehearsal postgres:16
# Aguarde pg_isready antes de executar.
export SALTCORN_GO_REHEARSAL_DISPOSABLE=1
export SALTCORN_GO_TEST_INSTALLATION_DATABASE_URL='postgres://postgres:go034_test_only@127.0.0.1:55575/go034_rehearsal'
python3 migracao/rehearsal/run.py --output /tmp/go034-rehearsal-01
```

O runner exige cluster exclusivo em loopback, banco com nome reservado e diretório de saída novo fora do checkout. Os testes criam bancos próprios com nomes aleatórios (`go032_*`, helper de instalação existente), removidos via cleanup; nunca apontar o teste diretamente a uma origem real. Não reutilizar a porta/volume da prévia. Depois do ensaio, remover somente o container criado: `docker --context default rm -fv saltcorn-go034-pg`.

Artefatos: `checkpoint.json` (fase atual, dados sintéticos, hashes, contagens e medidas), `report.json` (comando/versões/exit), `inputs.json` (árvore exata) e `tests.log` (eventos Go). Nenhum `instance.json`, senha ou backup privado é publicado. A CI publica o artefato `go034-recovery-evidence`. Todos os testes selecionados devem passar, sem skips; o teste principal deve aparecer e terminar em `complete`.

Critérios do laboratório: **RPO zero transações confirmadas** e **RTO ≤60s**, medido desde início da pausa/drenagem até candidato Go restaurado, reconciliado e admitido pela guarda. O ensaio é pequeno e a origem continua disponível para backup final; não mede indisponibilidade de um desastre, DNS/proxy, aplicação real ou SLO de produção. O contador de sequence inclui o ID consumido pela transação abortada (302), embora a maior linha confirmada seja 301.

A validação usa APIs reais Go de domínio, catálogo, pack, ownership, locks e backup/restore PostgreSQL. Não sobe novamente toda a UI/HTTP, já coberta pela GO-033; não atesta o leitor Node legado. `legacy_traffic_allowed` permanece **false**. O ensaio prova o caminho de pausa/reconciliação/recuperação em Go previsto no aceite da tarefa, mantendo o retorno legado condicionado à validação independente de schema/dados/plugins.
