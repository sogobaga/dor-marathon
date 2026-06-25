package reward

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CountSpinsToday 查詢今日已抽獎次數
func (r *Repository) CountSpinsToday(ctx context.Context, userID, raceID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM wheel_spins
		WHERE user_id=$1 AND race_id=$2 AND spun_at::date = CURRENT_DATE
	`, userID, raceID).Scan(&n)
	return n, err
}

// CountSpinsAll 查詢在某賽事的總抽獎次數
func (r *Repository) CountSpinsAll(ctx context.Context, userID, raceID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM wheel_spins WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(&n)
	return n, err
}

// RecordSpin 記錄抽獎結果
func (r *Repository) RecordSpin(ctx context.Context, userID, raceID, resultID, resultKind string, amount int) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO wheel_spins (user_id, race_id, result_id, result_kind, result_amount)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, raceID, resultID, resultKind, amount)
	return err
}

// CountCompletedMissions 查詢已完成任務數（決定可用抽獎次數）
func (r *Repository) CountCompletedMissions(ctx context.Context, userID, raceID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM mission_completions WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(&n)
	return n, err
}

// GetStickers 取得使用者在某賽事的集點貼紙
func (r *Repository) GetStickers(ctx context.Context, userID, raceID string) (map[int]bool, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sticker_no FROM user_stickers WHERE user_id=$1 AND race_id=$2
	`, userID, raceID)
	if err != nil {
		return nil, fmt.Errorf("get stickers: %w", err)
	}
	defer rows.Close()

	owned := map[int]bool{}
	for rows.Next() {
		var no int
		if err := rows.Scan(&no); err != nil {
			return nil, err
		}
		owned[no] = true
	}
	return owned, rows.Err()
}

// GrantSticker 授予貼紙（若已有則略過）
func (r *Repository) GrantSticker(ctx context.Context, userID, raceID string, no int) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO user_stickers (user_id, sticker_no, race_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, userID, no, raceID)
	return err
}

// CountOwnedStickers 取得已收集貼紙數
func (r *Repository) CountOwnedStickers(ctx context.Context, userID, raceID string) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM user_stickers WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(&n)
	return n, err
}
