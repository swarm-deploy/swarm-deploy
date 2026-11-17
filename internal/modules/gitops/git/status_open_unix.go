//go:build unix

package git

import (
	"os"

	"golang.org/x/sys/unix"
)

// A FIFO can replace a regular file between stat and open.
const statusOpenFlags = os.O_RDONLY | unix.O_NONBLOCK
