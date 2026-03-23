package reverb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bkincz/reverb"
	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/auth"
	"github.com/bkincz/reverb/collections"
	dbmodels "github.com/bkincz/reverb/db/models"
	"github.com/bkincz/reverb/db/sqlite"
)

func TestMount_HealthEndpoint(t *testing.T) {
	rb := reverb.New(reverb.Config{
		DB: sqlite.New(":memory:"),
	})

	mux := http.NewServeMux()
	var middleware []func(http.Handler) http.Handler

	target := reverb.MountTarget{
		Handle: func(pattern string, h http.Handler) { mux.Handle(pattern, h) },
		Use: func(mw ...func(http.Handler) http.Handler) {
			middleware = append(middleware, mw...)
		},
	}

	if err := rb.Mount(context.Background(), target); err != nil {
		t.Fatalf("Mount() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/_reverb/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want %q", body["status"], "ok")
	}
	if body["version"] == "" {
		t.Error("version should not be empty")
	}
}

func TestMount_MigrationsIdempotent(t *testing.T) {
	cfg := reverb.Config{DB: sqlite.New(":memory:")}

	// Mount twice against the same DB — second call should not error.
	rb1 := reverb.New(cfg)
	target1 := reverb.MountTarget{
		Handle: func(_ string, _ http.Handler) {},
		Use:    func(_ ...func(http.Handler) http.Handler) {},
	}
	if err := rb1.Mount(context.Background(), target1); err != nil {
		t.Fatalf("first Mount() error: %v", err)
	}

	// Reuse the same underlying DB via the bun.DB accessor.
	rb2 := reverb.New(cfg)
	target2 := reverb.MountTarget{
		Handle: func(_ string, _ http.Handler) {},
		Use:    func(_ ...func(http.Handler) http.Handler) {},
	}
	if err := rb2.Mount(context.Background(), target2); err != nil {
		t.Fatalf("second Mount() error: %v", err)
	}
}

func TestForServer(t *testing.T) {
	type fakeServer struct{ called bool }

	var handleCalled, useCalled bool

	handle := func(p string, h http.Handler) *fakeServer {
		handleCalled = true
		return &fakeServer{}
	}
	use := func(mw ...func(http.Handler) http.Handler) *fakeServer {
		useCalled = true
		return &fakeServer{}
	}

	mt := reverb.ForServer(handle, use)

	mt.Handle("/test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	mt.Use(func(next http.Handler) http.Handler { return next })

	if !handleCalled {
		t.Error("ForServer: Handle was not called through")
	}
	if !useCalled {
		t.Error("ForServer: Use was not called through")
	}
}

func TestCORSMiddleware(t *testing.T) {
	rb := reverb.New(reverb.Config{
		DB: sqlite.New(":memory:"),
	})

	mux := http.NewServeMux()
	var middleware []func(http.Handler) http.Handler

	target := reverb.MountTarget{
		Handle: func(pattern string, h http.Handler) { mux.Handle(pattern, h) },
		Use: func(mw ...func(http.Handler) http.Handler) {
			middleware = append(middleware, mw...)
		},
	}

	if err := rb.Mount(context.Background(), target); err != nil {
		t.Fatalf("Mount() error: %v", err)
	}

	// Build handler chain with collected middleware.
	var handler http.Handler = mux
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}

	req := httptest.NewRequest(http.MethodOptions, "/_reverb/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Error("CORS origin header missing")
	}
}

func TestCORSMiddleware_AllowsCredentialsForSpecificOrigins(t *testing.T) {
	rb := reverb.New(reverb.Config{
		DB: sqlite.New(":memory:"),
		CORS: api.CORSConfig{
			AllowedOrigins: []string{"http://localhost:3000"},
			AllowedMethods: []string{"GET", "POST", "OPTIONS"},
			AllowedHeaders: []string{"Authorization", "Content-Type"},
			MaxAge:         86400,
		},
	})

	mux := http.NewServeMux()
	var middleware []func(http.Handler) http.Handler

	target := reverb.MountTarget{
		Handle: func(pattern string, h http.Handler) { mux.Handle(pattern, h) },
		Use: func(mw ...func(http.Handler) http.Handler) {
			middleware = append(middleware, mw...)
		},
	}

	if err := rb.Mount(context.Background(), target); err != nil {
		t.Fatalf("Mount() error: %v", err)
	}

	var handler http.Handler = mux
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}

	req := httptest.NewRequest(http.MethodOptions, "/_reverb/auth/refresh", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("expected Access-Control-Allow-Credentials=true, got %q", rec.Header().Get("Access-Control-Allow-Credentials"))
	}
}

func TestMount_InvalidCollectionRoleFails(t *testing.T) {
	rb := reverb.New(reverb.Config{
		DB: sqlite.New(":memory:"),
	})
	rb.Collection("posts", collections.Schema{
		Access: collections.Access{
			Read: collections.Role("admni"),
		},
		Fields: []collections.Field{
			{Name: "title", Type: collections.TypeText},
		},
	})

	target := reverb.MountTarget{
		Handle: func(_ string, _ http.Handler) {},
		Use:    func(_ ...func(http.Handler) http.Handler) {},
	}

	if err := rb.Mount(context.Background(), target); err == nil {
		t.Fatal("expected invalid collection role to fail validation")
	}
}

func TestMount_AdminRoutesHiddenWithoutAuth(t *testing.T) {
	rb := reverb.New(reverb.Config{
		DB: sqlite.New(":memory:"),
	})

	mux := http.NewServeMux()
	target := reverb.MountTarget{
		Handle: func(pattern string, h http.Handler) { mux.Handle(pattern, h) },
		Use:    func(_ ...func(http.Handler) http.Handler) {},
	}

	if err := rb.Mount(context.Background(), target); err != nil {
		t.Fatalf("Mount() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/collections", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("admin route without auth = %d, want 404", rec.Code)
	}
}

func TestMount_ABVariantUsesJWTClaims(t *testing.T) {
	const secret = "test-secret-key-at-least-32-bytes!!"

	rb := reverb.New(reverb.Config{
		DB: sqlite.New(":memory:"),
		Auth: reverb.AuthConfig{
			Secret: secret,
		},
	})

	mux := http.NewServeMux()
	target := reverb.MountTarget{
		Handle: func(pattern string, h http.Handler) { mux.Handle(pattern, h) },
		Use:    func(_ ...func(http.Handler) http.Handler) {},
	}

	if err := rb.Mount(context.Background(), target); err != nil {
		t.Fatalf("Mount() error: %v", err)
	}

	variants, err := json.Marshal([]map[string]any{
		{"id": "variant-a", "name": "Variant A", "weight_percent": 100},
	})
	if err != nil {
		t.Fatalf("marshal variants: %v", err)
	}

	row := &dbmodels.ABTest{
		ID:        "test-1",
		Name:      "Homepage",
		Slug:      "homepage",
		Variants:  variants,
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := rb.DB().NewInsert().Model(row).Exec(context.Background()); err != nil {
		t.Fatalf("insert AB test: %v", err)
	}

	token, err := auth.SignAccess(auth.TokenConfig{
		Secret:    secret,
		AccessTTL: time.Minute,
	}, "user-123", "user@example.com", "viewer")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/ab/homepage/variant", nil)
	req.SetPathValue("slug", "homepage")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("A/B variant status = %d, want 200; body: %s", rec.Code, rec.Body)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["variant_id"] != "variant-a" {
		t.Fatalf("variant_id = %q, want %q", body["variant_id"], "variant-a")
	}
}
