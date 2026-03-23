package realtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/auth"
	"github.com/bkincz/reverb/collections"
)

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

func HandleStream(db *bun.DB, broker *Broker, reg *collections.Registry, authCfg auth.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		e, ok := reg.Get(slug)
		if !ok {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "collection not found")
			return
		}
		schema := e.Schema()

		role, err := resolveRole(r, authCfg)
		if err != nil {
			api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "invalid or expired token")
			return
		}

		if role == "" {
			if !schema.Access.Read.Allowed("public") {
				api.Error(w, http.StatusUnauthorized, api.CodeUnauthorized, "authentication required")
				return
			}
			role = "public"
		}

		if !schema.Access.Read.Allowed(role) {
			api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "streaming not supported")
			return
		}

		ch, unsubscribe, ok := broker.Subscribe(slug)
		if !ok {
			api.Error(w, http.StatusServiceUnavailable, api.CodeInternalError, "too many SSE connections")
			return
		}
		defer unsubscribe()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		fmt.Fprint(w, "event: connected\ndata: {}\n\n")
		flusher.Flush()

		for {
			select {
			case event := <-ch:
				if err := writeEvent(w, event, role, schema); err != nil {
					return
				}
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func resolveRole(r *http.Request, authCfg auth.Config) (string, error) {
	if authCfg.Tokens.Secret == "" {
		return "public", nil
	}

	tokenStr := bearerToken(r)
	if tokenStr != "" {
		claims, err := auth.VerifyAccess(authCfg.Tokens, tokenStr)
		if err != nil {
			return "", fmt.Errorf("realtime: verify token: %w", err)
		}
		return claims.Role, nil
	}

	if ticket := r.URL.Query().Get("ticket"); ticket != "" {
		claims, err := auth.VerifySSETicket(authCfg.Tokens, ticket)
		if err != nil {
			return "", fmt.Errorf("realtime: verify ticket: %w", err)
		}
		return claims.Role, nil
	}

	if token := r.URL.Query().Get("token"); token != "" {
		claims, err := auth.VerifyAccess(authCfg.Tokens, token)
		if err != nil {
			return "", fmt.Errorf("realtime: verify token: %w", err)
		}
		return claims.Role, nil
	}

	return "", nil
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(header, "Bearer ")
}

func writeEvent(w http.ResponseWriter, event Event, role string, schema collections.Schema) error {
	var payload map[string]any

	if event.Type == EventDeleted {
		payload = map[string]any{
			"type": string(event.Type),
			"slug": event.Slug,
			"id":   event.ID,
		}
	} else {
		filtered := filterFields(event.Entry, role, schema)
		payload = map[string]any{
			"type":  string(event.Type),
			"slug":  event.Slug,
			"entry": filtered,
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("realtime: marshal event: %w", err)
	}

	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", string(event.Type), data)
	return err
}

func filterFields(entry map[string]any, role string, schema collections.Schema) map[string]any {
	if entry == nil {
		return nil
	}

	out := make(map[string]any, len(entry))
	for k, v := range entry {
		out[k] = v
	}
	rawData, ok := entry["data"].(map[string]any)

	if !ok {
		return out
	}
	filteredData := make(map[string]any, len(rawData))
	for k, v := range rawData {
		filteredData[k] = v
	}
	for _, f := range schema.Fields {
		rule := f.Access
		if rule == nil {
			rule = schema.Access.Read
		}
		if !rule.Allowed(role) {
			delete(filteredData, f.Name)
		}
	}
	out["data"] = filteredData
	return out
}
