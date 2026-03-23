package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	defaultWorkerCount = 16
	defaultQueueSize   = 256
	defaultTimeout     = 10 * time.Second
	defaultMaxAttempts = 3
	defaultBackoff     = 500 * time.Millisecond
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Config defines a single webhook endpoint.
type Config struct {
	// URL is the endpoint that receives POST requests.
	URL string
	// Secret is used to sign the payload as X-Reverb-Signature: sha256=<hex>.
	// Empty means no signature header is sent.
	Secret string
	// Events filters which event types trigger this webhook (entry.created,
	// entry.updated, entry.deleted). Empty means all events.
	Events []string
	// Slugs filters which collection slugs trigger this webhook.
	// Empty means all collections.
	Slugs []string
	// Timeout bounds a single delivery attempt. Zero uses the default.
	Timeout time.Duration
	// MaxAttempts controls total delivery attempts, including the first try.
	// Zero uses the default.
	MaxAttempts int
	// RetryBackoff is the base delay between retries. Zero uses the default.
	RetryBackoff time.Duration
}

type Payload struct {
	Event string         `json:"event"`
	Slug  string         `json:"slug"`
	Entry map[string]any `json:"entry,omitempty"`
	ID    string         `json:"id,omitempty"`
}

// ---------------------------------------------------------------------------
// Dispatcher
// ---------------------------------------------------------------------------

type Dispatcher struct {
	configs []Config
	client  *http.Client
	log     *slog.Logger
	jobs    chan dispatchJob
}

func NewDispatcher(configs []Config, log *slog.Logger) *Dispatcher {
	return newDispatcher(configs, log, defaultQueueSize, defaultWorkerCount, &http.Client{Timeout: defaultTimeout})
}

type dispatchJob struct {
	cfg     Config
	payload Payload
}

func newDispatcher(configs []Config, log *slog.Logger, queueSize, workerCount int, client *http.Client) *Dispatcher {
	if queueSize < 1 {
		queueSize = 1
	}
	if workerCount < 1 {
		workerCount = 1
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	d := &Dispatcher{
		configs: configs,
		client:  client,
		log:     log,
		jobs:    make(chan dispatchJob, queueSize),
	}
	for range workerCount {
		go d.worker()
	}
	return d
}

func (d *Dispatcher) worker() {
	for job := range d.jobs {
		d.fire(job.cfg, job.payload)
	}
}

func (d *Dispatcher) warn(msg string, args ...any) {
	if d.log != nil {
		d.log.Warn(msg, args...)
	}
}

func (d *Dispatcher) err(msg string, args ...any) {
	if d.log != nil {
		d.log.Error(msg, args...)
	}
}

func (d *Dispatcher) enqueue(cfg Config, payload Payload) {
	select {
	case d.jobs <- dispatchJob{cfg: cfg, payload: payload}:
	default:
		d.warn("webhook: queue full, dropping event",
			"url", cfg.URL,
			"event", payload.Event,
			"slug", payload.Slug,
		)
	}
}

func (d *Dispatcher) Dispatch(slug, event string, entry map[string]any, id string) {
	payload := Payload{
		Event: event,
		Slug:  slug,
		Entry: entry,
		ID:    id,
	}
	for _, cfg := range d.configs {
		if !matches(cfg.Slugs, slug) || !matches(cfg.Events, event) {
			continue
		}
		d.enqueue(cfg, payload)
	}
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (d *Dispatcher) fire(cfg Config, payload Payload) {
	attempts := cfg.MaxAttempts
	if attempts < 1 {
		attempts = defaultMaxAttempts
	}
	backoff := cfg.RetryBackoff
	if backoff <= 0 {
		backoff = defaultBackoff
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		retry, err := d.deliver(cfg, payload)
		if err == nil {
			return
		}
		if !retry {
			d.warn("webhook: delivery failed without retry",
				"url", cfg.URL,
				"attempt", attempt,
				"max_attempts", attempts,
				"error", err,
				"event", payload.Event,
				"slug", payload.Slug,
			)
			return
		}
		if attempt == attempts {
			d.err("webhook: delivery exhausted retries",
				"url", cfg.URL,
				"attempts", attempts,
				"error", err,
				"event", payload.Event,
				"slug", payload.Slug,
			)
			return
		}

		delay := retryDelay(backoff, attempt)
		d.warn("webhook: retrying delivery",
			"url", cfg.URL,
			"attempt", attempt,
			"next_attempt", attempt+1,
			"delay", delay.String(),
			"error", err,
			"event", payload.Event,
			"slug", payload.Slug,
		)
		time.Sleep(delay)
	}
}

func (d *Dispatcher) deliver(cfg Config, payload Payload) (bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("marshal payload: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "reverb-webhook/1")
	if cfg.Secret != "" {
		req.Header.Set("X-Reverb-Signature", sign(body, cfg.Secret))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return true, fmt.Errorf("deliver: %w", err)
	}
	defer resp.Body.Close()

	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}

	err = fmt.Errorf("non-2xx response: status=%d", resp.StatusCode)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return true, err
	}
	return false, err
}

// sign returns "sha256=<hex>" HMAC-SHA256 of body using secret.
func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return fmt.Sprintf("sha256=%s", hex.EncodeToString(mac.Sum(nil)))
}

func matches(filter []string, value string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == value {
			return true
		}
	}
	return false
}

func retryDelay(base time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for range attempt - 1 {
		if delay >= 8*time.Second {
			return 8 * time.Second
		}
		delay *= 2
	}
	if delay > 8*time.Second {
		return 8 * time.Second
	}
	return delay
}
