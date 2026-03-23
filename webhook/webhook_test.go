package webhook

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"strings"
	"testing"
	"time"
)

type blockingTransport struct {
	started chan struct{}
	release chan struct{}
}

func (t *blockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case t.started <- struct{}{}:
	default:
	}
	<-t.release
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestDispatch_DeliversSignedPayload(t *testing.T) {
	done := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if got := r.Header.Get("X-Reverb-Signature"); got == "" {
			t.Errorf("expected signature header")
		}
		if !bytes.Contains(body, []byte(`"event":"entry.created"`)) {
			t.Errorf("unexpected payload: %s", body)
		}
		done <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := newDispatcher([]Config{{
		URL:    server.URL,
		Secret: "signing-secret",
	}}, slog.Default(), 4, 1, server.Client())

	dispatcher.Dispatch("posts", "entry.created", map[string]any{"title": "Hello"}, "entry-1")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
	}
}

func TestDispatch_DropsWhenQueueIsFull(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	transport := &blockingTransport{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	client := &http.Client{Transport: transport}

	dispatcher := newDispatcher([]Config{{
		URL: "https://example.com/hook",
	}}, logger, 1, 1, client)

	dispatcher.Dispatch("posts", "entry.created", nil, "one")
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start first request")
	}

	dispatcher.Dispatch("posts", "entry.created", nil, "two")
	dispatcher.Dispatch("posts", "entry.created", nil, "three")

	if !strings.Contains(logs.String(), "queue full, dropping event") {
		t.Fatalf("expected queue full warning, got logs: %s", logs.String())
	}

	close(transport.release)
	time.Sleep(50 * time.Millisecond)
}

func TestDispatch_RetriesOnServerErrorUntilSuccess(t *testing.T) {
	var attempts atomic.Int32
	done := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		done <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dispatcher := newDispatcher([]Config{{
		URL:          server.URL,
		MaxAttempts:  3,
		RetryBackoff: 10 * time.Millisecond,
		Timeout:      250 * time.Millisecond,
	}}, slog.Default(), 4, 1, server.Client())

	dispatcher.Dispatch("posts", "entry.created", map[string]any{"title": "Hello"}, "entry-1")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook retry success")
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestDispatch_DoesNotRetryOnClientError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	dispatcher := newDispatcher([]Config{{
		URL:          server.URL,
		MaxAttempts:  3,
		RetryBackoff: 10 * time.Millisecond,
		Timeout:      250 * time.Millisecond,
	}}, slog.Default(), 4, 1, server.Client())

	dispatcher.Dispatch("posts", "entry.created", map[string]any{"title": "Hello"}, "entry-1")
	time.Sleep(100 * time.Millisecond)

	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}
