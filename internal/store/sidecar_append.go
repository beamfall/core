package store

import (
	"context"
	"strings"

	"github.com/beamfall/core/internal/serverrole"
)

// AddMediaFileSidecarCaption records one caption sidecar for a content
// fingerprint without a rescan (CORE-1690): a subtitle downloaded beside the
// media file plays at once. It upserts on (fingerprint, rel_path), the same key
// replaceMediaFileSidecarsTx uses, so the next scan, which finds the same file
// on disk, replaces the row with what it discovers rather than duplicating it.
// The write goes to the default catalog plane; callers refuse encrypted
// Libraries, whose rows live on their own plane.
func (s *Store) AddMediaFileSidecarCaption(ctx context.Context, fingerprint string, caption MediaFileSidecarCaption) error {
	if err := s.guardAuthority(serverrole.FaceCatalogRow); err != nil {
		return err
	}
	if strings.TrimSpace(fingerprint) == "" {
		return ValidationError("content fingerprint is required")
	}
	if caption.RelPath == "" || strings.ContainsAny(caption.RelPath, "/\\") {
		return ValidationError("sidecar path must be a file name beside the media file")
	}
	if caption.Format == "" {
		return ValidationError("sidecar format is required")
	}
	kind := caption.Kind
	if kind == "" {
		kind = SidecarKindCaption
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return err
	}
	_, err := s.catalogW.ExecContext(ctx, `
		INSERT INTO media_file_sidecar_caption(
		  content_fingerprint, rel_path, kind, format, language, is_forced, is_sdh)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(content_fingerprint, rel_path) DO UPDATE SET
		  kind = excluded.kind, format = excluded.format, language = excluded.language,
		  is_forced = excluded.is_forced, is_sdh = excluded.is_sdh
	`, fingerprint, caption.RelPath, kind, caption.Format, caption.Language, boolInt(caption.Forced), boolInt(caption.SDH))
	return err
}
