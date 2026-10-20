package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/beamfall/core/internal/capabilities"
)

const compatRefusalRecordVersion = 1

// compatRefusalFileJournal is the refusal-only write-ahead medium used when
// both SQLite invalidation writes fail. Each subject owns one atomically
// replaced file, so a synced rename is the commit point and a torn write can
// leave only an ignored temporary file, never a truncated live record.
type compatRefusalFileJournal struct {
	dir     string
	openErr error
}

func resolveCompatRefusalFileJournal(dir string) *compatRefusalFileJournal {
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return &compatRefusalFileJournal{openErr: fmt.Errorf("resolve refusal journal: %w", err)}
	}
	return newCompatRefusalFileJournal(resolved)
}

func newCompatRefusalFileJournal(dir string) *compatRefusalFileJournal {
	if !filepath.IsAbs(dir) || dir != filepath.Clean(dir) {
		return &compatRefusalFileJournal{openErr: errors.New("refusal journal path must resolve to an absolute clean path")}
	}
	return &compatRefusalFileJournal{dir: dir}
}

type compatRefusalRecord struct {
	Version    int                     `json:"version"`
	Subject    string                  `json:"subject"`
	Decision   capabilities.Decision   `json:"decision"`
	ReasonCode capabilities.ReasonCode `json:"reasonCode"`
	RecordedAt time.Time               `json:"recordedAt"`
	ExpiresAt  time.Time               `json:"expiresAt"`
}

func (j compatRefusalFileJournal) load(ctx context.Context, now time.Time) ([]capabilities.Invalidation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if j.openErr != nil {
		return nil, j.openErr
	}
	root, err := os.OpenRoot(j.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read refusal journal: %w", err)
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("read refusal journal: %w", err)
	}
	defer dir.Close()
	files, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read refusal journal: %w", err)
	}
	entries := make([]capabilities.Invalidation, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		entry, err := j.loadFile(root, file.Name())
		if err != nil {
			return nil, err
		}
		if compatRefusalFileName(entry.Subject) != file.Name() {
			return nil, fmt.Errorf("refusal journal subject does not match file %q", file.Name())
		}
		if !entry.Live(now) {
			if err := j.removeFile(file.Name()); err != nil {
				return nil, err
			}
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (j compatRefusalFileJournal) loadFile(root *os.Root, name string) (capabilities.Invalidation, error) {
	data, err := root.ReadFile(name)
	if err != nil {
		return capabilities.Invalidation{}, fmt.Errorf("read refusal record: %w", err)
	}
	var record compatRefusalRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return capabilities.Invalidation{}, fmt.Errorf("decode refusal record: %w", err)
	}
	entry := capabilities.Invalidation{
		Subject:    record.Subject,
		Decision:   record.Decision,
		ReasonCode: record.ReasonCode,
		RecordedAt: record.RecordedAt,
		ExpiresAt:  record.ExpiresAt,
	}
	if record.Version != compatRefusalRecordVersion || entry.Subject == "" || entry.ExpiresAt.IsZero() {
		return capabilities.Invalidation{}, errors.New("invalid refusal record")
	}
	return entry, nil
}

func (j compatRefusalFileJournal) record(ctx context.Context, entry capabilities.Invalidation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.openErr != nil {
		return j.openErr
	}
	if entry.Subject == "" || entry.ExpiresAt.IsZero() {
		return errors.New("invalid refusal entry")
	}
	data, err := json.Marshal(compatRefusalRecord{
		Version:    compatRefusalRecordVersion,
		Subject:    entry.Subject,
		Decision:   entry.Decision,
		ReasonCode: entry.ReasonCode,
		RecordedAt: entry.RecordedAt,
		ExpiresAt:  entry.ExpiresAt,
	})
	if err != nil {
		return err
	}
	if err := j.ensureDir(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(j.dir, ".refusal-*.tmp")
	if err != nil {
		return fmt.Errorf("create refusal record: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("protect refusal record: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write refusal record: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync refusal record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close refusal record: %w", err)
	}
	target := filepath.Join(j.dir, compatRefusalFileName(entry.Subject))
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("publish refusal record: %w", err)
	}
	if err := syncDirectory(j.dir); err != nil {
		return fmt.Errorf("sync refusal journal: %w", err)
	}
	return nil
}

func (j compatRefusalFileJournal) clear(ctx context.Context, subject string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.openErr != nil {
		return j.openErr
	}
	if subject == "" {
		return errors.New("refusal subject is required")
	}
	err := j.removeFile(compatRefusalFileName(subject))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (j compatRefusalFileJournal) removeFile(name string) error {
	if err := os.Remove(filepath.Join(j.dir, name)); err != nil {
		return err
	}
	if err := syncDirectory(j.dir); err != nil {
		return fmt.Errorf("sync refusal journal removal: %w", err)
	}
	return nil
}

func (j compatRefusalFileJournal) ensureDir() error {
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return fmt.Errorf("create refusal journal: %w", err)
	}
	if err := syncDirectory(filepath.Dir(j.dir)); err != nil {
		return fmt.Errorf("sync refusal journal parent: %w", err)
	}
	return nil
}

// The digest is a filesystem-safe stable key, not an attempt to conceal the
// subject: the clear path must derive the same name without trusting subject
// text as a pathname.
func compatRefusalFileName(subject string) string {
	digest := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(digest[:]) + ".json"
}

func syncDirectory(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
