package storage

import (
	"context"
	"io"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type UploadInput struct {
	Key         string
	Body        io.Reader
	Size        int64
	ContentType string
}

type ListItem struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// ---------------------------------------------------------------------------
// Adapter
// ---------------------------------------------------------------------------

type Adapter interface {
	Upload(ctx context.Context, input UploadInput) error
	Delete(ctx context.Context, key string) error
	URL(key string) string
	List(ctx context.Context, prefix string, limit int) ([]ListItem, error)
}

// ---------------------------------------------------------------------------
// Optional interfaces
// ---------------------------------------------------------------------------

// FileServer is implemented by adapters that need to serve files locally.
// Adapters backed by a CDN (S3, R2, etc.) do not implement this — their
// URL() method already points at a public host.
type FileServer interface {
	FileServer() http.Handler
	// FileServePath returns the path prefix the handler must be mounted at,
	// e.g. "/_reverb/storage/files/".
	FileServePath() string
}
