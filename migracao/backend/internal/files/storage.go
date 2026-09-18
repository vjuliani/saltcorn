package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// uploadingSuffixPrefix marca um arquivo de staging — nunca um objeto
// "real" (CleanupOrphans só varre nomes que contêm este marcador).
const uploadingSuffixPrefix = ".uploading."

// Backend é o armazenamento físico de bytes de arquivo — só LocalBackend é
// implementado nesta tarefa. Um backend S3 fica deliberadamente fora de
// escopo: o próprio legado marca S3 como "experimental" na UI
// (packages/server/routes/files.ts, blurb de aviso) e não há ADR ou
// menção no plano de arquitetura da migração exigindo S3 para o piloto —
// a interface existe para permitir um backend adicional futuro sem mudar
// nenhum chamador, mas nenhuma implementação S3 é fabricada aqui.
type Backend interface {
	// Save grava o conteúdo de r sob key, devolvendo o tamanho em bytes.
	// Nunca deixa um arquivo PARCIAL visível sob key: escreve num nome de
	// staging e só o promove (rename atômico) se r for consumido até EOF
	// sem erro — um upload interrompido (r retorna erro, ex.: cliente
	// desconectou) limpa o staging IMEDIATAMENTE, síncrono, dentro desta
	// chamada.
	Save(ctx context.Context, key string, r io.Reader) (size int64, err error)
	// Open abre key para leitura — ErrNotFound se não existir.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete remove key — nunca erra se key já não existir (idempotente).
	Delete(ctx context.Context, key string) error
}

// LocalBackend implementa Backend sobre um diretório do sistema de
// arquivos local.
type LocalBackend struct {
	RootDir string
}

func NewLocalBackend(rootDir string) *LocalBackend {
	return &LocalBackend{RootDir: rootDir}
}

func (b *LocalBackend) Save(ctx context.Context, key string, r io.Reader) (int64, error) {
	if err := os.MkdirAll(b.RootDir, 0o755); err != nil {
		return 0, fmt.Errorf("files: preparar diretório de armazenamento: %w", err)
	}
	finalPath := filepath.Join(b.RootDir, key)
	tmpPath := finalPath + uploadingSuffixPrefix + randomSuffix()

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("files: criar arquivo de staging: %w", err)
	}

	// cleanupStaging cobre TODO caminho de saída que não seja o rename
	// final bem-sucedido — upload interrompido (io.Copy falha), corpo
	// cancelado (ctx encerrado), ou falha ao fechar/renomear. Nenhum
	// desses caminhos deixa um arquivo de staging para trás: a limpeza é
	// síncrona, dentro desta mesma chamada, não depende de uma varredura
	// posterior (essa, CleanupOrphans, só é a rede de segurança para uma
	// queda ABRUPTA do processo no meio do caminho, que nenhum defer Go
	// consegue interceptar).
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = f.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	n, err := io.Copy(f, r)
	if err != nil {
		return 0, fmt.Errorf("files: upload interrompido: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("files: fechar arquivo de staging: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return 0, fmt.Errorf("files: finalizar upload: %w", err)
	}
	cleanupStaging = false
	return n, nil
}

func (b *LocalBackend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Join(b.RootDir, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

func (b *LocalBackend) Delete(ctx context.Context, key string) error {
	err := os.Remove(filepath.Join(b.RootDir, key))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// CleanupOrphans varre RootDir por arquivos de staging mais antigos que
// olderThan e os remove — a rede de segurança contra uma queda de
// processo NO MEIO de Save (o defer de Save já cobre todo erro
// "normal"; isto cobre um kill -9/queda de energia, que nenhum defer
// intercepta). Nunca remove um arquivo de staging mais NOVO que
// olderThan — um upload legitimamente em andamento nunca é confundido
// com um órfão, mesmo que a varredura rode no meio de um upload real e
// lento.
func (b *LocalBackend) CleanupOrphans(olderThan time.Duration) (removed int, err error) {
	entries, err := os.ReadDir(b.RootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries {
		if e.IsDir() || !strings.Contains(e.Name(), uploadingSuffixPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(b.RootDir, e.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// NewStorageKey gera uma chave de armazenamento aleatória — nunca o nome
// de arquivo enviado pelo usuário (evita colisão e travessia de caminho;
// o nome original fica só em File.Filename, metadado, nunca usado para
// endereçar o backend físico).
func NewStorageKey() string {
	return randomSuffix() + randomSuffix()
}

func randomSuffix() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
