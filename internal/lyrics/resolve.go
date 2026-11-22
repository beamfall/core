package lyrics

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxSidecarBytes bounds a sidecar read.
const maxSidecarBytes = 1 << 20

// Source names where a document came from.
const (
	SourceSidecarLRC = "sidecar-lrc"
	SourceEmbedded   = "embedded"
	SourceMetadata   = "metadata"
	SourceSidecarTxt = "sidecar-txt"
)

var errNotRegular = errors.New("lyrics: not a regular file")

// Resolve finds the lyrics for the audio file at primary. fieldText is the
// lyrics a plugin stored for the item, if any. Sources are tried in order:
// a same-name .lrc sidecar, an embedded SYLT frame, fieldText, an embedded
// USLT frame, then a same-name .txt sidecar. Synced sources come first so a
// timed copy wins over a plain one. It returns an empty source when nothing
// is found.
func Resolve(primary, fieldText string) (Document, string) {
	if primary != "" {
		if doc := Parse(readSidecar(primary, ".lrc")); !doc.Empty() {
			return doc, SourceSidecarLRC
		}
	}
	var tag embedded
	if primary != "" {
		tag = readEmbedded(primary)
		if !tag.synced.Empty() {
			return tag.synced, SourceEmbedded
		}
	}
	if doc := Parse(fieldText); !doc.Empty() {
		return doc, SourceMetadata
	}
	if doc := Parse(tag.unsynced); !doc.Empty() {
		return doc, SourceEmbedded
	}
	if primary != "" {
		if doc := Parse(readSidecar(primary, ".txt")); !doc.Empty() {
			return doc, SourceSidecarTxt
		}
	}
	return Document{}, ""
}

func readEmbedded(primary string) embedded {
	file, err := openRegular(filepath.Dir(primary), filepath.Base(primary))
	if err != nil {
		return embedded{}
	}
	defer file.Close()
	tag, _ := readID3(file)
	return tag
}

// readSidecar returns the text of the file beside primary with the same stem
// and the given extension, or "" when it is missing, not a regular file or too
// large.
func readSidecar(primary, ext string) string {
	base := filepath.Base(primary)
	name := strings.TrimSuffix(base, filepath.Ext(base)) + ext
	if name == base {
		return ""
	}
	file, err := openRegular(filepath.Dir(primary), name)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > maxSidecarBytes {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSidecarBytes))
	if err != nil {
		return ""
	}
	return string(data)
}

// openRegular opens name inside dir through an os.Root, so the read cannot
// leave the directory through a symlink, and refuses anything but a regular
// file that is still the entry it checked.
func openRegular(dir, name string) (*os.File, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !entry.Mode().IsRegular() {
		return nil, errNotRegular
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
		_ = file.Close()
		return nil, errNotRegular
	}
	return file, nil
}
