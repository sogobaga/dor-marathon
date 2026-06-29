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
	UserID      string
	Source      string
	ExternalID  string
	Fingerprint string // 精確指紋（起始秒|距離公尺|移動秒）
	DistanceKm  float64
	DurationS   int
	AvgPaceS    int
	AscentM     *float64
	AvgHR       *int
	RecordedAt  time.Time
}

// ImportResult 匯入結果
type ImportResult struct {
	Status string // inserted | exists | duplicate
	Reason string // flagged 原因（duplicate 時）
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

// detectDuplicate 回傳 (flagged, reason, dupOfID)。
// 1) 跨帳號精確指紋相同 → cross_account_duplicate（同帳號則 duplicate）
// 2) 同帳號時間區間重疊 → multi_device_duplicate（多裝置同一筆活動）
func (r *Repository) detectDuplicate(ctx context.Context, a *NormalizedActivity) (bool, string, string) {
	if a.Fingerprint != "" {
		var id, uid string
		err := r.db.QueryRow(ctx,
			`SELECT id::text, user_id::text FROM activities WHERE fingerprint=$1 AND NOT flagged LIMIT 1`,
			a.Fingerprint).Scan(&id, &uid)
		if err == nil {
			reason := "cross_account_duplicate"
			if uid == a.UserID {
				reason = "duplicate"
			}
			return true, reason, id
		}
	}
	// 同帳號時間重疊（多裝置）。新活動區間 [start, start+dur]；限 24h 窗加速。
	start := a.RecordedAt
	end := a.RecordedAt.Add(time.Duration(a.DurationS) * time.Second)
	var id string
	err := r.db.QueryRow(ctx, `
		SELECT id::text FROM activities
		WHERE user_id=$1 AND NOT flagged
		  AND recorded_at >= $2 AND recorded_at <= $3
		  AND (recorded_at + (duration_s || ' seconds')::interval) >= $4
		LIMIT 1`,
		a.UserID, start.Add(-24*time.Hour), end, start).Scan(&id)
	if err == nil {
		return true, "multi_device_duplicate", id
	}
	return false, "", ""
}

// ImportActivity 寫入活動：source+external_id 去重；偵測重複/跨帳號洗資料 → flag 且不計入賽事。
func (r *Repository) ImportActivity(ctx context.Context, a *NormalizedActivity) (ImportResult, error) {
	flagged, reason, dupOf := r.detectDuplicate(ctx, a)

	var raceArg, dupArg, reasonArg interface{}
	if flagged {
		reasonArg = reason
		dupArg = dupOf
		// flagged → race_id 留 NULL，不計入賽事
	} else {
		if raceID, ok, err := r.FindRegisteredRace(ctx, a.UserID, a.RecordedAt); err != nil {
			return ImportResult{}, err
		} else if ok {
			raceArg = raceID
		}
	}

	tag, err := r.db.Exec(ctx, `
		INSERT INTO activities
			(user_id, race_id, distance_km, duration_s, avg_pace_s, ascent_m, avg_hr, recorded_at,
			 processed, source, external_id, fingerprint, flagged, flag_reason, dup_of)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (source, external_id) DO NOTHING`,
		a.UserID, raceArg, a.DistanceKm, a.DurationS, a.AvgPaceS, a.AscentM, a.AvgHR, a.RecordedAt,
		a.Source, a.ExternalID, a.Fingerprint, flagged, reasonArg, dupArg)
	if err != nil {
		return ImportResult{}, fmt.Errorf("insert activity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ImportResult{Status: "exists"}, nil
	}
	if flagged {
		return ImportResult{Status: "duplicate", Reason: reason}, nil
	}
	return ImportResult{Status: "inserted"}, nil
}

// ActivityRow 個人活動清單單筆
type ActivityRow struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	DistanceKm float64   `json:"distance_km"`
	DurationS  int       `json:"duration_s"`
	AvgPaceS   int       `json:"avg_pace_s"`
	AscentM    *float64  `json:"ascent_m,omitempty"`
	AvgHR      *int      `json:"avg_hr,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
	RaceTitle  string    `json:"race_title,omitempty"`
	Flagged    bool      `json:"flagged"`
	FlagReason string    `json:"flag_reason,omitempty"`
}

// ListActivities 取得使用者活動（最新 N 筆，含賽事名稱與 flagged 狀態）
func (r *Repository) ListActivities(ctx context.Context, userID string, limit int) ([]ActivityRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := r.db.Query(ctx, `
		SELECT a.id::text, COALESCE(a.source,'manual'), a.distance_km, a.duration_s, a.avg_pace_s,
		       a.ascent_m, a.avg_hr, a.recorded_at, COALESCE(r.title,''), a.flagged, COALESCE(a.flag_reason,'')
		FROM activities a LEFT JOIN races r ON r.id = a.race_id
		WHERE a.user_id=$1
		ORDER BY a.recorded_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	defer rows.Close()
	out := []ActivityRow{}
	for rows.Next() {
		var a ActivityRow
		if err := rows.Scan(&a.ID, &a.Source, &a.DistanceKm, &a.DurationS, &a.AvgPaceS,
			&a.AscentM, &a.AvgHR, &a.RecordedAt, &a.RaceTitle, &a.Flagged, &a.FlagReason); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
