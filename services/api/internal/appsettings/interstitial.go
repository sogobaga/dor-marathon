// 蓋板廣告（拍立得卡片堆疊）：多張卡片 CRUD + 前台公開讀取（受 interstitial_enabled 總開關控制）。
package appsettings

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type InterstitialAd struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	SortOrder   int    `json:"sort_order"`
	ImageURL    string `json:"image_url"`
	Headline    string `json:"headline"`
	Description string `json:"description"`
	CTALabel    string `json:"cta_label"`
	CTAURL      string `json:"cta_url"`
}

const adCols = `id::text, enabled, sort_order, image_url, headline, description, cta_label, cta_url`

func scanAd(row interface {
	Scan(...any) error
}) (InterstitialAd, error) {
	var a InterstitialAd
	err := row.Scan(&a.ID, &a.Enabled, &a.SortOrder, &a.ImageURL, &a.Headline, &a.Description, &a.CTALabel, &a.CTAURL)
	return a, err
}

// PublicInterstitial 前台開啟時讀取要顯示的蓋板廣告（總開關開啟 + 該卡啟用 + 有圖）。
func (h *Handler) PublicInterstitial(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if GetInt(ctx, h.db, "interstitial_enabled", 0) != 1 {
		respondJSON(w, http.StatusOK, map[string]any{"ads": []InterstitialAd{}})
		return
	}
	rows, err := h.db.Query(ctx,
		`SELECT `+adCols+` FROM interstitial_ads WHERE enabled AND image_url<>'' ORDER BY sort_order, created_at`)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"ads": []InterstitialAd{}})
		return
	}
	defer rows.Close()
	ads := []InterstitialAd{}
	for rows.Next() {
		if a, err := scanAd(rows); err == nil {
			ads = append(ads, a)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"ads": ads})
}

// InterstitialAdminRouter 後台蓋板廣告 CRUD（掛 /admin/interstitial，需 settings 權限）。
func (h *Handler) InterstitialAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdList)
	r.Post("/", h.AdCreate)
	r.Put("/{id}", h.AdUpdate)
	r.Delete("/{id}", h.AdDelete)
	return r
}

func (h *Handler) AdList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT `+adCols+` FROM interstitial_ads ORDER BY sort_order, created_at`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	ads := []InterstitialAd{}
	for rows.Next() {
		if a, err := scanAd(rows); err == nil {
			ads = append(ads, a)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"ads": ads})
}

func (h *Handler) AdCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := decodeAd(w, r)
	if !ok {
		return
	}
	var id string
	err := h.db.QueryRow(r.Context(),
		`INSERT INTO interstitial_ads (enabled, sort_order, image_url, headline, description, cta_label, cta_url)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`,
		a.Enabled, a.SortOrder, a.ImageURL, a.Headline, a.Description, a.CTALabel, a.CTAURL).Scan(&id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (h *Handler) AdUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, ok := decodeAd(w, r)
	if !ok {
		return
	}
	ct, err := h.db.Exec(r.Context(),
		`UPDATE interstitial_ads SET enabled=$2, sort_order=$3, image_url=$4, headline=$5, description=$6,
		 cta_label=$7, cta_url=$8, updated_at=NOW() WHERE id=$1`,
		id, a.Enabled, a.SortOrder, a.ImageURL, a.Headline, a.Description, a.CTALabel, a.CTAURL)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) AdDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM interstitial_ads WHERE id=$1`, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func decodeAd(w http.ResponseWriter, r *http.Request) (InterstitialAd, bool) {
	var a InterstitialAd
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return a, false
	}
	a.ImageURL = strings.TrimSpace(a.ImageURL)
	a.Headline = strings.TrimSpace(a.Headline)
	a.Description = strings.TrimSpace(a.Description)
	a.CTALabel = strings.TrimSpace(a.CTALabel)
	a.CTAURL = strings.TrimSpace(a.CTAURL)
	return a, true
}
