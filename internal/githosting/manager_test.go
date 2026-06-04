package githosting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGithubProvider(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		token string
	}{
		{
			name:  "without token",
			token: "",
		},
		{
			name:  "with token",
			token: "secret-token",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			provider, err := NewGithubProvider(testCase.token)
			require.NoError(t, err, "create github provider")
			require.NotNil(t, provider, "provider must be created")
			assert.NotNil(t, provider.client, "github client must be initialized")
		})
	}
}

func TestProviderManagerGet(t *testing.T) {
	t.Parallel()

	manager, err := NewProviderManager(Config{})
	require.NoError(t, err, "create provider manager")

	testCases := []struct {
		name          string
		uri           string
		expectedOwner string
		expectedName  string
		expectedError string
	}{
		{
			name:          "github repository",
			uri:           "https://github.com/acme/platform",
			expectedOwner: "acme",
			expectedName:  "platform",
		},
		{
			name:          "github repository with trailing slash",
			uri:           "https://github.com/acme/platform/",
			expectedOwner: "acme",
			expectedName:  "platform",
		},
		{
			name:          "github repository without scheme",
			uri:           "github.com/acme/platform",
			expectedOwner: "acme",
			expectedName:  "platform",
		},
		{
			name:          "github repository branch path",
			uri:           "https://github.com/acme/platform/_/branch/main",
			expectedOwner: "acme",
			expectedName:  "platform",
		},
		{
			name:          "github repository with release path",
			uri:           "https://github.com/acme/platform/releases/tag/v1.0.0",
			expectedOwner: "acme",
			expectedName:  "platform",
		},
		{
			name:          "unsupported hosting",
			uri:           "https://gitlab.com/acme/platform",
			expectedError: ErrProviderNotSupported.Error(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			referencedProvider, getErr := manager.Get(testCase.uri)
			if testCase.expectedError != "" {
				require.Error(t, getErr, "expected provider manager error")
				assert.Contains(t, getErr.Error(), testCase.expectedError, "unexpected error")
				return
			}

			require.NoError(t, getErr, "get referenced provider")
			require.NotNil(t, referencedProvider, "referenced provider must be returned")
			assert.Equal(t, testCase.expectedOwner, referencedProvider.reference.Owner, "unexpected owner")
			assert.Equal(t, testCase.expectedName, referencedProvider.reference.Name, "unexpected repository name")
		})
	}
}
