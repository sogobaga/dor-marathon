// ranks.go：DORPG P11（CONTRACT §1/§2、WIRE）怪物強度九級表——rpg_monster_ranks 的型別／
// pgx 讀寫／DTO。行為本身（ScaleMonsterByRank 公式）在 scaling.go；HTTP 轉接層（GET /rpg/ranks、
// 後台 GET/PUT /admin/rpg/monster-ranks）分別在 battle.go／ranks_admin.go，比照
// strategies.go／strategies_admin.go 的既有分工。
package rpg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// SummonWave CONTRACT §1「summon JSONB { waves: [...] }」的單一波次。MonsterIDs 空＝用
// wave.Rank 對應的分級怪 DOR-MON-R-<rank>（見 migration 189 seed），非空＝後台指定特定怪物
// （CONTRACT：「未來後台可指定」，本輪先支援解析與 wire 化，種子資料全部留空陣列）。
//
// PowerScale P11 修正（2026-09-20，模擬證實用完整 rank 向量的召喚怪太強——A 級 60% 門檻召喚
// 2×E 讓 3 人隊 270 場只贏 1 場）：召喚怪整體強度折減乘數，套用在 buildSummonPool 算召喚怪數值
// 時的 encounterScale 上（見 battle.go），跟 rank.*_mult／monster.*_mult 是彼此獨立的另一個折減
// 旋鈕，讓之後模擬校準可以只調召喚怪、不動一般敵人的 rank 向量。實際數值由模擬決定寫進
// migration 189 seed，本欄只負責型別/預設值/驗證。
type SummonWave struct {
	AtHpPct    int      `json:"at_hp_pct"`
	Rank       string   `json:"rank"`
	Count      int      `json:"count"`
	MonsterIDs []string `json:"monster_ids,omitempty"`
	PowerScale float64  `json:"power_scale"`
}

// UnmarshalJSON 缺省 power_scale 視為 1.0（不折減）——既有 migration 189 seed 資料與大多數
// 後台先前寫入的 summon JSON 都沒有這個新 key，若直接用 Go 零值 0.0，會讓 buildSummonPool 算出
// 的 encounterScale 直接歸零、召喚怪 HP/ATK 全部退化成下限，等同一筆壞資料。用內嵌 alias +
// *float64 才能區分「JSON 完全沒有這個 key」跟「後台手動填了 0」——後者要讓 Validate 依然拒絕
// （0 < 0.05 下限），不能被這裡的預設值悄悄蓋掉。
func (w *SummonWave) UnmarshalJSON(data []byte) error {
	type alias SummonWave
	aux := struct {
		*alias
		PowerScale *float64 `json:"power_scale"`
	}{alias: (*alias)(w)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.PowerScale != nil {
		w.PowerScale = *aux.PowerScale
	} else {
		w.PowerScale = 1.0
	}
	return nil
}

// SummonConfig rpg_monster_ranks.summon 的頂層形狀。
type SummonConfig struct {
	Waves []SummonWave `json:"waves,omitempty"`
}

// Validate WIRE：「summon.waves[].rank 必須存在且 count 1–5、at_hp_pct 1–99」。rank 是否「存在」
// 這裡只查白名單（validMonsterRanks，見 content.go）——是否真的有該級的分級怪/後台是否已停用，
// 留給 DB 層 FK（monster_ids 非空時不受 FK 保護，屬於後台自行負責的自由文字，本輪不额外查
// rpg_monsters 是否存在，比照 EncounterRow.Validate 對 monster_id 的既有寬鬆風格）。
func (s SummonConfig) Validate() error {
	for i, w := range s.Waves {
		if w.AtHpPct < 1 || w.AtHpPct > 99 {
			return fmt.Errorf("summon.waves[%d].at_hp_pct 必須介於 1..99", i)
		}
		if !validMonsterRanks[w.Rank] {
			return fmt.Errorf("summon.waves[%d].rank 不合法", i)
		}
		if w.Count < 1 || w.Count > 5 {
			return fmt.Errorf("summon.waves[%d].count 必須介於 1..5", i)
		}
		// PowerScale P11 修正：0.05..2.0——下限避免後台誤填 0 或負值讓召喚怪 HP/ATK 直接歸零
		// （max(1,...) 保底會讓所有召喚怪變成 1 點血的無效狀態），上限對齊契約既有倍率類欄位
		// 「有界」的一貫風格，2.0 已經足夠讓召喚怪比一般敵人更強（若之後真的需要更強，屬於
		// 「用 rank 本身」而非這個折減旋鈕的職責）。
		if w.PowerScale < 0.05 || w.PowerScale > 2.0 {
			return fmt.Errorf("summon.waves[%d].power_scale 必須介於 0.05..2.0", i)
		}
	}
	return nil
}

// RankRow rpg_monster_ranks 資料列——json tag 直接對齊 DB 欄位（後台 GET/PUT 用，含
// summon/level_curve/calibrated_note）；玩家端 GET /rpg/ranks 改用下面精簡的 RankDTO
// （WIRE：「不含 summon／level_curve」，calibrated_note 同理不外流給玩家）。
type RankRow struct {
	Rank           string             `json:"rank"`
	Label          string             `json:"label"`
	SortOrder      int                `json:"sort_order"`
	HPMult         float64            `json:"hp_mult"`
	AtkMult        float64            `json:"atk_mult"`
	DefMult        float64            `json:"def_mult"`
	MdefMult       float64            `json:"mdef_mult"`
	LevelCurve     map[string]float64 `json:"level_curve"`
	Summon         SummonConfig       `json:"summon"`
	BadgeColor     string             `json:"badge_color"`
	Description    string             `json:"description"`
	CalibratedNote string             `json:"calibrated_note"`
}

// Validate 後台 PUT /admin/rpg/monster-ranks 用：rank 固定九值（不可新增/刪除，白名單即
// 把關）、四個倍率必須 > 0（同 MonsterRow.Validate 對 hp_mult 等欄位的既有規則），summon 遞交
// SummonConfig.Validate()。level_curve 的值本身不強制檢查（scaling.go levelCurveAt 對 <=0／
// 缺鍵一律退回 1，寬鬆值域不會讓戰鬥壞掉，比照 SkillRow.Effect 這類 JSONB 欄位的既有寬鬆風格）。
func (r RankRow) Validate() error {
	if !validMonsterRanks[r.Rank] {
		return fmt.Errorf("rank 必須是九級固定值之一")
	}
	if r.HPMult <= 0 || r.AtkMult <= 0 || r.DefMult <= 0 || r.MdefMult <= 0 {
		return fmt.Errorf("hp_mult/atk_mult/def_mult/mdef_mult 必須 > 0")
	}
	// 審查 PLAUSIBLE：倍率無上限的話，後台誤填超大數字會讓 ScaleMonsterByRank 算出天文數字
	// HP/ATK，戰鬥直接壞掉；1000 對齊 DB 欄位 NUMERIC(8,3) 的量級（實際九級表數值最大約 20），
	// 純粹當防呆上限，不影響既有校準資料。
	if r.HPMult > 1000 || r.AtkMult > 1000 || r.DefMult > 1000 || r.MdefMult > 1000 {
		return fmt.Errorf("hp_mult/atk_mult/def_mult/mdef_mult 不可超過 1000")
	}
	if err := r.Summon.Validate(); err != nil {
		return err
	}
	return nil
}

// RankDTO WIRE：GET /rpg/ranks 回應（「不含 summon／level_curve」）。
type RankDTO struct {
	Rank        string  `json:"rank"`
	Label       string  `json:"label"`
	SortOrder   int     `json:"sort_order"`
	HPMult      float64 `json:"hp_mult"`
	AtkMult     float64 `json:"atk_mult"`
	DefMult     float64 `json:"def_mult"`
	MdefMult    float64 `json:"mdef_mult"`
	Description string  `json:"description"`
	BadgeColor  string  `json:"badge_color"`
}

func toRankDTO(r RankRow) RankDTO {
	return RankDTO{
		Rank: r.Rank, Label: r.Label, SortOrder: r.SortOrder,
		HPMult: r.HPMult, AtkMult: r.AtkMult, DefMult: r.DefMult, MdefMult: r.MdefMult,
		Description: r.Description, BadgeColor: r.BadgeColor,
	}
}

// errRanksNotReady migration 189 尚未套用時（rpg_monster_ranks 表不存在）的固定 503 訊息，
// 比照既有 errJobsNotReady/errStrategiesNotReady 慣例。
const errRanksNotReady = "怪物強度九級尚未初始化（migration 189 未套用）"

const rankCols = `rank, label, sort_order, hp_mult, atk_mult, def_mult, mdef_mult, level_curve, summon, badge_color, description, calibrated_note`

func scanRank(row pgx.Row) (RankRow, error) {
	var r RankRow
	var curveRaw, summonRaw []byte
	err := row.Scan(&r.Rank, &r.Label, &r.SortOrder, &r.HPMult, &r.AtkMult, &r.DefMult, &r.MdefMult,
		&curveRaw, &summonRaw, &r.BadgeColor, &r.Description, &r.CalibratedNote)
	if err != nil {
		return r, err
	}
	r.LevelCurve = map[string]float64{}
	if len(curveRaw) > 0 {
		if uerr := json.Unmarshal(curveRaw, &r.LevelCurve); uerr != nil {
			r.LevelCurve = map[string]float64{} // 壞掉的 JSON 不該讓整支 API 500，比照既有 scanX 慣例
		}
	}
	if len(summonRaw) > 0 {
		if uerr := json.Unmarshal(summonRaw, &r.Summon); uerr != nil {
			r.Summon = SummonConfig{} // 同上：壞掉的召喚設定退回「不召喚」而不是讓戰鬥 bootstrap 500
		}
	}
	return r, nil
}

// listRanks GET /rpg/ranks／後台列表共用，依 sort_order（F→…→SS）。
func (h *Handler) listRanks(ctx context.Context) ([]RankRow, error) {
	rows, err := h.db.Query(ctx, `SELECT `+rankCols+` FROM rpg_monster_ranks ORDER BY sort_order, rank`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RankRow{}
	for rows.Next() {
		r, err := scanRank(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// getRank 單筆查詢（後台 PUT 回應、battle.go 組單場 rank 向量用）。查無資料回 pgx.ErrNoRows。
func (h *Handler) getRank(ctx context.Context, rank string) (RankRow, error) {
	row := h.db.QueryRow(ctx, `SELECT `+rankCols+` FROM rpg_monster_ranks WHERE rank=$1`, rank)
	return scanRank(row)
}

// getRanksByIDs 批次查詢（battle.go BattleEncounters/BattleBootstrap 用，避免逐場/逐怪各查一次
// 造成 N+1，比照 content_repo.go getMonstersByIDs／getScenesByIDs 的既有慣例）。查無資料的 id
// 不會出現在回傳 map 裡。
func (h *Handler) getRanksByIDs(ctx context.Context, ranks []string) (map[string]RankRow, error) {
	out := map[string]RankRow{}
	if len(ranks) == 0 {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `SELECT `+rankCols+` FROM rpg_monster_ranks WHERE rank = ANY($1)`, ranks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRank(rows)
		if err != nil {
			return nil, err
		}
		out[r.Rank] = r
	}
	return out, rows.Err()
}

// upsertRank 後台 PUT /admin/rpg/monster-ranks 用：rank 九值全部已由 migration 189 seed 好，
// 這裡的 ON CONFLICT (rank) DO UPDATE 只會走「更新既有列」這條路（WIRE：「不可新增刪除」，
// CHECK 值域本身也不允許第 10 個值），INSERT 分支理論上不會被真正觸發，保留只是讓函式在
// migration 189 剛套用、rpg_monster_ranks 意外缺列時仍有合理的預設可寫入。
//
// ⚠️ 刻意不寫 calibrated_note 欄位（既不在 INSERT 欄位清單、也不在 UPDATE SET 清單內）——WIRE
// 明講 PUT body 不含這欄，若把 RankRow 零值（後台請求 body 解析出來的空字串）寫進 UPDATE SET，
// 會把 migration 189 seed 好的校準警語靜靜洗成空字串。不寫入這欄，ON CONFLICT DO UPDATE 自動
// 保留該欄位目前的值，等同「唯讀」。
func (h *Handler) upsertRank(ctx context.Context, r RankRow) error {
	curveRaw, err := json.Marshal(r.LevelCurve)
	if err != nil {
		return err
	}
	summonRaw, err := json.Marshal(r.Summon)
	if err != nil {
		return err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO rpg_monster_ranks (rank, label, sort_order, hp_mult, atk_mult, def_mult, mdef_mult, level_curve, summon, badge_color, description, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NOW())
		ON CONFLICT (rank) DO UPDATE SET
			label=$2, sort_order=$3, hp_mult=$4, atk_mult=$5, def_mult=$6, mdef_mult=$7,
			level_curve=$8, summon=$9, badge_color=$10, description=$11, updated_at=NOW()`,
		r.Rank, r.Label, r.SortOrder, r.HPMult, r.AtkMult, r.DefMult, r.MdefMult, curveRaw, summonRaw, r.BadgeColor, r.Description)
	return err
}
