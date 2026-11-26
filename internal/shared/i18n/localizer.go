// Package i18n provides the common backend localization mechanism.
package i18n

import (
	"encoding/json"
	"fmt"
	"io/fs"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// Localizer renders messages from a feature-owned catalog.
type Localizer struct {
	localizer *goi18n.Localizer
}

// New loads the provided catalogs and selects the installation locale.
func New(locale string, catalogs fs.FS, paths ...string) (*Localizer, error) {
	bundle := goi18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	for _, path := range paths {
		if _, err := bundle.LoadMessageFileFS(catalogs, path); err != nil {
			return nil, fmt.Errorf("load catalog %s: %w", path, err)
		}
	}

	return &Localizer{localizer: goi18n.NewLocalizer(bundle, locale)}, nil
}

// Text renders a message without template parameters.
func (l *Localizer) Text(id string) string {
	return l.Format(id, nil)
}

// Format renders a message with named template parameters.
func (l *Localizer) Format(id string, data map[string]any) string {
	return l.localizer.MustLocalize(&goi18n.LocalizeConfig{MessageID: id, TemplateData: data})
}

// Plural renders a message using the plural form for count.
func (l *Localizer) Plural(id string, count any, data map[string]any) string {
	return l.localizer.MustLocalize(&goi18n.LocalizeConfig{
		MessageID:    id,
		TemplateData: data,
		PluralCount:  count,
	})
}
