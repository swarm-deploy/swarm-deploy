package selfheal

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareServicePolicy(t *testing.T) {
	const prefix = "services:\n  api:\n    image: example/api:1.0 # untouched\n"
	const suffix = "  worker:\n    image: worker:latest\n"
	disabled := false
	for _, tc := range []struct {
		name, before, after, source string
		stack                       *bool
		global, enabled             bool
	}{
		{name: "global enabled", source: "global", global: true, enabled: true},
		{name: "stack disables global", stack: &disabled, global: true, source: "stack"},
		{name: "missing deploy", source: "global"},
		{name: "mapping", before: "    deploy:\n      labels:\n        other: value\n        " + label + ": 'false' # keep\n", after: "    deploy:\n      labels:\n        other: value\n        " + label + ": 'true' # keep\n", source: "service"},
		{name: "list", before: "    deploy:\n      labels:\n        - other=value\n", after: "    deploy:\n      labels:\n        - other=value\n        - " + label + "=true\n", source: "global"},
		{name: "flow list", before: "    deploy: {labels: [other=value, '" + label + "=off']}\n", after: "    deploy: {labels: [other=value, '" + label + "=true']}\n", source: "service"},
		{name: "service enables override", before: "    deploy: {labels: {" + label + ": on}}\n", source: "service", enabled: true},
		{name: "flow map", before: "    deploy: {replicas: 2}\n", after: "    deploy: {replicas: 2, labels: {" + label + ": 'true'}}\n", source: "global"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := prefix + tc.before + suffix
			state, updated, err := prepare([]byte(original), "api", tc.stack, tc.global)
			require.NoError(t, err)
			assert.Equal(t, State{Source: tc.source, Enabled: tc.enabled}, state)
			if tc.enabled {
				assert.Nil(t, updated)
				return
			}
			want := prefix + tc.after + suffix
			if tc.before == "" {
				want = strings.Replace(original, " # untouched", " # untouched\n    deploy:\n      labels:\n        "+label+": 'true'", 1)
			}
			assert.Equal(t, want, string(updated))
			next, second, err := prepare(updated, "api", tc.stack, tc.global)
			require.NoError(t, err)
			assert.True(t, next.Enabled)
			assert.Nil(t, second)
		})
	}
}

func TestSharedLabelsCannotChangeAnotherService(t *testing.T) {
	raw := []byte("services:\n  api:\n    deploy:\n      labels: &shared {" + label + ": 'off'}\n  worker:\n    deploy: {labels: *shared}\n")
	_, updated, err := prepare(raw, "api", nil, false)
	require.Error(t, err)
	assert.Nil(t, updated)
}

func TestPreparePreservesComposeValues(t *testing.T) {
	prefix := "\ufeffservices: {api: {image: 'тест', deploy: {labels: {"
	suffix := "}}}, worker: {image: other}} # комментарий\r\n"
	original := prefix + label + ": 'false'" + suffix
	_, updated, err := prepare([]byte(original), "api", nil, false)
	require.NoError(t, err)
	assert.Equal(t, prefix+label+`: 'true'`+suffix, string(updated))
}

func TestPrepareDoesNotOverrideMergedDeploy(t *testing.T) {
	original := "x-base: &base {deploy: {replicas: 3}}\nservices:\n  api: {<<: *base, image: web}\n"
	_, updated, err := prepare([]byte(original), "api", nil, false)
	require.ErrorContains(t, err, "inherited")
	assert.Nil(t, updated)
}

func TestPrepareAcceptsYAMLLineBreaks(t *testing.T) {
	const input = "services:\n  api:\n    image: api\n  worker:\n    image: worker\n"
	for _, newline := range []string{"\r\n", "\r"} {
		original := strings.ReplaceAll(input, "\n", newline)
		_, updated, err := prepare([]byte(original), "api", nil, false)
		require.NoError(t, err)
		want := strings.Replace(original, "    image: api", "    image: api"+newline+"    deploy:"+newline+
			"      labels:"+newline+"        "+label+": 'true'", 1)
		assert.Equal(t, want, string(updated))
	}
}

func TestPrepareRejectsUnsupportedYAMLLineBreaks(t *testing.T) {
	const input = "services:\n  api:\n    image: api\n"
	for _, tc := range []struct{ name, newline string }{
		{name: "NEL", newline: "\u0085"},
		{name: "line separator", newline: "\u2028"},
		{name: "paragraph separator", newline: "\u2029"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := strings.ReplaceAll(input, "\n", tc.newline)
			_, updated, err := prepare([]byte(original), "api", nil, false)
			require.Error(t, err)
			assert.Nil(t, updated)
		})
	}
}

func TestPreparePreservesFoldedValues(t *testing.T) {
	const original = "services:\n" +
		"  api: {image: api}\n" +
		"  worker:\n" +
		"    command: >\n      one\n        two\n      three\n" +
		"    environment:\n      MESSAGE: >+\n        alpha\n\n        beta\n\n"
	_, updated, err := prepare([]byte(original), "api", nil, false)
	require.NoError(t, err)
	want := strings.Replace(original, "api: {image: api}",
		"api: {image: api, deploy: {labels: {"+label+`: 'true'}}}`, 1)
	assert.Equal(t, want, string(updated))
}
