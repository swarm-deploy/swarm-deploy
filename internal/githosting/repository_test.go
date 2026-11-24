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

func TestParseGitRemote(t *testing.T) {
	for _, tc := range []struct {
		remote, owner, name, branchURL string
		github                         bool
	}{
		{"git@github.com:team/app.git", "team", "app", "https://github.com/team/app/tree/proposal", true},
		{"github.com:team/app.git", "team", "app", "https://github.com/team/app/tree/proposal", true},
		{"ssh://git@gitlab.com:22/team/group/app.git", "team/group", "app", "https://gitlab.com/team/group/app/-/tree/proposal", false},
		{"https://user:password@host:8443/team/app.git?token=private#fragment", "team", "app", "https://host:8443/team/app/tree/proposal", false},
		{"/local/repo", "", "", "", false},
		{"file:///local/repo", "", "", "", false},
		{"github.com/team/app", "", "", "", false}, // Git interprets this clone address as a local path.
	} {
		repo := ParseGitRemote(tc.remote)
		assert.Equal(t, tc.owner, repo.Owner)
		assert.Equal(t, tc.name, repo.Name)
		assert.Equal(t, tc.branchURL, repo.BranchURL("proposal"))
		assert.Equal(t, tc.github, repo.IsGitHub())
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
		_, err = io.WriteString(w, `{"html_url":"`+link+`"}`)
		assert.NoError(t, err)
	}))
	defer server.Close()

	client, err := github.NewClient(github.WithURLs(&server.URL, nil), github.WithAuthToken("write-token"))
	require.NoError(t, err)
	provider := &GithubProvider{client: client}
	repo := ParseGitRemote("git@github.com:team/app.git")
	pr, err := provider.CreatePullRequest(t.Context(), repo, "deploy", "proposal", "enable self-heal", "details")
	require.NoError(t, err)
	assert.Equal(t, link, pr)
}
