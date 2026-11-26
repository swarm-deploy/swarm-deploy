package texts

import (
	"embed"
	"fmt"

	sharedi18n "github.com/swarm-deploy/swarm-deploy/internal/shared/i18n"
)

//go:embed active.en.json active.ru.json
var catalogs embed.FS

// Localizer renders assistant messages in the installation language.
type Localizer struct {
	*sharedi18n.Localizer
}

// New loads the embedded catalogs and selects the installation language.
func New(locale string) (*Localizer, error) {
	localizer, err := sharedi18n.New(locale, catalogs, "active.en.json", "active.ru.json")
	if err != nil {
		return nil, fmt.Errorf("load assistant texts: %w", err)
	}

	return &Localizer{Localizer: localizer}, nil
}
