package records

import (
	"fmt"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
)

// validateValue confirma que value é um tipo Go compatível com o tipo de
// campo declarado no catálogo — uma camada de correção além da resolução
// de identificadores: um filtro `{idade: "abc"}` contra um campo integer é
// rejeitado aqui, explicitamente, em vez de chegar ao Postgres como um
// erro de tipo menos claro (ou pior, uma conversão implícita surpreendente
// do driver).
func validateValue(field metadata.Field, value any) error {
	if value == nil {
		return nil // NULL é válido para qualquer tipo
	}
	switch field.Type {
	case metadata.FieldText:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: campo %q (text) recebeu %T", ErrTypeMismatch, field.Name, value)
		}
	case metadata.FieldInteger, metadata.FieldKey:
		switch value.(type) {
		case int, int32, int64:
		default:
			return fmt.Errorf("%w: campo %q (integer) recebeu %T", ErrTypeMismatch, field.Name, value)
		}
	case metadata.FieldBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%w: campo %q (boolean) recebeu %T", ErrTypeMismatch, field.Name, value)
		}
	case metadata.FieldFloat:
		switch value.(type) {
		case float32, float64, int, int32, int64:
		default:
			return fmt.Errorf("%w: campo %q (float) recebeu %T", ErrTypeMismatch, field.Name, value)
		}
	case metadata.FieldDate:
		if _, ok := value.(time.Time); !ok {
			return fmt.Errorf("%w: campo %q (date) recebeu %T", ErrTypeMismatch, field.Name, value)
		}
	}
	return nil
}
