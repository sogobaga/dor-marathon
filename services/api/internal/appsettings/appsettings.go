// Package appsettings 通用系統設定（key-value 單表），供後台「系統設定」頁調教。
// 值一律以字串儲存；讀取端用 GetInt/GetString 等 typed helper 解析並帶預設值。
package appsettings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/realtime"
)

// specs 登記所有合法設定 key 及其值驗證器（後端權威；新增設定時在此加一列）。
var specs = map[string]func(string) bool{
	"event_wait_min_sec":        isNonNegInt,
	"event_wait_max_sec":        isNonNegInt,
	"event_first_wait_run1_sec": isNonNegInt, // 新手加速：第 1/2/3 趟跑步「第一個事件」的等待秒數
	"event_first_wait_run2_sec": isNonNegInt,
	"event_first_wait_run3_sec": isNonNegInt,
	"active_skin":               func(v string) bool { return v == "" || v == "default" || v == "warm" || v == "warm2" },
	"interstitial_enabled":      func(v string) bool { return v == "" || v == "0" || v == "1" }, // 蓋板廣告總開關
	"favicon_url": func(v string) bool {
		return v == "" || (len(v) <= 512 && (strings.HasPrefix(v, "/") || strings.HasPrefix(v, "http")))
	},
	// 入口可見性：hidden 前台隱藏 / locked 顯示但不能按 / whitelist 顯示且指定帳號可按 / open 顯示且全部開放
	"personal_entry_state":        isEntryState,
	"personal_entry_whitelist":    isWhitelist,  // 換行/逗號分隔的帳號編碼或 email
	"explore_entry_state":         isEntryState, // 城市探索入口
	"explore_entry_whitelist":     isWhitelist,
	"gallery_entry_state":         isEntryState, // 卡片圖鑑入口
	"gallery_entry_whitelist":     isWhitelist,
	"title_entry_state":           isEntryState, // 稱號系統入口
	"title_entry_whitelist":       isWhitelist,
	"achievement_entry_state":     isEntryState, // 成就統計入口
	"achievement_entry_whitelist": isWhitelist,
	"training_entry_state":        isEntryState, // 自主訓練入口
	"training_entry_whitelist":    isWhitelist,
	"strategy_entry_state":        isEntryState, // 賽事策略入口（自主訓練第三分頁）
	"strategy_entry_whitelist":    isWhitelist,
	"monopoly_entry_state":        isEntryState, // 環台大富翁入口
	"monopoly_entry_whitelist":    isWhitelist,
	"knowledge_entry_state":       isEntryState, // 知識探索(知識卡圖鑑)入口
	"knowledge_entry_whitelist":   isWhitelist,
	// VIP 訂閱制（後台可調數值）
	"vip_trial_days":              isNonNegInt,           // 新註冊自動 VIP 試用天數
	"vip_price_monthly":           isNonNegInt,           // 月繳原價（元）
	"vip_price_annual":            isNonNegInt,           // 年繳原價（元）
	"vip_first_promo_monthly_pct": isPct,                 // 首購促銷・月繳實付%（70=付七成）
	"vip_first_promo_annual_pct":  isPct,                 // 首購促銷・年繳實付%（55=付五五）
	"vip_first_promo_days":        isNonNegInt,           // 首購促銷窗天數（試用到期後幾天內續訂享優惠）
	"vip_coupon_value_cents":      isPosIntMax(10000000), // 活動優惠券面額（分，預設10000=$100）；改動後立即套用於「之後」的報名折抵，已持有的券張數不受影響
	"vip_coupon_per_month":        isNonNegInt,           // VIP 每月補發活動優惠券張數（預設3）
	// 取消退費政策系統預設（見 race.CancellationPolicy／race.ResolveCancellationPolicy）；
	// 值為整包政策的 JSON 字串，個別賽事可在 races.config.cancellation_policy 覆寫。
	"cancellation_policy": isCancellationPolicyJSON,
	// 城市探索「打卡」每日上限與同點冷卻時數（見 internal/explore.Checkin）；程式讀取皆有預設值。
	"explore_checkin_daily_cap_normal": isPosIntMax(50),  // 一般會員每日打卡上限（預設 3）
	"explore_checkin_daily_cap_vip":    isPosIntMax(50),  // VIP 每日打卡上限（預設 5）
	"explore_checkin_cooldown_hours":   isPosIntMax(720), // 同一打卡點再次打卡需等待的小時數（預設 24）
	// 城市探索「打卡/完成獎勵」系統級預設（見 internal/explore.go effectiveCheckinRange/effectiveCompleteRange）；
	// explore_bosses 每點的 checkin_reward_*/complete_reward_* 非 0 時覆蓋這裡的預設，皆 0 時吃這裡。
	"explore_checkin_dp_min":     isNonNegInt, // 每次打卡 DP 下限（預設 1）
	"explore_checkin_dp_max":     isNonNegInt, // 每次打卡 DP 上限（預設 3）
	"explore_checkin_gp_min":     isNonNegInt, // 每次打卡 GP 下限（預設 1）
	"explore_checkin_gp_max":     isNonNegInt, // 每次打卡 GP 上限（預設 2）
	"explore_complete_gp_min":    isNonNegInt, // 關主完成 GP 下限（預設 5）
	"explore_complete_gp_max":    isNonNegInt, // 關主完成 GP 上限（預設 10）
	"explore_complete_gp_chance": isPct0to100, // 關主完成給 GP 的機率(%)（預設 30）；0 合法＝不擲
	// 環台大富翁（見 internal/monopoly）：擲骰 GP 成本（預設 3）、繞圈獎勵 GP（預設 0＝不給）
	"monopoly_dice_gp_cost":  isNonNegInt,
	"monopoly_lap_reward_gp": isNonNegInt,
	// 推薦/推廣連結系統（見 internal/referral）：達標（雙方 total_km 皆 >=10）時雙向 VIP 天數獎勵
	"referral_reward_referrer_days": isNonNegInt, // 推薦人（老朋友）獎勵天數，預設 1
	"referral_reward_referred_days": isNonNegInt, // 被推薦人（新朋友）獎勵天數，預設 3
}

func isEntryState(v string) bool {
	return v == "" || v == "hidden" || v == "locked" || v == "whitelist" || v == "open"
}
func isWhitelist(v string) bool { return len(v) <= 20000 }

// isPct 促銷實付百分比：空(用預設) 或 1..100。
func isPct(v string) bool {
	if v == "" {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 1 && n <= 100
}

// isPct0to100 機率設定：空(用預設) 或 0..100（與 isPct 不同，0 是合法值＝該機率事件永不發生）。
func isPct0to100(v string) bool {
	if v == "" {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 0 && n <= 100
}

// isPosIntMax 回傳一個驗證器：空字串(用程式內建預設)或 1..max 的整數。用於後台可調的數值型設定
// （如城市探索每日打卡上限、冷卻小時數），避免誤填 0 或負數把功能鎖死。
func isPosIntMax(max int) func(string) bool {
	return func(v string) bool {
		if v == "" {
			return true
		}
		n, err := strconv.Atoi(v)
		return err == nil && n >= 1 && n <= max
	}
}

// isCancellationPolicyJSON 驗證取消退費政策 JSON（空字串＝清空、退回程式內建預設）。
// 不引用 race 套件的 CancellationPolicy 型別——race 套件已 import 本套件（appsettings.GetString），
// 若這裡回頭 import race 會形成循環依賴，因此用同形狀的匿名結構體獨立驗證。
func isCancellationPolicyJSON(v string) bool {
	if v == "" {
		return true
	}
	var p struct {
		DeadlineDays int `json:"deadline_days"`
		Tiers        []struct {
			DaysBefore int `json:"days_before"`
			Ratio      int `json:"ratio"`
		} `json:"tiers"`
	}
	if err := json.Unmarshal([]byte(v), &p); err != nil {
		return false
	}
	if p.DeadlineDays < 0 {
		return false
	}
	for _, t := range p.Tiers {
		if t.DaysBefore < 0 || t.Ratio < 0 || t.Ratio > 100 {
			return false
		}
	}
	return true
}

// publicKeys 允許未登入前台讀取的 key（皆為非敏感外觀設定）。
var publicKeys = map[string]bool{"active_skin": true, "favicon_url": true}

func isNonNegInt(v string) bool {
	if v == "" {
		return true
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= 0
}

type Handler struct {
	db *pgxpool.Pool
	rt *realtime.Manager
}

func NewHandler(db *pgxpool.Pool, rt *realtime.Manager) *Handler { return &Handler{db: db, rt: rt} }

// AdminRouter 掛 /admin/app-settings（需 settings 權限）
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Put("/{key}", h.Set)
	return r
}

func (h *Handler) queryAll(ctx context.Context, publicOnly bool) map[string]string {
	m := map[string]string{}
	rows, err := h.db.Query(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return m
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil && (!publicOnly || publicKeys[k]) {
			m[k] = v
		}
	}
	return m
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"settings": h.queryAll(r.Context(), false)})
}

// Public 前台（可未登入）讀取白名單設定，如 active_skin。
func (h *Handler) Public(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"settings": h.queryAll(r.Context(), true)})
}

func (h *Handler) Set(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	validate, known := specs[key]
	if !known {
		respondErr(w, http.StatusBadRequest, "unknown setting")
		return
	}
	var b struct {
		Value string `json:"value"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	val := strings.TrimSpace(b.Value)
	if !validate(val) {
		respondErr(w, http.StatusBadRequest, "invalid value")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1,$2,NOW())
		 ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW()`,
		key, val); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	invalidateSettingsCache()
	h.rt.PublishData(r.Context(), "settings", nil)
	respondJSON(w, http.StatusOK, map[string]any{"settings": h.queryAll(r.Context(), false)})
}

// ---- 套件內記憶體快取（60 秒 TTL）----
//
// Dashboard 等熱路徑一次載入會呼叫 GetString/GetInt 多達 8-16 次，每次都現查 DB 對 1000 人併發
// 會放大成大量重複的單列查詢。這裡加一層 process-local 快取：命中且未過期直接回傳，未命中/過期
// 才查 DB。查無此 key（真的不存在）也快取（negative cache），否則不存在的 key 每次都會穿透查 DB。
// Set 寫入成功後整個清快取，讓下次讀取重新取得最新值；PublishData('settings') 廣播邏輯不受影響。

// settingsCacheTTL 快取有效期。
const settingsCacheTTL = 60 * time.Second

// settingsCacheEntry 一筆快取項。found 區分「DB 裡真的沒有這個 key（negative cache）」與
// 「值存在但為空字串」，避免用 zero value（空字串）混淆兩種情況。
type settingsCacheEntry struct {
	value string
	found bool
	at    time.Time
}

var settingsCache = struct {
	mu sync.RWMutex
	m  map[string]settingsCacheEntry
}{m: make(map[string]settingsCacheEntry)}

// isSettingsCacheExpired 純函式（不依賴 DB/全域狀態），判斷快取項是否已過期，方便單元測試。
// at 為零值視為「從未快取過」，一律視為過期。
func isSettingsCacheExpired(at, now time.Time, ttl time.Duration) bool {
	if at.IsZero() {
		return true
	}
	return now.Sub(at) >= ttl
}

// getCachedSetting 讀快取；命中且未過期才回傳 ok=true。
func getCachedSetting(key string) (settingsCacheEntry, bool) {
	settingsCache.mu.RLock()
	e, ok := settingsCache.m[key]
	settingsCache.mu.RUnlock()
	if !ok || isSettingsCacheExpired(e.at, time.Now(), settingsCacheTTL) {
		return settingsCacheEntry{}, false
	}
	return e, true
}

func setCachedSetting(key, value string, found bool) {
	settingsCache.mu.Lock()
	settingsCache.m[key] = settingsCacheEntry{value: value, found: found, at: time.Now()}
	settingsCache.mu.Unlock()
}

// invalidateSettingsCache 整個清空快取（Set 寫入成功後呼叫），簡單優先，避免只清單一 key 時
// 遺漏邊界情況。設定變更頻率低，清空後下一輪讀取重新查 DB 的成本可忽略。
func invalidateSettingsCache() {
	settingsCache.mu.Lock()
	settingsCache.m = make(map[string]settingsCacheEntry)
	settingsCache.mu.Unlock()
}

// getRawSetting 讀 key 對應的原始字串值（未 trim、未套用預設值），供 GetInt/GetString 共用。
// 命中快取（含 negative cache）直接回傳；未命中才查 DB 並回填快取。回傳 found=false 代表
// 「DB 裡沒有這個 key」，呼叫端應回傳自己的預設值。
//
// 只在確定查無資料（pgx.ErrNoRows）時才寫入 negative cache；其他錯誤（如 DB 暫時不可用）不快取，
// 讓下一次呼叫有機會在 DB 恢復後立刻拿到正確值，不必等滿一輪 TTL。
func getRawSetting(ctx context.Context, db *pgxpool.Pool, key string) (string, bool) {
	if e, ok := getCachedSetting(key); ok {
		return e.value, e.found
	}
	var v string
	err := db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			setCachedSetting(key, "", false)
		}
		return "", false
	}
	setCachedSetting(key, v, true)
	return v, true
}

// GetInt 讀整數設定；查無/解析失敗回 def。
func GetInt(ctx context.Context, db *pgxpool.Pool, key string, def int) int {
	v, found := getRawSetting(ctx, db, key)
	if !found {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

// GetString 讀字串設定；查無/空值回 def。
func GetString(ctx context.Context, db *pgxpool.Pool, key, def string) string {
	v, found := getRawSetting(ctx, db, key)
	if !found {
		return def
	}
	if v = strings.TrimSpace(v); v == "" {
		return def
	}
	return v
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]any{"error": msg})
}
