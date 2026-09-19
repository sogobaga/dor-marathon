// strategies_admin.go：DORPG P9 後台「遊戲化」新分頁的 AI 戰鬥策略 CRUD（WIRE：GET/PUT/DELETE
// /admin/rpg/ai-strategies），比照 armor_admin.go/weapons_admin.go 既有結構——main.go 把這個
// Router 掛在 "/admin/rpg/ai-strategies" 靜態子路徑，跟既有 "/admin/rpg" mount 共存，沿用同一個
// perm("rpg")。
package rpg

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// AdminAiStrategiesRouter GET "/" 列表（含停用）、PUT "/" 單筆 upsert、DELETE "/{id}"。
func (h *Handler) AdminAiStrategiesRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListAiStrategies)
	r.Put("/", h.AdminPutAiStrategy)
	r.Delete("/{id}", h.AdminDeleteAiStrategy)
	return r
}

func (h *Handler) AdminListAiStrategies(w http.ResponseWriter, r *http.Request) {
	list, err := h.listAllStrategies(r.Context())
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list strategies")
		return
	}
	dtos := make([]StrategyDTO, 0, len(list))
	for _, s := range list {
		dtos = append(dtos, toStrategyDTO(s))
	}
	// INTEGRATOR：回應鍵名比照既有姊妹端點慣例（armor-items→armor_items、weapons→weapons、
	// weapon-types→weapon_types，URL 斜線段落直接轉底線）——原本寫成 "strategies" 與
	// apps/web/src/lib/api.ts 的 adminRpgApi.aiStrategies() 已經在讀的 "ai_strategies" 不一致，
	// 會讓後台 AI 策略分頁載入後清單永遠是 undefined（WIRE 未明講此鍵名，故以既有慣例＋前端已寫
	// 的期待值為準）。
	respondJSON(w, http.StatusOK, map[string]any{"ai_strategies": dtos})
}

// aiStrategyPutBody PUT /admin/rpg/ai-strategies 的請求 body（WIRE）。
type aiStrategyPutBody struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Params      map[string]any `json:"params"`
	IsActive    bool           `json:"is_active"`
	SortOrder   int            `json:"sort_order"`
}

// AdminPutAiStrategy WIRE：「id 必須在引擎 registry 白名單（後端持有同一份 id 清單常數）否則
// 400 unknown_strategy」——後台只能調整六個既定策略的顯示名稱/說明/參數覆寫/啟用/排序，不能
// 新增引擎沒有實作的第七個 id（CONTRACT §1）。
func (h *Handler) AdminPutAiStrategy(w http.ResponseWriter, r *http.Request) {
	var body aiStrategyPutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !isKnownStrategyID(body.ID) {
		respondErr(w, http.StatusBadRequest, "unknown_strategy")
		return
	}
	if body.Name == "" {
		respondErr(w, http.StatusBadRequest, "name 不可為空")
		return
	}
	row := StrategyRow{
		ID: body.ID, Name: body.Name, Description: body.Description,
		Params: body.Params, IsActive: body.IsActive, SortOrder: body.SortOrder,
	}
	if err := h.upsertStrategy(r.Context(), row); err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, toStrategyDTO(row))
}

// AdminDeleteAiStrategy WIRE：「DELETE 只允許非 balanced 且無腳本／玩家引用，否則 409
// in_use」。balanced 是「未知 id 一律退回」的最終備援，刪除它會讓容錯路徑本身失去意義，故一律
// 擋在最前面，不必查使用量。
func (h *Handler) AdminDeleteAiStrategy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == DefaultStrategyID {
		respondErr(w, http.StatusConflict, "in_use")
		return
	}
	ctx := r.Context()
	inUse, err := h.strategyInUse(ctx, id)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to check usage")
		return
	}
	if inUse {
		respondErr(w, http.StatusConflict, "in_use")
		return
	}
	ok, err := h.deleteStrategy(ctx, id)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		// 防禦性後備：上面 strategyInUse 已經先查過一次，但兩次查詢之間若有併發寫入造成
		// TOCTOU（另一個請求剛好在這中間把腳本/角色的 strategy_id 改成這個 id），DB 的 FK
		// 仍會擋下這次 DELETE——一併轉成乾淨的 409 in_use，不要讓呼叫端看到裸的 FK 違規訊息。
		if isForeignKeyViolation(err) {
			respondErr(w, http.StatusConflict, "in_use")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}
	if !ok {
		respondErr(w, http.StatusNotFound, "not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
