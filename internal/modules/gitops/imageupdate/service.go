package imageupdate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/registry"
)

// Service proposes an image update in the configured Git write repository.
type Service struct {
	stacks   config.StackProvider
	resolver *registry.ImageVersionResolver
	spec     config.GitSpec
	dataDir  string
	remote   *url.URL
	github   *githosting.GithubProvider
}

// Result describes the pushed branch and the optional pull request.
type Result struct {
	// Branch is the pushed proposal branch.
	Branch string `json:"branch"`
	// BranchURL is the browser URL of the branch, when available.
	BranchURL string `json:"branchUrl,omitempty"`
	// PullRequestURL is the URL returned by GitHub.
	PullRequestURL string `json:"mergeRequestUrl,omitempty"`
	// PullRequestFailed indicates that the branch was pushed but opening a PR failed.
	PullRequestFailed bool `json:"pullRequestFailed,omitempty"`
	// NewImage is the updated Compose image reference.
	NewImage string `json:"newImage"`
	// Commit is the pushed Git commit hash.
	Commit string `json:"commit"`
}

// New creates the image update service with an isolated Git write path.
func New(
	stacks config.StackProvider, resolver *registry.ImageVersionResolver, spec config.GitSpec, dataDir string,
) (*Service, error) {
	var token string
	if push := spec.Push; push != nil {
		if push.Repository != "" && push.Repository != spec.Repository {
			spec.Repository = push.Repository
			spec.Auth = config.GitAuthSpec{}
		}
		if push.Branch != "" {
			spec.Branch = push.Branch
		}
		if push.Auth != nil {
			spec.Auth = *push.Auth
		}
		token = strings.TrimSpace(string(push.APIToken.Content))
	}
	service := &Service{
		stacks: stacks, resolver: resolver, spec: spec, dataDir: dataDir, remote: webRepositoryURL(spec.Repository),
	}
	if token != "" && service.remote != nil && strings.EqualFold(service.remote.Host, "github.com") {
		provider, err := githosting.NewGithubProvider(token)
		if err != nil {
			return nil, fmt.Errorf("initialize GitHub pull request client: %w", err)
		}
		service.github = provider
	}
	return service, nil
}

// Update pushes a version change for one service and optionally opens a GitHub pull request.
func (s *Service) Update(ctx context.Context, stack, service, version, author string) (Result, error) {
	composeFile, err := s.stackComposeFile(stack)
	if err != nil {
		return Result{}, err
	}
	normalizedService := branchPart(service)
	normalizedStack := branchPart(stack)
	if normalizedService == "" || normalizedStack == "" || !ValidTag(version) {
		return Result{}, errors.New("stack, service, and a valid image tag are required")
	}
	branch := fmt.Sprintf("%s-%s-up-image-%s", normalizedStack, normalizedService, version)
	title := fmt.Sprintf("chore: update %s/%s image to %s", stack, service, version)
	result := Result{Branch: branch}
	result.Commit, err = git.PushFileChange(ctx, s.spec, s.dataDir, branch, composeFile, title, author, func(
		editCtx context.Context, original []byte,
	) ([]byte, error) {
		updated, newImage, editErr := updateComposeImage(original, service, version)
		if editErr != nil {
			return nil, editErr
		}
		if _, resolveErr := s.resolver.ResolveActualVersion(editCtx, newImage); resolveErr != nil {
			return nil, fmt.Errorf("could not verify image %q in the registry", newImage)
		}
		result.NewImage = newImage
		return updated, nil
	})
	if err != nil {
		return Result{}, err
	}
	if s.remote != nil {
		branchURL := *s.remote
		branchURL.Path += "/tree/" + branch
		if strings.EqualFold(s.remote.Hostname(), "gitlab.com") {
			branchURL.Path = s.remote.Path + "/-/tree/" + branch
		}
		result.BranchURL = branchURL.String()
	}
	if s.github != nil {
		result.PullRequestURL = s.openPullRequest(ctx, branch, title, result.NewImage)
		result.PullRequestFailed = result.PullRequestURL == ""
	}
	return result, nil
}

func (s *Service) stackComposeFile(stack string) (string, error) {
	for _, spec := range s.stacks.Stacks() {
		if spec.Name == stack && spec.ComposeFile != "" {
			return spec.ComposeFile, nil
		}
	}
	return "", fmt.Errorf("GitOps stack %q has no Compose file", stack)
}

func (s *Service) openPullRequest(ctx context.Context, branch, title, image string) string {
	owner, name, ok := strings.Cut(strings.Trim(s.remote.Path, "/"), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	repo := githosting.RepositoryReference{Owner: owner, Name: name}
	prURL, err := s.github.CreatePullRequest(ctx, repo, s.spec.Branch, branch, title,
		fmt.Sprintf("Update the Compose image to `%s`.", image))
	if err != nil {
		return ""
	}
	return prURL
}

func webRepositoryURL(remote string) *url.URL {
	if !strings.Contains(remote, "://") {
		_, address, hasUser := strings.Cut(remote, "@")
		host, repo, ok := strings.Cut(address, ":")
		if !hasUser || !ok {
			return nil
		}
		remote = "ssh://" + host + "/" + strings.TrimPrefix(repo, "/")
	}
	repositoryURL, err := url.Parse(remote)
	if err != nil || repositoryURL.Hostname() == "" || repositoryURL.Path == "" {
		return nil
	}
	switch repositoryURL.Scheme {
	case "http", "https":
	case "ssh", "git":
		repositoryURL.Scheme = "https"
		repositoryURL.Host = repositoryURL.Hostname()
	default:
		return nil
	}
	return &url.URL{
		Scheme: repositoryURL.Scheme, Host: repositoryURL.Host,
		Path: strings.TrimSuffix(strings.TrimRight(repositoryURL.Path, "/"), ".git"),
	}
}

var invalidBranchPart = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func branchPart(value string) string {
	return strings.Trim(invalidBranchPart.ReplaceAllString(value, "-"), ".-")
}

// ValidTag reports whether the requested version is a literal OCI image tag.
func ValidTag(tag string) bool {
	return reference.TagRegexp.FindString(tag) == tag && tag != ""
}
