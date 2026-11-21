//go:build !unix

package store

import "os"

const mediaFileOpenFlags = os.O_RDONLY
