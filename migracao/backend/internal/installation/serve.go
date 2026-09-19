package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type ReleaseManifest struct {
	Format      int               `json:"format"`
	Version     string            `json:"version"`
	Schema      int               `json:"schema"`
	InternalAPI int               `json:"internal_api"`
	BFFAPI      int               `json:"bff_api"`
	NodeMajor   int               `json:"node_major"`
	Files       map[string]string `json:"sha256"`
}

func CheckRelease(dir string) error {
	var m ReleaseManifest
	b, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.Format != 1 || m.Version != Release || m.Schema != SchemaVersion || m.InternalAPI != 1 || m.BFFAPI != 1 || m.NodeMajor != 22 {
		return errors.New("installation: componentes da release incompatíveis")
	}
	for _, file := range []string{"bin/server", "bin/worker", "bff/dist/src/server.js", "web/index.html", "web/vendor/sbadmin2/sb-admin-2.min.css", "web/vendor/builder_bundle.js"} {
		if _, err = os.Stat(filepath.Join(dir, file)); err != nil {
			return err
		}
	}
	if len(m.Files) == 0 {
		return errors.New("installation: release sem hashes")
	}
	for name, expected := range m.Files {
		clean := filepath.Clean(name)
		if filepath.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("installation: caminho inválido no manifesto")
		}
		f, err := os.Open(filepath.Join(dir, clean))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != expected {
			return fmt.Errorf("installation: componente corrompido: %s", name)
		}
	}
	for _, name := range []string{"bin/cli", "bin/server", "bin/worker", "bff/dist/src/server.js", "web/index.html"} {
		if m.Files[name] == "" {
			return errors.New("installation: componente sem hash")
		}
	}
	output, err := exec.Command("node", "--version").Output()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(output), "v22.") {
		return errors.New("installation: runtime Node 22 obrigatório nesta release")
	}
	return nil
}
func loopback(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return errors.New("installation: endereço HTTP inválido")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("installation: bind deve ser loopback; exponha por proxy HTTPS")
	}
	return nil
}
func Probe(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("installation: health retornou %d", resp.StatusCode)
	}
	return nil
}

// Serve holds the administration lock throughout the lifetime of all children.
// Unexpected child exit terminates the whole group; TERM is followed by a
// bounded wait before KILL, and every child is reaped before releasing locks.
func Serve(ctx context.Context, root, release string, c Config, lease *Lock) error {
	if c.Driver != "postgres" {
		return errors.New("installation: serve web exige PostgreSQL; SQLite suporta administração do perfil metadata/records/outbox/sync")
	}
	if err := CheckRelease(release); err != nil {
		return err
	}
	if err := Check(ctx, root, c); err != nil {
		return err
	}
	if err := loopback(c.HTTPAddr); err != nil {
		return err
	}
	if err := loopback(c.BackendAddr); err != nil {
		return err
	}
	if c.HTTPAddr == c.BackendAddr {
		return errors.New("installation: portas de BFF e backend devem ser diferentes")
	}
	env := append(os.Environ(), "SALTCORN_GO_DATABASE_URL="+c.DatabaseURL, "SALTCORN_GO_SERVICE_IDENTITY_SECRET="+c.Secret, "SALTCORN_GO_HTTP_ADDR="+c.BackendAddr, "SALTCORN_GO_FILES_ROOT_DIR="+filepath.Join(root, "files"), "SALTCORN_GO_WORKER_TENANTS="+c.Tenant, "SALTCORN_BFF_HTTP_ADDR="+c.HTTPAddr, "SALTCORN_BFF_SERVICE_IDENTITY_SECRET="+c.Secret, "SALTCORN_BFF_GO_INTERNAL_API_URL=http://"+c.BackendAddr, "SALTCORN_BFF_WEB_ROOT="+filepath.Join(release, "web"), "SALTCORN_BFF_INSTALLATION_TENANT="+c.Tenant, "SALTCORN_BFF_INSTALLATION_ID="+c.ID)
	// The BFF receives delegated-identity configuration, never database or SMTP
	// credentials. Its only domain transport is the internal HTTP API.
	bffEnv := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, "SALTCORN_GO_") && !strings.HasPrefix(entry, "PG") {
			bffEnv = append(bffEnv, entry)
		}
	}
	commands := []*exec.Cmd{exec.Command(filepath.Join(release, "bin/server")), exec.Command("node", filepath.Join(release, "bff/dist/src/server.js")), exec.Command(filepath.Join(release, "bin/worker"))}
	exited := make(chan error, len(commands))
	started := 0
	defer func() {
		for _, cmd := range commands[:started] {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		for started > 0 {
			select {
			case <-exited:
				started--
			case <-deadline.C:
				for _, cmd := range commands {
					if cmd.Process != nil {
						_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					}
				}
				deadline.Reset(5 * time.Second)
			}
		}
	}()
	for i, cmd := range commands {
		cmd.Env = env
		if i == 1 {
			cmd.Env = bffEnv
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		started++
		go func(cmd *exec.Cmd) { exited <- cmd.Wait() }(cmd)
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ready := false
	deadline := time.Now().Add(30 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-exited:
			// Put the consumed exit back so cleanup accounts for every started child.
			exited <- err
			return fmt.Errorf("installation: processo filho encerrou inesperadamente: %v", err)
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			err := lease.Ping(probeCtx)
			if err == nil {
				err = Probe(probeCtx, "http://"+c.BackendAddr+"/readyz")
			}
			if err == nil {
				err = Probe(probeCtx, "http://"+c.HTTPAddr+"/readyz")
			}
			cancel()
			if err == nil {
				if !ready {
					fmt.Println("instância pronta em http://" + c.HTTPAddr)
					ready = true
				}
			} else if ready || time.Now().After(deadline) {
				return errors.New("installation: dependência indisponível; encerrando serviços")
			}
		}
	}
}
