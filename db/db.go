package db

import "github.com/uptrace/bun"

type Adapter interface {
	Open() (*bun.DB, error)
}
