package selfheal

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
)

// Service proposes enabling self-heal in the configured Git write repository.
type Service struct {
	stacks  config.StackProvider
	global  bool
	spec    config.GitSpec
	dataDir string
	remote  *url.URL
	github  *githosting.GithubProvider
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
	// AlreadyEnabled identifies the policy source when publication is unnecessary.
	AlreadyEnabled string `json:"alreadyEnabled,omitempty"`
	// Commit is the pushed Git commit hash.
	Commit string `json:"commit"`
}

// New creates the self-heal proposal service with an isolated Git write path.
func New(
	stacks config.StackProvider, global bool, spec config.GitSpec, dataDir string,
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
		stacks: stacks, global: global, spec: spec, dataDir: dataDir, remote: webRepositoryURL(spec.Repository),
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

// Preview reads policy from the base branch of the configured write repository.
func (s *Service) Preview(ctx context.Context, stack, service string) (State, error) {
	spec, err := s.stack(stack)
	if err != nil {
		return State{}, err
	}
	original, err := git.ReadProposalFile(ctx, s.spec, s.dataDir, spec.ComposeFile)
	if err != nil {
		return State{}, err
	}
	state, _, err := prepare(original, service, spec.Sync.Policy.SelfHeal, s.global)
	return state, err
}

// Enable publishes a service-scoped override after confirmation.
func (s *Service) Enable(ctx context.Context, stack, service, source, author string) (Result, error) {
	spec, err := s.stack(stack)
	if err != nil {
		return Result{}, err
	}
	branch := fmt.Sprintf("%s-%s-enable-self-heal", branchPart(stack), branchPart(service))
	title := fmt.Sprintf("chore: enable self-heal for %s/%s", stack, service)
	result := Result{Branch: branch}
	var current State
	result.Commit, err = git.PushFileChange(ctx, s.spec, s.dataDir, branch, spec.ComposeFile, title, author, func(
		_ context.Context, original []byte,
	) ([]byte, error) {
		var updated []byte
		var editErr error
		current, updated, editErr = prepare(original, service, spec.Sync.Policy.SelfHeal, s.global)
		if editErr != nil {
			return nil, editErr
		}
		if current.Enabled {
			return nil, errAlreadyEnabled
		}
		if current.Source != source {
			return nil, errors.New("self-heal policy changed since confirmation; request a new preview")
		}
		return updated, nil
	})
	if errors.Is(err, errAlreadyEnabled) {
		return Result{AlreadyEnabled: current.Source}, nil
	}
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
		result.PullRequestURL = s.openPullRequest(ctx, branch, title)
		result.PullRequestFailed = result.PullRequestURL == ""
	}
	return result, nil
}

func (s *Service) stack(stack string) (config.StackSpec, error) {
	for _, spec := range s.stacks.Stacks() {
		if spec.Name == stack && spec.ComposeFile != "" {
			return spec, nil
		}
	}
	return config.StackSpec{}, fmt.Errorf("GitOps stack %q has no Compose file", stack)
}

func (s *Service) openPullRequest(ctx context.Context, branch, title string) string {
	owner, name, ok := strings.Cut(strings.Trim(s.remote.Path, "/"), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	repo := githosting.RepositoryReference{Owner: owner, Name: name}
	prURL, err := s.github.CreatePullRequest(ctx, repo, s.spec.Branch, branch, title,
		"Enable automatic drift repair for this service by setting its Compose deploy label.")
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

var errAlreadyEnabled = errors.New("self-heal is already enabled")
