// Package imagepin owns Git reads, Compose image edits, and proposal publication
// for pinning a service image to a digest observed in Docker Swarm.
package imagepin

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

// ErrImageChanged indicates that the Compose image changed after preparation.
var ErrImageChanged = errors.New("compose image changed after preparation")

// Service proposes digest pins in the configured Git write repository.
type Service struct {
	stacks         config.StackProvider
	spec           config.GitSpec
	dataDir        string
	repo           githosting.RepositoryReference
	changeRequests githosting.ChangeRequestCreator
}

// Source is the image value read from the proposal base.
type Source struct {
	// Image preserves the semantic spelling of services.<service>.image in Compose.
	Image string
}

// Result describes the pushed branch and optional pull request.
type Result struct {
	// Branch is the pushed proposal branch.
	Branch string `json:"branch,omitempty"`
	// BranchURL is the browser URL of the branch, when available.
	BranchURL string `json:"branchUrl,omitempty"`
	// PullRequestURL is the URL returned by the configured hosting provider.
	PullRequestURL string `json:"pullRequestUrl,omitempty"`
	// PullRequestNumber is the number returned by the configured hosting provider.
	PullRequestNumber int `json:"pullRequestNumber,omitempty"`
	// PullRequestFailed indicates that the branch was pushed but opening a PR failed.
	PullRequestFailed bool `json:"pullRequestFailed,omitempty"`
	// PullRequestError contains a safe diagnostic when opening a PR failed.
	PullRequestError string `json:"pullRequestError,omitempty"`
	// AlreadyPinned reports that another commit already applied the confirmed image.
	AlreadyPinned bool `json:"alreadyPinned,omitempty"`
}

// New creates the image pin proposal service with an isolated Git write path.
func New(stacks config.StackProvider, spec config.GitSpec, dataDir string) (*Service, error) {
	write, token := spec.ForPush()
	repo, changeRequests, err := githosting.ResolvePushRepository(write.Repository, token)
	if err != nil {
		return nil, fmt.Errorf("initialize push repository: %w", err)
	}
	return &Service{
		stacks: stacks, spec: write, dataDir: dataDir, repo: repo, changeRequests: changeRequests,
	}, nil
}

// Preview reads the exact service image from the current write base.
func (s *Service) Preview(ctx context.Context, stack, service string) (Source, error) {
	file, err := s.open(ctx, stack)
	if err != nil {
		return Source{}, err
	}
	defer file.Close(ctx)
	image, err := inspect(file.Original, service)
	return Source{Image: image}, err
}

// Publish rechecks the image on a fresh checkout and publishes the confirmed edit.
func (s *Service) Publish(
	ctx context.Context, stack, service, expectedImage, proposedImage, author string,
) (Result, error) {
	file, err := s.open(ctx, stack)
	if err != nil {
		return Result{}, err
	}
	defer file.Close(ctx)
	current, err := inspect(file.Original, service)
	if err != nil {
		return Result{}, err
	}
	if current == proposedImage {
		return Result{AlreadyPinned: true}, nil
	}
	if current != expectedImage {
		return Result{}, ErrImageChanged
	}
	updated, err := setImage(file.Original, service, proposedImage)
	if err != nil {
		return Result{}, err
	}
	branch, err := git.NewProposalBranch(stack, service, "pin-image")
	if err != nil {
		return Result{}, err
	}
	title := fmt.Sprintf("chore: pin image for %s/%s", stack, service)
	if _, err = file.Push(ctx, branch, title, author, updated); err != nil {
		return Result{}, err
	}
	result := Result{Branch: branch, BranchURL: s.repo.BranchURL(branch)}
	if s.changeRequests != nil {
		description := fmt.Sprintf(
			"Pin the Compose image for `%s/%s` to the digest observed on all running Swarm tasks.", stack, service,
		)
		var changeRequest githosting.ChangeRequest
		changeRequest, err = s.changeRequests.CreateChangeRequest(
			ctx, s.repo, s.spec.Branch, branch, title, description,
		)
		if err != nil {
			result.PullRequestFailed = true
			result.PullRequestError = faults.SafeDiagnostic(err)
			slog.ErrorContext(ctx, "create pull request for image pin proposal",
				"stack", stack, "service", service, "branch", branch,
				"error", result.PullRequestError)
		} else {
			result.PullRequestURL = changeRequest.URL
			result.PullRequestNumber = changeRequest.Number
		}
	}
	return result, nil
}

func (s *Service) open(ctx context.Context, stack string) (*git.ProposalFile, error) {
	for _, spec := range s.stacks.Stacks() {
		if spec.Name == stack && spec.ComposeFile != "" {
			return git.OpenProposalFile(ctx, s.spec, s.dataDir, spec.ComposeFile)
		}
	}
	return nil, fmt.Errorf("GitOps stack %q has no Compose file", stack)
}
