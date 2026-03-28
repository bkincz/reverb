package collections

import "sort"

type AdminCollectionMetadata struct {
	Slug       string               `json:"slug"`
	SlugSource string               `json:"slug_source,omitempty"`
	Versioned  bool                 `json:"versioned,omitempty"`
	Access     AdminAccessMetadata  `json:"access"`
	Fields     []AdminFieldMetadata `json:"fields"`
}

type AdminAccessMetadata struct {
	Read   *AccessRule `json:"read,omitempty"`
	Write  *AccessRule `json:"write,omitempty"`
	Delete *AccessRule `json:"delete,omitempty"`
}

type AdminFieldMetadata struct {
	Name        string              `json:"name"`
	Type        FieldType           `json:"type"`
	Required    bool                `json:"required,omitempty"`
	Access      *AccessRule         `json:"access,omitempty"`
	Options     []string            `json:"options,omitempty"`
	Collection  string              `json:"collection,omitempty"`
	TargetSlug  string              `json:"target_slug,omitempty"`
	JoinField   string              `json:"join_field,omitempty"`
	ItemSchema  *AdminFieldMetadata `json:"item_schema,omitempty"`
	WrappedType *AdminFieldMetadata `json:"wrapped_type,omitempty"`
	ReadOnly    bool                `json:"read_only,omitempty"`
	WriteOnly   bool                `json:"write_only,omitempty"`
}

func AdminMetadata(reg *Registry) []AdminCollectionMetadata {
	all := reg.All()
	out := make([]AdminCollectionMetadata, 0, len(all))
	for _, e := range all {
		out = append(out, AdminMetadataForEntry(e))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Slug < out[j].Slug
	})
	return out
}

func AdminMetadataForEntry(e Entry) AdminCollectionMetadata {
	fields := make([]AdminFieldMetadata, 0, len(e.schema.Fields))
	for _, f := range e.schema.Fields {
		fields = append(fields, adminFieldMetadata(f))
	}

	return AdminCollectionMetadata{
		Slug:       e.slug,
		SlugSource: e.schema.SlugSource,
		Versioned:  e.schema.Versioned,
		Access: AdminAccessMetadata{
			Read:   e.schema.Access.Read,
			Write:  e.schema.Access.Write,
			Delete: e.schema.Access.Delete,
		},
		Fields: fields,
	}
}

func adminFieldMetadata(f Field) AdminFieldMetadata {
	out := AdminFieldMetadata{
		Name:       f.Name,
		Type:       f.Type,
		Required:   f.Required,
		Access:     f.Access,
		Options:    f.Options,
		Collection: f.Collection,
		TargetSlug: f.TargetSlug,
		JoinField:  f.JoinField,
		ReadOnly:   f.Type == TypeJoin,
		WriteOnly:  f.Type == TypePassword,
	}

	if f.ItemSchema != nil {
		child := adminFieldMetadata(*f.ItemSchema)
		out.ItemSchema = &child
	}
	if f.WrappedType != nil {
		child := adminFieldMetadata(*f.WrappedType)
		out.WrappedType = &child
	}

	return out
}
