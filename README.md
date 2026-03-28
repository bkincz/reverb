# Reverb

A self-hosted backend module for Go. Auth, collections, file storage, SSE streams, and more as a single embeddable package. Drop it into any `net/http` server.

**Requirements:** Go 1.24+

```bash
go get github.com/bkincz/reverb
```

---

## Quick Start

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/bkincz/reverb"
    "github.com/bkincz/reverb/collections"
    "github.com/bkincz/reverb/db/sqlite"
)

func main() {
    rb := reverb.New(reverb.Config{
        DB: sqlite.New("data.db"),
        Auth: reverb.AuthConfig{
            Secret:           "your-secret-at-least-32-characters-long",
            AccessCookieName: "reverb_access", // optional, useful for same-origin SSR apps
        },
    })

    rb.Collection("posts", collections.Schema{
        Access: collections.Access{
            Read:   collections.Public,
            Write:  collections.Role("editor"),
            Delete: collections.Role("admin"),
        },
        Fields: []collections.Field{
            {Name: "title", Type: collections.TypeText, Required: true},
            {Name: "body",  Type: collections.TypeRichText},
        },
    })

    mux := http.NewServeMux()
    if err := rb.Mount(context.Background(), reverb.ForServer(mux.Handle, func(...func(http.Handler) http.Handler) {})); err != nil {
        log.Fatal(err)
    }
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

---

## Features

- JWT auth: register, login, refresh, logout, `me`
- Role hierarchy: `admin > editor > viewer > public`
- Collections: define a schema, get full CRUD with field-level permissions
- Draft / published / archived workflow with scheduled publishing
- Slug management: auto-generated from any field, human-readable lookups
- `RichText` fields: ProseMirror JSON stored, HTML rendered on read
- `SEOMeta` fields: title, description, og:image, canonical, structured data
- File storage: local filesystem or any S3-compatible service (R2, MinIO, Backblaze)
- Real-time: SSE streams per collection with field-level filtering per subscriber
- A/B testing: deterministic variant assignment, conversion tracking
- Form builder: define schema via `rb.Form()`, submissions stored in DB
- Webhooks: HMAC-signed POST on collection events, filterable by slug and event type
- Type generation: `reverb gen types` outputs TypeScript interfaces from live schema
- OpenAPI spec at `GET /_reverb/openapi.json`
- Health endpoint at `GET /_reverb/health`

---

## Databases

```go
import "github.com/bkincz/reverb/db/sqlite"
import "github.com/bkincz/reverb/db/postgres"
import "github.com/bkincz/reverb/db/mysql"

reverb.Config{ DB: sqlite.New("data.db") }
reverb.Config{ DB: postgres.New("postgres://...") }
reverb.Config{ DB: mysql.New("user:pass@tcp(host)/db?parseTime=true") }
```

---

## Collections

```go
rb.Collection("posts", collections.Schema{
    Access: collections.Access{
        Read:   collections.Public,
        Write:  collections.Role("editor"),
        Delete: collections.Role("admin"),
    },
    Fields: []collections.Field{
        {Name: "title",          Type: collections.TypeText,     Required: true},
        {Name: "body",           Type: collections.TypeRichText},
        {Name: "published_at",   Type: collections.TypeDate},
        {Name: "internal_notes", Type: collections.TypeText,     Access: collections.Role("admin")},
    },
    SlugSource: "title", // auto-generates human-readable slugs from this field
})
```

**Endpoints mounted automatically:**

```
GET    /api/collections/{slug}                    list  (?status=&page=&limit=&sort=field:dir)
POST   /api/collections/{slug}                    create (body: {status?, data: {...}})
GET    /api/collections/{slug}/{id}               get (or ?slug=human-slug)
PATCH  /api/collections/{slug}/{id}               update (body: {status?, publish_at?, data: {...}})
DELETE /api/collections/{slug}/{id}               delete (404 if entry not found)
GET    /api/admin/collections                     schema introspection (admin only)
GET    /api/admin/collections/metadata            stable admin metadata (admin only)
GET    /api/collections/{slug}/{id}/versions      list version history (admin only)
GET    /api/collections/{slug}/{id}/versions/{n}  get a specific version snapshot (admin only)
```

Version history is recorded on every update when the schema has `Versioned: true`. Each snapshot captures the state **before** the update was applied.

Restricted fields are absent from responses entirely, not nulled.

`GET /api/admin/collections/metadata` is the stable machine-readable admin surface.

**Schema validation** — `Mount()` returns an error at startup if a collection schema has empty field names, duplicate field names, or a `SlugSource` that doesn't reference a known field.

---

## Auth

```
POST /_reverb/auth/register    {email, password}
POST /_reverb/auth/login       {email, password}  -> access token + refresh cookie (+ optional access cookie)
POST /_reverb/auth/refresh     rotates refresh token (+ optional access cookie)
POST /_reverb/auth/logout
GET  /_reverb/auth/me
```

Reverb always supports `Authorization: Bearer <token>`. If `Auth.AccessCookieName` is set, Reverb also issues an HttpOnly access-token cookie on register, login, and refresh.

`RequireAuth`, `ParseAuth`, and `ResolveSessionWithRefresh` support same-origin cookie sessions when `Auth.AccessCookieName` is enabled.

Protect your own routes:

```go
mux.Handle("GET /dashboard", rb.RequireAuth()(dashboardHandler))
mux.Handle("DELETE /posts/{id}", rb.RequireRole("editor")(deleteHandler))

// ParseAuth injects claims when a token is present but does not block public access.
mux.Handle("GET /feed", rb.ParseAuth()(feedHandler))
```

Resolve the current session directly from an incoming request when you need SSR-friendly auth handling outside middleware:

```go
session, err := rb.ResolveSession(r)
if err == nil {
    fmt.Println(session.Claims.Email, session.Source) // "header" or "cookie"
}

session, err = rb.ResolveSessionWithRefresh(w, r)
if err == nil && session.Refreshed {
    fmt.Println("cookies were rotated for this request")
}
```

---

## Storage

```go
import "github.com/bkincz/reverb/storage/local"
import "github.com/bkincz/reverb/storage/s3"

// Local filesystem
reverb.Config{ Storage: local.New("./uploads", "/_reverb/storage/files") }

// S3-compatible (AWS, R2, MinIO, Backblaze)
store, err := s3.New(s3.Config{
    Bucket:   "my-bucket",
    Region:   "auto",
    Endpoint: "https://...r2.cloudflarestorage.com",
    AccessKey: "...",
    SecretKey: "...",
    BaseURL:   "https://cdn.example.com/my-bucket",
})
if err != nil {
    panic(err)
}
reverb.Config{ Storage: store }
```

```
POST   /_reverb/storage/upload    multipart, 32 MB cap
DELETE /_reverb/storage/{id}
GET    /_reverb/storage
```

---

## Real-time

```
GET /_reverb/realtime/collections/{slug}    SSE stream
```

Clients authenticate via `Authorization: Bearer <token>` header or `?ticket=` (short-lived token from `POST /_reverb/realtime/ticket`). Events: `entry.created`, `entry.updated`, `entry.deleted`.

**TypeScript SDK:**

```ts
import { createClient } from "@reverb/sdk";

const reverb = createClient({ baseURL: "http://localhost:8080" });

reverb.realtime.collection("posts").on("entry.created", (post) => {
    console.log(post);
});
```

---

## Forms

```go
rb.Form("contact", forms.Schema{
    HoneypotField: "company",
    Fields: []forms.Field{
        {Name: "name",    Type: forms.FieldTypeText,     Required: true},
        {Name: "email",   Type: forms.FieldTypeEmail,    Required: true},
        {Name: "message", Type: forms.FieldTypeTextarea},
    },
})
```

```
POST /api/forms/{slug}                         submit
GET  /api/admin/forms                          list definitions (admin)
GET  /api/admin/forms/{slug}/submissions       paginated submissions (admin)
```

Admin routes are only mounted when auth is configured.
Public submissions are rate-limited per client IP. Behind a reverse proxy, set `TrustedProxies` so Reverb can resolve the real client IP safely.

---

## A/B Testing

```
GET  /api/ab/{slug}/variant?visitor_id=    assign (or retrieve) a variant
POST /api/ab/{slug}/convert                {visitor_id, event_name}

GET    /api/admin/ab              list tests
POST   /api/admin/ab              create test ({name, slug, variants, active})
GET    /api/admin/ab/{slug}
PATCH  /api/admin/ab/{slug}
DELETE /api/admin/ab/{slug}
```

Variant assignment is deterministic. The same visitor always gets the same variant.

---

## Webhooks

```go
reverb.Config{
    Webhooks: []webhook.Config{
        {
            URL:    "https://example.com/hook",
            Secret: "signing-secret",            // X-Reverb-Signature: sha256=<hex>
            Events: []string{"entry.created"},   // empty = all events
            Slugs:  []string{"posts"},            // empty = all collections
            Timeout:      5 * time.Second,
            MaxAttempts:  5,
            RetryBackoff: 500 * time.Millisecond,
        },
    },
}
```

Transient delivery failures (`429`, `5xx`, network timeout) are retried with exponential backoff. Other `4xx` responses are treated as permanent failures.

---

## Preview

Issue a short-lived (10-minute) signed token that lets an unauthenticated client read a single entry in its current state, regardless of `status`. Useful for headless CMS draft previews.

```
POST /_reverb/preview/token    {collection, entry_id}  -> {token}   (requires auth)
GET  /_reverb/preview?token=   returns entry filtered to public-readable fields
```

The token is HMAC-signed with a key derived from `Auth.Secret`. Fields that require a role above `public` are stripped from the preview response, and `TypePassword` fields are never returned.

---

## Scheduled Publishing

Set `publish_at` on any entry. Reverb flips it to `published` automatically:

```
PATCH /api/collections/posts/{id}
{"publish_at": "2025-06-01T09:00:00Z"}
```

Fire a callback on publish (e.g. trigger a static site rebuild):

```go
reverb.Config{
    OnPublish: func(ctx context.Context, collectionSlug, entryID string) {
        triggerRebuild()
    },
}
```

---

## Type Generation

```bash
reverb gen types --driver sqlite --db data.db --out reverb.d.ts
reverb clean deprecated --driver sqlite --db data.db
```

---

## Config from Environment

```go
cfg, err := reverb.FromEnv()
if err != nil {
    panic(err)
}
rb := reverb.New(cfg)
```

```
REVERB_DB_DRIVER       sqlite | postgres | mysql
REVERB_DB_DSN
REVERB_AUTH_SECRET
REVERB_AUTH_ACCESS_TTL
REVERB_AUTH_REFRESH_TTL
REVERB_AUTH_COOKIE_SECURE
REVERB_AUTH_COOKIE_DOMAIN
REVERB_TRUSTED_PROXIES
REVERB_FORMS_RATE_LIMIT
REVERB_CORS_ORIGINS
REVERB_LOG_MODE          dev | prod
```

---

## Echo Integration

```go
rb.Mount(ctx, reverb.ForServer(s.Handle, s.Use))
```

`ForServer` adapts any server that has `Handle(pattern, handler)` and `Use(middleware...)` methods.

---

## License

MIT
