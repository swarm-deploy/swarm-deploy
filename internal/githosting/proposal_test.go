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

func TestProposalRemoteLinks(t *testing.T) {
	for _, tc := range []struct {
		remote string
		want   string
	}{
		{"git@github.com:team/app.git", "https://github.com/team/app/tree/proposal"},
		{"ssh://git@gitlab.com:22/team/group/app.git", "https://gitlab.com/team/group/app/-/tree/proposal"},
		{"https://user:password@host:8443/team/app.git?token=private", "https://host:8443/team/app/tree/proposal"},
		{"/local/repo", ""},
	} {
		remote, err := NewProposalRemote(tc.remote, "")
		require.NoError(t, err)
		assert.Equal(t, tc.want, remote.BranchURL("proposal"))
		pr, err := remote.OpenPullRequest(t.Context(), "main", "proposal", "title", "body")
		require.NoError(t, err)
		assert.Empty(t, pr)
	}
}

func TestOpenPullRequestUsesPublishedBranchAndConfiguredBase(t *testing.T) {
	const link = "https://github.com/team/app/pull/1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "/repos/team/app/pulls", req.URL.Path)
		body, err := io.ReadAll(req.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, `{"base":"deploy","head":"proposal","title":"enable self-heal","body":"details"}`, string(body))
		w.WriteHeader(http.StatusCreated)
		_, err = io.WriteString(w, `{"html_url":"`+link+`"}`)
		assert.NoError(t, err)
	}))
	defer server.Close()

	remote, err := NewProposalRemote("git@github.com:team/app.git", "test")
	require.NoError(t, err)
	require.NotNil(t, remote.github)
	remote.github.client, err = github.NewClient(github.WithURLs(&server.URL, nil))
	require.NoError(t, err)
	pr, err := remote.OpenPullRequest(t.Context(), "deploy", "proposal", "enable self-heal", "details")
	require.NoError(t, err)
	assert.Equal(t, link, pr)
}
