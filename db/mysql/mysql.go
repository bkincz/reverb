// Package mysql provides a Reverb database adapter for MySQL and MariaDB.
//
// Usage:
//
//	reverb.New(reverb.Config{
//	    DB: mysql.New("user:pass@tcp(localhost:3306)/mydb?parseTime=true"),
//	})
//
// Note: include parseTime=true in your DSN so that time.Time fields are
// scanned correctly.
package mysql

import (
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
	_ "github.com/go-sql-driver/mysql"
)

type Adapter struct {
	DSN string
}

func New(dsn string) *Adapter {
	return &Adapter{DSN: dsn}
}

func (a *Adapter) Open() (*bun.DB, error) {
	sqldb, err := sql.Open("mysql", a.DSN)
	if err != nil {
		return nil, fmt.Errorf("mysql: open: %w", err)
	}
	return bun.NewDB(sqldb, mysqldialect.New()), nil
}
