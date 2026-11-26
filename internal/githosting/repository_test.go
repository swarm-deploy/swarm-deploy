package githosting

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v88/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvePushRepository(t *testing.T) {
	for _, tc := range []struct {
		remote, token, owner, name, branchURL string
		github                                bool
	}{
		{"git@github.com:team/app.git", "write-token", "team", "app", "https://github.com/team/app/tree/proposal", true},
		{"github.com:team/app.git", "write-token", "team", "app", "https://github.com/team/app/tree/proposal", true},
		{"git@github.com:team/app.git", "", "team", "app", "https://github.com/team/app/tree/proposal", false},
		{"ssh://git@gitlab.com:22/team/group/app.git", "write-token", "team/group", "app", "https://gitlab.com/team/group/app/-/tree/proposal", false},
		{"https://user:password@host:8443/team/app.git?token=private#fragment", "write-token", "team", "app", "https://host:8443/team/app/tree/proposal", false},
		{"/local/repo", "write-token", "", "", "", false},
		{"file:///local/repo", "write-token", "", "", "", false},
		{"github.com/team/app", "write-token", "", "", "", false}, // Git interprets this clone address as a local path.
	} {
		repo, client, err := ResolvePushRepository(tc.remote, tc.token)
		require.NoError(t, err)
		assert.Equal(t, tc.owner, repo.Owner)
		assert.Equal(t, tc.name, repo.Name)
		assert.Equal(t, tc.branchURL, repo.BranchURL("proposal"))
		assert.Equal(t, tc.github, client != nil)
	}
}

func TestOpenPullRequestUsesPublishedBranchAndConfiguredBase(t *testing.T) {
	const link = "https://github.com/team/app/pull/1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "Bearer write-token", req.Header.Get("Authorization"))
		assert.Equal(t, "/repos/team/app/pulls", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, `{"base":"deploy","head":"proposal","title":"enable self-heal","body":"details"}`, string(body))
		w.WriteHeader(http.StatusCreated)
		_, err = io.WriteString(w, `{"html_url":"`+link+`","number":42}`)
		assert.NoError(t, err)
	}))
	defer server.Close()

	client, err := github.NewClient(github.WithURLs(&server.URL, nil), github.WithAuthToken("write-token"))
	require.NoError(t, err)
	repo, provider, err := ResolvePushRepository("git@github.com:team/app.git", "write-token")
	require.NoError(t, err)
	require.NotNil(t, provider)
	githubProvider, ok := provider.(*GithubProvider)
	require.True(t, ok)
	githubProvider.client = client
	pr, err := provider.CreateChangeRequest(t.Context(), repo, "deploy", "proposal", "enable self-heal", "details")
	require.NoError(t, err)
	assert.Equal(t, ChangeRequest{URL: link, Number: 42}, pr)
}
