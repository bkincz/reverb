package forms

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
	"modernc.org/sqlite"

	reverbdb "github.com/bkincz/reverb/db"
	dbmodels "github.com/bkincz/reverb/db/models"
)

func newTestDB(t *testing.T) *bun.DB {
	t.Helper()

	driverName := "sqlite_forms_test_" + t.Name()
	sql.Register(driverName, &sqlite.Driver{})

	sqlDB, err := sql.Open(driverName, "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	if err := reverbdb.Migrate(context.Background(), bunDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	t.Cleanup(func() { _ = bunDB.Close() })
	return bunDB
}

func seedForm(t *testing.T, db *bun.DB, slug string, schema Schema) {
	t.Helper()

	rawSchema, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}

	row := &dbmodels.FormDefinition{
		ID:        "form-" + slug,
		Slug:      slug,
		Name:      slug,
		Schema:    rawSchema,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(row).Exec(context.Background()); err != nil {
		t.Fatalf("insert form definition: %v", err)
	}
}

func countSubmissions(t *testing.T, db *bun.DB) int {
	t.Helper()

	n, err := db.NewSelect().Model((*dbmodels.FormSubmission)(nil)).Count(context.Background())
	if err != nil {
		t.Fatalf("count submissions: %v", err)
	}
	return n
}

func TestHandleSubmit_HoneypotDropsSubmission(t *testing.T) {
	db := newTestDB(t)
	reg := NewRegistry()
	schema := Schema{
		HoneypotField: "company",
		Fields: []Field{
			{Name: "email", Type: FieldTypeEmail, Required: true},
		},
	}
	reg.Register("contact", schema)
	seedForm(t, db, "contact", schema)

	handler := HandleSubmit(db, reg)
	req := httptest.NewRequest(http.MethodPost, "/api/forms/contact", strings.NewReader(`{"email":"alice@example.com","company":"spam"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", "contact")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	if got := countSubmissions(t, db); got != 0 {
		t.Fatalf("submission count = %d, want 0", got)
	}
}

func TestHandleSubmit_StoresResolvedClientIP(t *testing.T) {
	db := newTestDB(t)
	reg := NewRegistry()
	schema := Schema{
		Fields: []Field{
			{Name: "email", Type: FieldTypeEmail, Required: true},
		},
	}
	reg.Register("contact", schema)
	seedForm(t, db, "contact", schema)

	handler := HandleSubmit(db, reg, func(*http.Request) string { return "198.51.100.3" })
	req := httptest.NewRequest(http.MethodPost, "/api/forms/contact", strings.NewReader(`{"email":"alice@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "forms-test")
	req.SetPathValue("slug", "contact")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}

	var sub dbmodels.FormSubmission
	if err := db.NewSelect().Model(&sub).Limit(1).Scan(context.Background()); err != nil {
		t.Fatalf("select submission: %v", err)
	}

	var metadata map[string]any
	if err := json.Unmarshal(sub.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if got := metadata["ip"]; got != "198.51.100.3" {
		t.Fatalf("metadata ip = %v, want %q", got, "198.51.100.3")
	}
}

func TestValidateSubmission_RejectsDisplayNameEmail(t *testing.T) {
	errs := validateSubmission(map[string]any{
		"email": "Alice <alice@example.com>",
	}, Schema{
		Fields: []Field{
			{Name: "email", Type: FieldTypeEmail, Required: true},
		},
	})

	if errs["email"] == "" {
		t.Fatal("expected display-name email to be rejected")
	}
}
