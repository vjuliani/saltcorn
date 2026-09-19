# ADR-0012 — Instalação e recuperação self-hosted

- Status: proposta implementada na GO-032, sujeita à revisão do PR.
- Contexto: server/worker/CLI já existiam, mas só havia seed destrutivo de E2E; schemas eram preparados externamente e não havia pacote operacional com BFF/frontend nem backup completo.

## Decisão

Distribuir uma pasta Linux com binários Go, BFF Node 22 e assets React/SB Admin 2. O build usa lockfiles em árvore isolada. Manifesto declara release, schema, versões de contratos, Node e hashes dos componentes; `serve` recusa mistura/corrupção antes de iniciar os processos.

Separar release imutável e diretório privado da instância. A CLI usa serviços internos compartilhados para setup de catálogo, configuração e migrations. PostgreSQL é o perfil web; SQLite suporta administração e recuperação do subconjunto metadata/records/outbox/sync já portado. O adapter HTTP inteiro ainda não aceita SQLite; não inferir paridade de todo o produto a partir do driver.

Usar banco PostgreSQL vazio/dedicado no setup/restore; recusar adoção de legado ou sobrescrita de instância existente. Um journal por tenant registra migrations aditivas transacionais, protegidas por advisory lock. UUID da instância vincula configuração e banco. Setup interrompido conserva configuração pendente para retomada. Upgrades preservam ownership existente e versões futuras bloqueiam downgrade.

Backups offline incluem banco completo, arquivos locais, configuração privada, pacotes de plugins e inventário de versões. Lock de arquivo e advisory lock PostgreSQL serializam administração/serve gerenciados. Escritores externos precisam ser encerrados pelo operador. Restore valida cópia em staging, hashes, formato, schema e plugins, exige destino vazio e usa transação única no PostgreSQL. Falha entre commit de banco e cópia final de arquivos requer nova restauração em outro destino; não há transação distribuída entre FS e banco.

O supervisor mantém Go, worker e BFF; sinal encerra todos, saída inesperada de filho ou perda de dependência também encerra o conjunto. BFF serve assets na mesma origem e consulta readiness do Go. Bind é local; proxy HTTPS e eventual SMTP são dependências operacionais declaradas, não embutidas no BFF.

Para o primeiro acesso, o operador com acesso ao diretório privado emite ticket administrativo via CLI, válido por 60 segundos, com audience/issuer próprios. O BFF exige Origin, consulta papel atual no Go e cria sessão/CSRF conforme ADR-0007; o ticket não entra em query string e seu jti não pode ser reutilizado no mesmo processo. Sessões em memória exigem novo acesso após restart. Isso não porta estratégias de login de usuários finais nem de plugins.

## Consequências e limites

- Backup contém segredos e código de plugins: acesso/transporte privados. SHA-256 detecta corrupção, não autentica backup malicioso. Somente backups/releases confiáveis podem ser restaurados/executados.
- Inventário/restauração de plugin não autoriza sua execução no runtime Go. Plugins incompatíveis continuam dependências do legado/host temporário; a release não habilita capacidades não portadas.
- O cliente mobile usa o mesmo BFF/sync versionado e conserva runtime JS/Capacitor. Arquivos SQLite de aparelhos não são dados do servidor; builds e assinaturas nativas não foram alterados nesta tarefa.
- `fixtures`, tenant/user CRUD geral, execução administrativa arbitrária de triggers e mobile-build do CLI legado permanecem fora dos comandos delimitados por GO-032; não declarar migração integral do CLI legado.
- Build/testes/upgrade/restore são evidências desta tarefa; merge/deploy e ondas de corte não são automáticos.

Runbook: [migracao/distribution/README.md](../../../migracao/distribution/README.md).
