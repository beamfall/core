//go:build darwin || linux

package ffmpeg

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

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
