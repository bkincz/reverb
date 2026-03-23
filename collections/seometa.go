package collections

import "fmt"

type SEOMeta struct {
	Title          string `json:"title,omitempty"`
	Description    string `json:"description,omitempty"`
	OGImage        string `json:"og_image,omitempty"`
	Canonical      string `json:"canonical,omitempty"`
	StructuredData any    `json:"structured_data,omitempty"`
}

func validateSEOMeta(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("must be an object")
	}
	if title, ok := m["title"]; ok {
		if _, ok := title.(string); !ok {
			return fmt.Errorf("title must be a string")
		}
	}
	if desc, ok := m["description"]; ok {
		if _, ok := desc.(string); !ok {
			return fmt.Errorf("description must be a string")
		}
	}
	if img, ok := m["og_image"]; ok {
		if _, ok := img.(string); !ok {
			return fmt.Errorf("og_image must be a string")
		}
	}
	if can, ok := m["canonical"]; ok {
		if _, ok := can.(string); !ok {
			return fmt.Errorf("canonical must be a string")
		}
	}
	return nil
}
