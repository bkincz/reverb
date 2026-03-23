package main

import (
	"fmt"
	"os"

	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/db/mysql"
	"github.com/bkincz/reverb/db/postgres"
	"github.com/bkincz/reverb/db/sqlite"
)

// ---------------------------------------------------------------------------
// DB
// ---------------------------------------------------------------------------

func openDB(driver, dsn string) (*bun.DB, error) {
	switch driver {
	case "sqlite":
		return sqlite.New(dsn).Open()
	case "postgres":
		return postgres.New(dsn).Open()
	case "mysql":
		return mysql.New(dsn).Open()
	default:
		return nil, fmt.Errorf("openDB: unknown driver %q (want sqlite, postgres, or mysql)", driver)
	}
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "reverb: "+format+"\n", args...)
	os.Exit(1)
}
