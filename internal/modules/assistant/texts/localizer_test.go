package texts

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var parameterPattern = regexp.MustCompile(`\{\{\s*\.([A-Za-z][A-Za-z0-9]*)\s*\}\}`)

func TestCatalogsMatch(t *testing.T) {
	english := readCatalog(t, "active.en.json")
	russian := readCatalog(t, "active.ru.json")
	require.Equal(t, slices.Sorted(maps.Keys(english)), slices.Sorted(maps.Keys(russian)))

	for id, enForms := range english {
		t.Run(id, func(t *testing.T) {
			params := templateParameters(enForms["other"])
			ruForms := russian[id]
			for _, forms := range []map[string]string{enForms, ruForms} {
				for _, message := range forms {
					assert.NotEmpty(t, message)
					assert.Equal(t, params, templateParameters(message))
				}
			}

			forms := []string{"other"}
			if _, plural := enForms["one"]; plural {
				forms = append(forms, "one", "few", "many")
			}
			assert.ElementsMatch(t, forms, slices.Collect(maps.Keys(ruForms)))
		})
	}
}

func TestPluralForms(t *testing.T) {
	english, err := New("en")
	require.NoError(t, err)
	russian, err := New("ru")
	require.NoError(t, err)
	for _, tc := range []struct {
		count           int
		replicaEN       string
		replicaRU       string
		remainingPauses string
	}{
		{1, "replica", "реплику", "1 прямая пауза сервиса останется активной."},
		{2, "replicas", "реплики", "2 прямые паузы сервисов останутся активными."},
		{5, "replicas", "реплик", "5 прямых пауз сервисов останутся активными."},
		{11, "replicas", "реплик", "11 прямых пауз сервисов останутся активными."},
		{21, "replicas", "реплику", "21 прямая пауза сервиса останется активной."},
	} {
		t.Run(fmt.Sprint(tc.count), func(t *testing.T) {
			data := map[string]any{"Count": tc.count, "Service": "worker_API", "Stack": "core"}
			assert.Equal(t, fmt.Sprintf("Set %d %s for `worker_API` in stack `core`?", tc.count, tc.replicaEN),
				english.Plural("operation.confirm_replicas", tc.count, data))
			assert.Equal(t, fmt.Sprintf("Установить %d %s для `worker_API` в стеке `core`?", tc.count, tc.replicaRU),
				russian.Plural("operation.confirm_replicas", tc.count, data))
			assert.Equal(t, tc.remainingPauses, russian.Plural("operation.remaining_pauses", tc.count, data))
		})
	}
}

func readCatalog(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	raw, err := catalogs.ReadFile(path)
	require.NoError(t, err)
	var entries map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &entries))
	result := make(map[string]map[string]string, len(entries))
	for id, entry := range entries {
		var message string
		if err := json.Unmarshal(entry, &message); err == nil {
			result[id] = map[string]string{"other": message}
			continue
		}
		var forms map[string]string
		require.NoError(t, json.Unmarshal(entry, &forms), id)
		result[id] = forms
	}
	return result
}

func templateParameters(message string) []string {
	params := map[string]struct{}{}
	for _, match := range parameterPattern.FindAllStringSubmatch(message, -1) {
		params[match[1]] = struct{}{}
	}
	return slices.Sorted(maps.Keys(params))
}
