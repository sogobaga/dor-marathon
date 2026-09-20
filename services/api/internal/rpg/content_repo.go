// content_repo.go：DORPG P2 內容表（rpg_monsters/rpg_skills/rpg_items/rpg_scenes/
// rpg_companions/rpg_encounters+rpg_encounter_monsters/rpg_battle_logs，見 migration 176）的
// pgx 讀寫。全部用欄位白名單組 SQL、參數一律走 $N 佔位符，不做任何字串拼接使用者輸入。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// errContentNotReady 缺表（migration 176 未套用）時對外的固定訊息——契約 §3.4：503 但不噴
// SQL 內容，避免把資料庫結構細節洩漏到錯誤訊息裡。
const errContentNotReady = "戰鬥內容尚未初始化（migration 176 未套用）"

// isMissingRelation 判斷 DB 錯誤是不是「資料表不存在」。用 pgconn.PgError 的 SQLSTATE 42P01
// 判斷，不解析錯誤字串（字串比對脆弱，換一種 pgx 版本或 driver 措辭就失效）。
func isMissingRelation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

// ---------------------------------------------------------------------------
// rpg_monsters
// ---------------------------------------------------------------------------

const monsterCols = `id, name, rank, attribute, size, race, sprite_id, poster_url, hp_mult, atk_mult, def_mult, speed_mult, threat, is_boss, is_active, sort_order, weak_elements`

func scanMonster(row pgx.Row) (MonsterRow, error) {
	var m MonsterRow
	err := row.Scan(&m.ID, &m.Name, &m.Rank, &m.Attribute, &m.Size, &m.Race, &m.SpriteID, &m.PosterURL,
		&m.HPMult, &m.AtkMult, &m.DefMult, &m.SpeedMult, &m.Threat, &m.IsBoss, &m.IsActive, &m.SortOrder,
		&m.WeakElements)
	if m.WeakElements == nil {
		m.WeakElements = []string{}
	}
	return m, err
}

func (h *Handler) listMonsters(ctx context.Context, activeOnly bool) ([]MonsterRow, error) {
	q := `SELECT ` + monsterCols + ` FROM rpg_monsters`
	if activeOnly {
		q += ` WHERE is_active`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := h.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MonsterRow{}
	for rows.Next() {
		m, err := scanMonster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// getMonstersByIDs 批次查詢（bootstrap 組怪物列表用），保留輸入順序無關，呼叫端自行用 map 對應。
func (h *Handler) getMonstersByIDs(ctx context.Context, ids []string) (map[string]MonsterRow, error) {
	out := map[string]MonsterRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `SELECT `+monsterCols+` FROM rpg_monsters WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMonster(rows)
		if err != nil {
			return nil, err
		}
		out[m.ID] = m
	}
	return out, rows.Err()
}

func (h *Handler) upsertMonster(ctx context.Context, m MonsterRow) error {
	weakElements := m.WeakElements
	if weakElements == nil {
		weakElements = []string{} // nil slice 傳給 pgx text[] 會變 SQL NULL（比照 CompanionRow.SkillIDs 慣例）
	}
	_, err := h.db.Exec(ctx, `
		INSERT INTO rpg_monsters (id, name, rank, attribute, size, race, sprite_id, poster_url, hp_mult, atk_mult, def_mult, speed_mult, threat, is_boss, is_active, sort_order, weak_elements, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,NOW())
		ON CONFLICT (id) DO UPDATE SET
			name=$2, rank=$3, attribute=$4, size=$5, race=$6, sprite_id=$7, poster_url=$8,
			hp_mult=$9, atk_mult=$10, def_mult=$11, speed_mult=$12, threat=$13, is_boss=$14,
			is_active=$15, sort_order=$16, weak_elements=$17, updated_at=NOW()`,
		m.ID, m.Name, m.Rank, m.Attribute, m.Size, m.Race, m.SpriteID, m.PosterURL,
		m.HPMult, m.AtkMult, m.DefMult, m.SpeedMult, m.Threat, m.IsBoss, m.IsActive, m.SortOrder, weakElements)
	return err
}

func (h *Handler) deleteMonster(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_monsters WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// rpg_jobs（P5：只需要 list/get，本輪沒有後台 CRUD——見 CONTRACT §7「後台 CRUD 不做」）。
// ---------------------------------------------------------------------------

const jobCols = `id, name, tagline, description, path_a_id, path_a_name, path_a_desc, path_b_id, path_b_name, path_b_desc, weapon, atk_branch, recommended_stats, sort_order`

func scanJob(row pgx.Row) (JobRow, error) {
	var j JobRow
	err := row.Scan(&j.ID, &j.Name, &j.Tagline, &j.Description,
		&j.PathA.ID, &j.PathA.Name, &j.PathA.Desc,
		&j.PathB.ID, &j.PathB.Name, &j.PathB.Desc,
		&j.Weapon, &j.AtkBranch, &j.RecommendedStats, &j.SortOrder)
	return j, err
}

func (h *Handler) listJobs(ctx context.Context) ([]JobRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+jobCols+` FROM rpg_jobs ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobRow{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// getJobByID 查無資料回 pgx.ErrNoRows（呼叫端依情境決定要回 400 還是保守視為「未選職業」）。
func (h *Handler) getJobByID(ctx context.Context, id string) (JobRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+jobCols+` FROM rpg_jobs WHERE id=$1`, id)
	return scanJob(row)
}

// ---------------------------------------------------------------------------
// rpg_skills
// ---------------------------------------------------------------------------

const skillCols = `id, name, icon_id, kind, target, weapon, element, mp_cost, cooldown_ms, coefficient, flat, cast_ms, is_default, is_active, sort_order, job_id, path, tier, max_level, effect, dmg_type, prereq_skill_id, prereq_level, mp_cost_per_level, display_text, implemented`

func scanSkill(row pgx.Row) (SkillRow, error) {
	var s SkillRow
	var effectRaw []byte
	err := row.Scan(&s.ID, &s.Name, &s.IconID, &s.Kind, &s.Target, &s.Weapon, &s.Element,
		&s.MPCost, &s.CooldownMs, &s.Coefficient, &s.Flat, &s.CastMs, &s.IsDefault, &s.IsActive, &s.SortOrder,
		&s.JobID, &s.Path, &s.Tier, &s.MaxLevel, &effectRaw, &s.DmgType, &s.PrereqSkillID, &s.PrereqLevel,
		&s.MPCostPerLevel, &s.DisplayText, &s.Implemented)
	if err != nil {
		return s, err
	}
	if len(effectRaw) > 0 {
		if err := json.Unmarshal(effectRaw, &s.Effect); err != nil {
			// 壞掉的 JSON 不該讓整支 API 500（比照 scanScene 對 slots 的既有慣例）——回零值，
			// 這個技能展開出來的效果會是全 0，後台看得到，可以馬上重新存檔修正。
			s.Effect = SkillEffect{}
		}
	}
	return s, nil
}

func (h *Handler) listSkills(ctx context.Context, activeOnly bool) ([]SkillRow, error) {
	q := `SELECT ` + skillCols + ` FROM rpg_skills`
	if activeOnly {
		q += ` WHERE is_active`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := h.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillRow{}
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) getSkillsByIDs(ctx context.Context, ids []string) (map[string]SkillRow, error) {
	out := map[string]SkillRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `SELECT `+skillCols+` FROM rpg_skills WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out[s.ID] = s
	}
	return out, rows.Err()
}

func (h *Handler) upsertSkill(ctx context.Context, s SkillRow) error {
	effectRaw, err := json.Marshal(s.Effect)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO rpg_skills (id, name, icon_id, kind, target, weapon, element, mp_cost, cooldown_ms, coefficient, flat, cast_ms, is_default, is_active, sort_order,
			job_id, path, tier, max_level, effect, dmg_type, prereq_skill_id, prereq_level, mp_cost_per_level, display_text, implemented, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15, $16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26, NOW())
		ON CONFLICT (id) DO UPDATE SET
			name=$2, icon_id=$3, kind=$4, target=$5, weapon=$6, element=$7, mp_cost=$8, cooldown_ms=$9,
			coefficient=$10, flat=$11, cast_ms=$12, is_default=$13, is_active=$14, sort_order=$15,
			job_id=$16, path=$17, tier=$18, max_level=$19, effect=$20, dmg_type=$21, prereq_skill_id=$22,
			prereq_level=$23, mp_cost_per_level=$24, display_text=$25, implemented=$26, updated_at=NOW()`,
		s.ID, s.Name, s.IconID, s.Kind, s.Target, s.Weapon, s.Element, s.MPCost, s.CooldownMs,
		s.Coefficient, s.Flat, s.CastMs, s.IsDefault, s.IsActive, s.SortOrder,
		s.JobID, s.Path, s.Tier, s.MaxLevel, effectRaw, s.DmgType, s.PrereqSkillID, s.PrereqLevel,
		s.MPCostPerLevel, s.DisplayText, s.Implemented)
	return err
}

func (h *Handler) deleteSkill(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_skills WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// P5：職業技能樹讀取（skills.go /rpg/skills、battle.go bootstrap 共用）。
// ---------------------------------------------------------------------------

// listSkillsByJob 某職業目前啟用中的全部技能，依 path→tier→sort_order 排序（WIRE：battle
// bootstrap 與 GET /rpg/skills 都依這個順序展示/填欄）。
func (h *Handler) listSkillsByJob(ctx context.Context, jobID string) ([]SkillRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+skillCols+` FROM rpg_skills WHERE job_id=$1 AND is_active ORDER BY path, tier, sort_order, id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SkillRow{}
	for rows.Next() {
		s, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// getPlayerSkillLevels 批次查詢玩家在給定技能 id 清單上的目前等級；查無列的技能不會出現在
// 回傳 map 裡（呼叫端用 `levels[id]`——Go map 查無 key 回傳零值 0，語意上等同「尚未配點」，
// 不需要另外判斷 ok）。
func (h *Handler) getPlayerSkillLevels(ctx context.Context, userID string, skillIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(skillIDs) == 0 {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `SELECT skill_id, level FROM player_skill_levels WHERE user_id=$1 AND skill_id = ANY($2)`, userID, skillIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var lvl int
		if err := rows.Scan(&id, &lvl); err != nil {
			return nil, err
		}
		out[id] = lvl
	}
	return out, rows.Err()
}

// sumSkillPointsSpent 目前職業已花費的技能點總數（每級花 1 點，見 CONTRACT §4）。jobID 為 nil
// （未選職業）時直接回 0，不查表——沒有職業就沒有「目前職業的技能」這個集合。
func (h *Handler) sumSkillPointsSpent(ctx context.Context, userID string, jobID *string) (int, error) {
	if jobID == nil {
		return 0, nil
	}
	var sum int
	err := h.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(psl.level),0) FROM player_skill_levels psl
		JOIN rpg_skills s ON s.id = psl.skill_id
		WHERE psl.user_id=$1 AND s.job_id=$2`, userID, *jobID).Scan(&sum)
	return sum, err
}

// loadPassives 目前職業已配點（level>=1）的 kind=passive 技能，展開成 Compute() 要吃的
// []PassiveEffect（見 skills.go ExpandEffect）。jobID 為 nil 時回空切片。
//
// SELECT 清單刻意把 psl.level 接在 skillCols 後面而不是重用 scanSkill——scanSkill 的 Scan
// 目的地數量與 skillCols 逐欄對應，多一欄 level 會讓那個共用函式的重用方式變得不直覺；這裡
// 技能數量最多 10 筆（一個職業的被動技能上限），直接展開 Scan 呼叫比為了重用硬拆一個新的
// scanSkill 變體更清楚。
func (h *Handler) loadPassives(ctx context.Context, userID string, jobID *string) ([]PassiveEffect, error) {
	if jobID == nil {
		return nil, nil
	}
	rows, err := h.db.Query(ctx, `
		SELECT `+skillCols+`, psl.level FROM rpg_skills s
		JOIN player_skill_levels psl ON psl.skill_id = s.id
		WHERE psl.user_id=$1 AND s.job_id=$2 AND s.kind='passive' AND psl.level > 0`, userID, *jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PassiveEffect
	for rows.Next() {
		var s SkillRow
		var effectRaw []byte
		var lvl int
		if err := rows.Scan(&s.ID, &s.Name, &s.IconID, &s.Kind, &s.Target, &s.Weapon, &s.Element,
			&s.MPCost, &s.CooldownMs, &s.Coefficient, &s.Flat, &s.CastMs, &s.IsDefault, &s.IsActive, &s.SortOrder,
			&s.JobID, &s.Path, &s.Tier, &s.MaxLevel, &effectRaw, &s.DmgType, &s.PrereqSkillID, &s.PrereqLevel,
			&s.MPCostPerLevel, &s.DisplayText, &s.Implemented, &lvl); err != nil {
			return nil, err
		}
		if len(effectRaw) > 0 {
			if err := json.Unmarshal(effectRaw, &s.Effect); err != nil {
				continue // 壞掉的 effect JSON：略過這個被動技能而非讓整個角色頁掛掉
			}
		}
		e := ExpandEffect(s, lvl)
		if e.Stat == "" {
			continue // effect JSONB 沒填 stat（設計疏漏或尚未 seed）——略過而非讓整個角色頁掛掉
		}
		out = append(out, PassiveEffect{Stat: e.Stat, Value: e.Value})
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// rpg_items
// ---------------------------------------------------------------------------

const itemCols = `id, name, icon_id, kind, amount, default_quantity, is_active, sort_order`

func scanItem(row pgx.Row) (ItemRow, error) {
	var it ItemRow
	err := row.Scan(&it.ID, &it.Name, &it.IconID, &it.Kind, &it.Amount, &it.DefaultQuantity, &it.IsActive, &it.SortOrder)
	return it, err
}

func (h *Handler) listItems(ctx context.Context, activeOnly bool) ([]ItemRow, error) {
	q := `SELECT ` + itemCols + ` FROM rpg_items`
	if activeOnly {
		q += ` WHERE is_active`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := h.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ItemRow{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// listDefaultQuantityItems bootstrap 用：每場戰鬥預帶的道具（default_quantity>0 且啟用）。
func (h *Handler) listDefaultQuantityItems(ctx context.Context) ([]ItemRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+itemCols+` FROM rpg_items WHERE is_active AND default_quantity > 0 ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ItemRow{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (h *Handler) upsertItem(ctx context.Context, it ItemRow) error {
	_, err := h.db.Exec(ctx, `
		INSERT INTO rpg_items (id, name, icon_id, kind, amount, default_quantity, is_active, sort_order, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW())
		ON CONFLICT (id) DO UPDATE SET
			name=$2, icon_id=$3, kind=$4, amount=$5, default_quantity=$6, is_active=$7, sort_order=$8, updated_at=NOW()`,
		it.ID, it.Name, it.IconID, it.Kind, it.Amount, it.DefaultQuantity, it.IsActive, it.SortOrder)
	return err
}

func (h *Handler) deleteItem(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_items WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// rpg_scenes
// ---------------------------------------------------------------------------

const sceneCols = `id, name, image_url, slots, location_note, is_active, sort_order`

func scanScene(row pgx.Row) (SceneRow, error) {
	var s SceneRow
	var raw []byte
	if err := row.Scan(&s.ID, &s.Name, &s.ImageURL, &raw, &s.LocationNote, &s.IsActive, &s.SortOrder); err != nil {
		return s, err
	}
	s.Slots = []SceneSlotRow{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.Slots); err != nil {
			// 壞掉的 JSON 不該讓整支 API 500——回空槽位陣列，前端場景就沒有怪物站位可用，
			// 比整頁噴錯好排查（後台場景分頁看得到，可以馬上重新存檔修正）。
			s.Slots = []SceneSlotRow{}
		}
	}
	return s, nil
}

func (h *Handler) listScenes(ctx context.Context, activeOnly bool) ([]SceneRow, error) {
	q := `SELECT ` + sceneCols + ` FROM rpg_scenes`
	if activeOnly {
		q += ` WHERE is_active`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := h.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SceneRow{}
	for rows.Next() {
		s, err := scanScene(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) getScene(ctx context.Context, id string) (SceneRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+sceneCols+` FROM rpg_scenes WHERE id=$1`, id)
	return scanScene(row)
}

// getScenesByIDs 批次查詢（審查1 CONFIRMED：BattleEncounters 原本逐一 getScene 是 N+1）。
// 比照 getMonstersByIDs：一次查完，呼叫端用 map 對應，查無資料的 id 就不會出現在回傳 map
// 裡——呼叫端決定「查無此筆」該留空字串還是視為錯誤，這裡只負責如實回傳查詢結果。
func (h *Handler) getScenesByIDs(ctx context.Context, ids []string) (map[string]SceneRow, error) {
	out := map[string]SceneRow{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `SELECT `+sceneCols+` FROM rpg_scenes WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanScene(rows)
		if err != nil {
			return nil, err
		}
		out[s.ID] = s
	}
	return out, rows.Err()
}

func (h *Handler) upsertScene(ctx context.Context, s SceneRow) error {
	slots := s.Slots
	if slots == nil {
		slots = []SceneSlotRow{}
	}
	raw, err := json.Marshal(slots)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO rpg_scenes (id, name, image_url, slots, location_note, is_active, sort_order, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
		ON CONFLICT (id) DO UPDATE SET
			name=$2, image_url=$3, slots=$4, location_note=$5, is_active=$6, sort_order=$7, updated_at=NOW()`,
		s.ID, s.Name, s.ImageURL, raw, s.LocationNote, s.IsActive, s.SortOrder)
	return err
}

func (h *Handler) deleteScene(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_scenes WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// rpg_companions
// ---------------------------------------------------------------------------

const companionCols = `id, name, portrait_id, role, weapon, level_offset, hp_mult, mp_mult, atk_mult, matk_mult, def_mult, mdef_mult, act_interval_mult, skill_ids, is_player_portrait, is_active, sort_order, job_id`

func scanCompanion(row pgx.Row) (CompanionRow, error) {
	var c CompanionRow
	err := row.Scan(&c.ID, &c.Name, &c.PortraitID, &c.Role, &c.Weapon, &c.LevelOffset,
		&c.HPMult, &c.MPMult, &c.AtkMult, &c.MatkMult, &c.DefMult, &c.MdefMult, &c.ActIntervalMult,
		&c.SkillIDs, &c.IsPlayerPortrait, &c.IsActive, &c.SortOrder, &c.JobID)
	if c.SkillIDs == nil {
		c.SkillIDs = []string{}
	}
	return c, err
}

func (h *Handler) listCompanions(ctx context.Context, activeOnly bool) ([]CompanionRow, error) {
	q := `SELECT ` + companionCols + ` FROM rpg_companions`
	if activeOnly {
		q += ` WHERE is_active`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := h.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CompanionRow{}
	for rows.Next() {
		c, err := scanCompanion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// getCompanionByID DORPG P6：酒館／腳本／隊伍三支新端點都要「單筆查某個傭兵」（拿它的
// hp_mult 等倍率、job_id、portrait_id），查無資料回 pgx.ErrNoRows（呼叫端依情境決定 400 還是
// 略過該筆，比照 getJobByID 的既有慣例）。
func (h *Handler) getCompanionByID(ctx context.Context, id string) (CompanionRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+companionCols+` FROM rpg_companions WHERE id=$1`, id)
	return scanCompanion(row)
}

// getPlayerPortraitCompanion 契約 D4：is_player_portrait=TRUE 那一列（唯一，DB 有 partial unique
// index 保證）。查無資料（後台誤刪）回 pgx.ErrNoRows，呼叫端保守處理成 null 頭像。
func (h *Handler) getPlayerPortraitCompanion(ctx context.Context) (CompanionRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+companionCols+` FROM rpg_companions WHERE is_player_portrait LIMIT 1`)
	return scanCompanion(row)
}

// listPartyCompanions bootstrap 用：非玩家頭像、啟用中、依 sort_order 取前 limit 位。
func (h *Handler) listPartyCompanions(ctx context.Context, limit int) ([]CompanionRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+companionCols+` FROM rpg_companions WHERE is_active AND NOT is_player_portrait ORDER BY sort_order, id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CompanionRow{}
	for rows.Next() {
		c, err := scanCompanion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// errPlayerPortraitTaken D4 唯一鍵衝突（partial unique index idx_rpg_companions_player_portrait）
// 轉成的中文訊息——sentinel error 讓呼叫端用 errors.Is 判斷該回 400（使用者輸入問題）而不是
// 500（系統錯誤），不必解析錯誤字串。
var errPlayerPortraitTaken = errors.New("已經有另一位角色設定為玩家頭像，請先取消該角色的 is_player_portrait")

func (h *Handler) upsertCompanion(ctx context.Context, c CompanionRow) error {
	skillIDs := c.SkillIDs
	if skillIDs == nil {
		skillIDs = []string{} // nil slice 傳給 pgx text[] 會變 SQL NULL（比照 race/handler.go 慣例）
	}
	_, err := h.db.Exec(ctx, `
		INSERT INTO rpg_companions (id, name, portrait_id, role, weapon, level_offset, hp_mult, mp_mult, atk_mult, matk_mult, def_mult, mdef_mult, act_interval_mult, skill_ids, is_player_portrait, is_active, sort_order, job_id, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,NOW())
		ON CONFLICT (id) DO UPDATE SET
			name=$2, portrait_id=$3, role=$4, weapon=$5, level_offset=$6, hp_mult=$7, mp_mult=$8,
			atk_mult=$9, matk_mult=$10, def_mult=$11, mdef_mult=$12, act_interval_mult=$13, skill_ids=$14,
			is_player_portrait=$15, is_active=$16, sort_order=$17, job_id=$18, updated_at=NOW()`,
		c.ID, c.Name, c.PortraitID, c.Role, c.Weapon, c.LevelOffset, c.HPMult, c.MPMult,
		c.AtkMult, c.MatkMult, c.DefMult, c.MdefMult, c.ActIntervalMult, skillIDs,
		c.IsPlayerPortrait, c.IsActive, c.SortOrder, c.JobID)
	if err != nil && isUniqueViolation(err, "idx_rpg_companions_player_portrait") {
		return errPlayerPortraitTaken
	}
	return err
}

// isUniqueViolation 判斷是不是指定名稱的唯一鍵衝突（用來把 DB 層的 partial unique index 撞牆
// 訊息轉成使用者看得懂的中文，而不是直接把 pgErr 字串吐回前端）。
func isUniqueViolation(err error, indexName string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && (indexName == "" || pgErr.ConstraintName == indexName)
}

// isForeignKeyViolation 判斷是不是外鍵違反（SQLSTATE 23503）——比照 isUniqueViolation，用來把
// 「刪除仍被其他表參照的資料」（審查4 CONFIRMED：例如刪除仍被某場遭遇使用中的怪物/場景，
// rpg_encounter_monsters.monster_id/rpg_encounters.scene_id 都沒有 ON DELETE 子句）轉成使用者
// 看得懂的 400，而不是像未分類錯誤一樣回通用 500——DB 層本來就會正確擋下刪除（資料不會壞），
// 差別只在 handler 要不要把「為什麼失敗」講清楚。不像 isUniqueViolation 需要比對特定索引名稱，
// 這裡任何 23503 都視為「還被引用中」，因為 rpg_* 的刪除操作只可能撞到這一種外鍵。
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func (h *Handler) deleteCompanion(ctx context.Context, id string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_companions WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// rpg_encounters + rpg_encounter_monsters
// ---------------------------------------------------------------------------

// encounterCols DORPG P11：migration 189 在既有表加了 scaling_mode/level_mode/rank/
// monster_count 四欄（不是新表）——比照 jobs.go loadJobExtras() 檔頭的既有決定，未套用 189
// 時這裡會直接拿到 42703（欄位不存在）而不是優雅降級的 42P01，push 前必須先套用該 migration
// （db-before-code-push.md），這裡不另外做容錯。
const encounterCols = `id, code, title, subtitle, scene_id, scene_kind, difficulty, power_scale, monster_level, escape_chance, can_escape, is_active, sort_order, scaling_mode, level_mode, rank, monster_count`

func scanEncounter(row pgx.Row) (EncounterRow, error) {
	var e EncounterRow
	err := row.Scan(&e.id, &e.Code, &e.Title, &e.Subtitle, &e.SceneID, &e.SceneKind, &e.Difficulty,
		&e.PowerScale, &e.MonsterLevel, &e.EscapeChance, &e.CanEscape, &e.IsActive, &e.SortOrder,
		&e.ScalingMode, &e.LevelMode, &e.Rank, &e.MonsterCount)
	e.Monsters = []EncounterMonsterRow{}
	return e, err
}

// listEncounters 列出全部遭遇＋各自的怪物編組（一次查詢遭遇列 + 一次批次查詢全部編組，避免
// N+1：契約 §8 審查鏡頭明講要看這個）。
func (h *Handler) listEncounters(ctx context.Context, activeOnly bool) ([]EncounterRow, error) {
	q := `SELECT ` + encounterCols + ` FROM rpg_encounters`
	if activeOnly {
		q += ` WHERE is_active`
	}
	q += ` ORDER BY sort_order, code`
	rows, err := h.db.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	list := []EncounterRow{}
	for rows.Next() {
		e, err := scanEncounter(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return list, nil
	}

	byID := make(map[string]int, len(list))
	ids := make([]string, len(list))
	for i, e := range list {
		byID[e.id] = i
		ids[i] = e.id
	}
	mrows, err := h.db.Query(ctx, `SELECT encounter_id::text, slot, monster_id, power_scale FROM rpg_encounter_monsters WHERE encounter_id = ANY($1::uuid[]) ORDER BY slot`, ids)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var encID string
		var em EncounterMonsterRow
		if err := mrows.Scan(&encID, &em.Slot, &em.MonsterID, &em.PowerScale); err != nil {
			return nil, err
		}
		if i, ok := byID[encID]; ok {
			list[i].Monsters = append(list[i].Monsters, em)
		}
	}
	return list, mrows.Err()
}

// getEncounterByCode 單場遭遇＋編組（GET /rpg/battle/bootstrap 用）。查無回 pgx.ErrNoRows。
func (h *Handler) getEncounterByCode(ctx context.Context, code string) (EncounterRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+encounterCols+` FROM rpg_encounters WHERE code=$1 AND is_active`, code)
	e, err := scanEncounter(row)
	if err != nil {
		return EncounterRow{}, err
	}
	mrows, err := h.db.Query(ctx, `SELECT slot, monster_id, power_scale FROM rpg_encounter_monsters WHERE encounter_id=$1 ORDER BY slot`, e.id)
	if err != nil {
		return EncounterRow{}, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var em EncounterMonsterRow
		if err := mrows.Scan(&em.Slot, &em.MonsterID, &em.PowerScale); err != nil {
			return EncounterRow{}, err
		}
		e.Monsters = append(e.Monsters, em)
	}
	return e, mrows.Err()
}

// upsertEncounter 契約 §3.5：單一交易內先刪後插 rpg_encounter_monsters，避免留下孤兒槽位。
func (h *Handler) upsertEncounter(ctx context.Context, e EncounterRow) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 成功路徑會先 Commit，Rollback 在那之後為 no-op

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO rpg_encounters (code, title, subtitle, scene_id, scene_kind, difficulty, power_scale, monster_level, escape_chance, can_escape, is_active, sort_order, scaling_mode, level_mode, rank, monster_count, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NOW())
		ON CONFLICT (code) DO UPDATE SET
			title=$2, subtitle=$3, scene_id=$4, scene_kind=$5, difficulty=$6, power_scale=$7,
			monster_level=$8, escape_chance=$9, can_escape=$10, is_active=$11, sort_order=$12,
			scaling_mode=$13, level_mode=$14, rank=$15, monster_count=$16, updated_at=NOW()
		RETURNING id::text`,
		e.Code, e.Title, e.Subtitle, e.SceneID, e.SceneKind, e.Difficulty, e.PowerScale, e.MonsterLevel,
		e.EscapeChance, e.CanEscape, e.IsActive, e.SortOrder, e.ScalingMode, e.LevelMode, e.Rank, e.MonsterCount,
	).Scan(&id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM rpg_encounter_monsters WHERE encounter_id=$1`, id); err != nil {
		return err
	}
	for _, m := range e.Monsters {
		if _, err := tx.Exec(ctx, `INSERT INTO rpg_encounter_monsters (encounter_id, slot, monster_id, power_scale) VALUES ($1,$2,$3,$4)`,
			id, m.Slot, m.MonsterID, m.PowerScale); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (h *Handler) deleteEncounter(ctx context.Context, code string) (bool, error) {
	ct, err := h.db.Exec(ctx, `DELETE FROM rpg_encounters WHERE code=$1`, code)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// rpg_battle_logs
// ---------------------------------------------------------------------------

func (h *Handler) insertBattleLog(ctx context.Context, userID string, in BattleLogInput) error {
	_, err := h.db.Exec(ctx, `
		INSERT INTO rpg_battle_logs (user_id, encounter_code, outcome, duration_ms, damage_dealt, damage_taken,
			enemies_defeated, attacks, charged_attacks, skills_used, items_used, guard_ms, player_level, player_power, client_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		userID, in.EncounterCode, in.Outcome, in.DurationMs, in.DamageDealt, in.DamageTaken,
		in.EnemiesDefeated, in.Attacks, in.ChargedAttacks, in.SkillsUsed, in.ItemsUsed, in.GuardMs,
		in.PlayerLevel, in.PlayerPower, in.ClientVersion)
	return err
}

// countBattleLogsToday 契約 §3.4：同一 user 每日最多 300 筆（超過直接 204 不寫入）。「今天」用
// UTC 日界（NOW() 的 date_trunc('day', ...) 預設吃 session timezone，站上其餘遙測/限流慣例
// 沒有特別調時區，這裡跟著吃 DB 預設時區，只是防濫用的粗粒度上限，不要求精確對齊台灣日界）。
//
// 審查5 PLAUSIBLE（刻意不修）：這裡跟 insertBattleLog（battle.go BattleReport）不在同一交易內，
// 兩個並發請求都可能在計數為 299 時讀到「未達上限」各自寫入，短暫超過 300 筆。D5 明講這支端點
// 只是遙測、不觸碰任何帳本/獎勵，多寫幾筆統計資料的風險遠低於為了精確保證上限而加鎖/交易
// 犧牲效能與 Neon 連線數，故不修——若之後這個上限要當真正的配額（例如接獎勵），才需要重新
// 評估用交易或 Redis 原子計數器收斂 TOCTOU。
func (h *Handler) countBattleLogsToday(ctx context.Context, userID string) (int, error) {
	var n int
	err := h.db.QueryRow(ctx, `SELECT COUNT(*) FROM rpg_battle_logs WHERE user_id=$1 AND created_at >= date_trunc('day', NOW())`, userID).Scan(&n)
	return n, err
}

// userBattleStat 單一遭遇的個人統計（GET /rpg/battle/encounters 用）。
type userBattleStat struct {
	Plays       int
	Wins        int
	BestMs      *int
	LastOutcome string
}

// loadUserBattleStats 依 encounter_code 分組聚合本人紀錄，兩條查詢（聚合數字 + 最近一筆結果）
// 而非逐場各查一次——契約 §8 明講要看 N+1。
func (h *Handler) loadUserBattleStats(ctx context.Context, userID string) (map[string]userBattleStat, error) {
	stats := map[string]userBattleStat{}
	rows, err := h.db.Query(ctx, `
		SELECT encounter_code, COUNT(*), COUNT(*) FILTER (WHERE outcome='victory'),
		       MIN(duration_ms) FILTER (WHERE outcome='victory')
		FROM rpg_battle_logs WHERE user_id=$1 GROUP BY encounter_code`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var code string
		var st userBattleStat
		var best *int
		if err := rows.Scan(&code, &st.Plays, &st.Wins, &best); err != nil {
			rows.Close()
			return nil, err
		}
		st.BestMs = best
		stats[code] = st
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	lrows, err := h.db.Query(ctx, `
		SELECT DISTINCT ON (encounter_code) encounter_code, outcome
		FROM rpg_battle_logs WHERE user_id=$1 ORDER BY encounter_code, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var code, outcome string
		if err := lrows.Scan(&code, &outcome); err != nil {
			return nil, err
		}
		st := stats[code]
		st.LastOutcome = outcome
		stats[code] = st
	}
	return stats, lrows.Err()
}

// battleLogListRow 後台「戰鬥數據」分頁的明細列——刻意不含 email/account_code（隱私規則），
// 顯示名一律 COALESCE(u.name,u.handle)。
type battleLogListRow struct {
	CreatedAt     time.Time `json:"created_at"`
	DisplayName   string    `json:"display_name"`
	EncounterCode string    `json:"encounter_code"`
	Outcome       string    `json:"outcome"`
	DurationMs    int       `json:"duration_ms"`
	DamageDealt   int       `json:"damage_dealt"`
	DamageTaken   int       `json:"damage_taken"`
}

// battleLogSummaryRow 後台「戰鬥數據」分頁的每遭遇彙總列。
type battleLogSummaryRow struct {
	EncounterCode  string  `json:"encounter_code"`
	Plays          int     `json:"plays"`
	Wins           int     `json:"wins"`
	WinRate        float64 `json:"win_rate"`
	AvgDurationMs  float64 `json:"avg_duration_ms"`
	P50DurationMs  float64 `json:"p50_duration_ms"`
	AvgDamageTaken float64 `json:"avg_damage_taken"`
	DefeatRate     float64 `json:"defeat_rate"`
}

// listBattleLogsAdmin GET /admin/rpg/battle-logs?code=&days=&limit= 用。summary 用一條聚合查詢
// 算好 win_rate/defeat_rate（避免拉全部明細回 Go 端再算，days 拉長時明細可能上萬筆）。
func (h *Handler) listBattleLogsAdmin(ctx context.Context, code string, days, limit int) ([]battleLogListRow, []battleLogSummaryRow, error) {
	since := time.Now().AddDate(0, 0, -days)

	rowsQ := `
		SELECT l.created_at, COALESCE(u.name, u.handle) AS display_name, l.encounter_code, l.outcome, l.duration_ms, l.damage_dealt, l.damage_taken
		FROM rpg_battle_logs l
		JOIN users u ON u.id = l.user_id
		WHERE l.created_at >= $1`
	args := []any{since}
	if code != "" {
		rowsQ += ` AND l.encounter_code = $2`
		args = append(args, code)
	}
	rowsQ += fmt.Sprintf(` ORDER BY l.created_at DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := h.db.Query(ctx, rowsQ, args...)
	if err != nil {
		return nil, nil, err
	}
	list := []battleLogListRow{}
	for rows.Next() {
		var r battleLogListRow
		if err := rows.Scan(&r.CreatedAt, &r.DisplayName, &r.EncounterCode, &r.Outcome, &r.DurationMs, &r.DamageDealt, &r.DamageTaken); err != nil {
			rows.Close()
			return nil, nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	sumQ := `
		SELECT encounter_code, COUNT(*), COUNT(*) FILTER (WHERE outcome='victory'), COUNT(*) FILTER (WHERE outcome='defeat'),
		       AVG(duration_ms), PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY duration_ms), AVG(damage_taken)
		FROM rpg_battle_logs
		WHERE created_at >= $1`
	sumArgs := []any{since}
	if code != "" {
		sumQ += ` AND encounter_code = $2`
		sumArgs = append(sumArgs, code)
	}
	sumQ += ` GROUP BY encounter_code ORDER BY encounter_code`
	srows, err := h.db.Query(ctx, sumQ, sumArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer srows.Close()
	summary := []battleLogSummaryRow{}
	for srows.Next() {
		var s battleLogSummaryRow
		var plays, wins, defeats int
		var avgDur, p50Dur, avgDmgTaken *float64
		if err := srows.Scan(&s.EncounterCode, &plays, &wins, &defeats, &avgDur, &p50Dur, &avgDmgTaken); err != nil {
			return nil, nil, err
		}
		s.Plays = plays
		s.Wins = wins
		if plays > 0 {
			s.WinRate = float64(wins) / float64(plays)
			s.DefeatRate = float64(defeats) / float64(plays)
		}
		if avgDur != nil {
			s.AvgDurationMs = *avgDur
		}
		if p50Dur != nil {
			s.P50DurationMs = *p50Dur
		}
		if avgDmgTaken != nil {
			s.AvgDamageTaken = *avgDmgTaken
		}
		summary = append(summary, s)
	}
	return list, summary, srows.Err()
}
