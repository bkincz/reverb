package realtime

import "sync"

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type subscriber struct {
	ch   chan Event
	done chan struct{}
	slug string
}

type Broker struct {
	mu          sync.Mutex
	subscribers map[string]map[uint64]*subscriber
	nextID      uint64
	maxConns    int
	totalConns  int
}

// ---------------------------------------------------------------------------
// Broker
// ---------------------------------------------------------------------------

func NewBroker() *Broker {
	return &Broker{
		subscribers: make(map[string]map[uint64]*subscriber),
		maxConns:    1000,
	}
}

func (b *Broker) Subscribe(slug string) (<-chan Event, func(), bool) {
	b.mu.Lock()
	if b.maxConns > 0 && b.totalConns >= b.maxConns {
		b.mu.Unlock()
		return nil, func() {}, false
	}
	b.totalConns++

	id := b.nextID
	b.nextID++

	sub := &subscriber{
		ch:   make(chan Event, 16),
		done: make(chan struct{}),
		slug: slug,
	}

	if b.subscribers[slug] == nil {
		b.subscribers[slug] = make(map[uint64]*subscriber)
	}
	b.subscribers[slug][id] = sub
	b.mu.Unlock()

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if subs, ok := b.subscribers[slug]; ok {
			if s, ok := subs[id]; ok {
				close(s.done)
				delete(subs, id)
				b.totalConns--
			}
			if len(subs) == 0 {
				delete(b.subscribers, slug)
			}
		}
	}

	return sub.ch, unsubscribe, true
}

func trySend(sub *subscriber, event Event) {
	select {
	case sub.ch <- event:
	case <-sub.done:
	default:
	}
}

func (b *Broker) Publish(event Event) {
	b.mu.Lock()
	subs := make([]*subscriber, 0, len(b.subscribers[event.Slug]))
	for _, sub := range b.subscribers[event.Slug] {
		subs = append(subs, sub)
	}
	b.mu.Unlock()

	for _, sub := range subs {
		trySend(sub, event)
	}
}
