// ranks_admin.go：DORPG P11（CONTRACT §1/§5、WIRE）後台「怪物強度」分頁——GET/PUT
// /admin/rpg/monster-ranks。九級本身固定（migration 189 seed），本輪只開放編輯既有九列的
// 倍率/level_curve/summon/badge_color/description，不做新增/刪除——比照 strategies_admin.go/
// jobs.go 的既有分工（只有 GET+PUT，沒有 DELETE），main.go 掛 perm("rpg")。
package rpg

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// AdminMonsterRanksRouter GET "/" 列表（九列，含 summon/level_curve/calibrated_note）、
// PUT "/" 單筆更新既有列。
func (h *Handler) AdminMonsterRanksRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListMonsterRanks)
	r.Put("/", h.AdminPutMonsterRank)
	return r
}

func (h *Handler) AdminListMonsterRanks(w http.ResponseWriter, r *http.Request) {
	list, err := h.listRanks(r.Context())
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errRanksNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to list monster ranks")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"monster_ranks": list})
}

// adminMonsterRankPutBody WIRE：「PUT body { rank, label, sort_order, hp_mult, atk_mult,
// def_mult, mdef_mult, level_curve, summon, badge_color, description }」——刻意不含
// calibrated_note（見 ranks.go upsertRank 註解：這欄由 migration 189 種好，PUT 不能覆寫）。
type adminMonsterRankPutBody struct {
	Rank        string             `json:"rank"`
	Label       string             `json:"label"`
	SortOrder   int                `json:"sort_order"`
	HPMult      float64            `json:"hp_mult"`
	AtkMult     float64            `json:"atk_mult"`
	DefMult     float64            `json:"def_mult"`
	MdefMult    float64            `json:"mdef_mult"`
	LevelCurve  map[string]float64 `json:"level_curve"`
	Summon      SummonConfig       `json:"summon"`
	BadgeColor  string             `json:"badge_color"`
	Description string             `json:"description"`
}

// AdminPutMonsterRank WIRE：「rank 固定九值，不可新增刪除；倍率 > 0；summon.waves[].rank
// 必須存在且 count 1–5、at_hp_pct 1–99」——全部委由 RankRow.Validate()／SummonConfig.Validate()
// 判斷（ranks.go），這裡只負責解析請求與組裝回應。
func (h *Handler) AdminPutMonsterRank(w http.ResponseWriter, r *http.Request) {
	var body adminMonsterRankPutBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	curve := body.LevelCurve
	if curve == nil {
		curve = map[string]float64{}
	}
	row := RankRow{
		Rank: body.Rank, Label: body.Label, SortOrder: body.SortOrder,
		HPMult: body.HPMult, AtkMult: body.AtkMult, DefMult: body.DefMult, MdefMult: body.MdefMult,
		LevelCurve: curve, Summon: body.Summon,
		BadgeColor: body.BadgeColor, Description: body.Description,
	}
	if err := row.Validate(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	if err := h.upsertRank(ctx, row); err != nil {
		if respondIfMissingRelationMsg(w, err, errRanksNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	// 重新查一次回傳完整列（含 upsertRank 刻意保留未動的 calibrated_note），而不是直接把請求
	// body 原樣送回——理由同 AdminPutJob 對 getJobByIDFull 的既有作法：PUT 回應應該反映 DB
	// 實際存的內容，不是「使用者以為存了什麼」。
	full, err := h.getRank(ctx, row.Rank)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load monster rank")
		return
	}
	respondJSON(w, http.StatusOK, full)
}
