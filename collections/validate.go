package collections

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var reColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

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

	case TypeArray:
		items, ok := val.([]any)
		if !ok {
			return fmt.Errorf("must be an array")
		}
		if f.ItemSchema != nil {
			var errs []string
			for i, elem := range items {
				if err := validateField(*f.ItemSchema, elem); err != nil {
					errs = append(errs, fmt.Sprintf("element[%d]: %s", i, err.Error()))
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("%s", strings.Join(errs, "; "))
			}
		}

	case TypePassword:
		s, ok := val.(string)
		if !ok || s == "" {
			return fmt.Errorf("must be a non-empty string")
		}

	case TypeColor:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("must be a string")
		}
		if !reColor.MatchString(s) {
			return fmt.Errorf("must be a hex color (#RGB or #RRGGBB)")
		}

	case TypePoint:
		m, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("must be an object with lat and lng")
		}
		lat, latOK := m["lat"].(float64)
		lng, lngOK := m["lng"].(float64)
		if !latOK || !lngOK {
			return fmt.Errorf("must contain numeric lat and lng fields")
		}
		if lat < -90 || lat > 90 {
			return fmt.Errorf("lat must be between -90 and 90")
		}
		if lng < -180 || lng > 180 {
			return fmt.Errorf("lng must be between -180 and 180")
		}

	case TypeLocale:
		m, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("must be an object keyed by locale code")
		}
		if f.WrappedType != nil {
			var errs []string
			for locale, v := range m {
				if err := validateField(*f.WrappedType, v); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %s", locale, err.Error()))
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("%s", strings.Join(errs, "; "))
			}
		}

	case TypeJoin:
		// Virtual field — never sent by clients, never validated.
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
