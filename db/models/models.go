package models

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

type User struct {
	bun.BaseModel `bun:"table:reverb_users,alias:u"`

	ID           string          `bun:"id,pk,type:varchar(36)"`
	Email        string          `bun:"email,notnull,unique"`
	PasswordHash string          `bun:"password_hash,notnull"`
	Role         string          `bun:"role,notnull,default:'viewer'"`
	Metadata     json.RawMessage `bun:"metadata,type:json,nullzero"`
	CreatedAt    time.Time       `bun:"created_at,notnull"`
	UpdatedAt    time.Time       `bun:"updated_at,notnull"`
}

type RefreshToken struct {
	bun.BaseModel `bun:"table:reverb_refresh_tokens,alias:rt"`

	ID        string    `bun:"id,pk,type:varchar(36)"`
	UserID    string    `bun:"user_id,notnull,type:varchar(36)"`
	TokenHash string    `bun:"token_hash,notnull,unique"`
	ExpiresAt time.Time `bun:"expires_at,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
}

// ---------------------------------------------------------------------------
// Collections
// ---------------------------------------------------------------------------

type Collection struct {
	bun.BaseModel `bun:"table:reverb_collections,alias:c"`

	ID               string          `bun:"id,pk,type:varchar(36)"`
	Slug             string          `bun:"slug,notnull,unique"`
	Name             string          `bun:"name,notnull"`
	Schema           json.RawMessage `bun:"schema,type:json,notnull"`
	DeprecatedFields json.RawMessage `bun:"deprecated_fields,type:json,nullzero"`
	Version          int             `bun:"version,notnull,default:1"`
	CreatedAt        time.Time       `bun:"created_at,notnull"`
	UpdatedAt        time.Time       `bun:"updated_at,notnull"`
}

type CollectionEntry struct {
	bun.BaseModel `bun:"table:reverb_collection_entries,alias:ce"`

	ID             string          `bun:"id,pk,type:varchar(36)"`
	CollectionSlug string          `bun:"collection_slug,notnull,type:varchar(255)"`
	Status         string          `bun:"status,notnull,default:'draft'"`
	Data           json.RawMessage `bun:"data,type:json,notnull"`
	PublishAt      *time.Time      `bun:"publish_at,nullzero"`
	CreatedAt      time.Time       `bun:"created_at,notnull"`
	UpdatedAt      time.Time       `bun:"updated_at,notnull"`
}

type CollectionSlug struct {
	bun.BaseModel `bun:"table:reverb_collection_slugs"`

	EntryID        string `bun:"entry_id,pk"`
	CollectionSlug string `bun:"collection_slug,notnull"`
	Slug           string `bun:"slug,notnull"`
}

// ---------------------------------------------------------------------------
// Media
// ---------------------------------------------------------------------------

type Media struct {
	bun.BaseModel `bun:"table:reverb_media,alias:m"`

	ID         string    `bun:"id,pk,type:varchar(36)"`
	UserID     string    `bun:"user_id,type:varchar(36),nullzero"`
	Filename   string    `bun:"filename,notnull"`
	StorageKey string    `bun:"storage_key,notnull"`
	MimeType   string    `bun:"mime_type,notnull"`
	Size       int64     `bun:"size,notnull"`
	Alt        string          `bun:"alt,nullzero"`
	Width      int             `bun:"width,nullzero"`
	Height     int             `bun:"height,nullzero"`
	Variants   json.RawMessage `bun:"variants,type:json,nullzero"`
	CreatedAt  time.Time       `bun:"created_at,notnull"`
}

// ---------------------------------------------------------------------------
// Versions
// ---------------------------------------------------------------------------

type EntryVersion struct {
	bun.BaseModel `bun:"table:reverb_versions,alias:ev"`

	ID             string          `bun:"id,pk,type:varchar(36)"`
	CollectionSlug string          `bun:"collection_slug,notnull"`
	EntryID        string          `bun:"entry_id,notnull"`
	Version        int             `bun:"version,notnull"`
	Data           json.RawMessage `bun:"data,type:json,notnull"`
	Status         string          `bun:"status,notnull"`
	CreatedByID    string          `bun:"created_by,nullzero"`
	Label          string          `bun:"label,nullzero"`
	CreatedAt      time.Time       `bun:"created_at,notnull"`
}

// ---------------------------------------------------------------------------
// A/B Tests
// ---------------------------------------------------------------------------

type ABTest struct {
	bun.BaseModel `bun:"table:reverb_ab_tests,alias:ab"`

	ID        string          `bun:"id,pk,type:varchar(36)"`
	Name      string          `bun:"name,notnull"`
	Slug      string          `bun:"slug,notnull,unique"`
	Variants  json.RawMessage `bun:"variants,type:json,notnull"`
	Rules     json.RawMessage `bun:"rules,type:json,nullzero"`
	Active    bool            `bun:"active,notnull,default:false"`
	CreatedAt time.Time       `bun:"created_at,notnull"`
}

type ABTestAssignment struct {
	bun.BaseModel `bun:"table:reverb_ab_assignments"`

	ID        string    `bun:"id,pk"`
	TestSlug  string    `bun:"test_slug,notnull"`
	VisitorID string    `bun:"visitor_id,notnull"`
	VariantID string    `bun:"variant_id,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
}

type ABConversionEvent struct {
	bun.BaseModel `bun:"table:reverb_ab_conversion_events"`

	ID        string    `bun:"id,pk"`
	TestSlug  string    `bun:"test_slug,notnull"`
	VisitorID string    `bun:"visitor_id,notnull"`
	EventName string    `bun:"event_name,notnull"`
	CreatedAt time.Time `bun:"created_at,notnull"`
}

// ---------------------------------------------------------------------------
// Forms
// ---------------------------------------------------------------------------

type FormDefinition struct {
	bun.BaseModel `bun:"table:reverb_form_definitions,alias:fd"`

	ID        string          `bun:"id,pk,type:varchar(36)"`
	Slug      string          `bun:"slug,notnull,unique"`
	Name      string          `bun:"name,notnull"`
	Schema    json.RawMessage `bun:"schema,type:json,notnull"`
	CreatedAt time.Time       `bun:"created_at,notnull"`
}

type FormSubmission struct {
	bun.BaseModel `bun:"table:reverb_form_submissions,alias:fs"`

	ID        string          `bun:"id,pk,type:varchar(36)"`
	FormID    string          `bun:"form_id,notnull,type:varchar(36)"`
	Data      json.RawMessage `bun:"data,type:json,notnull"`
	Metadata  json.RawMessage `bun:"metadata,type:json,nullzero"`
	CreatedAt time.Time       `bun:"created_at,notnull"`
}

// ---------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------

type Job struct {
	bun.BaseModel `bun:"table:reverb_jobs,alias:j"`

	ID          string          `bun:"id,pk,type:varchar(36)"`
	Name        string          `bun:"name,notnull"`
	Payload     json.RawMessage `bun:"payload,type:json,notnull"`
	Status      string          `bun:"status,notnull,default:'pending'"`
	Attempts    int             `bun:"attempts,notnull,default:0"`
	MaxAttempts int             `bun:"max_attempts,notnull,default:3"`
	LastError   string          `bun:"last_error,nullzero"`
	RunAt       time.Time       `bun:"run_at,notnull"`
	CreatedAt   time.Time       `bun:"created_at,notnull"`
	UpdatedAt   time.Time       `bun:"updated_at,notnull"`
}
