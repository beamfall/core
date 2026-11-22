package offlinepack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AttestationStatusURL is Google's Android Key Attestation certificate
// status list: every attestation certificate Google has revoked or
// suspended, keyed by serial number.
const AttestationStatusURL = "https://android.googleapis.com/attestation/status"

const (
	// attestationStatusRefreshAfter is how old a fetched list may get
	// before a verification tries to fetch a fresh one.
	attestationStatusRefreshAfter = 24 * time.Hour
	// attestationStatusMaxAge is how old a list may get before verification
	// refuses: an offline server keeps verifying for this long, then fails
	// closed rather than trust certificates it cannot check.
	attestationStatusMaxAge = 30 * 24 * time.Hour
	// attestationStatusRetryAfter spaces fetch attempts after a failure.
	attestationStatusRetryAfter = 5 * time.Minute
	attestationStatusMaxBytes   = 32 << 20
)

// AttestationStatusList is a parsed Google attestation status list.
type AttestationStatusList struct {
	entries map[string]string
}

// ParseAttestationStatusList parses the JSON Google serves at
// AttestationStatusURL.
func ParseAttestationStatusList(data []byte) (*AttestationStatusList, error) {
	var doc struct {
		Entries map[string]struct {
			Status string `json:"status"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("attestation status list is malformed: %w", err)
	}
	if doc.Entries == nil {
		return nil, errors.New("attestation status list has no entries object")
	}
	list := &AttestationStatusList{entries: make(map[string]string, len(doc.Entries))}
	for serial, entry := range doc.Entries {
		list.entries[strings.ToLower(serial)] = entry.Status
	}
	return list, nil
}

// status reports the listed status of a certificate serial number. Any
// listed certificate (REVOKED or SUSPENDED) is untrusted.
func (l *AttestationStatusList) status(serial *big.Int) (string, bool) {
	if l == nil || serial == nil {
		return "", false
	}
	status, ok := l.entries[serial.Text(16)]
	return status, ok
}

// AttestationStatusSource fetches the status list on demand, keeps it in
// memory and in a cache file, and serves it while it is fresh enough. It
// makes no request until a device key is verified.
type AttestationStatusSource struct {
	URL       string
	Client    *http.Client
	CachePath string
	Now       func() time.Time

	mu        sync.Mutex
	list      *AttestationStatusList
	fetchedAt time.Time
	lastTry   time.Time
	loaded    bool
}

// Current returns a list no older than the maximum age, fetching a fresh
// one when the held list is due for refresh. With an error it still returns
// the last list it held, if any, so known revocations are never forgotten.
func (s *AttestationStatusSource) Current(ctx context.Context) (*AttestationStatusList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if !s.loaded {
		s.loaded = true
		s.loadCache()
	}
	var fetchErr error
	if (s.list == nil || now.Sub(s.fetchedAt) > attestationStatusRefreshAfter) &&
		(s.lastTry.IsZero() || now.Sub(s.lastTry) > attestationStatusRetryAfter) {
		s.lastTry = now
		// Detached from the caller: one dropped request must not cost every
		// other device the retry window. The client timeout bounds it.
		fetchErr = s.fetch(context.WithoutCancel(ctx), now)
	}
	if s.list == nil || now.Sub(s.fetchedAt) > attestationStatusMaxAge || s.fetchedAt.After(now) {
		if fetchErr == nil {
			fetchErr = errors.New("no recent fetch")
		}
		return s.list, fmt.Errorf("attestation revocation status is unavailable: %w", fetchErr)
	}
	return s.list, nil
}

func (s *AttestationStatusSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *AttestationStatusSource) loadCache() {
	if s.CachePath == "" {
		return
	}
	data, err := os.ReadFile(s.CachePath)
	if err != nil {
		return
	}
	// The fetch time travels inside the file, not in its mtime, so a
	// restored or copied cache keeps its real age.
	var cached attestationStatusCache
	if json.Unmarshal(data, &cached) != nil || cached.FetchedAt.IsZero() {
		return
	}
	list, err := ParseAttestationStatusList(cached.Body)
	if err != nil {
		return
	}
	s.list, s.fetchedAt = list, cached.FetchedAt
}

type attestationStatusCache struct {
	FetchedAt time.Time       `json:"fetchedAt"`
	Body      json.RawMessage `json:"body"`
}

func (s *AttestationStatusSource) fetch(ctx context.Context, now time.Time) error {
	url := s.URL
	if url == "" {
		url = AttestationStatusURL
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("attestation status list fetch: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, attestationStatusMaxBytes+1))
	if err != nil {
		return err
	}
	if len(data) > attestationStatusMaxBytes {
		return errors.New("attestation status list is too large")
	}
	list, err := ParseAttestationStatusList(data)
	if err != nil {
		return err
	}
	s.list, s.fetchedAt = list, now
	s.saveCache(data, now)
	return nil
}

// saveCache replaces the cache file. When that fails the old file is
// removed: after a restart it could otherwise resurrect a list that predates
// a revocation this process has already seen.
func (s *AttestationStatusSource) saveCache(data []byte, now time.Time) {
	if s.CachePath == "" {
		return
	}
	if !s.writeCache(data, now) {
		_ = os.Remove(s.CachePath)
	}
}

func (s *AttestationStatusSource) writeCache(data []byte, now time.Time) bool {
	body, err := json.Marshal(attestationStatusCache{FetchedAt: now.UTC(), Body: data})
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(s.CachePath), 0o700); err != nil {
		return false
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.CachePath), ".attestation-status-*")
	if err != nil {
		return false
	}
	name := tmp.Name()
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(name, s.CachePath) != nil {
		_ = os.Remove(name)
		return false
	}
	return true
}
