package store

import (
	"testing"
)

// TestEncryptedIdentificationApplyLeaksNothing pins the current refusal: the
// identification write is shared-catalog only, so for an Encrypted Library's
// Media it fails and leaves no title, provenance or candidate ID in the shared
// catalog (CORE-0706: refuse rather than leak or fake success).
func TestEncryptedIdentificationApplyLeaksNothing(t *testing.T) {
	ctx := t.Context()
	st, media := seededEncryptedMedia(t, "identify-refusal")
	defer func() { _ = st.Close() }()
	title := "Private Identified Title 5b7e"
	err := st.ApplyMediaIdentification(ctx, media.ID, MediaMetadata{Title: &title},
		MediaMetadataProvenance{PluginID: "fixture", ScraperID: "fixture-scraper", CandidateID: "candidate-5b7e"}, nil)
	if err == nil {
		t.Fatal("identification of an Encrypted Library's Media reported success on the shared catalog")
	}
	var leaked int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM media_metadata_provenance WHERE media_id = ? OR candidate_id = 'candidate-5b7e')
		     + (SELECT COUNT(*) FROM media WHERE id = ? OR title = ?)
	`, media.ID, media.ID, title).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("shared catalog identification rows = %d, %v", leaked, err)
	}
}
