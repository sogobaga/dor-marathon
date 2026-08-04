package profile

import (
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
	var info ReferralInfo
	info.ReferralCode = code
	h.db.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referrer_user_id=$1`, userID).Scan(&info.ReferredCount)
	h.db.QueryRow(ctx, `SELECT COUNT(*) FROM referrals WHERE referrer_user_id=$1 AND rewarded_at IS NOT NULL`, userID).Scan(&info.RewardedCount)
	info.RewardDaysReferrer = appsettings.GetInt(ctx, h.db, "referral_reward_referrer_days", 1)
	info.RewardDaysReferred = appsettings.GetInt(ctx, h.db, "referral_reward_referred_days", 3)
	respondJSON(w, http.StatusOK, info)
}
