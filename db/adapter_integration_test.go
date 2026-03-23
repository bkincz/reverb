package db_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	reverbdb "github.com/bkincz/reverb/db"
	dbmodels "github.com/bkincz/reverb/db/models"
	dbmysql "github.com/bkincz/reverb/db/mysql"
	dbpostgres "github.com/bkincz/reverb/db/postgres"
	dbsqlite "github.com/bkincz/reverb/db/sqlite"
)

func TestSQLiteAdapter_Contract(t *testing.T) {
	runAdapterContract(t, "sqlite", dbsqlite.New("file:reverb_adapter_contract?mode=memory&cache=shared"))
}

func TestPostgresAdapter_Contract(t *testing.T) {
	dsn := os.Getenv("REVERB_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set REVERB_TEST_POSTGRES_DSN to run PostgreSQL adapter integration tests")
	}
	runAdapterContract(t, "postgres", dbpostgres.New(dsn))
}

func TestMySQLAdapter_Contract(t *testing.T) {
	dsn := os.Getenv("REVERB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set REVERB_TEST_MYSQL_DSN to run MySQL adapter integration tests")
	}
	runAdapterContract(t, "mysql", dbmysql.New(dsn))
}

func runAdapterContract(t *testing.T, name string, adapter reverbdb.Adapter) {
	t.Helper()

	bunDB, err := adapter.Open()
	if err != nil {
		t.Fatalf("%s open: %v", name, err)
	}
	defer bunDB.Close()

	ctx := context.Background()
	if err := bunDB.PingContext(ctx); err != nil {
		t.Fatalf("%s ping: %v", name, err)
	}

	if err := reverbdb.Migrate(ctx, bunDB); err != nil {
		t.Fatalf("%s migrate: %v", name, err)
	}
	if err := reverbdb.Migrate(ctx, bunDB); err != nil {
		t.Fatalf("%s second migrate: %v", name, err)
	}

	id := uuid.NewString()
	slug := "adapter-" + id[:8]
	row := &dbmodels.FormDefinition{
		ID:        id,
		Slug:      slug,
		Name:      "Adapter Contract",
		Schema:    json.RawMessage(`{"fields":[]}`),
		CreatedAt: time.Now().UTC(),
	}

	if _, err := bunDB.NewInsert().Model(row).Exec(ctx); err != nil {
		t.Fatalf("%s insert form definition: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = bunDB.NewDelete().Model((*dbmodels.FormDefinition)(nil)).Where("id = ?", id).Exec(context.Background())
	})

	var got dbmodels.FormDefinition
	if err := bunDB.NewSelect().Model(&got).Where("slug = ?", slug).Limit(1).Scan(ctx); err != nil {
		t.Fatalf("%s select form definition: %v", name, err)
	}
	if got.Name != row.Name {
		t.Fatalf("%s selected name = %q, want %q", name, got.Name, row.Name)
	}
}
