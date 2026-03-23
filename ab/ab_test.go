package ab_test

import (
	"bytes"
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

	"github.com/bkincz/reverb/ab"
	dbmodels "github.com/bkincz/reverb/db/models"
)

var abDriverSeq int

func newTestDB(t *testing.T) *bun.DB {
	t.Helper()
	abDriverSeq++
	name := "sqlite3_ab_test_" + time.Now().Format("150405.000000") + "_" + string(rune(abDriverSeq+'a'))
	sql.Register(name, &sqlite.Driver{})
	sqlDB, err := sql.Open(name, "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())
	ctx := context.Background()
	models := []any{
		(*dbmodels.ABTest)(nil),
		(*dbmodels.ABTestAssignment)(nil),
		(*dbmodels.ABConversionEvent)(nil),
	}
	for _, m := range models {
		if _, err := bunDB.NewCreateTable().Model(m).IfNotExists().Exec(ctx); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	t.Cleanup(func() { _ = bunDB.Close() })
	return bunDB
}

func TestValidateVariants_RejectsInvalidWeightSum(t *testing.T) {
	err := ab.ValidateVariants([]ab.Variant{
		{ID: "a", Name: "A", WeightPercent: 60},
		{ID: "b", Name: "B", WeightPercent: 20},
	})
	if err == nil {
		t.Fatal("expected invalid weight sum to fail")
	}
}

func TestHandleAdminCreate_RejectsInvalidVariants(t *testing.T) {
	db := newTestDB(t)

	body := `{
		"name":"Homepage",
		"slug":"homepage",
		"variants":[
			{"id":"a","name":"A","weight_percent":60},
			{"id":"b","name":"B","weight_percent":20}
		],
		"active":true
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/ab", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	ab.HandleAdminCreate(db).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d; body: %s", rr.Code, rr.Body.String())
	}
}

func TestAssignVariant_RejectsPersistedInvalidConfig(t *testing.T) {
	db := newTestDB(t)

	variants, err := json.Marshal([]map[string]any{
		{"id": "a", "name": "A", "weight_percent": 70},
		{"id": "a", "name": "B", "weight_percent": 30},
	})
	if err != nil {
		t.Fatalf("marshal variants: %v", err)
	}

	row := &dbmodels.ABTest{
		ID:        "test-1",
		Name:      "Homepage",
		Slug:      "homepage",
		Variants:  variants,
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(row).Exec(context.Background()); err != nil {
		t.Fatalf("insert AB test: %v", err)
	}

	_, err = ab.AssignVariant(context.Background(), db, "homepage", "visitor-1")
	if err == nil {
		t.Fatal("expected invalid persisted AB config to fail assignment")
	}
}

func TestHandleAdminUpdate_RejectsInvalidVariants(t *testing.T) {
	db := newTestDB(t)

	initialVariants, err := json.Marshal([]map[string]any{
		{"id": "a", "name": "A", "weight_percent": 50},
		{"id": "b", "name": "B", "weight_percent": 50},
	})
	if err != nil {
		t.Fatalf("marshal initial variants: %v", err)
	}

	row := &dbmodels.ABTest{
		ID:        "test-1",
		Name:      "Homepage",
		Slug:      "homepage",
		Variants:  initialVariants,
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(row).Exec(context.Background()); err != nil {
		t.Fatalf("insert AB test: %v", err)
	}

	payload := map[string]any{
		"variants": []map[string]any{
			{"id": "a", "name": "A", "weight_percent": 100},
			{"id": "a", "name": "B", "weight_percent": 0},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/admin/ab/homepage", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("slug", "homepage")
	rr := httptest.NewRecorder()

	ab.HandleAdminUpdate(db).ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d; body: %s", rr.Code, rr.Body.String())
	}
}
