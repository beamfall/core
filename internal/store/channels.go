package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ErrChannelNotFound means no Channel exists with the given ID.
var ErrChannelNotFound = errors.New("channel not found")

// ChannelID is a stable Channel identifier.
type ChannelID string

// ChannelOrdering is a Channel's ordering policy. v1 ships
// two policies; the column is open for `marathon` as a later extension.
type ChannelOrdering string

const (
	// ChannelOrderingShuffle plays the source in a seeded pseudo-random order.
	ChannelOrderingShuffle ChannelOrdering = "shuffle"
	// ChannelOrderingChronological plays the source in its natural feed order.
	ChannelOrderingChronological ChannelOrdering = "chronological"
)

// ChannelRepeat is a Channel's repeat policy.
type ChannelRepeat string

const (
	// ChannelRepeatOff stops at the end of the compiled window.
	ChannelRepeatOff ChannelRepeat = "off"
	// ChannelRepeatAll re-tunes from the top when the window is exhausted.
	ChannelRepeatAll ChannelRepeat = "all"
)

// Channel is a named virtual-linear playback object: a generic
// structural object — NOT a Core schema entity — wrapping a content source (a
// smart-collection reference or an inline query, ADR-0067) plus an ordering and
// repeat policy. A Channel is per-Profile (ProfileID set) or server-shared
// (ProfileID empty). SourceExpr is opaque client JSON stored and returned
// verbatim (same posture as SmartCollection.SourceExpr) — the store never
// interprets it here; it is decoded only when the Channel is tuned into a Play
// Queue (see TuneChannel). Channels compile to ordinary Play Queues using
// existing client vocabulary only (Decision 3), so a client that predates
// Channels still plays a tuned queue.
type Channel struct {
	ID        ChannelID
	LibraryID LibraryID
	// ProfileID scopes a per-Profile Channel; empty means a server-shared Channel.
	ProfileID  ProfileID
	Name       string
	SourceExpr string
	Ordering   ChannelOrdering
	Repeat     ChannelRepeat
	CreatedAt  time.Time
}

// validChannelOrdering normalizes and validates an ordering policy, defaulting
// an empty value to shuffle.
func validChannelOrdering(o ChannelOrdering) (ChannelOrdering, error) {
	switch o {
	case "":
		return ChannelOrderingShuffle, nil
	case ChannelOrderingShuffle, ChannelOrderingChronological:
		return o, nil
	default:
		return "", ValidationError("ordering must be shuffle or chronological")
	}
}

// validChannelRepeat normalizes and validates a repeat policy, defaulting an
// empty value to off.
func validChannelRepeat(r ChannelRepeat) (ChannelRepeat, error) {
	switch r {
	case "":
		return ChannelRepeatOff, nil
	case ChannelRepeatOff, ChannelRepeatAll:
		return r, nil
	default:
		return "", ValidationError("repeat must be off or all")
	}
}

// Channels returns the Channels visible to a Profile in a Library, newest first:
// the Profile's own per-Profile Channels plus every server-shared Channel. A
// server-shared Channel's own visibility default is resolved by the caller
// (ADR-0158 resolved question 1); this store read returns the union.
func (s *Store) Channels(ctx context.Context, profileID ProfileID, libraryID LibraryID) ([]Channel, error) {
	if libraryID == "" {
		return nil, ValidationError("library ID is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return nil, err
	}
	rows, err := s.catalogR.QueryContext(ctx, `
		SELECT id, library_id, profile_id, name, source_expr, ordering_policy, repeat_policy, created_at_ms
		FROM channel
		WHERE library_id = ? AND (profile_id = ? OR profile_id IS NULL)
		ORDER BY created_at_ms DESC
	`, libraryID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	if channels == nil {
		channels = []Channel{}
	}
	return channels, rows.Err()
}

// CreateChannel inserts a new Channel. The Library must exist (FK). Name is
// trimmed; empty name is rejected. Empty SourceExpr defaults to "{}" so the
// column is always valid JSON. Ordering/Repeat default to shuffle/off. An empty
// ProfileID stores a server-shared Channel.
func (s *Store) CreateChannel(ctx context.Context, c Channel, now time.Time) (Channel, error) {
	c.Name = strings.TrimSpace(c.Name)
	if c.LibraryID == "" {
		return Channel{}, ValidationError("library ID is required")
	}
	if c.Name == "" {
		return Channel{}, ValidationError("name is required")
	}
	ordering, err := validChannelOrdering(c.Ordering)
	if err != nil {
		return Channel{}, err
	}
	repeat, err := validChannelRepeat(c.Repeat)
	if err != nil {
		return Channel{}, err
	}
	c.Ordering = ordering
	c.Repeat = repeat
	c.SourceExpr = defaultJSON(c.SourceExpr)
	id, err := randomChannelID()
	if err != nil {
		return Channel{}, err
	}
	c.ID = id
	c.CreatedAt = now.UTC()
	createdAtMS := c.CreatedAt.UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Channel{}, err
	}
	_, err = s.catalogW.ExecContext(ctx, `
		INSERT INTO channel (id, library_id, profile_id, name, source_expr, ordering_policy, repeat_policy, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, c.ID, c.LibraryID, nullString(string(c.ProfileID)), c.Name, c.SourceExpr, string(c.Ordering), string(c.Repeat), createdAtMS)
	if err != nil {
		return Channel{}, err
	}
	return c, nil
}

// ChannelByID fetches a Channel by its ID without Profile restriction. Used by
// the graph layer to check Library visibility and ownership before edits.
func (s *Store) ChannelByID(ctx context.Context, id ChannelID) (Channel, error) {
	if id == "" {
		return Channel{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Channel{}, err
	}
	c, err := scanChannel(s.catalogR.QueryRowContext(ctx, `
		SELECT id, library_id, profile_id, name, source_expr, ordering_policy, repeat_policy, created_at_ms
		FROM channel WHERE id = ?
	`, id))
	if err != nil {
		return Channel{}, ErrChannelNotFound
	}
	return c, nil
}

// UpdateChannel updates a Channel's editable metadata, source expression, and
// policies. Empty Name/SourceExpr fall back to the existing values; empty
// Ordering/Repeat likewise keep the current policy, so a partial edit never
// clobbers an unspecified field.
func (s *Store) UpdateChannel(ctx context.Context, c Channel) (Channel, error) {
	if c.ID == "" {
		return Channel{}, ValidationError("id is required")
	}
	existing, err := s.ChannelByID(ctx, c.ID)
	if err != nil {
		return Channel{}, err
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = existing.Name
	}
	sourceExpr := c.SourceExpr
	if sourceExpr == "" {
		sourceExpr = existing.SourceExpr
	}
	ordering := existing.Ordering
	if c.Ordering != "" {
		ordering, err = validChannelOrdering(c.Ordering)
		if err != nil {
			return Channel{}, err
		}
	}
	repeat := existing.Repeat
	if c.Repeat != "" {
		repeat, err = validChannelRepeat(c.Repeat)
		if err != nil {
			return Channel{}, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Channel{}, err
	}
	if _, err := s.catalogW.ExecContext(ctx, `
		UPDATE channel SET name = ?, source_expr = ?, ordering_policy = ?, repeat_policy = ? WHERE id = ?
	`, name, sourceExpr, string(ordering), string(repeat), c.ID); err != nil {
		return Channel{}, err
	}
	existing.Name = name
	existing.SourceExpr = sourceExpr
	existing.Ordering = ordering
	existing.Repeat = repeat
	return existing, nil
}

// DeleteChannel removes a Channel by ID and returns the deleted row.
func (s *Store) DeleteChannel(ctx context.Context, id ChannelID) (Channel, error) {
	if id == "" {
		return Channel{}, ValidationError("id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.guardLocked(); err != nil {
		return Channel{}, err
	}
	c, err := scanChannel(s.catalogR.QueryRowContext(ctx, `
		SELECT id, library_id, profile_id, name, source_expr, ordering_policy, repeat_policy, created_at_ms
		FROM channel WHERE id = ?
	`, id))
	if err != nil {
		return Channel{}, ErrChannelNotFound
	}
	if _, err := s.catalogW.ExecContext(ctx, `DELETE FROM channel WHERE id = ?`, id); err != nil {
		return Channel{}, err
	}
	return c, nil
}

func scanChannel(scanner rowScanner) (Channel, error) {
	var c Channel
	var profileID sql.NullString
	var ordering, repeat string
	var createdAtMS int64
	if err := scanner.Scan(&c.ID, &c.LibraryID, &profileID, &c.Name, &c.SourceExpr, &ordering, &repeat, &createdAtMS); err != nil {
		return Channel{}, err
	}
	c.ProfileID = ProfileID(profileID.String)
	c.Ordering = ChannelOrdering(ordering)
	c.Repeat = ChannelRepeat(repeat)
	c.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	return c, nil
}

func randomChannelID() (ChannelID, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return ChannelID("channel-" + hex.EncodeToString(raw[:])), nil
}
