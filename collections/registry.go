package collections

import "sync"

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

type Entry struct {
	slug   string
	schema Schema
}

type Registry struct {
	mu          sync.RWMutex
	collections map[string]Entry
}

func NewRegistry() *Registry {
	return &Registry{collections: make(map[string]Entry)}
}

func (r *Registry) Register(slug string, schema Schema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collections[slug] = Entry{slug: slug, schema: schema}
}

func (r *Registry) Get(slug string) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.collections[slug]
	return e, ok
}

func (r *Registry) All() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, 0, len(r.collections))
	for _, e := range r.collections {
		out = append(out, e)
	}
	return out
}

// ---------------------------------------------------------------------------
// Entry accessors
// ---------------------------------------------------------------------------

func (e Entry) Slug() string { return e.slug }

func (e Entry) Schema() Schema { return e.schema }
