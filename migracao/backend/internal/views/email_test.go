package views

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/notify"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

func TestRenderShowPlanEmailHTML_RendersColumnsInOrder(t *testing.T) {
	plan := &ShowPlan{
		Table:    "books",
		RecordID: 1,
		Columns:  []ShowColumn{{FieldName: "title", HeaderLabel: "Título"}, {FieldName: "pages", HeaderLabel: "Páginas"}},
		Values:   map[string]any{"title": "Dune", "pages": 412},
	}
	got := RenderShowPlanEmailHTML(plan)
	if !strings.Contains(got, "<!DOCTYPE html>") {
		t.Fatal("esperado documento HTML completo")
	}
	titleIdx := strings.Index(got, "Título")
	pagesIdx := strings.Index(got, "Páginas")
	if titleIdx < 0 || pagesIdx < 0 || titleIdx > pagesIdx {
		t.Fatalf("esperado colunas na ordem de plan.Columns, obtido: %s", got)
	}
	if !strings.Contains(got, "Dune") || !strings.Contains(got, "412") {
		t.Fatalf("valores ausentes no HTML: %s", got)
	}
}

func TestRenderShowPlanEmailHTML_MissingValueRendersEmptyNotNil(t *testing.T) {
	plan := &ShowPlan{
		Table:   "books",
		Columns: []ShowColumn{{FieldName: "subtitle", HeaderLabel: "Subtítulo"}},
		Values:  map[string]any{},
	}
	got := RenderShowPlanEmailHTML(plan)
	if strings.Contains(got, "<nil>") {
		t.Fatalf("valor ausente não deveria aparecer como \"<nil>\": %s", got)
	}
}

func TestRenderShowPlanEmailHTML_EscapesHTML(t *testing.T) {
	plan := &ShowPlan{
		Table:   "books",
		Columns: []ShowColumn{{FieldName: "title", HeaderLabel: "Título"}},
		Values:  map[string]any{"title": `<script>alert("x")</script>`},
	}
	got := RenderShowPlanEmailHTML(plan)
	if strings.Contains(got, "<script>") {
		t.Fatalf("esperado valor escapado, HTML bruto vazou: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Fatalf("esperado escape HTML do valor: %s", got)
	}
}

// minimalFakeSMTP aceita uma única conexão e captura o corpo DATA — o
// bastante para provar que notify.SendEmail entrega de verdade o HTML
// produzido por RenderShowPlanEmailHTML, sem duplicar o servidor completo
// de internal/notify/email_test.go (pacote distinto, não reexportado).
func minimalFakeSMTP(t *testing.T) (addr string, body *strings.Builder, mu *sync.Mutex) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	body = &strings.Builder{}
	mu = &sync.Mutex{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		fmt.Fprint(conn, "220 fake.smtp ESMTP\r\n")
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if inData {
				if trimmed == "." {
					inData = false
					fmt.Fprint(conn, "250 OK\r\n")
					continue
				}
				mu.Lock()
				body.WriteString(line)
				mu.Unlock()
				continue
			}
			upper := strings.ToUpper(trimmed)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				fmt.Fprint(conn, "250 fake.smtp\r\n")
			case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
				fmt.Fprint(conn, "250 OK\r\n")
			case upper == "DATA":
				inData = true
				fmt.Fprint(conn, "354 End data with <CR><LF>.<CR><LF>\r\n")
			case upper == "QUIT":
				fmt.Fprint(conn, "221 bye\r\n")
				return
			default:
				fmt.Fprint(conn, "250 OK\r\n")
			}
		}
	}()
	return ln.Addr().String(), body, mu
}

// TestShowPlanToHTMLEmail_EndToEnd prova o Aceite de CAP-083 de ponta a
// ponta: compilar uma View Show REAL (Postgres), renderizar como e-mail
// HTML e efetivamente ENVIAR via SMTP — não só a renderização isolada.
func TestShowPlanToHTMLEmail_EndToEnd(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	booksWithFields(t, db, tenant, tableID)

	var plan *ShowPlan
	var view View
	var bookID int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		created, err := CreateView(ctx, tx, identity.RoleAdmin, "showbook_email", tableID, "Show", compatibleConfiguration(), ViewOptions{})
		view = created
		if err != nil {
			return err
		}
		rows, err := records.Rows(ctx, tx, identity.RoleAdmin, records.Query{Table: "books", OrderBy: []records.OrderTerm{{Field: "id"}}, Limit: 1})
		if err != nil {
			return err
		}
		bookID = int(rows[0]["id"].(int32))
		return nil
	}); err != nil {
		t.Fatalf("preparar fixture: %v", err)
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		plan, err = CompileShowPlan(ctx, tx, identity.RoleAdmin, view.ID, bookID)
		return err
	}); err != nil {
		t.Fatalf("CompileShowPlan: %v", err)
	}

	html := RenderShowPlanEmailHTML(plan)

	addr, body, mu := minimalFakeSMTP(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	err := notify.SendEmail(context.Background(), notify.SMTPConfig{Host: host, Port: port, From: "relatorios@example.com"},
		notify.EmailMessage{To: []string{"destino@example.com"}, Subject: "Relatório de livro", HTMLBody: html})
	if err != nil {
		t.Fatalf("SendEmail: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body.String(), "Dune") {
		t.Fatalf("corpo do e-mail entregue não contém o valor real da view: %s", body.String())
	}
	if !strings.Contains(body.String(), "multipart/alternative") {
		t.Fatal("esperado multipart/alternative no e-mail entregue")
	}
}
