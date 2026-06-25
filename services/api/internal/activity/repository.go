package activity

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// ListByUser 取得使用者的活動記錄（最新 N 筆）
func (r *Repository) ListByUser(ctx context.Context, userID string, limit int) ([]*Activity, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, COALESCE(race_id::text,'') as race_id,
		       COALESCE(mission_day,0) as mission_day,
		       distance_km, duration_s, avg_pace_s, recorded_at, created_at
		FROM activities
		WHERE user_id = $1
		ORDER BY recorded_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	defer rows.Close()

	var acts []*Activity
	for rows.Next() {
		a := &Activity{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.RaceID, &a.MissionDay,
			&a.DistanceKm, &a.DurationS, &a.AvgPaceS, &a.RecordedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		acts = append(acts, a)
	}
	return acts, rows.Err()
}

// ListByRace 取得某賽事中使用者的所有活動
func (r *Repository) ListByRace(ctx context.Context, userID, raceID string) ([]*Activity, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, user_id, COALESCE(race_id::text,'') as race_id,
		       COALESCE(mission_day,0) as mission_day,
		       distance_km, duration_s, avg_pace_s, recorded_at, created_at
		FROM activities
		WHERE user_id = $1 AND race_id = $2
		ORDER BY recorded_at ASC
	`, userID, raceID)
	if err != nil {
		return nil, fmt.Errorf("list race activities: %w", err)
	}
	defer rows.Close()

	var acts []*Activity
	for rows.Next() {
		a := &Activity{}
		if err := rows.Scan(&a.ID, &a.UserID, &a.RaceID, &a.MissionDay,
			&a.DistanceKm, &a.DurationS, &a.AvgPaceS, &a.RecordedAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		acts = append(acts, a)
	}
	return acts, rows.Err()
}

// TotalKmInRace 查詢使用者在某賽事的累積里程
func (r *Repository) TotalKmInRace(ctx context.Context, userID, raceID string) (float64, error) {
	var total float64
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(distance_km), 0)
		FROM activities WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(&total)
	return total, err
}

// IsMissionDone 檢查某日任務是否已完成
func (r *Repository) IsMissionDone(ctx context.Context, userID, raceID string, day int) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM mission_completions WHERE user_id=$1 AND race_id=$2 AND day=$3)
	`, userID, raceID, day).Scan(&exists)
	return exists, err
}

// RecordMissionCompletion 記錄任務完成
func (r *Repository) RecordMissionCompletion(ctx context.Context, userID, raceID string, day int, activityID string, rescueCount int) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO mission_completions (user_id, race_id, day, activity_id, rescue_count)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, race_id, day) DO NOTHING
	`, userID, raceID, day, activityID, rescueCount)
	return err
}

// GetMissionCompletions 取得使用者在某賽事的任務完成紀錄
func (r *Repository) GetMissionCompletions(ctx context.Context, userID, raceID string) (map[int]int, error) {
	rows, err := r.db.Query(ctx, `
		SELECT day, rescue_count FROM mission_completions WHERE user_id=$1 AND race_id=$2
	`, userID, raceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[int]int{}
	for rows.Next() {
		var day, rescue int
		if err := rows.Scan(&day, &rescue); err != nil {
			return nil, err
		}
		result[day] = rescue
	}
	return result, rows.Err()
}

// insertActivity 直接寫入 DB（Worker 用）
func (r *Repository) Insert(ctx context.Context, a *Activity) (string, error) {
	var raceID interface{}
	if a.RaceID != "" {
		raceID = a.RaceID
	}
	var missionDay interface{}
	if a.MissionDay > 0 {
		missionDay = a.MissionDay
	}

	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO activities (user_id, race_id, mission_day, distance_km, duration_s, avg_pace_s, recorded_at, processed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
		RETURNING id
	`, a.UserID, raceID, missionDay, a.DistanceKm, a.DurationS, a.AvgPaceS,
		a.RecordedAt.Format(time.RFC3339)).Scan(&id)
	return id, err
}
