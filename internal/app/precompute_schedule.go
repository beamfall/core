package app

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/beamfall/core/internal/playback"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
)

// libraryPrecomputeSchedules declares one rendition precompute Schedule per
// Library (CORE-2326). Each ships disabled and Reconcile never enables it:
// the operator enabling it in the Console is the explicit trigger ADR-0057 §2
// requires, so renditions never fill the disk on their own. A run precomputes
// that Library's renditions inside the rendition cache budget.
type libraryPrecomputeSchedules struct {
	store     *store.Store
	cacheRoot string
	budget    int64
	// sealed is set once the Encrypted Library unseal state is known at boot.
	// Until then, and if never set, Encrypted Libraries yield no work.
	sealed atomic.Pointer[func(store.LibraryID) bool]
}

func newLibraryPrecomputeSchedules(st *store.Store, cacheRoot string, budget int64) *libraryPrecomputeSchedules {
	return &libraryPrecomputeSchedules{store: st, cacheRoot: cacheRoot, budget: budget}
}

func (p *libraryPrecomputeSchedules) setSealed(sealed func(store.LibraryID) bool) {
	if p == nil || sealed == nil {
		return
	}
	p.sealed.Store(&sealed)
}

// enabled reports whether a rendition cache is configured. Without one there
// is nowhere to precompute into, so no Schedule is declared.
func (p *libraryPrecomputeSchedules) enabled() bool {
	return p != nil && p.store != nil && p.budget > 0 && p.cacheRoot != ""
}

// registerAll declares a Schedule for every live Library; call it before the
// boot Reconcile.
func (p *libraryPrecomputeSchedules) registerAll(ctx context.Context, service *schedulepkg.Service) error {
	if !p.enabled() || service == nil {
		return nil
	}
	libraries, err := p.store.Libraries(ctx, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		return err
	}
	for _, library := range libraries {
		service.Register(p.spec(library.ID))
	}
	return nil
}

func (p *libraryPrecomputeSchedules) register(service *schedulepkg.Service, libraryID store.LibraryID) {
	if !p.enabled() || service == nil {
		return
	}
	service.Register(p.spec(libraryID))
}

func (p *libraryPrecomputeSchedules) unregister(service *schedulepkg.Service, libraryID store.LibraryID) {
	if service == nil {
		return
	}
	service.Unregister(playback.PrecomputeScheduleID(libraryID))
}

func (p *libraryPrecomputeSchedules) spec(libraryID store.LibraryID) schedulepkg.Spec {
	return schedulepkg.Spec{
		ID:              string(playback.PrecomputeScheduleID(libraryID)),
		JobKind:         playback.PrecomputeJobKind,
		DefaultTrigger:  schedulepkg.Daily(2, 0),
		DefaultDisabled: true,
		BuildJobs: func(ctx context.Context, now time.Time) ([]store.Job, error) {
			return playback.PrecomputeLibraryJobs(ctx, p.store, playback.PrecomputeLibrarySpec{
				LibraryID:   libraryID,
				CacheRoot:   p.cacheRoot,
				BudgetBytes: p.budget,
				Sealed:      p.sealedPredicate(),
			}, now)
		},
	}
}

func (p *libraryPrecomputeSchedules) sealedPredicate() func(store.LibraryID) bool {
	if sealed := p.sealed.Load(); sealed != nil {
		return *sealed
	}
	return nil
}
