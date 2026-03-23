// Package sqlite provides a Reverb database adapter for SQLite.
// It uses modernc.org/sqlite, a pure-Go driver that requires no CGO.
//
// Usage:
//
//	reverb.New(reverb.Config{
//	    DB: sqlite.New("./data.db"),
//	})
package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

type Adapter struct {
	DSN string
}

func New(dsn string) *Adapter {
	return &Adapter{DSN: dsn}
}

func (a *Adapter) Open() (*bun.DB, error) {
	sqldb, err := sql.Open("sqlite", a.DSN)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", a.DSN, err)
	}
	sqldb.SetMaxOpenConns(1)
	return bun.NewDB(sqldb, sqlitedialect.New()), nil
}
