package language

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalizer(t *testing.T) {
	catalogs := fstest.MapFS{
		"messages.en.json": {Data: []byte(`{
			"greeting": "Hello, {{.Name}}!",
			"items": {"one": "{{.Count}} item", "other": "{{.Count}} items"}
		}`)},
		"messages.ru.json": {Data: []byte(`{
			"greeting": "Привет, {{.Name}}!",
			"items": {
				"one": "{{.Count}} элемент",
				"few": "{{.Count}} элемента",
				"many": "{{.Count}} элементов",
				"other": "{{.Count}} элемента"
			}
		}`)},
	}

	for _, tc := range []struct {
		locale   string
		greeting string
		items    string
	}{
		{locale: "en", greeting: "Hello, Swarm!", items: "2 items"},
		{locale: "ru", greeting: "Привет, Swarm!", items: "2 элемента"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			localizer, err := New(tc.locale, catalogs, "messages.en.json", "messages.ru.json")
			require.NoError(t, err)

			assert.Equal(t, tc.greeting, localizer.Format("greeting", map[string]any{"Name": "Swarm"}))
			assert.Equal(t, tc.items, localizer.Plural("items", 2, map[string]any{"Count": 2}))
		})
	}
}
