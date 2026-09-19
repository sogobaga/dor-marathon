// tavern.go：DORPG P6（CONTRACT §3.1/§3.2/§3.3）酒館與隊伍——player_party 的讀寫、
// GET /rpg/tavern、PUT /rpg/party，以及 battle.go bootstrap 共用的
// resolvePartyForBattle（隊伍成員解析：DB 列或「沒有列＝小咪＋系統預設」的預設隊伍）。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// ---------------------------------------------------------------------------
// DB 層：player_party（migration 181）
// ---------------------------------------------------------------------------

// partySlotRow player_party 一列（DB 現況，不是 WIRE DTO——DTO 還要另外查腳本名稱/等級，
// 見 buildPartySlotDTOs）。
type partySlotRow struct {
	Slot        int
	CompanionID string
	PresetID    *string
}

// getPartySlots 查這個使用者目前的隊伍列（可能 0~4 筆，見契約 §3.1「沒有任何列＝預設隊伍」）。
func (h *Handler) getPartySlots(ctx context.Context, userID string) ([]partySlotRow, error) {
	rows, err := h.db.Query(ctx, `SELECT slot, companion_id, preset_id::text FROM player_party WHERE user_id=$1 ORDER BY slot`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []partySlotRow{}
	for rows.Next() {
		var row partySlotRow
		if err := rows.Scan(&row.Slot, &row.CompanionID, &row.PresetID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// replacePartySlots PUT /rpg/party 語意：「伺服器整批覆蓋 4 格」（WIRE）——單一交易先清空
// 這個使用者的全部列再重新插入，避免「先刪某格、插入另一格」中途失敗留下不一致的殘局。
func (h *Handler) replacePartySlots(ctx context.Context, userID string, slots []partySlotRow) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 成功路徑會先 Commit，Rollback 在那之後為 no-op

	if _, err := tx.Exec(ctx, `DELETE FROM player_party WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, s := range slots {
		if _, err := tx.Exec(ctx, `INSERT INTO player_party (user_id, slot, companion_id, preset_id) VALUES ($1,$2,$3,$4)`,
			userID, s.Slot, s.CompanionID, s.PresetID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// defaultPartySlots 契約 §3.1：「沒有任何列＝預設隊伍＝小咪＋其系統預設腳本」——佔第 1 格，
// 其餘 3 格空。GET /rpg/tavern 與戰鬥 bootstrap 共用同一份預設，兩處看到的「預設隊伍」定義
// 才不會漂移。
func defaultPartySlots() []partySlotRow {
	return []partySlotRow{{Slot: 1, CompanionID: "char_xiaomi", PresetID: nil}}
}

// ---------------------------------------------------------------------------
// GET /rpg/tavern（WIRE）
// ---------------------------------------------------------------------------

type PartySlotDTO struct {
	Slot        int     `json:"slot"`
	CompanionID *string `json:"companion_id"`
	PresetID    *string `json:"preset_id"`
	PresetName  *string `json:"preset_name"`
	Level       *int    `json:"level"`
}

type MercenaryDTO struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	PortraitID string      `json:"portrait_id"`
	Role       string      `json:"role"`
	Job        JobRow      `json:"job"`
	InParty    bool        `json:"in_party"`
	Presets    []PresetDTO `json:"presets"`
}

type tavernLeaderDTO struct {
	Job            *JobRow `json:"job"`
	EffectiveLevel int     `json:"effective_level"`
	Name           string  `json:"name"`
}

type tavernResponse struct {
	Leader      tavernLeaderDTO `json:"leader"`
	Party       [4]PartySlotDTO `json:"party"`
	Mercenaries []MercenaryDTO  `json:"mercenaries"`
	// Strategies DORPG P9（CONTRACT §2、WIRE）：只含 is_active，依 sort_order——酒館腳本編輯器
	// 的「AI 策略」下拉選單資料來源。
	Strategies []StrategyDTO `json:"strategies"`
}

// buildPartySlotDTOs 契約 §3.1：rows 為空時套用 defaultPartySlots()；每一格再解析出腳本
// 名稱/等級（resolveCompanionPreset：preset_id 為 nil 時退回系統預設）。查無傭兵/腳本（髒資料，
// 例如後台把某個傭兵下架或使用者腳本被刪但沒有走 FK ON DELETE SET NULL 的極端情況）時，該格
// 保守顯示成只有 companion_id、沒有腳本資訊，而不是讓整支 API 500。
func (h *Handler) buildPartySlotDTOs(ctx context.Context, rows []partySlotRow) [4]PartySlotDTO {
	var out [4]PartySlotDTO
	for i := 0; i < 4; i++ {
		out[i] = PartySlotDTO{Slot: i + 1}
	}
	effective := rows
	if len(effective) == 0 {
		effective = defaultPartySlots()
	}
	for _, row := range effective {
		if row.Slot < 1 || row.Slot > 4 {
			continue // 髒資料防呆：DB CHECK 已經擋了寫入，這裡只是不讓讀取端連帶 panic
		}
		companionID := row.CompanionID
		dto := PartySlotDTO{Slot: row.Slot, CompanionID: &companionID}
		if preset, err := h.resolveCompanionPreset(ctx, row.CompanionID, row.PresetID); err == nil {
			dto.PresetID = &preset.ID
			dto.PresetName = &preset.Name
			dto.Level = &preset.Level
		}
		out[row.Slot-1] = dto
	}
	return out
}

// GET /rpg/tavern
func (h *Handler) Tavern(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	h.respondTavern(w, r, uid)
}

// respondTavern 共用尾段：GET /rpg/tavern 與 PUT /rpg/party 成功後都回同一種形狀（WIRE：
// 「回 /rpg/tavern 同形」），比照 handler.go respondMe／skills.go respondSkills 的既有慣例。
func (h *Handler) respondTavern(w http.ResponseWriter, r *http.Request, uid string) {
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
	_, effLevel, err := h.loadEffectiveLevel(ctx, uid, cfg, ch.TestLevel)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level")
		return
	}
	var leaderJob *JobRow
	if ch.JobID != nil {
		// DORPG P10：tavernResponse.Leader.Job 要含 paths/traits，改用 getJobByIDFull。
		j, jerr := h.getJobByIDFull(ctx, *ch.JobID)
		switch {
		case jerr == nil:
			leaderJob = &j
		case errors.Is(jerr, pgx.ErrNoRows):
			// 保留 nil（理論上不會發生，職業沒有 CRUD 刪除路徑）。
		default:
			if respondIfMissingRelationMsg(w, jerr, errJobsNotReady) {
				return
			}
			respondErr(w, http.StatusInternalServerError, "failed to load job")
			return
		}
	}
	var displayName string
	if err := h.db.QueryRow(ctx, `SELECT COALESCE(name, handle) FROM users WHERE id=$1`, uid).Scan(&displayName); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	mercenaries, err := h.listPartyCompanions(ctx, 10) // 只有 4 位傭兵，10 純粹是「拿全部」的寬鬆上限
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load companions")
		return
	}

	slots, err := h.getPartySlots(ctx, uid)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errJobsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load party")
		return
	}
	inParty := map[string]bool{}
	effectiveSlots := slots
	if len(effectiveSlots) == 0 {
		effectiveSlots = defaultPartySlots()
	}
	for _, s := range effectiveSlots {
		inParty[s.CompanionID] = true
	}

	// DORPG P9 N+1 修復（2026-09-19，見 presets.go loadGearCatalog 檔頭說明）：原本在下面的迴圈裡
	// 對每份腳本各自呼叫 resolvePresetEquipmentSlots(ctx,...)、逐格打 DB，N 份腳本×8 格造成
	// GET /rpg/tavern 實測 13–19 秒。改成分兩段：這一段先把 job/skills/presets 查好、收集全部
	// 腳本會用到的裝備 item_id；查完後一次性批次載入（loadGearCatalog），下面第二段迴圈才組 DTO，
	// 此時 resolvePresetEquipmentSlots 純粹查記憶體 map，不再碰 DB。
	type mercBuild struct {
		companion CompanionRow
		job       JobRow
		jobSkills []SkillRow
		presets   []PresetRow
	}
	builds := make([]mercBuild, 0, len(mercenaries))
	var allWeaponIDs, allArmorIDs []string
	for _, c := range mercenaries {
		if c.JobID == nil {
			continue // 理論上不會發生：migration 181 已把四位傭兵都補上 job_id
		}
		// DORPG P10：MercenaryDTO.Job 要含 paths/traits（角色頁／酒館顯示職業特性），改用
		// getJobByIDFull。
		job, jerr := h.getJobByIDFull(ctx, *c.JobID)
		if jerr != nil {
			if respondIfMissingRelationMsg(w, jerr, errJobsNotReady) {
				return
			}
			respondErr(w, http.StatusInternalServerError, "failed to load job")
			return
		}
		jobSkills, jerr := h.listSkillsByJob(ctx, *c.JobID)
		if jerr != nil {
			respondErr(w, http.StatusInternalServerError, "failed to load skills")
			return
		}
		presets, jerr := h.listPresetsForCompanion(ctx, uid, c.ID)
		if jerr != nil {
			if respondIfMissingRelationMsg(w, jerr, errJobsNotReady) {
				return
			}
			respondErr(w, http.StatusInternalServerError, "failed to load presets")
			return
		}
		for _, p := range presets {
			wIDs, aIDs := presetEquipmentIDs(p.Equipment)
			allWeaponIDs = append(allWeaponIDs, wIDs...)
			allArmorIDs = append(allArmorIDs, aIDs...)
		}
		builds = append(builds, mercBuild{companion: c, job: job, jobSkills: jobSkills, presets: presets})
	}

	weapons, armor, err := h.loadGearCatalog(ctx, dedupeNonEmpty(allWeaponIDs), dedupeNonEmpty(allArmorIDs))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load preset equipment")
		return
	}

	mercDTOs := make([]MercenaryDTO, 0, len(builds))
	for _, b := range builds {
		presetDTOs := make([]PresetDTO, 0, len(b.presets))
		for _, p := range b.presets {
			// DORPG P9（CONTRACT §2/§3、WIRE）：每份腳本各自解析自己的 equipment 八格——已存檔的
			// 腳本理論上早就通過 CreatePreset/UpdatePreset 的資格檢查，這裡純粹是「查得到就顯示」
			// 的展示用途，不重新擋 canEquip（見 presetEquip 檔頭註解）。
			eqSlots := resolvePresetEquipmentSlots(p.Equipment, weapons, armor)
			presetDTOs = append(presetDTOs, buildPresetDTO(cfg, b.job, b.jobSkills, b.companion, p, presetEquipmentWire(eqSlots, p.Level), presetEquip(eqSlots)))
		}
		mercDTOs = append(mercDTOs, MercenaryDTO{
			ID: b.companion.ID, Name: b.companion.Name, PortraitID: b.companion.PortraitID, Role: b.companion.Role,
			Job: b.job, InParty: inParty[b.companion.ID], Presets: presetDTOs,
		})
	}

	strategies, err := h.listActiveStrategies(ctx)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load strategies")
		return
	}
	strategyDTOs := make([]StrategyDTO, 0, len(strategies))
	for _, s := range strategies {
		strategyDTOs = append(strategyDTOs, toStrategyDTO(s))
	}

	respondJSON(w, http.StatusOK, tavernResponse{
		Leader:      tavernLeaderDTO{Job: leaderJob, EffectiveLevel: effLevel, Name: displayName},
		Party:       h.buildPartySlotDTOs(ctx, slots),
		Mercenaries: mercDTOs,
		Strategies:  strategyDTOs,
	})
}

// ---------------------------------------------------------------------------
// PUT /rpg/party（WIRE）
// ---------------------------------------------------------------------------

type putPartySlotInput struct {
	Slot        int     `json:"slot"`
	CompanionID string  `json:"companion_id"`
	PresetID    *string `json:"preset_id"`
}

type putPartyRequest struct {
	Slots []putPartySlotInput `json:"slots"`
}

// PUT /rpg/party {slots:[{slot,companion_id,preset_id|null}]}：只送有人的格子，伺服器整批覆蓋
// 4 格（WIRE）。錯誤碼：party_full（超過 4 格或同一格出現兩次）、duplicate_companion（同一傭兵
// 出現在多格）、not_mercenary（companion_id 不是四位傭兵之一）、preset_mismatch（腳本不屬於
// 這個傭兵、或不屬於本人／系統）。
func (h *Handler) PutParty(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body putPartyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if len(body.Slots) > 4 {
		respondErr(w, http.StatusBadRequest, "party_full")
		return
	}
	ctx := r.Context()

	mercenaries, err := h.listPartyCompanions(ctx, 10)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load companions")
		return
	}
	mercByID := make(map[string]bool, len(mercenaries))
	for _, m := range mercenaries {
		mercByID[m.ID] = true
	}

	seenSlot := map[int]bool{}
	seenCompanion := map[string]bool{}
	rows := make([]partySlotRow, 0, len(body.Slots))
	for _, s := range body.Slots {
		if s.Slot < 1 || s.Slot > 4 || seenSlot[s.Slot] {
			respondErr(w, http.StatusBadRequest, "party_full")
			return
		}
		seenSlot[s.Slot] = true
		if !mercByID[s.CompanionID] {
			respondErr(w, http.StatusBadRequest, "not_mercenary")
			return
		}
		if seenCompanion[s.CompanionID] {
			respondErr(w, http.StatusBadRequest, "duplicate_companion")
			return
		}
		seenCompanion[s.CompanionID] = true
		if s.PresetID != nil {
			preset, perr := h.getPreset(ctx, *s.PresetID)
			if perr != nil || preset.CompanionID != s.CompanionID || (preset.UserID != nil && *preset.UserID != uid) {
				respondErr(w, http.StatusBadRequest, "preset_mismatch")
				return
			}
		}
		rows = append(rows, partySlotRow{Slot: s.Slot, CompanionID: s.CompanionID, PresetID: s.PresetID})
	}

	if err := h.replacePartySlots(ctx, uid, rows); err != nil {
		if isMissingRelation(err) {
			respondErr(w, http.StatusServiceUnavailable, errJobsNotReady)
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.respondTavern(w, r, uid)
}

// ---------------------------------------------------------------------------
// 戰鬥 bootstrap 共用：契約 §3.2「隊伍成員＝player_party（或預設）」
// ---------------------------------------------------------------------------

// resolvedPartyMember 一位傭兵在這場戰鬥要用的完整資料：battle.go BattleBootstrap 用它組
// wirePartyMember（自己的 Compute 衍生值×倍率、自己的技能欄、presetName/level）。
type resolvedPartyMember struct {
	Slot      int
	Companion CompanionRow
	Job       JobRow
	JobSkills []SkillRow
	Preset    PresetRow
}

// clampSkillLevelsToMax 執行期防線（P6 事後修補的直接肇因：migration 181 的系統預設腳本
// 「阿深」把 hk_b3 灌到 Lv6，超過 migration 180 定義的 max_level=5，見
// migrations/182_rpg_preset_seed_fix.sql 檔頭根因說明）。rpg_companion_presets 的
// skill_levels 理論上只能透過 CreatePreset/UpdatePreset 經 ValidatePreset() 檢查後寫入，
// 但系統預設腳本是純 SQL seed，不吃 Go 這層驗證；一旦哪天又手滑塞進一筆超過 max_level（或
// 負值）的等級，戰鬥引擎不該把它當真、更不該讓超額等級放大技能係數/傷害。這裡把每個 level
// 都夾到 [0, max_level]，任何被夾住的技能各記一次 log.Printf（含 preset id／skill id／
// 原始值／上限），方便事後從 log 找出還有哪些髒資料沒清乾淨。查不到對應 SkillRow 的技能
// （skill_not_in_job 的範疇，不是這裡要處理的「超過上限」）只夾負值、不動它的上限。
func clampSkillLevelsToMax(presetID string, jobSkills []SkillRow, skillLevels map[string]int) map[string]int {
	if len(skillLevels) == 0 {
		return skillLevels
	}
	maxByID := make(map[string]int, len(jobSkills))
	for _, s := range jobSkills {
		maxByID[s.ID] = s.MaxLevel
	}
	out := make(map[string]int, len(skillLevels))
	for id, lvl := range skillLevels {
		clamped := lvl
		if clamped < 0 {
			clamped = 0
		}
		if max, ok := maxByID[id]; ok && clamped > max {
			clamped = max
		}
		if clamped != lvl {
			log.Printf("rpg preset skill level clamp: preset=%s skill=%s level=%d 超過合法範圍，已夾住為 %d（max_level=%d）",
				presetID, id, lvl, clamped, maxByID[id])
		}
		out[id] = clamped
	}
	return out
}

// resolvePartyForBattle 契約 §3.1/§3.2：沒有任何 player_party 列＝預設隊伍＝小咪＋其系統預設
// 腳本；有列則依 slot 排序解析每一位。任何一格指到「查無資料」的傭兵/腳本（後台下架/使用者
// 腳本被刪且系統預設也查不到的極端情況）保守略過該格，而不是讓整場 bootstrap 500——契約沒有
// 要求隊伍一定要滿編，缺一兩位隊友只是戰鬥變難，不是系統錯誤。
func (h *Handler) resolvePartyForBattle(ctx context.Context, userID string) ([]resolvedPartyMember, error) {
	rows, err := h.getPartySlots(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		rows = defaultPartySlots()
	}
	out := make([]resolvedPartyMember, 0, len(rows))
	for _, row := range rows {
		comp, cerr := h.getCompanionByID(ctx, row.CompanionID)
		if cerr != nil {
			if errors.Is(cerr, pgx.ErrNoRows) {
				continue
			}
			return nil, cerr
		}
		if comp.JobID == nil {
			continue
		}
		// DORPG P10：resolvedPartyMember.Job 要含 Traits——battle.go bootstrap 把它疊進這位
		// 傭兵的 equipmentEffects.damageTakenPct（CONTRACT §3），改用 getJobByIDFull。
		job, jerr := h.getJobByIDFull(ctx, *comp.JobID)
		if jerr != nil {
			return nil, jerr
		}
		jobSkills, jerr := h.listSkillsByJob(ctx, *comp.JobID)
		if jerr != nil {
			return nil, jerr
		}
		preset, perr := h.resolveCompanionPreset(ctx, row.CompanionID, row.PresetID)
		if perr != nil {
			continue // 腳本查無資料（已刪除且沒有系統預設可退）：略過這格
		}
		preset.SkillLevels = clampSkillLevelsToMax(preset.ID, jobSkills, preset.SkillLevels)
		out = append(out, resolvedPartyMember{Slot: row.Slot, Companion: comp, Job: job, JobSkills: jobSkills, Preset: preset})
	}
	return out, nil
}

// buildCompanionSkillsWire 契約 §3.2：「技能＝skill_levels 中 level≥1 且非 passive、
// implemented=true 的技能（ExpandEffect 展開）」。jobSkills 已由 listSkillsByJob 依
// path→tier→sort_order 排序，輸出順序沿用（AI 決策時「tier 最高的傷害技」等規則需要穩定順序）。
func buildCompanionSkillsWire(jobSkills []SkillRow, skillLevels map[string]int) []wireSkill {
	out := []wireSkill{}
	for _, s := range jobSkills {
		lvl := skillLevels[s.ID]
		if lvl < 1 || s.Kind == "passive" || !s.Implemented {
			continue
		}
		out = append(out, toWireSkillLeveled(s, lvl))
	}
	return out
}

// ---------------------------------------------------------------------------
// GET /rpg/tavern/gear（DORPG P9，WIRE）：酒館腳本編輯器用——某職業在某等級下的全部武器＋
// 防具/飾品目錄，供編輯器本地比對「哪些格子目前已選、能不能裝」，不吃玩家自己目前的職業/裝備
// （腳本可能是另一個跟玩家不同職業的傭兵，見 CONTRACT §2）。
// ---------------------------------------------------------------------------

// TavernGear GET /rpg/tavern/gear?job_id=<job>&level=<n> → { weapon_types, weapons, armor_items }
// （WIRE：can_equip 依 level 算；equipped/equipped_in 恆 false/null——編輯器自己比對哪些格子
// 已選）。
func (h *Handler) TavernGear(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		respondErr(w, http.StatusBadRequest, "缺少 job_id")
		return
	}
	level, _ := strconv.Atoi(r.URL.Query().Get("level"))
	level = clampLevel1to99(level)

	ctx := r.Context()
	if _, err := h.getJobByID(ctx, jobID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusBadRequest, "job not found")
			return
		}
		if respondIfMissingRelationMsg(w, err, errJobsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load job")
		return
	}

	weaponTypes, err := h.listWeaponTypesByJob(ctx, jobID)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load weapon types")
		return
	}
	weaponRows, err := h.listWeaponsByJob(ctx, jobID)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errWeaponsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load weapons")
		return
	}
	weapons := make([]weaponDTO, 0, len(weaponRows))
	for _, wRow := range weaponRows {
		weapons = append(weapons, toWeaponDTO(wRow, level, "")) // equippedID="" → equipped 恆 false（WIRE）
	}

	armorRows, err := h.listArmorItemsByJobOrGeneric(ctx, jobID)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errArmorNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load armor items")
		return
	}
	armorItems := toArmorDTOList(armorRows, level, nil) // equipped=nil → equipped_in 恆 null（WIRE）

	respondJSON(w, http.StatusOK, map[string]any{
		"weapon_types": weaponTypes,
		"weapons":      weapons,
		"armor_items":  armorItems,
	})
}
