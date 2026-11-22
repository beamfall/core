package offlinepack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
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
	attestationStatusRetryAfter   = 5 * time.Minute
	attestationStatusMaxBytes     = 32 << 20
	attestationStatusFetchTimeout = 30 * time.Second
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
// memory and serves it while it is fresh enough. It makes no request until
// a device key is verified. It keeps no disk cache: a stale or rolled-back
// file could hide a revocation, so a restarted server fetches again and
// fails closed while it cannot.
type AttestationStatusSource struct {
	URL    string
	Client *http.Client
	Now    func() time.Time

	mu        sync.Mutex
	list      *AttestationStatusList
	fetchedAt time.Time
	lastTry   time.Time
}

// Current returns a list no older than the maximum age, fetching a fresh
// one when the held list is due for refresh. With an error it still returns
// the last list it held, if any, so known revocations are never forgotten.
func (s *AttestationStatusSource) Current(ctx context.Context) (*AttestationStatusList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var fetchErr error
	if (s.list == nil || now.Sub(s.fetchedAt) > attestationStatusRefreshAfter) &&
		(s.lastTry.IsZero() || now.Sub(s.lastTry) > attestationStatusRetryAfter) {
		s.lastTry = now
		// Detached from the caller: one dropped request must not cost every
		// other device the retry window. Bounded here whatever the client.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attestationStatusFetchTimeout)
		fetchErr = s.fetch(fetchCtx, now)
		cancel()
	}
	if s.list == nil || now.Sub(s.fetchedAt) > attestationStatusMaxAge {
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

func (s *AttestationStatusSource) fetch(ctx context.Context, now time.Time) error {
	url := s.URL
	if url == "" {
		url = AttestationStatusURL
	}
	client := s.Client
	if client == nil {
		client = &http.Client{}
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
	return nil
}
