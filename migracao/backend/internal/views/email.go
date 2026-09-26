// E-mail HTML a partir de uma View (GO-046, CAP-083) — equivalente
// reduzido de viewToMjml/viewToEmailHtml do legado (models/email.ts):
// renderiza uma view Show compatível para um registro específico como um
// corpo de e-mail HTML autocontido.
//
// Divergência deliberada do legado: o legado usa MJML como linguagem
// intermediária (tags declarativas de layout de e-mail, compiladas para
// HTML responsivo pelo pacote npm `mjml`) — não existe compilador MJML em
// Go, e portar um motor de template inteiro só para este uso seria
// desproporcional ao caso de uso real (um e-mail administrativo simples,
// nunca um layout de e-mail marketing arbitrário). RenderShowPlanEmailHTML
// produz HTML final diretamente — uma tabela rótulo/valor, inline-styled
// (clientes de e-mail reais ignoram <style> externo/em <head> com
// frequência; todo estilo aqui é atributo inline, disciplina padrão de
// e-mail HTML) — sem depender de nenhum motor de template externo. Cobre
// literalmente o Aceite ("ao menos um modo de e-mail HTML a partir de View
// funciona"), não uma reimplementação da linguagem MJML.
package views

import (
	"fmt"
	"html"
	"strings"
)

// RenderShowPlanEmailHTML renderiza plan como um corpo de e-mail HTML
// autocontido — uma tabela com uma linha por coluna do Show, na MESMA
// ordem de plan.Columns. Valores ausentes aparecem como célula vazia,
// nunca "nil"/"<nil>" literal.
func RenderShowPlanEmailHTML(plan *ShowPlan) string {
	var rows strings.Builder
	for _, col := range plan.Columns {
		value := ""
		if v, ok := plan.Values[col.FieldName]; ok && v != nil {
			value = fmt.Sprint(v)
		}
		fmt.Fprintf(&rows,
			`<tr><td style="padding:8px 12px;border-bottom:1px solid #e2e2e2;color:#555555;font-weight:bold;">%s</td>`+
				`<td style="padding:8px 12px;border-bottom:1px solid #e2e2e2;color:#222222;">%s</td></tr>`,
			html.EscapeString(col.HeaderLabel), html.EscapeString(value))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="margin:0;padding:0;background-color:#f4f4f4;font-family:Arial,Helvetica,sans-serif;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background-color:#f4f4f4;padding:24px 0;">
<tr><td align="center">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" style="background-color:#ffffff;border-radius:4px;overflow:hidden;">
<tr><td style="padding:16px 24px;background-color:#2d3748;color:#ffffff;font-size:16px;">%s</td></tr>
<tr><td>
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0">
%s
</table>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`, html.EscapeString(plan.Table), rows.String())
}
