package qbittorrent

import (
	"encoding/json"
	"os"
)

// Sidecar is the Parse Hints sidecar written beside each linked payload.
// It carries the release name (usually far more parseable than
// the payload filename), the routing category, and the ingest provenance, so
// the standard identification pipeline can prefer the release name and record
// where the file entered from. This package writes the contract artifact; the
// pipeline consumes it (consumer-side wiring is separate from ingest).
type Sidecar struct {
	// Version pins the sidecar schema for forward-compatible readers.
	Version int `json:"version"`
	// Source is the ingest provenance (e.g. "qbittorrent").
	Source string `json:"source"`
	// ReleaseName is the primary Parse Hint: the torrent release name.
	ReleaseName string `json:"release_name"`
	// Category is the routing key the file was imported under.
	Category string `json:"category"`
	// TorrentHash is the originating torrent's info-hash (provenance trail).
	TorrentHash string `json:"torrent_hash"`
	// MemberPath is the file's path within a multi-file payload, "" for single.
	MemberPath string `json:"member_path,omitempty"`
}

// writeSidecar writes the Parse Hints sidecar next to a linked payload at
// <dst><SidecarSuffix>. memberPath is the file's path within the torrent for
// multi-file payloads.
func writeSidecar(dst string, item Item, memberPath string) error {
	sidecar := Sidecar{
		Version:     1,
		Source:      IngestProvenanceSource,
		ReleaseName: item.ReleaseName,
		Category:    item.Category,
		TorrentHash: item.ID,
		MemberPath:  memberPath,
	}
	data, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dst+SidecarSuffix, data, 0o644)
}
