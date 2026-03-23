package forms

import "sync"

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

type Registry struct {
	mu      sync.RWMutex
	schemas map[string]Schema
}

func NewRegistry() *Registry {
	return &Registry{schemas: make(map[string]Schema)}
}

func (r *Registry) Register(slug string, schema Schema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schemas[slug] = schema
}

func (r *Registry) Get(slug string) (Schema, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.schemas[slug]
	return s, ok
}

func (r *Registry) All() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.schemas))
	for slug := range r.schemas {
		out = append(out, slug)
	}
	return out
}
