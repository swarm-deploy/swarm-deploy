package scheduler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// snapshotCheckout takes a private copy of the checkout before another Git pull.
// Symlinks and Git metadata are excluded to avoid reading outside the checkout.
func snapshotCheckout(ctx context.Context, source, root string) (string, error) {
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(sourceAbs, rootAbs)
	if err != nil {
		return "", err
	}
	if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return "", errors.New("deployment snapshot directory must be outside the Git checkout")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(root, uuid.NewString())
	if err := os.Mkdir(target, 0o700); err != nil {
		return "", err
	}
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, readErr error) error {
		if readErr != nil {
			return readErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in Git checkout is unsupported: %s", relative)
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.Mkdir(destination, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported checkout file: %s", relative)
		}
		return copySnapshotFile(path, destination)
	})
	if err != nil {
		_ = os.RemoveAll(target)
		return "", err
	}
	return target, nil
}

func copySnapshotFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
