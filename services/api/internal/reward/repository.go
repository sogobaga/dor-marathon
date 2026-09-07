package reward

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// pgExecutor 是 *pgxpool.Pool 與 pgx.Tx 的共同子集（Query/QueryRow/Exec），讓 Spin 的「查剩餘次數→
// 寫入抽獎紀錄」可以在同一個交易＋advisory lock 內執行、複用既有以連線池為對象的查詢邏輯，比照
// internal/race/leaderboard.go 的 pgQueryer（那邊只需要 Query/QueryRow，這裡 RecordSpin 還需要 Exec）。
type pgExecutor interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
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
	return countSpinsAllWith(ctx, r.db, userID, raceID)
}

// CountSpinsAllTx 與 CountSpinsAll 相同查詢，但在呼叫端已開好的交易內執行——Spin 需要在
// pg_advisory_xact_lock 之後、同一交易內重新計數，避免與其他併發請求讀到同一份舊快照（見
// service.go Spin 上方註解／SEC 修補 M5）。
func (r *Repository) CountSpinsAllTx(ctx context.Context, tx pgx.Tx, userID, raceID string) (int, error) {
	return countSpinsAllWith(ctx, tx, userID, raceID)
}

func countSpinsAllWith(ctx context.Context, db pgExecutor, userID, raceID string) (int, error) {
	var n int
	err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM wheel_spins WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(&n)
	return n, err
}

// RecordSpin 記錄抽獎結果
func (r *Repository) RecordSpin(ctx context.Context, userID, raceID, resultID, resultKind string, amount int) error {
	return recordSpinWith(ctx, r.db, userID, raceID, resultID, resultKind, amount)
}

// RecordSpinTx 與 RecordSpin 相同寫入，但在呼叫端已開好的交易內執行（見 CountSpinsAllTx 註解）。
func (r *Repository) RecordSpinTx(ctx context.Context, tx pgx.Tx, userID, raceID, resultID, resultKind string, amount int) error {
	return recordSpinWith(ctx, tx, userID, raceID, resultID, resultKind, amount)
}

func recordSpinWith(ctx context.Context, db pgExecutor, userID, raceID, resultID, resultKind string, amount int) error {
	_, err := db.Exec(ctx, `
		INSERT INTO wheel_spins (user_id, race_id, result_id, result_kind, result_amount)
		VALUES ($1, $2, $3, $4, $5)
	`, userID, raceID, resultID, resultKind, amount)
	return err
}

// CountCompletedMissions 查詢已完成任務數（決定可用抽獎次數）
func (r *Repository) CountCompletedMissions(ctx context.Context, userID, raceID string) (int, error) {
	return countCompletedMissionsWith(ctx, r.db, userID, raceID)
}

// CountCompletedMissionsTx 與 CountCompletedMissions 相同查詢，但在呼叫端已開好的交易內執行
// （見 CountSpinsAllTx 註解）。
func (r *Repository) CountCompletedMissionsTx(ctx context.Context, tx pgx.Tx, userID, raceID string) (int, error) {
	return countCompletedMissionsWith(ctx, tx, userID, raceID)
}

func countCompletedMissionsWith(ctx context.Context, db pgExecutor, userID, raceID string) (int, error) {
	var n int
	err := db.QueryRow(ctx, `
		SELECT COUNT(*) FROM mission_completions WHERE user_id=$1 AND race_id=$2
	`, userID, raceID).Scan(&n)
	return n, err
}

// BeginSpinTx 開啟一筆抽獎交易，並立刻對「這位使用者在這個賽事的抽獎序列」取
// pg_advisory_xact_lock（交易 COMMIT/ROLLBACK 時自動釋放，比照 internal/race/reward_draw.go
// DrawRaceRewardWinners 的既有寫法）：同一 user+race 的併發 Spin 請求會在此序列化，第二個請求必須
// 等第一個交易結束才能繼續往下讀剩餘次數，修補「先查剩餘次數、再各自插入」中間沒有鎖、兩個併發請求
// 可能都讀到「還有 1 次」而各自插入一筆，實際多發一次抽獎結果的競態（SEC 修補 M5）。
// 呼叫端要負責在完成後 Commit，並用 defer tx.Rollback(ctx) 兜底（Commit 後為 no-op）。
func (r *Repository) BeginSpinTx(ctx context.Context, userID, raceID string) (pgx.Tx, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin spin tx: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, spinLockKey(userID, raceID)); err != nil {
		tx.Rollback(ctx)
		return nil, fmt.Errorf("spin advisory lock: %w", err)
	}
	return tx, nil
}

// spinLockKey 組出 BeginSpinTx 的 advisory lock 鍵——用 ":" 分隔 userID/raceID 兩個 UUID
// 字串本身不含 ":"，兩個不同的 (userID, raceID) 組合不會因為字串串接方式而混成同一把鎖
// （例如 "ab"+"cd" 與 "a"+"bcd" 若不分隔會撞成同一個字串）。抽成獨立函式方便單元測試。
func spinLockKey(userID, raceID string) string {
	return "wheel_spin:" + userID + ":" + raceID
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
