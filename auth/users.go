package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"

	"github.com/bkincz/reverb/db"
)

// ---------------------------------------------------------------------------
// Password
// ---------------------------------------------------------------------------

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	return string(hash), nil
}

func CheckPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

func CreateUser(ctx context.Context, bunDB *bun.DB, email, password, role string) (*db.User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	user := &db.User{
		ID:           uuid.New().String(),
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if _, err = bunDB.NewInsert().Model(user).Exec(ctx); err != nil {
		return nil, fmt.Errorf("auth: create user: %w", err)
	}
	return user, nil
}

func FindUserByEmail(ctx context.Context, bunDB *bun.DB, email string) (*db.User, error) {
	user := new(db.User)
	err := bunDB.NewSelect().Model(user).Where("email = ?", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: find user by email: %w", err)
	}
	return user, nil
}

func FindUserByID(ctx context.Context, bunDB *bun.DB, id string) (*db.User, error) {
	user := new(db.User)
	err := bunDB.NewSelect().Model(user).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: find user by id: %w", err)
	}
	return user, nil
}
