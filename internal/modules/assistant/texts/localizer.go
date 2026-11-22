package texts

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed active.en.json active.ru.json
var catalogs embed.FS

// Localizer renders assistant messages in the installation language.
type Localizer struct {
	localizer *i18n.Localizer
}

// New loads the embedded catalogs and selects the installation language.
func New(locale string) (*Localizer, error) {
	bundle := i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	for _, path := range []string{"active.en.json", "active.ru.json"} {
		if _, err := bundle.LoadMessageFileFS(catalogs, path); err != nil {
			return nil, fmt.Errorf("load assistant catalog %s: %w", path, err)
		}
	}
	return &Localizer{localizer: i18n.NewLocalizer(bundle, locale)}, nil
}

// Text renders a message without template parameters.
func (l *Localizer) Text(id string) string {
	return l.Format(id, nil)
}

// Format renders a message with named template parameters.
func (l *Localizer) Format(id string, data map[string]any) string {
	return l.localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: id, TemplateData: data})
}

// Plural renders a message using the plural form for count.
func (l *Localizer) Plural(id string, count any, data map[string]any) string {
	return l.localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: id, TemplateData: data, PluralCount: count})
}
