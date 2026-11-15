package offlinepack

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// These tests pin CORE-1742's second half (ADR-0235): High, Medium and Low
// packs build in an Encrypted Library, the transcode reads its source from the
// token-gated loopback endpoint and its output never reaches disk as plaintext,
// and a Seal or a lost grant ends the build with nothing staged.

// lockedDecryptSource serves plaintext bodies by path; the loopback endpoint
// opens it from its own goroutines.
type lockedDecryptSource struct {
	mu     sync.Mutex
	bodies map[string][]byte
	opens  int
}

type bytesReadSeekCloser struct{ *bytes.Reader }

func (bytesReadSeekCloser) Close() error { return nil }

func (s *lockedDecryptSource) OpenDecryptReader(_ string, path string, _, _ string) (io.ReadSeekCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.bodies[path]
	if !ok {
		return nil, errors.New("unknown decrypt path")
	}
	s.opens++
	return bytesReadSeekCloser{bytes.NewReader(body)}, nil
}

func (s *lockedDecryptSource) openCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opens
}

// profileStore rewrites the requesting Profile as an administrator's change
// would land mid-build.
type profileStore struct {
	Store
	mu         sync.Mutex
	edit       func(*store.Profile) error
	onProgress func()
}

func (s *profileStore) Profile(ctx context.Context, id store.ProfileID) (store.Profile, error) {
	profile, err := s.Store.Profile(ctx, id)
	if err != nil {
		return profile, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.edit != nil {
		err = s.edit(&profile)
	}
	return profile, err
}

func (s *profileStore) setEdit(edit func(*store.Profile) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edit = edit
}

// SetOfflinePackBuildProgress runs onProgress before recording, so a test can
// land a change between the build's phases.
func (s *profileStore) SetOfflinePackBuildProgress(ctx context.Context, id store.OfflinePackID, progress float64, now time.Time) error {
	s.mu.Lock()
	hook := s.onProgress
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	return s.Store.SetOfflinePackBuildProgress(ctx, id, progress, now)
}

// A minimal fragmented MP4 shaped like FFmpeg's
// frag_keyframe+empty_moov+default_base_moof output: ftyp, an empty moov with
// one video trak and its trex, then moof/mdat pairs.

func fmp4Box(typ string, parts ...[]byte) []byte {
	out := make([]byte, 8)
	copy(out[4:], typ)
	for _, part := range parts {
		out = append(out, part...)
	}
	binary.BigEndian.PutUint32(out, uint32(len(out))) //nolint:gosec // test boxes are small
	return out
}

func fmp4FullBox(typ string, version byte, flags uint32, parts ...[]byte) []byte {
	head := binary.BigEndian.AppendUint32(nil, uint32(version)<<24|flags&0xffffff)
	return fmp4Box(typ, append([][]byte{head}, parts...)...)
}

func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

const (
	fmp4Timescale  = 12288
	fmp4FrameCount = 24
	// fmp4Seconds is media-alpha's seeded duration, which the output must
	// cover.
	fmp4Seconds = 120
)

func fragmentedMP4(payload []byte, fragments int) []byte {
	frameDuration := fmp4Seconds * fmp4Timescale / (fragments * fmp4FrameCount)
	trak := fmp4Box("trak",
		fmp4FullBox("tkhd", 0, 3, be32(0), be32(0), be32(1), make([]byte, 68)),
		fmp4Box("mdia",
			fmp4FullBox("mdhd", 0, 0, be32(0), be32(0), be32(fmp4Timescale), be32(0), make([]byte, 4)),
			fmp4FullBox("hdlr", 0, 0, be32(0), []byte("vide"), make([]byte, 13))))
	trex := fmp4FullBox("trex", 0, 0, be32(1), be32(1), be32(0), be32(0), be32(0x01010000))
	out := fmp4Box("ftyp", []byte("iso5"), be32(512), []byte("iso5iso6mp41"))
	out = append(out, fmp4Box("moov", fmp4FullBox("mvhd", 0, 0, make([]byte, 96)), trak, fmp4Box("mvex", trex))...)
	chunk := (len(payload) + fragments - 1) / fragments
	for i := range fragments {
		durations := make([]byte, 0, 4*fmp4FrameCount)
		for range fmp4FrameCount {
			durations = append(durations, be32(uint32(frameDuration))...) //nolint:gosec // small test counts
		}
		traf := fmp4Box("traf",
			fmp4FullBox("tfhd", 0, 0x020000, be32(1)),
			fmp4FullBox("tfdt", 1, 0, binary.BigEndian.AppendUint64(nil, uint64(i*fmp4FrameCount*frameDuration))), //nolint:gosec // small test counts
			fmp4FullBox("trun", 0, 0x001|0x004|0x100, be32(fmp4FrameCount), be32(0), be32(0x02000000), durations))
		out = append(out, fmp4Box("moof", fmp4FullBox("mfhd", 0, 0, be32(uint32(i+1))), traf)...) //nolint:gosec // small test counts
		out = append(out, fmp4Box("mdat", payload[min(i*chunk, len(payload)):min((i+1)*chunk, len(payload))])...)
	}
	return out
}

// firstFragments cuts a fragmented MP4 before its (n+1)th moof, leaving a
// well-formed stream that runs short.
func firstFragments(t *testing.T, b []byte, n int) []byte {
	t.Helper()
	moofs := 0
	for off := 0; off+8 <= len(b); off += int(binary.BigEndian.Uint32(b[off:])) {
		if string(b[off+4:off+8]) != "moof" {
			continue
		}
		if moofs == n {
			return b[:off]
		}
		moofs++
	}
	t.Fatalf("stream has %d fragments, want more than %d", moofs, n)
	return nil
}

// clearDuration re-imports media-alpha's file without a duration, as a scan
// whose probe failed leaves it.
func (fx *encryptedVideoFixture) clearDuration(t *testing.T) {
	t.Helper()
	filter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	rows, err := fx.st.VisibleMediaBatch(context.Background(), []store.MediaID{"media-alpha"}, filter)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fx.st.MediaFilesForMedia(context.Background(), "media-alpha", filter)
	if err != nil || len(files) == 0 {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	media := rows["media-alpha"]
	if _, _, err := fx.st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: media.LibraryID, Path: files[0].Path, Title: media.Title, Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: files[0].ContentFingerprint,
	}); err != nil {
		t.Fatal(err)
	}
}

// topLevelBoxTypes lists b's top-level box types and the sidx's offset.
func topLevelBoxTypes(t *testing.T, b []byte) (types []string, sidxAt, sidxSize int) {
	t.Helper()
	sidxAt = -1
	for off := 0; off < len(b); {
		if len(b)-off < 8 {
			t.Fatalf("trailing %d bytes after the last box", len(b)-off)
		}
		size := int(binary.BigEndian.Uint32(b[off:]))
		if size < 8 || off+size > len(b) {
			t.Fatalf("box at %d has size %d", off, size)
		}
		typ := string(b[off+4 : off+8])
		if typ == "sidx" {
			sidxAt, sidxSize = off, size
		}
		types = append(types, typ)
		off += size
	}
	return types, sidxAt, sidxSize
}

// plaintextSource is a distinctive Media body and the windows of it that must
// never be found on disk.
func plaintextSource() (body []byte, needles [][]byte) {
	var b bytes.Buffer
	for i := 0; b.Len() < 200_000; i++ {
		fmt.Fprintf(&b, "encrypted-library plaintext frame %06d|", i)
	}
	body = b.Bytes()
	for off := 0; off+64 <= len(body); off += len(body) / 16 {
		needles = append(needles, body[off:off+64])
	}
	return body, needles
}

// scanDisk reports files under dirs that hold any needle, and stage files.
func scanDisk(t *testing.T, needles [][]byte, dirs ...string) (leaks, stages []string) {
	t.Helper()
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, encderiv.StageSuffix) {
				stages = append(stages, path)
			}
			body, err := os.ReadFile(path) //nolint:gosec // test-owned directories
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			for _, needle := range needles {
				if bytes.Contains(body, needle) {
					leaks = append(leaks, path)
					break
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return leaks, stages
}

// fetchSource reads the decrypted source the way FFmpeg would.
func fetchSource(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

type encryptedVideoFixture struct {
	svc       *Service
	st        *store.Store
	scope     auth.Scope
	profiles  *profileStore
	source    *lockedDecryptSource
	sealed    atomic.Bool
	body      []byte
	needles   [][]byte
	tmpDir    string
	device    *ecdh.PrivateKey
	fileCalls *fakeTranscoder
}

func newEncryptedVideoFixture(t *testing.T) *encryptedVideoFixture {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	svc, authService, st, _ := newFixture(t)
	setFixtureClass(t, st, "media-alpha", store.MediaClassVideo)
	scope := scopeFor(t, authService, "session-default")
	files := fixtureMediaFiles(t, st, scope, "media-alpha")
	body, needles := plaintextSource()
	device, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fx := &encryptedVideoFixture{
		svc: svc, st: st, scope: scope, body: body, needles: needles, tmpDir: tmpDir, device: device,
		source:    &lockedDecryptSource{bodies: map[string][]byte{files[0].Path: body}},
		fileCalls: &fakeTranscoder{out: map[string][]byte{}},
	}
	fx.profiles = &profileStore{Store: svc.store}
	svc.store = fx.profiles
	svc.SetEncryptedContent(fx.source, func(store.LibraryID) bool { return true }, func(store.LibraryID, string) (string, string, error) {
		return "media-file", "object-1", nil
	})
	svc.SetSealed(func(store.LibraryID) bool { return fx.sealed.Load() })
	svc.SetTranscoder(fx.fileCalls.transcode)
	svc.pipePoll = time.Millisecond
	return fx
}

func (fx *encryptedVideoFixture) request(quality string) BuildPackRequest {
	return BuildPackRequest{
		TargetKind: TargetKindMedia, TargetRef: "media-alpha", Quality: quality,
		DevicePublicKey: fx.device.PublicKey().Bytes(), DeviceAttestation: []byte("attested"),
	}
}

func (fx *encryptedVideoFixture) build(quality string) (store.OfflinePack, error) {
	return fx.svc.BuildPack(context.Background(), fx.scope, fx.request(quality))
}

// assertNothingLeft checks a failed build left no pack, no staging and no
// plaintext, and never fell back to the file transcoder.
func (fx *encryptedVideoFixture) assertNothingLeft(t *testing.T) {
	t.Helper()
	if entries := packRootEntries(t, fx.svc); len(entries) != 0 {
		t.Fatalf("pack root holds %v after a failed build", entries)
	}
	if leaks, stages := scanDisk(t, fx.needles, fx.svc.packRoot, fx.tmpDir); len(leaks) != 0 || len(stages) != 0 {
		t.Fatalf("plaintext in %v, staging %v after a failed build", leaks, stages)
	}
	packs, err := fx.st.OfflinePacksForProfile(context.Background(), fx.scope.ProfileID)
	if err != nil || len(packs) != 0 {
		t.Fatalf("packs = %+v, %v; want none", packs, err)
	}
	if len(fx.fileCalls.calls) != 0 {
		t.Fatalf("file transcoder ran %v for an Encrypted Library", fx.fileCalls.calls)
	}
}

// decryptItem unwraps the pack's CEK with the test device key and decrypts
// item 0 against its manifest entry.
func (fx *encryptedVideoFixture) decryptItem(t *testing.T, pack store.OfflinePack) []byte {
	t.Helper()
	claims := mustManifestClaims(t, fx.svc, pack.Manifest)
	envelope, err := base64.RawURLEncoding.DecodeString(claims.Wrap.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := BuildWrapAAD(WrapAADFields{
		Issuer: claims.Iss, PackID: claims.PackID, ProfileID: claims.ProfileID,
		DeviceEnrollmentID: claims.DeviceEnrollmentID, DeviceKeyThumbprint: claims.DeviceKeyThumbprint,
		ManifestSHA256: claims.ManifestSHA256, ManifestRevision: "1",
	})
	if err != nil {
		t.Fatal(err)
	}
	cek, err := UnwrapCEK(fx.device, envelope, aad, claims.Wrap.DeviceKID, claims.Wrap.EnvelopeSHA256)
	if err != nil {
		t.Fatalf("UnwrapCEK: %v", err)
	}
	manifestDigest, err := base64.RawURLEncoding.DecodeString(claims.ManifestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	items, err := fx.st.OfflinePackItems(context.Background(), pack.ID)
	if err != nil || len(items) != 1 || len(claims.Items) != 1 {
		t.Fatalf("items = %d / manifest %d, %v; want 1", len(items), len(claims.Items), err)
	}
	itemDigest, err := base64.RawURLEncoding.DecodeString(claims.Items[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(items[0].CiphertextPath)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecryptPackItem(cek, manifestDigest, 0, claims.Items[0].Size, itemDigest, file)
	if err != nil {
		t.Fatalf("DecryptPackItem: %v", err)
	}
	if items[0].PlaintextSize != int64(len(plain)) || items[0].SHA256 != claims.Items[0].SHA256 || items[0].SHA256 != digestOf(plain) {
		t.Fatalf("store item %d/%s, manifest %s; want the sealed layout's size and digest", items[0].PlaintextSize, items[0].SHA256, claims.Items[0].SHA256)
	}
	return plain
}

func TestBuildTranscodesEncryptedLibraryVideoInsideTheProtectedPlane(t *testing.T) {
	for _, quality := range []string{QualityHigh, QualityMedium, QualityLow} {
		t.Run(quality, func(t *testing.T) {
			fx := newEncryptedVideoFixture(t)
			var (
				mu                  sync.Mutex
				stream              []byte
				gotQuality          string
				midLeaks, midStages []string
				stageMode           fs.FileMode
			)
			fx.svc.SetPipeTranscoder(func(ctx context.Context, in encderiv.Input, stdout io.Writer, quality string, _ []store.MediaFileStream) error {
				src, err := fetchSource(ctx, in.URL)
				if err != nil {
					return err
				}
				if !bytes.Equal(src, fx.body) {
					return errors.New("endpoint served other bytes than the decrypted source")
				}
				out := fragmentedMP4(src, 4)
				half := len(out) / 2
				if _, err := stdout.Write(out[:half]); err != nil {
					return err
				}
				leaks, stages := scanDisk(t, fx.needles, fx.svc.packRoot, fx.tmpDir)
				var mode fs.FileMode
				if len(stages) == 1 {
					if info, err := os.Stat(stages[0]); err == nil {
						mode = info.Mode().Perm()
					}
				}
				mu.Lock()
				stream, gotQuality, midLeaks, midStages, stageMode = out, quality, leaks, stages, mode
				mu.Unlock()
				_, err = stdout.Write(out[half:])
				return err
			})
			pack, err := fx.build(quality)
			if err != nil {
				t.Fatalf("BuildPack: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if gotQuality != quality {
				t.Fatalf("pipe transcoder quality = %q, want %q", gotQuality, quality)
			}
			if len(midLeaks) != 0 {
				t.Fatalf("plaintext on disk mid-transcode in %v", midLeaks)
			}
			if len(midStages) != 1 || stageMode != 0o600 {
				t.Fatalf("mid-transcode staging = %v mode %o, want one 0600 stage file", midStages, stageMode)
			}
			if fx.source.openCount() < 2 {
				t.Fatalf("decrypt opens = %d, want the size probe and an endpoint read", fx.source.openCount())
			}
			if len(fx.fileCalls.calls) != 0 {
				t.Fatalf("file transcoder ran %v for an Encrypted Library", fx.fileCalls.calls)
			}
			if leaks, stages := scanDisk(t, fx.needles, fx.svc.packRoot, fx.tmpDir); len(leaks) != 0 || len(stages) != 0 {
				t.Fatalf("after the build: plaintext in %v, staging %v", leaks, stages)
			}

			final := fx.decryptItem(t, pack)
			types, sidxAt, sidxSize := topLevelBoxTypes(t, final)
			want := []string{"ftyp", "moov", "sidx", "moof", "mdat", "moof", "mdat", "moof", "mdat", "moof", "mdat"}
			if strings.Join(types, ",") != strings.Join(want, ",") {
				t.Fatalf("sealed item boxes = %v, want %v", types, want)
			}
			withoutIndex := append(bytes.Clone(final[:sidxAt]), final[sidxAt+sidxSize:]...)
			if !bytes.Equal(withoutIndex, stream) {
				t.Fatal("sealed item minus its sidx differs from the transcoder's stream")
			}
		})
	}
}

// TestBuildEncryptedTranscodeStopsWhenAuthorizationEnds pins that a Seal, a
// disabled Profile, a withdrawn Library grant or a vanished Profile landing
// mid-transcode revokes the endpoint, ends the transcode and leaves nothing
// staged.
func TestBuildEncryptedTranscodeStopsWhenAuthorizationEnds(t *testing.T) {
	cases := []struct {
		name   string
		revoke func(*encryptedVideoFixture)
	}{
		{"library sealed", func(fx *encryptedVideoFixture) { fx.sealed.Store(true) }},
		{"profile disabled", func(fx *encryptedVideoFixture) {
			fx.profiles.setEdit(func(p *store.Profile) error { p.Disabled = true; return nil })
		}},
		{"library withdrawn", func(fx *encryptedVideoFixture) {
			fx.profiles.setEdit(func(p *store.Profile) error { p.Library = nil; return nil })
		}},
		{"profile gone", func(fx *encryptedVideoFixture) {
			fx.profiles.setEdit(func(*store.Profile) error { return sql.ErrNoRows })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newEncryptedVideoFixture(t)
			if tc.name == "library withdrawn" && fx.scope.AllLibraries {
				t.Fatal("fixture scope grants every Library; the withdrawn-grant case needs a Profile-scoped session")
			}
			var stopped atomic.Bool
			fx.svc.SetPipeTranscoder(func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
				src, err := fetchSource(ctx, in.URL)
				if err != nil {
					return err
				}
				out := fragmentedMP4(src, 4)
				if _, err := stdout.Write(out[:len(out)/2]); err != nil {
					return err
				}
				tc.revoke(fx)
				select {
				case <-ctx.Done():
					stopped.Store(true)
					return ctx.Err()
				case <-time.After(10 * time.Second):
					return errors.New("transcode was not stopped")
				}
			})
			_, err := fx.build(QualityMedium)
			if !errors.Is(err, encderiv.ErrRevoked) {
				t.Fatalf("BuildPack err = %v, want ErrRevoked", err)
			}
			if !stopped.Load() {
				t.Fatal("revocation did not end the transcode")
			}
			fx.assertNothingLeft(t)
		})
	}
}

// TestBuildEncryptedTranscodeRevokedAfterItsOutput pins that a Seal landing
// as the transcode exits still keeps the staged derivative out of the pack.
func TestBuildEncryptedTranscodeRevokedAfterItsOutput(t *testing.T) {
	fx := newEncryptedVideoFixture(t)
	fx.svc.SetPipeTranscoder(func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
		src, err := fetchSource(ctx, in.URL)
		if err != nil {
			return err
		}
		_, err = stdout.Write(fragmentedMP4(src, 2))
		fx.sealed.Store(true)
		return err
	})
	if _, err := fx.build(QualityLow); !errors.Is(err, encderiv.ErrRevoked) {
		t.Fatalf("BuildPack err = %v, want ErrRevoked", err)
	}
	fx.assertNothingLeft(t)
}

// TestBuildEncryptedTranscodeSealedBeforeItsSeal pins that the staged
// derivative is re-authorized before it is sealed into the pack: a Seal that
// lands after the transcode finished still keeps it out.
func TestBuildEncryptedTranscodeSealedBeforeItsSeal(t *testing.T) {
	fx := newEncryptedVideoFixture(t)
	fx.svc.SetPipeTranscoder(func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
		src, err := fetchSource(ctx, in.URL)
		if err != nil {
			return err
		}
		_, err = stdout.Write(fragmentedMP4(src, 2))
		return err
	})
	fx.profiles.mu.Lock()
	fx.profiles.onProgress = func() { fx.sealed.Store(true) }
	fx.profiles.mu.Unlock()
	packID, err := NewPackID()
	if err != nil {
		t.Fatal(err)
	}
	req := fx.request(QualityHigh)
	req.PackID = packID
	if _, err := fx.svc.BuildPack(context.Background(), fx.scope, req); !errors.Is(err, encderiv.ErrRevoked) {
		t.Fatalf("BuildPack err = %v, want ErrRevoked", err)
	}
	fx.assertNothingLeft(t)
}

// readHook runs its function once read and then reports EOF, so an
// io.MultiReader can land a change part-way through a stream.
type readHook func()

func (h readHook) Read([]byte) (int, error) {
	h()
	return 0, io.EOF
}

// TestBuildEncryptedTranscodeRevokedWhileItemsSeal pins that a Seal, a
// disabled Profile or a withdrawn Library grant landing while the final item
// is encrypted into the pack, or after it is sealed but before the pack is
// signed, discards the pack: nothing ready is persisted and nothing is left.
func TestBuildEncryptedTranscodeRevokedWhileItemsSeal(t *testing.T) {
	revocations := []struct {
		name   string
		revoke func(*encryptedVideoFixture)
	}{
		{"library sealed", func(fx *encryptedVideoFixture) { fx.sealed.Store(true) }},
		{"profile disabled", func(fx *encryptedVideoFixture) {
			fx.profiles.setEdit(func(p *store.Profile) error { p.Disabled = true; return nil })
		}},
		{"library withdrawn", func(fx *encryptedVideoFixture) {
			fx.profiles.setEdit(func(p *store.Profile) error { p.Library = nil; return nil })
		}},
	}
	for _, rv := range revocations {
		for _, during := range []bool{true, false} {
			name := rv.name + "/after the final item is sealed"
			if during {
				name = rv.name + "/during the final item's encryption"
			}
			t.Run(name, func(t *testing.T) {
				fx := newEncryptedVideoFixture(t)
				if fx.scope.AllLibraries {
					t.Fatal("fixture scope grants every Library; the withdrawn-grant case needs a Profile-scoped session")
				}
				fx.svc.SetPipeTranscoder(func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
					src, err := fetchSource(ctx, in.URL)
					if err != nil {
						return err
					}
					_, err = stdout.Write(fragmentedMP4(src, 2))
					return err
				})
				// The fixture's target has one item, so the first item
				// encrypted is the final one.
				var encrypted atomic.Int32
				var revokedMid, itemSealed, reportedAfter atomic.Bool
				fx.svc.encryptItem = func(dst io.Writer, cek, manifestDigest []byte, index uint32, size uint64, digest []byte, src io.Reader) error {
					encrypted.Add(1)
					if during {
						src = io.MultiReader(io.LimitReader(src, 1024), readHook(func() {
							rv.revoke(fx)
							revokedMid.Store(true)
						}), src)
					}
					err := EncryptPackItemTo(dst, cek, manifestDigest, index, size, digest, src)
					itemSealed.Store(true)
					return err
				}
				fx.profiles.mu.Lock()
				fx.profiles.onProgress = func() {
					if !itemSealed.Load() {
						return
					}
					reportedAfter.Store(true)
					if !during {
						rv.revoke(fx)
					}
				}
				fx.profiles.mu.Unlock()
				packID, err := NewPackID()
				if err != nil {
					t.Fatal(err)
				}
				req := fx.request(QualityMedium)
				req.PackID = packID
				if _, err := fx.svc.BuildPack(context.Background(), fx.scope, req); !errors.Is(err, encderiv.ErrRevoked) {
					t.Fatalf("BuildPack err = %v, want ErrRevoked", err)
				}
				if encrypted.Load() != 1 || during != revokedMid.Load() {
					t.Fatalf("items encrypted = %d, revoked mid-item = %v; want 1, %v", encrypted.Load(), revokedMid.Load(), during)
				}
				if !reportedAfter.Load() && !during {
					t.Fatal("the revocation never landed after the item sealed")
				}
				// Revoked mid-item, the build stops at that item rather than
				// counting it sealed.
				if during && reportedAfter.Load() {
					t.Fatal("build reported the revoked item sealed")
				}
				if _, err := fx.st.OfflinePackByID(context.Background(), packID, fx.scope.ProfileID); !errors.Is(err, store.ErrOfflinePackNotFound) {
					t.Fatalf("OfflinePackByID err = %v, want no pack", err)
				}
				fx.assertNothingLeft(t)
			})
		}
	}
}

// TestBuildEncryptedTranscodeRefusals pins the refusals that never stage
// anything: a Sealed Library resolves empty before any transcode, and without
// the pipe transcoder an Encrypted Library refuses rather than falling back to
// the file transcoder. A transcoder that fails or writes unusable output
// fails the item even when it exits cleanly.
func TestBuildEncryptedTranscodeRefusals(t *testing.T) {
	t.Run("sealed library", func(t *testing.T) {
		fx := newEncryptedVideoFixture(t)
		fx.sealed.Store(true)
		var ran atomic.Bool
		fx.svc.SetPipeTranscoder(func(context.Context, encderiv.Input, io.Writer, string, []store.MediaFileStream) error {
			ran.Store(true)
			return nil
		})
		if _, err := fx.build(QualityHigh); !errors.Is(err, ErrEmptyTarget) {
			t.Fatalf("BuildPack err = %v, want ErrEmptyTarget", err)
		}
		if ran.Load() || fx.source.openCount() != 0 {
			t.Fatalf("sealed Library reached the transcoder (%v) or decrypted %d times", ran.Load(), fx.source.openCount())
		}
		fx.assertNothingLeft(t)
	})
	t.Run("no pipe transcoder", func(t *testing.T) {
		fx := newEncryptedVideoFixture(t)
		_, err := fx.build(QualityHigh)
		if !errors.Is(err, ErrTranscode) || FailureReason(err) != FailureTranscode {
			t.Fatalf("BuildPack err = %v, want ErrTranscode", err)
		}
		if fx.source.openCount() != 0 {
			t.Fatalf("decrypted %d times without a pipe transcoder", fx.source.openCount())
		}
		fx.assertNothingLeft(t)
	})
	for name, transcode := range map[string]PipeTranscoder{
		"transcoder error": func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
			src, err := fetchSource(ctx, in.URL)
			if err != nil {
				return err
			}
			out := fragmentedMP4(src, 2)
			_, _ = stdout.Write(out[:len(out)/2])
			return errors.New("ffmpeg: exit status 1")
		},
		"unusable output": func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
			src, err := fetchSource(ctx, in.URL)
			if err != nil {
				return err
			}
			_, err = stdout.Write(src)
			return err
		},
		// Three of four 30-second fragments: well formed, but short of the
		// source by more than the tolerance, as FFmpeg leaves a source it
		// could only partly decode.
		"short output, clean exit": func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
			src, err := fetchSource(ctx, in.URL)
			if err != nil {
				return err
			}
			_, err = stdout.Write(firstFragments(t, fragmentedMP4(src, 4), 3))
			return err
		},
		"truncated output": func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
			src, err := fetchSource(ctx, in.URL)
			if err != nil {
				return err
			}
			out := fragmentedMP4(src, 2)
			_, err = stdout.Write(out[:len(out)-7])
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newEncryptedVideoFixture(t)
			fx.svc.SetPipeTranscoder(transcode)
			_, err := fx.build(QualityMedium)
			if !errors.Is(err, ErrTranscode) || FailureReason(err) != FailureTranscode {
				t.Fatalf("BuildPack err = %v, want ErrTranscode", err)
			}
			fx.assertNothingLeft(t)
		})
	}
	t.Run("unknown source duration", func(t *testing.T) {
		fx := newEncryptedVideoFixture(t)
		fx.clearDuration(t)
		fx.svc.SetPipeTranscoder(func(context.Context, encderiv.Input, io.Writer, string, []store.MediaFileStream) error {
			t.Error("transcoder ran for a source of unknown duration")
			return nil
		})
		_, err := fx.build(QualityHigh)
		if !errors.Is(err, ErrTranscode) || FailureReason(err) != FailureTranscode {
			t.Fatalf("BuildPack err = %v, want ErrTranscode", err)
		}
		fx.assertNothingLeft(t)
	})
	t.Run("cancelled", func(t *testing.T) {
		fx := newEncryptedVideoFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fx.svc.SetPipeTranscoder(func(tctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
			src, err := fetchSource(tctx, in.URL)
			if err != nil {
				return err
			}
			out := fragmentedMP4(src, 2)
			if _, err := stdout.Write(out[:len(out)/2]); err != nil {
				return err
			}
			cancel()
			<-tctx.Done()
			return errors.New("ffmpeg: exit status 255")
		})
		_, err := fx.svc.BuildPack(ctx, fx.scope, fx.request(QualityHigh))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("BuildPack err = %v, want context.Canceled so the job yields", err)
		}
		fx.assertNothingLeft(t)
	})
}

// TestBuildEncryptedTranscodeRefusesATruncatedSourceWithRealFFmpeg builds from
// a faststart MP4 cut in half: FFmpeg reads the whole moov and then runs out of
// media data, which it would otherwise encode as far as it could and exit 0.
func TestBuildEncryptedTranscodeRefusesATruncatedSourceWithRealFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video")
	}
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "source.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	gen := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-loglevel", "error", "-nostdin", //nolint:gosec // fixed test argv
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=24:duration=12",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-movflags", "+faststart", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot generate the source: %v: %s", err, out)
	}
	full, err := os.ReadFile(path) //nolint:gosec // test temp file
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	profile, ok := ffmpeg.PackProfileByName("pack-480p")
	if !ok {
		t.Fatal("no pack-480p profile")
	}
	fx := newEncryptedVideoFixture(t)
	fx.source.mu.Lock()
	for name := range fx.source.bodies {
		fx.source.bodies[name] = full[:len(full)/2]
	}
	fx.source.mu.Unlock()
	fx.svc.SetPipeTranscoder(func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
		return ffmpeg.New().TranscodeToPipe(ctx, ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}, profile, stdout)
	})
	_, err = fx.svc.BuildPack(ctx, fx.scope, fx.request(QualityMedium))
	if errors.Is(err, ffmpeg.ErrPipeConfinementUnavailable) {
		t.Skip(err)
	}
	if !errors.Is(err, ErrTranscode) || FailureReason(err) != FailureTranscode {
		t.Fatalf("BuildPack err = %v, want ErrTranscode", err)
	}
	if fx.source.openCount() == 0 {
		t.Fatal("FFmpeg never read the source")
	}
	fx.assertNothingLeft(t)
}

// TestBuildEncryptedTranscodeChecksTheSelectedVersionsDuration gives
// media-alpha a second Version of a different length: the default Version runs
// 120 seconds, the duration the catalog probed from it, and an "extended" cut
// runs 180 seconds, which the catalog does not record. Each transcode yields
// 120 seconds of output. That is all of the selected default Version, so it
// seals; it is two thirds of the selected extended cut, so that build is
// refused before anything is decrypted rather than checked against the
// default Version's length.
func TestBuildEncryptedTranscodeChecksTheSelectedVersionsDuration(t *testing.T) {
	build := func(t *testing.T, version string) (*encryptedVideoFixture, store.OfflinePack, error) {
		t.Helper()
		fx := newEncryptedVideoFixture(t)
		extended := filepath.Join(t.TempDir(), "media-alpha-extended.mkv")
		if err := os.WriteFile(extended, []byte("extended-cut"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fx.st.AddMediaFileVersion(context.Background(), "media-alpha", store.MediaFile{
			Path: extended, ContentFingerprint: store.HashMediaFilePath(extended), VersionLabel: "extended",
		}, nil); err != nil {
			t.Fatalf("AddMediaFileVersion: %v", err)
		}
		fx.source.mu.Lock()
		fx.source.bodies[extended] = fx.body
		fx.source.mu.Unlock()
		fx.svc.SetPipeTranscoder(func(ctx context.Context, in encderiv.Input, stdout io.Writer, _ string, _ []store.MediaFileStream) error {
			src, err := fetchSource(ctx, in.URL)
			if err != nil {
				return err
			}
			_, err = stdout.Write(fragmentedMP4(src, 4))
			return err
		})
		req := fx.request(QualityMedium)
		req.SelectedVersions = map[store.MediaID]string{"media-alpha": version}
		pack, err := fx.svc.BuildPack(context.Background(), fx.scope, req)
		return fx, pack, err
	}
	t.Run("complete default Version", func(t *testing.T) {
		fx, pack, err := build(t, "")
		if err != nil {
			t.Fatalf("BuildPack of the complete default Version: %v", err)
		}
		items, err := fx.st.OfflinePackItems(context.Background(), pack.ID)
		if err != nil || len(items) != 1 {
			t.Fatalf("pack items = %+v, %v; want the one transcoded item", items, err)
		}
	})
	t.Run("truncated extended cut", func(t *testing.T) {
		fx, _, err := build(t, "extended")
		if !errors.Is(err, ErrTranscode) || FailureReason(err) != FailureTranscode {
			t.Fatalf("BuildPack err = %v, want ErrTranscode", err)
		}
		if fx.source.openCount() != 0 {
			t.Fatalf("decrypted %d times for a Version of unknown duration", fx.source.openCount())
		}
		fx.assertNothingLeft(t)
	})
}

func TestSweepStagingRemovesOnlyStagingFiles(t *testing.T) {
	svc, _, _, _ := newFixture(t)
	packDir := filepath.Join(svc.packRoot, "pack-crashed")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(packDir, "0000"+encderiv.StageSuffix)
	keep := filepath.Join(packDir, "0001.bfop")
	for _, path := range []string{stage, keep} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SweepStaging(); err != nil {
		t.Fatalf("SweepStaging: %v", err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stage file survived the sweep: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("sweep removed a pack file: %v", err)
	}
	if err := NewService(nil, nil, nil).SweepStaging(); err != nil {
		t.Fatalf("SweepStaging without a pack root = %v, want nil", err)
	}
}
