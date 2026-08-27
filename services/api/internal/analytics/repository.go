package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SaveReport 存檔（UPSERT，同一天重算允許覆寫——見 migration 148 檔頭註解）。report 先在 Go 端
// json.Marshal 成 []byte 再交給 pgx（走 JSONB 欄位），避免依賴 pgx 對具名 struct 的自動 JSON
// codec（明確可控、報錯訊息也更好定位）。
func SaveReport(ctx context.Context, db *pgxpool.Pool, rpt Report) error {
	body, err := json.Marshal(rpt)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO member_analytics_reports (day, report, computed_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (day) DO UPDATE SET report = EXCLUDED.report, computed_at = EXCLUDED.computed_at`,
		rpt.Day, body)
	return err
}

// LatestReport 讀最新一筆（依 day DESC）。ErrNoReport：表尚無任何一列（例如 migration 剛上線、
// 排程尚未跑過第一輪、也還沒有人手動觸發過 recompute）。
var ErrNoReport = errors.New("no analytics report computed yet")

func LatestReport(ctx context.Context, db *pgxpool.Pool) (raw json.RawMessage, computedAt time.Time, err error) {
	err = db.QueryRow(ctx, `
		SELECT report, computed_at FROM member_analytics_reports
		ORDER BY day DESC LIMIT 1`).Scan(&raw, &computedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, ErrNoReport
	}
	return raw, computedAt, err
}

// LatestComputedAt 只讀最新一筆的 computed_at（供排程判斷「距上次重算是否已超過門檻」，不必把整包
// JSONB 也撈出來）。ErrNoReport 語意同 LatestReport。
func LatestComputedAt(ctx context.Context, db *pgxpool.Pool) (time.Time, error) {
	var t time.Time
	err := db.QueryRow(ctx, `SELECT computed_at FROM member_analytics_reports ORDER BY day DESC LIMIT 1`).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNoReport
	}
	return t, err
}
