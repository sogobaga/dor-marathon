// strategies.go：DORPG P9（CONTRACT §1/§2/§4、WIRE）AI 戰鬥策略——rpg_ai_strategies 表的
// pgx 讀寫、StrategyDTO、STRATEGY_IDS 白名單常數。行為本身寫在引擎 engine/strategies.ts 的
// registry（後端不重複實作任何策略邏輯），這裡只管顯示名稱/說明/參數覆寫/啟用/排序——新增
// 行為＝引擎 registry 加一項＋這裡的 STRATEGY_IDS 加一個常數＋migration seed 一列，三處一起改
// 才算完成（CONTRACT §1「不能新增引擎沒有的行為」）。後台 CRUD 在 strategies_admin.go；
// PresetDTO/PUT /rpg/auto-battle 的資格檢查在 presets.go/handler.go。
package rpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// STRATEGY_IDS CONTRACT §1/WIRE「後端持有同一份 id 清單常數」：與引擎 engine/strategies.ts 的
// STRATEGY_IDS 同一份六個 id（順序無意義，純白名單）。未知 id 一律退回 DefaultStrategyID。
//
//nolint:revive // 常數命名對齊 WIRE.md 文件用語（大寫底線），跨語言契約刻意保持一致的拼寫。
var STRATEGY_IDS = []string{
	"balanced", "mp_conserve", "skill_aggressive", "protect_allies", "focus_fire", "element_advantage",
}

// DefaultStrategyID CONTRACT §1「未知 id 一律退回 balanced」；也是新腳本/新角色沒有明確指定
// 策略時的預設值（migration 186：兩個資料表的 strategy_id/auto_strategy_id 欄位都 DEFAULT
// 'balanced'）。
const DefaultStrategyID = "balanced"

// isKnownStrategyID STRATEGY_IDS 純白名單成員檢查（不查 DB／不看 is_active）——validate 端點、
// PUT /rpg/auto-battle、後台 PUT /admin/rpg/ai-strategies 共用同一份判斷式。
func isKnownStrategyID(id string) bool {
	for _, s := range STRATEGY_IDS {
		if s == id {
			return true
		}
	}
	return false
}

// StrategyRow rpg_ai_strategies 資料列。
type StrategyRow struct {
	ID          string
	Name        string
	Description string
	Params      map[string]any
	IsActive    bool
	SortOrder   int
}

const strategyCols = `id, name, description, params, is_active, sort_order`

func scanStrategy(row pgx.Row) (StrategyRow, error) {
	var s StrategyRow
	var raw []byte
	if err := row.Scan(&s.ID, &s.Name, &s.Description, &raw, &s.IsActive, &s.SortOrder); err != nil {
		return s, err
	}
	s.Params = map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.Params); err != nil {
			s.Params = map[string]any{} // 壞掉的 JSON 不該讓整支 API 500（比照既有 scanX 慣例）
		}
	}
	return s, nil
}

// listActiveStrategies GET /rpg/tavern 的 strategies[]（WIRE：「只含 is_active，依 sort_order」），
// 也是戰鬥 bootstrap config.aiStrategies 的資料來源。
func (h *Handler) listActiveStrategies(ctx context.Context) ([]StrategyRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+strategyCols+` FROM rpg_ai_strategies WHERE is_active ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrategyRow{}
	for rows.Next() {
		s, err := scanStrategy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// listAllStrategies 後台 GET /admin/rpg/ai-strategies 用（含停用），依 sort_order。
func (h *Handler) listAllStrategies(ctx context.Context) ([]StrategyRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+strategyCols+` FROM rpg_ai_strategies ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StrategyRow{}
	for rows.Next() {
		s, err := scanStrategy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) getStrategy(ctx context.Context, id string) (StrategyRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+strategyCols+` FROM rpg_ai_strategies WHERE id=$1`, id)
	return scanStrategy(row)
}

// isActiveStrategyID CONTRACT §3「strategy_id：unknown_strategy（不存在或 is_active=false）」的
// DB 那一半（isKnownStrategyID 只查白名單，不連 DB）。查無資料視為不合法（回 false, nil），
// 不是系統錯誤。
func (h *Handler) isActiveStrategyID(ctx context.Context, id string) (bool, error) {
	s, err := h.getStrategy(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return s.IsActive, nil
}

// checkStrategyID presets.go（CreatePreset/UpdatePreset/PresetsValidate）與 handler.go
// （PutAutoBattle）共用：id 必須先在 STRATEGY_IDS 白名單、再確認 DB 裡 is_active。回傳非 nil
// 的 *PresetError 代表「unknown_strategy」（呼叫端自行決定怎麼包裝成回應：presets.go 併入
// errors[] 陣列，handler.go 直接 400）。
func (h *Handler) checkStrategyID(ctx context.Context, id string) (*PresetError, error) {
	if !isKnownStrategyID(id) {
		e := presetErr("strategy_id", "unknown_strategy", "未知的 AI 戰鬥策略")
		return &e, nil
	}
	active, err := h.isActiveStrategyID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !active {
		e := presetErr("strategy_id", "unknown_strategy", "未知的 AI 戰鬥策略")
		return &e, nil
	}
	return nil, nil
}

func (h *Handler) upsertStrategy(ctx context.Context, s StrategyRow) error {
	params := s.Params
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO rpg_ai_strategies (id, name, description, params, is_active, sort_order, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT (id) DO UPDATE SET
			name=$2, description=$3, params=$4, is_active=$5, sort_order=$6, updated_at=NOW()`,
		s.ID, s.Name, s.Description, raw, s.IsActive, s.SortOrder)
	return err
}

// strategyInUse 後台 DELETE 用：DB 的兩個 FK（rpg_companion_presets.strategy_id／
// player_characters.auto_strategy_id）本來就會擋刪除，這裡先明確查一次，好回乾淨的 409
// in_use，而不是讓呼叫端收到裸的 FK 違規錯誤。
func (h *Handler) strategyInUse(ctx context.Context, id string) (bool, error) {
	var n int
	if err := h.db.QueryRow(ctx, `
		SELECT (SELECT COUNT(*) FROM rpg_companion_presets WHERE strategy_id=$1) +
		       (SELECT COUNT(*) FROM player_characters WHERE auto_strategy_id=$1)`, id).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func (h *Handler) deleteStrategy(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_ai_strategies WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// errStrategiesNotReady migration 186 尚未套用時（rpg_ai_strategies 表不存在）的固定 503
// 訊息，比照既有 errJobsNotReady/errWeaponsNotReady/errArmorNotReady 慣例。
const errStrategiesNotReady = "AI 戰鬥策略尚未初始化（migration 186 未套用）"

// StrategyDTO WIRE：GET /rpg/tavern strategies[]、後台 GET/PUT /admin/rpg/ai-strategies。
// 2026-09-19 修復：原本漏帶 IsActive，後台編輯表單（PUT /admin/rpg/ai-strategies）若照抓 GET
// 回來的 DTO 原樣送回（一般表單既有寫法：讀什麼、改一改、原樣送回），IsActive 永遠讀到
// Go 零值 false，等於「每次編輯都把這條策略靜默停用」——回應必須帶上這個欄位，後台才有值可以
// 原樣往返。
type StrategyDTO struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Params      map[string]any `json:"params"`
	IsActive    bool           `json:"is_active"`
	SortOrder   int            `json:"sort_order"`
}

func toStrategyDTO(s StrategyRow) StrategyDTO {
	params := s.Params
	if params == nil {
		params = map[string]any{}
	}
	return StrategyDTO{ID: s.ID, Name: s.Name, Description: s.Description, Params: params, IsActive: s.IsActive, SortOrder: s.SortOrder}
}

// wireStrategyParams 戰鬥 bootstrap config.aiStrategies 的單一 id 對應值（WIRE：
// `{ [id]: { params } }`）。
type wireStrategyParams struct {
	Params map[string]any `json:"params"`
}

// buildAiStrategiesWire 戰鬥 bootstrap config.aiStrategies（WIRE：「引擎預設 ⊕ DB 覆寫；只含
// is_active 的 id」）——這裡只負責把目前生效中的策略 id→params 攤平成 map；引擎那邊拿
// STRATEGY_IDS 的內建預設值跟這份 DB 覆寫做 merge（resolveStrategy），後端不重複維護一份引擎
// 預設參數表。
func buildAiStrategiesWire(strategies []StrategyRow) map[string]wireStrategyParams {
	out := make(map[string]wireStrategyParams, len(strategies))
	for _, s := range strategies {
		params := s.Params
		if params == nil {
			params = map[string]any{}
		}
		out[s.ID] = wireStrategyParams{Params: params}
	}
	return out
}
