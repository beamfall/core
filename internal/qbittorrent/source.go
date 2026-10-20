package qbittorrent

import (
	"context"
	"path/filepath"
	"strings"
)

// Source is the source-adapter seam. The ingest orchestrator
// consumes completed download Items from a Source without knowing whether they
// came from the qBittorrent Web API, a watch-folder, or *arr coordination —
// later adapters land by implementing this interface, no redesign.
//
// Per ADR-0016 the interface is defined here at the consumer; the qBittorrent
// adapter (qbtSource) is the v1 implementation.
type Source interface {
	// Categories returns the routing keys the Source can label items with, so
	// the operator's category->Library map can be validated against reality.
	Categories(ctx context.Context) ([]string, error)
	// CompletedItems returns every completed download the Source knows about.
	// Completion is the Source's own truth (qBittorrent reports it directly);
	// the orchestrator never guesses when a download is done.
	CompletedItems(ctx context.Context) ([]Item, error)
}

// Item is a completed download offered by a Source: the routing key, the parse
// hint, the idempotency key, and the on-disk payload to relocate.
type Item struct {
	// ID is the stable idempotency key (the torrent info-hash for qBittorrent).
	// Restarts never re-import a processed ID (§5).
	ID string
	// ReleaseName is the parseable release name — the primary Parse Hint (§4).
	ReleaseName string
	// Category is the routing key (§3); empty means uncategorized -> unrouted.
	Category string
	// ContentPath is the absolute path to the payload: a single file, or a
	// directory for multi-file downloads.
	ContentPath string
	// Files are the payload's member files (relative to ContentPath when
	// ContentPath is a directory). Empty for single-file payloads.
	Files []ItemFile
}

// ItemFile is one member file of a multi-file Item.
type ItemFile struct {
	// RelPath is the file path relative to the Item's content root.
	RelPath string
	// Size is the file size in bytes.
	Size int64
}

// qbtSource adapts a qBittorrent Client to the Source seam.
type qbtSource struct {
	client *Client
}

// NewSource wraps a Client as a Source.
func NewSource(client *Client) Source {
	return &qbtSource{client: client}
}

func (s *qbtSource) Categories(ctx context.Context) ([]string, error) {
	categories, err := s.client.Categories(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(categories))
	for _, category := range categories {
		names = append(names, category.Name)
	}
	return names, nil
}

func (s *qbtSource) CompletedItems(ctx context.Context) ([]Item, error) {
	torrents, err := s.client.CompletedTorrents(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(torrents))
	for _, torrent := range torrents {
		// Defensive: trust the "completed" filter but skip anything that does
		// not actually report full progress — an in-flight payload must never
		// be hardlinked.
		if torrent.Progress < 1 {
			continue
		}
		item := Item{
			ID:          strings.ToLower(strings.TrimSpace(torrent.Hash)),
			ReleaseName: torrent.Name,
			Category:    strings.TrimSpace(torrent.Category),
			ContentPath: filepath.Clean(torrent.ContentPath),
		}
		if item.ID == "" || item.ContentPath == "" || item.ContentPath == "." {
			continue
		}
		// Member files only matter for multi-file (directory) payloads; fetch
		// them lazily so single-file imports avoid an extra request.
		files, err := s.client.TorrentFiles(ctx, torrent.Hash)
		if err != nil {
			return nil, err
		}
		if len(files) > 1 {
			for _, file := range files {
				item.Files = append(item.Files, ItemFile{RelPath: file.Name, Size: file.Size})
			}
		}
		items = append(items, item)
	}
	return items, nil
}
