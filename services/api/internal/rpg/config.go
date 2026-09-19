// Package rpg 遊戲化系統第一輪：玩家角色數值（參考 RO 仙境傳說 stat/attr 素質系統，
// https://ro.ntome.com/stat/attr 由站長截圖提供對照表）。本檔為所有係數的「設定檔」——
// 每一條 RO 素質規則都對應一個可由後台調整的欄位，DefaultConfig() 是把 owner 提供的對照表
// 原封不動編碼成程式預設值；後台可整包覆寫（見 appsettings key rpg_config，JSON 存放）。
//
// ⚠️ 欄位名稱（json tag）已與前端 apps/web/src/lib/api.ts 的 RpgConfig type 對齊，勿自行改名
// ——前端「參數設定」分頁（apps/web/src/lib/rpgMeta.ts CONFIG_GROUPS）逐欄輸入框直接對應這裡
// 的 json tag，改名會讓後台頁面欄位全部失聯。
//
// 大部分「每幾點 +1」的規則拆成兩種欄位：
//   - 每 1 點的直接係數（例：str_melee_atk、agi_flee、int_matk）：value = stat * coef。
//   - 每 N 點才 +1 的門檻（例：agi_def_per=5、luk_atk_per=3）：grant 固定為 1，只有 N 可調，
//     value = floor(stat / N) * 1；因此這類欄位語意是「除數」而非「每點係數」。
//
// 現階段沒有裝備系統：EquipAtk/EquipMatk 恆為 0（Compute 直接吃 0，這裡只保留「每點裝備攻擊力
// +0.5%」的乘數係數，供之後接裝備時直接套用，不必再動 Config 形狀）。
package rpg

import (
	"encoding/json"
	"fmt"
)

// intMPRegenThreshold INT 決定 MP 自然恢復量的門檻值（RO 對照表固定為 120，前端未把這個門檻
// 本身開放成設定欄位——只有門檻前的除數 IntMPRegenPer、門檻獎勵 IntMPRegenAt120、門檻後的除數
// IntMPRegenPerAfter120 可調），因此在程式內寫死。
const intMPRegenThreshold = 120

// Config 遊戲化數值參數集合。全部欄位皆可由後台 /admin/rpg/config 覆寫（JSON 整包 PUT）。
type Config struct {
	// --- 初始值與加點規則 ---
	InitialStat       int    `json:"initial_stat"`        // 六圍初始值（RO 預設 1）
	InitialFreePoints int    `json:"initial_free_points"` // 初始可配點數（owner 指定 40）
	MaxStat           int    `json:"max_stat"`            // 單一素質上限
	CostBase          int    `json:"cost_base"`           // 加點成本公式基底：cost(n)=floor((n-1)/step)+base
	CostStepEvery     int    `json:"cost_step_every"`     // 加點成本公式的級距（每幾點成本+1）
	DefaultWeaponType string `json:"default_weapon_type"` // "melee" | "ranged"（現階段沒有裝備欄位，用系統設定決定素質物攻走哪個分支）

	// --- HP / MP ---
	BaseHP         float64 `json:"base_hp"`           // HP 基礎值（Base Lv=0 時）
	HPPerBaseLevel float64 `json:"hp_per_base_level"` // 每 Base Lv 增加的 HP（乘 VIT 加成前）
	BaseMP         float64 `json:"base_mp"`           // MP 基礎值
	MPPerBaseLevel float64 `json:"mp_per_base_level"` // 每 Base Lv 增加的 MP（乘 INT 加成前）

	// --- 攻速 Aspd（0~aspd_cap，RO 上限 193） / 負重 ---
	AspdBase     float64 `json:"aspd_base"`      // 攻速基礎值
	AspdPerAgi   float64 `json:"aspd_per_agi"`   // 每 1 點 AGI 增加的攻速
	AspdPerDex   float64 `json:"aspd_per_dex"`   // 每 1 點 DEX 增加的攻速（"稍為增加"，係數小於 AGI）
	AspdCap      float64 `json:"aspd_cap"`       // 攻速上限
	WeightBase   float64 `json:"weight_base"`    // 基礎負重量（STR 加成前）
	WeightPerStr float64 `json:"weight_per_str"` // 每 1 點 STR 增加的負重量

	// --- STR：近戰武器每1點素質物攻+1、裝備物攻+0.5%；遠程武器每N點素質物攻+1 ---
	StrMeleeAtk     float64 `json:"str_melee_atk"`      // 每 1 點：近戰素質物攻
	StrEquipAtkPct  float64 `json:"str_equip_atk_pct"`  // 每 1 點：裝備物攻 %（乘在 EquipAtk 上，現階段 EquipAtk=0 故無感）
	StrRangedAtkPer float64 `json:"str_ranged_atk_per"` // 每幾點：遠程素質物攻+1（除數，預設5）

	// --- AGI：每1點迴避+1、攻速加成(見上)、狀態抗性([出血][睡眠][著火])；每N點物防+1 ---
	AgiFlee   float64 `json:"agi_flee"`    // 每 1 點：迴避率
	AgiDefPer float64 `json:"agi_def_per"` // 每幾點：物理防禦力+1（除數，預設5）

	// --- VIT：每1點最大HP+1%、道具HP恢復+2%、狀態抗性([暈眩][中毒])；每N點物防/魔防/HP自然恢復+1；
	//     每 hp_regen_per_max_hp 點最大HP：HP自然恢復+1 ---
	VitHPPct        float64 `json:"vit_hp_pct"`          // 每 1 點：最大HP %
	VitItemHPPct    float64 `json:"vit_item_hp_pct"`     // 每 1 點：道具HP恢復量 %
	VitDefPer       float64 `json:"vit_def_per"`         // 每幾點：物理防禦力+1（除數，預設2）
	VitMdefPer      float64 `json:"vit_mdef_per"`        // 每幾點：魔法防禦力+1（除數，預設5）
	VitHPRegenPer   float64 `json:"vit_hp_regen_per"`    // 每幾點：HP自然恢復量+1（除數，預設5）
	HPRegenPerMaxHP float64 `json:"hp_regen_per_max_hp"` // 每多少最大HP：HP自然恢復量+1（除數，預設200）

	// --- DEX：每1點遠程素質物攻+1、裝備物攻+0.5%、命中+1、變動詠唱縮減、攻速稍增；
	//     每N點近戰素質物攻/魔攻/魔防+1 ---
	DexRangedAtk   float64 `json:"dex_ranged_atk"`    // 每 1 點：遠程素質物攻
	DexEquipAtkPct float64 `json:"dex_equip_atk_pct"` // 每 1 點：裝備物攻 %
	DexHit         float64 `json:"dex_hit"`           // 每 1 點：命中率
	DexMeleeAtkPer float64 `json:"dex_melee_atk_per"` // 每幾點：近戰素質物攻+1（除數，預設5）
	DexMatkPer     float64 `json:"dex_matk_per"`      // 每幾點：魔法攻擊力+1（除數，預設5）
	DexMdefPer     float64 `json:"dex_mdef_per"`      // 每幾點：魔法防禦力+1（除數，預設5）

	// --- INT：每1點魔攻+1.5、魔防+1、最大MP+1%、道具MP恢復+1%、變動詠唱縮減(DEX一半)、
	//     狀態抗性([黑暗][沉默][恐怖])；達 intMPRegenThreshold(120) 前每N點MP自然恢復+1，
	//     達門檻額外+X，門檻後每M點再+1；每多少最大MP：MP自然恢復+1 ---
	IntMatk               float64 `json:"int_matk"`                   // 每 1 點：魔法攻擊力（1.5）
	IntMdef               float64 `json:"int_mdef"`                   // 每 1 點：魔法防禦力
	IntMPPct              float64 `json:"int_mp_pct"`                 // 每 1 點：最大MP %
	IntItemMPPct          float64 `json:"int_item_mp_pct"`            // 每 1 點：道具MP恢復量 %
	IntMPRegenPer         float64 `json:"int_mp_regen_per"`           // 門檻前每幾點：MP自然恢復+1（除數，預設6）
	IntMPRegenAt120       float64 `json:"int_mp_regen_at_120"`        // 達到門檻(120)時：MP自然恢復額外值（預設4）
	IntMPRegenPerAfter120 float64 `json:"int_mp_regen_per_after_120"` // 門檻後每幾點：MP自然恢復+1（除數，預設2）
	MPRegenPerMaxMP       float64 `json:"mp_regen_per_max_mp"`        // 每多少最大MP：MP自然恢復+1（除數，預設100）

	// --- LUK：每1點暴擊率+0.3、狀態抗性([詛咒][混亂][恐怖][著火])；每N點物攻/魔攻/命中/迴避/
	//     暴擊迴避/完全迴避+1 ---
	LukCrit            float64 `json:"luk_crit"`              // 每 1 點：暴擊率（0.3）
	LukAtkPer          float64 `json:"luk_atk_per"`           // 每幾點：物理攻擊力+1（除數，預設3）
	LukMatkPer         float64 `json:"luk_matk_per"`          // 每幾點：魔法攻擊力+1（除數，預設3）
	LukHitPer          float64 `json:"luk_hit_per"`           // 每幾點：命中率+1（除數，預設3）
	LukFleePer         float64 `json:"luk_flee_per"`          // 每幾點：迴避率+1（除數，預設5）
	LukCritShieldPer   float64 `json:"luk_crit_shield_per"`   // 每幾點：暴擊迴避率+1（除數，預設5）
	LukPerfectDodgePer float64 `json:"luk_perfect_dodge_per"` // 每幾點：完全迴避+1（除數，預設10）

	// --- 基本等級（Base Level，取自 users.exp 換算的既有 DOR 等級）：
	//     每1級命中+1、迴避+1；每N級物防/物攻/魔攻/魔防+1 ---
	LvHit     float64 `json:"lv_hit"`      // 每 1 級：命中率
	LvFlee    float64 `json:"lv_flee"`     // 每 1 級：迴避率
	LvDefPer  float64 `json:"lv_def_per"`  // 每幾級：物理防禦力+1（除數，預設2）
	LvAtkPer  float64 `json:"lv_atk_per"`  // 每幾級：物理攻擊力+1（除數，預設4）
	LvMatkPer float64 `json:"lv_matk_per"` // 每幾級：魔法攻擊力+1（除數，預設4）
	LvMdefPer float64 `json:"lv_mdef_per"` // 每幾級：魔法防禦力+1（除數，預設4）

	// --- 變動詠唱時間縮減：DEX 1點=cast_unit_dex單位，INT 1點=cast_unit_int單位，
	//     每單位縮減 cast_pct_per_unit%，總和封頂 cast_cap_pct% ---
	CastUnitDex    float64 `json:"cast_unit_dex"`     // DEX 每點對應的「單位」數（預設1）
	CastUnitInt    float64 `json:"cast_unit_int"`     // INT 每點對應的「單位」數（預設0.5＝DEX一半）
	CastPctPerUnit float64 `json:"cast_pct_per_unit"` // 每單位縮減百分比（預設0.5）
	CastCapPct     float64 `json:"cast_cap_pct"`      // 縮減上限百分比（預設50）

	// --- 狀態抗性（AGI/VIT/INT/LUK 共用同一係數，見 compute.go 各狀態對應表）／迴避率上限 ---
	ResistPctPerPoint float64 `json:"resist_pct_per_point"` // 每點狀態抗性 %（預設0.1）
	FleeCapPct        float64 `json:"flee_cap_pct"`         // 迴避率上限（完全迴避不受此限，預設95）

	// --- DORPG P2：戰鬥內容/縮放/手感參數（契約 dorpg_p2 §3.1）。json tag 必須與
	// apps/web/src/lib/rpgMeta.ts CONFIG_GROUPS 逐欄一致——沿用上面同一條規則，這批欄位是
	// battle_* 前綴、跟六圍素質欄位分開一群，方便後台開新分頁「戰鬥」。 ---

	// BattleScaleMode 怪物數值縮放模式（D1）："power"＝以玩家戰力動態縮放（P2 唯一實作）；
	// "level"＝依 Base Lv 絕對表（P3 保留分支點）；"fixed"＝直接用 DB 絕對值（同上）。
	BattleScaleMode string `json:"battle_scale_mode"`
	// BattleMobHits 未蓄氣普攻打死一般怪的目標次數（D2 mobHp 公式的係數）。TUNE 依 BALANCE.md
	// 模擬報告從草案 6 調到 56（＋833%）：草案是用「玩家單打獨鬥」估算，但實際戰鬥是玩家＋4 位
	// 隊友同時對同一目標出手，整隊 DPS 遠高於玩家單人——用單人尺度校準時，訓練場 3 隻雜魚 5~8
	// 秒就團滅（目標 30~50 秒，差 6~8 倍），必須用「整隊」尺度重新校準才成立（見 BALANCE.md §0-1）。
	BattleMobHits float64 `json:"battle_mob_hits"`
	// BattleMobDefRatio 怪物 DEF ÷ 玩家 ATK 的比例（D2 mobDef 公式）。BALANCE 模擬顯示未蓄氣一拳
	// 傷害／玩家 ATK 落在 82~87% 之間，沒有出現防禦吃到剩 1 或幾乎無視防禦的極端，沿用草案值。
	BattleMobDefRatio float64 `json:"battle_mob_def_ratio"`
	// BattleEnemyDPSRatio 全場敵人合計 DPS ÷ 玩家 MaxHP（每秒）。TUNE 依 BALANCE.md 模擬報告從
	// 草案 0.010 調到 0.2（20 倍）：草案只以「玩家一人的 HP」抓 DPS，但敵人攻擊目標隨機分配到
	// 全隊 5 人（合計 HPMax 是玩家單人的 5 倍）、且隊友「小咪」的治療技能有 flat=80 高額固定回血，
	// 草案數值下全場幾乎零死亡風險，20 倍才能讓難度 2 以上場次出現契約要求的「不防禦不補血會輸」
	// 的風險（見 BALANCE.md §0-2、§4）。
	BattleEnemyDPSRatio float64 `json:"battle_enemy_dps_ratio"`
	// BattlePlayerMinAtk 玩家 ATK 保底（P2 專用：owner 現有角色多數點數未配，沒有保底完全打不動；
	// P3 接上裝備/技能成長後這個保底會取消，見契約 §1 D1）。
	BattlePlayerMinAtk float64 `json:"battle_player_min_atk"`
	// BattlePlayerMinHP 玩家 MaxHP 保底，理由同上。
	BattlePlayerMinHP float64 `json:"battle_player_min_hp"`
	// BattleAttackCooldownMs/ChargeMinMs/.../CritMultiplier 這一批直接對齊前端
	// engine/types.ts 的 BattleConfig 同名欄位（camelCase↔snake_case 一對一），開放後台調整手感
	// 之後由 battle.go 的 GET /rpg/battle/bootstrap 轉成 Partial<BattleConfig> 送給前端引擎；
	// 前端沒對應欄位的（enemyWindupMs 等純動畫時間常數）不開放，繼續吃引擎預設值。
	BattleAttackCooldownMs    int     `json:"battle_attack_cooldown_ms"`
	BattleChargeMinMs         int     `json:"battle_charge_min_ms"`
	BattleChargeFullMs        int     `json:"battle_charge_full_ms"`
	BattleChargeMaxMultiplier float64 `json:"battle_charge_max_multiplier"`
	BattleGuardMultiplier     float64 `json:"battle_guard_multiplier"`
	BattleRecoveryMs          int     `json:"battle_recovery_ms"`
	BattleDefaultCastMs       int     `json:"battle_default_cast_ms"`
	BattleEscapeJudgeMs       int     `json:"battle_escape_judge_ms"`
	BattleResolveDelayMs      int     `json:"battle_resolve_delay_ms"`
	// BattleEnemyActMinMs/MaxMs 怪物行動間隔隨機範圍（乘上各怪 speed_mult 後才是實際間隔，見
	// scaling.go ScaleMonster），也用於 D2 mobAtk 公式算 actSec。
	BattleEnemyActMinMs int `json:"battle_enemy_act_min_ms"`
	BattleEnemyActMaxMs int `json:"battle_enemy_act_max_ms"`
	// BattleAllyActMinMs/MaxMs 隊友 AI 行動間隔隨機範圍（直接對齊前端 allyActIntervalMs，
	// P2 沒有 D2 公式使用它，只是原樣轉發給引擎）。
	BattleAllyActMinMs   int     `json:"battle_ally_act_min_ms"`
	BattleAllyActMaxMs   int     `json:"battle_ally_act_max_ms"`
	BattleHitRate        float64 `json:"battle_hit_rate"`
	BattleCritRate       float64 `json:"battle_crit_rate"`
	BattleCritMultiplier float64 `json:"battle_crit_multiplier"`
	// BattleExpPreviewPerLevel 結算畫面「預估經驗」＝Σ敵人等級×此值，純顯示用，不入帳、不影響
	// 任何獎勵/EXP 系統（D5：P2 沒有伺服器判定也沒有獎勵發放）。
	BattleExpPreviewPerLevel float64 `json:"battle_exp_preview_per_level"`

	// --- DORPG P2 修正第 1 輪（審查 data.md 缺陷1 根因修復）：參考 HP/MP 縮放 ---
	//
	// rpg_skills.flat（heal/shield）與 rpg_items.amount（hp/mp）在 DB 裡存的是絕對值，語意是
	// 「參考玩家（HPMax=battle_reference_hp、MPMax=battle_reference_mp）身上的絕對回復量」，
	// 不是任何角色都通用的絕對值。怪物 HP/ATK/DEF 全部隨玩家戰力縮放（ScaleMonster），但治療
	// 技能/道具的絕對值原本完全沒有縮放：owner 實測 Base Lv 27、HPMax 919（種子假設是 Lv5/300）
	// 時，同一瓶 150 的紅藥水從補 50% HP 掉到補 16%，小咪的 flat=80 治療從 27% 掉到 8.7%——這讓
	// 「對任何角色都即刻可玩、節奏一致」（契約 D1）在高等級完全失效。ScaleSkill/ScaleItem
	// （scaling.go）用這兩個參考值算出 ratioHP/ratioMP，等比例縮放 flat/amount。
	BattleReferenceHP float64 `json:"battle_reference_hp"` // 技能/道具絕對回復量的參考玩家 HPMax（預設 300）
	BattleReferenceMP float64 `json:"battle_reference_mp"` // 同上，MP 類（預設 100）

	// --- DORPG P2 修正第 2 輪（暴擊／Miss／無效攻擊上線，SPEC §6）：json tag 對齊前端
	// engine/types.ts BattleConfig 新增欄位（camelCase↔snake_case 對照見 battle.go
	// buildWireConfig）；battle_element_chart 的 key 是「怪物 attribute」，DB 現況存的就是
	// 中文字串（金/木/水/火/土/光/闇/無），value 表的 key 是「技能 element」英文代碼
	// （ElementKind），因此刻意混用中英文 key，不是輸入錯誤。 ---

	// BattleBaseMissPct/HitFleeScale/MissMinPct/MissMaxPct：missChance() 的基礎落空率、
	// (defender.flee−attacker.hit) 差額換算成落空率的係數、clamp 上下限。三個 *Pct 欄位是
	// missPct 這個「輸出值」本身（0~100 的機率百分比），HitFleeScale 是換算係數（不是機率）。
	BattleBaseMissPct  float64 `json:"battle_base_miss_pct"`
	BattleHitFleeScale float64 `json:"battle_hit_flee_scale"`
	BattleMissMinPct   float64 `json:"battle_miss_min_pct"`
	BattleMissMaxPct   float64 `json:"battle_miss_max_pct"`
	// BattleMonsterHitBase/FleeBase：怪物命中/迴避＝「跟著玩家等級基線走的一次函數」的截距
	// （見下面 BattleMonsterHitPerLevel/FleePerLevel），不再是絕對常數（P2 修正第 3 輪，審查
	// data.md 缺陷1/2 CONFIRMED 根因修復）：
	//
	//	monsterHit  = playerBaseLevel × battle_monster_hit_per_level  + battle_monster_hit_base
	//	monsterFlee = (playerBaseLevel × battle_monster_flee_per_level + battle_monster_flee_base) × speed_mult
	//
	// ⚠️ 語意改變：這兩個欄位的 json tag 沒變、但意義從「絕對基準值」改成「相對等級基線的
	// 偏移量」——舊語意下 HitBase=100/FleeBase=8 是固定常數，玩家 hit/flee 隨等級線性成長、
	// 約 Lv9 起就穩定超過怪物 flee（DEX 配點在中後期形同虛設）、而玩家 flee 被 flee_cap_pct=95
	// 封頂、(95−100)×hit_flee_scale 恆為負值（AGI 配到滿也不可能提高迴避率）。新語意下兩邊
	// 等級成長同步（維持既有 D1「怪物數值依玩家縮放、等級無關」架構），預設值改為 2：完全不配
	// AGI/DEX 的角色（Hit=Flee=baseLv×1+2）與怪物打平、missChance 落在下限，每配一點 AGI/DEX
	// 才會真的把差距拉開（效果驗算見本輪 SPEC：Lv27 AGI 2→missChance 2%下限、AGI
	// 40→約13%、AGI滿(flee封頂95)→約23%）。同樣不做 0~100 範圍檢查，只擋負值（理由同舊註解：
	// 這是「評級數值空間」不是機率本身）。FleeBase 一項另乘 monster.speed_mult、由 scaling.go
	// MonsterRating 推導。
	BattleMonsterHitBase  float64 `json:"battle_monster_hit_base"`
	BattleMonsterFleeBase float64 `json:"battle_monster_flee_base"`
	// BattleMonsterHitPerLevel/FleePerLevel：上述公式裡「每級」的係數，預設 1.0 對齊
	// compute.go 玩家端 LvHit/LvFlee 預設值（都是 1），確保兩邊等級成長曲線斜率一致——不然即使
	// 修好截距，斜率不同還是會讓中後期再度失衡。只擋負值（0 是合法的「怪物命中/迴避跟等級完全
	// 無關，退化回舊的絕對常數模式」設計選擇）。
	BattleMonsterHitPerLevel float64 `json:"battle_monster_hit_per_level"`
	// 怪物命中的絕對上限（2026-09-14 對抗式審查 CONFIRMED 修正）：玩家的 flee 被 flee_cap_pct（預設 95）
	// 硬性封頂，但上面那條「隨玩家 Base Lv 線性成長」的怪物命中不封頂——Base Lv 93 之後怪物命中就會
	// 超過玩家可能達到的最高 flee，AGI 配再多迴避率都會掉回下限，等於「AGI 無效」這個缺陷在高等級重演。
	// 夾在這個上限（預設 75，刻意留 20 點空間給 flee_cap_pct=95）之下，任何等級都保證還有可投資空間。
	BattleMonsterHitMax       float64 `json:"battle_monster_hit_max"`
	BattleMonsterFleePerLevel float64 `json:"battle_monster_flee_per_level"`
	// BattleMonsterCritPct/CritShieldBase：怪物沒有個別 rating 覆寫時的暴擊率/暴擊迴避基準，
	// 這兩個語意對齊玩家 Derived.CritPct/CritShield，同樣是「%」尺度但比照既有 Derived 欄位
	// 的既有慣例（compute.go 從不封頂 CritPct/CritShield），所以只擋負值不擋上限——後台若想
	// 讓某隻怪物暴擊率設 150% 造成「必爆擊」也是合理的極端設計，不視為系統壞掉。
	// CritShieldBase 另乘 monster.def_mult、由 scaling.go MonsterRating 推導。
	BattleMonsterCritPct        float64 `json:"battle_monster_crit_pct"`
	BattleMonsterCritShieldBase float64 `json:"battle_monster_crit_shield_base"`
	// BattleElementChart 屬性相剋表：外層 key＝怪物 attribute（中文），內層 key＝技能/攻擊
	// element（英文 ElementKind 字面值）；查無 key 或查無 element 一律視為 1.0（不相剋也不
	// 吃虧），0.0＝完全無效（engine elementMultiplier() 用來判 'immune'）。
	//
	// ⚠️ 這是全 Config 唯一的 map 欄位：ParseConfig 對它有特別處理（見該函式註解），
	// 因為 encoding/json 對「已存在的非 nil map」是逐鍵合併、不是整體覆蓋——若不特別處理，
	// 管理者在後台刪掉某個屬性/element key 存檔後，DefaultConfig() 的舊值會在下次讀取時復活
	// （P2 修正第 3 輪，審查 data.md 缺陷3 CONFIRMED）。
	BattleElementChart map[string]map[string]float64 `json:"battle_element_chart"`

	// --- DORPG P2 修正第 3 輪：AGI→攻速→攻擊冷卻、DEX→詠唱縮減→技能施放時間（使用者當面
	// 要求；同時緩解缺陷2：DEX 除了命中，戰鬥裡多一個一路有感的用途）。只套用在玩家身上——
	// 隊友沿用 config 固定值（battle_attack_cooldown_ms／castMs 原樣），避免隊友 AI 被連帶
	// 加速影響既有平衡；怪物本來就沒有攻速/詠唱概念。公式見 scaling.go
	// PlayerBattleStatsFrom／battle.go 呼叫端（實際套用點在前端引擎，這裡只負責把
	// aspd/castReductionPct 透過 CombatRating 帶到前端）。 ---

	// BattleAspdReference 攻速換算的基準點（RO「攻擊間隔與 200−ASPD 成正比」公式裡的參考
	// ASPD），預設 150＝AspdBase 預設值，讓完全不配 AGI/DEX 的角色維持現有 1500ms
	// 攻擊冷卻、不動既有平衡，配點越多才越快。必須嚴格落在 (0,200)：Compute() 的 AspdCap
	// 上限是 193，200 是這個 RO 公式的天花板（(200−aspd) 若 <=0 或 aspd>=200 會讓换算出的
	// 冷卻時間變成 0 或負值，除以 (200−reference) 為 0 則直接除零）。
	BattleAspdReference float64 `json:"battle_aspd_reference"`
	// BattleAttackCooldownMinMs 玩家攻擊冷卻換算後的下限（AGI 配滿也不會快過這個值），
	// 預設 700ms。必須 >0 且 <= battle_attack_cooldown_ms（下限不能比未加速的基準值還慢，
	// 否則「配 AGI 變快」這個效果會反過來被下限吃掉，變成配了也沒用甚至配了更慢的錯亂）。
	BattleAttackCooldownMinMs int `json:"battle_attack_cooldown_min_ms"`
	// BattleCastMinMs 玩家技能施放時間（套用 DEX 詠唱縮減後）的下限，預設 120ms。必須 >0 且
	// <= battle_default_cast_ms（詠唱縮到底也不該比「預設沒詠唱時間的技能」瞬發得更誇張，
	// 且避免下限設得比基準還高導致縮短完全沒有下界意義）。
	BattleCastMinMs int `json:"battle_cast_min_ms"`

	// --- DORPG P5（CONTRACT §2/§3/§4/§6）：職業／測試等級／配點規則／技能等級／戰鬥公式微調。
	// json tag 沿用本檔一貫命名規則，FRONTEND 需同步把這批欄位加進 apps/web/src/lib/rpgMeta.ts
	// CONFIG_GROUPS 才會出現在 /admin/rpg 設定頁（本檔只負責後端這一半）。---

	// StatPointsInitial/PerLevelBase/StepLevels：RO 給點公式 TotalStatPoints(L) = initial +
	// Σ_{k=2..L}(floor((k-1)/step)+base)，見 compute.go TotalStatPoints()。預設 48/3/5 對照
	// RO pre-renewal statpoint.yml（Lv1=48、Lv27=186、Lv99=1273，compute_test.go 逐級核對）。
	StatPointsInitial      int `json:"stat_points_initial"`
	StatPointsPerLevelBase int `json:"stat_points_per_level_base"`
	StatPointsStepLevels   int `json:"stat_points_step_levels"`

	// SkillPointsInitial/PerLevel：TotalSkillPoints(L) = initial + max(0,L-1)×per_level，
	// 預設 0/1（Lv27 → 26 點）。
	SkillPointsInitial  int `json:"skill_points_initial"`
	SkillPointsPerLevel int `json:"skill_points_per_level"`

	// StrTierCoef/IntTierCoef：STR/INT 整十階梯係數（CONTRACT §6）：
	// atk += floor(STR/10)²×str_tier_coef、matk += floor(INT/10)²×int_tier_coef。全職業統一
	// （弓箭手也吃 STR 階梯，契約明講「已知簡化」）。
	StrTierCoef float64 `json:"str_tier_coef"`
	IntTierCoef float64 `json:"int_tier_coef"`

	// LvDefBreakpoint/PerLow/PerHigh：等級→防禦新曲線（CONTRACT §6），取代舊版 floor(L/2)：
	// lvDef(L) = L<=breakpoint ? L×per_low : breakpoint×per_low + (L-breakpoint)×per_high，取
	// floor。lv_def_per 舊欄位保留讀取相容（後台舊資料不會因為多一個欄位就整包解析失敗），
	// 但 Compute() 不再使用它——新曲線是唯一生效的路徑。
	LvDefBreakpoint int     `json:"lv_def_breakpoint"`
	LvDefPerLow     float64 `json:"lv_def_per_low"`
	LvDefPerHigh    float64 `json:"lv_def_per_high"`

	// CritMultMin/Max：暴擊倍率改為每次暴擊在 [min,max] 均勻抽（取代固定 battle_crit_multiplier，
	// 後者保留欄位但前端不再使用，見 battle.go buildWireConfig 註解）。預設 1.75/2.25。
	CritMultMin float64 `json:"crit_mult_min"`
	CritMultMax float64 `json:"crit_mult_max"`

	// BattleWeaknessBonusPct：技能 element 命中怪物 weak_elements 時的傷害加成百分比，預設 25
	// （即 ×1.25）。見 scaling.go 檔頭與 battle.go buildWireConfig。
	BattleWeaknessBonusPct float64 `json:"battle_weakness_bonus_pct"`

	// --- DORPG P6（CONTRACT §2）：怪物等級制（battle_scale_mode="level"，本輪起為預設）。
	// 怪物數值＝參考玩家 RefPlayerStats(cfg,N)（見 reflevel.go）的衍生值，乘上怪物自己的
	// *_mult、乘上這四個「等級模式」專用比例、再乘上 encounter/slot 的 power_scale。matk 刻意
	// 不乘這四個比例之一（見 ScaleMonsterByLevel 註解，契約 §2 明講 matk 只乘 atk_mult，不疊加
	// battle_lvl_atk_ratio/power_scale）。
	//
	// SIM 校準（2026-09-18，真引擎模擬：Lv27 輕騎士玩家＋小咪／小咪+阿深，六場 monster_level
	// 10..60 各 ≥50 種子，見 scratchpad/dorpg_p6/sim/RESULT.md）：1.0/1.0/1.0/1.0 這組「完全信任
	// RefPlayerStats 曲線本身」的預設值雖然勝率曲線技術上單調，但存在兩個問題——① Lv10–40 四場
	// 全部 100% 勝率、直到 Lv50 才斷崖式崩落（單一 companion 隊伍 100%→0–2%），難度中段幾乎沒有
	// 感覺；② Lv60（首領戰）主要是 timeout（雙方都打不死對方）而不是真的 defeat——怪物總血量
	// （尤其首領 hp_mult=7.0 疊乘）遠超過玩家＋隊友在 300 秒內能打穿的量，變成「耗到超時」而非
	// 乾脆的敗北，體驗上比爽快輸掉更差。調整：HP 比例下修到 0.8（同比例壓低怪物總血量，讓高等級
	// 戰鬥能在時限內真正分出勝負）、ATK 比例上修到 1.25（提高怪物威脅，把「單一 companion 隊伍
	// 在 Lv50 幾乎必勝」往下拉到接近五五波，Lv60 從 timeout 轉成乾脆的 defeat）；DEF/MDEF 維持
	// 1.0（不需要動：per-hit 傷害量本身沒有離譜到打不動或一擊必殺，問題出在血量總量與時限的關係，
	// 動 DEF 反而會連帶影響「有沒有打得動」這個更基本的手感）。只調了兩個比例、沒有動怪物 *_mult
	// 或任何 encounter.power_scale，符合「最小調整」原則；重跑後六場曲線見 RESULT.md，仍然單調且
	// Lv10 輕鬆全勝／Lv60 兩組隊伍皆幾乎必敗（且是真的 defeat，不再是 timeout）。
	BattleLvHPRatio   float64 `json:"battle_lvl_hp_ratio"`
	BattleLvAtkRatio  float64 `json:"battle_lvl_atk_ratio"`
	BattleLvDefRatio  float64 `json:"battle_lvl_def_ratio"`
	BattleLvMdefRatio float64 `json:"battle_lvl_mdef_ratio"`

	// --- DORPG P7（CONTRACT §1）：屬性相剋（五行＋光暗＋同屬性）的三個百分比係數，跟既有
	// BattleWeaknessBonusPct（P5，weak_elements 專用加成）並存——elements.go ElementMultiplier
	// 對「屬性關係」（剋/被剋/同屬性）用這三個，對「怪物弱點清單」仍用 BattleWeaknessBonusPct，
	// 兩者互相獨立，取兩者較大者生效（見該函式註解）。範圍寬鬆到 −100..300：−100 是「完全免疫」
	// 的下限（傷害歸零，不允許更負，負傷害沒有意義），300 給管理者足夠空間設計「四倍傷害」等
	// 誇張的相剋效果，不強制對稱到 owner 目前拍板的 ±25。
	BattleElementAdvantagePct    float64 `json:"battle_element_advantage_pct"`
	BattleElementDisadvantagePct float64 `json:"battle_element_disadvantage_pct"`
	BattleElementSamePct         float64 `json:"battle_element_same_pct"`

	// TestLevelEnabled 審查#2【中・CONFIRMED】新增：測試等級功能的獨立總開關。CONTRACT §2 的
	// test_level 原本只靠 requireEntry 白名單擋（見 handler.go）——但白名單本來就是拿來放寬給
	// 更多人測試用的，一旦放寬，任何在白名單內的人都能把自己的等級設成 99，沒有第二道閘門。
	// 正式上線前把這個開關關掉（PutTestLevel 開頭檢查，見 jobs.go），既有 test_level 也會被
	// effectiveTestLevel()／loadEffectiveLevel() 忽略（見 compute.go 該函式註解），不是只擋新的
	// PUT 請求。預設 true（開發/測試階段維持現行行為，不影響本輪其餘測試），正式上線前手動關閉。
	TestLevelEnabled bool `json:"test_level_enabled"`
}

// DefaultConfig 站長截圖對照表原封不動編碼成的預設值（所有數字皆可由後台覆寫）。
func DefaultConfig() Config {
	return Config{
		InitialStat:       1,
		InitialFreePoints: 40,
		MaxStat:           99,
		CostBase:          2,
		CostStepEvery:     10,
		DefaultWeaponType: "melee",

		BaseHP:         100,
		HPPerBaseLevel: 30,
		BaseMP:         50,
		MPPerBaseLevel: 5,

		AspdBase:     150,
		AspdPerAgi:   0.25,
		AspdPerDex:   0.1,
		AspdCap:      193,
		WeightBase:   2000,
		WeightPerStr: 30,

		StrMeleeAtk:     1,
		StrEquipAtkPct:  0.5,
		StrRangedAtkPer: 5,

		AgiFlee:   1,
		AgiDefPer: 5,

		VitHPPct:        1,
		VitItemHPPct:    2,
		VitDefPer:       2,
		VitMdefPer:      5,
		VitHPRegenPer:   5,
		HPRegenPerMaxHP: 200,

		DexRangedAtk:   1,
		DexEquipAtkPct: 0.5,
		DexHit:         1,
		DexMeleeAtkPer: 5,
		DexMatkPer:     5,
		DexMdefPer:     5,

		IntMatk:               1.5,
		IntMdef:               1,
		IntMPPct:              1,
		IntItemMPPct:          1,
		IntMPRegenPer:         6,
		IntMPRegenAt120:       4,
		IntMPRegenPerAfter120: 2,
		MPRegenPerMaxMP:       100,

		LukCrit:            0.3,
		LukAtkPer:          3,
		LukMatkPer:         3,
		LukHitPer:          3,
		LukFleePer:         5,
		LukCritShieldPer:   5,
		LukPerfectDodgePer: 10,

		LvHit:     1,
		LvFlee:    1,
		LvDefPer:  2,
		LvAtkPer:  4,
		LvMatkPer: 4,
		LvMdefPer: 4,

		CastUnitDex:    1,
		CastUnitInt:    0.5,
		CastPctPerUnit: 0.5,
		CastCapPct:     50,

		ResistPctPerPoint: 0.1,
		FleeCapPct:        95,

		// DORPG P6（CONTRACT §2）：預設模式由 "power" 改為 "level"——怪物數值改依
		// rpg_encounters.monster_level 對照參考玩家算絕對值，"power" 路徑完整保留、可整場切回。
		BattleScaleMode:     "level",
		BattleMobHits:       56,
		BattleMobDefRatio:   0.25,
		BattleEnemyDPSRatio: 0.2,
		BattlePlayerMinAtk:  30,
		BattlePlayerMinHP:   300,

		BattleAttackCooldownMs:    1500,
		BattleChargeMinMs:         300,
		BattleChargeFullMs:        1200,
		BattleChargeMaxMultiplier: 2.5,
		BattleGuardMultiplier:     0.40,
		BattleRecoveryMs:          400,
		BattleDefaultCastMs:       500,
		BattleEscapeJudgeMs:       1200,
		BattleResolveDelayMs:      800,
		BattleEnemyActMinMs:       2500,
		BattleEnemyActMaxMs:       4500,
		BattleAllyActMinMs:        2200,
		BattleAllyActMaxMs:        3600,
		BattleHitRate:             1.0,
		// BattleCritRate 語意由「只有玩家吃得到的暴擊機率」改為「全體基礎暴擊率」（SPEC §6）：
		// 0→0.08，讓 LUK 沒特別配點的角色也看得到暴擊發生，實際數值交給 BALANCE 之後調整。
		BattleCritRate:           0.08,
		BattleCritMultiplier:     2.0,
		BattleExpPreviewPerLevel: 3.0,

		BattleReferenceHP: 300,
		BattleReferenceMP: 100,

		// BattleBaseMissPct 由 SPEC §6 預設值 8 改為 0（TUNE 套用 BALANCE 建議，見
		// scratchpad/dorpg_p2/REBALANCE_CRIT.md §3）：8% 的固定基礎值對低等級玩家（hit 值小、
		// 未被 missMinPct 下限保護）殺傷力遠大於高等級玩家（hit 早被下限吃掉、這個參數對他們
		// 形同虛設），會讓 Lv5 在多個難度2+場次的勝率腰斬到目標區間之下；調到 0 後 Lv5 全部回到
		// 既有基準，Lv27/Lv50 完全不受影響（他們的自我 miss 率本來就已經被下限夾住）。
		BattleBaseMissPct:  0,
		BattleHitFleeScale: 0.35,
		BattleMissMinPct:   2,
		BattleMissMaxPct:   35,

		// BattleMonsterHitBase/FleeBase 由絕對常數 100/8 改為等級基線的偏移量 2（P2 修正第 3
		// 輪，見欄位註解）；HitPerLevel/FleePerLevel 預設 1.0 對齊玩家 LvHit/LvFlee=1。
		BattleMonsterHitBase:        2,
		BattleMonsterFleeBase:       2,
		BattleMonsterHitPerLevel:    1.0,
		BattleMonsterHitMax:         75,
		BattleMonsterFleePerLevel:   1.0,
		BattleMonsterCritPct:        0,
		BattleMonsterCritShieldBase: 0,

		// DORPG P5（CONTRACT §6）：預設表清空，屬性相剋改由 rpg_monsters.weak_elements 驅動
		// （技能 element ∈ 怪物 weak_elements → ×(1+battle_weakness_bonus_pct/100)）。這張表
		// 保留給管理者「覆寫」用（有對應 key 才用，可做 0 倍免疫），不再預先寫死任何一組——P2
		// 那組草案值（金/木/土/闇/無）已由 weak_elements 機制取代，繼續放著會讓兩套相剋規則
		// 同時生效、互相打架。ParseConfig 的 probe-and-reset 邏輯不受影響（見該函式註解）。
		BattleElementChart: map[string]map[string]float64{},

		// P2 修正第 3 輪：AGI→攻速→攻擊冷卻、DEX→詠唱縮減。150＝AspdBase 預設值，讓不配
		// AGI/DEX 的角色維持現有 1500ms 攻擊冷卻（既有平衡不動）。
		BattleAspdReference:       150,
		BattleAttackCooldownMinMs: 700,
		BattleCastMinMs:           120,

		// DORPG P5（CONTRACT §0/§2/§3/§4/§6 拍板值）：
		StatPointsInitial:      48,
		StatPointsPerLevelBase: 3,
		StatPointsStepLevels:   5,

		SkillPointsInitial:  0,
		SkillPointsPerLevel: 1,

		StrTierCoef: 1.0,
		IntTierCoef: 1.0,

		LvDefBreakpoint: 50,
		LvDefPerLow:     0.5,
		LvDefPerHigh:    0.35,

		CritMultMin: 1.75,
		CritMultMax: 2.25,

		BattleWeaknessBonusPct: 25,

		// DORPG P7（CONTRACT §1 拍板值）：五行/光暗剋制 +25%、被剋 −25%、同屬性 −25%。
		BattleElementAdvantagePct:    25,
		BattleElementDisadvantagePct: -25,
		BattleElementSamePct:         -25,

		// DORPG P6：level 模式四個比例。2026-09-19 使用者決策改版：怪物基礎能力（HP/ATK/DEF/MDEF）
		// 至少是同級參考玩家的 3–4 倍，理由是怪物沒有技能也沒有裝備，理論上該比玩家單體強得多。
		// 真引擎滿隊模擬（玩家＋4 傭兵同級，Lv10–60 三職業，50 種子，17 組設定；報告整理於
		// docs/dorpg/MONSTER_X3_CALIBRATION.md）發現 HP/ATK/DEF/MDEF 不能等比例
		// 一起放大到 3 倍：ATK×3 讓玩家每次被打≈2% HPMax（終於有痛感）、HP×3 讓戰鬥時長拉長到約
		// 三倍（換取「怪物真的變壯」的體感），但 DEF/MDEF×3 在 `max(1, atk-def)` 線性減防公式下
		// 會讓玩家幾乎每次普攻都被夾成傷害 1、殺不死怪（滿隊模擬 fp_u3：18 格裡 17 格勝率 0%）——
		// DEF/MDEF 只決定「玩家砍穿怪物要多久」這個時間軸維度，跟「怪物多能打」正交，所以刻意反向
		// 調低（1.5→1.0→0.4 單調改善，實測直接證實方向與幅度），最終選最低點 0.4，讓怪物防禦力
		// 提升有感、但不會讓戰鬥卡死。四個比例現在改用同一個公式性質（× RefPlayer(N)）但方向不同，
		// 不是同一個「整體放大 3–4 倍」的字面意思均分到四個欄位。
		//
		// 殘留待辦（此輪 monster-side 調參範圍之外，留給後續處理）：
		//   1. Lv60 台北101 首領（DOR-MON-A-67000200001）自身 hpMult=7.0 與這次全域下限相乘＝有效
		//      21 倍血量，導致 Lv60 三職業勝率恆為 0%，需要另外調整該首領自身倍率或重新設計編組。
		//   2. 法師職業在 Lv40 起近乎必敗（非 monster MDEF 過高——已用 mdef=0.15 的非對稱測試排除
		//      這個假設），是既有職業強度曲線問題，需要跟角色技能/成長曲線一起校準。
		//   3. 模擬用的玩家技能點固定配置在 Lv25 那組配點，高等級（Lv50/60）數據可能偏悲觀。
		// 詳細判準、17 組設定總表與完整驗證數據見 docs/dorpg/MONSTER_X3_CALIBRATION.md。
		BattleLvHPRatio:   3.0,
		BattleLvAtkRatio:  3.0,
		BattleLvDefRatio:  0.4,
		BattleLvMdefRatio: 0.4,

		// 審查#2：預設開啟（維持現行「測試階段人人可設」行為），正式上線前由後台手動關閉。
		TestLevelEnabled: true,
	}
}

// requirePositive 收集「必須 > 0」的除數/上限欄位檢查（這些欄位當分母用，0 或負值會讓 Compute
// 除零/算出負值可利用漏洞）。
func requirePositive(errs *[]string, name string, v float64) {
	if v <= 0 {
		*errs = append(*errs, name)
	}
}

// requireNonNegative 收集「必須 >= 0」的欄位檢查（0 是合法的「暫時關閉這個加成」設計選擇，
// 但負值會讓 scaling.go 算出方向相反的評級——例如 hit_flee_scale 若允許負值，迴避越高反而
// 命中率越高，這是邏輯反轉的漏洞而非合理的極端設計）。
func requireNonNegative(errs *[]string, name string, v float64) {
	if v < 0 {
		*errs = append(*errs, name)
	}
}

// Validate 檢查參數是否落在合理範圍（後台 PUT /admin/rpg/config 前置檢查）。刻意只擋「會讓
// 系統壞掉/算出負值可利用漏洞」的邊界，不擋設計上合理的極端值（例如某係數想暫時設 0 停用）。
func (c Config) Validate() error {
	if c.InitialStat < 0 {
		return fmt.Errorf("initial_stat must be >= 0")
	}
	if c.InitialFreePoints < 0 {
		return fmt.Errorf("initial_free_points must be >= 0")
	}
	if c.MaxStat <= c.InitialStat {
		return fmt.Errorf("max_stat must be > initial_stat")
	}
	if c.CostBase <= 0 {
		return fmt.Errorf("cost_base must be > 0")
	}
	if c.CostStepEvery <= 0 {
		return fmt.Errorf("cost_step_every must be > 0")
	}
	if c.DefaultWeaponType != "melee" && c.DefaultWeaponType != "ranged" {
		return fmt.Errorf("default_weapon_type must be melee or ranged")
	}
	if c.BaseHP < 0 || c.BaseMP < 0 || c.HPPerBaseLevel < 0 || c.MPPerBaseLevel < 0 {
		return fmt.Errorf("base_hp/base_mp/hp_per_base_level/mp_per_base_level must be >= 0")
	}
	if c.AspdCap <= 0 || c.AspdCap > 1000 {
		return fmt.Errorf("aspd_cap must be within (0,1000]")
	}
	if c.WeightBase < 0 {
		return fmt.Errorf("weight_base must be >= 0")
	}
	if c.FleeCapPct < 0 || c.FleeCapPct > 100 {
		return fmt.Errorf("flee_cap_pct must be within [0,100]")
	}
	if c.CastCapPct < 0 || c.CastCapPct > 100 {
		return fmt.Errorf("cast_cap_pct must be within [0,100]")
	}
	var bad []string
	requirePositive(&bad, "str_ranged_atk_per", c.StrRangedAtkPer)
	requirePositive(&bad, "agi_def_per", c.AgiDefPer)
	requirePositive(&bad, "vit_def_per", c.VitDefPer)
	requirePositive(&bad, "vit_mdef_per", c.VitMdefPer)
	requirePositive(&bad, "vit_hp_regen_per", c.VitHPRegenPer)
	requirePositive(&bad, "hp_regen_per_max_hp", c.HPRegenPerMaxHP)
	requirePositive(&bad, "dex_melee_atk_per", c.DexMeleeAtkPer)
	requirePositive(&bad, "dex_matk_per", c.DexMatkPer)
	requirePositive(&bad, "dex_mdef_per", c.DexMdefPer)
	requirePositive(&bad, "int_mp_regen_per", c.IntMPRegenPer)
	requirePositive(&bad, "int_mp_regen_per_after_120", c.IntMPRegenPerAfter120)
	requirePositive(&bad, "mp_regen_per_max_mp", c.MPRegenPerMaxMP)
	requirePositive(&bad, "luk_atk_per", c.LukAtkPer)
	requirePositive(&bad, "luk_matk_per", c.LukMatkPer)
	requirePositive(&bad, "luk_hit_per", c.LukHitPer)
	requirePositive(&bad, "luk_flee_per", c.LukFleePer)
	requirePositive(&bad, "luk_crit_shield_per", c.LukCritShieldPer)
	requirePositive(&bad, "luk_perfect_dodge_per", c.LukPerfectDodgePer)
	requirePositive(&bad, "lv_def_per", c.LvDefPer)
	requirePositive(&bad, "lv_atk_per", c.LvAtkPer)
	requirePositive(&bad, "lv_matk_per", c.LvMatkPer)
	requirePositive(&bad, "lv_mdef_per", c.LvMdefPer)
	if len(bad) > 0 {
		return fmt.Errorf("these fields must be > 0 (used as divisors): %v", bad)
	}

	// --- DORPG P2 戰鬥參數檢查：只擋會讓 scaling.go 算出負值/除零/當機等級的邊界，
	// 不擋設計上合理的極端值（例如 crit_rate 想暫時設 0）。---
	if c.BattleScaleMode != "power" && c.BattleScaleMode != "level" && c.BattleScaleMode != "fixed" {
		return fmt.Errorf("battle_scale_mode must be power, level or fixed")
	}
	requirePositive(&bad, "battle_mob_hits", c.BattleMobHits)
	requirePositive(&bad, "battle_mob_def_ratio", c.BattleMobDefRatio)
	requirePositive(&bad, "battle_enemy_dps_ratio", c.BattleEnemyDPSRatio)
	requirePositive(&bad, "battle_player_min_atk", c.BattlePlayerMinAtk)
	requirePositive(&bad, "battle_player_min_hp", c.BattlePlayerMinHP)
	requirePositive(&bad, "battle_attack_cooldown_ms", float64(c.BattleAttackCooldownMs))
	requirePositive(&bad, "battle_charge_full_ms", float64(c.BattleChargeFullMs))
	requirePositive(&bad, "battle_charge_max_multiplier", c.BattleChargeMaxMultiplier)
	requirePositive(&bad, "battle_recovery_ms", float64(c.BattleRecoveryMs))
	requirePositive(&bad, "battle_default_cast_ms", float64(c.BattleDefaultCastMs))
	requirePositive(&bad, "battle_escape_judge_ms", float64(c.BattleEscapeJudgeMs))
	requirePositive(&bad, "battle_resolve_delay_ms", float64(c.BattleResolveDelayMs))
	requirePositive(&bad, "battle_enemy_act_min_ms", float64(c.BattleEnemyActMinMs))
	requirePositive(&bad, "battle_enemy_act_max_ms", float64(c.BattleEnemyActMaxMs))
	requirePositive(&bad, "battle_ally_act_min_ms", float64(c.BattleAllyActMinMs))
	requirePositive(&bad, "battle_ally_act_max_ms", float64(c.BattleAllyActMaxMs))
	if c.BattleEnemyActMaxMs < c.BattleEnemyActMinMs {
		return fmt.Errorf("battle_enemy_act_max_ms must be >= battle_enemy_act_min_ms")
	}
	if c.BattleAllyActMaxMs < c.BattleAllyActMinMs {
		return fmt.Errorf("battle_ally_act_max_ms must be >= battle_ally_act_min_ms")
	}
	if c.BattleChargeMinMs < 0 || c.BattleChargeMinMs > c.BattleChargeFullMs {
		return fmt.Errorf("battle_charge_min_ms must be within [0, battle_charge_full_ms]")
	}
	if c.BattleGuardMultiplier < 0 || c.BattleGuardMultiplier > 1 {
		return fmt.Errorf("battle_guard_multiplier must be within [0,1]")
	}
	if c.BattleHitRate < 0 || c.BattleHitRate > 1 {
		return fmt.Errorf("battle_hit_rate must be within [0,1]")
	}
	if c.BattleCritRate < 0 || c.BattleCritRate > 1 {
		return fmt.Errorf("battle_crit_rate must be within [0,1]")
	}
	// ⚠️ 已知限制（審查 data.md，本輪刻意不修）：BattleCritMultiplier 無上限，且這裡不擋
	// 「def_mult × power_scale 過高、疊加 mob_def_ratio 導致 mobDef 逼近 playerAtk」這種讓
	// hitDamage 被 max(1,...) 保底吃住、實質造成非預期 'immune'（傷害恆為地板值 1）的組合——
	// 那是 ScaleMonster 公式與怪物資料的交互作用，不是這個欄位本身的邊界問題，留給 P3
	// 連同怪物資料驗證一起處理。
	if c.BattleCritMultiplier < 0 {
		return fmt.Errorf("battle_crit_multiplier must be >= 0")
	}
	if c.BattleExpPreviewPerLevel < 0 {
		return fmt.Errorf("battle_exp_preview_per_level must be >= 0")
	}
	requirePositive(&bad, "battle_reference_hp", c.BattleReferenceHP)
	requirePositive(&bad, "battle_reference_mp", c.BattleReferenceMP)
	if len(bad) > 0 {
		return fmt.Errorf("these fields must be > 0 (used as divisors): %v", bad)
	}

	// --- DORPG P2 修正第 2 輪（暴擊／Miss／無效攻擊）檢查：百分比欄位夾在 [0,100]、
	// min<=max、相剋表倍率 >=0；其餘（hit_flee_scale、monster_hit_base/flee_base/
	// crit_pct/crit_shield_base）語意對齊玩家 Compute() 算出的 Hit/Flee/CritPct/CritShield
	// ——那些既有 Derived 欄位本來就不封頂在 100（見 compute.go），所以這裡只擋負值，不擋
	// 上限（本檔開頭的一貫原則：只擋系統壞掉的邊界，不擋設計上合理的極端值）。 ---
	if c.BattleBaseMissPct < 0 || c.BattleBaseMissPct > 100 {
		return fmt.Errorf("battle_base_miss_pct must be within [0,100]")
	}
	if c.BattleMissMinPct < 0 || c.BattleMissMinPct > 100 {
		return fmt.Errorf("battle_miss_min_pct must be within [0,100]")
	}
	if c.BattleMissMaxPct < 0 || c.BattleMissMaxPct > 100 {
		return fmt.Errorf("battle_miss_max_pct must be within [0,100]")
	}
	if c.BattleMissMaxPct < c.BattleMissMinPct {
		return fmt.Errorf("battle_miss_max_pct must be >= battle_miss_min_pct")
	}
	var bad2 []string
	requireNonNegative(&bad2, "battle_hit_flee_scale", c.BattleHitFleeScale)
	requireNonNegative(&bad2, "battle_monster_hit_base", c.BattleMonsterHitBase)
	requireNonNegative(&bad2, "battle_monster_flee_base", c.BattleMonsterFleeBase)
	requireNonNegative(&bad2, "battle_monster_crit_pct", c.BattleMonsterCritPct)
	requireNonNegative(&bad2, "battle_monster_crit_shield_base", c.BattleMonsterCritShieldBase)
	if len(bad2) > 0 {
		return fmt.Errorf("these fields must be >= 0: %v", bad2)
	}
	for attribute, row := range c.BattleElementChart {
		for element, mul := range row {
			if mul < 0 {
				return fmt.Errorf("battle_element_chart[%s][%s] must be >= 0", attribute, element)
			}
		}
	}

	// --- DORPG P2 修正第 3 輪（怪物命中/迴避改跟等級基線走＋AGI 攻速＋DEX 詠唱縮減）檢查：
	// per_level 與 base 只擋負值（0 是合法的「退化回舊常數模式/關閉」設計選擇）；
	// aspd_reference 必須嚴格落在 (0,200)（見欄位註解：200 是 RO 公式天花板，>=200 會讓
	// (200−aspd) 变成 <=0、除以 (200−reference)=0 直接除零）；兩個 min_ms 必須 >0 且不超過
	// 對應的基準值，否則「配點變快/變短」的效果會被下限本身吃掉甚至邏輯反轉。 ---
	var bad3 []string
	requireNonNegative(&bad3, "battle_monster_hit_per_level", c.BattleMonsterHitPerLevel)
	requireNonNegative(&bad3, "battle_monster_flee_per_level", c.BattleMonsterFleePerLevel)
	// battle_monster_hit_max 必須嚴格小於 flee_cap_pct，否則高等級的玩家不管怎麼配 AGI，
	// 迴避率都會被夾在下限（見 MonsterRating 的高等級保護註解）。
	if c.BattleMonsterHitMax <= 0 || c.BattleMonsterHitMax >= c.FleeCapPct {
		bad3 = append(bad3, "battle_monster_hit_max 必須 > 0 且小於 flee_cap_pct")
	}
	if len(bad3) > 0 {
		return fmt.Errorf("these fields must be >= 0: %v", bad3)
	}
	if c.BattleAspdReference <= 0 || c.BattleAspdReference >= 200 {
		return fmt.Errorf("battle_aspd_reference must be within (0,200)")
	}
	if c.BattleAttackCooldownMinMs <= 0 || c.BattleAttackCooldownMinMs > c.BattleAttackCooldownMs {
		return fmt.Errorf("battle_attack_cooldown_min_ms must be within (0, battle_attack_cooldown_ms]")
	}
	if c.BattleCastMinMs <= 0 || c.BattleCastMinMs > c.BattleDefaultCastMs {
		return fmt.Errorf("battle_cast_min_ms must be within (0, battle_default_cast_ms]")
	}

	// --- DORPG P5（CONTRACT §2/§3/§4/§6）新增欄位檢查：只擋會讓 compute.go 除零/算出負值可
	// 利用漏洞的邊界，其餘沿用本檔一貫的寬鬆風格（0 是合法的「暫時關閉這個加成」設計選擇）。---
	if c.StatPointsInitial < 0 {
		return fmt.Errorf("stat_points_initial must be >= 0")
	}
	if c.StatPointsPerLevelBase < 0 {
		return fmt.Errorf("stat_points_per_level_base must be >= 0")
	}
	if c.StatPointsStepLevels <= 0 {
		return fmt.Errorf("stat_points_step_levels must be > 0")
	}
	if c.SkillPointsInitial < 0 {
		return fmt.Errorf("skill_points_initial must be >= 0")
	}
	if c.SkillPointsPerLevel < 0 {
		return fmt.Errorf("skill_points_per_level must be >= 0")
	}
	if c.StrTierCoef < 0 || c.IntTierCoef < 0 {
		return fmt.Errorf("str_tier_coef/int_tier_coef must be >= 0")
	}
	if c.LvDefBreakpoint < 0 {
		return fmt.Errorf("lv_def_breakpoint must be >= 0")
	}
	if c.LvDefPerLow < 0 || c.LvDefPerHigh < 0 {
		return fmt.Errorf("lv_def_per_low/lv_def_per_high must be >= 0")
	}
	if c.CritMultMin <= 0 || c.CritMultMax <= 0 {
		return fmt.Errorf("crit_mult_min/crit_mult_max must be > 0")
	}
	if c.CritMultMax < c.CritMultMin {
		return fmt.Errorf("crit_mult_max must be >= crit_mult_min")
	}
	if c.BattleWeaknessBonusPct < 0 {
		return fmt.Errorf("battle_weakness_bonus_pct must be >= 0")
	}

	// --- DORPG P7（CONTRACT §1）：三個屬性相剋百分比，範圍 −100..300（見欄位註解）。 ---
	if c.BattleElementAdvantagePct < -100 || c.BattleElementAdvantagePct > 300 {
		return fmt.Errorf("battle_element_advantage_pct must be within [-100,300]")
	}
	if c.BattleElementDisadvantagePct < -100 || c.BattleElementDisadvantagePct > 300 {
		return fmt.Errorf("battle_element_disadvantage_pct must be within [-100,300]")
	}
	if c.BattleElementSamePct < -100 || c.BattleElementSamePct > 300 {
		return fmt.Errorf("battle_element_same_pct must be within [-100,300]")
	}

	// --- DORPG P6（CONTRACT §2）：level 模式四個比例必須 > 0——這四個數字是乘數，0 或負值會讓
	// 怪物 HP/ATK/DEF/MDEF 變成 0 或負的（DEF/MDEF 是唯一沒有 max(1,...) 保底的一側，見
	// ScaleMonsterByLevel），不是「暫時關閉某加成」的合理極端值，而是讓整場戰鬥失去意義。 ---
	var bad4 []string
	requirePositive(&bad4, "battle_lvl_hp_ratio", c.BattleLvHPRatio)
	requirePositive(&bad4, "battle_lvl_atk_ratio", c.BattleLvAtkRatio)
	requirePositive(&bad4, "battle_lvl_def_ratio", c.BattleLvDefRatio)
	requirePositive(&bad4, "battle_lvl_mdef_ratio", c.BattleLvMdefRatio)
	if len(bad4) > 0 {
		return fmt.Errorf("these fields must be > 0: %v", bad4)
	}
	return nil
}

// ParseConfig 解析後台 JSON 設定（appsettings key "rpg_config"）；空字串回預設值。未出現在 JSON
// 裡的欄位維持 DefaultConfig() 的值（先套預設值再 Unmarshal 覆蓋，避免後台只想改一兩個係數時
// 其餘欄位被歸零）。
//
// ⚠️ BattleElementChart 特殊處理（P2 修正第 3 輪，審查 data.md 缺陷3 CONFIRMED）：Go 的
// encoding/json 對「已存在的非 nil map」欄位是逐鍵合併、不是整體覆蓋——上面「先套預設值再
// Unmarshal」對純數值/字串欄位是刻意的（未出現的欄位維持預設值），但套在 map 欄位上會有副作用：
// 管理者在後台 PUT 時如果刪掉某個屬性 key（或某屬性底下的某個 element key），Unmarshal 只會
// 新增/覆蓋 JSON 裡出現的 key，DefaultConfig() 裡沒被提到的舊 key 會原封不動留著、等於「刪除」
// 操作在下次讀取時被復活。修法：先探測 raw 頂層是否真的含有 "battle_element_chart" 這個 key——
//   - 沒有這個 key：完全不動 cfg.BattleElementChart，維持既有「未出現的欄位維持預設值」慣例
//     （這正是本函式一貫的設計，不能被這個修復破壞：只想調別的係數的 PUT 不該連帶清空整張表）。
//   - 有這個 key（不論值是完整表、部分表甚至 {}）：把 cfg.BattleElementChart 重設為空 map，
//     讓 Unmarshal 對它的「合併」在解碼當下必定是對著空 map 操作，等同整體覆蓋——刪掉的 key
//     就真的消失，不會被空 map 裡不存在的舊值復活。
func ParseConfig(raw string) (Config, error) {
	cfg := DefaultConfig()
	if raw == "" {
		return cfg, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err == nil {
		if _, hasChart := probe["battle_element_chart"]; hasChart {
			cfg.BattleElementChart = map[string]map[string]float64{}
		}
	}
	// probe 解析失敗（例如 raw 頂層不是 JSON 物件）時刻意不在此處回錯——沿用下面的正式
	// Unmarshal 去回報真正的錯誤訊息，避免同一種輸入錯誤在兩處產生不同措辭的錯誤。
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid rpg_config json: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
