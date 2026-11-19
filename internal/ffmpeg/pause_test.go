//go:build darwin || linux

package ffmpeg

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPacedOutputReceiveEnforcesFileSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{
		{name: "at limit", size: 32},
		{name: "one byte over", size: 33},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			out, err := New().NewPacedHLSOutput(filepath.Join(root, "cache"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = out.Close() })
			if err := out.Gate(0); err != nil {
				t.Fatal(err)
			}
			const name, playlist = "seg00000.m4s", "live.m3u8"
			// Keep the read end pinned so the fixture can finish before
			// Receive opens its own descriptor and reads the buffered data.
			path := filepath.Join(out.Dir, name)
			fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			writer, err := os.OpenFile(path, os.O_WRONLY, 0) // #nosec G304 -- test-owned gate
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := io.WriteString(writer, strings.Repeat("x", tc.size))
			if err := errors.Join(writeErr, writer.Close()); err != nil {
				t.Fatal(err)
			}
			tmp := filepath.Join(out.Dir, playlist+".tmp")
			if err := os.WriteFile(tmp, []byte("#EXTM3U\n"+name+"\n#EXT-X-ENDLIST\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, filepath.Join(out.Dir, playlist)); err != nil {
				t.Fatal(err)
			}
			exited := make(chan struct{})
			close(exited)
			err = out.Receive(0, playlist, exited, 32)
			if tc.size == 32 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), "exceeds the 32 byte file limit") {
					t.Fatalf("oversized fragment = %v, want a size error without EOF", err)
				}
				f, openErr := out.Open(name)
				if openErr == nil {
					_ = f.Close()
					t.Fatal("oversized fragment became playable")
				}
			}
			staging, err := filepath.Glob(filepath.Join(out.Dir, ".receive-*"))
			if err != nil || len(staging) != 0 {
				t.Fatalf("receive staging after completion = %v, %v", staging, err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(out.Dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("encoder staging after Close = %v", err)
			}
		})
	}
}

func TestPacedOutputPinsMuxerDirectoryAndPublishesRegularFiles(t *testing.T) {
	dst := t.TempDir()
	out, err := New().NewPacedHLSOutput(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	const name = "seg00000.m4s"
	if err := os.WriteFile(filepath.Join(out.Dir, name), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(out.Dir, out.Dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(out.Dir + "-moved") })
	if err := os.Mkdir(out.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out.Dir, name), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := out.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	err = out.Publish(name, f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dst, name)) // #nosec G304 -- test-owned cache
	if err != nil || string(data) != "original" {
		t.Fatalf("published data = %q, %v", data, err)
	}
}

func TestPacedOutputRejectsUnsafeMuxerFiles(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "cache")
	if err := os.Mkdir(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := New().NewPacedHLSOutput(dst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	outside := filepath.Join(root, "private")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	const name = "init.mp4"
	for _, test := range []struct {
		name string
		make func(string) error
	}{
		{"symlink", func(path string) error { return os.Symlink(outside, path) }},
		{"hardlink", func(path string) error { return os.Link(outside, path) }},
		{"fifo", func(path string) error { return unix.Mkfifo(path, 0o600) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(out.Dir, name)
			if err := test.make(path); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			f, err := out.Open(name)
			if err == nil {
				_ = f.Close()
				t.Fatal("opened an unsafe muxer file")
			}
		})
	}
	if err := out.Publish("../private", strings.NewReader("replacement")); err == nil {
		t.Fatal("published a path outside the cache")
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("encoder directory survived Close: %v", err)
	}
}
