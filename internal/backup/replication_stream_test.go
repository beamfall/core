package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// patternReader yields size deterministic pseudo-random bytes without ever
// materializing them: the fixture for large-object streaming tests.
type patternReader struct {
	offset int64
	size   int64
}

func patternByte(i int64) byte {
	return byte((i*131 + (i >> 7) + 17) % 251)
}

func (p *patternReader) Read(buf []byte) (int, error) {
	if p.offset >= p.size {
		return 0, io.EOF
	}
	n := int64(len(buf))
	if remaining := p.size - p.offset; remaining < n {
		n = remaining
	}
	for i := int64(0); i < n; i++ {
		buf[i] = patternByte(p.offset + i)
	}
	p.offset += n
	return int(n), nil
}

// heapSampler records the peak LIVE heap observed at sample points. When the
// raw HeapAlloc (which includes uncollected garbage) exceeds the soft limit it
// forces a GC and re-reads, so only genuinely referenced memory — a buffered
// whole object, not transient chunk garbage — can raise the recorded peak.
type heapSampler struct {
	mu        sync.Mutex
	softLimit uint64
	peak      uint64
}

func newHeapSampler() *heapSampler {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return &heapSampler{softLimit: ms.HeapAlloc + 24<<20, peak: ms.HeapAlloc}
}

func (h *heapSampler) sample() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapAlloc > h.softLimit {
		runtime.GC()
		runtime.ReadMemStats(&ms)
	}
	h.mu.Lock()
	if ms.HeapAlloc > h.peak {
		h.peak = ms.HeapAlloc
	}
	h.mu.Unlock()
}

// fileBucket is the bounded-buffer fake bucket the COREREV-18 verify criterion
// calls for: objects stream to temp files in 64KiB chunks (never a whole-object
// buffer in process memory), and every Put/Get chunk boundary samples the live
// heap so a regression back to whole-object buffering is caught as a peak.
type fileBucket struct {
	dir     string
	sampler *heapSampler
}

func (b *fileBucket) path(key string) string {
	return filepath.Join(b.dir, hex.EncodeToString([]byte(key)))
}

func (b *fileBucket) Put(_ context.Context, key string, r io.Reader) error {
	f, err := os.Create(b.path(key))
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	chunks := 0
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if chunks++; chunks%16 == 0 {
			b.sampler.sample()
		}
		if errors.Is(err, io.EOF) {
			b.sampler.sample()
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (b *fileBucket) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f, err := os.Open(b.path(key))
	if err != nil {
		return nil, fmt.Errorf("backup/test: key not found: %s", key)
	}
	return f, nil
}

func (b *fileBucket) Delete(_ context.Context, key string) error {
	_ = os.Remove(b.path(key))
	return nil
}

func (b *fileBucket) List(_ context.Context, prefix string) ([]string, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for _, entry := range entries {
		raw, err := hex.DecodeString(entry.Name())
		if err != nil {
			continue
		}
		if key := string(raw); len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func TestReplicationStreamsLargeObjectWithBoundedMemory(t *testing.T) {
	// COREREV-18 acceptance: replicating and restoring a large fixture keeps
	// peak allocation O(chunk). The 64MiB object streams source→encrypt→bucket
	// and bucket→decrypt→handler; a regression to io.ReadAll / bytes.Buffer
	// holds the whole object live and blows the bound.
	const objectSize = 64 << 20
	const heapBound = objectSize * 3 / 4

	sampler := newHeapSampler()
	baseline := sampler.peak
	bucket := &fileBucket{dir: t.TempDir(), sampler: sampler}
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  bucket,
		Key:     key,
		SetID:   "set-large-stream",
	})
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "large", Open: func() (io.ReadCloser, error) {
			return io.NopCloser(&patternReader{size: objectSize}), nil
		}},
	})
	if err != nil {
		t.Fatal("Replicate:", err)
	}
	if manifest.Objects[0].SizeBytes <= objectSize {
		t.Fatalf("ciphertext size = %d, want > plaintext %d", manifest.Objects[0].SizeBytes, objectSize)
	}
	if peak := sampler.peak; peak-baseline > heapBound {
		t.Fatalf("replicate peak live heap grew %d bytes (baseline %d), want O(chunk) < %d", peak-baseline, baseline, heapBound)
	}

	var restoredBytes int64
	mismatch := int64(-1)
	if err := replicator.Restore(context.Background(), func(object ReplicationManifestObject, r io.Reader) error {
		buf := make([]byte, 64<<10)
		chunks := 0
		for {
			n, err := r.Read(buf)
			for i := 0; i < n; i++ {
				if buf[i] != patternByte(restoredBytes+int64(i)) && mismatch < 0 {
					mismatch = restoredBytes + int64(i)
				}
			}
			restoredBytes += int64(n)
			if chunks++; chunks%16 == 0 {
				sampler.sample()
			}
			if errors.Is(err, io.EOF) {
				sampler.sample()
				return nil
			}
			if err != nil {
				return err
			}
		}
	}); err != nil {
		t.Fatal("Restore:", err)
	}
	if restoredBytes != objectSize || mismatch >= 0 {
		t.Fatalf("restored %d bytes (want %d), first mismatch at %d (want none)", restoredBytes, objectSize, mismatch)
	}
	if peak := sampler.peak; peak-baseline > heapBound {
		t.Fatalf("restore peak live heap grew %d bytes (baseline %d), want O(chunk) < %d", peak-baseline, baseline, heapBound)
	}
}

// failNthObjectGetBucket poisons the nth Get of one key mid-stream: the reader
// yields a prefix then errors, simulating a transient fetch drop.
type failNthObjectGetBucket struct {
	BucketClient
	key   string
	n     int
	calls int
}

type truncatedReader struct {
	io.ReadCloser
	remaining int
}

func (r *truncatedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errors.New("backup/test: transient stream drop")
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadCloser.Read(p)
	r.remaining -= n
	return n, err
}

func (b *failNthObjectGetBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := b.BucketClient.Get(ctx, key)
	if err != nil || key != b.key {
		return rc, err
	}
	b.calls++
	if b.calls == b.n {
		return &truncatedReader{ReadCloser: rc, remaining: 80}, nil
	}
	return rc, nil
}

func TestReplicationRestoreRetryRestartsObjectStream(t *testing.T) {
	// COREREV-18 retry contract: the streaming sink cannot be transparently
	// resumed mid-object, so a transient fetch/decrypt failure re-invokes the
	// handler with a fresh stream from byte 0 and the retried delivery is the
	// complete, correct plaintext.
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	inner := newMemBucket()
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  inner,
		Key:     key,
		SetID:   "set-restore-retry",
		Backoff: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := "payload that must survive a transient mid-stream drop"
	manifest, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte(payload))},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Object-key Gets: #1 is verifyCiphertext, #2 is the first restore attempt
	// (poisoned mid-stream), #3 is the retry.
	flaky := &failNthObjectGetBucket{BucketClient: inner, key: manifest.Objects[0].ObjectKey, n: 2}
	restoring, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  flaky,
		Key:     key,
		SetID:   "set-restore-retry",
		Backoff: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	invocations := 0
	deliveries := []string{}
	if err := restoring.Restore(context.Background(), func(object ReplicationManifestObject, r io.Reader) error {
		invocations++
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		deliveries = append(deliveries, string(data))
		return nil
	}); err != nil {
		t.Fatal("Restore:", err)
	}
	if invocations != 2 {
		t.Fatalf("handler invocations = %d, want 2 (poisoned attempt + fresh retry)", invocations)
	}
	if final := deliveries[len(deliveries)-1]; final != payload {
		t.Fatalf("final delivery = %q, want full payload", final)
	}
}

func TestReplicationMigrationDefaultRefusesKeylessManifest(t *testing.T) {
	// COREREV-19: without the BackupKey the manifest MAC cannot be checked, so
	// the copy is refused unless the operator explicitly opts into an
	// integrity-opaque migration.
	_, err := CopyReplicationSet(context.Background(), ReplicationMigrationOptions{
		Enabled:     true,
		Source:      newMemBucket(),
		Destination: newMemBucket(),
		SetID:       "set-keyless",
	})
	if !errors.Is(err, ErrManifestUnverified) {
		t.Fatalf("keyless copy err = %v, want ErrManifestUnverified", err)
	}
}

func TestReplicationLocatorsAreOpaqueAndDomainSeparated(t *testing.T) {
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	manifest := replicationBackupID(key, "manifest", "private-set", "")
	object := replicationBackupID(key, "object", "private-set", "catalog")
	receipt := replicationBackupID(key, "receipt", "private-set", "catalog")
	for name, backupID := range map[string]string{
		"manifest": manifest,
		"object":   object,
		"receipt":  receipt,
	} {
		locator := strings.TrimPrefix(backupID, "replication/v2/")
		if len(locator) != sha256.Size*2 {
			t.Errorf("%s locator length = %d, want %d", name, len(locator), sha256.Size*2)
		}
		if _, err := hex.DecodeString(locator); err != nil {
			t.Errorf("%s locator is not hex: %v", name, err)
		}
	}
	if manifest == object || manifest == receipt || object == receipt {
		t.Fatal("manifest, object, and receipt locators are not domain-separated")
	}
	if object != replicationBackupID(key, "object", "private-set", "catalog") {
		t.Fatal("locator is not deterministic under one key")
	}
	if object == replicationBackupID(otherKey, "object", "private-set", "catalog") {
		t.Fatal("locator is linkable across backup keys")
	}
	if replicationBackupID(key, "object", "a\x00b", "c") == replicationBackupID(key, "object", "a", "b\x00c") {
		t.Fatal("length-prefixed locator fields are ambiguous")
	}
}

func TestReplicationMigrationKeyedRejectsTamperedManifest(t *testing.T) {
	// COREREV-19: an attacker with source-bucket write access swaps an
	// ObjectKey/SHA256 row (self-consistent with tampered ciphertext). The
	// keyed migrator must refuse: the manifest MAC no longer verifies.
	ctx := context.Background()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newMemBucket()
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  source,
		Key:     key,
		SetID:   "set-manifest-swap",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicator.Replicate(ctx, []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog bytes"))},
	}); err != nil {
		t.Fatal(err)
	}

	// Rewrite the manifest's object row self-consistently (valid JSON, valid
	// shape, stale MAC) — the keyless shape checks cannot catch this.
	manifestBackupID := replicator.manifestBackupID()
	var plaintext bytes.Buffer
	if err := NewService(source).Restore(ctx, key, manifestBackupID, &plaintext); err != nil {
		t.Fatal(err)
	}
	var manifest ReplicationManifest
	if err := json.Unmarshal(plaintext.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Objects[0].SHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
	swapped, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewService(source).BackupObject(ctx, key, manifestBackupID, bytes.NewReader(swapped)); err != nil {
		t.Fatal(err)
	}

	_, err = CopyReplicationSet(ctx, ReplicationMigrationOptions{
		Enabled:     true,
		Source:      source,
		Destination: newMemBucket(),
		SetID:       "set-manifest-swap",
		Key:         key,
	})
	if !errors.Is(err, ErrManifestIntegrity) {
		t.Fatalf("keyed copy of tampered manifest err = %v, want ErrManifestIntegrity", err)
	}
}

func TestReplicationMigrationCannotBypassEncryptedManifestWithUnverifiedCopy(t *testing.T) {
	// GCR-020-B: v2 manifests and locators are encrypted/keyed metadata. The
	// legacy explicit integrity-opaque flag cannot make a keyless copy safe
	// because there is deliberately no plaintext object list to inspect.
	ctx := context.Background()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newMemBucket()
	destination := newMemBucket()
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  source,
		Key:     key,
		SetID:   "set-opaque",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicator.Replicate(ctx, []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("opaque copy payload"))},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = CopyReplicationSet(ctx, ReplicationMigrationOptions{
		Enabled:                 true,
		Source:                  source,
		Destination:             destination,
		SetID:                   "set-opaque",
		AllowUnverifiedManifest: true,
	})
	if !errors.Is(err, ErrManifestUnverified) {
		t.Fatalf("explicit unverified copy err = %v, want ErrManifestUnverified", err)
	}
	keys, err := destination.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("keyless migration wrote destination objects: %v", keys)
	}
}
