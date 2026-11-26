package git

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProposalBranch(t *testing.T) {
	tests := []struct {
		name     string
		parts    []string
		random   string
		expected string
	}{
		{
			name:     "readable operation",
			parts:    []string{"prod", "api", "pin-image"},
			random:   "12345",
			expected: "prod-api-pin-image-gezdgnbv",
		},
		{
			name:     "sanitized parts",
			parts:    []string{".prod/core.", "api..worker", "enable-self-heal"},
			random:   "abcde",
			expected: "prod-core-api.worker-enable-self-heal-mfrggzdf",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			branch, err := newProposalBranch(strings.NewReader(test.random), test.parts...)
			require.NoError(t, err)
			assert.Equal(t, test.expected, branch)
		})
	}
}

func TestNewProposalBranchErrors(t *testing.T) {
	tests := []struct {
		name     string
		parts    []string
		random   io.Reader
		expected string
	}{
		{
			name: "empty parts", parts: []string{"...", "---"},
			random: strings.NewReader("12345"), expected: "at least one non-empty part",
		},
		{
			name: "random failure", parts: []string{"prod"},
			random: iotest.ErrReader(errors.New("failed")), expected: "generate proposal branch suffix",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newProposalBranch(test.random, test.parts...)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.expected)
		})
	}
}
