package git

import (
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// maxSymlinkHops bounds symlink resolution like the Linux kernel does, so cycles fail instead of looping.
const maxSymlinkHops = 40

var errExternalPath = errors.New("path escapes the repository")

// resolveRevisionFile walks a repository-relative path through tree and returns the blob of the regular
// file it designates. Symlinks, in intermediate directories as well as the final file, are resolved
// against the same revision; a resolution leaving the repository is rejected.
func (r *GoGitRepository) resolveRevisionFile(tree *object.Tree, repositoryPath string) (plumbing.Hash, error) {
	if repositoryPath == "" || strings.ContainsRune(repositoryPath, 0) {
		return plumbing.ZeroHash, errors.New("invalid repository file path")
	}
	if path.IsAbs(repositoryPath) || filepath.IsAbs(repositoryPath) || filepath.VolumeName(repositoryPath) != "" {
		return plumbing.ZeroHash, errExternalPath
	}
	resolver := &revisionPathResolver{
		repository: r, tree: tree, pending: strings.Split(filepath.ToSlash(repositoryPath), "/"),
	}
	for len(resolver.pending) > 0 {
		hash, found, stepErr := resolver.step()
		if stepErr != nil || found {
			return hash, stepErr
		}
	}

	return plumbing.ZeroHash, errors.New("path designates a directory")
}

// revisionPathResolver resolves a path component by component, like path resolution in a kernel.
type revisionPathResolver struct {
	repository *GoGitRepository
	tree       *object.Tree
	// pending holds components still to resolve; symlink targets are prepended to it.
	pending []string
	// resolved holds the components of the directory resolved so far.
	resolved []string
	hops     int
}

// step consumes one pending component and reports the blob once the path designates a regular file.
func (res *revisionPathResolver) step() (plumbing.Hash, bool, error) {
	name := res.pending[0]
	res.pending = res.pending[1:]
	switch name {
	case "", ".":
		return plumbing.ZeroHash, false, nil
	case "..":
		return plumbing.ZeroHash, false, res.parent()
	}

	current := append(slices.Clone(res.resolved), name)
	currentPath := path.Join(current...)
	entry, err := res.tree.FindEntry(currentPath)
	if err != nil {
		return plumbing.ZeroHash, false, fmt.Errorf("%s: %w", currentPath, object.ErrFileNotFound)
	}

	switch {
	case entry.Mode == filemode.Symlink:
		return plumbing.ZeroHash, false, res.followSymlink(currentPath, entry.Hash)
	case entry.Mode == filemode.Dir:
		if len(res.pending) == 0 {
			return plumbing.ZeroHash, false, fmt.Errorf("%s is a directory", currentPath)
		}
		res.resolved = current
		return plumbing.ZeroHash, false, nil
	case entry.Mode.IsFile():
		if len(res.pending) > 0 {
			return plumbing.ZeroHash, false, fmt.Errorf("%s is not a directory", currentPath)
		}
		return entry.Hash, true, nil
	default:
		return plumbing.ZeroHash, false, fmt.Errorf("%s has unsupported entry type %s", currentPath, entry.Mode)
	}
}

func (res *revisionPathResolver) parent() error {
	if len(res.resolved) == 0 {
		return errExternalPath
	}
	res.resolved = res.resolved[:len(res.resolved)-1]
	return nil
}

// followSymlink continues resolution at the link target within the same revision.
func (res *revisionPathResolver) followSymlink(linkPath string, target plumbing.Hash) error {
	res.hops++
	if res.hops > maxSymlinkHops {
		return fmt.Errorf("%s: too many levels of symbolic links", linkPath)
	}
	content, err := res.repository.readBlob(target)
	if err != nil {
		return fmt.Errorf("read symlink %s: %w", linkPath, err)
	}

	targetPath := string(content)
	if path.IsAbs(targetPath) || filepath.IsAbs(targetPath) || filepath.VolumeName(targetPath) != "" {
		return errExternalPath
	}
	if targetPath == "" || strings.ContainsRune(targetPath, 0) {
		return fmt.Errorf("%s: invalid symbolic link target", linkPath)
	}
	// A relative target is resolved from the directory holding the link.
	res.pending = append(strings.Split(filepath.ToSlash(targetPath), "/"), res.pending...)
	return nil
}

func (r *GoGitRepository) readBlob(hash plumbing.Hash) ([]byte, error) {
	blob, err := r.repository.BlobObject(hash)
	if err != nil {
		return nil, err
	}
	reader, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
}
