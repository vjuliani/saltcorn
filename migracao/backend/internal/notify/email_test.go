// Corpus de e-mail — um servidor SMTP falso, mínimo, real por TCP (sem
// dependência externa: só o protocolo de texto SMTP), para exercitar
// SendEmail de ponta a ponta sem precisar de um servidor de e-mail de
// verdade.
package notify

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

type fakeSMTPServer struct {
	mu          sync.Mutex
	mailFrom    string
	rcptTo      []string
	dataBody    string
	dataFailure bool
}

func (s *fakeSMTPServer) snapshot() (mailFrom string, rcptTo []string, dataBody string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mailFrom, append([]string(nil), s.rcptTo...), s.dataBody
}

func startFakeSMTPServer(t *testing.T, srv *fakeSMTPServer) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleFakeSMTPConn(conn, srv)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

func handleFakeSMTPConn(conn net.Conn, srv *fakeSMTPServer) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	fmt.Fprint(conn, "220 fake.smtp ESMTP\r\n")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			fmt.Fprint(conn, "250-fake.smtp\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(upper, "AUTH"):
			fmt.Fprint(conn, "235 authenticated\r\n")
		case strings.HasPrefix(upper, "MAIL FROM"):
			srv.mu.Lock()
			srv.mailFrom = line
			srv.mu.Unlock()
			fmt.Fprint(conn, "250 OK\r\n")
		case strings.HasPrefix(upper, "RCPT TO"):
			srv.mu.Lock()
			srv.rcptTo = append(srv.rcptTo, line)
			srv.mu.Unlock()
			fmt.Fprint(conn, "250 OK\r\n")
		case upper == "DATA":
			if srv.dataFailure {
				fmt.Fprint(conn, "554 transaction failed\r\n")
				continue
			}
			fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
			var body strings.Builder
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				body.WriteString(dataLine)
			}
			srv.mu.Lock()
			srv.dataBody = body.String()
			srv.mu.Unlock()
			fmt.Fprint(conn, "250 OK\r\n")
		case upper == "QUIT":
			fmt.Fprint(conn, "221 bye\r\n")
			return
		default:
			fmt.Fprint(conn, "250 OK\r\n")
		}
	}
}

func TestSendEmail_HappyPath(t *testing.T) {
	srv := &fakeSMTPServer{}
	host, port := startFakeSMTPServer(t, srv)

	err := SendEmail(context.Background(), SMTPConfig{
		Host: host, Port: port, Username: "user", Password: "senha", From: "remetente@example.com",
	}, EmailMessage{To: []string{"destino@example.com"}, Subject: "Assunto de teste", Body: "Corpo do e-mail"})
	if err != nil {
		t.Fatalf("SendEmail: %v", err)
	}

	mailFrom, rcptTo, body := srv.snapshot()
	if !strings.Contains(mailFrom, "remetente@example.com") {
		t.Errorf("mailFrom = %q, esperado conter remetente@example.com", mailFrom)
	}
	if len(rcptTo) != 1 || !strings.Contains(rcptTo[0], "destino@example.com") {
		t.Errorf("rcptTo = %v, esperado conter destino@example.com", rcptTo)
	}
	if !strings.Contains(body, "Corpo do e-mail") || !strings.Contains(body, "Assunto de teste") {
		t.Errorf("body = %q, esperado conter assunto e corpo", body)
	}
}

func TestSendEmail_WithoutAuth(t *testing.T) {
	srv := &fakeSMTPServer{}
	host, port := startFakeSMTPServer(t, srv)

	err := SendEmail(context.Background(), SMTPConfig{Host: host, Port: port, From: "remetente@example.com"},
		EmailMessage{To: []string{"destino@example.com"}, Subject: "sem auth", Body: "ok"})
	if err != nil {
		t.Fatalf("SendEmail sem autenticação: %v, esperado sucesso", err)
	}
}

// TestSendEmail_ProviderFailure_ReturnsExplicitError prova que uma falha
// do provedor (aqui, o comando DATA rejeitado) propaga como erro
// explícito — nunca engolida em silêncio (divergência deliberada do
// MailQueue.send do legado, que só loga `.catch((e) => state.log(...))`).
// É este erro que outbox.ProcessPending (GO-014) usa para decidir retry.
func TestSendEmail_ProviderFailure_ReturnsExplicitError(t *testing.T) {
	srv := &fakeSMTPServer{dataFailure: true}
	host, port := startFakeSMTPServer(t, srv)

	err := SendEmail(context.Background(), SMTPConfig{Host: host, Port: port, From: "remetente@example.com"},
		EmailMessage{To: []string{"destino@example.com"}, Subject: "vai falhar", Body: "..."})
	if err == nil {
		t.Fatal("esperado erro quando o provedor rejeita DATA")
	}
}

func TestSendEmail_ConnectionRefused_ReturnsExplicitError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close() // porta livre de novo, mas ninguém escuta — conexão recusada

	err = SendEmail(context.Background(), SMTPConfig{Host: "127.0.0.1", Port: addr.Port, From: "x@example.com"},
		EmailMessage{To: []string{"y@example.com"}, Subject: "x", Body: "x"})
	if err == nil {
		t.Fatal("esperado erro de conexão recusada")
	}
}
