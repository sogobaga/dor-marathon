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
	// Rating P2（暴擊／Miss／無效攻擊）新增：命中/暴擊評級，直接取自 Compute() 的
	// Derived.Hit/Flee/CritPct/CritShield，不套用任何保底——SPEC 只點名 Atk/HPMax 需要下限
	// （owner 現有角色六圍多數點數未配，完全打不動任何怪），Hit/Flee/CritPct/CritShield 維持
	// 角色真實數值，DEX/AGI/LUK 的配點才第一次對戰鬥產生意義（保底掉配點差異就沒有意義了）。
	Rating CombatRating
}

// CombatRating 對齊前端 apps/web/src/lib/dorpg/types.ts 的 CombatRating（P2）。命名慣例同檔頭
// 「混用命名慣例」：這裡直接用 camelCase，因為要被組進 wire party[].rating / enemies[].rating，
// 前端型別「直接吃」，不再轉換一次大小寫。
//
// ⚠️ Hit/Flee 不是 0~100 的機率百分比制——它們是跟玩家 Compute()/怪物 MonsterRating 一樣的
// 「評級數值空間」，隨等級與素質線性成長、本來就不封頂在 100（前端 missChance() 拿
// (defenderFlee−attackerHit) 的差額去換算才是真正的機率）；只有 CritPct/CritShield 才是
// 0~100 的百分比制（P2 修正第 3 輪：審查 data.md 抓到本檔先前這段註解誤植成「0~100 百分比」，
// 這裡改成如實描述，不影響任何計算，純粹是文件正確性修正）。
type CombatRating struct {
	Hit        float64 `json:"hit"`
	Flee       float64 `json:"flee"`
	CritPct    float64 `json:"critPct"`
	CritShield float64 `json:"critShield"`
	// Aspd/CastReductionPct P2 修正第 3 輪新增：AGI→攻速→攻擊冷卻、DEX→詠唱縮減（使用者當面
	// 要求，同時緩解「DEX 中後期只剩命中一個用途」的問題）。玩家直接取 Compute() 的
	// Derived.Aspd/CastReductionPct；隊友與怪物給 Aspd=battle_aspd_reference、
	// CastReductionPct=0（＝維持 config 固定的攻擊冷卻/施放時間，見 PlayerBattleStatsFrom
	// 與 MonsterRating/CompanionRating 各自的組裝邏輯），只有玩家的配點會影響戰鬥節奏。
	Aspd             float64 `json:"aspd"`
	CastReductionPct float64 `json:"castReductionPct"`
	// CritDmgPct 審查#5【低・PLAUSIBLE】新增：被動技能 crit_dmg_pct 加成的總和（%，見 compute.go
	// Derived.CritDmgPct 註解）。過去 Compute() 算出這個值卻從未接進 PlayerBattleStats.Rating，
	// engine 的暴擊倍率（effects.ts rollCritMultiplier）因此永遠讀不到玩家的暴擊傷害加成——
	// 玩家點了 crit_dmg_pct 被動技能，角色頁看得到數字，實戰卻毫無效果。怪物/隊友沒有這個加成
	// 來源（MonsterRating 不設值，零值即 0；CompanionRating 直接沿用玩家值，同其餘欄位）。
	CritDmgPct float64 `json:"critDmgPct"`
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
		// CritPct 在 Compute() 已經是百分比尺度（compute.go：`CritPct float64 json:"crit_pct"
		// // 暴擊率（%）`），這裡直接沿用不再乘 100——呼叫端 SPEC 特別提醒過這一點。
		// Aspd/CastReductionPct 同樣直接取自 Compute() 的 Derived（已套用 aspd_cap/cast_cap_pct
		// 上限），不另外保底——保底只點名 Atk/HPMax（見上）。
		Rating: CombatRating{
			Hit: d.Hit, Flee: d.Flee, CritPct: d.CritPct, CritShield: d.CritShield,
			Aspd: d.Aspd, CastReductionPct: d.CastReductionPct,
			// 審查#5：CritDmgPct 直接取自 Compute() 的 Derived.CritDmgPct，不套用任何保底/換算
			// （跟 Hit/Flee/CritPct/CritShield 同一個精神，只有 Atk/HPMax 才有保底，見上方註解）。
			CritDmgPct: d.CritDmgPct,
		},
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
	// WeakElements P5（CONTRACT §6）：這隻怪的弱點屬性桶（DOR 8 桶英文代碼，例如
	// metal/wood/water/fire/earth/light/dark/neutral——見 content.go validElementKinds）。
	// 技能 element 命中其中之一 → 傷害 ×(1+battle_weakness_bonus_pct/100)，見 battle.go
	// buildWireConfig／wireEnemy。既有 5 隻怪的實際清單由另一個 Workflow 產生，貼進
	// migration 180 的 MONSTER WEAKNESS SEED 標記之間，本檔只負責型別/讀寫。
	WeakElements []string `json:"weak_elements"`
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
	// JobID DORPG P6（CONTRACT §3.1）：小咪 cleric／小優 archer／阿光 light_knight／
	// 阿深 heavy_knight；小井（is_player_portrait）維持 NULL——不是傭兵。決定這位傭兵的腳本
	// 只能配哪個職業的技能（presets.go ValidatePreset）與戰鬥 bootstrap 的 WeaponType/被動。
	JobID *string `json:"job_id"`
}

// ScaledMonster 這隻怪在本場戰鬥實際要用的數值（已套保底/縮放，可直接組進 wire Enemy/stats）。
type ScaledMonster struct {
	HPMax, Atk, Matk, Def, Mdef int
	ActMinMs, ActMaxMs          int
	Level                       int
	// Rating P2 新增：這隻怪的命中/暴擊評級（見 MonsterRating），組進 wire Enemy.rating。
	Rating CombatRating
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
		Rating:   MonsterRating(cfg, p, m),
	}
}

// ScaleMonsterByLevel DORPG P6（CONTRACT §2）：battle_scale_mode="level" 的怪物數值公式——
// 不再用「玩家戰力」當基準（那是 ScaleMonster/"power" 模式），改用「參考玩家 RefPlayerStats(cfg,N)」
// （reflevel.go，N＝這場遭遇的 rpg_encounters.monster_level）當絕對基準，讓六場遭遇的強度差異
// 由「monster_level 這個數字」直接決定，跟打這場戰鬥的玩家自己多強完全無關：
//
//	hp   = floor(Ref.HPMax × hp_mult   × battle_lvl_hp_ratio   × encounterScale × slotScale)
//	atk  = floor(Ref.Atk   × atk_mult  × battle_lvl_atk_ratio  × encounterScale)
//	def  = floor(Ref.Def   × def_mult  × battle_lvl_def_ratio  × encounterScale)
//	mdef = floor(Ref.Mdef  × mdef_mult × battle_lvl_mdef_ratio × encounterScale)
//	matk = floor(Ref.Matk  × atk_mult)   —— 契約明講怪物施法用的 matk 只乘 atk_mult，
//	                                        不疊加 battle_lvl_atk_ratio/encounterScale
//	                                        （純粹給少數會施法的怪物一個粗略基準，非本輪
//	                                        主要縮放對象，故意跟 atk 走不同公式，不是疏漏）。
//
// hit/flee 沿用「等級基線」公式（同 MonsterRating），但基線換成怪物自己的等級 N，不再是玩家
// Base Lv——這正是「怪物有沒有獨立等級概念」在 level 模式下唯一的差異點：power 模式沒有怪物
// 等級，只能借用玩家等級當命中/迴避基線；level 模式怪物本來就有自己的 N，改用它更符合直覺。
// 行動間隔沿用既有 speed_mult 換算（跟 ScaleMonster 完全相同，不是縮放的一部分）。
func ScaleMonsterByLevel(cfg Config, m MonsterRow, encounterScale, slotScale float64, level int) ScaledMonster {
	ref := RefPlayerStats(cfg, level)

	// MonsterRow 沒有獨立的 mdef_mult 欄位（跟 ScaleMonster/"power" 模式一樣——power 模式的
	// mdef 也是直接沿用 mobDef，見上面 ScaleMonster「P2 敵人不分物魔」），mdef 沿用同一個
	// DefMult，只是另外乘 battle_lvl_mdef_ratio 這個獨立比例（後台仍可分開調物防/魔防手感）。
	hp := float64(ref.MaxHP) * m.HPMult * cfg.BattleLvHPRatio * encounterScale * slotScale
	atk := ref.Atk * m.AtkMult * cfg.BattleLvAtkRatio * encounterScale
	def := ref.Def * m.DefMult * cfg.BattleLvDefRatio * encounterScale
	mdef := ref.Mdef * m.DefMult * cfg.BattleLvMdefRatio * encounterScale
	matk := ref.Matk * m.AtkMult

	hpMax := int(math.Floor(hp))
	if hpMax < 1 {
		hpMax = 1 // 同 ScaleMonster：避免極端小倍率把 HP 四捨五入/取整到 0（打不死也殺不掉的無效狀態）
	}

	actMin := int(math.Round(float64(cfg.BattleEnemyActMinMs) * m.SpeedMult))
	actMax := int(math.Round(float64(cfg.BattleEnemyActMaxMs) * m.SpeedMult))
	if actMin < minActIntervalMs {
		actMin = minActIntervalMs
	}
	if actMax < actMin {
		actMax = actMin
	}

	return ScaledMonster{
		HPMax:    hpMax,
		Atk:      int(math.Floor(atk)),
		Matk:     int(math.Floor(matk)),
		Def:      int(math.Floor(def)),
		Mdef:     int(math.Floor(mdef)),
		ActMinMs: actMin,
		ActMaxMs: actMax,
		Level:    level, // 契約：「怪物 level 欄位送前端顯示 Lv.N」——level 模式直接用 N，不像 power 模式借用玩家等級 + boss 加成
		Rating:   MonsterRatingByLevel(cfg, level, m),
	}
}

// MonsterRatingByLevel DORPG P6：同 MonsterRating 的等級基線公式，但基線換成怪物自己的等級 N
// （level 模式沒有「玩家」這個縮放來源，見 ScaleMonsterByLevel 檔頭）。CritPct/CritShield 語意
// 與 MonsterRating 完全相同（不吃等級，只吃 def_mult）。
func MonsterRatingByLevel(cfg Config, level int, m MonsterRow) CombatRating {
	lv := float64(level)
	hit := lv*cfg.BattleMonsterHitPerLevel + cfg.BattleMonsterHitBase
	if cfg.BattleMonsterHitMax > 0 && hit > cfg.BattleMonsterHitMax {
		hit = cfg.BattleMonsterHitMax
	}
	return CombatRating{
		Hit:              hit,
		Flee:             (lv*cfg.BattleMonsterFleePerLevel + cfg.BattleMonsterFleeBase) * m.SpeedMult,
		CritPct:          cfg.BattleMonsterCritPct,
		CritShield:       cfg.BattleMonsterCritShieldBase * m.DefMult,
		Aspd:             cfg.BattleAspdReference,
		CastReductionPct: 0,
	}
}

// MonsterRating P2 修正第 3 輪（審查 data.md 缺陷1/2 CONFIRMED 根因修復）：怪物沒有配點系統，
// 命中/迴避改成跟著「玩家等級基線」走，不再是與等級無關的絕對常數——舊版
// hit=battle_monster_hit_base(100固定)、flee=battle_monster_flee_base(8)×speed_mult，
// 而玩家 hit/flee 隨 Base Lv 線性成長且不封頂（flee 另被 flee_cap_pct=95 封頂），兩邊尺度不搭
// 造成兩個 CONFIRMED 缺陷：① 玩家 flee 封頂 95 後 (95−100)<0 恆為負，AGI 配到滿也不可能提高
// 迴避率；② 約 Lv9 起 playerHit 穩定超過怪物 flee=8，missChance 恆卡下限，DEX 配點中後期
// 形同虛設。修法：兩邊都用「玩家 Base Lv」當基線，且斜率（*_per_level 預設 1.0）對齊玩家
// LvHit/LvFlee=1，讓兩邊等級成長同步（維持 D1「怪物數值依玩家縮放、等級無關」架構——這裡的
// 「等級」用的是玩家 Base Lv，不是怪物自己的等級，怪物依然沒有獨立等級概念）：
//
//	hit        = playerBaseLevel × battle_monster_hit_per_level  + battle_monster_hit_base
//	flee       = (playerBaseLevel × battle_monster_flee_per_level + battle_monster_flee_base) × speed_mult
//	critPct    = battle_monster_crit_pct
//	critShield = battle_monster_crit_shield_base × def_mult（越硬的怪越不容易被暴擊，直覺對應防禦力）
//
// 語意：完全不配 AGI/DEX 的角色（Compute() 算出的 Hit/Flee 恰好等於 baseLv×1+2）與怪物打平，
// missChance 落在下限；每配一點 AGI/DEX 才會真的把差距拉開（效果驗算見 config.go
// BattleMonsterHitBase/FleeBase 欄位註解）。
//
// 不吃 encounterScale/slotScale：SPEC 明講這兩個縮放只影響 HP（跟 ScaleMonster 的 mobDef/mobAtk
// 一致，只有 HP 有 slotScale 這個額外維度），評級是「這隻怪天生的閃避/防暴擊體質（疊加玩家等級
// 基線）」，不該因為同一場戰鬥用不同 power_scale 開場就跟著變。
func MonsterRating(cfg Config, p PlayerBattleStats, m MonsterRow) CombatRating {
	lv := float64(p.BaseLevel)
	// 高等級保護（2026-09-14 對抗式審查 CONFIRMED）：玩家 flee 被 flee_cap_pct（預設 95）硬性封頂，
	// 而這裡的 hit 隨 Base Lv 線性成長不封頂——Lv93 之後 hit ≥ 95，玩家 AGI 配到滿（flee 也只到 95）
	// 都無法讓 missChance 離開下限，「AGI 有沒有用」這個缺陷會在高等級原樣重演。夾在
	// battle_monster_hit_max（預設 75，刻意比 flee_cap_pct 低 20）之下，任何等級都保證留有投資空間。
	hit := lv*cfg.BattleMonsterHitPerLevel + cfg.BattleMonsterHitBase
	if cfg.BattleMonsterHitMax > 0 && hit > cfg.BattleMonsterHitMax {
		hit = cfg.BattleMonsterHitMax
	}
	return CombatRating{
		Hit:        hit,
		Flee:       (lv*cfg.BattleMonsterFleePerLevel + cfg.BattleMonsterFleeBase) * m.SpeedMult,
		CritPct:    cfg.BattleMonsterCritPct,
		CritShield: cfg.BattleMonsterCritShieldBase * m.DefMult,
		// 怪物沒有攻速/詠唱概念（P2 敵人只有固定行動間隔 ActMinMs/ActMaxMs，跟玩家的攻擊
		// 冷卻/施放時間是兩套獨立機制），這裡給 aspd_reference 只是讓 CombatRating 這個共用
		// 結構體有個一致的「基準值」可填，wire 序列化上前端引擎目前也不會拿怪物的 aspd 做任何
		// 事——純粹避免欄位是零值造成誤解（例如以為這隻怪 aspd=0）。
		Aspd:             cfg.BattleAspdReference,
		CastReductionPct: 0,
	}
}

// CompanionRating SPEC §1：「隊友再乘 companion 的對應倍率，沒有的話沿用玩家值」——
// rpg_companions（migration 176/177 既定 DDL）目前沒有 hit_mult/flee_mult/crit_pct_mult/
// crit_shield_mult 這類欄位，本輪硬規則禁止新增 migration，所以「沒有的話」目前是唯一情形：
// Hit/Flee/CritPct/CritShield 直接沿用玩家評級。c 參數保留（不使用）只是為了跟 ScaleCompanion
// 簽名對稱——之後 DB 若真的加上這些倍率欄位，呼叫端不必再改函式簽名，只要在這裡補上乘法。
//
// Aspd/CastReductionPct 是本輪（P2 修正第 3 輪）新增的例外：明講只套用在玩家身上（使用者當面
// 要求），隊友的攻擊冷卻/施放時間固定走 config 值，不隨玩家配點連動加速——否則玩家配好 AGI/DEX
// 會連帶讓隊友 AI 也跟著變快，既有隊友節奏平衡（BattleAllyActMinMs/MaxMs 等既有調校）會被打亂，
// 且不是這次要解決的問題（AGI/DEX 該影響的是「玩家自己打得多快」）。
func CompanionRating(cfg Config, playerRating CombatRating, c CompanionRow) CombatRating {
	r := playerRating
	r.Aspd = cfg.BattleAspdReference
	r.CastReductionPct = 0
	return r
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
