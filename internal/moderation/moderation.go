// Package moderation implements the App Store Guideline 1.2 UGC substrate for
// a self-hosted Beamfall server client (NATIVEMGR-4, apple ADR-0004 §4 /
// ADR-0003 §4): in-app content reporting, per-Profile content blocking, and a
// published moderation/contact path.
//
// Persistence mirrors internal/webhook.FileDeadLetter — a versioned, bounded
// JSON state object under the data directory, written 0600 via temp+rename. It
// touches neither the catalog/events SQLite schema nor the GraphQL wire schema
// (hard invariants #1-#3): a report/block target is a generic (targetType,
// targetID) tuple, never a domain entity. Unlike the dead-letter feed, the
// BLOCK list is never aged out — a silently-evicted block would re-expose
// previously blocked content — so only the reports feed is bounded; a block
// past the cap fails loudly rather than evicting an existing block.
package moderation

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/jsonstate"
	"github.com/beamfall/core/internal/store"
)

// maxReports bounds the persisted reports feed so the file never grows without
// limit; the oldest reports are dropped first (an operator inspects recent
// reports, and an unbounded file is its own operational hazard). maxBlocks caps
// the per-store block list; a new block past the cap fails with ErrBlockLimit
// rather than evicting an existing block (a block must never silently expire —
// see the package comment). Both are vars (not consts) only so the bound tests
// can lower them and stay fast under -race; production never reassigns them.
var (
	maxReports              = 1000
	maxBlocks               = 10000
	maxModerationFieldBytes = 4096
	maxModerationStateBytes = 1 << 20
)

// ErrInvalidRequest is returned for a malformed report/block (empty target).
var ErrInvalidRequest = errors.New("moderation: invalid request")

// ErrBlockLimit is returned when the block list is full; the caller must not
// treat this as success.
var ErrBlockLimit = errors.New("moderation: block limit reached")

// ReportRequest is an in-app content report. TargetType is a generic shape
// ("media"), never a domain label (invariant #1).
type ReportRequest struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetID"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail,omitempty"`
}

// Report is a persisted content report.
type Report struct {
	TargetType        string    `json:"targetType"`
	TargetID          string    `json:"targetID"`
	Reason            string    `json:"reason"`
	Detail            string    `json:"detail,omitempty"`
	At                time.Time `json:"at"`
	ReporterProfileID string    `json:"reporterProfileID"`
}

// BlockRequest is a per-Profile content block.
type BlockRequest struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetID"`
}

// Block is a persisted per-Profile (and, when known, per-Device) block.
type Block struct {
	TargetType string    `json:"targetType"`
	TargetID   string    `json:"targetID"`
	At         time.Time `json:"at"`
	ProfileID  string    `json:"profileID"`
	DeviceID   string    `json:"deviceID,omitempty"`
}

// ContactInfo is the published moderation/contact path returned to clients.
type ContactInfo struct {
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
	Note  string `json:"note,omitempty"`
}

// Scope is the subset of an authenticated session that a moderation write is
// attributed to. The server resolves it from auth.Scope; this package depends
// only on store ID types (arch: moderation -> store).
type Scope struct {
	ProfileID store.ProfileID
	DeviceID  store.DeviceID
}

// state is the persisted form: a version stamp plus the bounded reports feed
// and the unbounded block list.
type state struct {
	Version int      `json:"version"`
	Reports []Report `json:"reports"`
	Blocks  []Block  `json:"blocks"`
}

// Service is a file-backed moderation store plus the published contact info.
type Service struct {
	path    string
	contact ContactInfo
	mu      sync.Mutex
	state   state
}

// Open loads (or initializes) the moderation store at path. A missing file is a
// fresh store; a corrupt file is an error rather than a silent reset, so an
// operator notices a clobbered record instead of losing every prior block.
func Open(path string, contact ContactInfo) (*Service, error) {
	s := &Service{path: path, contact: contact, state: state{Version: 1}}
	var st state
	found, err := jsonstate.Load(path, &st)
	if err != nil {
		return nil, fmt.Errorf("moderation: corrupt store %s: %w", path, err)
	}
	if !found {
		return s, nil
	}
	st.Version = 1
	s.state = st
	return s, nil
}

// Report appends one content report attributed to scope.ProfileID.
func (s *Service) Report(scope Scope, req ReportRequest) error {
	if strings.TrimSpace(req.TargetType) == "" || strings.TrimSpace(req.TargetID) == "" ||
		!moderationFieldsWithinLimit(req.TargetType, req.TargetID, req.Reason, req.Detail) {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := append([]Report(nil), s.state.Reports...)
	s.state.Reports = append(s.state.Reports, Report{
		TargetType:        req.TargetType,
		TargetID:          req.TargetID,
		Reason:            req.Reason,
		Detail:            req.Detail,
		At:                time.Now().UTC(),
		ReporterProfileID: string(scope.ProfileID),
	})
	if len(s.state.Reports) > maxReports {
		s.state.Reports = s.state.Reports[len(s.state.Reports)-maxReports:]
	}
	if err := s.persistLocked(); err != nil {
		s.state.Reports = previous
		return err
	}
	return nil
}

// Block records a per-Profile block. It is idempotent: blocking the same target
// for the same Profile again is a no-op success. A new (non-duplicate) block
// past maxBlocks fails with ErrBlockLimit rather than evicting an existing one.
func (s *Service) Block(scope Scope, req BlockRequest) error {
	if strings.TrimSpace(req.TargetType) == "" || strings.TrimSpace(req.TargetID) == "" ||
		!moderationFieldsWithinLimit(req.TargetType, req.TargetID) {
		return ErrInvalidRequest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.state.Blocks {
		if b.ProfileID == string(scope.ProfileID) && b.TargetType == req.TargetType && b.TargetID == req.TargetID {
			return nil // idempotent
		}
	}
	if len(s.state.Blocks) >= maxBlocks {
		return ErrBlockLimit
	}
	previous := append([]Block(nil), s.state.Blocks...)
	s.state.Blocks = append(s.state.Blocks, Block{
		TargetType: req.TargetType,
		TargetID:   req.TargetID,
		At:         time.Now().UTC(),
		ProfileID:  string(scope.ProfileID),
		DeviceID:   string(scope.DeviceID),
	})
	if err := s.persistLocked(); err != nil {
		s.state.Blocks = previous
		return err
	}
	return nil
}

// Blocks returns this Profile's block list, newest first.
func (s *Service) Blocks(scope Scope) []Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Block, 0)
	for _, b := range s.state.Blocks {
		if b.ProfileID == string(scope.ProfileID) {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Contact returns the published moderation/contact path.
func (s *Service) Contact() ContactInfo {
	return s.contact
}

// persistLocked writes the store atomically (temp + rename). The caller holds
// the mutex. It is 0600 because it shares the secrets-adjacent data tree.
func (s *Service) persistLocked() error {
	s.state.Version = 1
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if len(data) > maxModerationStateBytes {
		return ErrInvalidRequest
	}
	return jsonstate.Save(s.path, s.state)
}

func moderationFieldsWithinLimit(values ...string) bool {
	for _, value := range values {
		if len(value) > maxModerationFieldBytes {
			return false
		}
	}
	return true
}
