package genassets

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newPublicationTestService(t *testing.T) *Service {
	t.Helper()
	return &Service{dir: filepath.Join(t.TempDir(), "generated")}
}

func TestPublicationConcurrentSameInputIsIdempotent(t *testing.T) {
	svc := newPublicationTestService(t)
	const workers = 16
	start := make(chan struct{})
	errs := make(chan error, workers)
	var ready sync.WaitGroup
	ready.Add(workers)
	for range workers {
		go func() {
			ready.Done()
			<-start
			errs <- svc.publishBytes("media-a", "sha256:same", "waveform:v1", WaveformName, []byte("same-content"))
		}()
	}
	ready.Wait()
	close(start)
	for range workers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent same-input publication: %v", err)
		}
	}
	got, err := os.ReadFile(svc.WaveformPath("sha256:same"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "same-content" {
		t.Fatalf("published bytes = %q, want same-content", got)
	}
}

func TestPublicationConcurrentDifferentInputsHaveDistinctIdentity(t *testing.T) {
	svc := newPublicationTestService(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, input := range []struct {
		owner, source, content string
	}{{"media-a", "sha256:a", "content-a"}, {"media-b", "sha256:b", "content-b"}} {
		input := input
		go func() {
			<-start
			errs <- svc.publishBytes(input.owner, input.source, "waveform:v1", WaveformName, []byte(input.content))
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for source, want := range map[string]string{"sha256:a": "content-a", "sha256:b": "content-b"} {
		got, err := os.ReadFile(svc.WaveformPath(source))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s bytes = %q, want %q", source, got, want)
		}
	}
}

func TestPublicationRejectsInjectedGenerationCollision(t *testing.T) {
	svc := newPublicationTestService(t)
	publication, err := svc.beginPublication("media-a", "sha256:a", "waveform:v1", WaveformName)
	if err != nil {
		t.Fatal(err)
	}
	defer publication.close()
	data := []byte("honest-content")
	stageName := WaveformName
	if err := publication.root.WriteFile(filepath.Join(publication.stageDir, stageName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	contentHash := sha256.Sum256(data)
	generation := publicationDigest("media-a", "sha256:a", "waveform:v1", publication.publicName, stringHex(contentHash[:]))
	if err := publication.root.WriteFile(filepath.Join(assetGenerationDir, generation), []byte("collision"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publication.commit(stageName, publication.publicName); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("commit error = %v, want ErrPublicationConflict", err)
	}
	if _, err := os.Stat(svc.WaveformPath("sha256:a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("collision published a public asset: %v", err)
	}
}

func TestPublicationExactCASRejectsDifferentContent(t *testing.T) {
	svc := newPublicationTestService(t)
	if err := svc.publishBytes("media-a", "sha256:a", "waveform:v1", WaveformName, []byte("winner")); err != nil {
		t.Fatal(err)
	}
	err := svc.publishBytes("media-a", "sha256:a", "waveform:v1", WaveformName, []byte("loser"))
	if !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("second publication error = %v, want ErrPublicationConflict", err)
	}
	got, err := os.ReadFile(svc.WaveformPath("sha256:a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "winner" {
		t.Fatalf("exact CAS overwrote winner with %q", got)
	}
}

func TestSweepPublicationResidueRemovesOnlyUnreferencedGenerations(t *testing.T) {
	svc := newPublicationTestService(t)
	if err := svc.publishBytes("media-a", "sha256:a", "waveform:v1", WaveformName, []byte("referenced")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(svc.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(filepath.Join(assetGenerationDir, "unreferenced"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = root.Close()

	removed, err := svc.SweepPublicationResidue()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want one unreferenced generation", removed)
	}
	got, err := os.ReadFile(svc.WaveformPath("sha256:a"))
	if err != nil || string(got) != "referenced" {
		t.Fatalf("referenced generation was removed: bytes=%q err=%v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(svc.dir, assetGenerationDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("generation count = %d, want only the referenced generation", len(entries))
	}
}

func TestNewServiceRecoversCrashedStagingBeforeGeneration(t *testing.T) {
	dataDir := t.TempDir()
	generated := filepath.Join(dataDir, "generated")
	crashed := filepath.Join(generated, assetStagingDir, "crashed.stage")
	if err := os.MkdirAll(crashed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crashed, "partial"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, nil, dataDir)
	if svc.publishErr != nil {
		t.Fatalf("startup recovery failed: %v", svc.publishErr)
	}
	if _, err := os.Stat(crashed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("crashed staging survived startup recovery: %v", err)
	}
}

func TestPurgeFingerprintCollectsItsUnreferencedGeneration(t *testing.T) {
	svc := newPublicationTestService(t)
	if err := svc.publishBytes("media-a", "sha256:a", "waveform:v1", WaveformName, []byte("private-derivative")); err != nil {
		t.Fatal(err)
	}
	if err := svc.PurgeFingerprint("sha256:a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(svc.WaveformPath("sha256:a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("public asset survived purge: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(svc.dir, assetGenerationDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("immutable generation survived purge: %v", entries)
	}
}

func TestPublicationRejectsSymlinkedFingerprintDirectory(t *testing.T) {
	svc := newPublicationTestService(t)
	outside := t.TempDir()
	if err := os.MkdirAll(svc.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(svc.dir, assetFingerprintDir("sha256:a"))); err != nil {
		t.Fatal(err)
	}
	err := svc.publishBytes("media-a", "sha256:a", "waveform:v1", WaveformName, []byte("escaped"))
	if err == nil {
		t.Fatal("publication through an escaping symlink succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, WaveformName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asset escaped root: %v", err)
	}
}

func TestPublicationRootSwapFailsClosed(t *testing.T) {
	svc := newPublicationTestService(t)
	parent := filepath.Dir(svc.dir)
	attackerRoot := svc.dir
	movedRoot := filepath.Join(parent, "generated-original")
	err := svc.publishGeneratedFile("media-a", "sha256:a", "preview:v1", PreviewClipName, func(path string) error {
		if err := os.Rename(svc.dir, movedRoot); err != nil {
			return err
		}
		if err := os.MkdirAll(attackerRoot, 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("attacker-root"), 0o600)
	})
	if err == nil {
		t.Fatal("publication succeeded after generated root was swapped")
	}
	if _, err := os.Stat(filepath.Join(attackerRoot, assetFingerprintDir("sha256:a"), PreviewClipName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asset published into replacement root: %v", err)
	}
}

func stringHex(data []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(data)*2)
	for i, value := range data {
		out[i*2] = digits[value>>4]
		out[i*2+1] = digits[value&0xf]
	}
	return string(out)
}
