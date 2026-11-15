package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeCipherHeader marks a fake encrypted member; the body is XOR-masked so no
// playlist or box marker survives in it.
var fakeCipherHeader = []byte("FAKE-CIPHERTEXT\x00")

func fakeSeal(plain []byte) []byte {
	out := append([]byte(nil), fakeCipherHeader...)
	for _, b := range plain {
		out = append(out, b^0x5a)
	}
	return out
}

type passthroughLibraryDBCipher struct{}

func (passthroughLibraryDBCipher) UnsealDBFile(context.Context, store.LibraryID, string, string) error {
	return nil
}

func (passthroughLibraryDBCipher) SealDBFile(context.Context, store.LibraryID, string, string) error {
	return nil
}

// fakeEncryptedPrecompute writes a complete generation of fake ciphertext.
// plaintext writes one member unsealed (the mutation the verify step must
// catch); during runs inside the generation, after the members are written.
type fakeEncryptedPrecompute struct {
	calls     []EncryptedPrecomputeSpec
	plaintext string
	during    func(EncryptedPrecomputeSpec) error
}

func (f *fakeEncryptedPrecompute) PrecomputeEncryptedHLS(_ context.Context, spec EncryptedPrecomputeSpec) error {
	f.calls = append(f.calls, spec)
	rungDir := filepath.Join(spec.OutputDir, spec.RungID)
	if err := os.MkdirAll(rungDir, 0o700); err != nil {
		return err
	}
	members := map[string][]byte{
		masterPlaylistName: PrecomputeMasterPlaylist(spec.PrecomputeSpec),
		filepath.Join(spec.RungID, "index.m3u8"): PrecomputeMediaPlaylist([]PrecomputeSegment{
			{URI: "seg00000.m4s", Duration: 4 * time.Second},
		}),
		filepath.Join(spec.RungID, "init.mp4"):     []byte("....ftyp init"),
		filepath.Join(spec.RungID, "seg00000.m4s"): []byte("....moof segment"),
	}
	for name, plain := range members {
		data := fakeSeal(plain)
		if filepath.ToSlash(name) == f.plaintext {
			data = plain
		}
		if err := os.WriteFile(filepath.Join(spec.OutputDir, name), data, 0o600); err != nil {
			return err
		}
	}
	if f.during != nil {
		return f.during(spec)
	}
	return nil
}

func (f *fakeEncryptedPrecompute) DecryptPrecomputeMember(_ store.LibraryID, _ store.PlaybackRenditionID, _, ciphertextPath string) ([]byte, error) {
	data, err := os.ReadFile(ciphertextPath) // #nosec G304 -- test-owned temp path.
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, fakeCipherHeader) {
		return nil, errors.New("not fake ciphertext")
	}
	plain := data[len(fakeCipherHeader):]
	out := make([]byte, len(plain))
	for i, b := range plain {
		out[i] = b ^ 0x5a
	}
	return out, nil
}

func (f *fakeEncryptedPrecompute) IsPrecomputeCiphertext(r io.Reader) (bool, error) {
	head := make([]byte, len(fakeCipherHeader))
	if _, err := io.ReadFull(r, head); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(head, fakeCipherHeader), nil
}

type encryptedPrecomputeFixture struct {
	st        *store.Store
	mediaID   store.MediaID
	cacheRoot string
	sealed    atomic.Bool
	now       time.Time
}

func newEncryptedPrecomputeFixture(t *testing.T) *encryptedPrecomputeFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, storetest.MigratedDataDir(t), store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetLibraryBacking(ctx, "library-local", false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, "library-local", passthroughLibraryDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}
	f := &encryptedPrecomputeFixture{st: st, cacheRoot: t.TempDir(), now: time.Unix(900, 0).UTC()}
	// No stream rows: the Library plane's media_file_stream lacks the
	// catalog's stream_type column, so streamed imports fail there today.
	f.mediaID = importProbedMedia(t, st, "fp-encrypted-precompute", "mkv", "hevc", 1280, 720, nil)
	return f
}

func (f *encryptedPrecomputeFixture) enqueue(t *testing.T) []store.Job {
	t.Helper()
	jobs, err := PrecomputeMediaRenditionsAllowingEncrypted(context.Background(), f.st, f.mediaID, "h264", f.cacheRoot, f.now, f.sealedFor)
	if err != nil {
		t.Fatalf("PrecomputeMediaRenditionsAllowingEncrypted: %v", err)
	}
	return jobs
}

func (f *encryptedPrecomputeFixture) sealedFor(id store.LibraryID) bool {
	return id == "library-local" && f.sealed.Load()
}

func (f *encryptedPrecomputeFixture) handler(gen EncryptedPrecomputeGenerator) PrecomputeJobHandler {
	return PrecomputeJobHandler{
		Store:     f.st,
		Generator: &fakePrecomputeGenerator{},
		Admission: NewPrecomputeResourceGovernor(1 << 30),
		Now:       func() time.Time { return f.now.Add(time.Minute) },
		Encrypted: gen,
		Sealed:    f.sealedFor,
	}
}

func (f *encryptedPrecomputeFixture) run(t *testing.T, handler PrecomputeJobHandler) (store.Job, error) {
	t.Helper()
	claimed, err := f.st.ClaimNextJob(context.Background(), "encrypted-precompute-test", []store.JobClass{store.JobClassBackground}, f.now)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	return claimed, handler.Handle(context.Background(), claimed)
}

func TestEncryptedPrecomputeEnqueuesFingerprintFreePayloads(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	jobs := f.enqueue(t)
	if len(jobs) != 2 {
		t.Fatalf("enqueued %d jobs, want the 720p and 480p rungs", len(jobs))
	}
	for _, job := range jobs {
		if strings.Contains(string(job.Payload), "fp-encrypted-precompute") || strings.Contains(job.DedupeKey, "fp-encrypted-precompute") {
			t.Fatalf("job carries the content fingerprint: %s / %s", job.Payload, job.DedupeKey)
		}
		var payload PrecomputePayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			t.Fatalf("payload JSON: %v", err)
		}
		if !payload.Encrypted || payload.LibraryID != "library-local" || payload.SourcePath != "" || payload.ContentFingerprint != "" {
			t.Fatalf("payload = %+v, want an encrypted payload without source identity", payload)
		}
		if !IsEncryptedPrecomputeRenditionID(payload.RenditionID) {
			t.Fatalf("rendition ID %q is not an encrypted precompute ID", payload.RenditionID)
		}
	}

	// The public entry point still refuses the same Library.
	refused, err := PrecomputeMediaRenditions(context.Background(), f.st, f.mediaID, "h264", f.cacheRoot, f.now)
	if err != nil || len(refused) != 0 {
		t.Fatalf("PrecomputeMediaRenditions = %d jobs, %v; want refusal", len(refused), err)
	}
}

func TestEncryptedPrecomputeSealedLibraryEnqueuesNothing(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	f.sealed.Store(true)
	if jobs := f.enqueue(t); len(jobs) != 0 {
		t.Fatalf("enqueued %d jobs for a Sealed Library, want 0", len(jobs))
	}
	if jobs, err := PrecomputeMediaRenditionsAllowingEncrypted(context.Background(), f.st, f.mediaID, "h264", f.cacheRoot, f.now, nil); err != nil || len(jobs) != 0 {
		t.Fatalf("nil sealed predicate enqueued %d jobs, %v; want 0", len(jobs), err)
	}
}

func TestEncryptedPrecomputePublishesCiphertextInTheLibraryPlane(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	f.enqueue(t)
	gen := &fakeEncryptedPrecompute{}
	handler := f.handler(gen)
	registry := jobs.NewRegistry(f.st, "encrypted-precompute-test")
	registry.Register(PrecomputeJobKind, func(ctx context.Context, job store.Job) (jobs.Result, error) {
		return jobs.Result{}, handler.Handle(ctx, job)
	})
	for range 2 {
		if _, err := registry.RunOne(context.Background(), []store.JobClass{store.JobClassBackground}, f.now); err != nil {
			t.Fatalf("RunOne: %v", err)
		}
	}
	if len(gen.calls) != 2 {
		t.Fatalf("encrypted generator calls = %d, want 2", len(gen.calls))
	}
	spec := gen.calls[0]
	if spec.ContentFingerprint != "fp-encrypted-precompute" || spec.SourcePath == "" || spec.LibraryID != "library-local" {
		t.Fatalf("spec = %+v, want the source identity hydrated from the Library DB", spec)
	}
	if spec.Revoked == nil || spec.MaxBytes <= 0 || spec.StageName == "" || !filepath.IsAbs(spec.StageDir) {
		t.Fatalf("spec staging = %+v, want revocation, a byte cap and an absolute stage dir", spec)
	}

	id := EncryptedPrecomputeRenditionID(f.mediaID, "720p")
	rendition, err := f.st.PlaybackRendition(context.Background(), id, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatalf("PlaybackRendition: %v", err)
	}
	if rendition.Status != store.PlaybackRenditionReady {
		t.Fatalf("rendition status = %q, want ready", rendition.Status)
	}
	if !strings.HasPrefix(rendition.Path, filepath.Join(f.cacheRoot, encryptedPrecomputeDir)) {
		t.Fatalf("rendition path = %q, want under the encrypted root", rendition.Path)
	}
	assertPrecomputeResidueEmpty(t, f.cacheRoot)
	assertNoPlaintextUnder(t, f.cacheRoot)

	// A rewrap purge removes the Library's ciphertext in one removal.
	if err := PurgeEncryptedPrecomputedRenditions(f.cacheRoot, "library-local"); err != nil {
		t.Fatalf("PurgeEncryptedPrecomputedRenditions: %v", err)
	}
	if _, err := os.Stat(rendition.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rendition path after purge: %v, want removed", err)
	}
}

func TestEncryptedPrecomputeRefusesWhenSealedAtClaim(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	f.enqueue(t)
	f.sealed.Store(true)
	gen := &fakeEncryptedPrecompute{}
	if _, err := f.run(t, f.handler(gen)); !errors.Is(err, ErrPrecomputeSealed) {
		t.Fatalf("Handle = %v, want ErrPrecomputeSealed", err)
	}
	if len(gen.calls) != 0 {
		t.Fatalf("generator ran %d times for a Sealed Library", len(gen.calls))
	}
	assertPrecomputeResidueEmpty(t, f.cacheRoot)
	assertNoEncryptedGenerations(t, f.cacheRoot)
}

func TestEncryptedPrecomputeMidRunSealAbortsAndCleansUp(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	f.enqueue(t)
	gen := &fakeEncryptedPrecompute{during: func(EncryptedPrecomputeSpec) error {
		f.sealed.Store(true)
		return nil // the handler itself must notice the seal before publishing
	}}
	if _, err := f.run(t, f.handler(gen)); !errors.Is(err, ErrPrecomputeSealed) {
		t.Fatalf("Handle = %v, want ErrPrecomputeSealed", err)
	}
	assertPrecomputeResidueEmpty(t, f.cacheRoot)
	assertNoEncryptedGenerations(t, f.cacheRoot)
}

func TestEncryptedPrecomputeRejectsAPlaintextMember(t *testing.T) {
	for _, member := range []string{"720p/seg00000.m4s", "720p/init.mp4", "master.m3u8"} {
		t.Run(member, func(t *testing.T) {
			f := newEncryptedPrecomputeFixture(t)
			f.enqueue(t)
			gen := &fakeEncryptedPrecompute{plaintext: member}
			_, err := f.run(t, f.handler(gen))
			if !errors.Is(err, ErrPrecomputeIncomplete) {
				t.Fatalf("Handle = %v, want ErrPrecomputeIncomplete", err)
			}
			assertPrecomputeResidueEmpty(t, f.cacheRoot)
			assertNoEncryptedGenerations(t, f.cacheRoot)
		})
	}
}

func TestEncryptedPrecomputeRefusesWithoutAnEncryptedGenerator(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	f.enqueue(t)
	handler := f.handler(nil)
	public := handler.Generator.(*fakePrecomputeGenerator)
	if _, err := f.run(t, handler); !errors.Is(err, ErrPrecomputeEncrypted) {
		t.Fatalf("Handle = %v, want ErrPrecomputeEncrypted", err)
	}
	if len(public.calls) != 0 {
		t.Fatal("an encrypted job fell through to the public generator")
	}
}

func TestEncryptedPrecomputeRefusesAPublicPayloadForAnEncryptedLibrary(t *testing.T) {
	f := newEncryptedPrecomputeFixture(t)
	gen := &fakeEncryptedPrecompute{}
	handler := f.handler(gen)
	public := handler.Generator.(*fakePrecomputeGenerator)
	req := PrecomputeRequest{
		MediaID:            f.mediaID,
		RenditionID:        PrecomputeRenditionID(f.mediaID, "fp-encrypted-precompute", "720p"),
		ContentFingerprint: "fp-encrypted-precompute",
		RungID:             "720p",
		Codec:              "h264",
		Height:             720,
		Bandwidth:          4_256_000,
		SourcePath:         "/unused/source.mkv",
		Path:               filepath.Join(f.cacheRoot, "public-720p"),
	}
	claimed := claimPrecomputeJob(t, f.st, req, f.now, "encrypted-precompute-test")
	if err := handler.Handle(context.Background(), claimed); !errors.Is(err, ErrPrecomputeEncrypted) {
		t.Fatalf("Handle = %v, want ErrPrecomputeEncrypted", err)
	}
	if len(public.calls) != 0 || len(gen.calls) != 0 {
		t.Fatalf("generators ran for a public payload on an encrypted Library: public=%d encrypted=%d", len(public.calls), len(gen.calls))
	}
}

func TestPrecomputeMediaPlaylistIsAVODPlaylist(t *testing.T) {
	got := string(PrecomputeMediaPlaylist([]PrecomputeSegment{
		{URI: "seg00000.m4s", Duration: 4 * time.Second},
		{URI: "seg00001.m4s", Duration: 4200 * time.Millisecond},
	}))
	want := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:5\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=\"init.mp4\"\n" +
		"#EXTINF:4.000000,\nseg00000.m4s\n#EXTINF:4.200000,\nseg00001.m4s\n#EXT-X-ENDLIST\n"
	if got != want {
		t.Fatalf("playlist =\n%s\nwant\n%s", got, want)
	}
}

func assertNoEncryptedGenerations(t *testing.T, cacheRoot string) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(cacheRoot, encryptedPrecomputeDir), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			t.Errorf("unexpected published member %s", path)
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("walk: %v", err)
	}
}

func assertNoPlaintextUnder(t *testing.T, root string) {
	t.Helper()
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		data, err := os.ReadFile(path) // #nosec G304 G122 -- test-owned temp-dir walk.
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(data, fakeCipherHeader) {
			t.Errorf("%s is not ciphertext", path)
		}
		for _, marker := range []string{"#EXTM3U", "ftyp", "moof"} {
			if bytes.Contains(data, []byte(marker)) {
				t.Errorf("%s contains %q", path, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if files == 0 {
		t.Fatal("no published members to check")
	}
}
