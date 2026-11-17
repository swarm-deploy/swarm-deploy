package git

import (
	"errors"
	"os"

	"github.com/go-git/go-billy/v5"
)

var errStatusSpecialFile = errors.New("unsupported special file in Git status")

func openStatusFile(filesystem billy.Filesystem, root *os.Root, name string) (*os.File, error) {
	// Use go-git's path validation before opening beneath the captured root.
	info, err := filesystem.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errStatusSpecialFile
	}
	file, err := root.OpenFile(name, statusOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	// The path can change between the initial stat and open. Check the opened
	// descriptor before go-git can read it.
	info, err = file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errStatusSpecialFile
	}
	return file, nil
}
