package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// CORE-2583 restores the resumable new-Media catch-up requirement: a purge
// between the scan watermark and import must not hide the new Media.
func TestMediaInsertionWatermarkSurvivesPurge(t *testing.T) {
	for _, captureAfterPurge := range []bool{false, true} {
		t.Run(fmt.Sprintf("capture-after-purge-%v", captureAfterPurge), func(t *testing.T) {
			st := openSeeded(t)
			ctx := context.Background()
			importMedia := func(name string) Media {
				t.Helper()
				m, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
					LibraryID: "library-local", Path: "/media/" + name + ".mp4",
					Title: name, Container: "mp4", Codec: "h264", ContentFingerprint: "sha256:" + name,
				})
				if err != nil {
					t.Fatal(err)
				}
				return m
			}
			rowID := func(id MediaID) int64 {
				t.Helper()
				var row int64
				if err := st.catalogR.QueryRowContext(ctx, `SELECT rowid FROM media WHERE id = ?`, id).Scan(&row); err != nil {
					t.Fatal(err)
				}
				return row
			}
			old := importMedia("purged")
			oldRow := rowID(old.ID)
			floor, err := st.MaxMediaInsertSeq(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.TrashMedia(ctx, old.ID, LibraryFilter{AllLibraries: true, Revealed: true}); err != nil {
				t.Fatal(err)
			}
			if count, err := st.PurgeTrashedBefore(ctx, time.Now().Add(time.Hour)); err != nil || count != 1 {
				t.Fatalf("purge = %d, %v; want 1", count, err)
			}
			if captureAfterPurge {
				postPurge, err := st.MaxMediaInsertSeq(ctx)
				if err != nil || postPurge != floor {
					t.Fatalf("purge lowered watermark: %d, %v; want %d", postPurge, err, floor)
				}
				floor = postPurge
			}
			fresh := importMedia("replacement")
			if freshRow := rowID(fresh.ID); freshRow != oldRow {
				t.Fatalf("fixture did not reuse rowid: %d != %d", freshRow, oldRow)
			}
			latest, err := st.MaxMediaInsertSeq(ctx)
			if err != nil || latest <= floor {
				t.Fatalf("latest = %d, %v; must exceed floor %d", latest, err, floor)
			}
			refs, err := st.MediaIDsAfterInsertSeq(ctx, floor, 10)
			if err != nil || len(refs) != 1 || refs[0].ID != fresh.ID {
				t.Fatalf("after floor = %+v, %v; want %s", refs, err, fresh.ID)
			}
		})
	}
}

func TestMediaInsertSeqWatermarkAndPagination(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	floor, err := st.MaxMediaInsertSeq(ctx)
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
		page, err := st.MediaIDsAfterInsertSeq(ctx, after, 2)
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
			if ref.InsertSeq <= after {
				t.Fatalf("sequence %d did not advance past %d", ref.InsertSeq, after)
			}
			got = append(got, ref.ID)
			after = ref.InsertSeq
		}
	}
	if !equalMediaIDs(got, want) {
		t.Fatalf("insertion order = %v, want %v", got, want)
	}
	if maxID, err := st.MaxMediaInsertSeq(ctx); err != nil || maxID != after {
		t.Fatalf("watermark = %d, %v, want %d", maxID, err, after)
	}
	for _, limit := range []int{0, -1} {
		if _, err := st.MediaIDsAfterInsertSeq(ctx, 0, limit); err == nil {
			t.Fatalf("accepted limit %d", limit)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := st.MaxMediaInsertSeq(canceled); err == nil {
		t.Fatal("watermark ignored cancellation")
	}
	if _, err := st.MediaIDsAfterInsertSeq(canceled, 0, 1); err == nil {
		t.Fatal("pagination ignored cancellation")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MaxMediaInsertSeq(ctx); err == nil {
		t.Fatal("watermark accepted closed store")
	}
	if _, err := st.MediaIDsAfterInsertSeq(ctx, 0, 1); err == nil {
		t.Fatal("pagination accepted closed store")
	}
}

func TestMediaInsertSeqEmptyCatalog(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if floor, err := st.MaxMediaInsertSeq(ctx); err != nil || floor != 0 {
		t.Fatalf("empty watermark = %d, %v", floor, err)
	}
	if refs, err := st.MediaIDsAfterInsertSeq(ctx, 0, 5); err != nil || len(refs) != 0 {
		t.Fatalf("empty page = %v, %v", refs, err)
	}
}
