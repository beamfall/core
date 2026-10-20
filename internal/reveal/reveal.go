// Package reveal records content-bearing surfaces for Reveal-contract tests.
package reveal

import (
	"slices"
	"sync"
)

// SurfaceClass describes how a surface can reveal content.
type SurfaceClass string

const (
	// ContentJSON is a JSON surface that can carry content names or structure.
	ContentJSON SurfaceClass = "content-json"
	// Aggregate is a count or metric surface that can reveal existence.
	Aggregate SurfaceClass = "aggregate"
	// ContentBytes is a byte-serving surface.
	ContentBytes SurfaceClass = "content-bytes"
	// EventFanout is a long-lived delivery surface.
	EventFanout SurfaceClass = "event-fanout"
)

// Surface is one registered Reveal-contract surface.
type Surface struct {
	ID    string
	Class SurfaceClass
}

// Registry stores surfaces for one App instance.
type Registry struct {
	mu       sync.RWMutex
	surfaces []Surface
}

// NewRegistry creates an empty instance registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a surface if it has not already been registered.
func (r *Registry) Register(surface Surface) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.surfaces {
		if existing.ID == surface.ID {
			return
		}
	}
	r.surfaces = append(r.surfaces, surface)
}

// All returns all registered surfaces.
func (r *Registry) All() []Surface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Clone(r.surfaces)
}
