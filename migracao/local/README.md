# Execução local da migração

How-to completo: [docs/migracao-go/EXECUCAO-LOCAL.md](../../docs/migracao-go/EXECUCAO-LOCAL.md).

Na raiz do repositório, com Node 22, Go 1.22 e Docker Compose:

```bash
migracao/local/local.sh setup
migracao/local/local.sh up
```

Em outro terminal, execute `migracao/local/local.sh login`, abra o link administrativo e depois http://localhost:5173 para desenvolver o frontend. Ctrl+C encerra a aplicação; `db-stop` para o banco preservando seus dados. Consulte o how-to antes de alterar a configuração de uma instância existente.
