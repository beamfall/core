package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/libencrypt"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/playback/sessiontest"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// encryptedPrecomputeMarker rides in every fake mdat so the no-plaintext walk
// can find any decrypted copy of the rendition.
const encryptedPrecomputeMarker = "copper lantern rendition marker"

const (
	encryptedPrecomputeLibrary = store.LibraryID("library-local")
	encryptedPrecomputeSeconds = 16
	encryptedPrecomputeFrags   = 4
)

// endpointRenditionRunner stands in for FFmpeg's rendition pass: it reads the
// whole decrypted source from the loopback endpoint, as FFmpeg would, then
// writes one fragmented MP4 to stdout.
type endpointRenditionRunner struct {
	source []byte
	calls  atomic.Int32
	during func()
}

func (r *endpointRenditionRunner) ProbePipeDuration(context.Context, ffmpeg.PipeInput) (time.Duration, error) {
	return encryptedPrecomputeSeconds * time.Second, nil
}

func (r *endpointRenditionRunner) RenditionToPipe(ctx context.Context, in ffmpeg.PipeInput, _ ffmpeg.Rendition, segmentSeconds int, stdout io.Writer) error {
	r.calls.Add(1)
	if segmentSeconds != playback.PrecomputeSegmentSeconds {
		return fmt.Errorf("segment seconds = %d", segmentSeconds)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	if !bytes.Equal(got, r.source) {
		return errors.New("endpoint did not serve the decrypted source")
	}
	if r.during != nil {
		r.during()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err = stdout.Write(precomputeTestFMP4([]byte(strings.Repeat(encryptedPrecomputeMarker+" ", 8)), encryptedPrecomputeFrags, encryptedPrecomputeSeconds))
	return err
}

// A minimal fragmented MP4 shaped like FFmpeg's
// frag_keyframe+empty_moov+default_base_moof output: ftyp, an empty moov with
// one video trak and its trex, then moof/mdat pairs whose first sample is a
// sync sample.

func precomputeTestBox(typ string, parts ...[]byte) []byte {
	out := make([]byte, 8)
	copy(out[4:], typ)
	for _, part := range parts {
		out = append(out, part...)
	}
	binary.BigEndian.PutUint32(out, uint32(len(out))) //nolint:gosec // test boxes are small
	return out
}

func precomputeTestFullBox(typ string, version byte, flags uint32, parts ...[]byte) []byte {
	head := binary.BigEndian.AppendUint32(nil, uint32(version)<<24|flags&0xffffff)
	return precomputeTestBox(typ, append([][]byte{head}, parts...)...)
}

func precomputeTestBE32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func precomputeTestFMP4(payload []byte, fragments, seconds int) []byte {
	const timescale, frames = 12288, 24
	be32 := precomputeTestBE32
	frameDuration := seconds * timescale / (fragments * frames)
	trak := precomputeTestBox("trak",
		precomputeTestFullBox("tkhd", 0, 3, be32(0), be32(0), be32(1), make([]byte, 68)),
		precomputeTestBox("mdia",
			precomputeTestFullBox("mdhd", 0, 0, be32(0), be32(0), be32(timescale), be32(0), make([]byte, 4)),
			precomputeTestFullBox("hdlr", 0, 0, be32(0), []byte("vide"), make([]byte, 13))))
	trex := precomputeTestFullBox("trex", 0, 0, be32(1), be32(1), be32(0), be32(0), be32(0x01010000))
	out := precomputeTestBox("ftyp", []byte("iso5"), be32(512), []byte("iso5iso6mp41"))
	out = append(out, precomputeTestBox("moov", precomputeTestFullBox("mvhd", 0, 0, make([]byte, 96)), trak, precomputeTestBox("mvex", trex))...)
	chunk := (len(payload) + fragments - 1) / fragments
	for i := range fragments {
		durations := make([]byte, 0, 4*frames)
		for range frames {
			durations = append(durations, be32(uint32(frameDuration))...) //nolint:gosec // small test counts
		}
		traf := precomputeTestBox("traf",
			precomputeTestFullBox("tfhd", 0, 0x020000, be32(1)),
			precomputeTestFullBox("tfdt", 1, 0, binary.BigEndian.AppendUint64(nil, uint64(i*frames*frameDuration))), //nolint:gosec // small test counts
			precomputeTestFullBox("trun", 0, 0x001|0x004|0x100, be32(frames), be32(0), be32(0x02000000), durations))
		out = append(out, precomputeTestBox("moof", precomputeTestFullBox("mfhd", 0, 0, be32(uint32(i+1))), traf)...) //nolint:gosec // small test counts
		out = append(out, precomputeTestBox("mdat", payload[min(i*chunk, len(payload)):min((i+1)*chunk, len(payload))])...)
	}
	return out
}

// walkingContent runs a plaintext walk after every member write, so a member
// that reaches disk unencrypted is caught even if a later step removes it.
type walkingContent struct {
	precomputeContent
	afterWrite func()
}

func (c walkingContent) EncryptObject(libraryID store.LibraryID, src io.Reader, ciphertextPath, objectClass, immutableObjectID string) error {
	if err := c.precomputeContent.EncryptObject(libraryID, src, ciphertextPath, objectClass, immutableObjectID); err != nil {
		return err
	}
	c.afterWrite()
	return nil
}

// plaintextContent is the mutation: it writes each member without encrypting
// it.
type plaintextContent struct{ precomputeContent }

func (plaintextContent) EncryptObject(_ store.LibraryID, src io.Reader, ciphertextPath, _, _ string) error {
	data, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	return os.WriteFile(ciphertextPath, data, 0o600)
}

type passthroughAppDBCipher struct{}

func (passthroughAppDBCipher) UnsealDBFile(context.Context, store.LibraryID, string, string) error {
	return nil
}

func (passthroughAppDBCipher) SealDBFile(context.Context, store.LibraryID, string, string) error {
	return nil
}

// refusingPublicGenerator fails the test's public ladder: an encrypted job
// must never reach it.
type refusingPublicGenerator struct{}

func (refusingPublicGenerator) PrecomputeHLS(context.Context, playback.PrecomputeSpec) error {
	return errors.New("public generator used for an Encrypted Library")
}

type encryptedPrecomputeAppFixture struct {
	st        *store.Store
	manager   *libencrypt.Manager
	adapter   appLibEncryptDBCipher
	runner    *endpointRenditionRunner
	mediaID   store.MediaID
	cacheRoot string
	dirs      []string // every directory the pipeline can write
	now       time.Time

	mu       sync.Mutex
	findings []string
}

// newEncryptedPrecomputeAppFixture builds precompute with the production
// adapters (a real libencrypt manager and the encderiv-backed generator) over
// a store whose Encrypted Library DB is unsealed; only FFmpeg is faked. Every
// directory the pipeline can write, including TMPDIR, is test-owned.
func newEncryptedPrecomputeAppFixture(t *testing.T) *encryptedPrecomputeAppFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	tempDir := filepath.Join(root, "tmp")
	dataDir := filepath.Join(root, "data")
	cacheRoot := filepath.Join(root, "cache")
	for _, dir := range []string{tempDir, dataDir, cacheRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", tempDir)

	storeDir := storetest.MigratedDataDir(t)
	st, err := store.Open(ctx, storeDir, store.Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetLibraryBacking(ctx, encryptedPrecomputeLibrary, false, true); err != nil {
		t.Fatalf("SetLibraryBacking: %v", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, encryptedPrecomputeLibrary, passthroughAppDBCipher{}); err != nil {
		t.Fatalf("UnsealEncryptedLibraryDB: %v", err)
	}

	manager := libencrypt.NewManagerWithParams(memSecretStore{}, libencrypt.TestArgon2Params)
	if _, err := manager.Provision(string(encryptedPrecomputeLibrary), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	adapter := appLibEncryptDBCipher{manager: manager}

	source := bytes.Repeat([]byte("synthetic source container bytes "), 64)
	sourcePath := filepath.Join(dataDir, "source.bin")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := store.HashMediaFileContent(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	// No stream rows: the Library plane's media_file_stream lacks the
	// catalog's stream_type column, so streamed imports fail there today.
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: encryptedPrecomputeLibrary, Path: sourcePath, Title: "Encrypted Rendition Source",
		Container: "mkv", Codec: "hevc", Class: store.MediaClassVideo, ContentFingerprint: fingerprint,
		DurationSecondsHint: encryptedPrecomputeSeconds, VideoWidthHint: 1280, VideoHeightHint: 720,
		SizeBytes: int64(len(source)),
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	if err := adapter.EncryptObjectFile(string(encryptedPrecomputeLibrary), sourcePath, sourcePath+".enc", fingerprint); err != nil {
		t.Fatalf("EncryptObjectFile: %v", err)
	}
	if err := os.Rename(sourcePath+".enc", sourcePath); err != nil {
		t.Fatal(err)
	}

	return &encryptedPrecomputeAppFixture{
		st: st, manager: manager, adapter: adapter, runner: &endpointRenditionRunner{source: source},
		mediaID: media.ID, cacheRoot: cacheRoot, dirs: []string{cacheRoot, dataDir, tempDir, storeDir},
		now: time.Unix(900, 0).UTC(),
	}
}

func (f *encryptedPrecomputeAppFixture) sealed(id store.LibraryID) bool {
	return f.manager.Provisioned(string(id)) && f.manager.Sealed(string(id))
}

// enqueue goes through the composition root's controller, as the API does.
func (f *encryptedPrecomputeAppFixture) enqueue(t *testing.T) int {
	t.Helper()
	controller := playbackCacheController{store: f.st, cacheRoot: f.cacheRoot, budget: 1 << 30, now: func() time.Time { return f.now }, sealed: f.sealed}
	n, err := controller.PrecomputeMedia(context.Background(), f.mediaID)
	if err != nil {
		t.Fatalf("PrecomputeMedia: %v", err)
	}
	return n
}

// handler wires the production generator over content, with a plaintext walk
// after every member write.
func (f *encryptedPrecomputeAppFixture) handler(content precomputeContent) playback.PrecomputeJobHandler {
	walked := walkingContent{precomputeContent: content, afterWrite: func() {
		found := f.plaintextFindings(true)
		f.mu.Lock()
		f.findings = append(f.findings, found...)
		f.mu.Unlock()
	}}
	return playback.PrecomputeJobHandler{
		Store:     f.st,
		Generator: refusingPublicGenerator{},
		Admission: playback.NewPrecomputeResourceGovernor(1 << 30),
		Now:       func() time.Time { return f.now.Add(time.Minute) },
		Encrypted: encryptedPrecomputeGenerator{content: walked, runner: f.runner, pollInterval: 10 * time.Millisecond},
		Sealed:    f.sealed,
	}
}

func (f *encryptedPrecomputeAppFixture) runOne(t *testing.T, handler playback.PrecomputeJobHandler) error {
	t.Helper()
	job, err := f.st.ClaimNextJob(context.Background(), "encrypted-precompute-test", []store.JobClass{store.JobClassBackground}, f.now)
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	return handler.Handle(context.Background(), job)
}

func (f *encryptedPrecomputeAppFixture) walkFindings() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.findings...)
}

// plaintextFindings walks every directory the pipeline can write: no file may
// hold a playlist, an fMP4 box type or the payload marker, and every finished
// file under the cache root must be a ciphertext object. midRun tolerates
// files that vanish during the walk and exempts encderiv's stage file (its own
// ephemeral-key format) from the object-header check.
func (f *encryptedPrecomputeAppFixture) plaintextFindings(midRun bool) []string {
	var findings []string
	for _, dir := range f.dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if midRun && errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			raw, err := os.ReadFile(path) // #nosec G304 G122 -- test-owned temp-dir walk.
			if err != nil {
				if midRun && errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			for _, marker := range []string{"#EXTM3U", "ftyp", "moof", encryptedPrecomputeMarker} {
				if bytes.Contains(raw, []byte(marker)) {
					findings = append(findings, fmt.Sprintf("%s contains %q", path, marker))
				}
			}
			if rel, relErr := filepath.Rel(f.cacheRoot, path); relErr == nil && filepath.IsLocal(rel) {
				if midRun && strings.HasSuffix(path, "-transcode") {
					return nil
				}
				if ok, _ := libencrypt.HasObjectHeader(bytes.NewReader(raw)); !ok {
					findings = append(findings, path+" is not a ciphertext object")
				}
			}
			return nil
		})
		if err != nil {
			findings = append(findings, fmt.Sprintf("walk %s: %v", dir, err))
		}
	}
	return findings
}

func (f *encryptedPrecomputeAppFixture) cacheFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(f.cacheRoot, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatalf("walk cache: %v", err)
	}
	return files
}

// serve fetches one member of a precomputed rendition through the HLS route.
func (f *encryptedPrecomputeAppFixture) serve(t *testing.T, renditionID store.PlaybackRenditionID) func(name string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := context.Background()
	authService := auth.NewService(f.st)
	now := time.Unix(100, 0).UTC()
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return now })
	scope, err := authService.Scope(ctx, "session-default")
	if err != nil {
		t.Fatal(err)
	}
	token, err := tokens.Issue(ctx, streamtoken.Grant{
		Target: streamtoken.TargetMediaHLS, MediaID: f.mediaID, DeviceID: scope.DeviceID,
		SessionID: scope.SessionID, ProfileID: scope.ProfileID, PlaybackRenditionID: renditionID,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	handler := mediahttp.NewHandler(f.st, authService, tokens, artwork.NewService(f.st, nil, dataDir), sessiontest.New(t, nil, dataDir, nil))
	handler.SetSealed(f.sealed)
	handler.SetEncryptedBytes(f.adapter)
	return func(name string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.HLS(rec, httptest.NewRequest(http.MethodGet, "/api/media/hls/"+token+"/"+name, nil))
		return rec
	}
}

// fmp4TopLevel parses b as a sequence of top-level boxes and returns their
// types and the concatenated mdat payloads; it fails on any malformed box.
func fmp4TopLevel(t *testing.T, b []byte) ([]string, []byte) {
	t.Helper()
	var types []string
	var mdat []byte
	for off := 0; off < len(b); {
		if off+8 > len(b) {
			t.Fatalf("truncated box header at %d", off)
		}
		size := int(binary.BigEndian.Uint32(b[off:]))
		if size < 8 || off+size > len(b) {
			t.Fatalf("box at %d has size %d of %d", off, size, len(b))
		}
		typ := string(b[off+4 : off+8])
		types = append(types, typ)
		if typ == "mdat" {
			mdat = append(mdat, b[off+8:off+size]...)
		}
		off += size
	}
	return types, mdat
}

func TestEncryptedPrecomputeServesDecryptedFMP4ThroughProductionAdapters(t *testing.T) {
	// CORE-1963: precompute runs through encderiv's loopback endpoint, every
	// member lands as a ciphertext object, and the HLS route serves the
	// decrypted playlists, init section and segments.
	f := newEncryptedPrecomputeAppFixture(t)
	n := f.enqueue(t)
	if n != 2 {
		t.Fatalf("enqueued %d jobs, want the 720p and 480p rungs", n)
	}
	handler := f.handler(f.adapter)
	for range n {
		if err := f.runOne(t, handler); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if got := f.runner.calls.Load(); got != 2 {
		t.Fatalf("runner calls = %d, want 2", got)
	}
	if findings := append(f.walkFindings(), f.plaintextFindings(false)...); len(findings) != 0 {
		t.Fatalf("plaintext on disk: %v", findings)
	}
	if files := f.cacheFiles(t); len(files) == 0 {
		t.Fatal("no published members under the cache root")
	}

	get := f.serve(t, playback.EncryptedPrecomputeRenditionID(f.mediaID, "720p"))
	master := get("master.m3u8")
	if master.Code != http.StatusOK || !strings.HasPrefix(master.Body.String(), "#EXTM3U") || !strings.Contains(master.Body.String(), "720p/index.m3u8") {
		t.Fatalf("master = %d %q", master.Code, master.Body.String())
	}
	index := get("720p/index.m3u8")
	if index.Code != http.StatusOK || !strings.HasPrefix(index.Body.String(), "#EXTM3U") {
		t.Fatalf("index = %d %q", index.Code, index.Body.String())
	}
	if !strings.Contains(index.Body.String(), "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(index.Body.String(), "#EXT-X-ENDLIST") {
		t.Fatalf("index is not a VOD playlist: %q", index.Body.String())
	}
	var segments []string
	for _, line := range strings.Split(index.Body.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			segments = append(segments, line)
		}
	}
	if len(segments) != encryptedPrecomputeFrags {
		t.Fatalf("segments = %v, want %d", segments, encryptedPrecomputeFrags)
	}

	init := get("720p/init.mp4")
	if init.Code != http.StatusOK {
		t.Fatalf("init = %d %q", init.Code, init.Body.String())
	}
	initTypes, _ := fmp4TopLevel(t, init.Body.Bytes())
	if len(initTypes) < 2 || initTypes[0] != "ftyp" || initTypes[1] != "moov" {
		t.Fatalf("init boxes = %v, want ftyp then moov", initTypes)
	}
	stream := append([]byte(nil), init.Body.Bytes()...)
	for _, name := range segments {
		seg := get("720p/" + name)
		if seg.Code != http.StatusOK || seg.Header().Get("Content-Type") != "video/mp4" {
			t.Fatalf("%s = %d %q", name, seg.Code, seg.Header().Get("Content-Type"))
		}
		types, _ := fmp4TopLevel(t, seg.Body.Bytes())
		if len(types) == 0 || types[0] != "moof" {
			t.Fatalf("%s boxes = %v, want a moof first", name, types)
		}
		stream = append(stream, seg.Body.Bytes()...)
	}
	types, mdat := fmp4TopLevel(t, stream)
	moofs, mdats := 0, 0
	for _, typ := range types {
		switch typ {
		case "moof":
			moofs++
		case "mdat":
			mdats++
		}
	}
	if moofs != encryptedPrecomputeFrags || mdats != encryptedPrecomputeFrags {
		t.Fatalf("stream boxes = %v, want %d moof/mdat pairs", types, encryptedPrecomputeFrags)
	}
	if want := strings.Repeat(encryptedPrecomputeMarker+" ", 8); string(mdat) != want {
		t.Fatalf("stream payload = %q, want the rendition's mdat bytes", mdat)
	}
}

func TestEncryptedPrecomputeSealedLibraryRefusesThroughProductionAdapters(t *testing.T) {
	t.Run("sealed before enqueue", func(t *testing.T) {
		f := newEncryptedPrecomputeAppFixture(t)
		f.manager.Seal(string(encryptedPrecomputeLibrary))
		if n := f.enqueue(t); n != 0 {
			t.Fatalf("enqueued %d jobs for a Sealed Library, want 0", n)
		}
	})
	t.Run("sealed at claim", func(t *testing.T) {
		f := newEncryptedPrecomputeAppFixture(t)
		if n := f.enqueue(t); n == 0 {
			t.Fatal("enqueued nothing")
		}
		f.manager.Seal(string(encryptedPrecomputeLibrary))
		if err := f.runOne(t, f.handler(f.adapter)); err == nil {
			t.Fatal("Handle succeeded for a Sealed Library")
		}
		if got := f.runner.calls.Load(); got != 0 {
			t.Fatalf("runner calls = %d, want 0", got)
		}
		if files := f.cacheFiles(t); len(files) != 0 {
			t.Fatalf("cache files after refusal: %v", files)
		}
	})
	t.Run("sealed mid-run", func(t *testing.T) {
		f := newEncryptedPrecomputeAppFixture(t)
		if n := f.enqueue(t); n == 0 {
			t.Fatal("enqueued nothing")
		}
		f.runner.during = func() { f.manager.Seal(string(encryptedPrecomputeLibrary)) }
		if err := f.runOne(t, f.handler(f.adapter)); err == nil {
			t.Fatal("Handle succeeded after a mid-run seal")
		}
		if got := f.runner.calls.Load(); got != 1 {
			t.Fatalf("runner calls = %d, want 1", got)
		}
		if files := f.cacheFiles(t); len(files) != 0 {
			t.Fatalf("cache files after a mid-run seal: %v", files)
		}
		if findings := append(f.walkFindings(), f.plaintextFindings(false)...); len(findings) != 0 {
			t.Fatalf("plaintext on disk: %v", findings)
		}
	})
}

func TestEncryptedPrecomputePlaintextWalkCatchesAnUnencryptedMember(t *testing.T) {
	// Mutation proof: a generator that skips encryption must trip both the
	// per-write walk and the handler's ciphertext verification.
	f := newEncryptedPrecomputeAppFixture(t)
	if n := f.enqueue(t); n == 0 {
		t.Fatal("enqueued nothing")
	}
	if err := f.runOne(t, f.handler(plaintextContent{f.adapter})); err == nil {
		t.Fatal("Handle published plaintext members")
	}
	if findings := f.walkFindings(); len(findings) == 0 {
		t.Fatal("the plaintext walk found nothing; it cannot detect a plaintext member")
	}
	if files := f.cacheFiles(t); len(files) != 0 {
		t.Fatalf("cache files after a rejected generation: %v", files)
	}
}
