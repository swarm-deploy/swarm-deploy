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

func TestNewProposalRemote(t *testing.T) {
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
		remote, err := NewProposalRemote(tc.remote, tc.token)
		require.NoError(t, err)
		assert.Equal(t, tc.owner, remote.repo.Owner)
		assert.Equal(t, tc.name, remote.repo.Name)
		assert.Equal(t, tc.branchURL, remote.BranchURL("proposal"))
		assert.Equal(t, tc.github, remote.github != nil)
		if !tc.github {
			pr, openErr := remote.OpenPullRequest(t.Context(), "deploy", "proposal", "enable self-heal", "details")
			require.NoError(t, openErr)
			assert.Empty(t, pr)
		}
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
	remote, err := NewProposalRemote("git@github.com:team/app.git", "write-token")
	require.NoError(t, err)
	require.NotNil(t, remote.github)
	remote.github.client = client
	pr, err := remote.OpenPullRequest(t.Context(), "deploy", "proposal", "enable self-heal", "details")
	require.NoError(t, err)
	assert.Equal(t, link, pr)
}
