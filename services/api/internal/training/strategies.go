// 賽事策略（配速計劃＋補給計劃）——自主訓練新分頁；開跑時前端帶 /track?strategy=<id> 進入
// 「比賽專注模式」（半透明黑底大字資訊＋配速/補給提醒）。比照本套件 v0.1.565 起的慣例：清單/單筆
// 唯讀端點對非 VIP 開放（requireLogin），建立/修改/刪除為 VIP 限定（requireVIP）；每帳號最多
// strategyLimit 份，後端把關（超過回 409 strategy_limit）。契約見 apps/web/src/lib/api.ts
// strategiesApi／StrategySegment／FuelPoint／RaceStrategy，已定案不可改動。
package training

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// strategyLimit 每帳號最多同時保留的賽事策略數（POST /training/strategies 超過即 409 strategy_limit，
// 契約已定案見 apps/web/src/lib/api.ts strategiesApi 註解）。
const strategyLimit = 5

const (
	strategyNameMax     = 50     // name 去空白後上限字數
	strategySegMax      = 30     // segments 上限段數
	strategyFuelMax     = 30     // fuel 上限點數
	strategyPaceMinS    = 120    // pace_s 下限（秒/公里，約 3:20/km，涵蓋菁英）
	strategyPaceMaxS    = 1800   // pace_s 上限（秒/公里，30:00/km，涵蓋健走）
	strategyFuelTimeMax = 86400  // fuel mode=time 的 at 上限（秒，24 小時，涵蓋超馬關門時間）
	strategyFuelDistMax = 200000 // fuel mode=distance 的 at 上限（公尺，200K，涵蓋超馬距離）
)

// validFuelKind／validFuelMode：FuelPoint 白名單（契約見 apps/web/src/lib/api.ts FuelKind）。
var validFuelKind = map[string]bool{"gel": true, "salt": true, "electrolyte": true, "caffeine": true}
var validFuelMode = map[string]bool{"time": true, "distance": true}

// StrategySegment 配速段：from_km 由前一段 to_km 銜接（首段固定 0），pace_s=目標配速（秒/公里）
// （契約見 apps/web/src/lib/api.ts StrategySegment，欄位不可改動）。
type StrategySegment struct {
	FromKm float64 `json:"from_km"`
	ToKm   float64 `json:"to_km"`
	PaceS  int     `json:"pace_s"`
}

// FuelPoint 補給點：mode="time" 時 at=開跑後秒數；mode="distance" 時 at=移動距離公尺
// （契約見 apps/web/src/lib/api.ts FuelPoint，欄位不可改動）。
type FuelPoint struct {
	Kind string  `json:"kind"`
	Mode string  `json:"mode"`
	At   float64 `json:"at"`
}

// RaceStrategy 一份賽事策略（契約見 apps/web/src/lib/api.ts RaceStrategy）。TotalKm 為冗餘欄位，由
// 後端依 Segments 最後一段 to_km 計算寫入（見 validateStrategy），不信任前端傳入值，供列表顯示與
// track 頁 ETA 計算用。
type RaceStrategy struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	TotalKm   float64           `json:"total_km"`
	Segments  []StrategySegment `json:"segments"`
	Fuel      []FuelPoint       `json:"fuel"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// strategyRequest POST/PUT /training/strategies 請求體（契約見 apps/web/src/lib/api.ts
// strategiesApi.create/update body）。
type strategyRequest struct {
	Name     string            `json:"name"`
	Segments []StrategySegment `json:"segments"`
	Fuel     []FuelPoint       `json:"fuel"`
}

// normalizeStrategyRequest 把可能為 nil 的 slice 欄位（fuel 可 0 筆＝合法，若請求體省略該欄位
// json.Decode 會留 nil）正規化成空 slice——避免下游 json.Marshal(nil) 產生 JSON "null" 寫入 DB，
// 使儲存值與 API 回傳值都能維持「fuel 一律是陣列，不會是 null」（契約 apps/web/src/lib/api.ts
// FuelPoint[]，前端據此直接 .map() 不另外判空）。
func normalizeStrategyRequest(req *strategyRequest) {
	if req.Fuel == nil {
		req.Fuel = []FuelPoint{}
	}
}

// validateStrategy 驗證 name/segments/fuel，回傳 trim 後的 name 與計算出的 total_km（＝segments 最後
// 一段 to_km，後端權威計算、不信前端）；不通過時 errCode 非空、可直接丟給 respondErr。
//
// segments 規則：1~strategySegMax 段；首段 from_km 必為 0；每段 from_km 須與前一段 to_km 連續銜接
// （不可有缺口或重疊）；to_km 須大於 from_km（不可零長度或倒退）；pace_s 落在
// [strategyPaceMinS, strategyPaceMaxS] 合理跑步配速區間內。
// fuel 規則：0~strategyFuelMax 點（選填，0 筆合法）；kind/mode 皆須落在白名單；at 必須為正值，
// 且依 mode 各自有上限（time：一日秒數上限；distance：超馬等級距離上限），避免離譜資料寫入。
func validateStrategy(req strategyRequest) (name string, totalKm float64, errCode string) {
	name = strings.TrimSpace(req.Name)
	if name == "" {
		return "", 0, "invalid_name"
	}
	if len([]rune(name)) > strategyNameMax {
		return "", 0, "invalid_name"
	}

	if len(req.Segments) == 0 || len(req.Segments) > strategySegMax {
		return "", 0, "invalid_segments"
	}
	prevTo := 0.0
	for i, seg := range req.Segments {
		if i == 0 && seg.FromKm != 0 {
			return "", 0, "segments_must_start_at_zero"
		}
		if seg.FromKm != prevTo {
			return "", 0, "segments_not_contiguous"
		}
		if seg.ToKm <= seg.FromKm {
			return "", 0, "invalid_segment_range"
		}
		if seg.PaceS < strategyPaceMinS || seg.PaceS > strategyPaceMaxS {
			return "", 0, "invalid_pace"
		}
		prevTo = seg.ToKm
	}

	if len(req.Fuel) > strategyFuelMax {
		return "", 0, "invalid_fuel"
	}
	for _, f := range req.Fuel {
		if !validFuelKind[f.Kind] {
			return "", 0, "invalid_fuel_kind"
		}
		if !validFuelMode[f.Mode] {
			return "", 0, "invalid_fuel_mode"
		}
		if f.At <= 0 {
			return "", 0, "invalid_fuel_at"
		}
		if f.Mode == "time" && f.At > strategyFuelTimeMax {
			return "", 0, "invalid_fuel_at"
		}
		if f.Mode == "distance" && f.At > strategyFuelDistMax {
			return "", 0, "invalid_fuel_at"
		}
	}

	return name, prevTo, ""
}

// ListStrategies GET /training/strategies — 登入即可讀（唯讀瀏覽，比照本套件 v0.1.565 起慣例）：
// 該帳號的賽事策略清單，依 created_at DESC 排序。
func (h *Handler) ListStrategies(w http.ResponseWriter, r *http.Request) {
	uid := h.requireLogin(w, r)
	if uid == "" {
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT id, name, COALESCE(total_km,0), segments, fuel, created_at, updated_at
		FROM user_race_strategies WHERE user_id=$1 ORDER BY created_at DESC`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	strategies := []RaceStrategy{}
	for rows.Next() {
		s, err := scanStrategyRow(rows)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		strategies = append(strategies, s)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"strategies": strategies, "limit": strategyLimit})
}

// GetStrategy GET /training/strategies/{id} — 登入即可讀；WHERE user_id 防越權，非本人的策略一律
// 404（不洩漏是否存在）。
func (h *Handler) GetStrategy(w http.ResponseWriter, r *http.Request) {
	uid := h.requireLogin(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	row := h.db.QueryRow(r.Context(), `
		SELECT id, name, COALESCE(total_km,0), segments, fuel, created_at, updated_at
		FROM user_race_strategies WHERE id=$1 AND user_id=$2`, id, uid)
	s, err := scanStrategyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"strategy": s})
}

// CreateStrategy POST /training/strategies — VIP 專屬：新增一份賽事策略。超過 strategyLimit 份回 409
// strategy_limit；請求體不合規則回 400（見 validateStrategy）。total_km 由後端計算寫入，不信前端。
func (h *Handler) CreateStrategy(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	ctx := r.Context()

	var req strategyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	normalizeStrategyRequest(&req)
	name, totalKm, errCode := validateStrategy(req)
	if errCode != "" {
		respondErr(w, http.StatusBadRequest, errCode)
		return
	}

	var count int
	if err := h.db.QueryRow(ctx, `SELECT COUNT(*) FROM user_race_strategies WHERE user_id=$1`, uid).Scan(&count); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if count >= strategyLimit {
		respondErr(w, http.StatusConflict, "strategy_limit")
		return
	}

	segmentsJSON, err := json.Marshal(req.Segments)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	fuelJSON, err := json.Marshal(req.Fuel)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	row := h.db.QueryRow(ctx, `
		INSERT INTO user_race_strategies (user_id, name, total_km, segments, fuel)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, name, COALESCE(total_km,0), segments, fuel, created_at, updated_at`,
		uid, name, totalKm, segmentsJSON, fuelJSON)
	s, err := scanStrategyRow(row)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"strategy": s})
}

// UpdateStrategy PUT /training/strategies/{id} — VIP 專屬：整份覆寫（name/segments/fuel），
// total_km 重新計算。WHERE user_id 防越權，非本人的策略一律 404。
func (h *Handler) UpdateStrategy(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	var req strategyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	normalizeStrategyRequest(&req)
	name, totalKm, errCode := validateStrategy(req)
	if errCode != "" {
		respondErr(w, http.StatusBadRequest, errCode)
		return
	}

	segmentsJSON, err := json.Marshal(req.Segments)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	fuelJSON, err := json.Marshal(req.Fuel)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	row := h.db.QueryRow(ctx, `
		UPDATE user_race_strategies
		SET name=$1, total_km=$2, segments=$3, fuel=$4, updated_at=NOW()
		WHERE id=$5 AND user_id=$6
		RETURNING id, name, COALESCE(total_km,0), segments, fuel, created_at, updated_at`,
		name, totalKm, segmentsJSON, fuelJSON, id, uid)
	s, err := scanStrategyRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"strategy": s})
}

// DeleteStrategy DELETE /training/strategies/{id} — VIP 專屬。WHERE user_id 防越權；比照本套件
// DeleteSchedule/DeletePlan 慣例，冪等（本來就不存在／非本人的也一律回 ok，不特別區分）。
func (h *Handler) DeleteStrategy(w http.ResponseWriter, r *http.Request) {
	uid := h.requireVIP(w, r)
	if uid == "" {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM user_race_strategies WHERE id=$1 AND user_id=$2`, id, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// strategyRowScanner 讓 scanStrategyRow 同時支援 pgx.Row（QueryRow）與 pgx.Rows（Query 逐列）。
type strategyRowScanner interface {
	Scan(dest ...any) error
}

// scanStrategyRow 掃描一列 user_race_strategies 到 RaceStrategy；segments/fuel 先掃進 []byte 原始
// JSON 再 unmarshal 成型別化 slice（比照本套件 admin_training.go／personaltask 的 JSONB 慣例，避免
// 依賴 pgx 對任意 struct/slice 目的地的隱式 JSON scan 行為）。unmarshal 失敗時以空 slice 兜底而非
// 中斷整支請求——理論上不會發生（寫入路徑一律經 json.Marshal 型別化 slice），僅作防禦。
func scanStrategyRow(row strategyRowScanner) (RaceStrategy, error) {
	var s RaceStrategy
	var segmentsRaw, fuelRaw []byte
	if err := row.Scan(&s.ID, &s.Name, &s.TotalKm, &segmentsRaw, &fuelRaw, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return RaceStrategy{}, err
	}
	s.Segments = []StrategySegment{}
	_ = json.Unmarshal(segmentsRaw, &s.Segments)
	s.Fuel = []FuelPoint{}
	_ = json.Unmarshal(fuelRaw, &s.Fuel)
	return s, nil
}
