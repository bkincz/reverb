package collections

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func ValidateData(data map[string]any, schema Schema, requireAll bool) map[string]string {
	errs := map[string]string{}

	fieldIndex := map[string]Field{}
	for _, f := range schema.Fields {
		fieldIndex[f.Name] = f
	}

	if requireAll {
		for _, f := range schema.Fields {
			if f.Required {
				if _, ok := data[f.Name]; !ok {
					errs[f.Name] = "required"
				}
			}
		}
	}

	for name, val := range data {
		f, ok := fieldIndex[name]
		if !ok {
			continue
		}
		if err := validateField(f, val); err != nil {
			errs[name] = err.Error()
		}
	}
	return errs
}

// ---------------------------------------------------------------------------
// Field-level type checking
// ---------------------------------------------------------------------------

func validateField(f Field, val any) error {
	if val == nil {
		return nil
	}

	switch f.Type {
	case TypeText:
		if _, ok := val.(string); !ok {
			return fmt.Errorf("must be a string")
		}

	case TypeRichText:
		if _, ok := val.(map[string]any); !ok {
			return fmt.Errorf("must be a ProseMirror JSON object")
		}

	case TypeSEOMeta:
		if err := validateSEOMeta(val); err != nil {
			return err
		}

	case TypeNumber:
		if _, ok := val.(float64); !ok {
			return fmt.Errorf("must be a number")
		}

	case TypeBoolean:
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("must be a boolean")
		}

	case TypeDate:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("must be a date string")
		}
		if err := parseDate(s); err != nil {
			return fmt.Errorf("must be a valid date (RFC3339 or YYYY-MM-DD)")
		}

	case TypeSelect:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("must be a string")
		}
		for _, opt := range f.Options {
			if s == opt {
				return nil
			}
		}
		return fmt.Errorf("must be one of: %v", f.Options)

	case TypeMedia, TypeRelation:
		if _, ok := val.(string); !ok {
			return fmt.Errorf("must be a string (UUID reference)")
		}

	case TypeJSON:
		// Any value is acceptable for raw JSON fields.
	}

	return nil
}

func parseDate(s string) error {
	// Try RFC3339 first, then date-only.
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return nil
	}
	if _, err := time.Parse("2006-01-02", s); err == nil {
		return nil
	}
	return fmt.Errorf("unparseable date: %q", s)
}
