package realtime

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type EventType string

const (
	EventCreated EventType = "entry.created"
	EventUpdated EventType = "entry.updated"
	EventDeleted EventType = "entry.deleted"
)

type Event struct {
	Type  EventType      `json:"type"`
	Slug  string         `json:"slug"`
	Entry map[string]any `json:"entry,omitempty"`
	ID    string         `json:"id,omitempty"`
}
