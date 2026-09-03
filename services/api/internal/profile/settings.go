package profile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/ttlcache"
)

// SiteSettings 全站外觀設定（單例）
type SiteSettings struct {
	MemberPanelBgURL      string `json:"member_panel_bg_url"`
	StravaPoweredDarkURL  string `json:"strava_powered_dark_url"`  // 深色 skin 用（白字版）
	StravaPoweredLightURL string `json:"strava_powered_light_url"` // 淺色 skin 用（深字版）
}

// siteSettingsCacheTTL 見下方 siteSettingsCache 註解。
const siteSettingsCacheTTL = 10 * time.Minute

// siteSettingsCache 快取 GET /settings（site_settings 單例資料表）的回應（10 分鐘 TTL）。理由同
// appsettings.publicSettingsCache（Neon 夜間喚醒問題「先做 A」）——這支雖然主要是前台 client 端
// useSWR/useEffect 呼叫（見 AchievementScreen/MemberPanel/ProfileScreen 等），仍會經 Next.js
// rewrite 落到這支 Go handler、每次都對 site_settings 現查 DB；改走快取後穩態下最多每 10 分鐘一次
// 查詢。
//
// 在 NewHandler 建構時就綁死 load closure（不用 sync.Once 延遲初始化）：main.go 只呼叫一次
// profile.NewHandler（唯一正式入口），建構當下是單一 goroutine（HTTP server 尚未開始收
// request），對這個 package-level 變數的寫入沒有資料競賽疑慮。titles_export.go 的 AwardTitles
// 另外會繞過 NewHandler 直接 new 一個「陽春版」&Handler{db: db}（稱號引擎用，摸不到這支
// GetSettings/PutSettings），不影響這裡的初始化時機假設；GetSettings/PutSettings 仍加了 nil 檢查
// 保底，避免萬一真的有人繞過 NewHandler 呼叫到這兩支時直接 panic。
var siteSettingsCache *ttlcache.Cache[SiteSettings]

func newSiteSettingsCache(h *Handler) *ttlcache.Cache[SiteSettings] {
	return ttlcache.New(siteSettingsCacheTTL, func(ctx context.Context) (SiteSettings, error) {
		return loadSiteSettings(ctx, h)
	})
}

// loadSiteSettings 現查 DB（無快取）。查無資料列（pgx.ErrNoRows，例如尚未有人存過設定）視為
// 「合法的空設定」而非錯誤——沿用 GetSettings 舊版行為（回全空欄位），這裡明確區分開，
// 才不會讓 ttlcache 把「查無資料」誤判成「查詢失敗」而白白保留（其實不存在的）舊值。
func loadSiteSettings(ctx context.Context, h *Handler) (SiteSettings, error) {
	var s SiteSettings
	err := h.db.QueryRow(ctx,
		`SELECT COALESCE(member_panel_bg_url,''), COALESCE(strava_powered_dark_url,''), COALESCE(strava_powered_light_url,'')
		 FROM site_settings WHERE id=TRUE`).
		Scan(&s.MemberPanelBgURL, &s.StravaPoweredDarkURL, &s.StravaPoweredLightURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SiteSettings{}, nil
		}
		return SiteSettings{}, err
	}
	return s, nil
}

// GET /api/v1/settings — 公開，前台讀全站外觀設定
func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	if siteSettingsCache == nil {
		// 防禦：理論上不會發生（main.go 一定經 NewHandler 建構），僅防繞過 NewHandler 的測試情境；
		// 退化成不快取直接查 DB，行為與快取命中前的舊版一致。
		s, _ := loadSiteSettings(r.Context(), h)
		respondJSON(w, http.StatusOK, map[string]any{"settings": s})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"settings": siteSettingsCache.Get(r.Context())})
}

// PUT /api/v1/admin/settings — admin 設定全站外觀（前端一律送完整物件，避免漏欄位被清空）
func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var s SiteSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO site_settings (id, member_panel_bg_url, strava_powered_dark_url, strava_powered_light_url, updated_at)
		 VALUES (TRUE,$1,$2,$3,NOW())
		 ON CONFLICT (id) DO UPDATE SET member_panel_bg_url=$1, strava_powered_dark_url=$2, strava_powered_light_url=$3, updated_at=NOW()`,
		s.MemberPanelBgURL, s.StravaPoweredDarkURL, s.StravaPoweredLightURL); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if siteSettingsCache != nil {
		siteSettingsCache.Invalidate()
	}
	respondJSON(w, http.StatusOK, map[string]any{"settings": s})
}
