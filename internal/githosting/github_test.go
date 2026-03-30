package githosting

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubProviderSupports(t *testing.T) {
	provider := NewGitHubProvider()

	assert.True(
		t,
		provider.Supports("https://github.com/acme/swarm-config.git"),
		"github repository should be supported",
	)
	assert.True(
		t,
		provider.Supports("git@github.com:acme/swarm-config.git"),
		"github ssh repository should be supported",
	)
	assert.False(
		t,
		provider.Supports("https://gitlab.com/acme/swarm-config.git"),
		"gitlab repository should not be supported",
	)
}

func TestGitHubProviderCreateMergeRequest(t *testing.T) {
	var payload struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}

	client := &fakeHTTPDoer{
		do: func(request *http.Request) (*http.Response, error) {
			assert.Equal(t, "/repos/acme/swarm-config/pulls", request.URL.Path, "unexpected request path")
			assert.Equal(t, "Bearer token-1", request.Header.Get("Authorization"), "unexpected authorization header")
			assert.Equal(t, "application/vnd.github+json", request.Header.Get("Accept"), "unexpected accept header")
			assert.Equal(t, "2022-11-28", request.Header.Get("X-GitHub-Api-Version"), "unexpected api version")

			err := json.NewDecoder(request.Body).Decode(&payload)
			require.NoError(t, err, "decode request payload")

			return &http.Response{
				StatusCode: http.StatusCreated,
				Body:       io.NopCloser(bytes.NewBufferString(`{"html_url":"https://github.com/acme/swarm-config/pull/11"}`)),
			}, nil
		},
	}

	provider := NewGitHubProviderWithClient(client)
	provider.apiBaseURL = "https://api.github.com"

	url, err := provider.CreateMergeRequest(context.Background(), CreateMergeRequestRequest{
		RepositoryURL: "https://github.com/acme/swarm-config.git",
		BaseBranch:    "main",
		HeadBranch:    "api-up-image-2.0.0",
		Title:         "chore(api): up image to 2.0.0",
		Body:          "please update by artem",
		Token:         "token-1",
	})
	require.NoError(t, err, "create merge request")
	assert.Equal(t, "https://github.com/acme/swarm-config/pull/11", url, "unexpected merge request url")
	assert.Equal(t, "chore(api): up image to 2.0.0", payload.Title, "unexpected title")
	assert.Equal(t, "api-up-image-2.0.0", payload.Head, "unexpected head branch")
	assert.Equal(t, "main", payload.Base, "unexpected base branch")
	assert.Equal(t, "please update by artem", payload.Body, "unexpected body")
}

type fakeHTTPDoer struct {
	do func(request *http.Request) (*http.Response, error)
}

func (f *fakeHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return f.do(request)
}
