package yamledit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetPreservesUnchangedContent(t *testing.T) {
	const source = "# keep\nlabels: {other: value, app/name: 'old'} # comment\ntext: >\n  one\n  two\n"
	updated, err := Set([]byte(source), []string{"labels", "app/name"}, "new")
	require.NoError(t, err)
	assert.Equal(t,
		"# keep\nlabels: {other: value, app/name: 'new'} # comment\ntext: >\n  one\n  two\n",
		string(updated),
	)
}

func TestSetRejectsUnsupportedYAMLLineBreaks(t *testing.T) {
	const source = "root:\n  value: old\n"
	for _, tc := range []struct{ name, newline string }{
		{name: "NEL", newline: "\u0085"},
		{name: "line separator", newline: "\u2028"},
		{name: "paragraph separator", newline: "\u2029"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.ReplaceAll(source, "\n", tc.newline)
			updated, err := Set([]byte(input), []string{"root", "value"}, "new")
			require.Error(t, err)
			assert.Nil(t, updated)
		})
	}
}
