// Package integration 第三方運動數據整合（OAuth token 儲存 + 活動匯入）。
package integration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// Connection 使用者對某 provider 的連線
type Connection struct {
	ID             string
	UserID         string
	Provider       string
	ProviderUserID string
	AccessToken    string
	RefreshToken   string
	ExpiresAt      time.Time
	Scope          string
	AthleteName    string
}

// Save upsert（依 user_id+provider）
func (r *Repository) Save(ctx context.Context, c *Connection) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_integrations
			(user_id, provider, provider_user_id, access_token, refresh_token, expires_at, scope, athlete_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id, provider) DO UPDATE SET
			provider_user_id = EXCLUDED.provider_user_id,
			access_token     = EXCLUDED.access_token,
			refresh_token    = EXCLUDED.refresh_token,
			expires_at       = EXCLUDED.expires_at,
			scope            = EXCLUDED.scope,
			athlete_name     = EXCLUDED.athlete_name,
			updated_at       = NOW()`,
		c.UserID, c.Provider, c.ProviderUserID, c.AccessToken, c.RefreshToken, c.ExpiresAt, c.Scope, c.AthleteName)
	return err
}

func scanConn(row pgx.Row) (*Connection, error) {
	c := &Connection{}
	err := row.Scan(&c.ID, &c.UserID, &c.Provider, &c.ProviderUserID,
		&c.AccessToken, &c.RefreshToken, &c.ExpiresAt, &c.Scope, &c.AthleteName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

const connCols = `SELECT id, user_id, provider, provider_user_id, access_token, refresh_token,
	expires_at, COALESCE(scope,''), COALESCE(athlete_name,'') FROM user_integrations`

func (r *Repository) GetByUser(ctx context.Context, userID, provider string) (*Connection, error) {
	return scanConn(r.db.QueryRow(ctx, connCols+` WHERE user_id=$1 AND provider=$2`, userID, provider))
}

func (r *Repository) GetByProviderUser(ctx context.Context, provider, providerUserID string) (*Connection, error) {
	return scanConn(r.db.QueryRow(ctx, connCols+` WHERE provider=$1 AND provider_user_id=$2`, provider, providerUserID))
}

func (r *Repository) UpdateTokens(ctx context.Context, id, access, refresh string, expiresAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE user_integrations SET access_token=$1, refresh_token=$2, expires_at=$3, updated_at=NOW() WHERE id=$4`,
		access, refresh, expiresAt, id)
	return err
}

func (r *Repository) Delete(ctx context.Context, userID, provider string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_integrations WHERE user_id=$1 AND provider=$2`, userID, provider)
	return err
}

// NormalizedActivity 各 provider 正規化後的活動
type NormalizedActivity struct {
	UserID     string
	Source     string
	ExternalID string
	DistanceKm float64
	DurationS  int
	AvgPaceS   int
	AscentM    *float64
	AvgHR      *int
	RecordedAt time.Time
}

// FindRegisteredRace 找出 recordedAt 落在賽事期間、且該使用者有報名的賽事（取最近一場）
func (r *Repository) FindRegisteredRace(ctx context.Context, userID string, recordedAt time.Time) (string, bool, error) {
	var raceID string
	err := r.db.QueryRow(ctx, `
		SELECT r.id::text FROM races r
		JOIN registrations reg ON reg.race_id = r.id
		WHERE reg.user_id = $1 AND reg.status <> 'cancelled'
		  AND $2 BETWEEN r.start_date AND r.end_date
		ORDER BY r.start_date DESC
		LIMIT 1`, userID, recordedAt).Scan(&raceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return raceID, true, nil
}

// ImportActivity 寫入活動（依 source+external_id 去重），自動關聯報名中的賽事。回傳是否新插入。
func (r *Repository) ImportActivity(ctx context.Context, a *NormalizedActivity) (bool, error) {
	raceID, ok, err := r.FindRegisteredRace(ctx, a.UserID, a.RecordedAt)
	if err != nil {
		return false, err
	}
	var raceArg interface{}
	if ok {
		raceArg = raceID
	}
	tag, err := r.db.Exec(ctx, `
		INSERT INTO activities
			(user_id, race_id, distance_km, duration_s, avg_pace_s, ascent_m, avg_hr, recorded_at, processed, source, external_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,$9,$10)
		ON CONFLICT (source, external_id) DO NOTHING`,
		a.UserID, raceArg, a.DistanceKm, a.DurationS, a.AvgPaceS, a.AscentM, a.AvgHR,
		a.RecordedAt, a.Source, a.ExternalID)
	if err != nil {
		return false, fmt.Errorf("insert activity: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
