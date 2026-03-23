package forms

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type FieldType string

const (
	FieldTypeText     FieldType = "text"
	FieldTypeEmail    FieldType = "email"
	FieldTypeTextarea FieldType = "textarea"
	FieldTypeNumber   FieldType = "number"
	FieldTypeBoolean  FieldType = "boolean"
	FieldTypeSelect   FieldType = "select"
)

type Field struct {
	Name     string
	Type     FieldType
	Required bool
	Options  []string
}

type Schema struct {
	Fields []Field
}
