# GO-034 — Evidência de laboratório

Execução final em 2026-09-20T11:42:51.370032191Z, base `c5c37b7a5d337ae58bc8000c48fc65e109b8d100` mais inputs identificados por SHA-256 `7cc4d4f74613fa7b488d284d4d954ffb04e65a079400889e46bed38d5de9b4a4`. [Hashes da árvore validada](inputs.json), [comando/ambiente/exit](report.json), [checkpoints e dados sintéticos](checkpoint.json), [runbook](RUNBOOK.md).

| Critério | Resultado |
| --- | --- |
| Suítes instalação, cutover, pack e CLI | 36 testes/subtestes PASS com race; zero falhas/skips; exit 0 |
| RPO do ensaio | 0 das 3 transações confirmadas perdidas; snapshot recuperado idêntico ao pós-escritas Go |
| RTO do ensaio | 1.483s, abaixo de 60s; pausa/dreno → backup final → restore → reconciliação → guarda Go validada |
| Dados e relações | Origem e destino com 2 autores/2 livros; IDs 10/40 e 100/300; depois Go mantém 100, cria 301 e remove 300; FK preservada |
| Sequence | High-water mark 302 preservado, incluindo consumo por transação abortada |
| Estado de efeitos | 3 chaves confirmadas/3 eventos preservados; replay não repete efeito; evento externo não despachado |
| Expansão/contração | Schema 1→2 repetível; DROP de coluna recusado na janela, permitido somente no candidato após fechamento |
| Exclusividade/retomada | Segundo executor recusado; timeout de dreno mantém pausa; fence persiste após recarga |
| Negativos de recuperação | Backup antigo saudável mas divergente, backup corrompido e schema futuro recusados para promoção |
| Origem | Hash antes/depois idêntico: `38fbf961abadd99f2358a92427cf8f65a8114a7eaae2656e97baed3d4de16a1d` |
| Retorno legado | **BLOQUEADO**; leitor Node/schema/plugins de produção não certificados |

Resultado válido para fixture sintética pequena com origem disponível. Não mede recuperação após perda do cluster, startup HTTP/UI, tráfego real ou SLO de produção. A decisão ensaiada é pausa/reconciliação e candidato Go recuperado; não há deploy ou migração integral declarada.

Coleta inicial `/tmp/go034-rehearsal-01`: PASS, RTO 1,874s. Coleta final `/tmp/go034-rehearsal-02` repetida após revisão de contagens/high-water mark e exclusividade do candidato; ambas preservadas. Logs completos locais; workflow publica nova evidência por SHA em `go034-recovery-evidence`.
