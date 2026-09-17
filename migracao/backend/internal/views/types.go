package views

import "github.com/vjuliani/saltcorn/migracao/backend/internal/identity"

// View é o registro persistido de uma view — o shape mínimo que o ciclo
// do editor (GO-019) precisa: identidade, papel mínimo de leitura
// ("publicar" é baixar MinRole), e a configuração (o documento de layout
// que o builder Craft.js produz/consome, GO-018). Version é o token de
// concorrência otimista (xmin::text), mesma convenção de
// internal/records.Record's "_version".
type View struct {
	ID            int
	Name          string
	TableID       int
	Template      string
	MinRole       identity.RoleID
	Configuration map[string]any
	Version       string
}

// ViewOptions são os parâmetros opcionais de CreateView. Zero-value usa o
// padrão seguro: MinRole = identity.RoleAdmin — uma view recém-criada
// nunca é visível publicamente por omissão, "publicar" é um ato explícito
// (UpdateView baixando MinRole), mesmo espírito de
// internal/metadata.TableOptions.
type ViewOptions struct {
	MinRole identity.RoleID
}
