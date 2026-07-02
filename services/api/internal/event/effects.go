// 效果資產覆寫：把程式內建的暫代 emoji/合成音效，換成後台上傳的正式圖片/音檔。
package event

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
)

var effectSlugRe = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// EffectAssetsRouter 後台效果管理（掛 /admin/effect-assets，需 event_tasks 權限）
func (h *Handler) EffectAssetsRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListEffectAssets)
	r.Put("/{slug}", h.SetEffectAsset)
	r.Delete("/{slug}", h.ClearEffectAsset)
	return r
}

func (h *Handler) respondEffectAssets(w http.ResponseWriter, ctx context.Context) {
	rows, err := h.db.Query(ctx, `SELECT slug, url FROM effect_assets WHERE url<>''`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var s, u string
		if rows.Scan(&s, &u) == nil {
			m[s] = u
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"assets": m})
}

// ListEffectAssets 後台讀；PublicEffectAssets 前台跑步引擎讀（同內容）
func (h *Handler) ListEffectAssets(w http.ResponseWriter, r *http.Request)   { h.respondEffectAssets(w, r.Context()) }
func (h *Handler) PublicEffectAssets(w http.ResponseWriter, r *http.Request) { h.respondEffectAssets(w, r.Context()) }

func (h *Handler) SetEffectAsset(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !effectSlugRe.MatchString(slug) {
		respondErr(w, http.StatusBadRequest, "invalid slug")
		return
	}
	var b struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO effect_assets (slug, url, updated_at) VALUES ($1,$2,NOW())
		 ON CONFLICT (slug) DO UPDATE SET url=EXCLUDED.url, updated_at=NOW()`,
		slug, strings.TrimSpace(b.URL)); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	h.respondEffectAssets(w, r.Context())
}

func (h *Handler) ClearEffectAsset(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	_, _ = h.db.Exec(r.Context(), `DELETE FROM effect_assets WHERE slug=$1`, slug)
	h.respondEffectAssets(w, r.Context())
}
