package openapi

import (
	"strings"

	"github.com/bkincz/reverb/collections"
)

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

func BuildSpec(reg *collections.Registry, authEnabled, storageEnabled bool) map[string]any {
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "Reverb API",
			"version": "1.0.0",
		},
		"paths": buildPaths(reg, authEnabled, storageEnabled),
		"components": map[string]any{
			"schemas":         buildSchemas(reg),
			"securitySchemes": buildSecuritySchemes(),
		},
	}
	return spec
}

// ---------------------------------------------------------------------------
// Paths
// ---------------------------------------------------------------------------

func buildPaths(reg *collections.Registry, authEnabled, storageEnabled bool) map[string]any {
	paths := map[string]any{}

	paths["/_reverb/health"] = map[string]any{
		"get": op("Health check", "Returns server status.", nil, response200(ref("HealthResponse")), false),
	}

	if authEnabled {
		paths["/_reverb/auth/register"] = map[string]any{
			"post": op("Register", "Create a new user account.", body(ref("AuthRequest")), response200(ref("AuthResponse")), false),
		}
		paths["/_reverb/auth/login"] = map[string]any{
			"post": op("Login", "Authenticate and receive tokens.", body(ref("AuthRequest")), response200(ref("AuthResponse")), false),
		}
		paths["/_reverb/auth/refresh"] = map[string]any{
			"post": op("Refresh token", "Exchange refresh token for new access token.", nil, response200(ref("AuthResponse")), false),
		}
		paths["/_reverb/auth/logout"] = map[string]any{
			"post": op("Logout", "Invalidate the current refresh token.", nil, response200(nil), true),
		}
		paths["/_reverb/auth/me"] = map[string]any{
			"get": op("Current user", "Return the authenticated user's profile.", nil, response200(ref("UserResponse")), true),
		}
		paths["/_reverb/realtime/ticket"] = map[string]any{
			"post": op("SSE ticket", "Issue a short-lived (30s) ticket for EventSource connections.", nil, response200(ref("TicketResponse")), true),
		}
	}

	if storageEnabled {
		paths["/_reverb/storage"] = map[string]any{
			"get": op("List files", "List uploaded files (admin sees all, user sees own).", nil, response200(ref("StorageListResponse")), true),
		}
		paths["/_reverb/storage/upload"] = map[string]any{
			"post": op("Upload file", "Upload a file (multipart/form-data, max 32 MB).", nil, response200(ref("MediaResponse")), true),
		}
		paths["/_reverb/storage/{id}"] = map[string]any{
			"delete": op("Delete file", "Delete a file by ID (owner or admin).", nil, response200(nil), true),
		}
	}

	if authEnabled {
		paths["/api/admin/collections"] = map[string]any{
			"get": op("List schemas", "Return all registered collection schemas (admin only).", nil, response200(nil), true),
		}
	}

	for _, e := range reg.All() {
		slug := e.Slug()
		listPath := "/api/collections/" + slug
		itemPath := "/api/collections/" + slug + "/{id}"
		sseStreamPath := "/_reverb/realtime/collections/" + slug

		paths[listPath] = map[string]any{
			"get":  op("List "+slug, "List entries with filtering, sorting, pagination.", nil, response200(ref(typeName(slug)+"List")), false),
			"post": op("Create "+slug, "Create a new entry.", body(ref(typeName(slug)+"Input")), response200(ref(typeName(slug))), true),
		}
		paths[itemPath] = map[string]any{
			"get":    op("Get "+slug, "Get a single entry by ID.", nil, response200(ref(typeName(slug))), false),
			"patch":  op("Update "+slug, "Partially update an entry.", body(ref(typeName(slug)+"Input")), response200(ref(typeName(slug))), true),
			"delete": op("Delete "+slug, "Delete an entry.", nil, response200(nil), true),
		}
		paths[sseStreamPath] = map[string]any{
			"get": op("Subscribe to "+slug, "Open an SSE stream for collection changes.", nil, response200(nil), false),
		}
	}

	return paths
}

// ---------------------------------------------------------------------------
// Schemas
// ---------------------------------------------------------------------------

func buildSchemas(reg *collections.Registry) map[string]any {
	schemas := map[string]any{
		"HealthResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status":  prop("string", ""),
				"version": prop("string", ""),
			},
		},
		"AuthRequest": map[string]any{
			"type":     "object",
			"required": []string{"email", "password"},
			"properties": map[string]any{
				"email":    prop("string", "format:email"),
				"password": prop("string", ""),
			},
		},
		"AuthResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"access_token": prop("string", ""),
				"user":         ref("UserResponse"),
			},
		},
		"UserResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":    prop("string", "format:uuid"),
				"email": prop("string", "format:email"),
				"role":  prop("string", ""),
			},
		},
		"TicketResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ticket":     prop("string", ""),
				"expires_in": prop("integer", ""),
			},
		},
		"MediaResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":         prop("string", "format:uuid"),
				"filename":   prop("string", ""),
				"url":        prop("string", "format:uri"),
				"mime_type":  prop("string", ""),
				"size":       prop("integer", ""),
				"created_at": prop("string", "format:date-time"),
			},
		},
		"StorageListResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"data":  array(ref("MediaResponse")),
				"total": prop("integer", ""),
			},
		},
	}

	for _, e := range reg.All() {
		slug := e.Slug()
		name := typeName(slug)
		schema := e.Schema()

		dataProps := map[string]any{}
		inputProps := map[string]any{}
		required := []string{}

		for _, f := range schema.Fields {
			oasType := fieldTypeToOAS(f)
			dataProps[f.Name] = oasType
			inputProps[f.Name] = oasType
			if f.Required {
				required = append(required, f.Name)
			}
		}

		entrySchema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":         prop("string", "format:uuid"),
				"status":     prop("string", ""),
				"created_at": prop("string", "format:date-time"),
				"updated_at": prop("string", "format:date-time"),
				"data":       map[string]any{"type": "object", "properties": dataProps},
			},
		}

		inputSchema := map[string]any{
			"type":       "object",
			"properties": inputProps,
		}
		if len(required) > 0 {
			inputSchema["required"] = required
		}

		listSchema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"data":  array(ref(name)),
				"total": prop("integer", ""),
				"page":  prop("integer", ""),
				"limit": prop("integer", ""),
			},
		}

		schemas[name] = entrySchema
		schemas[name+"Input"] = inputSchema
		schemas[name+"List"] = listSchema
	}

	return schemas
}

func buildSecuritySchemes() map[string]any {
	return map[string]any{
		"bearerAuth": map[string]any{
			"type":         "http",
			"scheme":       "bearer",
			"bearerFormat": "JWT",
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func op(summary, description string, requestBody, responses map[string]any, requiresAuth bool) map[string]any {
	o := map[string]any{
		"summary":     summary,
		"description": description,
		"responses":   responses,
	}
	if requestBody != nil {
		o["requestBody"] = requestBody
	}
	if requiresAuth {
		o["security"] = []map[string]any{{"bearerAuth": []string{}}}
	}
	return o
}

func body(schema map[string]any) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{"schema": schema},
		},
	}
}

func response200(schema map[string]any) map[string]any {
	resp := map[string]any{
		"description": "Success",
	}
	if schema != nil {
		resp["content"] = map[string]any{
			"application/json": map[string]any{"schema": schema},
		}
	}
	return map[string]any{"200": resp}
}

func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func prop(typ, format string) map[string]any {
	p := map[string]any{"type": typ}
	trimmed := strings.TrimPrefix(format, "format:")
	if trimmed != "" {
		p["format"] = trimmed
	}
	return p
}

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func typeName(slug string) string {
	parts := strings.Split(slug, "-")
	var b strings.Builder
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

func fieldTypeToOAS(f collections.Field) map[string]any {
	switch f.Type {
	case collections.TypeNumber:
		return map[string]any{"type": "number"}
	case collections.TypeBoolean:
		return map[string]any{"type": "boolean"}
	case collections.TypeDate:
		return map[string]any{"type": "string", "format": "date"}
	case collections.TypeSelect:
		m := map[string]any{"type": "string"}
		if len(f.Options) > 0 {
			m["enum"] = f.Options
		}
		return m
	case collections.TypeJSON, collections.TypeSEOMeta:
		return map[string]any{"type": "object"}
	default:
		return map[string]any{"type": "string"}
	}
}
