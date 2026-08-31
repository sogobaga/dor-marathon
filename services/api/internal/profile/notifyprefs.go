package profile

import (
	"encoding/json"
	"net/http"

	"github.com/dor/api/internal/auth"
)

// POST /api/v1/profile/notify-prefs  {"runmeet_reminder_email": true|false}
//
// 團練「開跑前提醒」Email 開關（users.runmeet_reminder_email，migration 163；預設 TRUE）。
// 獨立於 UpdateMe（PUT /profile，要求前端送整個 Profile 物件）之外另開一支窄端點，比照既有
// SetDataSource（POST /profile/data-source，見 dedup.go）的偏好切換慣例——這類「只改一個布林/
// 列舉欄位」的偏好，不必逼前端組完整 Profile 物件才能改一格。
//
// ⚠️ 這個開關只管 Email；站內信（user_mail）一律照發，不受此開關影響（規格明訂：站內信一律發）。
// 與 email_unsubscribes（migration 141，行銷退訂）是兩個獨立閘門，寄送端（reminder.go
// eligibleForReminderEmail）兩者都要通過才寄。
func (h *Handler) SetNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		RunmeetReminderEmail *bool `json:"runmeet_reminder_email"`
	}
	// *bool（而非 bool）刻意用來分辨「沒送這個欄位」與「送了 false」——省略欄位時 400，
	// 避免舊版前端誤送空 body 把偏好靜默清成 false。
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RunmeetReminderEmail == nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE users SET runmeet_reminder_email=$2, updated_at=NOW() WHERE id=$1`,
		userID, *req.RunmeetReminderEmail); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "runmeet_reminder_email": *req.RunmeetReminderEmail})
}
