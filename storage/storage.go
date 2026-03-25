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
// Image processing
// ---------------------------------------------------------------------------

type StoredVariant struct {
	Key    string `json:"key"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type ProcessedVariant struct {
	Name        string
	Data        []byte
	Width       int
	Height      int
	ContentType string
}

type ImageProcessor interface {
	ProcessImage(ctx context.Context, original []byte, mime string) (variants []ProcessedVariant, origWidth, origHeight int, err error)
}

// ---------------------------------------------------------------------------
// Optional interfaces
// ---------------------------------------------------------------------------
type FileServer interface {
	FileServer() http.Handler
	FileServePath() string
}
