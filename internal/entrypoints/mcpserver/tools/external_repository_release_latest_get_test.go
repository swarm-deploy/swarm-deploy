package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
)

func TestGetExternalRepositoryLatestReleaseExecute(t *testing.T) {
	t.Parallel()

	releaseTime := time.Date(2026, time.June, 4, 9, 30, 0, 0, time.FixedZone("UTC+03", 3*60*60))
	releaseProvider := &fakeGitHostingProvider{
		release: &githosting.Release{
			Tag:         "v1.2.3",
			Commit:      "abc123",
			Body:        "release notes",
			URL:         "https://github.com/acme/platform/releases/tag/v1.2.3",
			PublishedAt: releaseTime,
		},
	}
	manager := &fakeGitHostingProviderManager{
		referencedProvider: githosting.NewReferencedProvider(releaseProvider, githosting.RepositoryReference{
			Owner: "acme",
			Name:  "platform",
		}),
	}

	tool := NewGetExternalRepositoryLatestRelease(manager)
	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: getExternalRepositoryLatestReleaseRequest{
			Repository: "https://github.com/acme/platform",
		},
	})
	require.NoError(t, err, "execute external_repository_release_latest_get")
	assert.Equal(t, 1, manager.called, "manager must be called once")
	assert.Equal(t, "https://github.com/acme/platform", manager.uri, "unexpected repository URI")
	assert.Equal(t, 1, releaseProvider.called, "release provider must be called once")
	assert.Equal(t, "acme", releaseProvider.repo.Owner, "unexpected owner")
	assert.Equal(t, "platform", releaseProvider.repo.Name, "unexpected repository name")

	var payload struct {
		Repository  string `json:"repository"`
		Tag         string `json:"tag"`
		Commit      string `json:"commit"`
		Body        string `json:"body"`
		URL         string `json:"url"`
		PublishedAt string `json:"published_at"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "https://github.com/acme/platform", payload.Repository, "unexpected repository")
	assert.Equal(t, "v1.2.3", payload.Tag, "unexpected tag")
	assert.Equal(t, "abc123", payload.Commit, "unexpected commit")
	assert.Equal(t, "release notes", payload.Body, "unexpected body")
	assert.Equal(t, "https://github.com/acme/platform/releases/tag/v1.2.3", payload.URL, "unexpected URL")
	assert.Equal(t, "2026-06-04T06:30:00Z", payload.PublishedAt, "unexpected published_at")
}

func TestGetExternalRepositoryLatestReleaseExecuteFails(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		tool         *GetExternalRepositoryLatestRelease
		request      routing.Request
		expectedText string
	}{
		{
			name: "missing repository",
			tool: NewGetExternalRepositoryLatestRelease(&fakeGitHostingProviderManager{}),
			request: routing.Request{
				Payload: getExternalRepositoryLatestReleaseRequest{},
			},
			expectedText: "repository is required",
		},
		{
			name: "provider manager error",
			tool: NewGetExternalRepositoryLatestRelease(&fakeGitHostingProviderManager{
				err: errors.New("unsupported hosting"),
			}),
			request: routing.Request{
				Payload: getExternalRepositoryLatestReleaseRequest{
					Repository: "https://gitlab.example.com/acme/platform",
				},
			},
			expectedText: "unsupported hosting",
		},
		{
			name: "provider error",
			tool: NewGetExternalRepositoryLatestRelease(&fakeGitHostingProviderManager{
				referencedProvider: githosting.NewReferencedProvider(
					&fakeGitHostingProvider{err: errors.New("release not found")},
					githosting.RepositoryReference{Owner: "acme", Name: "platform"},
				),
			}),
			request: routing.Request{
				Payload: getExternalRepositoryLatestReleaseRequest{
					Repository: "https://github.com/acme/platform",
				},
			},
			expectedText: "release not found",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := testCase.tool.Execute(context.Background(), testCase.request)
			require.Error(t, err, "expected tool execution error")
			assert.Contains(t, err.Error(), testCase.expectedText, "unexpected error")
		})
	}
}
