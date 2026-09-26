// Criptografia de backup (GO-046, CAP-081) — o backup produzido por
// Backup() é um DIRETÓRIO em texto claro; EncryptArchive empacota esse
// diretório (tar+gzip, biblioteca padrão) e cifra o resultado como um
// ÚNICO arquivo, com AES-256-GCM (autenticado — qualquer adulteração é
// detectada na descriptografia, nunca silenciosamente aceita) e uma
// chave derivada da senha via scrypt (parâmetros N=2^15/r=8/p=1,
// equivalentes aos "interativos" recomendados por golang.org/x/crypto/
// scrypt — mais forte que a senha de zip do legado, que usa o esquema
// fraco e amplamente quebrado do PKZIP tradicional).
//
// Formato do arquivo cifrado: [salt 16 bytes][nonce 12 bytes][ciphertext
// GCM]. Nenhum metadado adicional — o próprio manifest.json (dentro do
// tar, cifrado junto) continua sendo a fonte de verdade de compatibilidade
// de formato/schema, exatamente como no backup em texto claro.
package installation

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32 // AES-256
	saltLen      = 16
	gcmNonceLen  = 12 // tamanho de nonce padrão de cipher.NewGCM
)

func deriveKey(passphrase, salt []byte) ([]byte, error) {
	return scrypt.Key(passphrase, salt, scryptN, scryptR, scryptP, scryptKeyLen)
}

// archiveDir empacota src (um diretório) em dst (tar+gzip) — só arquivos
// regulares e diretórios, mesma recusa de link/especial de copyTree.
func archiveDir(src, dst string) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			return errors.New("installation: arquivamento recusa links e arquivos especiais")
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// unarchiveDir reverte archiveDir — dst deve ser um diretório novo/vazio
// (mesma disciplina de Restore: nunca sobrescreve dado existente).
func unarchiveDir(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
			return errors.New("installation: caminho inválido no arquivo")
		}
		target := filepath.Join(dst, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			closeErr := out.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return errors.New("installation: entrada de arquivo não suportada")
		}
	}
}

// encryptFile cifra src (arquivo) para dst com AES-256-GCM, chave derivada
// de passphrase por scrypt com um salt aleatório novo por arquivo.
func encryptFile(src, dst string, passphrase []byte) error {
	plaintext, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.Write(salt); err != nil {
		return err
	}
	if _, err := out.Write(nonce); err != nil {
		return err
	}
	if _, err := out.Write(ciphertext); err != nil {
		return err
	}
	return out.Sync()
}

// decryptFile reverte encryptFile — falha explícita (nunca dado
// silenciosamente corrompido) se a senha estiver errada ou o arquivo
// tiver sido adulterado, graças à autenticação do GCM.
func decryptFile(src, dst string, passphrase []byte) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if len(data) < saltLen+gcmNonceLen {
		return errors.New("installation: arquivo cifrado corrompido ou truncado")
	}
	salt, nonce, ciphertext := data[:saltLen], data[saltLen:saltLen+gcmNonceLen], data[saltLen+gcmNonceLen:]
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return errors.New("installation: senha incorreta ou backup cifrado adulterado")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := out.Write(plaintext); err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// BackupEncrypted produz um backup completo (mesma cobertura de Backup)
// como um ÚNICO arquivo cifrado em dest, nunca um diretório em texto
// claro. Usa um diretório de staging temporário (fora de root e de dest)
// para o backup em texto claro intermediário, sempre removido ao final —
// mesmo em caso de erro.
func BackupEncrypted(ctx context.Context, root string, c Config, dest string, passphrase []byte) error {
	if len(passphrase) == 0 {
		return errors.New("installation: senha de criptografia obrigatória")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return errors.New("installation: destino do backup deve ser novo")
	}
	stageDir, err := os.MkdirTemp(filepath.Dir(dest), ".backup-plain-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)
	plainBackup := filepath.Join(stageDir, "backup")
	if err := Backup(ctx, root, c, plainBackup); err != nil {
		return err
	}
	archive := filepath.Join(stageDir, "backup.tar.gz")
	if err := archiveDir(plainBackup, archive); err != nil {
		return err
	}
	return encryptFile(archive, dest, passphrase)
}

// RestoreEncrypted reverte BackupEncrypted — decifra e desempacota source
// num diretório de staging temporário, depois delega a Restore.
func RestoreEncrypted(ctx context.Context, root, source string, passphrase []byte, dsn string) (Config, error) {
	if len(passphrase) == 0 {
		return Config{}, errors.New("installation: senha de criptografia obrigatória")
	}
	stageDir, err := os.MkdirTemp(filepath.Dir(root), ".restore-plain-*")
	if err != nil {
		return Config{}, err
	}
	defer os.RemoveAll(stageDir)
	archive := filepath.Join(stageDir, "backup.tar.gz")
	if err := decryptFile(source, archive, passphrase); err != nil {
		return Config{}, err
	}
	plainBackup := filepath.Join(stageDir, "backup")
	if err := unarchiveDir(archive, plainBackup); err != nil {
		return Config{}, err
	}
	return Restore(ctx, root, plainBackup, dsn)
}
