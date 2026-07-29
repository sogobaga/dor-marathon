package monopoly

// figure_redeem.go 完賽公仔兌換 A（會員端）：集滿 9 片九宮格貼紙後可申請兌換實體公仔，每人限一筆
// （monopoly_figure_redemptions.user_id UNIQUE，見 107_monopoly_figure_redeem.sql）。
// 冪等設計：EnsureFigureRedemption 用「INSERT ... ON CONFLICT (user_id) DO UPDATE SET
// user_id=EXCLUDED.user_id RETURNING status」單一陳述式完成「查無則建立 pending、已有則回現況」，
// 不會覆寫既有 status/note，也不需要先查一次再判斷要不要 INSERT。

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrStickersIncomplete 九宮格貼紙尚未集滿全部片數，不可申請兌換（見 Service.RedeemFigure）。
var ErrStickersIncomplete = errors.New("monopoly: stickers not complete")

// EnsureFigureRedemption 若使用者尚無兌換申請則建立一筆 pending 並回傳 "pending"；若已有申請則直接
// 回傳現況（冪等，不重複建立、不報錯）。呼叫端須先自行驗證已集滿全部貼紙（見 Service.RedeemFigure）。
func (r *Repository) EnsureFigureRedemption(ctx context.Context, userID string) (string, error) {
	var status string
	err := r.db.QueryRow(ctx, `
		INSERT INTO monopoly_figure_redemptions (user_id, status)
		VALUES ($1, 'pending')
		ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
		RETURNING status`, userID,
	).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("ensure figure redemption: %w", err)
	}
	return status, nil
}

// GetFigureRedemptionStatus 供 GET /monopoly/stickers 附帶目前兌換狀態；查無申請回空字串（代表尚未申請）。
func (r *Repository) GetFigureRedemptionStatus(ctx context.Context, userID string) (string, error) {
	var status string
	err := r.db.QueryRow(ctx, `SELECT status FROM monopoly_figure_redemptions WHERE user_id=$1`, userID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get figure redemption status: %w", err)
	}
	return status, nil
}
