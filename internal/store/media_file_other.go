//go:build !unix

package store

import "os"

const mediaFileOpenFlags = os.O_RDONLY

func clearMediaFileNonblock(*os.File) error { return nil }
