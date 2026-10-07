package compose

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestReadParseComposeError(t *testing.T) {
	t.Parallel()

	io := &faults.IOError{Err: errors.New("disk")}
	cases := []struct {
		name string
		err  error
	}{
		{"read", &ReadComposeError{StackName: "s", FilePath: "f", Err: io}},
		{"parse", &ParseComposeError{StackName: "s", FilePath: "f", Err: io}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wrapped := fmt.Errorf("ctx: %w", tc.err)
			require.ErrorIs(t, wrapped, io)
			var fe faults.Error
			require.ErrorAs(t, wrapped, &fe)
			assert.False(t, fe.Temporary())
			assert.Contains(t, tc.err.Error(), `"s"`)
		})
	}
}

func TestValidateComposeError(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("x: %w", &ValidateComposeError{
		StackName: "s", FilePath: "f",
		Issues: []ValidationIssue{{ResourceType: "service", ResourceName: "web", Field: "image", Code: "required", Message: "missing"}},
	})
	var ve *ValidateComposeError
	require.ErrorAs(t, err, &ve)
	assert.Len(t, ve.Issues, 1)
	assert.Contains(t, ve.Error(), "web")
}
