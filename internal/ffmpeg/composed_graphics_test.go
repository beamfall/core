package ffmpeg

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestComposedGraphicsOptionsFollowTheOutputClock(t *testing.T) {
	r := Rendition{Height: 720}.WithAudioAdjust(AudioAdjust{Tempo: 1.25})
	got := composedGraphicsOptions(r, 30)
	if got.Width != 1280 || got.Height != 720 || got.FPS != 30 || got.Start != 37.5 || got.Tempo != 1.25 {
		t.Fatalf("options = %+v, want 1280x720 at 30 fps from source 37.5 s at 1.25x", got)
	}
	if got := composedGraphicsOptions(Rendition{Height: 360}, 0); got.Width != 640 || got.Start != 0 || got.Tempo != 1 {
		t.Fatalf("options = %+v, want 640x360 from 0 s at 1x", got)
	}
}

func TestOpenGraphicsReadsATrackBesideTheSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.cdg")
	if err := os.WriteFile(path, []byte("packs"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(newGraphicsStream(dir, path))
	if err != nil || string(b) != "packs" {
		t.Fatalf("stream = %q, %v; want the track", b, err)
	}
}

func TestOpenGraphicsFollowsALinkThatResolvesBesideTheSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "actual.cdg"), []byte("packs"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := filepath.Join(dir, "links")
	if err := os.Mkdir(links, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(links, "pick.cdg")
	if err := os.Symlink("../actual.cdg", path); err != nil {
		t.Skip(err)
	}
	b, err := io.ReadAll(newGraphicsStream(dir, path))
	if err != nil || string(b) != "packs" {
		t.Fatalf("stream = %q, %v; want the linked track", b, err)
	}
}

func TestOpenGraphicsRefusesATrackSwappedForAnEscapingSymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "song.cdg")
	if err := os.Symlink(secret, path); err != nil {
		t.Skip(err)
	}
	// The resolved path is outside the source directory: the root refuses it.
	if f, err := openGraphics(dir, path); err == nil {
		_ = f.Close()
		t.Fatal("openGraphics followed a symlink out of the source directory")
	}
}

func TestGraphicsStreamCutEndsAStalledRead(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	s := &graphicsStream{r: r}
	done := make(chan error, 1)
	go func() {
		_, err := s.Read(make([]byte, 8))
		done <- err
	}()
	s.cut()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Read after the cut returned no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cut did not end the read")
	}
	if err := s.result(errors.New("presenter saw a read error")); err != nil {
		t.Fatalf("result after a cut = %v, want nil", err)
	}
}

func TestStartComposedProcessReturnsWhenTheEncoderCannotStart(t *testing.T) {
	dir := t.TempDir()
	src, track := filepath.Join(dir, "song.mp3"), filepath.Join(dir, "song.cdg")
	for _, p := range []string{src, track} {
		if err := os.WriteFile(p, make([]byte, 7200), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(dir, "no-such-ffmpeg")) // #nosec G204 -- a test path that does not exist
	done := make(chan error, 1)
	go func() {
		_, err := startComposedProcess(t.Context(), cmd, src, ComposedPicture{GraphicsPath: track}, Rendition{Height: 360}, 0)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("start succeeded without an encoder")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("start hung joining the presenter after the encoder failed to start")
	}
}

// The playback handoff can kill the encoder and wait for it before start
// returns, while this process still holds the pipe's read end.
func TestComposedWaitReturnsWhenTheHandoffKillsTheEncoder(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	src, track := filepath.Join(dir, "song.mp3"), filepath.Join(dir, "song.cdg")
	for _, p := range []string{src, track} {
		if err := os.WriteFile(p, make([]byte, 7200*60), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	waited := make(chan error, 1)
	ctx := context.WithValue(t.Context(), playbackSpawnKey{}, playbackSpawn{
		permit: func() error { return nil },
		handoff: func(p *HLSProcess) {
			time.Sleep(200 * time.Millisecond) // let the presenter block on a frame
			p.killTree()
			waited <- p.Wait()
		},
	})
	cmd := exec.CommandContext(ctx, "sleep", "60")
	done := make(chan error, 1)
	go func() {
		_, err := startComposedProcess(ctx, cmd, src, ComposedPicture{GraphicsPath: track}, Rendition{Height: 360}, 0)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("start = %v", err)
		}
		select {
		case <-waited:
		default:
			t.Fatal("start returned without running the handoff")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("start hung: the handoff's Wait never returned")
	}
}

func TestComposedWaitReturnsAfterTheEncoderIsKilled(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	src, track := filepath.Join(dir, "song.mp3"), filepath.Join(dir, "song.cdg")
	for _, p := range []string{src, track} {
		if err := os.WriteFile(p, make([]byte, 7200*60), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// An encoder that never reads its stdin, killed as an eviction would.
	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	p, err := startComposedProcess(t.Context(), cmd, src, ComposedPicture{GraphicsPath: track}, Rendition{Height: 360}, 0)
	if err != nil {
		t.Fatal(err)
	}
	p.killTree()
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait hung after the encoder was killed")
	}
}
