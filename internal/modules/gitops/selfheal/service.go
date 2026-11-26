package selfheal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/githosting"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// ErrPolicyChanged indicates that publication needs a new plan for the current policy.
var ErrPolicyChanged = errors.New("self-heal policy changed after preparation. Prepare a new plan")

// Service proposes changes to service self-heal in the configured Git write repository.
type Service struct {
	stacks         config.StackProvider
	global         bool
	spec           config.GitSpec
	dataDir        string
	repo           githosting.RepositoryReference
	changeRequests githosting.ChangeRequestCreator
}

// Result describes the pushed branch and the optional pull request.
type Result struct {
	// Branch is the pushed proposal branch.
	Branch string `json:"branch"`
	// BranchURL is the browser URL of the branch, when available.
	BranchURL string `json:"branchUrl,omitempty"`
	// PullRequestURL is the URL returned by the configured hosting provider.
	PullRequestURL string `json:"mergeRequestUrl,omitempty"`
	// PullRequestNumber is the number returned by the configured hosting provider.
	PullRequestNumber int `json:"pullRequestNumber,omitempty"`
	// PullRequestFailed indicates that the branch was pushed but opening a PR failed.
	PullRequestFailed bool `json:"pullRequestFailed,omitempty"`
	// PullRequestError contains a safe diagnostic when opening a PR failed.
	PullRequestError string `json:"pullRequestError,omitempty"`
	// AlreadyEnabled identifies the policy source when publication is unnecessary.
	AlreadyEnabled string `json:"alreadyEnabled,omitempty"`
	// AlreadyDisabled identifies the policy source when publication is unnecessary.
	AlreadyDisabled string `json:"alreadyDisabled,omitempty"`
}

// New creates the self-heal proposal service with an isolated Git write path.
func New(
	stacks config.StackProvider, global bool, spec config.GitSpec, dataDir string,
) (*Service, error) {
	write, token := spec.ForPush()
	repo, changeRequests, err := githosting.ResolvePushRepository(write.Repository, token)
	if err != nil {
		return nil, fmt.Errorf("initialize push repository: %w", err)
	}
	return &Service{
		stacks: stacks, global: global, spec: write, dataDir: dataDir,
		repo: repo, changeRequests: changeRequests,
	}, nil
}

// Preview reads policy and checks whether the requested self-heal override is possible.
func (s *Service) Preview(ctx context.Context, stack, service string, desiredEnabled bool) (State, error) {
	change, err := s.prepare(ctx, stack, service, desiredEnabled)
	if err != nil {
		return State{}, err
	}
	defer change.file.Close(ctx)
	return change.state, nil
}

// Publish rechecks the policy observed during preparation on a fresh checkout before publishing.
func (s *Service) Publish(
	ctx context.Context, stack, service string, expected State, desiredEnabled bool, author string,
) (Result, error) {
	change, err := s.prepare(ctx, stack, service, desiredEnabled)
	if err != nil {
		return Result{}, err
	}
	defer change.file.Close(ctx)
	if change.state.Enabled == desiredEnabled {
		if desiredEnabled {
			return Result{AlreadyEnabled: change.state.Source}, nil
		}
		return Result{AlreadyDisabled: change.state.Source}, nil
	}
	if change.state != expected {
		return Result{}, ErrPolicyChanged
	}
	action, description := "enable", "Enable automatic drift repair for this service by setting its Compose deploy label."
	if !desiredEnabled {
		action = "disable"
		description = "Disable automatic drift repair for this service by setting its Compose deploy label."
	}
	branch, err := git.NewProposalBranch(stack, service, action, "self-heal")
	if err != nil {
		return Result{}, err
	}
	title := fmt.Sprintf("chore: %s self-heal for %s/%s", action, stack, service)
	_, err = change.file.Push(ctx, branch, title, author, change.updated)
	if err != nil {
		return Result{}, err
	}
	result := Result{Branch: branch, BranchURL: s.repo.BranchURL(branch)}
	if s.changeRequests != nil {
		var changeRequest githosting.ChangeRequest
		changeRequest, err = s.changeRequests.CreateChangeRequest(ctx, s.repo, s.spec.Branch, branch, title,
			description)
		if err != nil {
			result.PullRequestFailed = true
			result.PullRequestError = faults.SafeDiagnostic(err)
			slog.ErrorContext(ctx, "create pull request for self-heal proposal",
				"stack", stack, "service", service, "branch", branch,
				"error", result.PullRequestError)
		} else {
			result.PullRequestURL = changeRequest.URL
			result.PullRequestNumber = changeRequest.Number
		}
	}
	return result, nil
}

type proposal struct {
	file    *git.ProposalFile
	state   State
	updated []byte
}

func (s *Service) prepare(ctx context.Context, stack, service string, enabled bool) (proposal, error) {
	for _, spec := range s.stacks.Stacks() {
		if spec.Name != stack || spec.ComposeFile == "" {
			continue
		}
		file, err := git.OpenProposalFile(ctx, s.spec, s.dataDir, spec.ComposeFile)
		if err != nil {
			return proposal{}, err
		}
		state, updated, err := prepare(file.Original, service, spec.Sync.Policy.SelfHeal, s.global, enabled)
		if err != nil {
			file.Close(ctx)
			return proposal{}, err
		}
		return proposal{file: file, state: state, updated: updated}, nil
	}
	return proposal{}, fmt.Errorf("GitOps stack %q has no Compose file", stack)
}
