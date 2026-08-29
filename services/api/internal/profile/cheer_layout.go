// 啦啦隊角色位置校正（2026-08-29）：GPS 跑步頁「🎯 校正啦啦隊」校正模式儲存端。
// 入口受 requireEntry(cheer_edit_entry_state/whitelist) 保護（見 RequireCheerLayoutEntry），
// 校正值寫入 app_settings key cheer_char_layout，供 Dashboard.cheer_char_layout 回顯（見 membership.go）。
package profile

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/dor/api/internal/appsettings"
)

// CheerLayoutItem 啦啦隊單一角色的位置校正值：dx/dy 相對角色容器自身寬/高的百分比位移
// （正 dx 往右、正 dy 往下），scale 縮放倍率（transform-origin 上中）。與前端
// apps/web/src/lib/api.ts 的 CheerCharLayoutItem 型別一一對應。
type CheerLayoutItem struct {
	DX    float64 `json:"dx"`
	DY    float64 `json:"dy"`
	Scale float64 `json:"scale"`
}

// CheerLayout 八張角色（01~08）各自的位置校正值。
type CheerLayout map[string]CheerLayoutItem

// defaultCheerCharLayoutJSON 系統設定 cheer_char_layout 查無資料時的內建預設（置中、不偏移、
// 原尺寸）；與 migration 153 種子資料同一份字面值，維持單一事實來源。
const defaultCheerCharLayoutJSON = `{"01":{"dx":0,"dy":0,"scale":1},"02":{"dx":0,"dy":0,"scale":1},"03":{"dx":0,"dy":0,"scale":1},"04":{"dx":0,"dy":0,"scale":1},"05":{"dx":0,"dy":0,"scale":1},"06":{"dx":0,"dy":0,"scale":1},"07":{"dx":0,"dy":0,"scale":1},"08":{"dx":0,"dy":0,"scale":1}}`

// defaultCheerLayoutItem 單一角色缺 key 時補齊用的預設值（置中、不偏移、原尺寸）。
var defaultCheerLayoutItem = CheerLayoutItem{DX: 0, DY: 0, Scale: 1}

// cheerLayoutKeys 合法角色 key（固定八個，對應八張啦啦隊圖，01~08 擴充自舊版 01~03）。
var cheerLayoutKeys = [8]string{"01", "02", "03", "04", "05", "06", "07", "08"}

// cheerLayoutKeySet 合法角色 key 的集合，用於 O(1) 判斷輸入是否含未知 key。
var cheerLayoutKeySet = func() map[string]bool {
	m := make(map[string]bool, len(cheerLayoutKeys))
	for _, k := range cheerLayoutKeys {
		m[k] = true
	}
	return m
}()

// normalizeCheerLayout 驗證並正規化前台送來的校正值：輸入的 key 必須是 01~08 的子集（出現
// 未知 key 直接回錯誤），缺少的 key 用預設值（dx0/dy0/scale1）補齊；每筆 dx/dy 須為有限數且
// 介於 -300~300、scale 須為有限數且介於 0.2~4，通過後四捨五入到小數 2 位。輸出恆為 8 筆完整
// 資料，讓舊資料（僅 3 筆）與前端只送部分 key 都能相容。獨立成純函式（不碰 http/db）方便單元測試。
func normalizeCheerLayout(in CheerLayout) (CheerLayout, error) {
	for key := range in {
		if !cheerLayoutKeySet[key] {
			return nil, fmt.Errorf("unknown key %q", key)
		}
	}
	out := make(CheerLayout, len(cheerLayoutKeys))
	for _, key := range cheerLayoutKeys {
		item, ok := in[key]
		if !ok {
			out[key] = defaultCheerLayoutItem
			continue
		}
		if !isFiniteInRange(item.DX, -300, 300) {
			return nil, fmt.Errorf("%s.dx out of range", key)
		}
		if !isFiniteInRange(item.DY, -300, 300) {
			return nil, fmt.Errorf("%s.dy out of range", key)
		}
		if !isFiniteInRange(item.Scale, 0.2, 4) {
			return nil, fmt.Errorf("%s.scale out of range", key)
		}
		out[key] = CheerLayoutItem{DX: round2(item.DX), DY: round2(item.DY), Scale: round2(item.Scale)}
	}
	return out, nil
}

// isFiniteInRange 純函式：v 為有限數（非 NaN/Inf）且落在 [min,max] 區間。
func isFiniteInRange(v, min, max float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= min && v <= max
}

// round2 四捨五入到小數 2 位（校正值不需要更高精度，避免資料庫存一堆浮點雜訊位數）。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// PutCheerLayout PUT /me/cheer-layout — 儲存啦啦隊角色位置校正值。掛
// RequireCheerLayoutEntry（見下）：非白名單一律 403，這裡不必再查一次。
// body {"layout": {"01":{"dx":0,"dy":0,"scale":1}, "02":{...}, ...}}；可只送 01~08 的子集，
// 缺的 key 由 normalizeCheerLayout 補預設值，出現未知 key 才回 400。
// 回 {"ok":true, "layout": {...正規化後，恆 8 筆}}；驗證失敗回 400。
func (h *Handler) PutCheerLayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Layout CheerLayout `json:"layout"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	out, err := normalizeCheerLayout(body.Layout)
	if err != nil {
		respondErr(w, http.StatusBadRequest, "invalid layout: "+err.Error())
		return
	}
	raw, err := json.Marshal(out)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	// 直接 upsert app_settings（比照 monopoly.Repository.AdminSetSetting 的寫法）：cheer_char_layout
	// 不在 /admin/app-settings 管理者可調清單「唯讀寫入端」之外還有這支專屬 API，兩邊共用同一個 key，
	// 不需要透過 appsettings.Handler.Set。寫入成功後必須呼叫 appsettings.InvalidateCache() 清掉 60 秒記憶體
	// 快取：前端儲存後會 mutate Dashboard 重新讀取 cheer_char_layout，若快取未清會拿到舊值把校正結果彈回去。
	if _, err := h.db.Exec(r.Context(), `
		INSERT INTO app_settings (key, value, updated_at) VALUES ('cheer_char_layout', $1, NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		string(raw)); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	appsettings.InvalidateCache()
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "layout": out})
}

// RequireCheerLayoutEntry 啦啦隊角色位置校正 API 的入口白名單中介層（cheer_edit_entry_state /
// cheer_edit_entry_whitelist）。requireEntry 本身是套件私有的中介層工廠（main.go 在 package main，
// 呼叫不到小寫方法），這裡包一層 exported 方法讓 main.go 能直接掛在 PUT /me/cheer-layout 上；
// 非白名單（且非 super_admin）一律 403，比照 titles/achievements 路由群組的掛法。
func (h *Handler) RequireCheerLayoutEntry(next http.Handler) http.Handler {
	return h.requireEntry("cheer_edit_entry_state", "cheer_edit_entry_whitelist")(next)
}
