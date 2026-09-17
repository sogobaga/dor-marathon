// skills.go：DORPG P5 技能端點——GET /rpg/skills（目前職業的技能樹＋等級＋可升條件）、
// POST /rpg/skills/allocate（+1/-1/max）、POST /rpg/skills/reset（目前職業全部歸零）。
// ExpandEffect 是本檔的核心純函式：把 rpg_skills.effect（base+per_level）依「目前等級」展開
// 成即時數值，battle.go bootstrap 的技能欄與這裡的 GET /rpg/skills 共用同一份展開邏輯，避免
// 兩處分別實作公式後彼此漂移（見 CONTRACT §6「技能等級進戰鬥」）。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// EffectAtLevel 技能在某個等級的即時數值（WIRE：GET /rpg/skills 的 effect_at_level/
// effect_next_level，以及戰鬥 bootstrap wireSkill.effect）。欄位皆為選填，依 Kind 而定哪些
// 有意義：damage/heal/shield 用 Coef/Flat/Hits，buff/debuff/passive 用 Stat/Value，special
// 兩者都不展開（未實裝，只有 Kind 有意義）。
type EffectAtLevel struct {
	Kind       string  `json:"kind"`
	Stat       string  `json:"stat,omitempty"`
	Value      float64 `json:"value,omitempty"`
	DurationMs int     `json:"duration_ms,omitempty"`
	Coef       float64 `json:"coef,omitempty"`
	Flat       float64 `json:"flat,omitempty"`
	Hits       int     `json:"hits,omitempty"`
	Target     string  `json:"target,omitempty"`
	MPCost     float64 `json:"mp_cost,omitempty"`
	Element    string  `json:"element,omitempty"`
}

// ExpandEffect 依技能目前等級展開 base+per_level×(lv-1) 為即時數值。level<=0 時比照 lv=1 預覽
// （WIRE：「level=0 時用 lv=1 預覽」）——GET /rpg/skills 的 effect_at_level（level=0，尚未配點）、
// battle.go bootstrap（只送 level>=1 的技能，理論上不會吃到 <=0，這裡仍防呆）、
// content_repo.go loadPassives（被動技能加成）三處共用。
func ExpandEffect(s SkillRow, level int) EffectAtLevel {
	lv := level
	if lv <= 0 {
		lv = 1
	}
	steps := float64(lv - 1)
	e := s.Effect

	out := EffectAtLevel{
		Kind:       s.Kind,
		Target:     e.Target,
		Element:    s.Element,
		DurationMs: e.DurationMs,
		MPCost:     float64(s.MPCost) + s.MPCostPerLevel*steps,
	}
	if out.Target == "" {
		out.Target = s.Target // effect JSONB 沒填 target 時退回頂層欄位（既有 5 個技能列的慣例）
	}

	switch s.Kind {
	case "damage", "heal", "shield":
		out.Coef = e.CoefBase + e.CoefPerLevel*steps
		out.Flat = e.FlatBase + e.FlatPerLevel*steps
		out.Hits = e.Hits
		if out.Hits <= 0 {
			out.Hits = 1
		}
	case "buff", "debuff", "passive":
		out.Stat = e.Stat
		out.Value = e.ValueBase + e.ValuePerLevel*steps
	case "special":
		// 未實裝：CONTRACT §5 明講「不進行任何數值展開，前端只顯示文字」。
	}
	return out
}

// lvPreviewText WIRE lv_preview 的單一等級文字（{"1":...,"5":...,"10":...}）。刻意不依賴
// display_text 裡的任何佔位符語法（技能 seed 由另一個 Workflow 產生，本檔寫death時還看不到
// 實際內容）——用 ExpandEffect 展開出的數字自己組一段通用格式，displayText 只是附加的風味文字。
func lvPreviewText(s SkillRow, lv int) string {
	e := ExpandEffect(s, lv)
	var numeric string
	switch s.Kind {
	case "damage":
		numeric = fmt.Sprintf("倍率 %.2f", e.Coef)
		if e.Flat != 0 {
			numeric += fmt.Sprintf("、固定傷害 +%.0f", e.Flat)
		}
		if e.Hits > 1 {
			numeric += fmt.Sprintf("、%d 段", e.Hits)
		}
	case "heal":
		numeric = fmt.Sprintf("倍率 %.2f、治療 +%.0f", e.Coef, e.Flat)
	case "shield":
		numeric = fmt.Sprintf("護盾量 %.0f", e.Flat)
	case "buff", "debuff", "passive":
		numeric = fmt.Sprintf("%s %+.1f", e.Stat, e.Value)
	}
	switch {
	case numeric == "":
		return s.DisplayText
	case s.DisplayText == "":
		return numeric
	default:
		return s.DisplayText + "（" + numeric + "）"
	}
}

// SkillDTO GET /rpg/skills 的單一技能列（WIRE §「會員端 REST」SkillDTO）。
type SkillDTO struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Path            string            `json:"path"`
	Tier            int               `json:"tier"`
	Kind            string            `json:"kind"`
	DmgType         string            `json:"dmg_type"`
	Element         string            `json:"element"`
	Target          string            `json:"target"`
	MaxLevel        int               `json:"max_level"`
	Level           int               `json:"level"`
	PrereqSkillID   *string           `json:"prereq_skill_id"`
	PrereqLevel     int               `json:"prereq_level"`
	PrereqOK        bool              `json:"prereq_ok"`
	CanLevelUp      bool              `json:"can_level_up"`
	MPCost          int               `json:"mp_cost"`
	MPCostPerLevel  float64           `json:"mp_cost_per_level"`
	CooldownMs      int               `json:"cooldown_ms"`
	CastMs          int               `json:"cast_ms"`
	DisplayText     string            `json:"display_text"`
	Implemented     bool              `json:"implemented"`
	EffectAtLevel   EffectAtLevel     `json:"effect_at_level"`
	EffectNextLevel *EffectAtLevel    `json:"effect_next_level"`
	LvPreview       map[string]string `json:"lv_preview"`
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// skillsResponse GET /rpg/skills 的完整回應形狀，POST .../allocate、.../reset 成功後也回這個
// （WIRE：「回 /rpg/skills 同形」）。
type skillsResponse struct {
	Job              *JobRow    `json:"job"`
	Skills           []SkillDTO `json:"skills"`
	SkillPointsTotal int        `json:"skill_points_total"`
	SkillPointsFree  int        `json:"skill_points_free"`
}

// buildSkillsResponse 目前職業的技能樹＋玩家等級＋已配點狀態 → 完整 DTO 清單。ch.JobID 為 nil
// 時 skills 為空陣列（WIRE 明講）。
func (h *Handler) buildSkillsResponse(ctx context.Context, cfg Config, ch character, effLevel int) (skillsResponse, error) {
	if ch.JobID == nil {
		return skillsResponse{Job: nil, Skills: []SkillDTO{}, SkillPointsTotal: 0, SkillPointsFree: 0}, nil
	}
	job, err := h.getJobByID(ctx, *ch.JobID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return skillsResponse{Job: nil, Skills: []SkillDTO{}, SkillPointsTotal: 0, SkillPointsFree: 0}, nil
		}
		return skillsResponse{}, err
	}
	skills, err := h.listSkillsByJob(ctx, *ch.JobID)
	if err != nil {
		return skillsResponse{}, err
	}
	ids := make([]string, len(skills))
	for i, s := range skills {
		ids[i] = s.ID
	}
	levels, err := h.getPlayerSkillLevels(ctx, ch.UserID, ids)
	if err != nil {
		return skillsResponse{}, err
	}

	skillTotal := TotalSkillPoints(cfg, effLevel)
	spent := 0
	for _, lv := range levels {
		spent += lv
	}
	skillFree := skillTotal - spent
	if skillFree < 0 {
		skillFree = 0
	}

	dtos := make([]SkillDTO, 0, len(skills))
	for _, s := range skills {
		lvl := levels[s.ID]
		prereqOK := true
		if s.PrereqSkillID != nil {
			prereqOK = levels[*s.PrereqSkillID] >= s.PrereqLevel
		}
		canLevelUp := s.Implemented && prereqOK && lvl < s.MaxLevel && skillFree > 0

		var nextEffect *EffectAtLevel
		if lvl < s.MaxLevel {
			e := ExpandEffect(s, lvl+1)
			nextEffect = &e
		}

		dtos = append(dtos, SkillDTO{
			ID: s.ID, Name: s.Name, Path: s.Path, Tier: s.Tier, Kind: s.Kind, DmgType: s.DmgType,
			Element: s.Element, Target: s.Target, MaxLevel: s.MaxLevel, Level: lvl,
			PrereqSkillID: s.PrereqSkillID, PrereqLevel: s.PrereqLevel, PrereqOK: prereqOK,
			CanLevelUp: canLevelUp, MPCost: s.MPCost, MPCostPerLevel: s.MPCostPerLevel,
			CooldownMs: s.CooldownMs, CastMs: s.CastMs, DisplayText: s.DisplayText, Implemented: s.Implemented,
			EffectAtLevel:   ExpandEffect(s, lvl),
			EffectNextLevel: nextEffect,
			LvPreview: map[string]string{
				"1":  lvPreviewText(s, minInt(1, s.MaxLevel)),
				"5":  lvPreviewText(s, minInt(5, s.MaxLevel)),
				"10": lvPreviewText(s, minInt(10, s.MaxLevel)),
			},
		})
	}
	return skillsResponse{Job: &job, Skills: dtos, SkillPointsTotal: skillTotal, SkillPointsFree: skillFree}, nil
}

// respondSkills 共用尾段：查角色列＋等級 → 組 skillsResponse → 回 JSON（GET /rpg/skills 與兩個
// POST 端點成功後都呼叫這支，確保回應形狀一致，比照 handler.go respondMe 的既有慣例）。
func (h *Handler) respondSkills(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	ch, err := h.getOrCreateCharacter(ctx, userID, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	_, effLevel, err := h.loadEffectiveLevel(ctx, userID, cfg, ch.TestLevel)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level")
		return
	}
	resp, err := h.buildSkillsResponse(ctx, cfg, ch, effLevel)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errJobsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load skills")
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

// GET /rpg/skills
func (h *Handler) Skills(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	h.respondSkills(w, r, uid)
}

type skillAllocateRequest struct {
	SkillID string          `json:"skill_id"`
	Delta   json.RawMessage `json:"delta"`
}

// parseSkillDelta delta 可以是數字 1/-1 或字串 "max"（WIRE：`delta: 1|-1|"max"`）。
func parseSkillDelta(raw json.RawMessage) (deltaInt int, isMax bool, ok bool) {
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		if asInt == 1 || asInt == -1 {
			return asInt, false, true
		}
		return 0, false, false
	}
	var asStr string
	if err := json.Unmarshal(raw, &asStr); err == nil && asStr == "max" {
		return 0, true, true
	}
	return 0, false, false
}

// violatesPrereqDependents 審查#3【中低・CONFIRMED】：這個技能（skillID）降級到 newLevel 之後，
// 是否有同職業裡「其他已投資」（level>=1）的技能仍然把它當前置、且要求的 prereq_level 高於
// newLevel。true＝應該拒絕這次降級。抽成獨立純函式方便不連 DB 的單元測試（見 skills_test.go），
// SkillsAllocate 只負責組好 jobSkills/levels 呼叫它。
func violatesPrereqDependents(jobSkills []SkillRow, levels map[string]int, skillID string, newLevel int) bool {
	for _, other := range jobSkills {
		if other.PrereqSkillID == nil || *other.PrereqSkillID != skillID {
			continue
		}
		if levels[other.ID] >= 1 && other.PrereqLevel > newLevel {
			return true
		}
	}
	return false
}

// POST /rpg/skills/allocate {"skill_id":"...", "delta": 1|-1|"max"}
func (h *Handler) SkillsAllocate(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req skillAllocateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.SkillID == "" {
		respondErr(w, http.StatusBadRequest, "invalid skill_id")
		return
	}
	deltaInt, isMax, ok := parseSkillDelta(req.Delta)
	if !ok {
		respondErr(w, http.StatusBadRequest, `delta must be 1, -1 or "max"`)
		return
	}

	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	if ch.JobID == nil {
		respondErr(w, http.StatusBadRequest, "尚未選擇職業")
		return
	}
	skillsByID, err := h.getSkillsByIDs(ctx, []string{req.SkillID})
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errJobsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load skill")
		return
	}
	skill, ok := skillsByID[req.SkillID]
	if !ok {
		respondErr(w, http.StatusNotFound, "skill not found")
		return
	}
	if skill.JobID == nil || *skill.JobID != *ch.JobID {
		respondErr(w, http.StatusBadRequest, "此技能不屬於目前職業")
		return
	}

	// baseLevel 只需要 users.exp，不受這支端點的鎖影響，交易外查詢即可（理由同 handler.go
	// Allocate 的審查#6 修復註解：只有 test_level 需要跟技能等級鎖在同一個交易內一起讀）。
	baseLevel, err := h.baseLevelForUser(ctx, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level")
		return
	}

	jobSkills, err := h.listSkillsByJob(ctx, *ch.JobID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load skills")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 成功路徑會先 Commit，Rollback 在那之後為 no-op

	// 審查#6【極低・PLAUSIBLE】根因修復：test_level 曾經在交易「外」（loadEffectiveLevel）讀取，
	// 跟這裡交易「內」才鎖的技能等級列不是同一個時間點的快照——另一個分頁同時呼叫
	// PUT /rpg/test-level 就可能讓 skillTotal 依過期等級算出。改成跟 player_characters 該列的
	// test_level 一起鎖（FOR UPDATE），effLevel/skillTotal 都在鎖之後才算。
	var testLevel *int
	if err := tx.QueryRow(ctx, `SELECT test_level FROM player_characters WHERE user_id=$1 FOR UPDATE`, uid).Scan(&testLevel); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	effLevel := EffectiveLevel(effectiveTestLevel(cfg, testLevel), baseLevel)
	skillTotal := TotalSkillPoints(cfg, effLevel)

	// 鎖目前職業全部技能等級列，避免併發配點超花（技能數量固定上限 10 個，逐列 FOR UPDATE
	// 開銷可接受；查無列視為 level=0，尚未配點過，不是錯誤）。
	levels := map[string]int{}
	for _, s := range jobSkills {
		var lvl int
		err := tx.QueryRow(ctx, `SELECT level FROM player_skill_levels WHERE user_id=$1 AND skill_id=$2 FOR UPDATE`, uid, s.ID).Scan(&lvl)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusInternalServerError, "failed to load skill levels")
			return
		}
		levels[s.ID] = lvl
	}
	spent := 0
	for _, lv := range levels {
		spent += lv
	}
	skillFree := skillTotal - spent
	if skillFree < 0 {
		skillFree = 0
	}

	prereqOK := true
	if skill.PrereqSkillID != nil {
		prereqOK = levels[*skill.PrereqSkillID] >= skill.PrereqLevel
	}
	curLevel := levels[skill.ID]
	newLevel := curLevel

	switch {
	case isMax:
		for newLevel < skill.MaxLevel && prereqOK && skillFree > 0 {
			newLevel++
			skillFree--
		}
		// mode=max：卡在 0 沒有變化也視為成功（比照 /rpg/allocate mode:max 的既有慣例），不報錯。
	case deltaInt == 1:
		if !prereqOK {
			respondErr(w, http.StatusBadRequest, "prereq")
			return
		}
		if curLevel >= skill.MaxLevel {
			respondErr(w, http.StatusBadRequest, "max_level")
			return
		}
		if skillFree < 1 {
			respondErr(w, http.StatusBadRequest, "no_points")
			return
		}
		newLevel = curLevel + 1
	case deltaInt == -1:
		if curLevel <= 0 {
			respondErr(w, http.StatusBadRequest, "min_level")
			return
		}
		newLevel = curLevel - 1
		// 審查#3【中低・CONFIRMED】根因修復：降級前檢查是否有其他「已投資」（level>=1）技能把
		// 本技能當前置、且降級後的新等級已經低於該技能要求的 prereq_level——不擋的話會出現
		// 「A 降到 0 後 B 仍停留在需要 A>=3 才能點滿的 Lv5」這種前置條件事後失效、但既有配點
		// 沒有連動的不一致狀態。刻意不做級聯自動歸零（cascading reset）：直接拒絕，請玩家自己
		// 先降依賴方，比在玩家沒預期的情況下幫他把一整條已投資的技能鏈炸掉更安全。
		if violatesPrereqDependents(jobSkills, levels, skill.ID, newLevel) {
			respondErr(w, http.StatusBadRequest, "prereq_dependents")
			return
		}
	}

	if newLevel != curLevel {
		if _, err := tx.Exec(ctx, `
			INSERT INTO player_skill_levels (user_id, skill_id, level, updated_at) VALUES ($1,$2,$3,NOW())
			ON CONFLICT (user_id, skill_id) DO UPDATE SET level=EXCLUDED.level, updated_at=NOW()`,
			uid, skill.ID, newLevel); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to save")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to commit")
		return
	}
	h.respondSkills(w, r, uid)
}

// POST /rpg/skills/reset：目前職業全部技能 level=0（沒有職業時等同已經是空技能欄，直接回現狀）。
func (h *Handler) SkillsReset(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	if ch.JobID == nil {
		h.respondSkills(w, r, uid)
		return
	}
	skills, err := h.listSkillsByJob(ctx, *ch.JobID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load skills")
		return
	}
	ids := make([]string, len(skills))
	for i, s := range skills {
		ids[i] = s.ID
	}
	if len(ids) > 0 {
		if _, err := h.db.Exec(ctx, `DELETE FROM player_skill_levels WHERE user_id=$1 AND skill_id = ANY($2)`, uid, ids); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to reset")
			return
		}
	}
	h.respondSkills(w, r, uid)
}
