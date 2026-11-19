package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
)

// fakeEncryptedTranscoder stands in for ffmpeg on an Encrypted Library: each
// start uploads an init segment, its media segments and a playlist through the
// run's loopback endpoint, as the real muxer does, before returning.
type fakeEncryptedTranscoder struct {
	fakeTranscoder
	encMu     sync.Mutex
	encStarts []int
	// segment returns the bytes of media segment n; nil means mediaSegment.
	segment func(n int) []byte
}

func (f *fakeEncryptedTranscoder) StartEncryptedHLSSegments(_ context.Context, ep ffmpeg.EncryptedLiveEndpoint, startSegment, _ int, _ ffmpeg.Rendition) (*ffmpeg.HLSProcess, error) {
	f.encMu.Lock()
	f.encStarts = append(f.encStarts, startSegment)
	segment := f.segment
	f.encMu.Unlock()
	if segment == nil {
		segment = func(int) []byte { return mediaSegment() }
	}
	uploads := []struct {
		name string
		body []byte
	}{{"init.mp4", mp4Box("ftyp", []byte("iso6"))}}
	for i := startSegment; i < startSegment+2; i++ {
		uploads = append(uploads, struct {
			name string
			body []byte
		}{fmt.Sprintf("seg%05d.m4s", i), segment(i)})
	}
	uploads = append(uploads, struct {
		name string
		body []byte
	}{"live.m3u8", []byte("#EXTM3U\n")})
	for _, u := range uploads {
		if err := putLive(ep.Output+"/"+u.name, u.body); err != nil {
			return nil, err
		}
	}
	return &ffmpeg.HLSProcess{}, nil
}

func (f *fakeEncryptedTranscoder) encryptedStarts() int {
	f.encMu.Lock()
	defer f.encMu.Unlock()
	return len(f.encStarts)
}

func putLive(url string, body []byte) error {
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	// A repeat init upload is refused (first writer wins), as with ffmpeg.
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("PUT %s: %s", url[strings.LastIndex(url, "/")+1:], resp.Status)
	}
	return nil
}

// testEncryptedSource is a source whose plaintext the engine never needs: the
// fake transcoder does not read it.
func testEncryptedSource(revoked *atomic.Bool) *EncryptedSource {
	src := &EncryptedSource{
		LibraryID: "library-sealed",
		Open: func() (io.ReadSeekCloser, error) {
			return nopReadSeekCloser{bytes.NewReader([]byte("plaintext-source"))}, nil
		},
		Size: int64(len("plaintext-source")),
	}
	if revoked != nil {
		src.Revoked = revoked.Load
	}
	return src
}

type nopReadSeekCloser struct{ *bytes.Reader }

func (nopReadSeekCloser) Close() error { return nil }

func encryptedFilePath(sessions *Sessions, key SessionKey, secure *EncryptedSource, durationSeconds, burnSubtitleIndex int, composed bool, name string) (string, error) {
	return sessions.FilePathShaped(context.Background(), key, "", durationSeconds, 1280, 720, false, false,
		ladderCaps{}, VideoSignal{}, burnSubtitleIndex, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, nil,
		StreamShape{Encrypted: secure, ComposedPicture: composed}, PriorityNormal, name)
}

// readLiveArtifact returns the decrypted bytes of an encrypted session file.
func readLiveArtifact(t *testing.T, sessions *Sessions, key SessionKey, path string) []byte {
	t.Helper()
	artifact, err := testArtifactForPath(sessions, key, path)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Release()
	f, err := artifact.OpenEncrypted()
	if err != nil {
		t.Fatalf("OpenLiveArtifact(%s): %v", filepath.Base(path), err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestEncryptedLiveSessionKeepsOnlyCiphertext runs a session on an Encrypted
// Library end to end through the engine: every file under the transcode root
// is ciphertext, OpenLiveArtifact decrypts it, and the plain path never runs.
func TestEncryptedLiveSessionKeepsOnlyCiphertext(t *testing.T) {
	t.Parallel()
	transcoder := &fakeEncryptedTranscoder{}
	root := t.TempDir()
	sessions := newTestSessions(t, transcoder, root, nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-sealed", MediaID: "media-sealed"}
	secure := testEncryptedSource(nil)

	master, err := encryptedFilePath(sessions, key, secure, 120, -1, false, "master.m3u8")
	if err != nil {
		t.Fatalf("master.m3u8: %v", err)
	}
	if raw := mustRead(t, master); bytes.Contains(raw, []byte("#EXTM3U")) {
		t.Fatal("master playlist stored in plaintext")
	}
	if got := string(readLiveArtifact(t, sessions, key, master)); !strings.Contains(got, "720p/index.m3u8") {
		t.Fatalf("decrypted master lacks the 720p rung:\n%s", got)
	}

	segment, err := encryptedFilePath(sessions, key, secure, 120, -1, false, "720p/seg00000.m4s")
	if err != nil {
		t.Fatalf("segment: %v", err)
	}
	if raw := mustRead(t, segment); bytes.Contains(raw, []byte("media-bytes")) || bytes.Contains(raw, []byte("moof")) {
		t.Fatal("segment stored in plaintext")
	}
	if got := readLiveArtifact(t, sessions, key, segment); !bytes.Equal(got, mediaSegment()) {
		t.Fatalf("decrypted segment = %q, want the uploaded bytes", got)
	}
	variant, err := encryptedFilePath(sessions, key, secure, 120, -1, false, "720p/index.m3u8")
	if err != nil {
		t.Fatalf("variant playlist: %v", err)
	}
	if got := string(readLiveArtifact(t, sessions, key, variant)); !strings.Contains(got, "seg00000.m4s") {
		t.Fatalf("decrypted variant playlist lacks segment 0:\n%s", got)
	}

	if transcoder.encryptedStarts() == 0 {
		t.Fatal("encrypted path never started")
	}
	transcoder.mu.Lock()
	plainStarts := len(transcoder.starts)
	transcoder.mu.Unlock()
	if plainStarts != 0 {
		t.Fatalf("plain transcode started %d times on an encrypted session", plainStarts)
	}

	// No file anywhere under the root carries a plaintext marker: the
	// uploaded playlist was discarded and nothing was stored as uploaded.
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw := mustRead(t, path)
		for _, marker := range []string{"#EXTM3U", "media-bytes", "moof", "ftyp", "plaintext-source"} {
			if bytes.Contains(raw, []byte(marker)) {
				t.Errorf("%s holds plaintext %q", filepath.Base(path), marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEncryptedLiveSessionEndsWhenRevoked proves a revoked source tears the
// session down: the directory goes and its files can no longer be opened.
func TestEncryptedLiveSessionEndsWhenRevoked(t *testing.T) {
	t.Parallel()
	sessions := newTestSessions(t, &fakeEncryptedTranscoder{}, t.TempDir(), nil)
	sessions.revocationPoll = 5 * time.Millisecond
	defer sessions.Close()
	key := SessionKey{SessionID: "session-revoke", MediaID: "media-revoke"}
	var revoked atomic.Bool

	segment, err := encryptedFilePath(sessions, key, testEncryptedSource(&revoked), 120, -1, false, "720p/seg00000.m4s")
	if err != nil {
		t.Fatalf("segment: %v", err)
	}
	dir := filepath.Dir(filepath.Dir(segment))
	revoked.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, statErr := os.Stat(dir)
		artifact, openErr := testArtifactForPath(sessions, key, segment)
		if artifact != nil {
			artifact.Release()
		}
		if errors.Is(statErr, os.ErrNotExist) && errors.Is(openErr, ErrSessionEvicted) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after revocation: stat err %v, open err %v", statErr, openErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestEncryptedLiveSessionRefusesUnsupportedShapes covers ADR-0236 D4.
func TestEncryptedLiveSessionRefusesUnsupportedShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		duration int
		burn     int
		composed bool
	}{
		{"burned-in subtitle", 120, 0, false},
		{"composed picture", 120, -1, true},
		{"unknown duration", 0, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transcoder := &fakeEncryptedTranscoder{}
			sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
			defer sessions.Close()
			key := SessionKey{SessionID: "session-shape", MediaID: "media-shape"}
			_, err := encryptedFilePath(sessions, key, testEncryptedSource(nil), tc.duration, tc.burn, tc.composed, "master.m3u8")
			if !errors.Is(err, ErrEncryptedLiveUnavailable) {
				t.Fatalf("err = %v, want ErrEncryptedLiveUnavailable", err)
			}
			if transcoder.encryptedStarts() != 0 {
				t.Fatal("refused shape started an encoder")
			}
		})
	}
}

// TestEncryptedLiveSessionNeedsTheEncryptedTranscoder refuses a session when
// the transcoder cannot run the encrypted path, rather than falling back to a
// plaintext transcode.
func TestEncryptedLiveSessionNeedsTheEncryptedTranscoder(t *testing.T) {
	t.Parallel()
	transcoder := &fakeTranscoder{}
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-plain", MediaID: "media-plain"}
	_, err := encryptedFilePath(sessions, key, testEncryptedSource(nil), 120, -1, false, "720p/seg00000.m4s")
	if !errors.Is(err, ErrEncryptedLiveUnavailable) {
		t.Fatalf("err = %v, want ErrEncryptedLiveUnavailable", err)
	}
	transcoder.mu.Lock()
	defer transcoder.mu.Unlock()
	if len(transcoder.starts) != 0 {
		t.Fatal("plain transcode started for an encrypted session")
	}
}

// TestEncryptedLiveSessionDetectsAnEmptySegmentThroughDecryption: the
// past-the-end check reads the decrypted segment, not its ciphertext.
func TestEncryptedLiveSessionDetectsAnEmptySegmentThroughDecryption(t *testing.T) {
	t.Parallel()
	transcoder := &fakeEncryptedTranscoder{segment: func(int) []byte { return emptySegment() }}
	sessions := newTestSessions(t, transcoder, t.TempDir(), nil)
	defer sessions.Close()
	key := SessionKey{SessionID: "session-empty", MediaID: "media-empty"}
	_, err := encryptedFilePath(sessions, key, testEncryptedSource(nil), 120, -1, false, "720p/seg00010.m4s")
	if !errors.Is(err, ErrSourceEnded) {
		t.Fatalf("err = %v, want ErrSourceEnded", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	return data
}
