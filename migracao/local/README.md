# Execução local da migração

How-to completo: [docs/migracao-go/EXECUCAO-LOCAL.md](../../docs/migracao-go/EXECUCAO-LOCAL.md).

Na raiz do repositório, com Node 22, Go 1.22 e Docker Compose:

```bash
migracao/local/local.sh setup
migracao/local/local.sh up
```

Em outro terminal, execute `migracao/local/local.sh login`, abra o link administrativo e depois http://localhost:5173 para desenvolver o frontend. Ctrl+C encerra a aplicação; `db-stop` para o banco preservando seus dados. Consulte o how-to antes de alterar a configuração de uma instância existente.

Para executar toda a aplicação em containers, com Docker Compose e Bash:

```bash
migracao/local/docker.sh up
migracao/local/docker.sh login
```

Abra o link gerado (porta padrão 5180). `docker.sh logs` acompanha os logs; `docker.sh down` remove os containers preservando os volumes. O [how-to](../../docs/migracao-go/EXECUCAO-LOCAL.md#alternativa-aplicação-inteira-com-docker-compose) detalha configuração, persistência e recompilação. Este modo usa uma instância e um banco separados de `local.sh`.
