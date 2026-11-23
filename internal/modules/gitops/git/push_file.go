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
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

// PushFileChange edits one tracked file on a new branch in a disposable checkout.
func PushFileChange(
	ctx context.Context,
	spec config.GitSpec,
	dataDir, branch, filename, message, author string,
	edit func(context.Context, []byte) ([]byte, error),
) (string, error) {
	ref := plumbing.NewBranchReferenceName(branch)
	if branch == spec.Branch || ref.Validate() != nil {
		return "", errors.New("invalid proposal branch")
	}
	filename = path.Clean(filename)
	if !filepath.IsLocal(filename) || filename == "." {
		return "", errors.New("compose file path must be repository-relative")
	}
	checkout, err := os.MkdirTemp(dataDir, "push-repo-")
	if err != nil {
		return "", fmt.Errorf("create push checkout: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(checkout); cleanupErr != nil {
			slog.WarnContext(ctx, "remove push checkout", slog.Any("err", cleanupErr))
		}
	}()

	auth, err := resolveAuthMethod(spec.Auth)
	if err != nil {
		return "", fmt.Errorf("configure push authentication: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, gitPullTimeout(spec))
	defer cancel()

	repo, err := openRepository(ctx, checkout, spec.Repository, spec.Branch, auth)
	if err != nil {
		return "", ClassifyPullError(err, spec.Repository, spec.Auth.Type)
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("open push worktree: %w", err)
	}
	if checkoutErr := worktree.Checkout(&gogit.CheckoutOptions{Branch: ref, Create: true}); checkoutErr != nil {
		return "", fmt.Errorf("create local proposal branch: %w", checkoutErr)
	}
	if editErr := editTrackedFile(ctx, repo, checkout, filename, edit); editErr != nil {
		return "", editErr
	}
	if _, addErr := worktree.Add(filename); addErr != nil {
		return "", fmt.Errorf("stage Compose change: %w", addErr)
	}
	commit, err := worktree.Commit(message, &gogit.CommitOptions{Author: &object.Signature{
		Name: author, Email: "swarm-deploy@localhost", When: time.Now(),
	}})
	if err != nil {
		return "", fmt.Errorf("commit Compose change: %w", err)
	}
	if pushErr := repo.PushContext(ctx, &gogit.PushOptions{
		Auth: auth, RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec(ref.String() + ":" + ref.String())},
		ProxyOptions: sshDeadlineProxyOptions(ctx, spec.Repository),
	}); pushErr != nil {
		return "", errors.New("could not push proposal branch; check write access and whether the branch already exists")
	}
	return commit.String(), nil
}

func editTrackedFile(
	ctx context.Context, repo *gogit.Repository, checkout, filename string,
	edit func(context.Context, []byte) ([]byte, error),
) error {
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("read push base revision: %w", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return fmt.Errorf("read push base commit: %w", err)
	}
	file, err := commit.File(filename)
	if err != nil {
		return fmt.Errorf("read tracked Compose file: %w", err)
	}
	if file.Mode != filemode.Regular && file.Mode != filemode.Executable {
		return errors.New("tracked Compose file must be a regular file")
	}
	original, err := file.Contents()
	if err != nil {
		return fmt.Errorf("read Compose file contents: %w", err)
	}
	updated, err := edit(ctx, []byte(original))
	if err != nil {
		return err
	}
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
