//go:build !unix

package git

import "os"

const statusOpenFlags = os.O_RDONLY
