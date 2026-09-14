// scaling.go：DORPG P2（契約 dorpg_p2 §1 D2、§3.2）怪物/隊友數值縮放的純函式實作。
// 不碰 DB、不吃 Config 以外的任何外部狀態，方便單元測試逐條核對公式（見 scaling_test.go）。
// 呼叫端（battle.go）負責把 DB 讀出的 Config／角色 Derived／怪物列組成這裡要的輸入型別。
//
// ⚠️ battle_scale_mode 目前只有 "power"（D1：以玩家戰力為基準動態縮放）真正實作；"level"
// （依 Base Lv 絕對表，P3 用）與 "fixed"（直接用 DB 絕對值）只是 Config.Validate() 已經開放
// 的列舉值，讓後台可以先選、不會被 400 擋下，但本檔與呼叫端（battle.go）都還沒有依
// battle_scale_mode 切換分支——目前無論後台選哪個模式，實際算出來的數值都是 power 這條路徑
// （P2 沒有 bug，只是模式開關還沒接線；P3 若要真的支援另外兩種模式，要在呼叫 ScaleMonster
// 前依 cfg.BattleScaleMode 分派到對應公式，見 battle.go BattleBootstrap 內的 TODO(P3) 註解）。
//
// 混用命名慣例：本檔多數型別的 json tag 直接對齊前端 apps/web/src/lib/dorpg/types.ts 的
// camelCase（ActorStats 會被 battle.go 原封不動塞進 BattleSample.party[].stats /
// enemies[].stats），跟站上多數後端 API 慣用的 snake_case不同——這是契約 §3 明講的刻意混用，
// 不是疏漏。MonsterRow/CompanionRow 則走站上慣例 snake_case，因為它們同時也是 content_repo.go
// 的 DB 讀寫列與 battle_admin.go 後台 CRUD 的 JSON 序列化型別，逐欄對齊 migration 176 DDL。
package rpg

import "math"

// PlayerBattleStats 玩家在這場戰鬥要用的數值快照（Derived 套用保底值之後）。BaseLevel 用來決定
// 怪物顯示等級（D1）與隊友顯示等級（D3：玩家 Base Lv + level_offset）。
type PlayerBattleStats struct {
	Atk, Matk, Def, Mdef, HPMax, MPMax float64
	BaseLevel                          int
}

// PlayerBattleStatsFrom 從既有 Compute() 的 Derived + Base Lv 組出 PlayerBattleStats，套用
// battle_player_min_atk/battle_player_min_hp 保底（P2 專用：owner 現有角色六圍多數點數未配、
// Atk 個位數，若不設保底完全打不動任何怪；P3 接上裝備/技能有穩定成長曲線後這兩個保底欄位會
// 取消，見 config.go 對應欄位註解）。只對 Atk/HPMax 套保底——契約只點名這兩個欄位需要下限，
// Matk/Def/Mdef/MPMax 維持角色真實數值。
func PlayerBattleStatsFrom(cfg Config, baseLevel int, d Derived) PlayerBattleStats {
	atk := d.Atk
	if atk < cfg.BattlePlayerMinAtk {
		atk = cfg.BattlePlayerMinAtk
	}
	hpMax := float64(d.MaxHP)
	if hpMax < cfg.BattlePlayerMinHP {
		hpMax = cfg.BattlePlayerMinHP
	}
	return PlayerBattleStats{
		Atk:       atk,
		Matk:      d.Matk,
		Def:       d.Def,
		Mdef:      d.Mdef,
		HPMax:     hpMax,
		MPMax:     float64(d.MaxMP),
		BaseLevel: baseLevel,
	}
}

// ActorStats 對齊前端 apps/web/src/lib/dorpg/types.ts 的 ActorStats（見檔頭「混用命名慣例」）。
type ActorStats struct {
	HPMax float64 `json:"hpMax"`
	MPMax float64 `json:"mpMax"`
	Atk   float64 `json:"atk"`
	Matk  float64 `json:"matk"`
	Def   float64 `json:"def"`
	Mdef  float64 `json:"mdef"`
}

// MonsterRow rpg_monsters 資料列。content_repo.go 的讀寫、battle_admin.go 後台 CRUD 的 JSON
// 序列化、本檔的縮放計算三邊共用同一個型別，避免三處各自定義同一張表造成欄位漂移。
type MonsterRow struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Rank      string  `json:"rank"`
	Attribute string  `json:"attribute"`
	Size      string  `json:"size"`
	Race      string  `json:"race"`
	SpriteID  string  `json:"sprite_id"`
	PosterURL string  `json:"poster_url"`
	HPMult    float64 `json:"hp_mult"`
	AtkMult   float64 `json:"atk_mult"`
	DefMult   float64 `json:"def_mult"`
	SpeedMult float64 `json:"speed_mult"`
	Threat    int     `json:"threat"`
	IsBoss    bool    `json:"is_boss"`
	IsActive  bool    `json:"is_active"`
	SortOrder int     `json:"sort_order"`
}

// CompanionRow rpg_companions 資料列，共用原則同 MonsterRow。
type CompanionRow struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	PortraitID       string   `json:"portrait_id"`
	Role             string   `json:"role"`
	Weapon           string   `json:"weapon"`
	LevelOffset      int      `json:"level_offset"`
	HPMult           float64  `json:"hp_mult"`
	MPMult           float64  `json:"mp_mult"`
	AtkMult          float64  `json:"atk_mult"`
	MatkMult         float64  `json:"matk_mult"`
	DefMult          float64  `json:"def_mult"`
	MdefMult         float64  `json:"mdef_mult"`
	ActIntervalMult  float64  `json:"act_interval_mult"`
	SkillIDs         []string `json:"skill_ids"`
	IsPlayerPortrait bool     `json:"is_player_portrait"`
	IsActive         bool     `json:"is_active"`
	SortOrder        int      `json:"sort_order"`
}

// ScaledMonster 這隻怪在本場戰鬥實際要用的數值（已套保底/縮放，可直接組進 wire Enemy/stats）。
type ScaledMonster struct {
	HPMax, Atk, Matk, Def, Mdef int
	ActMinMs, ActMaxMs          int
	Level                       int
}

// minActIntervalMs 行動間隔下限防呆：DDL 沒有 CHECK 約束擋後台把 speed_mult 誤填成 0 或負值，
// 0/負的行動間隔換算出來會是 <=0ms——不是無效狀態（不用回錯），但讓怪物變成每個 tick 都出手，
// 這裡夾住下限，不讓一筆髒資料的怪把整場戰鬥的節奏弄壞。
const minActIntervalMs = 200

// ScaleMonster 契約 §1 D2 公式：
//
//	mobDef     = playerAtk × battle_mob_def_ratio × monster.def_mult × encounterScale
//	hitDamage  = max(1, playerAtk − mobDef)
//	mobHp      = round(hitDamage × battle_mob_hits × monster.hp_mult × encounterScale × slotScale)
//	actSec     = (enemy_act_min_ms+enemy_act_max_ms)/2000 × monster.speed_mult
//	mobAtk     = round(playerDef + playerHp × battle_enemy_dps_ratio × shareOfAtkMult × actSec × encounterScale)
//	mobMatk = mobAtk、mobMdef = mobDef（P2 敵人不分物魔）
//
// encounterScale＝該場 rpg_encounters.power_scale；slotScale＝該怪在
// rpg_encounter_monsters.power_scale（同場個別再乘，契約明講只乘進 HP，不乘 Def/Atk）；
// shareOfAtkMult＝AtkMultShares() 對整場怪算好的「這隻怪佔全場 atk_mult 的份額」（呼叫端一次
// 對整場算，見 battle.go buildBattleSample）。
func ScaleMonster(cfg Config, p PlayerBattleStats, m MonsterRow, encounterScale, slotScale, shareOfAtkMult float64) ScaledMonster {
	playerAtk := p.Atk
	playerDef := p.Def
	playerHP := p.HPMax

	mobDef := playerAtk * cfg.BattleMobDefRatio * m.DefMult * encounterScale
	hitDamage := playerAtk - mobDef
	if hitDamage < 1 {
		hitDamage = 1 // 契約 D2：max(1, playerAtk − mobDef)，避免怪 DEF 設太高時普攻永遠 0 傷、變成打不死
	}
	mobHP := hitDamage * cfg.BattleMobHits * m.HPMult * encounterScale * slotScale

	actSec := (float64(cfg.BattleEnemyActMinMs+cfg.BattleEnemyActMaxMs) / 2000) * m.SpeedMult
	mobAtk := playerDef + playerHP*cfg.BattleEnemyDPSRatio*shareOfAtkMult*actSec*encounterScale

	level := p.BaseLevel
	if m.IsBoss {
		level += 5 // 契約：「怪物顯示等級 = 玩家 Base Lv（boss +5，is_boss）」
	}

	actMin := int(math.Round(float64(cfg.BattleEnemyActMinMs) * m.SpeedMult))
	actMax := int(math.Round(float64(cfg.BattleEnemyActMaxMs) * m.SpeedMult))
	if actMin < minActIntervalMs {
		actMin = minActIntervalMs
	}
	if actMax < actMin {
		actMax = actMin
	}

	// HPMax 下限防呆（審查 data-4：前端 fixture.ts 的鏡像公式有 Math.max(1,...)，Go 端原本沒有
	// 對齊——理論上 hitDamage 保底 1 已經讓 mobHP 不會 <=0，但 hp_mult/slotScale/encounterScale
	// 若被後台誤填成極小值（例如 0.0001）仍可能四捨五入到 0，兩邊算式必須完全一致才不會出現
	// 「前端試算 HP=1 但後端 bootstrap 給 HP=0」的落差。
	hpMax := int(math.Round(mobHP))
	if hpMax < 1 {
		hpMax = 1
	}

	return ScaledMonster{
		HPMax:    hpMax,
		Atk:      int(math.Round(mobAtk)),
		Matk:     int(math.Round(mobAtk)), // P2 敵人不分物魔（D2）
		Def:      int(math.Round(mobDef)),
		Mdef:     int(math.Round(mobDef)),
		ActMinMs: actMin,
		ActMaxMs: actMax,
		Level:    level,
	}
}

// ScaleCompanion 契約 D3：隊友數值＝玩家數值 × rpg_companions.*_mult，不另存絕對值。
// cfg 參數目前沒有直接用在乘法公式裡，簽名保留是為了跟 ScaleMonster 對稱——之後如果要對隊友
// 也套用某種係數（例如保底值），呼叫端不必再改。
func ScaleCompanion(cfg Config, p PlayerBattleStats, c CompanionRow) ActorStats {
	return ActorStats{
		HPMax: p.HPMax * c.HPMult,
		MPMax: p.MPMax * c.MPMult,
		Atk:   p.Atk * c.AtkMult,
		Matk:  p.Matk * c.MatkMult,
		Def:   p.Def * c.DefMult,
		Mdef:  p.Mdef * c.MdefMult,
	}
}

// AtkMultShares 算「這場戰鬥全體怪物」的 DPS 分配份額（契約 D2：
// enemyShare = monster.atk_mult / Σ(atk_mult)）。輸入的 atkMults 只放「目前存活」的怪（契約：
// 「同一場所有存活怪」），順序即回傳份額的順序。Σ<=0（例如全部怪的 atk_mult 被後台設成 0）時
// 退化成平均分配，避免除以 0 產生 NaN——NaN 編碼進 JSON 是不合法字面值，會讓前端 JSON.parse
// 直接炸掉整個 bootstrap 回應。
func AtkMultShares(atkMults []float64) []float64 {
	shares := make([]float64, len(atkMults))
	if len(atkMults) == 0 {
		return shares
	}
	sum := 0.0
	for _, v := range atkMults {
		sum += v
	}
	if sum <= 0 {
		equal := 1.0 / float64(len(atkMults))
		for i := range shares {
			shares[i] = equal
		}
		return shares
	}
	for i, v := range atkMults {
		shares[i] = v / sum
	}
	return shares
}

// PlayerPower 顯示/遙測用戰力數字（不是戰鬥公式的一部分，只用於角色摘要列與 rpg_battle_logs
// 稽核欄位）：round(Atk + Def + HPMax/10)。
func PlayerPower(p PlayerBattleStats) int {
	return int(math.Round(p.Atk + p.Def + p.HPMax/10))
}

// --- 參考 HP/MP 縮放（P2 修正第 1 輪，根因見 config.go BattleReferenceHP/MP 欄位註解）---
//
// referenceRatioClampMin/Max：clamp 上下限。理由要寫清楚，不是隨手挑的數字——不設上限的話，
// 之後角色 HP 破萬（例如破萬 HPMax 的高等級玩家）會讓一瓶藥水補到荒謬的量（等同直接全滿）；
// 不設下限的話，全新角色（HPMax 遠低於參考值）會被縮放到補不了血（flat/amount 被縮到 0 附近，
// 即使 max(1,...) 保底也會變成「補 1 點」等於沒補）。0.25~8 倍是契約給定的邊界，兩端都留了
// 足夠空間讓縮放在正常等級範圍內幾乎感覺不到（owner 現況 HPMax 919 ÷ 參考值 300 ≈ 3.06 倍，
// 落在中段，不會被 clamp 夾住）。
const (
	referenceRatioClampMin = 0.25
	referenceRatioClampMax = 8
)

func clampReferenceRatio(v float64) float64 {
	if v < referenceRatioClampMin {
		return referenceRatioClampMin
	}
	if v > referenceRatioClampMax {
		return referenceRatioClampMax
	}
	return v
}

// referenceRatioHP/MP：玩家實際 HPMax/MPMax 相對「參考玩家」的倍率。cfg.BattleReferenceHP/MP
// 已由 Config.Validate() 保證 > 0，這裡的 <=0 防呆只是避免萬一有呼叫端繞過 Validate（例如測試
// 直接建構 Config 零值）時除以 0 產生 +Inf 而不是直接 panic 或算出 NaN。
func referenceRatioHP(cfg Config, p PlayerBattleStats) float64 {
	ref := cfg.BattleReferenceHP
	if ref <= 0 {
		ref = 1
	}
	return clampReferenceRatio(p.HPMax / ref)
}

func referenceRatioMP(cfg Config, p PlayerBattleStats) float64 {
	ref := cfg.BattleReferenceMP
	if ref <= 0 {
		ref = 1
	}
	return clampReferenceRatio(p.MPMax / ref)
}

// ScaleSkill 契約「參考 HP 縮放」：DB 存的 rpg_skills.flat 語意是「參考玩家（HPMax=
// battle_reference_hp）身上的絕對回復量」，不是任何角色都通用的絕對值——玩家 HPMax 偏離參考值
// 時，heal/shield 類技能的 flat 要跟著等比例縮放，否則玩家等級越高，一次治療佔血量的比例反而
// 越低（審查 data.md 缺陷1：owner 實測 Lv27 HPMax 919 時，小咪 flat=80 治療從佔 27% 掉到
// 8.7%）。
//
// 只縮放 flat，其餘欄位刻意不動：
//   - coefficient 乘的是 MATK，MATK 本身已經隨角色六圍/等級自然成長，不需要再疊加一次縮放；
//   - mp_cost/cooldown_ms/cast_ms 是節奏/資源參數，跟角色強度無關；
//   - kind=damage 的 flat 是加在玩家 ATK 上的固定傷害加成，ATK 已有自己的保底
//     （battle_player_min_atk）與自然成長，契約明講這種 flat 完全不動。
func ScaleSkill(cfg Config, p PlayerBattleStats, s SkillRow) SkillRow {
	if s.Kind != "heal" && s.Kind != "shield" {
		return s
	}
	out := s
	flat := math.Round(float64(s.Flat) * referenceRatioHP(cfg, p))
	if flat < 1 {
		flat = 1 // 避免縮放後變成 0（等同技能完全沒有效果，跟後台設計的「一定有基礎量」矛盾）
	}
	out.Flat = int(flat)
	return out
}

// ScaleItem 契約「參考 HP 縮放」：rpg_items.amount 對 hp/mp 兩種道具的語意同上（參考玩家身上
// 的絕對回復量），對 revive 道具則維持「復活後 HP 佔 hpMax 的百分比」，這是相對值本來就不需要
// 縮放（契約明講 revive 不動）。
func ScaleItem(cfg Config, p PlayerBattleStats, it ItemRow) ItemRow {
	out := it
	switch it.Kind {
	case "hp":
		amt := math.Round(float64(it.Amount) * referenceRatioHP(cfg, p))
		if amt < 1 {
			amt = 1
		}
		out.Amount = int(amt)
	case "mp":
		amt := math.Round(float64(it.Amount) * referenceRatioMP(cfg, p))
		if amt < 1 {
			amt = 1
		}
		out.Amount = int(amt)
	// case "revive"：不動（契約明講；amount 是百分比，跟玩家 HPMax 無關）。
	}
	return out
}
