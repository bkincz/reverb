package collections

import (
	"encoding/json"

	"github.com/bkincz/reverb/internal/roles"
)

// ---------------------------------------------------------------------------
// Field types
// ---------------------------------------------------------------------------

type FieldType string

const (
	TypeText     FieldType = "text"
	TypeRichText FieldType = "richtext"
	TypeNumber   FieldType = "number"
	TypeBoolean  FieldType = "boolean"
	TypeDate     FieldType = "date"
	TypeMedia    FieldType = "media"
	TypeRelation FieldType = "relation"
	TypeSelect   FieldType = "select"
	TypeJSON     FieldType = "json"
	TypeSEOMeta  FieldType = "seometa"
	TypeArray    FieldType = "array"
	TypePassword FieldType = "password"
	TypeColor    FieldType = "color"
	TypePoint    FieldType = "point"
	TypeLocale   FieldType = "locale"
	TypeJoin     FieldType = "join"
)

// ---------------------------------------------------------------------------
// Access rules
// ---------------------------------------------------------------------------

type AccessRule struct {
	minRole string
}

var Public = &AccessRule{}

func Role(role string) *AccessRule {
	return &AccessRule{minRole: role}
}

func (a *AccessRule) RequiredRole() string {
	if a == nil {
		return ""
	}
	return a.minRole
}

func (a *AccessRule) Allowed(role string) bool {
	if a == nil || a.minRole == "" {
		return true
	}
	return roles.Allowed(role, a.minRole)
}

func (a *AccessRule) MarshalJSON() ([]byte, error) {
	if a == nil || a.minRole == "" {
		return []byte(`{}`), nil
	}
	return json.Marshal(struct {
		MinRole string `json:"min_role"`
	}{MinRole: a.minRole})
}

// ---------------------------------------------------------------------------
// Schema types
// ---------------------------------------------------------------------------

type Access struct {
	Read   *AccessRule
	Write  *AccessRule
	Delete *AccessRule
}

type Field struct {
	Name        string
	Type        FieldType
	Required    bool
	Access      *AccessRule
	Options     []string
	Collection  string
	ItemSchema  *Field
	TargetSlug  string
	JoinField   string
	WrappedType *Field
}

type Schema struct {
	Access     Access
	Fields     []Field
	SlugSource string
	Versioned  bool
}
