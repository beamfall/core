package backup

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDefaultS3HTTPClientHasTimeout(t *testing.T) {
	if defaultS3HTTPClient.Timeout != defaultS3HTTPTimeout {
		t.Fatalf("timeout = %v, want %v", defaultS3HTTPClient.Timeout, defaultS3HTTPTimeout)
	}
}

// s3Stub is a minimal S3-compatible HTTP server for tests: it handles PUT, GET,
// DELETE, and a simple listing endpoint. All objects are held in memory.
// No authentication — tests pass RequestSigner=nil to S3Bucket.
type s3Stub struct {
	mu      sync.Mutex
	objects map[string][]byte // path (bucket/key) -> bytes
}

func newS3Stub() *s3Stub { return &s3Stub{objects: map[string][]byte{}} }

func (s *s3Stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := strings.TrimPrefix(r.URL.Path, "/")
	switch r.Method {
	case http.MethodPut:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.objects[path] = data
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if r.URL.Query().Get("list-type") == "2" {
			bucketPrefix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/"), "/")
			prefix := bucketPrefix + "/" + r.URL.Query().Get("prefix")
			var body bytes.Buffer
			body.WriteString(`<ListBucketResult>`)
			for k := range s.objects {
				if strings.HasPrefix(k, prefix) {
					body.WriteString(`<Contents><Key>`)
					xml.EscapeText(&body, []byte(strings.TrimPrefix(k, bucketPrefix+"/"))) //nolint:errcheck
					body.WriteString(`</Key></Contents>`)
				}
			}
			body.WriteString(`</ListBucketResult>`)
			w.WriteHeader(http.StatusOK)
			w.Write(body.Bytes()) //nolint:errcheck
			return
		}
		data, ok := s.objects[path]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	case http.MethodDelete:
		delete(s.objects, path)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// TestBackupRestoreRoundTripS3 exercises the full round-trip against an HTTP
// S3-compatible target (httptest stub). This is the [auto] acceptance criterion:
// "round-trip encrypt→replicate→restore against a local S3-compatible target
// with the locally-minted Recovery Key and no account/server alive at restore;
// ciphertext-only at the destination." (BACKUP-1 ticket)
//
// The stub is an HTTP server — the test verifies that the S3Bucket wire layer
// works and that restore requires only the BackupKey + ciphertext over HTTP.
// io:egress-backup-s3-compatible
func TestBackupRestoreRoundTripS3(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()

	bucket := &S3Bucket{
		Endpoint: ts.URL,
		Bucket:   "testbucket",
		Client:   ts.Client(),
	}
	svc := NewService(bucket)
	ctx := context.Background()

	// Mint the BackupKey locally — it never leaves the box.
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal("MintBackupKey:", err)
	}

	want := []byte("test payload for S3-compatible backup round-trip — some bytes")

	// Backup: encrypt + PUT to the HTTP S3 stub.
	if err := svc.Backup(ctx, key, "lib1/snap1", bytes.NewReader(want)); err != nil {
		t.Fatal("Backup:", err)
	}

	// Verify the stored bytes are ciphertext-only (no plaintext at the destination).
	stub.mu.Lock()
	stored := stub.objects["testbucket/backup/lib1/snap1"]
	stub.mu.Unlock()
	if bytes.Contains(stored, want) {
		t.Error("HTTP bucket contains plaintext — ciphertext-only invariant violated")
	}

	// Restore with no server alive beyond the S3 stub — only the BackupKey matters.
	var got bytes.Buffer
	if err := svc.Restore(ctx, key, "lib1/snap1", &got); err != nil {
		t.Fatal("Restore:", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("round-trip mismatch: got %q, want %q", got.Bytes(), want)
	}
}

// TestS3BucketDelete verifies the DELETE path cleans up the object.
func TestS3BucketDelete(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()

	bucket := &S3Bucket{
		Endpoint: ts.URL,
		Bucket:   "testbucket",
		Client:   ts.Client(),
	}
	ctx := context.Background()

	if err := bucket.Put(ctx, "backup/k1", strings.NewReader("somedata")); err != nil {
		t.Fatal("Put:", err)
	}
	if err := bucket.Delete(ctx, "backup/k1"); err != nil {
		t.Fatal("Delete:", err)
	}
	_, err := bucket.Get(ctx, "backup/k1")
	if err == nil {
		t.Error("expected error after Delete, got nil")
	}
}

func TestS3BucketEscapesObjectKeysForPutGetDelete(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()
	bucket := &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()}
	ctx := context.Background()
	key := "backup/lib 1/snap?one#frag"

	if err := bucket.Put(ctx, key, strings.NewReader("somedata")); err != nil {
		t.Fatal("Put:", err)
	}
	body, err := bucket.Get(ctx, key)
	if err != nil {
		t.Fatal("Get:", err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil {
		t.Fatal("ReadAll:", err)
	}
	if string(got) != "somedata" {
		t.Fatalf("body = %q, want somedata", got)
	}
	if err := bucket.Delete(ctx, key); err != nil {
		t.Fatal("Delete:", err)
	}
	if _, err := bucket.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete err = %v, want ErrNotFound", err)
	}
}

func TestS3BucketListEscapesPrefixAndParsesXML(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()
	bucket := &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()}
	ctx := context.Background()
	if err := bucket.Put(ctx, "backup/lib 1/snap?one", strings.NewReader("one")); err != nil {
		t.Fatal("Put one:", err)
	}
	if err := bucket.Put(ctx, "backup/lib 2/snap?two", strings.NewReader("two")); err != nil {
		t.Fatal("Put two:", err)
	}
	keys, err := bucket.List(ctx, "backup/lib 1/")
	if err != nil {
		t.Fatal("List:", err)
	}
	if len(keys) != 1 || keys[0] != "backup/lib 1/snap?one" {
		t.Fatalf("keys = %#v, want only lib 1 key", keys)
	}
}

func TestS3BucketListPaginates(t *testing.T) {
	t.Parallel()

	const nextToken = "page 2+/="
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("list-type"); got != "2" {
			t.Errorf("list-type = %q, want 2", got)
		}
		if got := r.URL.Query().Get("prefix"); got != "backup/lib 1/" {
			t.Errorf("prefix = %q, want backup/lib 1/", got)
		}

		switch token := r.URL.Query().Get("continuation-token"); token {
		case "":
			w.Write([]byte(`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>page 2+/=</NextContinuationToken><Contents><Key>z-last</Key></Contents><Contents><Key>a-first</Key></Contents></ListBucketResult>`)) //nolint:errcheck
		case nextToken:
			w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>m-middle</Key></Contents></ListBucketResult>`)) //nolint:errcheck
		default:
			t.Errorf("continuation-token = %q, want empty or %q", token, nextToken)
			http.Error(w, "unexpected continuation token", http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	bucket := &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()}
	keys, err := bucket.List(context.Background(), "backup/lib 1/")
	if err != nil {
		t.Fatal("List:", err)
	}
	want := []string{"z-last", "a-first", "m-middle"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %#v, want %#v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %#v, want %#v", keys, want)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestS3BucketListRejectsRepeatedContinuationToken(t *testing.T) {
	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests > 2 {
			http.Error(w, "pagination loop", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>repeat</NextContinuationToken></ListBucketResult>`))
	}))
	defer ts.Close()

	bucket := &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()}
	_, err := bucket.List(context.Background(), "backup/")
	if err == nil || !strings.Contains(err.Error(), "repeated continuation token") {
		t.Fatalf("List error = %v, want repeated continuation token", err)
	}
	if requests != 2 {
		t.Fatalf("LIST requests = %d, want 2", requests)
	}
}

func TestReplicationRoundTripS3CompatibleEncryptedObjects(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	bucket := &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()}
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  bucket,
		Key:     key,
		SetID:   "set-round-trip",
	})
	if err != nil {
		t.Fatal(err)
	}

	payloads := map[string]string{
		"catalog": "catalog plaintext bytes",
		"events":  "events plaintext bytes",
	}
	manifest, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte(payloads["catalog"]))},
		{Name: "events", Open: ReplicationBytesSource([]byte(payloads["events"]))},
	})
	if err != nil {
		t.Fatal("Replicate:", err)
	}
	if len(manifest.Objects) != 2 || manifest.Integrity.MAC == "" {
		t.Fatalf("manifest = %+v", manifest)
	}

	stub.mu.Lock()
	for objectKey, stored := range stub.objects {
		for _, plaintext := range payloads {
			if bytes.Contains(stored, []byte(plaintext)) {
				t.Fatalf("%s contains plaintext %q", objectKey, plaintext)
			}
		}
		if bytes.Contains(stored, key.Bytes()) {
			t.Fatalf("%s contains backup key bytes", objectKey)
		}
	}
	stub.mu.Unlock()

	restored := map[string]string{}
	if err := replicator.Restore(context.Background(), func(object ReplicationManifestObject, r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		restored[object.Name] = string(data)
		return nil
	}); err != nil {
		t.Fatal("Restore:", err)
	}
	if restored["catalog"] != payloads["catalog"] || restored["events"] != payloads["events"] || len(restored) != len(payloads) {
		t.Fatalf("restored = %#v, want %#v", restored, payloads)
	}
}

func TestReplicationBucketSeamHidesLibraryMetadata(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	const setID = "alice-private-library"
	names := []string{"alice-catalog", "family-photo-index"}
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()},
		Key:     key,
		SetID:   setID,
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: names[0], Open: ReplicationBytesSource([]byte("private catalog payload"))},
		{Name: names[1], Open: ReplicationBytesSource([]byte("private photo metadata"))},
	})
	if err != nil {
		t.Fatal("Replicate:", err)
	}

	metadata := append([]string{setID}, names...)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for objectKey, stored := range stub.objects {
		for _, plaintext := range metadata {
			if strings.Contains(objectKey, plaintext) {
				t.Errorf("bucket object key %q contains library metadata %q", objectKey, plaintext)
			}
			if bytes.Contains(stored, []byte(plaintext)) {
				t.Errorf("bucket object %q contains library metadata %q", objectKey, plaintext)
			}
		}
	}
	metadataKeys := []string{"testbucket/" + replicator.manifestKey()}
	for _, object := range manifest.Objects {
		metadataKeys = append(metadataKeys, "testbucket/"+object.ReceiptKey)
	}
	for _, objectKey := range metadataKeys {
		stored := stub.objects[objectKey]
		if len(stored) < bbk1HeaderFixed || !bytes.HasPrefix(stored, []byte(bbk1Magic)) {
			t.Errorf("metadata object %q is not a complete BBK1 envelope", objectKey)
			continue
		}
		if stored[4] != bbk1VersionV3 {
			t.Errorf("metadata object %q BBK version = %d, want context-bound v3", objectKey, stored[4])
			continue
		}
		payloadOffset := bbk1HeaderFixed + int(binary.BigEndian.Uint16(stored[5:7]))
		if payloadOffset+4 > len(stored) || string(stored[payloadOffset:payloadOffset+4]) != "BFE2" {
			t.Errorf("metadata object %q does not contain a context-bound BFE2 payload", objectKey)
		}
	}
}

func TestReplicationV2RejectsPlaintextManifest(t *testing.T) {
	t.Parallel()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	bucket := newMemBucket()
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  bucket,
		Key:     key,
		SetID:   "set-plaintext-manifest",
	})
	if err != nil {
		t.Fatal(err)
	}
	bucket.objects[replicator.manifestKey()] = []byte(`{"version":1,"setId":"set-plaintext-manifest","objects":[]}`)

	called := false
	err = replicator.Restore(context.Background(), func(ReplicationManifestObject, io.Reader) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrManifestIntegrity) {
		t.Fatalf("plaintext manifest err = %v, want ErrManifestIntegrity", err)
	}
	if called {
		t.Fatal("restore handler called for plaintext manifest")
	}
}

func TestReplicationTamperFailsIntegrity(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()},
		Key:     key,
		SetID:   "set-tamper",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog plaintext bytes"))},
	})
	if err != nil {
		t.Fatal(err)
	}

	stub.mu.Lock()
	rawKey := "testbucket/" + manifest.Objects[0].ObjectKey
	stub.objects[rawKey][len(stub.objects[rawKey])-1] ^= 0xff
	stub.mu.Unlock()

	err = replicator.Restore(context.Background(), func(ReplicationManifestObject, io.Reader) error { return nil })
	if !errors.Is(err, ErrManifestIntegrity) {
		t.Fatalf("Restore tamper err = %v, want ErrManifestIntegrity", err)
	}
}

func TestReplicationManifestTamperFailsIntegrity(t *testing.T) {
	t.Parallel()

	stub := newS3Stub()
	ts := httptest.NewServer(stub)
	defer ts.Close()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  &S3Bucket{Endpoint: ts.URL, Bucket: "testbucket", Client: ts.Client()},
		Key:     key,
		SetID:   "set-manifest-tamper",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog plaintext bytes"))},
	}); err != nil {
		t.Fatal(err)
	}

	stub.mu.Lock()
	manifestKey := "testbucket/" + replicator.manifestKey()
	stub.objects[manifestKey][len(stub.objects[manifestKey])-1] ^= 0xff
	stub.mu.Unlock()

	err = replicator.Restore(context.Background(), func(ReplicationManifestObject, io.Reader) error { return nil })
	if !errors.Is(err, ErrManifestIntegrity) {
		t.Fatalf("Restore manifest tamper err = %v, want ErrManifestIntegrity", err)
	}
}

func TestReplicationFailsClosedWhenDisabledOrMisconfigured(t *testing.T) {
	t.Parallel()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewReplicator(ReplicationOptions{Bucket: newMemBucket(), Key: key, SetID: "set"}); !errors.Is(err, ErrReplicationDisabled) {
		t.Fatalf("disabled err = %v, want ErrReplicationDisabled", err)
	}
	if _, err := NewReplicator(ReplicationOptions{Enabled: true, Key: key, SetID: "set"}); !errors.Is(err, ErrReplicationMisconfigured) {
		t.Fatalf("missing bucket err = %v, want ErrReplicationMisconfigured", err)
	}
	if _, err := NewReplicator(ReplicationOptions{Enabled: true, Bucket: newMemBucket(), Key: key}); !errors.Is(err, ErrReplicationMisconfigured) {
		t.Fatalf("missing set err = %v, want ErrReplicationMisconfigured", err)
	}
}

func TestReplicationValidatesAllObjectsBeforeWriting(t *testing.T) {
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]ReplicationObject{
		"duplicate name": {
			{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog"))},
			{Name: "catalog", Open: ReplicationBytesSource([]byte("duplicate"))},
		},
		"missing source": {
			{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog"))},
			{Name: "events"}, // no Open: source is required
		},
	}
	for name, objects := range tests {
		t.Run(name, func(t *testing.T) {
			bucket := newMemBucket()
			replicator, err := NewReplicator(ReplicationOptions{
				Enabled: true,
				Bucket:  bucket,
				Key:     key,
				SetID:   "set-validation",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replicator.Replicate(context.Background(), objects); err == nil {
				t.Fatal("Replicate succeeded, want validation error")
			}
			keys, err := bucket.List(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			if len(keys) != 0 {
				t.Fatalf("validation failure wrote bucket objects: %v", keys)
			}
		})
	}
}

func TestReplicationRetriesTransientUploadFailure(t *testing.T) {
	t.Parallel()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	bucket := &flakyBucket{BucketClient: newMemBucket(), failPuts: map[string]int{}}
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled:     true,
		Bucket:      bucket,
		Key:         key,
		SetID:       "set-retry",
		MaxAttempts: 2,
		Backoff:     time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog plaintext bytes"))},
	}); err != nil {
		t.Fatal(err)
	}
	if bucket.failed == 0 {
		t.Fatal("transient failure path was not exercised")
	}
}

func TestReplicationResumesCompletedObjectFromReceipt(t *testing.T) {
	t.Parallel()

	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	bucket := newMemBucket()
	replicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  bucket,
		Key:     key,
		SetID:   "set-resume",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog plaintext bytes"))},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := replicator.Replicate(context.Background(), []ReplicationObject{
		{Name: "catalog", Open: func() (io.ReadCloser, error) {
			return nil, errors.New("resume must not reopen the source")
		}},
	}); err != nil {
		t.Fatalf("resume replicate read source again: %v", err)
	}
}

func TestReplicationMigrationCopiesCiphertextWithoutReencryption(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newMemBucket()
	destination := newMemBucket()
	sourceReplicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  source,
		Key:     key,
		SetID:   "set-migrate",
	})
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string]string{
		"catalog": "catalog bytes that must not be visible while moving buckets",
		"events":  "events bytes that must not be visible while moving buckets",
	}
	manifest, err := sourceReplicator.Replicate(ctx, []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte(payloads["catalog"]))},
		{Name: "events", Open: ReplicationBytesSource([]byte(payloads["events"]))},
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceCiphertexts := map[string][]byte{}
	for _, object := range manifest.Objects {
		sourceCiphertexts[object.ObjectKey] = append([]byte(nil), source.objects[object.ObjectKey]...)
	}

	copied, err := CopyReplicationSet(ctx, ReplicationMigrationOptions{
		Enabled:     true,
		Source:      source,
		Destination: destination,
		SetID:       "set-migrate",
		Key:         key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(copied.Objects) != len(manifest.Objects) || copied.Integrity.MAC == "" {
		t.Fatalf("copied manifest = %+v, want %d objects and MAC", copied, len(manifest.Objects))
	}
	for _, object := range copied.Objects {
		got := destination.objects[object.ObjectKey]
		if !bytes.Equal(got, sourceCiphertexts[object.ObjectKey]) {
			t.Fatalf("%s destination ciphertext changed during migration", object.ObjectKey)
		}
		for _, plaintext := range payloads {
			if bytes.Contains(got, []byte(plaintext)) {
				t.Fatalf("%s contains plaintext %q", object.ObjectKey, plaintext)
			}
		}
	}
	if !bytes.Equal(destination.objects[sourceReplicator.manifestKey()], source.objects[sourceReplicator.manifestKey()]) {
		t.Fatal("destination manifest changed during migration")
	}

	restored := map[string]string{}
	if err := RestoreReplication(ctx, ReplicationOptions{
		Enabled: true,
		Bucket:  destination,
		Key:     key,
		SetID:   "set-migrate",
	}, func(object ReplicationManifestObject, r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		restored[object.Name] = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if restored["catalog"] != payloads["catalog"] || restored["events"] != payloads["events"] || len(restored) != len(payloads) {
		t.Fatalf("restored = %#v, want %#v", restored, payloads)
	}
}

func TestReplicationMigrationResumesMatchingDestinationObject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newMemBucket()
	sourceReplicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  source,
		Key:     key,
		SetID:   "set-migrate-resume",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := sourceReplicator.Replicate(ctx, []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog bytes"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	object := manifest.Objects[0]
	destination := newMemBucket()
	destination.objects[object.ObjectKey] = append([]byte(nil), source.objects[object.ObjectKey]...)
	guarded := &failPutKeyBucket{BucketClient: destination, key: object.ObjectKey}

	if _, err := CopyReplicationSet(ctx, ReplicationMigrationOptions{
		Enabled:     true,
		Source:      source,
		Destination: guarded,
		SetID:       "set-migrate-resume",
		Key:         key,
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.objects[object.ReceiptKey], source.objects[object.ReceiptKey]) {
		t.Fatal("destination receipt changed during migration")
	}
	destinationReplicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  destination,
		Key:     key,
		SetID:   "set-migrate-resume",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destinationReplicator.Replicate(ctx, []ReplicationObject{
		{Name: "catalog", Open: func() (io.ReadCloser, error) {
			return nil, errors.New("migrated receipt must resume without reopening the payload")
		}},
	}); err != nil {
		t.Fatalf("resume from migrated receipt: %v", err)
	}
}

func TestReplicationMigrationRejectsTamperedSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newMemBucket()
	destination := newMemBucket()
	sourceReplicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  source,
		Key:     key,
		SetID:   "set-migrate-tamper",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := sourceReplicator.Replicate(ctx, []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog bytes"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	object := manifest.Objects[0]
	source.objects[object.ObjectKey][len(source.objects[object.ObjectKey])-1] ^= 0xff

	_, err = CopyReplicationSet(ctx, ReplicationMigrationOptions{
		Enabled:     true,
		Source:      source,
		Destination: destination,
		SetID:       "set-migrate-tamper",
		Key:         key,
	})
	if !errors.Is(err, ErrManifestIntegrity) {
		t.Fatalf("CopyReplicationSet tamper err = %v, want ErrManifestIntegrity", err)
	}
}

func TestReplicationMigrationRejectsPlaintextReceiptBeforePublishingManifest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newMemBucket()
	destination := newMemBucket()
	sourceReplicator, err := NewReplicator(ReplicationOptions{
		Enabled: true,
		Bucket:  source,
		Key:     key,
		SetID:   "set-migrate-receipt-tamper",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := sourceReplicator.Replicate(ctx, []ReplicationObject{
		{Name: "catalog", Open: ReplicationBytesSource([]byte("catalog bytes"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	object := manifest.Objects[0]
	var plaintext bytes.Buffer
	if err := NewService(source).Restore(ctx, key, sourceReplicator.receiptBackupID(object.Name), &plaintext); err != nil {
		t.Fatal(err)
	}
	source.objects[object.ReceiptKey] = append([]byte(nil), plaintext.Bytes()...)

	_, err = CopyReplicationSet(ctx, ReplicationMigrationOptions{
		Enabled:     true,
		Source:      source,
		Destination: destination,
		SetID:       "set-migrate-receipt-tamper",
		Key:         key,
	})
	if !errors.Is(err, ErrManifestIntegrity) {
		t.Fatalf("CopyReplicationSet receipt tamper err = %v, want ErrManifestIntegrity", err)
	}
	if _, ok := destination.objects[sourceReplicator.manifestKey()]; ok {
		t.Fatal("destination manifest published after receipt authentication failed")
	}
	if _, ok := destination.objects[object.ReceiptKey]; ok {
		t.Fatal("destination receipt written before source receipt authentication")
	}
}

type flakyBucket struct {
	BucketClient
	mu       sync.Mutex
	failPuts map[string]int
	failed   int
}

func (f *flakyBucket) Put(ctx context.Context, key string, r io.Reader) error {
	f.mu.Lock()
	f.failPuts[key]++
	shouldFail := f.failPuts[key] == 1
	if shouldFail {
		f.failed++
	}
	f.mu.Unlock()
	if shouldFail {
		return errors.New("transient put failure")
	}
	return f.BucketClient.Put(ctx, key, r)
}

type failPutKeyBucket struct {
	BucketClient
	key string
}

func (f *failPutKeyBucket) Put(ctx context.Context, key string, r io.Reader) error {
	if key == f.key {
		return errors.New("object should not be recopied")
	}
	return f.BucketClient.Put(ctx, key, r)
}
