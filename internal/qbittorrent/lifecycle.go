package qbittorrent

import (
	"context"
	"errors"

	"github.com/beamfall/core/internal/store"
)

// Reconfigure atomically swaps the live source and routing without a process
// restart. It validates the candidate routing first
// (ValidateRouting against the new source + new routing); only on success does
// it commit, so a rejected config never disturbs the running ingestor. The
// caller (the composition root) builds the new source from the operator's
// connection settings + the password it just wrote to the secure store, and
// supplies the new source's download dirs for the save-path-overlap check.
//
// The supplied downloadDirs also become the new canonical approved download
// roots: the containment gate trusts only roots captured at this approval
// moment, so a root from a PREVIOUS configuration stops admitting imports the
// instant the swap commits (stale-root refusal), and the endpoint cannot
// widen its own containment between approvals.
func (i *Ingestor) Reconfigure(ctx context.Context, source Source, routing map[string]store.LibraryID, downloadDirs []string) error {
	if source == nil {
		return errors.New("qbittorrent: source is required")
	}
	candidate := make(map[string]store.LibraryID, len(routing))
	for category, library := range routing {
		candidate[category] = library
	}
	i.transitionMu.Lock()
	defer i.transitionMu.Unlock()
	if err := i.validateRouting(ctx, source, candidate, downloadDirs); err != nil {
		return err
	}
	i.cancelCurrentGeneration()
	i.publishMu.Lock()
	defer i.publishMu.Unlock()
	i.mu.Lock()
	i.installGenerationLocked(source, candidate, cleanRoots(downloadDirs))
	i.mu.Unlock()
	return nil
}

// Disable retires the live configuration and makes future passes inert. It is
// safe to call repeatedly; each call advances ownership so an ABA transition
// can never revive a pass holding an older source snapshot.
func (i *Ingestor) Disable() {
	i.transitionMu.Lock()
	defer i.transitionMu.Unlock()
	i.cancelCurrentGeneration()
	i.publishMu.Lock()
	defer i.publishMu.Unlock()
	i.mu.Lock()
	i.installGenerationLocked(nil, nil, nil)
	i.mu.Unlock()
}

var errGenerationRetired = errors.New("qbittorrent: ingest generation retired")

func (i *Ingestor) cancelCurrentGeneration() {
	i.mu.RLock()
	cancel := i.cancelGeneration
	i.mu.RUnlock()
	if cancel != nil {
		cancel(errGenerationRetired)
	}
}

func (i *Ingestor) installGenerationLocked(source Source, routing map[string]store.LibraryID, approvedRoots []string) {
	generationCtx, cancelGeneration := context.WithCancelCause(context.Background())
	i.generation++
	i.source = source
	i.routing = routing
	i.approvedRoots = approvedRoots
	i.generationCtx = generationCtx
	i.cancelGeneration = cancelGeneration
}
