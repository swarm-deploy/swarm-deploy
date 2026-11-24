package selfheal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
)

// ErrPolicyChanged indicates that publication needs a new plan for the current policy.
var ErrPolicyChanged = errors.New("self-heal policy changed after preparation. Prepare a new plan")

// Service proposes enabling self-heal in the configured Git write repository.
type Service struct {
	stacks  config.StackProvider
	global  bool
	spec    config.GitSpec
	dataDir string
	repo    githosting.RepositoryReference
	github  *githosting.GithubProvider // optional, uses the write repository API token
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
	write, token := spec.ForPush()
	repo := githosting.ParseGitRemote(write.Repository)
	s := &Service{stacks: stacks, global: global, spec: write, dataDir: dataDir, repo: repo}
	if token != "" && repo.IsGitHub() {
		github, err := githosting.NewGithubProvider(token)
		if err != nil {
			return nil, fmt.Errorf("initialize GitHub pull request client: %w", err)
		}
		s.github = github
	}
	return s, nil
}

// Preview reads policy from the base branch of the configured write repository.
func (s *Service) Preview(ctx context.Context, stack, service string) (State, error) {
	change, err := s.prepare(ctx, stack, service)
	if err != nil {
		return State{}, err
	}
	defer change.file.Close(ctx)
	return change.state, nil
}

// Enable rechecks the policy observed during preparation on a fresh checkout before publishing.
func (s *Service) Enable(ctx context.Context, stack, service string, expected State, author string) (Result, error) {
	change, err := s.prepare(ctx, stack, service)
	if err != nil {
		return Result{}, err
	}
	defer change.file.Close(ctx)
	if change.state.Enabled {
		return Result{AlreadyEnabled: change.state.Source}, nil
	}
	if change.state != expected {
		return Result{}, ErrPolicyChanged
	}
	branch := fmt.Sprintf("%s-%s-enable-self-heal", branchPart(stack), branchPart(service))
	title := fmt.Sprintf("chore: enable self-heal for %s/%s", stack, service)
	commit, err := change.file.Push(ctx, branch, title, author, change.updated)
	if err != nil {
		return Result{}, err
	}
	result := Result{Commit: commit, Branch: branch, BranchURL: s.repo.BranchURL(branch)}
	if s.github != nil {
		result.PullRequestURL, err = s.github.CreatePullRequest(ctx, s.repo, s.spec.Branch, branch, title,
			"Enable automatic drift repair for this service by setting its Compose deploy label.")
		result.PullRequestFailed = err != nil
	}
	return result, nil
}

type proposal struct {
	file    *git.ProposalFile
	state   State
	updated []byte
}

func (s *Service) prepare(ctx context.Context, stack, service string) (proposal, error) {
	for _, spec := range s.stacks.Stacks() {
		if spec.Name != stack || spec.ComposeFile == "" {
			continue
		}
		file, err := git.OpenProposalFile(ctx, s.spec, s.dataDir, spec.ComposeFile)
		if err != nil {
			return proposal{}, err
		}
		state, updated, err := prepare(file.Original, service, spec.Sync.Policy.SelfHeal, s.global)
		if err != nil {
			file.Close(ctx)
			return proposal{}, err
		}
		return proposal{file: file, state: state, updated: updated}, nil
	}
	return proposal{}, fmt.Errorf("GitOps stack %q has no Compose file", stack)
}

var invalidBranchPart = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func branchPart(value string) string {
	return strings.Trim(invalidBranchPart.ReplaceAllString(value, "-"), ".-")
}
