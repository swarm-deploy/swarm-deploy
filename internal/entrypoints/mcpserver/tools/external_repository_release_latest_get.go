package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// GetExternalRepositoryLatestRelease returns latest release for external repository.
type GetExternalRepositoryLatestRelease struct {
	providers GitHostingProviderManager
}

type getExternalRepositoryLatestReleaseRequest struct {
	Repository string `json:"repository"`
}

// NewGetExternalRepositoryLatestRelease creates external_repository_release_latest_get component.
func NewGetExternalRepositoryLatestRelease(
	providers GitHostingProviderManager,
) *GetExternalRepositoryLatestRelease {
	return &GetExternalRepositoryLatestRelease{
		providers: providers,
	}
}

// Definition returns tool metadata visible to the model.
func (g *GetExternalRepositoryLatestRelease) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "external_repository_release_latest_get",
		Description: "Returns latest published release for an external git repository URL supported by configured hosting providers.", //nolint:lll // not need
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"repository",
			},
			"properties": map[string]any{
				"repository": map[string]any{
					"type":        "string",
					"description": "Repository URL, for example https://github.com/owner/repository.",
				},
			},
		},
		Request: getExternalRepositoryLatestReleaseRequest{},
	}
}

// Execute runs external_repository_release_latest_get tool.
func (g *GetExternalRepositoryLatestRelease) Execute(
	ctx context.Context,
	request routing.Request,
) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[getExternalRepositoryLatestReleaseRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	repository := strings.TrimSpace(parsedRequest.Repository)
	if repository == "" {
		return routing.Response{}, fmt.Errorf("repository is required")
	}

	provider, err := g.providers.Get(repository)
	if err != nil {
		return routing.Response{}, err
	}

	release, err := provider.GetLatestRelease(ctx)
	if err != nil {
		return routing.Response{}, err
	}

	payload := struct {
		// Repository is a requested repository URL.
		Repository string `json:"repository"`

		// Tag is a release tag.
		Tag string `json:"tag"`

		// Commit is a target release commit-ish.
		Commit string `json:"commit"`

		// Body is a release body text.
		Body string `json:"body,omitempty"`

		// URL is a release page URL.
		URL string `json:"url,omitempty"`

		// PublishedAt is a release publication timestamp in RFC3339.
		PublishedAt string `json:"published_at"`
	}{
		Repository:  repository,
		Tag:         release.Tag,
		Commit:      release.Commit,
		Body:        release.Body,
		URL:         release.URL,
		PublishedAt: release.PublishedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}

	return routing.Response{Payload: payload}, nil
}
