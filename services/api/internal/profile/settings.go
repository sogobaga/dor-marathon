package profile

import (
	"encoding/json"
	"net/http"
)

// SiteSettings 全站外觀設定（單例）
type SiteSettings struct {
	MemberPanelBgURL string `json:"member_panel_bg_url"`
}

// GET /api/v1/settings — 公開，前台讀全站外觀設定
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	var s SiteSettings
	if err := h.db.QueryRow(r.Context(),
		`SELECT COALESCE(member_panel_bg_url,'') FROM site_settings WHERE id=TRUE`).
		Scan(&s.MemberPanelBgURL); err != nil {
		// 尚無資料列也回空設定（非錯誤）
		respondJSON(w, http.StatusOK, map[string]any{"settings": SiteSettings{}})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"settings": s})
}

// PUT /api/v1/admin/settings — admin 設定全站外觀
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var s SiteSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO site_settings (id, member_panel_bg_url, updated_at) VALUES (TRUE,$1,NOW())
		 ON CONFLICT (id) DO UPDATE SET member_panel_bg_url=$1, updated_at=NOW()`,
		s.MemberPanelBgURL); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"settings": s})
}
