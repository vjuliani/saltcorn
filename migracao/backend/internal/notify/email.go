package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// defaultSMTPTimeout limita a conexão TCP inicial — o legado
// (nodemailer) usa smtp_timeout_seconds (default 120s); um valor mais
// curto aqui é deliberado (subconjunto prioritário: falha rápido,
// deixando o retry do outbox, GO-014, cuidar de tentar de novo, em vez
// de segurar um worker inteiro por 2 minutos numa conexão travada).
const defaultSMTPTimeout = 30 * time.Second

// SMTPConfig é a configuração mínima de envio — equivalente reduzido de
// getMailTransport() do legado (models/email.ts). SEM suporte a OAuth2
// (smtp_auth_method="oauth2" do legado) — fora do subconjunto
// prioritário desta tarefa, autenticação PLAIN/LOGIN via usuário+senha
// cobre o caminho comum.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// InsecureSkipVerify replica smtp_allow_self_signed do legado — nunca
	// o padrão (zero value é false = verifica certificado).
	InsecureSkipVerify bool
}

// EmailMessage é um e-mail de texto simples ou HTML (GO-046, CAP-083).
// HTMLBody vazio mantém o comportamento original (texto puro); HTMLBody
// preenchido gera um corpo multipart/alternative com AMBAS as partes —
// texto (Body, se vazio um fallback simples derivado do assunto) e HTML
// (HTMLBody) — mesmo formato que qualquer cliente de e-mail real espera
// para escolher a melhor representação disponível. Nenhum motor MJML
// (o legado usa MJML como linguagem intermediária compilada para HTML via
// o pacote `mjml`, sem equivalente Go): HTMLBody já é HTML final, pronto
// para enviar — quem monta esse HTML (ex.: internal/views, GO-046) decide
// o próprio layout responsivo.
type EmailMessage struct {
	To       []string
	Subject  string
	Body     string
	HTMLBody string
}

// SendEmail conecta, faz STARTTLS se o servidor anunciar suporte, autentica
// se Username não for vazio, e envia msg — implementação direta sobre
// net/smtp (biblioteca padrão do Go, sem dependência externa nova), ao
// contrário do legado que usa nodemailer.
func SendEmail(ctx context.Context, cfg SMTPConfig, msg EmailMessage) error {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	dialer := &net.Dialer{Timeout: defaultSMTPTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("notify: conectar ao servidor SMTP: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("notify: iniciar cliente SMTP: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // controlado explicitamente por SMTPConfig.InsecureSkipVerify, nunca o padrão
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("notify: STARTTLS: %w", err)
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("notify: autenticar no servidor SMTP: %w", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("notify: MAIL FROM: %w", err)
	}
	for _, to := range msg.To {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("notify: RCPT TO %s: %w", to, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("notify: DATA: %w", err)
	}
	if _, err := w.Write(buildMessage(cfg.From, msg)); err != nil {
		return fmt.Errorf("notify: escrever corpo do e-mail: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("notify: finalizar corpo do e-mail: %w", err)
	}
	return client.Quit()
}

func buildMessage(from string, msg EmailMessage) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(msg.To, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	if msg.HTMLBody == "" {
		b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(msg.Body)
		return b.Bytes()
	}

	textPart := msg.Body
	if textPart == "" {
		textPart = "Este e-mail requer um cliente compatível com HTML para ser exibido corretamente."
	}
	boundary := "saltcorn-go-" + hex.EncodeToString(randomBoundary())
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n\r\n", boundary, textPart)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n\r\n", boundary, msg.HTMLBody)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func randomBoundary() []byte {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
