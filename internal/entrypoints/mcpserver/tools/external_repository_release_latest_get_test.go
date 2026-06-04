package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"go.uber.org/mock/gomock"
)

func TestGetExternalRepositoryLatestReleaseExecute(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	releaseTime := time.Date(2026, time.June, 4, 9, 30, 0, 0, time.FixedZone("UTC+03", 3*60*60))
	provider := githosting.NewMockProvider(ctrl)
	manager := NewMockGitHostingProviderManager(ctrl)
	manager.EXPECT().
		Get("https://github.com/acme/platform").
		Return(githosting.NewReferencedProvider(provider, githosting.RepositoryReference{
			Owner: "acme",
			Name:  "platform",
		}), nil)
	provider.EXPECT().
		GetLatestRelease(gomock.Any(), githosting.RepositoryReference{
			Owner: "acme",
			Name:  "platform",
		}).
		Return(&githosting.Release{
			Tag:         "v1.2.3",
			Commit:      "abc123",
			Body:        "release notes",
			URL:         "https://github.com/acme/platform/releases/tag/v1.2.3",
			PublishedAt: releaseTime,
		}, nil)

	tool := NewGetExternalRepositoryLatestRelease(manager)
	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: getExternalRepositoryLatestReleaseRequest{
			Repository: "https://github.com/acme/platform",
		},
	})
	require.NoError(t, err, "execute external_repository_release_latest_get")

	var payload struct {
		Repository    string `json:"repository"`
		Tag           string `json:"tag"`
		Commit        string `json:"commit"`
		Body          string `json:"body"`
		BodyTruncated bool   `json:"body_truncated"`
		URL           string `json:"url"`
		PublishedAt   string `json:"published_at"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "https://github.com/acme/platform", payload.Repository, "unexpected repository")
	assert.Equal(t, "v1.2.3", payload.Tag, "unexpected tag")
	assert.Equal(t, "abc123", payload.Commit, "unexpected commit")
	assert.Equal(t, "release notes", payload.Body, "unexpected body")
	assert.False(t, payload.BodyTruncated, "body should not be truncated")
	assert.Equal(t, "https://github.com/acme/platform/releases/tag/v1.2.3", payload.URL, "unexpected URL")
	assert.Equal(t, "2026-06-04T06:30:00Z", payload.PublishedAt, "unexpected published_at")
}

func TestGetExternalRepositoryLatestReleaseExecuteTruncatesBody(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	releaseTime := time.Date(2026, time.June, 4, 9, 30, 0, 0, time.UTC)
	longBody := strings.Repeat("a", maxExternalRepositoryReleaseBodyLength+32)
	provider := githosting.NewMockProvider(ctrl)
	manager := NewMockGitHostingProviderManager(ctrl)
	manager.EXPECT().
		Get("https://github.com/acme/platform").
		Return(githosting.NewReferencedProvider(provider, githosting.RepositoryReference{
			Owner: "acme",
			Name:  "platform",
		}), nil)
	provider.EXPECT().
		GetLatestRelease(gomock.Any(), githosting.RepositoryReference{
			Owner: "acme",
			Name:  "platform",
		}).
		Return(&githosting.Release{
			Tag:         "v1.2.3",
			Commit:      "abc123",
			Body:        longBody,
			URL:         "https://github.com/acme/platform/releases/tag/v1.2.3",
			PublishedAt: releaseTime,
		}, nil)

	tool := NewGetExternalRepositoryLatestRelease(manager)
	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: getExternalRepositoryLatestReleaseRequest{
			Repository: "https://github.com/acme/platform",
		},
	})
	require.NoError(t, err, "execute external_repository_release_latest_get with long body")

	var payload struct {
		Body          string `json:"body"`
		BodyTruncated bool   `json:"body_truncated"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Len(t, []rune(payload.Body), maxExternalRepositoryReleaseBodyLength, "unexpected body length")
	assert.True(t, payload.BodyTruncated, "body should be marked as truncated")
}

func TestGetExternalRepositoryLatestReleaseExecuteFails(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		request      routing.Request
		buildTool    func(*gomock.Controller) *GetExternalRepositoryLatestRelease
		expectedText string
	}{
		{
			name: "missing repository",
			request: routing.Request{
				Payload: getExternalRepositoryLatestReleaseRequest{},
			},
			buildTool: func(ctrl *gomock.Controller) *GetExternalRepositoryLatestRelease {
				return NewGetExternalRepositoryLatestRelease(NewMockGitHostingProviderManager(ctrl))
			},
			expectedText: "repository is required",
		},
		{
			name: "provider manager error",
			request: routing.Request{
				Payload: getExternalRepositoryLatestReleaseRequest{
					Repository: "https://gitlab.example.com/acme/platform",
				},
			},
			buildTool: func(ctrl *gomock.Controller) *GetExternalRepositoryLatestRelease {
				manager := NewMockGitHostingProviderManager(ctrl)
				manager.EXPECT().
					Get("https://gitlab.example.com/acme/platform").
					Return(nil, errors.New("unsupported hosting"))
				return NewGetExternalRepositoryLatestRelease(manager)
			},
			expectedText: "unsupported hosting",
		},
		{
			name: "provider error",
			request: routing.Request{
				Payload: getExternalRepositoryLatestReleaseRequest{
					Repository: "https://github.com/acme/platform",
				},
			},
			buildTool: func(ctrl *gomock.Controller) *GetExternalRepositoryLatestRelease {
				provider := githosting.NewMockProvider(ctrl)
				manager := NewMockGitHostingProviderManager(ctrl)
				manager.EXPECT().
					Get("https://github.com/acme/platform").
					Return(githosting.NewReferencedProvider(provider, githosting.RepositoryReference{
						Owner: "acme",
						Name:  "platform",
					}), nil)
				provider.EXPECT().
					GetLatestRelease(gomock.Any(), githosting.RepositoryReference{
						Owner: "acme",
						Name:  "platform",
					}).
					Return(nil, errors.New("release not found"))
				return NewGetExternalRepositoryLatestRelease(manager)
			},
			expectedText: "release not found",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tool := testCase.buildTool(ctrl)
			_, err := tool.Execute(context.Background(), testCase.request)
			require.Error(t, err, "expected tool execution error")
			assert.Contains(t, err.Error(), testCase.expectedText, "unexpected error")
		})
	}
}
