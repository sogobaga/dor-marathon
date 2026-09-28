package profile

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/dor/api/internal/auth"
)

// validUiSkins 合法風格值（migration 193+194 CHECK 約束同一組值；純函式，供 SetUiSkin 與測試共用）。
var validUiSkins = map[string]bool{"default": true, "scifi": true, "retro": true, "cute": true}

// isValidUiSkin 純函式版本（獨立於 map 之外，方便單元測試不依賴套件層級可變狀態）。
func isValidUiSkin(skin string) bool { return validUiSkins[skin] }

// PUT /api/v1/me/ui-skin  body: {"skin": "default"|"scifi"|"retro"|"cute"}
//
// 帳號層級「風格設定」寫入端（會員管理→個人資料頁「風格設定」區塊，見 resolveSkinSelectEntry，
// migration 193，契約 retro_skin/CONTRACT.md §2.1）。只有 skin_select_entry==='shown' 的帳號能
// 寫入；未授權一律 403 skin_not_allowed（不細分「白名單未命中」或「入口整個關閉」，避免對外洩漏
// 白名單判定細節）。值不在合法集合（skin_options 固定 ['default','scifi','retro','cute']）400
// invalid_skin。只寫本人（userID 來自 auth context，body 不接受 user_id 欄位，杜絕竄改他人帳號）。
func (h *Handler) SetUiSkin(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var email, code string
	var vipExpiresAt *time.Time
	if err := h.db.QueryRow(r.Context(),
		`SELECT COALESCE(email,''), COALESCE(account_code,''), vip_expires_at FROM users WHERE id=$1`, userID).
		Scan(&email, &code, &vipExpiresAt); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	isVIP := vipExpiresAt != nil && vipExpiresAt.After(time.Now())
	if resolveSkinSelectEntry(r.Context(), h.db, email, code, isVIP) != "shown" {
		respondErr(w, http.StatusForbidden, "skin_not_allowed")
		return
	}
	var req struct {
		Skin string `json:"skin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !isValidUiSkin(req.Skin) {
		respondErr(w, http.StatusBadRequest, "invalid_skin")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE users SET ui_skin=$2, updated_at=NOW() WHERE id=$1`, userID, req.Skin); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ui_skin": req.Skin})
}
