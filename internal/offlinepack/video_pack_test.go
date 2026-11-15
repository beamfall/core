package offlinepack

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// setFixtureClass re-imports a fixture Media's primary file under class.
func setFixtureClass(t *testing.T, st *store.Store, id store.MediaID, class store.MediaClass) {
	t.Helper()
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	rows, err := st.VisibleMediaBatch(context.Background(), []store.MediaID{id}, filter)
	if err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForMedia(context.Background(), id, filter)
	if err != nil || len(files) == 0 {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	if _, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: rows[id].LibraryID, Path: files[0].Path, Title: rows[id].Title, Container: "mkv", Codec: "hevc",
		Class: class, ContentFingerprint: files[0].ContentFingerprint,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = st.VisibleMediaBatch(context.Background(), []store.MediaID{id}, filter)
	if err != nil || rows[id].Class != class {
		t.Fatalf("class of %s = %q (%v), want %q", id, rows[id].Class, err, class)
	}
}

// fakeTranscoder writes fixed bytes per quality and records each call.
type fakeTranscoder struct {
	out     map[string][]byte
	err     error
	calls   []string
	streams [][]store.MediaFileStream
}

func (f *fakeTranscoder) transcode(_ context.Context, src, dst, quality string, streams []store.MediaFileStream) error {
	f.calls = append(f.calls, filepath.Base(src)+"@"+quality)
	f.streams = append(f.streams, streams)
	if f.err != nil {
		_ = os.WriteFile(dst, []byte("partial"), 0o600)
		return f.err
	}
	return os.WriteFile(dst, f.out[quality], 0o600)
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func packRootEntries(t *testing.T, svc *Service) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(svc.packRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, filepath.Base(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestNormalizeQuality(t *testing.T) {
	for in, want := range map[string]string{"": QualityOriginal, "original": QualityOriginal, "high": QualityHigh, "medium": QualityMedium, "low": QualityLow} {
		got, err := NormalizeQuality(in)
		if err != nil || got != want {
			t.Fatalf("NormalizeQuality(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"ultra", "High", " low"} {
		if _, err := NormalizeQuality(in); !errors.Is(err, ErrUnknownQuality) {
			t.Fatalf("NormalizeQuality(%q) err = %v, want ErrUnknownQuality", in, err)
		}
	}
}

func TestBuildVideoPackAtOriginalQuality(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha"})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	claims := mustManifestClaims(t, svc, pack.Manifest)
	original := []byte("bytes-media-alpha")
	if pack.State != store.OfflinePackReady || len(claims.Items) != 1 || claims.Items[0].SHA256 != digestOf(original) {
		t.Fatalf("pack = %+v items = %+v, want the original bytes sealed", pack, claims.Items)
	}
}

func TestBuildRefusesImageMedia(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassImage)
	scope := scopeFor(t, authService, "session-default")
	_, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha"})
	if err == nil || !strings.Contains(err.Error(), "audio and video") {
		t.Fatalf("BuildPack err = %v, want the class refusal", err)
	}
	if entries := packRootEntries(t, svc); len(entries) != 0 {
		t.Fatalf("pack root holds %v after a refused build", entries)
	}
}

func TestBuildTranscodesVideoAndSealsTheTranscode(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	transcoded := []byte("transcoded-medium-bytes-longer-than-source")
	fake := &fakeTranscoder{out: map[string][]byte{QualityMedium: transcoded}}
	svc.SetTranscoder(fake.transcode)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityMedium,
	})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "media-alpha.bin@medium" {
		t.Fatalf("transcoder calls = %v, want the video item once", fake.calls)
	}
	claims := mustManifestClaims(t, svc, pack.Manifest)
	if len(claims.Items) != 1 {
		t.Fatalf("items = %+v, want 1", claims.Items)
	}
	video := claims.Items[0]
	if video.Size != uint64(len(transcoded)) || video.SHA256 != digestOf(transcoded) {
		t.Fatalf("video item = %+v, want the transcode's size and digest", video)
	}
	if pack.BytesTotal != int64(len(transcoded)) {
		t.Fatalf("BytesTotal = %d, want %d", pack.BytesTotal, len(transcoded))
	}
	for _, name := range packRootEntries(t, svc) {
		if strings.HasSuffix(name, ".src.mp4") {
			t.Fatalf("temporary transcode %s left in the pack directory", name)
		}
	}
}

func TestBuildKeepsAudioBytesAtEveryQuality(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	fake := &fakeTranscoder{out: map[string][]byte{QualityLow: []byte("never")}}
	svc.SetTranscoder(fake.transcode)
	scope := scopeFor(t, authService, "session-default")
	pack, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityLow})
	if err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	claims := mustManifestClaims(t, svc, pack.Manifest)
	if len(fake.calls) != 0 || claims.Items[0].SHA256 != digestOf([]byte("bytes-media-alpha")) {
		t.Fatalf("audio item = %+v, transcoder calls %v; want original bytes, no transcode", claims.Items[0], fake.calls)
	}
}

func TestBuildTranscodeFailuresLeaveNoPack(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*Service, *fakeTranscoder)
		wantErr string
	}{
		{"no transcoder", func(*Service, *fakeTranscoder) {}, "transcoder is not configured"},
		{"transcoder error", func(s *Service, f *fakeTranscoder) {
			f.err = errors.New("encoder exited 1")
			s.SetTranscoder(f.transcode)
		}, "encoder exited 1"},
		{"empty output", func(s *Service, f *fakeTranscoder) { s.SetTranscoder(f.transcode) }, "produced no bytes"},
		{"over the byte budget", func(s *Service, f *fakeTranscoder) {
			f.out = map[string][]byte{QualityLow: []byte("0123456789")}
			s.SetBuildByteBudget(9)
			s.SetTranscoder(f.transcode)
		}, "byte budget"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, authService, st, _ := newFixture(t)
			setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
			tc.setup(svc, &fakeTranscoder{})
			scope := scopeFor(t, authService, "session-default")
			_, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityLow})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("BuildPack err = %v, want %q", err, tc.wantErr)
			}
			if entries := packRootEntries(t, svc); len(entries) != 0 {
				t.Fatalf("pack root holds %v after a failed build", entries)
			}
			packs, err := st.OfflinePacksForProfile(context.Background(), scope.ProfileID)
			if err != nil || len(packs) != 0 {
				t.Fatalf("packs = %+v, %v; want none", packs, err)
			}
		})
	}
}

func TestBuildTranscodeCancellationStaysVisible(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	ctx, cancel := context.WithCancel(context.Background())
	svc.SetTranscoder(func(context.Context, string, string, string, []store.MediaFileStream) error {
		cancel()
		return errors.New("ffmpeg convert: exit status 255")
	})
	scope := scopeFor(t, authService, "session-default")
	_, err := svc.BuildPack(ctx, scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityLow})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildPack err = %v, want context.Canceled so the job yields", err)
	}
	if entries := packRootEntries(t, svc); len(entries) != 0 {
		t.Fatalf("pack root holds %v after an interrupted build", entries)
	}
}

func TestBuildRefusesTranscodeInEncryptedLibrary(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	fake := &fakeTranscoder{out: map[string][]byte{QualityHigh: []byte("x")}}
	svc.SetTranscoder(fake.transcode)
	svc.SetEncryptedContent(&observingDecryptSource{bodies: map[string][]byte{}}, func(store.LibraryID) bool { return true }, func(store.LibraryID, string) (string, string, error) {
		return "media-file", "object-1", nil
	})
	scope := scopeFor(t, authService, "session-default")
	_, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityHigh})
	if err == nil || !strings.Contains(err.Error(), "encrypted Library") {
		t.Fatalf("BuildPack err = %v, want the encrypted refusal", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("transcoder ran %v for an encrypted Library", fake.calls)
	}
}

func TestBuildRejectsUnknownQuality(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	_, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: "ultra"})
	if !errors.Is(err, ErrUnknownQuality) {
		t.Fatalf("BuildPack err = %v, want ErrUnknownQuality", err)
	}
}

func TestBuildWithPreassignedPackIDIsIdempotent(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	fake := &fakeTranscoder{out: map[string][]byte{QualityMedium: []byte("medium")}}
	svc.SetTranscoder(fake.transcode)
	scope := scopeFor(t, authService, "session-default")
	id, err := NewPackID()
	if err != nil {
		t.Fatal(err)
	}
	// A crashed earlier attempt left a stray file under the ID's directory.
	if err := os.MkdirAll(filepath.Join(svc.packRoot, string(id)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svc.packRoot, string(id), "stale"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := BuildJobPayloadFor(scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityMedium, PackID: id})
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.HandleBuildJob(context.Background(), payload)
	if err != nil {
		t.Fatalf("HandleBuildJob: %v", err)
	}
	if first.ID != id {
		t.Fatalf("pack ID = %q, want the pre-assigned %q", first.ID, id)
	}
	for _, name := range packRootEntries(t, svc) {
		if name == "stale" {
			t.Fatal("stale file from a crashed attempt survived the build")
		}
	}
	again, err := svc.HandleBuildJob(context.Background(), payload)
	if err != nil || again.ID != id || again.Manifest != first.Manifest {
		t.Fatalf("retry = %+v, %v; want the existing pack", again, err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("transcoder calls = %v, want one across the retry", fake.calls)
	}
	packs, err := st.OfflinePacksForProfile(context.Background(), scope.ProfileID)
	if err != nil || len(packs) != 1 {
		t.Fatalf("packs = %d, %v; want 1", len(packs), err)
	}
}

func TestBuildRejectsMalformedPackID(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	scope := scopeFor(t, authService, "session-default")
	for _, id := range []store.OfflinePackID{"../x", "offline-pack-xyz", store.OfflinePackID("offline-pack-" + strings.Repeat("A", 32))} {
		_, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", PackID: id})
		if err == nil || !strings.Contains(err.Error(), "malformed") {
			t.Fatalf("PackID %q err = %v, want malformed", id, err)
		}
	}
}

type profileResolver struct {
	gotProfile store.ProfileID
	plainCalls int
}

func (r *profileResolver) Resolve(context.Context, string, store.LibraryFilter) (ResolvedTarget, error) {
	r.plainCalls++
	return ResolvedTarget{}, errors.New("Resolve must not be called")
}

func (r *profileResolver) ResolveForProfile(_ context.Context, ref string, profileID store.ProfileID, _ store.LibraryFilter) (ResolvedTarget, error) {
	r.gotProfile = profileID
	return ResolvedTarget{MediaIDs: []store.MediaID{store.MediaID(ref)}, Label: "Up Next"}, nil
}

func TestBuildPrefersProfileResolver(t *testing.T) {
	svc, authService, _, _ := newFixture(t)
	resolver := &profileResolver{}
	svc.RegisterResolver("watch-state", resolver)
	scope := scopeFor(t, authService, "session-default")
	if _, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{TargetKind: "watch-state", TargetRef: "media-alpha"}); err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if resolver.gotProfile != scope.ProfileID || resolver.plainCalls != 0 {
		t.Fatalf("resolver saw profile %q and %d plain calls", resolver.gotProfile, resolver.plainCalls)
	}
}

// TestBuildHandsTheSourceStreamInventoryToTheTranscoder: the transcoder gets
// the source file's recorded streams, so it can tone-map an HDR source
// (CORE-1744).
func TestBuildHandsTheSourceStreamInventoryToTheTranscoder(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	rows, err := st.VisibleMediaBatch(context.Background(), []store.MediaID{"media-alpha"}, filter)
	if err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForMedia(context.Background(), "media-alpha", filter)
	if err != nil || len(files) == 0 {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	streams := []store.MediaFileStream{{Index: 0, Type: "video", Codec: "hevc", ColorTransfer: "smpte2084", ColorPrimaries: "bt2020"}}
	if _, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: rows["media-alpha"].LibraryID, Path: files[0].Path, Title: rows["media-alpha"].Title, Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: files[0].ContentFingerprint, Streams: streams,
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeTranscoder{out: map[string][]byte{QualityHigh: []byte("transcoded-high")}}
	svc.SetTranscoder(fake.transcode)
	scope := scopeFor(t, authService, "session-default")
	if _, err := svc.BuildPack(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityHigh,
	}); err != nil {
		t.Fatalf("BuildPack: %v", err)
	}
	if len(fake.streams) != 1 || len(fake.streams[0]) != 1 || fake.streams[0][0].ColorTransfer != "smpte2084" {
		t.Fatalf("transcoder streams = %+v, want the HDR10 video stream", fake.streams)
	}
}

// setFixtureVideoSize makes id a video whose primary file reports sizeBytes.
// The file is extended sparsely, so the per-item bound is exercised without
// writing a file that large.
func setFixtureVideoSize(t *testing.T, st *store.Store, id store.MediaID, sizeBytes int64) {
	t.Helper()
	setFixtureClass(t, st, id, store.MediaClassVideo)
	files, err := st.MediaFilesForMedia(context.Background(), id, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(files) == 0 {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	if err := os.Truncate(files[0].Path, sizeBytes); err != nil {
		t.Fatal(err)
	}
}

// TestOriginalVideoOverTheItemBoundIsRefusedAtAdmission covers CORE-1745: an
// Original pack cannot seal a video item over the 4 GiB BFOP bound, so the
// request is refused with a typed error before any build work.
func TestOriginalVideoOverTheItemBoundIsRefusedAtAdmission(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureVideoSize(t, st, "media-alpha", int64(MaxItemSize)+1)
	scope := scopeFor(t, authService, "session-default")
	for _, quality := range []string{"", QualityOriginal} {
		req := BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: quality}
		if err := svc.AdmitBuild(context.Background(), scope, req); !errors.Is(err, ErrOriginalItemTooLarge) {
			t.Fatalf("AdmitBuild(quality %q) = %v, want ErrOriginalItemTooLarge", quality, err)
		}
		if _, err := svc.BuildPack(context.Background(), scope, req); !errors.Is(err, ErrOriginalItemTooLarge) {
			t.Fatalf("BuildPack(quality %q) = %v, want ErrOriginalItemTooLarge", quality, err)
		}
	}
	if !strings.Contains(ErrOriginalItemTooLarge.Error(), "4 GiB") {
		t.Fatalf("error %q does not name the 4 GiB limit", ErrOriginalItemTooLarge)
	}
}

// TestOriginalVideoAtTheItemBoundIsAdmitted keeps the bound inclusive: an item
// of exactly 4 GiB is admitted at Original.
func TestOriginalVideoAtTheItemBoundIsAdmitted(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureVideoSize(t, st, "media-alpha", int64(MaxItemSize))
	scope := scopeFor(t, authService, "session-default")
	if err := svc.AdmitBuild(context.Background(), scope, BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityOriginal,
	}); err != nil {
		t.Fatalf("AdmitBuild = %v, want nil", err)
	}
}

// TestTranscodedQualityPacksAVideoOverTheItemBound: the bound limits only the
// sealed bytes, so a transcoded quality still packs a source over 4 GiB.
func TestTranscodedQualityPacksAVideoOverTheItemBound(t *testing.T) {
	svc, authService, st, _ := newFixture(t)
	setFixtureVideoSize(t, st, "media-alpha", int64(MaxItemSize)+1)
	fake := &fakeTranscoder{out: map[string][]byte{QualityHigh: []byte("transcoded-high")}}
	svc.SetTranscoder(fake.transcode)
	scope := scopeFor(t, authService, "session-default")
	req := BuildPackRequest{TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: QualityHigh}
	if err := svc.AdmitBuild(context.Background(), scope, req); err != nil {
		t.Fatalf("AdmitBuild = %v, want nil", err)
	}
	if _, err := svc.BuildPack(context.Background(), scope, req); err != nil {
		t.Fatalf("BuildPack = %v, want a transcoded pack", err)
	}
}
