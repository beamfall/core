package libencrypt

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readObject(t *testing.T, m *Manager, path string, obj ObjectContext) string {
	t.Helper()
	reader, err := m.OpenDecryptReader(testLib, path, obj)
	if err != nil {
		t.Fatalf("OpenDecryptReader: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	return string(got)
}

func TestEncryptObjectFromRoundTripsInEveryGeneration(t *testing.T) {
	payload := strings.Repeat("streamed object payload ", 9000) // spans several chunks
	for _, generation := range []string{"v2", "v1"} {
		t.Run(generation, func(t *testing.T) {
			m, _ := newTestManager()
			if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
				t.Fatalf("Provision: %v", err)
			}
			if generation == "v1" {
				forceV1Generation(t, m, testLib)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "object.enc")
			obj := GeneratedAssetObjectContext(testLib, "sha256:fixture", "subtitle-0")
			if err := m.EncryptObjectFrom(testLib, strings.NewReader(payload), path, obj); err != nil {
				t.Fatalf("EncryptObjectFrom: %v", err)
			}
			if got := readObject(t, m, path, obj); got != payload {
				t.Fatalf("round trip returned %d bytes, want %d", len(got), len(payload))
			}
			raw, err := os.ReadFile(path) // #nosec G304 -- test temp-dir path.
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte("streamed object payload")) {
				t.Fatal("ciphertext object contains plaintext")
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Fatalf("object mode = %v, want 0600", perm)
			}
			assertOnlyEntries(t, dir, "object.enc")
		})
	}
}

type failingObjectSource struct{ sent bool }

func (s *failingObjectSource) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true
		return copy(p, "partial plaintext before failure"), nil
	}
	return 0, errors.New("source failed")
}

func TestEncryptObjectFromFailureLeavesNoTempAndKeepsTarget(t *testing.T) {
	m, _ := newTestManager()
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "object.enc")
	obj := GeneratedAssetObjectContext(testLib, "sha256:fixture", "subtitle-0")
	if err := m.EncryptObjectFrom(testLib, strings.NewReader("good object"), path, obj); err != nil {
		t.Fatalf("EncryptObjectFrom: %v", err)
	}
	if err := m.EncryptObjectFrom(testLib, &failingObjectSource{}, path, obj); err == nil {
		t.Fatal("EncryptObjectFrom with a failing source succeeded")
	}
	assertOnlyEntries(t, dir, "object.enc")
	if got := readObject(t, m, path, obj); got != "good object" {
		t.Fatalf("target after failed write = %q, want the previous object", got)
	}

	fresh := filepath.Join(dir, "fresh.enc")
	if err := m.EncryptObjectFrom(testLib, &failingObjectSource{}, fresh, obj); err == nil {
		t.Fatal("EncryptObjectFrom with a failing source succeeded")
	}
	assertOnlyEntries(t, dir, "object.enc")
}

func TestEncryptObjectFromRefusesSealedAndInvalidContext(t *testing.T) {
	m, _ := newTestManager()
	dir := t.TempDir()
	path := filepath.Join(dir, "object.enc")
	obj := GeneratedAssetObjectContext(testLib, "sha256:fixture", "subtitle-0")
	if err := m.EncryptObjectFrom(testLib, strings.NewReader("x"), path, obj); !errors.Is(err, ErrSealed) {
		t.Fatalf("unprovisioned library: err = %v, want ErrSealed", err)
	}
	if _, err := m.Provision(testLib, UnsealModeAuto, ""); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := m.EncryptObjectFrom(testLib, strings.NewReader("x"), path, ObjectContext{}); !errors.Is(err, ErrObjectContext) {
		t.Fatalf("empty context: err = %v, want ErrObjectContext", err)
	}
	if err := m.EncryptObjectFrom(testLib, nil, path, obj); err == nil {
		t.Fatal("nil source succeeded")
	}
	assertOnlyEntries(t, dir)
}

func TestHasObjectHeaderTellsCiphertextFromPlaintext(t *testing.T) {
	dek := v2TestDEK()
	var v1, v2 bytes.Buffer
	if err := EncryptStream(&v1, strings.NewReader("cue text"), dek); err != nil {
		t.Fatal(err)
	}
	if err := EncryptStreamV2(&v2, strings.NewReader("cue text"), dek, testObj); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		in   []byte
		want bool
	}{
		"v1 object":         {v1.Bytes(), true},
		"v2 object":         {v2.Bytes(), true},
		"webvtt":            {[]byte("WEBVTT\n\n00:00.000 --> 00:01.000\ncue text\n"), false},
		"empty":             {nil, false},
		"short":             {[]byte("BFE2"), false},
		"wrong version":     {append([]byte("BFE2\x01"), v2.Bytes()[5:]...), false},
		"wrong chunk":       {append(append([]byte{}, v1.Bytes()[:5]...), 0, 0, 0, 1), false},
		"magic prefix only": {[]byte("BFE1 is not a header at all"), false},
	}
	for name, tc := range cases {
		got, err := HasObjectHeader(bytes.NewReader(tc.in))
		if err != nil || got != tc.want {
			t.Errorf("%s: HasObjectHeader = %v, %v; want %v", name, got, err, tc.want)
		}
	}
	if _, err := HasObjectHeader(iotestErrReader{}); err == nil {
		t.Error("read error was swallowed")
	}
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func assertOnlyEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("directory entries = %v, want %v", got, want)
	}
}
