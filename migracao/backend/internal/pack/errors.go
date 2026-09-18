package pack

import (
	"errors"
	"fmt"
	"strings"
)

// ErrMissingDependencies é o sentinela que MissingDependenciesError
// envolve — o chamador usa errors.Is(err, pack.ErrMissingDependencies)
// sem precisar conhecer o tipo concreto.
var ErrMissingDependencies = errors.New("pack: dependências de plugin ausentes ou incompatíveis")

// ErrUnsupportedVersion é devolvido por Import quando Pack.Version é
// maior que Version (um pack de uma versão futura do formato,
// desconhecida por este binário) — nunca uma tentativa de importar pela
// metade um formato que não entende.
var ErrUnsupportedVersion = errors.New("pack: versão do pack não suportada por esta versão do backend")

// MissingDependenciesError carrega a lista completa de dependências
// ausentes/incompatíveis — Import nunca aplica NENHUMA mutação de
// catálogo quando este erro é devolvido (ver Import).
type MissingDependenciesError struct {
	Missing []MissingDependency
}

func (e *MissingDependenciesError) Error() string {
	parts := make([]string, len(e.Missing))
	for i, m := range e.Missing {
		parts[i] = m.String()
	}
	return fmt.Sprintf("%s: %s", ErrMissingDependencies, strings.Join(parts, ", "))
}

func (e *MissingDependenciesError) Unwrap() error {
	return ErrMissingDependencies
}
