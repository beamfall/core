package governor

import "sync"

// ClassInference is the heavy, preemptible job class local AI enrichment runs
// under: inference is niced background work, preempted
// by interactive playback/transcode, and cancellable — batched overnight, never
// synchronously depended on. Disabling the AI capability cancels every in-flight
// inference job through CancelClass (the disable-cancels-jobs lifecycle ADR-0124
// decision 3 states for Workflow Plugins, restated here for this Integration
// Plugin).
const ClassInference = "inference"

// jobRegistry tracks the in-flight jobs of a heavy governor job class so the
// capability's disable path can cancel them all before deactivation, never
// orphaning a running job. Each registered job carries a cancel func (typically
// a context.CancelFunc); cancelling one is idempotent — a job that already
// finished has been deregistered and is a no-op.
type jobRegistry struct {
	mu     sync.Mutex
	nextID int64
	jobs   map[string]map[int64]func()
}

func newJobRegistry() *jobRegistry {
	return &jobRegistry{jobs: map[string]map[int64]func(){}}
}

// register enrolls one in-flight job of the named class, returning a release
// func the caller MUST invoke when the job finishes so a completed job is never
// later cancelled. cancel is stored verbatim; a nil cancel registers a job that
// occupies the class but has no cancellation effect.
func (r *jobRegistry) register(class string, cancel func()) (release func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := r.nextID
	byID, ok := r.jobs[class]
	if !ok {
		byID = map[int64]func(){}
		r.jobs[class] = byID
	}
	byID[id] = cancel
	return func() { r.deregister(class, id) }
}

// deregister removes one job from its class without cancelling it — the normal
// completion path.
func (r *jobRegistry) deregister(class string, id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if byID, ok := r.jobs[class]; ok {
		delete(byID, id)
		if len(byID) == 0 {
			delete(r.jobs, class)
		}
	}
}

// cancelClass cancels every in-flight job of the named class and clears it,
// returning how many jobs were cancelled. Cancel funcs are invoked outside the
// lock so a cancel callback that races to deregister itself cannot deadlock.
func (r *jobRegistry) cancelClass(class string) int {
	r.mu.Lock()
	byID := r.jobs[class]
	cancels := make([]func(), 0, len(byID))
	for _, cancel := range byID {
		cancels = append(cancels, cancel)
	}
	delete(r.jobs, class)
	r.mu.Unlock()

	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
	return len(cancels)
}

// activeClass reports how many in-flight jobs of the named class are registered.
func (r *jobRegistry) activeClass(class string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs[class])
}
