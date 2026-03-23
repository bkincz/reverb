// Package reverb provides authentication, database collections, storage, and
// real-time capabilities as a single embeddable Go module. It is designed to
// work alongside the Echo SSR framework but can be used with any HTTP server.
package reverb

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/ab"
	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/auth"
	"github.com/bkincz/reverb/collections"
	"github.com/bkincz/reverb/db"
	dbmodels "github.com/bkincz/reverb/db/models"
	"github.com/bkincz/reverb/forms"
	"github.com/bkincz/reverb/internal/realip"
	"github.com/bkincz/reverb/internal/roles"
	"github.com/bkincz/reverb/openapi"
	"github.com/bkincz/reverb/realtime"
	"github.com/bkincz/reverb/storage"
	"github.com/bkincz/reverb/webhook"
)

const version = "0.1.0"

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type AuthConfig struct {
	// Auth routes are not registered when this is empty.
	Secret       string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
	CookieSecure bool
	CookieDomain string
}

type FormsConfig struct {
	RateLimitPerMinute int
}

type Config struct {
	DB             db.Adapter
	CORS           api.CORSConfig
	Auth           AuthConfig
	Forms          FormsConfig
	TrustedProxies []string
	// Storage is the file storage adapter. When nil, storage routes are not registered.
	Storage  storage.Adapter
	Webhooks []webhook.Config
	// SeedFunc is called during Mount after migrations complete.
	// It is always called and must be idempotent — check for existing data
	// before inserting to avoid duplicating records on restart.
	SeedFunc func(ctx context.Context, db *bun.DB) error
	// LogMode controls log output format. "prod" uses JSON; "" or "dev" uses text.
	LogMode string
	// OnPublish is called after any entry transitions to published status —
	// either by a direct write or by the scheduled publisher.
	// Use this to trigger static site rebuilds.
	OnPublish func(ctx context.Context, collectionSlug, entryID string)
	// HeadlessMode sets CORS.AllowedOrigins to ["*"] automatically when
	// AllowedOrigins has not been explicitly configured. Enables public
	// read access for GET /api/collections/* from any origin.
	HeadlessMode bool
}

// ---------------------------------------------------------------------------
// Reverb
// ---------------------------------------------------------------------------

type Reverb struct {
	cfg         Config
	db          *bun.DB
	authCfg     auth.Config
	registry    *collections.Registry
	forms       *forms.Registry
	store       storage.Adapter
	broker      *realtime.Broker
	dispatcher  *webhook.Dispatcher
	log         *slog.Logger
	openAPISpec map[string]any
	clientIP    func(*http.Request) string
}

func New(cfg Config) *Reverb {
	if len(cfg.CORS.AllowedOrigins) == 0 {
		cfg.CORS = api.DefaultCORSConfig()
	}
	if cfg.HeadlessMode {
		cfg.CORS.AllowedOrigins = []string{"*"}
	}
	if cfg.Auth.AccessTTL == 0 {
		cfg.Auth.AccessTTL = 15 * time.Minute
	}
	if cfg.Auth.RefreshTTL == 0 {
		cfg.Auth.RefreshTTL = 7 * 24 * time.Hour
	}
	if cfg.Forms.RateLimitPerMinute == 0 {
		cfg.Forms.RateLimitPerMinute = 10
	}
	logger := newLogger(cfg.LogMode)
	return &Reverb{
		cfg:        cfg,
		registry:   collections.NewRegistry(),
		forms:      forms.NewRegistry(),
		store:      cfg.Storage,
		broker:     realtime.NewBroker(),
		dispatcher: webhook.NewDispatcher(cfg.Webhooks, logger),
		log:        logger,
		clientIP:   realip.RemoteAddr,
	}
}

// Collection registers a collection schema under the given slug.
//
//	rb.Collection("posts", collections.Schema{...})
func (r *Reverb) Collection(slug string, schema collections.Schema) {
	r.registry.Register(slug, schema)
}

// Form registers a form schema under the given slug.
// On Mount, Reverb upserts a form_definition record in the DB.
func (r *Reverb) Form(slug string, schema forms.Schema) {
	r.forms.Register(slug, schema)
}

type MountTarget struct {
	Handle func(pattern string, h http.Handler)
	Use    func(mw ...func(http.Handler) http.Handler)
}

// ForServer creates a MountTarget from any server's Handle and Use methods,
// discarding their return values. Works with Echo and any server that follows
// the same middleware pattern.
//
//	rb.Mount(ctx, reverb.ForServer(s.Handle, s.Use))
func ForServer[H, U any](
	handle func(string, http.Handler) H,
	use func(...func(http.Handler) http.Handler) U,
) MountTarget {
	return MountTarget{
		Handle: func(p string, h http.Handler) { handle(p, h) },
		Use:    func(mw ...func(http.Handler) http.Handler) { use(mw...) },
	}
}

func (r *Reverb) Mount(ctx context.Context, target MountTarget) error {
	if err := r.validateConfig(); err != nil {
		return err
	}

	bunDB, err := r.cfg.DB.Open()
	if err != nil {
		return fmt.Errorf("reverb: open db: %w", err)
	}
	r.db = bunDB

	if err := db.Migrate(ctx, r.db); err != nil {
		return err
	}

	if err := collections.CheckDeprecations(ctx, r.db, r.registry, r.log); err != nil {
		return err
	}

	for _, slug := range r.forms.All() {
		schema, _ := r.forms.Get(slug)
		schemaJSON, err := json.Marshal(schema)
		if err != nil {
			return fmt.Errorf("reverb: marshal form schema %q: %w", slug, err)
		}
		row := &dbmodels.FormDefinition{
			ID:        uuid.New().String(),
			Slug:      slug,
			Name:      slug,
			Schema:    schemaJSON,
			CreatedAt: time.Now().UTC(),
		}
		if _, err := r.db.NewInsert().
			Model(row).
			On("CONFLICT (slug) DO UPDATE SET schema = EXCLUDED.schema").
			Exec(ctx); err != nil {
			return fmt.Errorf("reverb: upsert form definition %q: %w", slug, err)
		}
	}

	if r.cfg.SeedFunc != nil {
		if err := r.cfg.SeedFunc(ctx, r.db); err != nil {
			return fmt.Errorf("reverb: seed: %w", err)
		}
	}

	if r.cfg.Auth.Secret != "" {
		r.authCfg = auth.Config{
			DB: r.db,
			Tokens: auth.TokenConfig{
				Secret:     r.cfg.Auth.Secret,
				AccessTTL:  r.cfg.Auth.AccessTTL,
				RefreshTTL: r.cfg.Auth.RefreshTTL,
			},
			CookieSecure: r.cfg.Auth.CookieSecure,
			CookieDomain: r.cfg.Auth.CookieDomain,
		}
	}

	resolver, err := realip.New(r.cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("reverb: TrustedProxies: %w", err)
	}
	r.clientIP = resolver.ClientIP

	target.Use(api.CORS(r.cfg.CORS))

	target.Handle("GET /_reverb/health", http.HandlerFunc(r.handleHealth))

	publish := func(slug, typ string, entry map[string]any, id string) {
		r.broker.Publish(realtime.Event{
			Type:  realtime.EventType(typ),
			Slug:  slug,
			Entry: entry,
			ID:    id,
		})
		r.dispatcher.Dispatch(slug, typ, entry, id)

		if r.cfg.OnPublish != nil {
			status, _ := entry["status"].(string)
			if (typ == "entry.created" || typ == "entry.updated") && status == "published" {
				go r.cfg.OnPublish(context.Background(), slug, id)
			}
		}
	}

	collections.StartScheduler(ctx, r.db, r.log, publish)

	target.Handle("GET /api/collections/{slug}", r.parseAuthMiddleware(collections.HandleList(r.db, r.registry, nil)))
	target.Handle("POST /api/collections/{slug}", r.parseAuthMiddleware(collections.HandleCreate(r.db, r.registry, publish)))
	target.Handle("GET /api/collections/{slug}/{id}", r.parseAuthMiddleware(collections.HandleGet(r.db, r.registry, nil)))
	target.Handle("PATCH /api/collections/{slug}/{id}", r.parseAuthMiddleware(collections.HandleUpdate(r.db, r.registry, publish)))
	target.Handle("DELETE /api/collections/{slug}/{id}", r.parseAuthMiddleware(collections.HandleDelete(r.db, r.registry, publish)))
	target.Handle("GET /_reverb/realtime/collections/{slug}",
		realtime.HandleStream(r.db, r.broker, r.registry, r.authCfg))

	if r.cfg.Auth.Secret != "" {
		rl := auth.NewRateLimiter(10, r.clientIP)

		target.Handle("GET /api/admin/collections", r.adminMiddleware(collections.HandleAdminList(r.registry)))
		target.Handle("POST /_reverb/auth/register", rl(auth.Register(r.authCfg)))
		target.Handle("POST /_reverb/auth/login", rl(auth.Login(r.authCfg)))
		target.Handle("POST /_reverb/auth/refresh", rl(auth.Refresh(r.authCfg)))
		target.Handle("POST /_reverb/auth/logout", auth.Logout(r.authCfg))
		target.Handle("GET /_reverb/auth/me", auth.Me(r.authCfg))
		target.Handle("POST /_reverb/realtime/ticket",
			auth.RequireAuth(r.authCfg)(realtime.HandleTicket(r.authCfg)))
		target.Handle("GET /api/admin/ab", r.adminMiddleware(ab.HandleAdminList(r.db)))
		target.Handle("POST /api/admin/ab", r.adminMiddleware(ab.HandleAdminCreate(r.db)))
		target.Handle("GET /api/admin/ab/{slug}", r.adminMiddleware(ab.HandleAdminGet(r.db)))
		target.Handle("PATCH /api/admin/ab/{slug}", r.adminMiddleware(ab.HandleAdminUpdate(r.db)))
		target.Handle("DELETE /api/admin/ab/{slug}", r.adminMiddleware(ab.HandleAdminDelete(r.db)))
		target.Handle("GET /api/admin/forms", r.adminMiddleware(forms.HandleAdminListForms(r.db)))
		target.Handle("GET /api/admin/forms/{slug}/submissions", r.adminMiddleware(forms.HandleAdminListSubmissions(r.db)))
	}

	if r.store != nil {
		target.Handle("GET /_reverb/storage", r.authMiddleware(storage.HandleList(r.db, r.store)))
		target.Handle("POST /_reverb/storage/upload", r.authMiddleware(storage.HandleUpload(r.db, r.store)))
		target.Handle("DELETE /_reverb/storage/{id}", r.authMiddleware(storage.HandleDelete(r.db, r.store)))

		if fs, ok := r.store.(storage.FileServer); ok {
			target.Handle(fs.FileServePath(), fs.FileServer())
		}
	}

	target.Handle("GET /api/ab/{slug}/variant", r.parseAuthMiddleware(ab.HandleAssign(r.db)))
	target.Handle("POST /api/ab/{slug}/convert", r.parseAuthMiddleware(ab.HandleConvert(r.db)))

	formsRateLimit := auth.NewRateLimiter(r.cfg.Forms.RateLimitPerMinute, r.clientIP)
	target.Handle("POST /api/forms/{slug}", formsRateLimit(forms.HandleSubmit(r.db, r.forms, r.clientIP)))

	r.openAPISpec = openapi.BuildSpec(r.registry, r.cfg.Auth.Secret != "", r.store != nil)
	target.Handle("GET /_reverb/openapi.json", http.HandlerFunc(r.handleOpenAPI))

	r.log.Info("reverb mounted", "version", version)
	return nil
}

func (r *Reverb) DB() *bun.DB {
	return r.db
}

func (r *Reverb) RequireAuth() func(http.Handler) http.Handler {
	return auth.RequireAuth(r.authCfg)
}

func (r *Reverb) RequireRole(role string) func(http.Handler) http.Handler {
	return auth.RequireRole(r.authCfg, role)
}

func (r *Reverb) ParseAuth() func(http.Handler) http.Handler {
	return auth.ParseAuth(r.authCfg)
}

// ---------------------------------------------------------------------------
// Config validation
// ---------------------------------------------------------------------------

func (r *Reverb) validateConfig() error {
	if r.cfg.DB == nil {
		return fmt.Errorf("reverb: Config.DB is required")
	}
	if r.cfg.Auth.Secret != "" && len(r.cfg.Auth.Secret) < 32 {
		return fmt.Errorf("reverb: Auth.Secret must be at least 32 characters")
	}
	if r.cfg.Auth.AccessTTL < 0 {
		return fmt.Errorf("reverb: Auth.AccessTTL must not be negative")
	}
	if r.cfg.Auth.RefreshTTL < 0 {
		return fmt.Errorf("reverb: Auth.RefreshTTL must not be negative")
	}
	if r.cfg.Auth.AccessTTL > 0 && r.cfg.Auth.RefreshTTL > 0 && r.cfg.Auth.RefreshTTL < r.cfg.Auth.AccessTTL {
		return fmt.Errorf("reverb: Auth.RefreshTTL must not be shorter than Auth.AccessTTL")
	}
	if r.cfg.Forms.RateLimitPerMinute < 0 {
		return fmt.Errorf("reverb: Forms.RateLimitPerMinute must not be negative")
	}
	if _, err := realip.New(r.cfg.TrustedProxies); err != nil {
		return fmt.Errorf("reverb: TrustedProxies: %w", err)
	}
	for _, entry := range r.registry.All() {
		if err := validateCollectionSchema(entry.Slug(), entry.Schema()); err != nil {
			return err
		}
	}
	for i, wh := range r.cfg.Webhooks {
		if wh.URL == "" {
			return fmt.Errorf("reverb: Webhooks[%d].URL is required", i)
		}
		u, err := url.Parse(wh.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("reverb: Webhooks[%d].URL must be an http or https URL", i)
		}
		if wh.Timeout < 0 {
			return fmt.Errorf("reverb: Webhooks[%d].Timeout must not be negative", i)
		}
		if wh.MaxAttempts < 0 {
			return fmt.Errorf("reverb: Webhooks[%d].MaxAttempts must not be negative", i)
		}
		if wh.RetryBackoff < 0 {
			return fmt.Errorf("reverb: Webhooks[%d].RetryBackoff must not be negative", i)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Middleware helpers
// ---------------------------------------------------------------------------

func (r *Reverb) authMiddleware(h http.Handler) http.Handler {
	if r.cfg.Auth.Secret == "" {
		return h
	}
	return auth.RequireAuth(r.authCfg)(h)
}

func (r *Reverb) adminMiddleware(h http.Handler) http.Handler {
	if r.cfg.Auth.Secret == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "route not found")
		})
	}
	return auth.RequireRole(r.authCfg, "admin")(h)
}

func (r *Reverb) parseAuthMiddleware(h http.Handler) http.Handler {
	if r.cfg.Auth.Secret == "" {
		return h
	}
	return auth.ParseAuth(r.authCfg)(h)
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (r *Reverb) handleHealth(w http.ResponseWriter, req *http.Request) {
	if err := r.db.PingContext(req.Context()); err != nil {
		api.JSON(w, http.StatusServiceUnavailable, map[string]string{
			"status":  "degraded",
			"version": version,
			"error":   err.Error(),
		})
		return
	}
	api.JSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": version,
	})
}

func (r *Reverb) handleOpenAPI(w http.ResponseWriter, req *http.Request) {
	api.JSON(w, http.StatusOK, r.openAPISpec)
}

func validateCollectionSchema(slug string, schema collections.Schema) error {
	if err := validateAccessRule(schema.Access.Read, slug, "read access"); err != nil {
		return err
	}
	if err := validateAccessRule(schema.Access.Write, slug, "write access"); err != nil {
		return err
	}
	if err := validateAccessRule(schema.Access.Delete, slug, "delete access"); err != nil {
		return err
	}
	for _, field := range schema.Fields {
		if err := validateAccessRule(field.Access, slug, fmt.Sprintf("field %q access", field.Name)); err != nil {
			return err
		}
	}
	return nil
}

func validateAccessRule(rule *collections.AccessRule, slug, location string) error {
	requiredRole := rule.RequiredRole()
	if requiredRole == "" {
		return nil
	}
	if !roles.IsValid(requiredRole) {
		return fmt.Errorf("reverb: collection %q %s references unknown role %q", slug, location, requiredRole)
	}
	return nil
}
