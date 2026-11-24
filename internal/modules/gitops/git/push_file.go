package git

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

// ProposalFile is a tracked file in an isolated checkout of the write branch.
type ProposalFile struct {
	// Original is the content at the base revision used for this proposal.
	Original []byte

	repo               *gogit.Repository
	dir, filename      string
	baseBranch, remote string
	deadline           time.Time
	auth               transport.AuthMethod
}

// OpenProposalFile checks out the write base. The caller must close the file to remove the checkout.
func OpenProposalFile(ctx context.Context, spec config.GitSpec, dataDir, filename string) (*ProposalFile, error) {
	filename = path.Clean(filename)
	if !filepath.IsLocal(filename) || filename == "." {
		return nil, errors.New("compose file path must be repository-relative")
	}
	auth, err := resolveAuthMethod(spec.Auth)
	if err != nil {
		return nil, fmt.Errorf("configure push authentication: %w", err)
	}
	dir, err := os.MkdirTemp(dataDir, "push-repo-")
	if err != nil {
		return nil, fmt.Errorf("create push checkout: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, gitPullTimeout(spec))
	defer cancel()
	deadline, _ := ctx.Deadline()
	file := &ProposalFile{
		dir: dir, filename: filename, baseBranch: spec.Branch, remote: spec.Repository,
		auth: auth, deadline: deadline,
	}
	file.repo, err = openRepository(ctx, dir, spec.Repository, spec.Branch, auth)
	if err != nil {
		file.Close(ctx)
		return nil, ClassifyPullError(err, spec.Repository, spec.Auth.Type)
	}
	file.Original, err = readTrackedFile(file.repo, filename)
	if err != nil {
		file.Close(ctx)
		return nil, err
	}
	return file, nil
}

// Close removes the disposable checkout.
func (f *ProposalFile) Close(ctx context.Context) {
	if err := os.RemoveAll(f.dir); err != nil {
		slog.WarnContext(ctx, "remove push checkout", slog.Any("err", err))
	}
}

// Push commits the edited file and publishes it on a proposal branch.
func (f *ProposalFile) Push(ctx context.Context, branch, message, author string, updated []byte) (string, error) {
	ref := plumbing.NewBranchReferenceName(branch)
	if branch == f.baseBranch || ref.Validate() != nil {
		return "", errors.New("invalid proposal branch")
	}
	worktree, err := f.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("open push worktree: %w", err)
	}
	if err = worktree.Checkout(&gogit.CheckoutOptions{Branch: ref, Create: true}); err != nil {
		return "", fmt.Errorf("create local proposal branch: %w", err)
	}
	if err = writeTrackedFile(f.dir, f.filename, updated); err != nil {
		return "", err
	}
	if _, err = worktree.Add(f.filename); err != nil {
		return "", fmt.Errorf("stage Compose change: %w", err)
	}
	commit, err := worktree.Commit(message, &gogit.CommitOptions{Author: &object.Signature{
		Name: author, Email: "swarm-deploy@localhost", When: time.Now(),
	}})
	if err != nil {
		return "", fmt.Errorf("commit Compose change: %w", err)
	}
	ctx, cancel := context.WithDeadline(ctx, f.deadline)
	defer cancel()
	if err = f.repo.PushContext(ctx, &gogit.PushOptions{
		Auth: f.auth, RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec(ref.String() + ":" + ref.String())},
		ProxyOptions: sshDeadlineProxyOptions(ctx, f.remote),
	}); err != nil {
		return "", errors.New("could not push proposal branch; check write access and whether the branch already exists")
	}
	return commit.String(), nil
}

func readTrackedFile(repo *gogit.Repository, filename string) ([]byte, error) {
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("read push base revision: %w", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("read push base commit: %w", err)
	}
	file, err := commit.File(filename)
	if err != nil {
		return nil, fmt.Errorf("read tracked Compose file: %w", err)
	}
	if file.Mode != filemode.Regular && file.Mode != filemode.Executable {
		return nil, errors.New("tracked Compose file must be a regular file")
	}
	original, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("read Compose file contents: %w", err)
	}
	return []byte(original), nil
}

func writeTrackedFile(checkout, filename string, updated []byte) error {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return fmt.Errorf("open push checkout: %w", err)
	}
	defer root.Close()
	// The file already exists in the checkout and WriteFile preserves its mode.
	if writeErr := root.WriteFile(filename, updated, 0); writeErr != nil {
		return fmt.Errorf("write Compose file: %w", writeErr)
	}
	return nil
}
