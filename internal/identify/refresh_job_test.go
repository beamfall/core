package identify

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

func TestNewRefreshJobDedupesPerLibrary(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	job, err := NewRefreshJob("library-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if job.Kind != RefreshJobKind || job.Class != store.JobClassBackground || job.DedupeKey != "library-a" || !job.CreatedAt.Equal(now) {
		t.Fatalf("job = %+v", job)
	}
	// Operations reads the Library target from the payload's library_id.
	var payload map[string]any
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["library_id"] != "library-a" {
		t.Fatalf("payload = %s", job.Payload)
	}
	if _, err := NewRefreshJob("", now); err == nil {
		t.Fatal("NewRefreshJob without a Library succeeded")
	}
}

func TestRefreshWithoutProvenanceReaderHasNothingToRefresh(t *testing.T) {
	service := NewService(nil, nil, nil)
	if err := service.Refresh(t.Context(), "media-a"); err != ErrNothingToRefresh {
		t.Fatalf("Refresh = %v, want ErrNothingToRefresh", err)
	}
}
