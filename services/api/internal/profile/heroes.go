// 百里英雄榜：累積里程突破 100 公里的跑者，前 100 名（見設計備忘 activity-reward-system 系列
// 之外的第 15 套小功能）。公開可讀（OptionalAuth，見 cmd/api/main.go 掛載），帶登入 token 時
// 附帶 is_following/is_self，供前台顯示「追蹤/已追蹤」與「自己」標示；追蹤/取消追蹤沿用既有
// POST /profile/follow、DELETE /profile/follow/{userID}，本檔不新增追蹤端點。
package profile

import (
	"net/http"

	"github.com/dor/api/internal/auth"
)

// HundredHero 百里英雄榜單列。刻意不排除 admin/虛擬選手（虛擬選手入榜是造勢設計，見任務需求）；
// 帳號編碼(account_code)/email 等機敏欄位絕不回傳，只回暱稱優先的顯示名稱＋頭像＋累積里程。
type HundredHero struct {
	UserID      string  `json:"user_id"`
	Name        string  `json:"name"`       // 暱稱優先：COALESCE(NULLIF(nickname,''), 顯示名稱)
	AvatarURL   string  `json:"avatar_url"`
	TotalKm     float64 `json:"total_km"`
	IsFollowing bool    `json:"is_following"` // 登入時才有意義；未登入或查詢自己恆 false
	IsSelf      bool    `json:"is_self"`      // 登入時才有意義；未登入恆 false
}

// GET /api/v1/heroes/hundred — 百里英雄榜：users.total_km >= 100 依 total_km desc 前 100 名。
// 公開可讀（middleware.OptionalAuth）；未登入時 is_following/is_self 皆 false（比照
// internal/race/personal_leaderboard.go 的 optional-auth 排行榜寫法，userID 可為空字串）。
func (h *Handler) HundredHeroes(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	// 顯示名稱口徑＝COALESCE(NULLIF(u.name,''), u.handle)，與「一般賽事排行榜」（race/leaderboard.go:65）
	// 完全一致——玩家在兩個榜看到的自己必須是同一個名字（2026-08-28 使用者回報：英雄榜顯示個資暱稱
	// 「廣三」而非排行榜慣用的顯示名稱「MiMi」）。personal_leaderboard.go 原本仍用 p.nickname 優先，
	// 屬既有不一致，已於同日全面盤點後改齊統一口徑（顯示名稱統一口徑（2026-08-28 使用者定案：
	// 個資暱稱已由顯示名稱取代））。
	rows, err := h.db.Query(r.Context(), `
		SELECT u.id::text, COALESCE(NULLIF(u.name,''), u.handle) AS name,
		       COALESCE(u.avatar_url,'') AS avatar_url, u.total_km,
		       ($1 <> '' AND EXISTS(
		           SELECT 1 FROM follows f WHERE f.follower_id = NULLIF($1,'')::uuid AND f.followee_id = u.id
		       )) AS is_following
		FROM users u
		WHERE u.total_km >= 100
		ORDER BY u.total_km DESC
		LIMIT 100`, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load hundred heroes")
		return
	}
	defer rows.Close()

	out := []HundredHero{}
	for rows.Next() {
		var row HundredHero
		if err := rows.Scan(&row.UserID, &row.Name, &row.AvatarURL, &row.TotalKm, &row.IsFollowing); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		row.IsSelf = userID != "" && row.UserID == userID
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load hundred heroes")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"heroes": out, "count": len(out)})
}
