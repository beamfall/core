package artwork

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/store"
)

// EntityPosterSource names one place an Entity kind inherits a poster from.
// With FromKind empty it is the Entity's own member Media through Relation,
// in relation order; with FromKind set it is the Entities of that kind that
// point at the Entity through Relation, by title.
type EntityPosterSource struct {
	Relation string
	FromKind string
}

// EntityPosterRule declares that one plugin's Entity kind, when it has no
// poster of its own, takes a copy of the first poster found among Sources in
// order. Kinds without a rule never inherit.
type EntityPosterRule struct {
	PluginID string
	Kind     string
	Sources  []EntityPosterSource
}

const (
	// entitySweepBatchSize bounds the Entities one sweep invocation visits.
	// The steady state is a relation read and a stat per Entity, so the batch
	// is larger than the Media pass's; the invocation still ends early after
	// sweepJobBatchSize copies, which each cost a derivative set.
	entitySweepBatchSize = 50
	// entityPosterSourceLimit bounds the sources read per rule entry: the
	// first poster among this many members is plenty, and it keeps one very
	// large Entity from dominating a batch.
	entityPosterSourceLimit = 200
)

// inheritedMarkerPath is the mark beside an Entity poster that was copied
// from a source rather than supplied for the Entity. It records which source
// the copy came from; a poster without the mark is the Entity's own and the
// sweep never touches it.
func inheritedMarkerPath(path string) string { return path + ".inherited" }

// inheritedSource is the mark's content: the source poster (slash-separated,
// relative to the artwork root) and the size and mtime it had when copied.
// An empty or unreadable mark is an inherited poster of unknown source, which
// the next sweep resolves again.
type inheritedSource struct {
	Source    string `json:"source"`
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mod_time_ns"`
}

func (r inheritedSource) matches(info os.FileInfo) bool {
	return r.Source != "" && r.Size == info.Size() && r.ModTimeNS == info.ModTime().UnixNano()
}

// SetEntityPosterRules declares which Entity kinds inherit a poster and from
// where. Rules are swept in the order given, so a kind that inherits from
// another kind's posters belongs after it.
func (s *Service) SetEntityPosterRules(rules []EntityPosterRule) {
	s.entityRules = append([]EntityPosterRule(nil), rules...)
}

// EntityPosterInherited reports whether the Entity's poster is an inherited
// copy rather than artwork supplied for the Entity itself. Identification
// treats such a poster as absent, so provider artwork still replaces it.
func (s *Service) EntityPosterInherited(id store.EntityID) bool {
	path := s.EntityPosterPath(id)
	return regularFileExists(path) && regularFileExists(inheritedMarkerPath(path))
}

// posterCandidate is one source poster an Entity could inherit.
type posterCandidate struct {
	path string
	// media marks a Media poster, whose server-generated fallback (a frame
	// grab) is not a cover and is never inherited.
	media bool
}

func (c posterCandidate) usable() (os.FileInfo, bool) {
	info, err := os.Stat(c.path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, false
	}
	if c.media && regularFileExists(generatedMarkerPath(c.path)) {
		return nil, false
	}
	return info, true
}

func (s *Service) posterCandidates(ctx context.Context, id store.EntityID, source EntityPosterSource) ([]posterCandidate, error) {
	if source.FromKind == "" {
		members, err := s.store.EntityMemberMediaIDs(ctx, id, source.Relation, entityPosterSourceLimit)
		if err != nil {
			return nil, err
		}
		out := make([]posterCandidate, 0, len(members))
		for _, member := range members {
			out = append(out, posterCandidate{path: s.PosterPath(member), media: true})
		}
		return out, nil
	}
	related, err := s.store.EntitiesRelatingTo(ctx, id, source.FromKind, source.Relation, entityPosterSourceLimit)
	if err != nil {
		return nil, err
	}
	out := make([]posterCandidate, 0, len(related))
	for _, from := range related {
		out = append(out, posterCandidate{path: s.EntityPosterPath(from)})
	}
	return out, nil
}

// sourceName is the stable name a mark records for a source poster.
func (s *Service) sourceName(path string) string {
	rel, err := filepath.Rel(s.dir, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// inheritEntityPoster brings one Entity's poster in line with its rule and
// reports whether it wrote a poster. An Entity with its own poster is left
// alone. An inherited poster stays while the source it was copied from is
// still one of the Entity's sources and unchanged; otherwise the first usable
// source is copied, and with no usable source the stale copy is removed.
func (s *Service) inheritEntityPoster(ctx context.Context, ref store.EntityRef, rule EntityPosterRule) (bool, error) {
	// Sealed and Encrypted Libraries hold no plaintext artwork (ADR-0045
	// §2.3, ADR-0098 area 2); fail closed like poster generation does.
	if (s.sealed != nil && s.sealed(ref.LibraryID)) || (s.encrypted != nil && s.encrypted(ref.LibraryID)) {
		return false, nil
	}
	dst := s.EntityPosterPath(ref.ID)
	marker := inheritedMarkerPath(dst)
	hasPoster := regularFileExists(dst)
	recorded, marked := readInheritedSource(marker)
	if hasPoster && !marked {
		return false, nil
	}
	var candidates []posterCandidate
	for _, source := range rule.Sources {
		found, err := s.posterCandidates(ctx, ref.ID, source)
		if err != nil {
			return false, err
		}
		if hasPoster && recorded.Source != "" {
			for _, candidate := range found {
				if s.sourceName(candidate.path) != recorded.Source {
					continue
				}
				if info, ok := candidate.usable(); ok && recorded.matches(info) {
					return false, nil
				}
				// The recorded source changed; resolve from the top.
				recorded = inheritedSource{}
				break
			}
		}
		candidates = append(candidates, found...)
	}
	for _, candidate := range candidates {
		info, ok := candidate.usable()
		if !ok {
			continue
		}
		wrote, err := s.copyInheritedPoster(dst, candidate, info)
		if err != nil || !wrote {
			return false, err
		}
		for bucket := range bucketWidths {
			if ctx.Err() != nil {
				break
			}
			_, _ = s.EntityPosterDerivative(ctx, ref.ID, bucket)
		}
		return true, nil
	}
	if hasPoster || marked {
		return false, s.removeInheritedPoster(dst)
	}
	return false, nil
}

// copyInheritedPoster publishes a copy of the source poster as the Entity's
// inherited poster and reports whether the poster bytes changed. The mark is
// on disk before the poster and names its source only after it, so a crash
// never leaves an inherited copy looking like the Entity's own artwork, nor a
// mark vouching for bytes that were not written.
func (s *Service) copyInheritedPoster(dst string, candidate posterCandidate, info os.FileInfo) (bool, error) {
	// #nosec G304 -- the source is a poster path this service built under its artwork root.
	data, err := os.ReadFile(candidate.path)
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, nil
	}
	record, err := json.Marshal(inheritedSource{Source: s.sourceName(candidate.path), Size: info.Size(), ModTimeNS: info.ModTime().UnixNano()})
	if err != nil {
		return false, err
	}
	marker := inheritedMarkerPath(dst)

	s.entityPosterMu.Lock()
	defer s.entityPosterMu.Unlock()
	hasPoster := regularFileExists(dst)
	marked := regularFileExists(marker)
	if hasPoster && !marked {
		// The Entity's own poster arrived while the sources were being read.
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return false, err
	}
	if hasPoster {
		// #nosec G304 -- dst is the Entity poster path under the artwork root.
		if current, err := os.ReadFile(dst); err == nil && bytes.Equal(current, data) {
			// Same picture from a new or rewritten source: keep the poster
			// and its derivatives, and re-point the mark.
			return false, writeAtomicFile(marker, record, nil)
		}
	}
	if !marked {
		if err := writeAtomicFile(marker, nil, nil); err != nil {
			return false, err
		}
	}
	if err := writeAtomicFile(dst, data, nil); err != nil {
		if !hasPoster {
			_ = os.Remove(marker)
		}
		return false, err
	}
	for bucket := range bucketWidths {
		_ = os.Remove(filepath.Join(filepath.Dir(dst), SlotPoster+"."+bucket+".jpg"))
	}
	return true, writeAtomicFile(marker, record, nil)
}

// removeInheritedPoster drops an inherited poster whose sources no longer
// offer one, with its derivatives and mark. The Entity's own poster, should
// one have arrived meanwhile, is kept.
func (s *Service) removeInheritedPoster(dst string) error {
	marker := inheritedMarkerPath(dst)
	s.entityPosterMu.Lock()
	defer s.entityPosterMu.Unlock()
	if !regularFileExists(marker) {
		return nil
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for bucket := range bucketWidths {
		_ = os.Remove(filepath.Join(filepath.Dir(dst), SlotPoster+"."+bucket+".jpg"))
	}
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readInheritedSource reads the mark beside an Entity poster. marked reports
// that the mark exists at all; its record is empty when the mark is the
// pending one written ahead of a copy, or unreadable.
func readInheritedSource(marker string) (record inheritedSource, marked bool) {
	// #nosec G304 -- marker is derived from an Entity poster path under the artwork root.
	data, err := os.ReadFile(marker)
	if err != nil {
		return inheritedSource{}, false
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return inheritedSource{}, true
	}
	return record, true
}

// inheritEntityPosters runs one batch of the sweep's Entity pass from the
// checkpoint, rule by rule in declared order, and reports whether Entities
// remain for a later invocation.
func (s *Service) inheritEntityPosters(ctx context.Context, checkpoint *sweepCheckpoint) (bool, error) {
	budget := entitySweepBatchSize
	written := 0
	for checkpoint.EntityRule < len(s.entityRules) {
		rule := s.entityRules[checkpoint.EntityRule]
		refs, err := s.store.EntitiesOfKindAfter(ctx, rule.PluginID, rule.Kind, store.EntityID(checkpoint.EntityLastID), budget+1)
		if err != nil {
			return false, err
		}
		for _, ref := range refs {
			if budget == 0 || written >= sweepJobBatchSize || ctx.Err() != nil {
				return true, nil
			}
			wrote, err := s.inheritEntityPoster(ctx, ref, rule)
			switch {
			case err != nil:
				checkpoint.Failed++
				slog.Debug("artwork: entity poster inheritance failed", "entity", string(ref.ID), "error", err.Error())
			case wrote:
				checkpoint.Inherited++
				written++
			}
			checkpoint.EntityLastID = string(ref.ID)
			budget--
		}
		checkpoint.EntityRule++
		checkpoint.EntityLastID = ""
	}
	return false, nil
}
