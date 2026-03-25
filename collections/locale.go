package collections

import (
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Locale resolution
// ---------------------------------------------------------------------------

func resolveLocale(stored map[string]any, preference string) any {
	if preference != "" {
		if v, ok := stored[preference]; ok {
			return v
		}
		if idx := strings.IndexByte(preference, '-'); idx > 0 {
			tag := preference[:idx]
			if v, ok := stored[tag]; ok {
				return v
			}
		}
	}

	if v, ok := stored["en"]; ok {
		return v
	}

	keys := make([]string, 0, len(stored))
	for k := range stored {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return stored[keys[0]]
	}
	return nil
}
