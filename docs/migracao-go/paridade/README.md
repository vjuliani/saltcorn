# GO-033 — Paridade consolidada

A matriz cobre as **70 capacidades do inventário GO-001**. Consulte o [resultado por capacidade](RESULTADOS.md), o [relatório estruturado](report.json) e os [hashes dos inputs validados](inputs.json). Os comandos, instalação da fixture e regras de retomada estão no [runner](../../../migracao/parity/README.md); histórico em [GO-033](../execucoes/GO-033.md).

Há dois resultados distintos:

- **Auditoria de evidências:** todas as oito suítes precisam passar, com testes executados e sem skips de dependências. Evidência faltante faz a CI falhar.
- **Paridade do produto:** uma capacidade parcial/ausente bloqueia seu perfil. Os perfis piloto e integral permanecem bloqueados, mesmo com as suítes aprovadas. A CI publica essa decisão, sem autorizar promoção ou retirada do legado.

As 48 capacidades selecionadas conservadoramente para o piloto incluem sua infraestrutura web/admin. Destas, 39 ainda têm lacunas; das 70 integrais, 60 têm lacunas. Os números contam linhas da matriz, não bugs independentes: uma integração ausente pode afetar várias capacidades. `PASS` cobre o escopo de evidência escrito na linha, sem extrapolação para comportamento legado não testado.

Prioridades antes do piloto: conectar automação ao HTTP, entregar formulários/templates usados pela aplicação, completar login de usuários e persistência do canvas, validar a aplicação guitars com substituições propostas de plugins. Antes da migração integral: incluir SQLite web/domínios restantes, plataformas mobile nativas, contratos/plugins de produção e demais linhas pendentes. Ensaios de dados/rollback, carga e canário continuam nas GO-034/035/036.

O relatório versionado registra uma execução local. Cada PR que afeta a matriz executa novamente o workflow `migracao-parity-ci`, que publica logs completos e relatório no artefato `go033-parity-evidence`, vinculado ao SHA daquela execução. O resumo local não substitui a CI de código posterior.
