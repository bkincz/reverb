package collections_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/extra/bundebug"
	"modernc.org/sqlite"

	"github.com/bkincz/reverb/auth"
	"github.com/bkincz/reverb/collections"
	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestDB(t *testing.T) *bun.DB {
	t.Helper()
	sql.Register("sqlite3_col_"+t.Name(), &sqlite.Driver{})
	sqlDB, err := sql.Open("sqlite3_col_"+t.Name(), "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	bunDB.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(false)))

	ctx := context.Background()
	models := []any{
		(*dbmodels.Collection)(nil),
		(*dbmodels.CollectionEntry)(nil),
		(*dbmodels.CollectionSlug)(nil),
	}
	for _, m := range models {
		if _, err := bunDB.NewCreateTable().Model(m).IfNotExists().Exec(ctx); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	t.Cleanup(func() { _ = bunDB.Close() })
	return bunDB
}

var blogSchema = collections.Schema{
	Access: collections.Access{
		Read:   collections.Public,
		Write:  collections.Role("editor"),
		Delete: collections.Role("admin"),
	},
	Fields: []collections.Field{
		{Name: "title", Type: collections.TypeText, Required: true},
		{Name: "body", Type: collections.TypeRichText},
		{Name: "secret", Type: collections.TypeText, Access: collections.Role("admin")},
		{Name: "views", Type: collections.TypeNumber},
	},
}

func bearerRequest(method, path, body, token string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

const testSecret = "test-secret-key-at-least-32-bytes!!"

var testTokenCfg = auth.TokenConfig{
	Secret:    testSecret,
	AccessTTL: 15 * time.Minute,
}

func signToken(t *testing.T, role string) string {
	t.Helper()
	tok, err := auth.SignAccess(testTokenCfg, "user-1", "u@test.com", role)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

func authMiddleware(h http.Handler) http.Handler {
	cfg := auth.Config{
		Tokens: testTokenCfg,
	}
	return auth.RequireAuth(cfg)(h)
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func serveWithAuth(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	authMiddleware(h).ServeHTTP(rr, req)
	return rr
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

func setPathValues(req *http.Request, vals map[string]string) *http.Request {
	for k, v := range vals {
		req.SetPathValue(k, v)
	}
	return req
}

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := collections.NewRegistry()
	reg.Register("posts", blogSchema)

	e, ok := reg.Get("posts")
	if !ok {
		t.Fatal("expected to find posts collection")
	}
	if len(e.Schema().Fields) != len(blogSchema.Fields) {
		t.Fatalf("field count mismatch: want %d got %d", len(blogSchema.Fields), len(e.Schema().Fields))
	}
}

func TestRegistry_All(t *testing.T) {
	reg := collections.NewRegistry()
	reg.Register("posts", blogSchema)
	reg.Register("pages", blogSchema)

	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("want 2 collections, got %d", len(all))
	}
}

func TestRegistry_UnknownSlug(t *testing.T) {
	reg := collections.NewRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Fatal("expected false for unknown slug")
	}
}

// ---------------------------------------------------------------------------
// Query tests
// ---------------------------------------------------------------------------

func TestCreateAndGetEntry(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	entry, err := collections.CreateEntry(ctx, db, "posts", map[string]any{
		"title": "Hello World",
	}, "draft", "editor", blogSchema)
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if entry.ID == "" {
		t.Fatal("expected non-empty ID")
	}

	got, err := collections.GetEntry(ctx, db, "posts", entry.ID, "editor", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	data, _ := got["data"].(map[string]any)
	if data["title"] != "Hello World" {
		t.Fatalf("want title=Hello World, got %v", data["title"])
	}
}

func TestListEntries_Pagination(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := collections.CreateEntry(ctx, db, "posts", map[string]any{
			"title": "Post",
		}, "draft", "editor", blogSchema)
		if err != nil {
			t.Fatalf("create entry %d: %v", i, err)
		}
	}

	entries, total, err := collections.ListEntries(ctx, db, "posts", collections.ListParams{
		Page:  1,
		Limit: 2,
	}, "editor", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Fatalf("want total=5, got %d", total)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries on page 1, got %d", len(entries))
	}

	page2, _, err := collections.ListEntries(ctx, db, "posts", collections.ListParams{
		Page:  2,
		Limit: 2,
	}, "editor", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("want 2 entries on page 2, got %d", len(page2))
	}
}

func TestListEntries_Sort(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := collections.CreateEntry(ctx, db, "posts", map[string]any{"title": "Post"}, "draft", "editor", blogSchema)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	entries, _, err := collections.ListEntries(ctx, db, "posts", collections.ListParams{
		Sort: "created_at:asc",
	}, "editor", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("list asc: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}

	_, _, err = collections.ListEntries(ctx, db, "posts", collections.ListParams{
		Sort: "injected_field:asc; DROP TABLE--",
	}, "editor", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("invalid sort should not error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Field-level permission filtering
// ---------------------------------------------------------------------------

func TestFieldPermissionFiltering(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	entry, err := collections.CreateEntry(ctx, db, "posts", map[string]any{
		"title":  "Public Post",
		"secret": "top secret",
	}, "draft", "admin", blogSchema)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	adminView, err := collections.GetEntry(ctx, db, "posts", entry.ID, "admin", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("get as admin: %v", err)
	}
	adminData, _ := adminView["data"].(map[string]any)
	if _, ok := adminData["secret"]; !ok {
		t.Fatal("admin should see secret field")
	}

	viewerView, err := collections.GetEntry(ctx, db, "posts", entry.ID, "viewer", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("get as viewer: %v", err)
	}
	viewerData, _ := viewerView["data"].(map[string]any)
	if _, ok := viewerData["secret"]; ok {
		t.Fatal("viewer should not see secret field")
	}
	if viewerData["title"] != "Public Post" {
		t.Fatal("viewer should see title")
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func TestValidation_MissingRequiredField(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	_, err := collections.CreateEntry(ctx, db, "posts", map[string]any{
		"body": "no title here",
	}, "draft", "editor", blogSchema)
	if err == nil {
		t.Fatal("expected validation error")
	}

	var ve *collections.ValidationError
	if !collections.IsValidationError(err, &ve) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if ve.Fields["title"] == "" {
		t.Fatal("expected error for missing required field 'title'")
	}
}

func TestValidation_UnknownFieldsDropped(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	entry, err := collections.CreateEntry(ctx, db, "posts", map[string]any{
		"title":   "Valid",
		"unknown": "this should be dropped",
	}, "draft", "editor", blogSchema)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := collections.GetEntry(ctx, db, "posts", entry.ID, "admin", blogSchema, collections.ReadOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	data, _ := got["data"].(map[string]any)
	if _, ok := data["unknown"]; ok {
		t.Fatal("unknown field should have been dropped")
	}
}

func TestValidation_TypeErrors(t *testing.T) {
	errs := collections.ValidateData(map[string]any{
		"title": 42,
		"views": "x",
	}, blogSchema, false)

	if errs["title"] == "" {
		t.Fatal("expected type error for title")
	}
	if errs["views"] == "" {
		t.Fatal("expected type error for views")
	}
}

// ---------------------------------------------------------------------------
// HTTP handler tests
// ---------------------------------------------------------------------------

func TestHandleList_UnknownSlug(t *testing.T) {
	db := newTestDB(t)
	reg := collections.NewRegistry()

	h := collections.HandleList(db, reg, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/collections/nope", nil)
	req.SetPathValue("slug", "nope")

	rr := serve(h, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestHandleCreate_ValidationError(t *testing.T) {
	db := newTestDB(t)
	reg := collections.NewRegistry()
	reg.Register("posts", blogSchema)

	h := collections.HandleCreate(db, reg, nil)

	tok := signToken(t, "editor")
	req := bearerRequest(http.MethodPost, "/api/collections/posts",
		`{"body":"missing title"}`, tok)
	req.SetPathValue("slug", "posts")

	rr := serveWithAuth(h, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d — body: %s", rr.Code, rr.Body)
	}
}

func TestHandleCreate_ForbiddenRole(t *testing.T) {
	db := newTestDB(t)
	reg := collections.NewRegistry()
	reg.Register("posts", blogSchema)

	h := collections.HandleCreate(db, reg, nil)

	// viewer cannot write
	tok := signToken(t, "viewer")
	req := bearerRequest(http.MethodPost, "/api/collections/posts",
		`{"title":"Hello"}`, tok)
	req.SetPathValue("slug", "posts")

	rr := serveWithAuth(h, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

func TestHandleCreateAndList(t *testing.T) {
	db := newTestDB(t)
	reg := collections.NewRegistry()
	reg.Register("posts", blogSchema)

	createH := collections.HandleCreate(db, reg, nil)
	listH := collections.HandleList(db, reg, nil)

	tok := signToken(t, "editor")

	for i := 0; i < 2; i++ {
		req := bearerRequest(http.MethodPost, "/api/collections/posts",
			`{"data":{"title":"Test Post"}}`, tok)
		req.SetPathValue("slug", "posts")
		rr := serveWithAuth(createH, req)
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d: want 201, got %d — %s", i, rr.Code, rr.Body)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/collections/posts", nil)
	req.SetPathValue("slug", "posts")
	rr := serve(listH, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d — %s", rr.Code, rr.Body)
	}

	body := decodeBody(t, rr)
	total, _ := body["total"].(float64)
	if total != 2 {
		t.Fatalf("want total=2, got %v", total)
	}
}

func TestHandleCreate_ResponseUsesReadFiltering(t *testing.T) {
	db := newTestDB(t)
	reg := collections.NewRegistry()
	reg.Register("private-posts", collections.Schema{
		Access: collections.Access{
			Read:  collections.Role("admin"),
			Write: collections.Public,
		},
		Fields: []collections.Field{
			{Name: "title", Type: collections.TypeText, Required: true},
		},
	})

	h := collections.HandleCreate(db, reg, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/collections/private-posts", strings.NewReader(`{"data":{"title":"Hidden"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", "private-posts")

	rr := serve(h, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d â€” body: %s", rr.Code, rr.Body)
	}

	body := decodeBody(t, rr)
	if _, ok := body["data"]; !ok {
		t.Fatalf("expected filtered entry response, got %v", body)
	}
	data := body["data"].(map[string]any)
	if _, ok := data["title"]; ok {
		t.Fatal("public create response should not expose unreadable fields")
	}
}

func TestHandleGet_NotFound(t *testing.T) {
	db := newTestDB(t)
	reg := collections.NewRegistry()
	reg.Register("posts", blogSchema)

	h := collections.HandleGet(db, reg, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/collections/posts/no-such-id", nil)
	req.SetPathValue("slug", "posts")
	req.SetPathValue("id", "no-such-id")

	rr := serve(h, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestHandleAdminList_RequiresAdmin(t *testing.T) {
	reg := collections.NewRegistry()
	reg.Register("posts", blogSchema)

	h := collections.HandleAdminList(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/collections", nil)
	rr := serve(h, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}

	tok := signToken(t, "viewer")
	req = bearerRequest(http.MethodGet, "/api/admin/collections", "", tok)
	rr = serveWithAuth(h, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer: want 403, got %d", rr.Code)
	}

	tok = signToken(t, "admin")
	req = bearerRequest(http.MethodGet, "/api/admin/collections", "", tok)
	rr = serveWithAuth(h, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin: want 200, got %d — %s", rr.Code, rr.Body)
	}
}

func TestHandleAdminMetadata_RequiresAdminAndReturnsStableShape(t *testing.T) {
	reg := collections.NewRegistry()
	reg.Register("z-posts", collections.Schema{
		Access: collections.Access{
			Read:   collections.Role("viewer"),
			Write:  collections.Role("editor"),
			Delete: collections.Role("admin"),
		},
		Fields: []collections.Field{
			{Name: "title", Type: collections.TypeText, Required: true},
			{Name: "status", Type: collections.TypeSelect, Options: []string{"draft", "published"}},
			{Name: "author", Type: collections.TypeRelation, Collection: "users", TargetSlug: "users"},
			{Name: "related_posts", Type: collections.TypeJoin, Collection: "posts", JoinField: "post_id"},
			{Name: "password", Type: collections.TypePassword},
			{
				Name: "tags",
				Type: collections.TypeArray,
				ItemSchema: &collections.Field{
					Name:     "tag",
					Type:     collections.TypeText,
					Required: true,
				},
			},
			{
				Name: "localized_title",
				Type: collections.TypeLocale,
				WrappedType: &collections.Field{
					Name:     "value",
					Type:     collections.TypeText,
					Required: true,
				},
			},
		},
		SlugSource: "title",
		Versioned:  true,
	})
	reg.Register("a-authors", collections.Schema{
		Access: collections.Access{
			Read: collections.Public,
		},
		Fields: []collections.Field{
			{Name: "name", Type: collections.TypeText, Required: true},
		},
	})

	h := collections.HandleAdminMetadata(reg)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/collections/metadata", nil)
	rr := serve(h, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}

	tok := signToken(t, "viewer")
	req = bearerRequest(http.MethodGet, "/api/admin/collections/metadata", "", tok)
	rr = serveWithAuth(h, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer: want 403, got %d", rr.Code)
	}

	tok = signToken(t, "admin")
	req = bearerRequest(http.MethodGet, "/api/admin/collections/metadata", "", tok)
	rr = serveWithAuth(h, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin: want 200, got %d — %s", rr.Code, rr.Body)
	}

	body := decodeBody(t, rr)
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("expected data array, got %T", body["data"])
	}
	if len(data) != 2 {
		t.Fatalf("want 2 collections, got %d", len(data))
	}

	first, _ := data[0].(map[string]any)
	second, _ := data[1].(map[string]any)
	if first["slug"] != "a-authors" || second["slug"] != "z-posts" {
		t.Fatalf("collections should be sorted by slug, got %v then %v", first["slug"], second["slug"])
	}

	if second["slug_source"] != "title" {
		t.Fatalf("want slug_source=title, got %v", second["slug_source"])
	}
	if second["versioned"] != true {
		t.Fatalf("want versioned=true, got %v", second["versioned"])
	}

	access, ok := second["access"].(map[string]any)
	if !ok {
		t.Fatalf("expected access object, got %T", second["access"])
	}
	read, _ := access["read"].(map[string]any)
	write, _ := access["write"].(map[string]any)
	deleteRule, _ := access["delete"].(map[string]any)
	if read["min_role"] != "viewer" || write["min_role"] != "editor" || deleteRule["min_role"] != "admin" {
		t.Fatalf("unexpected access metadata: %+v", access)
	}

	fields, ok := second["fields"].([]any)
	if !ok {
		t.Fatalf("expected fields array, got %T", second["fields"])
	}
	fieldsByName := make(map[string]map[string]any, len(fields))
	for _, field := range fields {
		m, ok := field.(map[string]any)
		if !ok {
			t.Fatalf("expected field object, got %T", field)
		}
		name, _ := m["name"].(string)
		fieldsByName[name] = m
	}

	if fieldsByName["title"]["type"] != "text" || fieldsByName["title"]["required"] != true {
		t.Fatalf("title metadata mismatch: %+v", fieldsByName["title"])
	}

	statusOptions, ok := fieldsByName["status"]["options"].([]any)
	if !ok || len(statusOptions) != 2 || statusOptions[1] != "published" {
		t.Fatalf("status options mismatch: %+v", fieldsByName["status"]["options"])
	}

	if fieldsByName["author"]["collection"] != "users" || fieldsByName["author"]["target_slug"] != "users" {
		t.Fatalf("author relation metadata mismatch: %+v", fieldsByName["author"])
	}

	if fieldsByName["related_posts"]["read_only"] != true || fieldsByName["related_posts"]["join_field"] != "post_id" {
		t.Fatalf("join metadata mismatch: %+v", fieldsByName["related_posts"])
	}

	if fieldsByName["password"]["write_only"] != true {
		t.Fatalf("password metadata should be write_only: %+v", fieldsByName["password"])
	}

	itemSchema, ok := fieldsByName["tags"]["item_schema"].(map[string]any)
	if !ok || itemSchema["name"] != "tag" || itemSchema["type"] != "text" || itemSchema["required"] != true {
		t.Fatalf("array item schema mismatch: %+v", fieldsByName["tags"]["item_schema"])
	}

	wrappedType, ok := fieldsByName["localized_title"]["wrapped_type"].(map[string]any)
	if !ok || wrappedType["name"] != "value" || wrappedType["type"] != "text" || wrappedType["required"] != true {
		t.Fatalf("wrapped type mismatch: %+v", fieldsByName["localized_title"]["wrapped_type"])
	}
}
