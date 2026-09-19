package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/installation"
)

func installationCommand(command string, args []string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	dir := fs.String("dir", "", "diretório privado da instância (obrigatório)")
	driver := fs.String("driver", "postgres", "postgres ou sqlite (setup)")
	tenant := fs.String("tenant", "app", "tenant (setup)")
	email := fs.String("email", "", "e-mail do administrador (setup)")
	passwordFile := fs.String("password-file", "", "arquivo privado com senha inicial (setup)")
	output := fs.String("output", "", "novo diretório de backup")
	source := fs.String("backup", "", "diretório do backup (restore)")
	release := fs.String("release", "", "diretório do pacote (serve)")
	key := fs.String("key", "", "chave de configuração")
	value := fs.String("value", "", "valor JSON (set-cfg)")
	httpAddr := fs.String("http", "127.0.0.1:3100", "bind local do BFF (setup)")
	backendAddr := fs.String("backend-http", "127.0.0.1:8090", "bind local do Go (setup)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *dir == "" {
		return errors.New("--dir é obrigatório; argumentos posicionais não são aceitos")
	}
	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// login only reads protected local configuration and can be used while serve runs.
	if command == "login" {
		c, err := installation.Load(root)
		if err != nil {
			return err
		}
		if c.Driver != "postgres" || c.AdminID <= 0 {
			return errors.New("login exige perfil web PostgreSQL")
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"aud": "saltcorn-cli-login", "iss": c.ID, "sub": strconv.Itoa(c.AdminID), "tenant": c.Tenant, "jti": installation.RandomID(), "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix()})
		signed, err := token.SignedString([]byte(c.Secret))
		if err != nil {
			return err
		}
		loginAddr := strings.Replace(c.HTTPAddr, "127.0.0.1:", "localhost:", 1)
		fmt.Println("http://" + loginAddr + "/login#" + signed)
		return nil
	}
	lock, err := installation.Acquire(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	if command == "restore" {
		if *source == "" {
			return errors.New("--backup obrigatório")
		}
		_, err = installation.Restore(ctx, root, *source, os.Getenv("SALTCORN_GO_DATABASE_URL"))
		return err
	}
	var c installation.Config
	if command == "setup" {
		if _, err = os.Stat(filepath.Join(root, "instance.json")); !os.IsNotExist(err) {
			return errors.New("instância existente; use migrate")
		}
		// Pending config makes setup recoverable if the process dies after DB commit.
		pending := filepath.Join(root, "setup.pending.json")
		if b, e := os.ReadFile(pending); e == nil {
			if err = json.Unmarshal(b, &c); err != nil {
				return err
			}
		} else {
			entries, readErr := os.ReadDir(root)
			if readErr != nil {
				return readErr
			}
			for _, entry := range entries {
				if entry.Name() != ".operation.lock" {
					return errors.New("setup exige diretório novo/vazio ou setup.pending.json válido")
				}
			}
			c = installation.Config{Format: 1, ID: installation.RandomID(), Driver: *driver, Tenant: *tenant, DatabaseURL: os.Getenv("SALTCORN_GO_DATABASE_URL"), Secret: installation.RandomID(), HTTPAddr: *httpAddr, BackendAddr: *backendAddr}
			if err = c.Validate(); err != nil {
				return err
			}
			if err = installation.WriteJSON(pending, c); err != nil {
				return err
			}
		}
		if c.Driver == "postgres" && (*email == "" || *passwordFile == "") {
			return errors.New("setup PostgreSQL exige --email e --password-file")
		}
		if c.Driver == "sqlite" {
			*email = "desktop"
			c.DatabaseURL = ""
		}
		password := ""
		if *passwordFile != "" {
			b, e := os.ReadFile(*passwordFile)
			if e != nil {
				return e
			}
			password = strings.TrimSuffix(string(b), "\n")
			if len(password) < 12 {
				return errors.New("senha inicial deve ter pelo menos 12 caracteres")
			}
		}
		if err = lock.PostgresLease(ctx, c); err != nil {
			return err
		}
		if err = installation.PrepareDirs(root); err != nil {
			return err
		}
		if err = installation.Migrate(ctx, root, &c, *email, password); err != nil {
			return err
		}
		if err = installation.WriteJSON(filepath.Join(root, "instance.json"), c); err != nil {
			return err
		}
		_ = os.Remove(pending)
		fmt.Println("instalação preparada; schema", installation.SchemaVersion)
		return nil
	}
	c, err = installation.Load(root)
	if err != nil {
		return err
	}
	if err = lock.PostgresLease(ctx, c); err != nil {
		return err
	}
	if command != "migrate" {
		if err = installation.Check(ctx, root, c); err != nil {
			return err
		}
	}
	switch command {
	case "migrate":
		if err = installation.Migrate(ctx, root, &c, "", ""); err == nil {
			err = installation.WriteJSON(filepath.Join(root, "instance.json"), c)
		}
		return err
	case "backup":
		if *output == "" {
			return errors.New("--output obrigatório")
		}
		return installation.Backup(ctx, root, c, *output)
	case "check":
		return installation.Check(ctx, root, c)
	case "get-cfg", "set-cfg":
		result, e := installation.ConfigValue(ctx, root, c, *key, *value, command == "set-cfg")
		if e == nil && command == "get-cfg" {
			fmt.Println(result)
		}
		return e
	case "plugins":
		return installation.SyncPlugins(ctx, root, c)
	case "serve":
		if *release == "" {
			exe, e := os.Executable()
			if e != nil {
				return e
			}
			*release = filepath.Dir(filepath.Dir(exe))
		}
		abs, e := filepath.Abs(*release)
		if e != nil {
			return e
		}
		return installation.Serve(ctx, root, abs, c, lock)
	}
	return errors.New("comando desconhecido")
}
