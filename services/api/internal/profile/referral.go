package profile

import (
	"context"
	"net/http"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/referral"
)

// ReferralInfo 推薦連結資訊
type ReferralInfo struct {
	ReferralCode       string `json:"referral_code"`
	ReferredCount      int    `json:"referred_count"`       // 已綁定推薦關係的人數
	RewardedCount      int    `json:"rewarded_count"`       // 已達標(雙方發過獎)的人數
	RewardDaysReferrer int    `json:"reward_days_referrer"` // 推薦人（自己）每成功一位可得天數
	RewardDaysReferred int    `json:"reward_days_referred"` // 被推薦人（新朋友）可得天數
}

// buildReferralInfo 組完整 ReferralInfo（推薦碼 + 已綁定/已達標人數 + 雙方獎勵天數）；
// 供 POST（產生/取得）與 GET（僅查現況）共用，避免重複查詢邏輯。
func (h *Handler) buildReferralInfo(ctx context.Context, userID, code string) ReferralInfo {
	var info ReferralInfo
	info.ReferralCode = code
	h.db.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referrer_user_id=$1`, userID).Scan(&info.ReferredCount)
	h.db.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referrer_user_id=$1 AND rewarded_at IS NOT NULL`, userID).Scan(&info.RewardedCount)
	info.RewardDaysReferrer = appsettings.GetInt(ctx, h.db, "referral_reward_referrer_days", 1)
	info.RewardDaysReferred = appsettings.GetInt(ctx, h.db, "referral_reward_referred_days", 3)
	return info
}

// GetOrCreateReferral POST /api/v1/profile/referral — 產生（或取得既有）專屬推薦碼；
// 需 total_km >= 10 才能產生，避免新帳號還沒累積任何里程就先散發連結。
func (h *Handler) GetOrCreateReferral(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()
	var totalKm float64
	if err := h.db.QueryRow(ctx, `SELECT total_km FROM users WHERE id=$1`, userID).Scan(&totalKm); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if totalKm < 10 {
		respondErr(w, http.StatusForbidden, "需累積完成 10 公里才能產生推廣連結")
		return
	}
	code, err := referral.GetOrCreateCode(ctx, h.db, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, h.buildReferralInfo(ctx, userID, code))
}

// GetReferral GET /api/v1/profile/referral — 只查現況、不產生；供頁面重新掛載時把「已產生過」的
// 推廣碼載回來持續顯示連結（不做 10km 門檻檢查——即使 total_km 之後掉回 10 以下，既有碼仍照顯示）。
// 沒產生過（碼為 NULL/空）→ 回 200 + 空碼，前端據此顯示「產生我的推廣連結」按鈕。
func (h *Handler) GetReferral(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	ctx := r.Context()
	var code *string
	if err := h.db.QueryRow(ctx, `SELECT referral_code FROM users WHERE id=$1`, userID).Scan(&code); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if code == nil || *code == "" {
		respondJSON(w, http.StatusOK, ReferralInfo{})
		return
	}
	respondJSON(w, http.StatusOK, h.buildReferralInfo(ctx, userID, *code))
}
