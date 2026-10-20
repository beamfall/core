package backup

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const (
	defaultReplicationAttempts = 3
	defaultReplicationBackoff  = 10 * time.Millisecond
	// v2 is a pre-release format cutover: metadata is encrypted and every
	// locator is keyed. Plaintext v1 manifests are deliberately never read.
	replicationManifestVersion = 2
)

var (
	ErrReplicationDisabled      = errors.New("backup: BYO replication is disabled")
	ErrReplicationMisconfigured = errors.New("backup: BYO replication is misconfigured")
	ErrManifestIntegrity        = errors.New("backup: replication manifest integrity check failed")
	// ErrManifestUnverified fails a migration closed when no BackupKey was
	// supplied. Replication metadata and locators are encrypted/keyed in v2, so
	// a keyless copier cannot locate or inspect a set safely.
	ErrManifestUnverified = errors.New("backup: encrypted replication metadata requires the BackupKey")
)

// ReplicationObject names one object of a replication set and how to open its
// content.
//
// Open returns a fresh reader over the object's FULL content, from byte 0.
// Replicate may call it more than once: every upload attempt (including each
// retry after a transient bucket failure) opens its own reader. Requiring a
// re-openable source — a file, a bucket object, an in-memory payload — is what
// lets replication stream end-to-end in O(chunk) memory instead of buffering
// the whole object in RAM to survive a retry (COREREV-18: a multi-GB object
// must not OOM a constrained server).
type ReplicationObject struct {
	Name string
	Open func() (io.ReadCloser, error)
}

type ReplicationOptions struct {
	Enabled     bool
	Bucket      BucketClient
	Key         BackupKey
	SetID       string
	MaxAttempts int
	Backoff     time.Duration
}

// ReplicationMigrationOptions configures a bucket-to-bucket copy of a sealed
// replication set.
//
// Trust model (COREREV-19, documented beside the manifest format below): the
// manifest's per-object SHA256s are self-referential — an attacker with
// source-bucket write access can rewrite ObjectKey/SHA256 rows alongside
// matching tampered ciphertext, and a keyless copier would faithfully
// propagate them. The manifest MAC is the only signal that binds the object
// list to the set owner, and only the BackupKey can check it. So:
//
// The migrator decrypts and HMAC-verifies the manifest before copying. Payloads
// stay opaque BBK1 ciphertext and are never unwrapped or re-encrypted. A
// BackupKey is mandatory because v2 metadata is encrypted and every bucket
// locator is keyed; there is no plaintext object list or set identifier to copy.
type ReplicationMigrationOptions struct {
	Enabled     bool
	Source      BucketClient
	Destination BucketClient
	SetID       string
	// Key holds the set's BackupKey so metadata can be decrypted and verified
	// before the copy. It never decrypts payloads.
	Key BackupKey
	// AllowUnverifiedManifest is retained for source compatibility. Encrypted
	// v2 metadata cannot be copied keylessly, so setting it does not bypass
	// ErrManifestUnverified.
	AllowUnverifiedManifest bool
	MaxAttempts             int
	Backoff                 time.Duration
}

type ReplicationManifest struct {
	Version   int                          `json:"version"`
	SetID     string                       `json:"setId"`
	Objects   []ReplicationManifestObject  `json:"objects"`
	Integrity ReplicationManifestIntegrity `json:"integrity"`
}

type ReplicationManifestObject struct {
	Name       string `json:"name"`
	BackupID   string `json:"backupId"`
	ObjectKey  string `json:"objectKey"`
	ReceiptKey string `json:"receiptKey"`
	SizeBytes  int64  `json:"sizeBytes"`
	SHA256     string `json:"sha256"`
}

type ReplicationManifestIntegrity struct {
	Algorithm string `json:"algorithm"`
	MAC       string `json:"mac"`
}

type replicationReceipt struct {
	Object    ReplicationManifestObject    `json:"object"`
	Integrity ReplicationManifestIntegrity `json:"integrity"`
}

type Replicator struct {
	opts ReplicationOptions
}

type ReplicationMigrator struct {
	opts ReplicationMigrationOptions
}

func NewReplicator(opts ReplicationOptions) (*Replicator, error) {
	if !opts.Enabled {
		return nil, ErrReplicationDisabled
	}
	if opts.Bucket == nil || strings.TrimSpace(opts.SetID) == "" || len(opts.Key.Bytes()) != 32 {
		return nil, ErrReplicationMisconfigured
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultReplicationAttempts
	}
	if opts.Backoff <= 0 {
		opts.Backoff = defaultReplicationBackoff
	}
	return &Replicator{opts: opts}, nil
}

func NewReplicationMigrator(opts ReplicationMigrationOptions) (*ReplicationMigrator, error) {
	if !opts.Enabled {
		return nil, ErrReplicationDisabled
	}
	if opts.Source == nil || opts.Destination == nil || strings.TrimSpace(opts.SetID) == "" {
		return nil, ErrReplicationMisconfigured
	}
	if keyLen := len(opts.Key.Bytes()); keyLen != 0 && keyLen != 32 {
		return nil, ErrReplicationMisconfigured
	}
	// Encrypted metadata and keyed locators make a keyless v2 copy impossible:
	// there is deliberately no plaintext object list or SetID at the bucket seam.
	if len(opts.Key.Bytes()) != 32 {
		return nil, ErrManifestUnverified
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultReplicationAttempts
	}
	if opts.Backoff <= 0 {
		opts.Backoff = defaultReplicationBackoff
	}
	return &ReplicationMigrator{opts: opts}, nil
}

func (r *Replicator) Replicate(ctx context.Context, objects []ReplicationObject) (ReplicationManifest, error) {
	if len(objects) == 0 {
		return ReplicationManifest{}, errors.New("backup: at least one replication object is required")
	}
	manifest := ReplicationManifest{
		Version: replicationManifestVersion,
		SetID:   r.opts.SetID,
		Objects: make([]ReplicationManifestObject, 0, len(objects)),
	}
	seen := map[string]bool{}
	for _, object := range objects {
		if strings.TrimSpace(object.Name) == "" {
			return ReplicationManifest{}, errors.New("backup: replication object name is required")
		}
		if object.Open == nil {
			return ReplicationManifest{}, errors.New("backup: replication object source is required")
		}
		if seen[object.Name] {
			return ReplicationManifest{}, fmt.Errorf("backup: duplicate replication object %q", object.Name)
		}
		seen[object.Name] = true
	}
	for _, object := range objects {
		entry, err := r.replicateObject(ctx, object)
		if err != nil {
			return ReplicationManifest{}, err
		}
		manifest.Objects = append(manifest.Objects, entry)
	}
	sort.Slice(manifest.Objects, func(i, j int) bool {
		return manifest.Objects[i].Name < manifest.Objects[j].Name
	})
	if err := r.sealManifest(ctx, &manifest); err != nil {
		return ReplicationManifest{}, err
	}
	return manifest, nil
}

// Restore verifies each manifest object's ciphertext against the sealed
// manifest, then streams its decrypted plaintext to handle. The plaintext is
// threaded end-to-end as a stream: peak memory stays O(chunk),
// never a whole-object buffer, so a multi-GB object restores on a constrained
// server.
//
// Retry contract: the bucket is a re-openable source, so a failed attempt
// restarts the WHOLE object — handle may be invoked again with a fresh stream
// from byte 0 and must treat any prior partial delivery as void (re-create or
// truncate its output). handle need not drain the stream: returning nil early
// discards the remainder after the decryptor's integrity verdict.
func (r *Replicator) Restore(ctx context.Context, handle func(ReplicationManifestObject, io.Reader) error) error {
	if handle == nil {
		return errors.New("backup: restore handler is required")
	}
	manifest, err := r.loadManifest(ctx)
	if err != nil {
		return err
	}
	svc := NewService(r.opts.Bucket)
	for _, object := range manifest.Objects {
		if err := r.verifyCiphertext(ctx, object); err != nil {
			return err
		}
		object := object
		if err := r.retry(ctx, func() error {
			return r.restoreObjectStream(ctx, svc, object, handle)
		}); err != nil {
			return err
		}
	}
	return nil
}

// restoreObjectStream decrypts one object through a pipe into handle — the
// streaming replacement for the historical whole-plaintext bytes.Buffer.
func (r *Replicator) restoreObjectStream(ctx context.Context, svc *Service, object ReplicationManifestObject, handle func(ReplicationManifestObject, io.Reader) error) error {
	pr, pw := io.Pipe()
	restoreDone := make(chan error, 1)
	go func() {
		err := svc.Restore(ctx, r.opts.Key, object.BackupID, pw)
		pw.CloseWithError(err)
		restoreDone <- err
	}()
	handleErr := handle(object, pr)
	if handleErr == nil {
		// The handler may return before draining; consume the remainder so
		// the decryptor processes the whole object and its verdict below is
		// authoritative — the same coverage the buffered implementation had
		// by decrypting everything before the handler ran.
		_, _ = io.Copy(io.Discard, pr)
	}
	pr.CloseWithError(handleErr)
	restoreErr := <-restoreDone
	if handleErr != nil {
		return handleErr
	}
	return restoreErr
}

// Copy moves a completed replication set between buckets using only the sealed
// manifest and opaque BBK1 ciphertext objects. It never calls Restore or Backup,
// so it never unwraps keys, decrypts payloads, or re-encrypts destination bytes.
func (m *ReplicationMigrator) Copy(ctx context.Context) (ReplicationManifest, error) {
	manifest, rawManifest, err := m.loadManifest(ctx)
	if err != nil {
		return ReplicationManifest{}, err
	}
	for _, object := range manifest.Objects {
		if err := m.copyObject(ctx, object); err != nil {
			return ReplicationManifest{}, err
		}
	}
	if err := m.retry(ctx, func() error {
		return m.opts.Destination.Put(ctx, m.manifestKey(), bytes.NewReader(rawManifest))
	}); err != nil {
		return ReplicationManifest{}, err
	}
	return manifest, nil
}

func (m *ReplicationMigrator) loadManifest(ctx context.Context) (ReplicationManifest, []byte, error) {
	var data []byte
	if err := m.retry(ctx, func() error {
		rc, err := m.opts.Source.Get(ctx, m.manifestKey())
		if err != nil {
			return err
		}
		defer rc.Close()
		data, err = io.ReadAll(rc)
		return err
	}); err != nil {
		return ReplicationManifest{}, nil, err
	}
	var plaintext bytes.Buffer
	if _, err := InspectEnvelopeWithID(m.opts.Key, m.manifestBackupID(), bytes.NewReader(data), &plaintext); err != nil {
		return ReplicationManifest{}, nil, ErrManifestIntegrity
	}
	var manifest ReplicationManifest
	if err := json.Unmarshal(plaintext.Bytes(), &manifest); err != nil {
		return ReplicationManifest{}, nil, err
	}
	if manifest.Version != replicationManifestVersion || manifest.SetID != m.opts.SetID ||
		manifest.Integrity.Algorithm != "hmac-sha256" || strings.TrimSpace(manifest.Integrity.MAC) == "" {
		return ReplicationManifest{}, nil, ErrManifestIntegrity
	}
	got := manifest.Integrity
	manifest.Integrity = ReplicationManifestIntegrity{}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return ReplicationManifest{}, nil, err
	}
	if !hmac.Equal([]byte(got.MAC), []byte(replicationMAC(m.opts.Key, "manifest", canonical))) {
		return ReplicationManifest{}, nil, ErrManifestIntegrity
	}
	manifest.Integrity = got
	return manifest, data, nil
}

func (m *ReplicationMigrator) copyObject(ctx context.Context, object ReplicationManifestObject) error {
	if err := m.verifyCiphertext(ctx, m.opts.Destination, object); err == nil {
		return m.copyReceipt(ctx, object)
	}
	if err := m.verifyCiphertext(ctx, m.opts.Source, object); err != nil {
		return err
	}
	if err := m.retry(ctx, func() error {
		rc, err := m.opts.Source.Get(ctx, object.ObjectKey)
		if err != nil {
			return err
		}
		defer rc.Close()
		return m.opts.Destination.Put(ctx, object.ObjectKey, rc)
	}); err != nil {
		return err
	}
	if err := m.verifyCiphertext(ctx, m.opts.Destination, object); err != nil {
		return err
	}
	return m.copyReceipt(ctx, object)
}

func (m *ReplicationMigrator) copyReceipt(ctx context.Context, object ReplicationManifestObject) error {
	backupID := replicationBackupID(m.opts.Key, "receipt", m.opts.SetID, object.Name)
	if object.ReceiptKey != bucketKey(backupID) {
		return ErrManifestIntegrity
	}
	return m.retry(ctx, func() error {
		rc, err := m.opts.Source.Get(ctx, object.ReceiptKey)
		if errors.Is(err, ErrNotFound) {
			// No receipt on the source set — nothing to copy.
			return nil
		}
		if err != nil {
			return err
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return err
		}
		receipt, err := openReplicationReceipt(m.opts.Key, data, backupID)
		if err != nil {
			return err
		}
		if receipt.Object != object {
			return ErrManifestIntegrity
		}
		return m.opts.Destination.Put(ctx, object.ReceiptKey, bytes.NewReader(data))
	})
}

func (m *ReplicationMigrator) verifyCiphertext(ctx context.Context, bucket BucketClient, object ReplicationManifestObject) error {
	var size int64
	hash := sha256.New()
	if err := m.retry(ctx, func() error {
		size = 0
		hash.Reset()
		rc, err := bucket.Get(ctx, object.ObjectKey)
		if err != nil {
			return err
		}
		defer rc.Close()
		n, err := io.Copy(hash, rc)
		size = n
		return err
	}); err != nil {
		return err
	}
	if size != object.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != object.SHA256 {
		return ErrManifestIntegrity
	}
	return nil
}

func (m *ReplicationMigrator) retry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < m.opts.MaxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if attempt == m.opts.MaxAttempts-1 {
			break
		}
		timer := time.NewTimer(m.opts.Backoff << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func (m *ReplicationMigrator) manifestKey() string {
	return bucketKey(m.manifestBackupID())
}

func (m *ReplicationMigrator) manifestBackupID() string {
	return replicationBackupID(m.opts.Key, "manifest", m.opts.SetID, "")
}

func (r *Replicator) replicateObject(ctx context.Context, object ReplicationObject) (ReplicationManifestObject, error) {
	if strings.TrimSpace(object.Name) == "" {
		return ReplicationManifestObject{}, errors.New("backup: replication object name is required")
	}
	if object.Open == nil {
		return ReplicationManifestObject{}, errors.New("backup: replication object source is required")
	}
	entry := ReplicationManifestObject{
		Name:       object.Name,
		BackupID:   r.objectBackupID(object.Name),
		ObjectKey:  bucketKey(r.objectBackupID(object.Name)),
		ReceiptKey: bucketKey(r.receiptBackupID(object.Name)),
	}
	if receipt, ok := r.resumeReceipt(ctx, entry); ok {
		return receipt, nil
	}
	// Stream the plaintext straight through encrypt-and-upload:
	// each attempt re-opens the source instead of replaying a whole-object RAM
	// buffer, and the ciphertext digest is teed inside hashingBucket, so peak
	// memory is O(chunk) regardless of object size.
	hasher := &hashingBucket{bucket: r.opts.Bucket}
	svc := NewService(hasher)
	if err := r.retry(ctx, func() error {
		hasher.reset()
		src, err := object.Open()
		if err != nil {
			return err
		}
		defer src.Close()
		return svc.Backup(ctx, r.opts.Key, entry.BackupID, src)
	}); err != nil {
		return ReplicationManifestObject{}, err
	}
	entry.SizeBytes = hasher.size
	entry.SHA256 = hasher.sum()
	if err := r.writeReceipt(ctx, entry); err != nil {
		return ReplicationManifestObject{}, err
	}
	return entry, nil
}

func (r *Replicator) resumeReceipt(ctx context.Context, entry ReplicationManifestObject) (ReplicationManifestObject, bool) {
	rc, err := r.opts.Bucket.Get(ctx, entry.ReceiptKey)
	if err != nil {
		return ReplicationManifestObject{}, false
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return ReplicationManifestObject{}, false
	}
	var receipt ReplicationManifestObject
	envelope, err := r.openReceipt(data, r.receiptBackupID(entry.Name))
	if err != nil {
		return ReplicationManifestObject{}, false
	}
	receipt = envelope.Object
	if receipt.Name != entry.Name || receipt.BackupID != entry.BackupID || receipt.ObjectKey != entry.ObjectKey || receipt.ReceiptKey != entry.ReceiptKey {
		return ReplicationManifestObject{}, false
	}
	if err := r.verifyCiphertext(ctx, receipt); err != nil {
		return ReplicationManifestObject{}, false
	}
	return receipt, true
}

func (r *Replicator) writeReceipt(ctx context.Context, entry ReplicationManifestObject) error {
	envelope := replicationReceipt{Object: entry}
	objectData, err := json.Marshal(envelope.Object)
	if err != nil {
		return err
	}
	envelope.Integrity = ReplicationManifestIntegrity{
		Algorithm: "hmac-sha256",
		MAC:       r.mac("receipt", objectData),
	}
	plaintext, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	svc := NewService(r.opts.Bucket)
	backupID := r.receiptBackupID(entry.Name)
	return r.retry(ctx, func() error {
		return svc.BackupObject(ctx, r.opts.Key, backupID, bytes.NewReader(plaintext))
	})
}

func (r *Replicator) sealManifest(ctx context.Context, manifest *ReplicationManifest) error {
	manifest.Integrity = ReplicationManifestIntegrity{}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	manifest.Integrity = ReplicationManifestIntegrity{
		Algorithm: "hmac-sha256",
		MAC:       r.mac("manifest", data),
	}
	plaintext, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	svc := NewService(r.opts.Bucket)
	return r.retry(ctx, func() error {
		return svc.BackupObject(ctx, r.opts.Key, r.manifestBackupID(), bytes.NewReader(plaintext))
	})
}

func (r *Replicator) loadManifest(ctx context.Context) (ReplicationManifest, error) {
	var data []byte
	if err := r.retry(ctx, func() error {
		rc, err := r.opts.Bucket.Get(ctx, r.manifestKey())
		if err != nil {
			return err
		}
		defer rc.Close()
		data, err = io.ReadAll(rc)
		return err
	}); err != nil {
		return ReplicationManifest{}, err
	}
	var plaintext bytes.Buffer
	if _, err := InspectEnvelopeWithID(r.opts.Key, r.manifestBackupID(), bytes.NewReader(data), &plaintext); err != nil {
		return ReplicationManifest{}, ErrManifestIntegrity
	}
	var manifest ReplicationManifest
	if err := json.Unmarshal(plaintext.Bytes(), &manifest); err != nil {
		return ReplicationManifest{}, err
	}
	if manifest.Version != replicationManifestVersion || manifest.SetID != r.opts.SetID {
		return ReplicationManifest{}, ErrManifestIntegrity
	}
	got := manifest.Integrity
	manifest.Integrity = ReplicationManifestIntegrity{}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return ReplicationManifest{}, err
	}
	if got.Algorithm != "hmac-sha256" || !hmac.Equal([]byte(got.MAC), []byte(r.mac("manifest", canonical))) {
		return ReplicationManifest{}, ErrManifestIntegrity
	}
	manifest.Integrity = got
	return manifest, nil
}

func (r *Replicator) verifyCiphertext(ctx context.Context, object ReplicationManifestObject) error {
	var size int64
	hash := sha256.New()
	if err := r.retry(ctx, func() error {
		size = 0
		hash.Reset()
		rc, err := r.opts.Bucket.Get(ctx, object.ObjectKey)
		if err != nil {
			return err
		}
		defer rc.Close()
		n, err := io.Copy(hash, rc)
		size = n
		return err
	}); err != nil {
		return err
	}
	if size != object.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != object.SHA256 {
		return ErrManifestIntegrity
	}
	return nil
}

func (r *Replicator) retry(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < r.opts.MaxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if attempt == r.opts.MaxAttempts-1 {
			break
		}
		timer := time.NewTimer(r.opts.Backoff << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func (r *Replicator) objectBackupID(name string) string {
	return replicationBackupID(r.opts.Key, "object", r.opts.SetID, name)
}

func (r *Replicator) receiptBackupID(name string) string {
	return replicationBackupID(r.opts.Key, "receipt", r.opts.SetID, name)
}

func (r *Replicator) manifestBackupID() string {
	return replicationBackupID(r.opts.Key, "manifest", r.opts.SetID, "")
}

func (r *Replicator) manifestKey() string {
	return bucketKey(r.manifestBackupID())
}

func (r *Replicator) openReceipt(data []byte, backupID string) (replicationReceipt, error) {
	return openReplicationReceipt(r.opts.Key, data, backupID)
}

func openReplicationReceipt(key BackupKey, data []byte, backupID string) (replicationReceipt, error) {
	var plaintext bytes.Buffer
	if _, err := InspectEnvelopeWithID(key, backupID, bytes.NewReader(data), &plaintext); err != nil {
		return replicationReceipt{}, ErrManifestIntegrity
	}
	var envelope replicationReceipt
	if err := json.Unmarshal(plaintext.Bytes(), &envelope); err != nil {
		return replicationReceipt{}, err
	}
	got := envelope.Integrity
	envelope.Integrity = ReplicationManifestIntegrity{}
	objectData, err := json.Marshal(envelope.Object)
	if err != nil {
		return replicationReceipt{}, err
	}
	if got.Algorithm != "hmac-sha256" || !hmac.Equal([]byte(got.MAC), []byte(replicationMAC(key, "receipt", objectData))) {
		return replicationReceipt{}, ErrManifestIntegrity
	}
	envelope.Integrity = got
	return envelope, nil
}

func (r *Replicator) mac(label string, data []byte) string {
	return replicationMAC(r.opts.Key, label, data)
}

// replicationMAC computes the replication-set HMAC used for manifest and
// receipt integrity. Shared by the keyed Replicator and the keyed migrator
// verification path so the two can never drift.
func replicationMAC(key BackupKey, label string, data []byte) string {
	mac := hmac.New(sha256.New, key.Bytes())
	mac.Write([]byte("beamfall backup replication v2 " + label))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func replicationBackupID(key BackupKey, kind, setID, objectName string) string {
	mac := hmac.New(sha256.New, key.Bytes())
	mac.Write([]byte("beamfall backup replication locator v2"))
	writeReplicationLocatorField(mac, kind)
	writeReplicationLocatorField(mac, setID)
	writeReplicationLocatorField(mac, objectName)
	return "replication/v2/" + hex.EncodeToString(mac.Sum(nil))
}

func writeReplicationLocatorField(dst io.Writer, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = dst.Write(size[:])
	_, _ = io.WriteString(dst, value)
}

type hashingBucket struct {
	bucket BucketClient
	size   int64
	hash   [32]byte
}

func (h *hashingBucket) Put(ctx context.Context, key string, r io.Reader) error {
	hash := sha256.New()
	counting := &countingReader{reader: io.TeeReader(r, hash)}
	if err := h.bucket.Put(ctx, key, counting); err != nil {
		return err
	}
	h.size = counting.n
	copy(h.hash[:], hash.Sum(nil))
	return nil
}

func (h *hashingBucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return h.bucket.Get(ctx, key)
}

func (h *hashingBucket) Delete(ctx context.Context, key string) error {
	return h.bucket.Delete(ctx, key)
}

func (h *hashingBucket) List(ctx context.Context, prefix string) ([]string, error) {
	return h.bucket.List(ctx, prefix)
}

func (h *hashingBucket) reset() {
	h.size = 0
	h.hash = [32]byte{}
}

func (h *hashingBucket) sum() string {
	return hex.EncodeToString(h.hash[:])
}

type countingReader struct {
	reader io.Reader
	n      int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.n += int64(n)
	return n, err
}

func Replicate(ctx context.Context, opts ReplicationOptions, objects []ReplicationObject) (ReplicationManifest, error) {
	replicator, err := NewReplicator(opts)
	if err != nil {
		return ReplicationManifest{}, err
	}
	return replicator.Replicate(ctx, objects)
}

func RestoreReplication(ctx context.Context, opts ReplicationOptions, handle func(ReplicationManifestObject, io.Reader) error) error {
	replicator, err := NewReplicator(opts)
	if err != nil {
		return err
	}
	return replicator.Restore(ctx, handle)
}

func CopyReplicationSet(ctx context.Context, opts ReplicationMigrationOptions) (ReplicationManifest, error) {
	migrator, err := NewReplicationMigrator(opts)
	if err != nil {
		return ReplicationManifest{}, err
	}
	return migrator.Copy(ctx)
}
