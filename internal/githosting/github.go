package githosting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const (
	gitHubDefaultAPIBaseURL     = "https://api.github.com"
	repositoryPathSegmentsCount = 2
)

type httpDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

// GitHubProvider creates pull requests on GitHub.
type GitHubProvider struct {
	apiBaseURL string
	client     httpDoer
}

// NewGitHubProvider creates GitHub provider with default HTTP client.
func NewGitHubProvider() *GitHubProvider {
	return &GitHubProvider{
		apiBaseURL: gitHubDefaultAPIBaseURL,
		client:     &http.Client{},
	}
}

// NewGitHubProviderWithClient creates GitHub provider with custom HTTP client.
func NewGitHubProviderWithClient(client httpDoer) *GitHubProvider {
	return &GitHubProvider{
		apiBaseURL: gitHubDefaultAPIBaseURL,
		client:     client,
	}
}

// Supports reports whether repository URL points to GitHub.
func (p *GitHubProvider) Supports(repositoryURL string) bool {
	repositoryRef, err := parseRepositoryReference(repositoryURL)
	if err != nil {
		return false
	}

	return strings.EqualFold(repositoryRef.host, "github.com")
}

// CreateMergeRequest creates GitHub pull request and returns URL.
func (p *GitHubProvider) CreateMergeRequest(
	ctx context.Context,
	request CreateMergeRequestRequest,
) (string, error) {
	repositoryRef, err := parseRepositoryReference(request.RepositoryURL)
	if err != nil {
		return "", err
	}

	requestURL := fmt.Sprintf(
		"%s/repos/%s/pulls",
		strings.TrimSuffix(p.apiBaseURL, "/"),
		repositoryRef.repositoryPath(),
	)

	payload := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}{
		Title: strings.TrimSpace(request.Title),
		Head:  strings.TrimSpace(request.HeadBranch),
		Base:  strings.TrimSpace(request.BaseBranch),
		Body:  strings.TrimSpace(request.Body),
	}

	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode github pull request payload: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payloadRaw))
	if err != nil {
		return "", fmt.Errorf("build github pull request request: %w", err)
	}

	httpRequest.Header.Set("Accept", "application/vnd.github+json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(request.Token))
	httpRequest.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	httpResponse, err := p.client.Do(httpRequest)
	if err != nil {
		return "", fmt.Errorf("send github pull request request: %w", err)
	}
	defer httpResponse.Body.Close()

	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return "", fmt.Errorf("read github pull request response: %w", err)
	}

	if httpResponse.StatusCode != http.StatusCreated {
		return "", fmt.Errorf(
			"unexpected github pull request status %d: %s",
			httpResponse.StatusCode,
			strings.TrimSpace(string(responseBody)),
		)
	}

	var responsePayload struct {
		HTMLURL string `json:"html_url"`
	}
	if err = json.Unmarshal(responseBody, &responsePayload); err != nil {
		return "", fmt.Errorf("decode github pull request response: %w", err)
	}
	if strings.TrimSpace(responsePayload.HTMLURL) == "" {
		return "", fmt.Errorf("github pull request response does not contain html_url")
	}

	return strings.TrimSpace(responsePayload.HTMLURL), nil
}

type repositoryReference struct {
	host  string
	owner string
	name  string
}

func (r repositoryReference) repositoryPath() string {
	return fmt.Sprintf("%s/%s", r.owner, r.name)
}

func parseRepositoryReference(repositoryURL string) (repositoryReference, error) {
	raw := strings.TrimSpace(repositoryURL)
	if raw == "" {
		return repositoryReference{}, fmt.Errorf("repository url is required")
	}

	if strings.Contains(raw, "://") {
		return parseURLRepositoryReference(raw)
	}
	if strings.HasPrefix(raw, "git@") {
		return parseSCPRepositoryReference(raw)
	}

	return repositoryReference{}, fmt.Errorf("unsupported repository url format %q", repositoryURL)
}

func parseURLRepositoryReference(raw string) (repositoryReference, error) {
	parsedURL, err := url.Parse(raw)
	if err != nil {
		return repositoryReference{}, fmt.Errorf("parse repository url %q: %w", raw, err)
	}

	repositoryPath := strings.TrimPrefix(parsedURL.Path, "/")
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")

	segments := strings.Split(repositoryPath, "/")
	if len(segments) < repositoryPathSegmentsCount {
		return repositoryReference{}, fmt.Errorf("repository url %q must contain owner and repository", raw)
	}

	owner := segments[len(segments)-repositoryPathSegmentsCount]
	name := segments[len(segments)-1]
	if owner == "" || name == "" {
		return repositoryReference{}, fmt.Errorf("repository url %q must contain owner and repository", raw)
	}

	return repositoryReference{
		host:  parsedURL.Hostname(),
		owner: owner,
		name:  name,
	}, nil
}

func parseSCPRepositoryReference(raw string) (repositoryReference, error) {
	withoutUser := strings.TrimPrefix(raw, "git@")
	parts := strings.SplitN(withoutUser, ":", repositoryPathSegmentsCount)
	if len(parts) != repositoryPathSegmentsCount {
		return repositoryReference{}, fmt.Errorf("parse repository url %q", raw)
	}

	host := strings.TrimSpace(parts[0])
	repositoryPath := strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
	if host == "" || repositoryPath == "" {
		return repositoryReference{}, fmt.Errorf("parse repository url %q", raw)
	}

	owner := path.Dir(repositoryPath)
	name := path.Base(repositoryPath)
	if owner == "." || owner == "" || name == "." || name == "" {
		return repositoryReference{}, fmt.Errorf("repository url %q must contain owner and repository", raw)
	}

	return repositoryReference{
		host:  host,
		owner: owner,
		name:  name,
	}, nil
}
