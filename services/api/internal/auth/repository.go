package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID           string  `db:"id"`
	Email        string  `db:"email"`
	Handle       string  `db:"handle"`
	Name         string  `db:"name"`
	PasswordHash string  `db:"password_hash"`
	AvatarURL    string  `db:"avatar_url"`
	TotalKm      float64 `db:"total_km"`
	Role         string  `db:"role"` // user | organizer | admin
}

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, email, handle, name, hash, role string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		INSERT INTO users (email, handle, name, password_hash, role)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, email, handle, name, password_hash,
		          COALESCE(avatar_url, '') as avatar_url, total_km, role
	`, email, handle, name, hash, role).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

func (r *Repository) FindByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, handle, name, password_hash,
		       COALESCE(avatar_url, '') as avatar_url, total_km, role
		FROM users WHERE email = $1
	`, email).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}
	return u, nil
}

func (r *Repository) FindByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(ctx, `
		SELECT id, email, handle, name, password_hash,
		       COALESCE(avatar_url, '') as avatar_url, total_km, role
		FROM users WHERE id = $1
	`, id).Scan(
		&u.ID, &u.Email, &u.Handle, &u.Name, &u.PasswordHash, &u.AvatarURL, &u.TotalKm, &u.Role,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

func (r *Repository) EmailExists(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists)
	return exists, err
}

func (r *Repository) HandleExists(ctx context.Context, handle string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE handle=$1)`, handle).Scan(&exists)
	return exists, err
}
