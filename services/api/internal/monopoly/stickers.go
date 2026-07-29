package monopoly

// stickers.go 完賽公仔九宮格貼紙讀取（供 Phase 2b 收集頁）。純讀取，不異動任何資料。
// 與 knowledge.go 的防劇透規則不同：貼紙灰階片本來就要顯示讓玩家看到缺哪片，因此全部 pieces 一律
// 附完整展示欄位（title/rarity/gray_url），owned 只影響「是否已收集」的展示狀態，不影響回傳內容。

import (
	"context"
	"fmt"
)

// GetStickerPieces 回傳 set_key='finisher' 全部 active 貼紙片（依 position 排序），附使用者收集狀態
// （LEFT JOIN monopoly_user_stickers：查無列即 owned=false, obtained_count=0）。
// 注意用 monopoly_user_stickers 而非 user_stickers——後者是 001_init 的完賽貼紙舊表（sticker_no/race_id）。
func (r *Repository) GetStickerPieces(ctx context.Context, userID string) ([]StickerPieceEntry, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.position, p.title, p.rarity, p.image_url,
		       (u.piece_id IS NOT NULL) AS owned,
		       COALESCE(u.obtained_count, 0) AS obtained_count
		FROM sticker_pieces p
		LEFT JOIN monopoly_user_stickers u ON u.piece_id = p.id AND u.user_id = $1
		WHERE p.set_key = 'finisher' AND p.is_active = true
		ORDER BY p.position`, userID)
	if err != nil {
		return nil, fmt.Errorf("load sticker pieces: %w", err)
	}
	defer rows.Close()

	var pieces []StickerPieceEntry
	for rows.Next() {
		var e StickerPieceEntry
		if err := rows.Scan(&e.Position, &e.Title, &e.Rarity, &e.GrayURL, &e.Owned, &e.ObtainedCount); err != nil {
			return nil, fmt.Errorf("scan sticker piece: %w", err)
		}
		pieces = append(pieces, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pieces, nil
}
