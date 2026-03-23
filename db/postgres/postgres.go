// Package postgres provides a Reverb database adapter for PostgreSQL.
//
// Usage:
//
//	reverb.New(reverb.Config{
//	    DB: postgres.New("postgres://user:pass@localhost:5432/mydb?sslmode=disable"),
//	})
package postgres

import (
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Adapter struct {
	DSN string
}

func New(dsn string) *Adapter {
	return &Adapter{DSN: dsn}
}

func (a *Adapter) Open() (*bun.DB, error) {
	sqldb, err := sql.Open("pgx", a.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	return bun.NewDB(sqldb, pgdialect.New()), nil
}
