package texts

import (
	"embed"
	"fmt"

	sharedlanguage "github.com/swarm-deploy/swarm-deploy/internal/shared/language"
)

//go:embed active.en.json active.ru.json
var catalogs embed.FS

// Localizer renders assistant messages in the installation language.
type Localizer struct {
	*sharedlanguage.Localizer
}

// New loads the embedded catalogs and selects the installation language.
func New(locale string) (*Localizer, error) {
	localizer, err := sharedlanguage.New(locale, catalogs, "active.en.json", "active.ru.json")
	if err != nil {
		return nil, fmt.Errorf("load assistant texts: %w", err)
	}

	return &Localizer{Localizer: localizer}, nil
}
