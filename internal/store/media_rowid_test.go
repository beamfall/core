package store

import (
	"context"
	"fmt"
	"testing"
)

func TestMediaRowIDWatermarkAndPagination(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	floor, err := st.MaxMediaRowID(ctx)
	if err != nil || floor == 0 {
		t.Fatalf("seed watermark = %d, %v", floor, err)
	}
	var want []MediaID
	for i := range 5 {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: "library-local", Path: fmt.Sprintf("/media/rowid-%d.mp4", i),
			Title: fmt.Sprintf("Item %d", i), Container: "mp4", Codec: "h264",
			ContentFingerprint: fmt.Sprintf("sha256:rowid-%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, media.ID)
	}
	var got []MediaID
	after := floor
	for {
		page, err := st.MediaIDsAfterRowID(ctx, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if len(page) > 2 {
			t.Fatalf("page size = %d", len(page))
		}
		for _, ref := range page {
			if ref.RowID <= after {
				t.Fatalf("rowid %d did not advance past %d", ref.RowID, after)
			}
			got = append(got, ref.ID)
			after = ref.RowID
		}
	}
	if !equalMediaIDs(got, want) {
		t.Fatalf("insertion order = %v, want %v", got, want)
	}
	if maxID, err := st.MaxMediaRowID(ctx); err != nil || maxID != after {
		t.Fatalf("watermark = %d, %v, want %d", maxID, err, after)
	}
	for _, limit := range []int{0, -1} {
		if _, err := st.MediaIDsAfterRowID(ctx, 0, limit); err == nil {
			t.Fatalf("accepted limit %d", limit)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := st.MaxMediaRowID(canceled); err == nil {
		t.Fatal("watermark ignored cancellation")
	}
	if _, err := st.MediaIDsAfterRowID(canceled, 0, 1); err == nil {
		t.Fatal("pagination ignored cancellation")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MaxMediaRowID(ctx); err == nil {
		t.Fatal("watermark accepted closed store")
	}
	if _, err := st.MediaIDsAfterRowID(ctx, 0, 1); err == nil {
		t.Fatal("pagination accepted closed store")
	}
}

func TestMediaRowIDEmptyCatalog(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if floor, err := st.MaxMediaRowID(ctx); err != nil || floor != 0 {
		t.Fatalf("empty watermark = %d, %v", floor, err)
	}
	if refs, err := st.MediaIDsAfterRowID(ctx, 0, 5); err != nil || len(refs) != 0 {
		t.Fatalf("empty page = %v, %v", refs, err)
	}
}
