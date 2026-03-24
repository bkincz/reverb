package realtime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bkincz/reverb/auth"
	"github.com/bkincz/reverb/collections"
	"github.com/bkincz/reverb/realtime"
)

// ---------------------------------------------------------------------------
// Broker tests
// ---------------------------------------------------------------------------

func TestBroker_SubscribeAndReceive(t *testing.T) {
	b := realtime.NewBroker()
	ch, unsub, ok := b.Subscribe("posts")
	if !ok {
		t.Fatal("Subscribe returned false (at capacity)")
	}
	defer unsub()

	b.Publish(realtime.Event{Type: realtime.EventCreated, Slug: "posts", Entry: map[string]any{"title": "hello"}})

	select {
	case got := <-ch:
		if got.Type != realtime.EventCreated {
			t.Fatalf("expected EventCreated, got %s", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBroker_MultipleSubscribersOnSameSlug(t *testing.T) {
	b := realtime.NewBroker()
	ch1, unsub1, _ := b.Subscribe("posts")
	ch2, unsub2, _ := b.Subscribe("posts")
	defer unsub1()
	defer unsub2()

	b.Publish(realtime.Event{Type: realtime.EventCreated, Slug: "posts"})

	for i, ch := range []<-chan realtime.Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Slug != "posts" {
				t.Fatalf("subscriber %d: unexpected slug: %s", i, got.Slug)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestBroker_DifferentSlugIsolation(t *testing.T) {
	b := realtime.NewBroker()
	ch, unsub, _ := b.Subscribe("comments")
	defer unsub()

	b.Publish(realtime.Event{Type: realtime.EventCreated, Slug: "posts"})

	select {
	case got := <-ch:
		t.Fatalf("comments subscriber should not receive posts event, got: %v", got)
	case <-time.After(50 * time.Millisecond):
		// correct — no event delivered
	}
}

func TestBroker_UnsubscribeStopsDelivery(t *testing.T) {
	b := realtime.NewBroker()
	_, unsub, _ := b.Subscribe("posts")

	unsub()

	b.Publish(realtime.Event{Type: realtime.EventCreated, Slug: "posts"})
}

func TestBroker_SlowSubscriberNotBlocked(t *testing.T) {
	b := realtime.NewBroker()
	_, unsub, _ := b.Subscribe("posts")
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			b.Publish(realtime.Event{Type: realtime.EventCreated, Slug: "posts"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on full channel")
	}
}

func TestBroker_ConcurrentPublishAndSubscribe(t *testing.T) {
	b := realtime.NewBroker()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, unsub, _ := b.Subscribe("posts")
			b.Publish(realtime.Event{Type: realtime.EventCreated, Slug: "posts"})
			unsub()
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Handler tests
// ---------------------------------------------------------------------------

func newTestRegistry(slug string) *collections.Registry {
	reg := collections.NewRegistry()
	reg.Register(slug, collections.Schema{
		Access: collections.Access{
			Read:   collections.Public,
			Write:  collections.Public,
			Delete: collections.Public,
		},
		Fields: []collections.Field{
			{Name: "title", Type: collections.TypeText},
		},
	})
	return reg
}

type sseRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func newSSERecorder() *sseRecorder {
	return &sseRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		flushed:          make(chan struct{}, 64),
	}
}

func (r *sseRecorder) Flush() {
	r.ResponseRecorder.Flush()
	select {
	case r.flushed <- struct{}{}:
	default:
	}
}

func (r *sseRecorder) waitFlush(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-r.flushed:
	case <-time.After(timeout):
		t.Fatal("timed out waiting for SSE flush")
	}
}

func TestHandleStream_ConnectedEvent(t *testing.T) {
	broker := realtime.NewBroker()
	reg := newTestRegistry("posts")

	ctx, cancel := context.WithCancel(context.Background())

	req := httptest.NewRequest("GET", "/_reverb/realtime/collections/posts", nil).WithContext(ctx)
	req.SetPathValue("slug", "posts")
	w := newSSERecorder()

	done := make(chan struct{})
	go func() {
		realtime.HandleStream(broker, reg, auth.Config{}).ServeHTTP(w, req)
		close(done)
	}()

	w.waitFlush(t, time.Second)
	cancel()
	<-done

	body := w.Body.String()
	if !strings.Contains(body, "event: connected") {
		t.Fatalf("expected connected event in body, got:\n%s", body)
	}
	if !strings.Contains(body, "data: {}") {
		t.Fatalf("expected data: {} in body, got:\n%s", body)
	}
}

func TestHandleStream_PublishedEventAppearsInStream(t *testing.T) {
	broker := realtime.NewBroker()
	reg := newTestRegistry("posts")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequest("GET", "/_reverb/realtime/collections/posts", nil).WithContext(ctx)
	req.SetPathValue("slug", "posts")
	w := newSSERecorder()

	done := make(chan struct{})
	go func() {
		realtime.HandleStream(broker, reg, auth.Config{}).ServeHTTP(w, req)
		close(done)
	}()

	w.waitFlush(t, time.Second)

	broker.Publish(realtime.Event{
		Type:  realtime.EventCreated,
		Slug:  "posts",
		Entry: map[string]any{"title": "hello"},
	})

	w.waitFlush(t, time.Second)

	cancel()
	<-done

	body := w.Body.String()
	if !strings.Contains(body, "entry.created") {
		t.Fatalf("expected entry.created in stream body, got:\n%s", body)
	}
}

func TestHandleStream_UnknownSlug404(t *testing.T) {
	broker := realtime.NewBroker()
	reg := collections.NewRegistry()

	req := httptest.NewRequest("GET", "/_reverb/realtime/collections/missing", nil)
	req.SetPathValue("slug", "missing")
	w := httptest.NewRecorder()

	realtime.HandleStream(broker, reg, auth.Config{}).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleStream_ContextCancellationClosesStream(t *testing.T) {
	broker := realtime.NewBroker()
	reg := newTestRegistry("posts")

	ctx, cancel := context.WithCancel(context.Background())

	req := httptest.NewRequest("GET", "/_reverb/realtime/collections/posts", nil).WithContext(ctx)
	req.SetPathValue("slug", "posts")
	w := newSSERecorder()

	done := make(chan struct{})
	go func() {
		realtime.HandleStream(broker, reg, auth.Config{}).ServeHTTP(w, req)
		close(done)
	}()

	w.waitFlush(t, time.Second)
	cancel()

	select {
	case <-done:

	case <-time.After(time.Second):
		t.Fatal("handler did not return after context cancellation")
	}
}
