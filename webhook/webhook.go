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
}

func NewDispatcher(configs []Config, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		configs: configs,
		client:  &http.Client{Timeout: 10 * time.Second},
		log:     log,
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
		go d.fire(cfg, payload)
	}
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func (d *Dispatcher) fire(cfg Config, payload Payload) {
	body, err := json.Marshal(payload)
	if err != nil {
		d.log.Error("webhook: marshal payload", "error", err, "url", cfg.URL)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		d.log.Error("webhook: build request", "error", err, "url", cfg.URL)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "reverb-webhook/1")
	if cfg.Secret != "" {
		req.Header.Set("X-Reverb-Signature", sign(body, cfg.Secret))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		d.log.Error("webhook: deliver", "error", err, "url", cfg.URL)
		return
	}

	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		d.log.Warn("webhook: non-2xx response",
			"url", cfg.URL,
			"status", resp.StatusCode,
			"event", payload.Event,
			"slug", payload.Slug,
		)
	}
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
