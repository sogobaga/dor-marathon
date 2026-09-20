// battle.go：DORPG P2 會員端戰鬥端點（契約 §3.4）。掛在既有 Handler 底下、套用套件私有
// requireEntry（同 handler.go 的 /rpg/me、/rpg/allocate），main.go 把 BattleRouter() 掛在
// "/rpg/battle"（跟既有 "/rpg" mount 同一個已登入會員群組底下的手足路由，共用同一組
// requireEntry 白名單判斷，不另外重複一份）。
//
// 命名慣例（契約 §3 檔頭）：外層包裝（encounter/config/hints）與內容表欄位一律 snake_case，
// 對齊站上其餘 API；只有直接塞進 BattleSample 的 wire* 型別（前端引擎的資料契約）用 camelCase，
// 因為那些欄位最終要被 apps/web/src/lib/dorpg/types.ts 的型別「直接吃」，不能再轉換一次大小寫。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// --- 素材路徑（比照前端 apps/web/src/lib/dorpg/assets.ts 的 charPortrait()/kitAsset()。
// 後端沒辦法 import 前端的 TS 檔案，這裡重寫同一條路徑規則；兩邊的慣例改了要一起改，
// 不是各自獨立維護的來源） ---

const dorpgAssetBase = "/ui/dorpg"

// charPortraitURL 角色肖像（256 尺寸，跟 sampleBattle.ts 目前唯一使用的尺寸一致）。
func charPortraitURL(id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("%s/char/%s_256.webp", dorpgAssetBase, id)
}

// kitAssetURL 07 UI kit 元件（技能/道具圖示走這個路徑）。
func kitAssetURL(id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("%s/ui/%s.webp", dorpgAssetBase, id)
}

// slotOrder 五個怪物站位的固定排序，對齊前端 engine/formulas.ts 的 SLOT_ORDER——挑選
// initialTargetId 時「同序取槽位順序最小」要跟前端 pickInitialTarget 的 tie-break 一致，
// 不然 sample.initialTargetId 跟前端事後重算的結果對不上（雖然引擎會優先採用 sample 給的值，
// 兩邊不一致只是浪費一次計算，但保持一致比較不會讓人誤會）。
var slotOrder = map[string]int{
	"rear_left": 0, "rear_right": 1, "front_left": 2, "front_center": 3, "front_right": 4,
}

// enemyRowFromSlot DORPG P12（CONTRACT §1「排位＝既有槽位」）：怪物排位是槽位的衍生值，不是
// 資料庫新欄位——rpg_encounter_monsters.slot 五個合法值裡 front_* 三個是前排、rear_* 兩個是
// 後排，slotOrder（上面）已經窮舉全部合法槽位，這裡用同一份分類依據。未知槽位理論上不會發生
// （壞資料的槽位在別處已經被略過，見 BattleBootstrap 迴圈的 `continue`），保守當前排（多數
// 怪物是前排，且前排沒有額外的貫穿受害風險——後排才是貫穿的目標，誤判前排比誤判後排安全）。
func enemyRowFromSlot(slot string) string {
	switch slot {
	case "rear_left", "rear_right":
		return "rear"
	default:
		return "front"
	}
}

// --- wire 型別：直接對齊 apps/web/src/lib/dorpg/types.ts（camelCase）---

type wireScene struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	ImageURL string         `json:"imageUrl"`
	Slots    []SceneSlotRow `json:"slots"`
}

type wireSkill struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	IconURL     string  `json:"iconUrl"`
	CooldownMs  int     `json:"cooldownMs"`
	Kind        string  `json:"kind"`
	Target      string  `json:"target"`
	MPCost      int     `json:"mpCost"`
	Coefficient float64 `json:"coefficient"`
	Flat        int     `json:"flat"`
	Element     string  `json:"element,omitempty"`
	Weapon      string  `json:"weapon"`
	CastMs      int     `json:"castMs,omitempty"`

	// --- P5（WIRE：戰鬥 bootstrap 技能欄新增欄位）---
	Level       int           `json:"level"`
	MaxLevel    int           `json:"maxLevel"`
	DisplayText string        `json:"displayText"`
	DmgType     string        `json:"dmgType"`
	Implemented bool          `json:"implemented"`
	Effect      EffectAtLevel `json:"effect"`
	// Tier DORPG P6：INTEGRATOR 補上（2026-09-18）——ENGINE 向 BACKEND 提的需求「wireSkill 加
	// tier」原本沒人接手，ai.ts pickHighestTierSkill() 只能用陣列位置當代理（同一路線內成立，
	// 跨路線比較會失真）。0 是既有 5 個無職業技能的預設值（omitempty 隱藏，不影響既有前端），
	// 只有 toWireSkillLeveled（職業技能／傭兵腳本技能）會填入真正的 s.Tier。
	Tier int `json:"tier,omitempty"`
	// Hits 審查#1 CONFIRMED 新增：一次施放命中次數的「頂層」鏡射，跟上面 Coefficient/Flat/MPCost
	// 同一批既有欄位——前端 fromApi.ts mapSkill()／engine/combat.ts 讀的正是這批頂層欄位（既有
	// 5 個一般技能的既有慣例），從來不讀 Effect 這個巢狀物件；Effect.Hits 只是給 FRONTEND 顯示
	// 鏡射用。缺這個頂層欄位會讓連段技能（hits>1）在戰鬥裡永遠只打 1 下（見 toWireSkillLeveled
	// 註解）。toWireSkillLegacy（既有技能，沒有等級概念）固定填 1。
	Hits int `json:"hits"`

	// Taunt DORPG P10（CONTRACT §3、WIRE「wire skill 新增 kind:"taunt" 與 taunt:{...}」）：
	// 僅 kind=taunt 才非 nil，見 wireTaunt 註解。
	Taunt *wireTaunt `json:"taunt,omitempty"`
}

// wireTaunt DORPG P10（WIRE）：kind=taunt 技能展開後的專屬子物件，camelCase 對齊引擎詞彙——跟
// skills.go TauntEffectDTO 是同一份 EffectAtLevel 展開結果的兩種殼（snake_case／camelCase），
// 不會漂移。
type wireTaunt struct {
	DurationMs     int     `json:"durationMs"`
	DamageTakenPct float64 `json:"damageTakenPct"`
	Retarget       bool    `json:"retarget"`
}

type wireItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IconURL  string `json:"iconUrl"`
	Quantity int    `json:"quantity"`
	Kind     string `json:"kind"`
	Amount   int    `json:"amount"`
}

// wireWeapon DORPG P7（WIRE：戰鬥 bootstrap party member.weapon 的物件形狀）——id/name/typeId
// 留空代表「沒有真正的武器實體」（隊友的固定視覺、或理論上的未來擴充），只有 visual 保證有值。
type wireWeapon struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	TypeID  string            `json:"typeId"`
	Visual  string            `json:"visual"`
	Profile WeaponProfileWire `json:"profile"`
}

// buildCompanionWeaponWire 傭兵本輪不裝備（CONTRACT §2「維持腳本＋固定視覺」）：固定送
// visual=CompanionRow.Weapon、Profile=中性值（等同過去「沒有武器系統」時的戰鬥行為，crit_pct
// 等欄位全 0、hits=1/hit_mul=1 不改變攻擊次數與傷害）。
func buildCompanionWeaponWire(visual string) *wireWeapon {
	return &wireWeapon{Visual: visual, Profile: ToWeaponProfileWire(DefaultWeaponProfile())}
}

// buildPlayerWeaponWire 玩家目前裝備的武器（nil＝未裝備）。visual 用 type.visual（CONTRACT §3
// 「武器視覺＝type.visual（覆蓋職業預設）」），未裝備時整個物件送 nil，前端／引擎沿用職業預設
// 視覺（不需要後端重複塞一份 job.Weapon 進來）。
func buildPlayerWeaponWire(weapon *WeaponRow, wtype *WeaponTypeRow) *wireWeapon {
	if weapon == nil || wtype == nil {
		return nil
	}
	// DORPG P12（CONTRACT §3）：排位加成／貫穿掛在武器類型 traits，不是武器本身的 profile——
	// 這裡是玩家與傭兵唯一共用的組裝點（resolveCompanionWeaponWire 裝備武器時也呼叫這支函式），
	// 合併一次就同時覆蓋兩條路徑，不需要在呼叫端各自合併。
	profile := ToWeaponProfileWire(weapon.Profile).withRowBonus(weaponTypeRowBonus(wtype.Traits))
	return &wireWeapon{
		ID: weapon.ID, Name: weapon.Name, TypeID: weapon.TypeID, Visual: wtype.Visual,
		Profile: profile,
	}
}

// resolvePlayerWeaponWire DORPG P7（INTEGRATOR 補，BattleBootstrap 呼叫）：玩家有裝備武器就用
// 它（buildPlayerWeaponWire 非 nil）；未裝備／migration 183 未套用時退回職業預設視覺（比照
// buildCompanionWeaponWire 對傭兵的既有處理：id/name/typeId 留空、只有 visual 有意義、Profile
// 中性值），而不是整個送 null——P7 之前 wirePartyMember.Weapon 本來就是 job.Weapon 這個字串
// （見 git blame，本輪被物件形狀取代），前端沒有另外實作「jobId＋Jobs 清單」查表退回機制
// （ENGINE/FRONTEND 交接記錄皆承認尚未做）；若這裡真的送 null，武器系統上線當下（甚至 migration
// 183 套用前）每一位玩家戰鬥畫面都會顯示成引擎寫死的預設劍，等同讓非劍職業的戰鬥視覺整批倒退。
// job 為 nil（未選職業，或查無職業列）時保守回傳 nil，跟原本 buildPlayerWeaponWire 的行為一致。
func resolvePlayerWeaponWire(weaponRow *WeaponRow, wtype *WeaponTypeRow, job *JobRow) *wireWeapon {
	if w := buildPlayerWeaponWire(weaponRow, wtype); w != nil {
		return w
	}
	if job == nil {
		return nil
	}
	return buildCompanionWeaponWire(job.Weapon)
}

// resolveCompanionWeaponWire DORPG P9（CONTRACT §2/§3、WIRE「傭兵 party member 新增 weapon
// （WeaponWire，同玩家）...未裝備時沿用 companion 表的 weapon 欄位」）：傭兵腳本裝備了武器就用
// 它自己的 id/name/typeId/type.visual/profile（buildPlayerWeaponWire，跟玩家同一條路徑）；
// 未裝備／查無資料時退回這位傭兵表定的固定視覺＋中性 profile（buildCompanionWeaponWire，P6/P7
// 時代的既有行為），理由同 resolvePlayerWeaponWire 對玩家未裝備時的既有處理。
func resolveCompanionWeaponWire(weaponRow *WeaponRow, wtype *WeaponTypeRow, fallbackVisual string) *wireWeapon {
	if w := buildPlayerWeaponWire(weaponRow, wtype); w != nil {
		return w
	}
	return buildCompanionWeaponWire(fallbackVisual)
}

type wirePartyMember struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Level       int         `json:"level"`
	HP          int         `json:"hp"`
	HPMax       int         `json:"hpMax"`
	MP          int         `json:"mp"`
	MPMax       int         `json:"mpMax"`
	PortraitURL *string     `json:"portraitUrl"`
	Stats       *ActorStats `json:"stats,omitempty"`
	// Weapon DORPG P7（WIRE：party member.weapon 由純視覺字串升級為完整武器物件）：玩家未裝備
	// 武器時為 nil（前端可依 JobID 從 Jobs 清單找職業預設視覺頂替顯示，見 CONTRACT §3「武器
	// 視覺＝type.visual（覆蓋職業預設）」——沒有覆蓋值時退回職業預設是前端的既有能力，不需要
	// 後端在這裡重複塞一份）；隊友本輪不裝備，固定送一個只有 visual 有意義、Profile 為中性值的
	// 物件（等同「沒有武器系統」的舊行為，不影響既有戰鬥手感，見 buildCompanionWeaponWire）。
	Weapon *wireWeapon   `json:"weapon"`
	Rating *CombatRating `json:"rating,omitempty"` // P2：命中/暴擊評級，見 scaling.go CombatRating
	JobID  *string       `json:"jobId,omitempty"`  // P5：目前職業（未選職業 omit，維持現行預設）

	// --- DORPG P6（WIRE：「party member（隊友）」新增欄位）---
	// Skills 隊友 AI 可用技能（該傭兵目前腳本 level>=1 且非 passive、implemented=true 的技能，
	// 已用 ExpandEffect 展開，見 tavern.go buildCompanionSkillsWire）。玩家（party[0]）不填這個
	// 欄位——玩家的技能欄是既有的 Skills（[10]*wireSkill，見 wireBattleSample.Skills），跟隊友
	// 這個「AI 決策用」的清單是兩回事，故意不共用同一個欄位名。
	Skills []wireSkill `json:"skills,omitempty"`
	// PresetName 這位隊友目前套用的腳本名稱（"預設"＝系統預設腳本）。玩家（party[0]）不填。
	PresetName string `json:"presetName,omitempty"`

	// EquipmentEffects DORPG P8（WIRE：party member「equipmentEffects」，只彙總防具與飾品，不含
	// 武器——武器仍走上面的 Weapon.Profile）：引擎用它算攻擊冷卻（跟 weapon.intervalPct 相加）、
	// 技能 MP 成本減免、每 5 秒 HP/MP 回復、受到傷害/屬性抗性（跟 weapon/buff 相加）。DORPG P9
	// 起傭兵也會裝備防具/飾品（presetEquipmentEffects），沒有配置的格子自然貢獻零值，不需要另外
	// 判斷——前端／引擎不必額外判斷這個欄位存不存在。
	EquipmentEffects wireEquipmentEffects `json:"equipmentEffects"`

	// StrategyID DORPG P9（CONTRACT §1/§4、WIRE）：這位成員的 AI 戰鬥策略 id——玩家＝
	// auto_strategy_id，傭兵＝目前套用腳本的 strategy_id。引擎 decideAction() 用它決定行為，
	// 未知 id 一律退回 balanced（見 strategies.go DefaultStrategyID）。
	StrategyID string `json:"strategyId"`

	// GuardTaunt DORPG P10（CONTRACT §3、WIRE）：這位成員是否學到「守護本能」（passive，effect
	// 帶 guard_taunt:true）≥1 級——引擎的 inGuardianState() 用它判斷「按防禦（GUARD）期間」是否
	// 也算進入守護狀態（見 hasGuardTauntPassive，skills.go）。
	GuardTaunt bool `json:"guardTaunt"`
	// JobTraits DORPG P10（CONTRACT §1/§4、WIRE）：職業天生特性，目前只有 damageTakenPct 一項。
	// 數值已經合併進上面 EquipmentEffects.DamageTakenPct（引擎只需要讀那個欄位就會生效），這裡
	// 另外原樣送一份純粹供顯示（角色頁「職業特性：受到傷害 −15%」，見 wireJobTraits 註解）。
	JobTraits wireJobTraits `json:"jobTraits"`
}

// wireJobTraits DORPG P10（WIRE：戰鬥 bootstrap party member.jobTraits 的形狀）——camelCase，
// 目前只有一項；沒有這項特性的職業一律送 0（跟「沒有 traits」語意相同，battle.go 呼叫端不必
// 另外判斷這個欄位存不存在，比照 wireEquipmentEffects 對缺裝備時全零的既有慣例）。
type wireJobTraits struct {
	DamageTakenPct float64 `json:"damageTakenPct"`
}

// jobTraitsDamageTakenPct 讀 JobTraits.DamageTakenPct（nil＝這個職業沒有這項特性，回 0）。
func jobTraitsDamageTakenPct(t JobTraits) float64 {
	if t.DamageTakenPct == nil {
		return 0
	}
	return *t.DamageTakenPct
}

// clampDamageTakenPct DORPG P10（CONTRACT §1「（職業特性）套用到玩家與同職業傭兵的戰鬥
// equipmentEffects.damageTakenPct（相加，仍受 −60 夾限）」）：armor.go AggregateEquipment 對
// 純裝備彙總已經套用同一個 -60 下限，但那支函式不在本輪 BACKEND 所有權內（見 content.go
// JobRow.PathC 檔頭說明），這裡疊加職業特性之後要重新夾一次——只能在自己的檔案內複寫同一個
// 常數，兩處之後若要改動夾限值，需要一起改。
func clampDamageTakenPct(v float64) float64 {
	if v < -60 {
		return -60
	}
	return v
}

// wireEquipmentEffects DORPG P8（WIRE：戰鬥 bootstrap party member.equipmentEffects 的形狀）
// ——camelCase 對齊前端 engine 詞彙，六個欄位皆為 EquipBonus 裡「只彙總防具與飾品」的子集（見
// armor.go EquipBonus 檔頭註解）。
type wireEquipmentEffects struct {
	IntervalPct      float64 `json:"intervalPct"`
	MpCostReducePct  float64 `json:"mpCostReducePct"`
	HpRegenPctPer5s  float64 `json:"hpRegenPctPer5s"`
	MpRegenPctPer5s  float64 `json:"mpRegenPctPer5s"`
	DamageTakenPct   float64 `json:"damageTakenPct"`
	ElementResistPct float64 `json:"elementResistPct"`
}

// toWireEquipmentEffects 轉換 EquipBonus（AggregateEquipment(nil, armors) 只彙總防具/飾品的
// 結果）成戰鬥 bootstrap 要送給引擎的形狀。
func toWireEquipmentEffects(b EquipBonus) wireEquipmentEffects {
	return wireEquipmentEffects{
		IntervalPct: b.IntervalPct, MpCostReducePct: b.MpCostReducePct,
		HpRegenPctPer5s: b.HpRegenPctPer5s, MpRegenPctPer5s: b.MpRegenPctPer5s,
		DamageTakenPct: b.DamageTakenPct, ElementResistPct: b.ElementResistPct,
	}
}

type wireEnemy struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Level int    `json:"level"`
	HP    int    `json:"hp"`
	HPMax int    `json:"hpMax"`
	Slot  string `json:"slot"`
	// Row DORPG P12（CONTRACT §1/§3）：'front'|'rear'，由 Slot 推導（enemyRowFromSlot）——引擎
	// rowBonusMultiplier()／貫穿命中判定用它決定「這隻怪是前排還是後排」，不用重新解析 Slot 字串。
	Row            string        `json:"row"`
	ImageURL       string        `json:"imageUrl"`
	Rank           string        `json:"rank,omitempty"`
	Attribute      string        `json:"attribute,omitempty"`
	Size           string        `json:"size,omitempty"`
	Race           string        `json:"race,omitempty"`
	Stats          *ActorStats   `json:"stats,omitempty"`
	ThreatPriority int           `json:"threatPriority,omitempty"`
	Rating         *CombatRating `json:"rating,omitempty"` // P2：命中/暴擊評級，見 scaling.go CombatRating
	WeakElements   []string      `json:"weakElements"`     // P5：CONTRACT §6 屬性相剋，DOR 8 桶英文代碼
	// ⚠️ 不能加 omitempty：Go 的 omitempty 對 bool 是「等於零值(false)就省略」，會讓
	// canEscape=false（契約最在意的那個值，BOSS 場整場不能逃）被吃掉、前端收到 undefined
	// 又落回預設 true——這裡刻意每筆都明確送 true/false，比省略省一點 bytes 更重要。
	CanEscape bool `json:"canEscape"`

	// RankLabel/BadgeColor DORPG P11（WIRE：「enemy 新增 rank/rankLabel/badgeColor」）：來自
	// rpg_monster_ranks（透過 mr.Rank 查表，見 battle.go 呼叫端），查無資料（rank 空字串／
	// migration 189 未套用時退化）留空，前端沒有徽章可顯示但不影響戰鬥本身。
	RankLabel  string `json:"rankLabel,omitempty"`
	BadgeColor string `json:"badgeColor,omitempty"`
	// IsSummoned DORPG P11（WIRE：「isSummoned(false)」）：同 CanEscape，bool 的有意義預設值
	// 是 false，不能用 omitempty（省略會被前端當成 undefined，理論上等同 falsy，但這裡跟隨
	// CanEscape 的既有規則明確每筆都送，避免未來有人誤加 omitempty 造成語意混淆）。一般敵人
	// 恆為 false；summonPool 裡的敵人恆為 true（battle.go buildSummonPool）。
	IsSummoned bool `json:"isSummoned"`
}

// wireSummonWave DORPG P11（CONTRACT §1、WIRE：「summonPool: [{ summonerEnemyId, atHpPct,
// enemies: EnemyWire[] }]」）：某個召喚者（rank ∈ A/SA/S/SS）在 HP% 跨越 atHpPct 門檻時要放進
// 場上的一整波敵人（可能混合多個 rank，見 SS 20% 門檻同時召喚 2×B＋1×A——CONTRACT §1「一波可
// 多筆」，這裡用「同一 summonerEnemyId＋同一 atHpPct」合併多筆 SummonWave 成一個 wire 物件，
// 而不是逐一 SummonWave 各自一個 wire 物件，理由見 buildSummonPool 註解）。
type wireSummonWave struct {
	SummonerEnemyID string      `json:"summonerEnemyId"`
	AtHpPct         int         `json:"atHpPct"`
	Enemies         []wireEnemy `json:"enemies"`
}

type wireBattleSample struct {
	Party           []wirePartyMember `json:"party"`
	Enemies         []wireEnemy       `json:"enemies"`
	Scene           wireScene         `json:"scene"`
	Skills          [10]*wireSkill    `json:"skills"` // P5：8→10 格（兩排各 5，見 CONTRACT §4）
	Items           []wireItem        `json:"items"`
	InitialTargetID string            `json:"initialTargetId"`
	SceneKind       string            `json:"sceneKind"`
	EscapeChance    float64           `json:"escapeChance"`
	// SummonPool DORPG P11（WIRE）：預先算好的召喚波次（見 wireSummonWave／buildSummonPool），
	// 沒有任何召喚者時是空陣列（不是 null——比照本檔其餘 []wireXxx{} 初始化慣例，前端可以直接
	// .length/.map 不必先判斷是否存在）。
	SummonPool []wireSummonWave `json:"summonPool"`
}

// wireBattleConfig 對齊前端 engine/types.ts BattleConfig 的同名子集（見 config.go 新增欄位的
// 註解）；引擎專屬、後台不開放調整的欄位（enemyWindupMs 等純動畫時間常數）不在這裡，前端
// fromApi.ts 合併時繼續吃 DEFAULT_BATTLE_CONFIG。
type wireBattleConfig struct {
	AttackCooldownMs      int     `json:"attackCooldownMs"`
	ChargeMinMs           int     `json:"chargeMinMs"`
	ChargeFullMs          int     `json:"chargeFullMs"`
	ChargeMaxMultiplier   float64 `json:"chargeMaxMultiplier"`
	GuardDamageMultiplier float64 `json:"guardDamageMultiplier"`
	RecoveryMs            int     `json:"recoveryMs"`
	DefaultCastMs         int     `json:"defaultCastMs"`
	EscapeJudgeMs         int     `json:"escapeJudgeMs"`
	EnemyActIntervalMs    [2]int  `json:"enemyActIntervalMs"`
	AllyActIntervalMs     [2]int  `json:"allyActIntervalMs"`
	HitRate               float64 `json:"hitRate"`
	CritRate              float64 `json:"critRate"`
	CritMultiplier        float64 `json:"critMultiplier"`
	ResolveDelayMs        int     `json:"resolveDelayMs"`

	// ---- P2（暴擊／Miss／無效攻擊）新增：對齊前端 engine/types.ts BattleConfig 同名欄位。 ----
	BaseMissPct           float64                       `json:"baseMissPct"`
	HitFleeScale          float64                       `json:"hitFleeScale"`
	MissMinPct            float64                       `json:"missMinPct"`
	MissMaxPct            float64                       `json:"missMaxPct"`
	MonsterHitBase        float64                       `json:"monsterHitBase"`
	MonsterFleeBase       float64                       `json:"monsterFleeBase"`
	MonsterCritPct        float64                       `json:"monsterCritPct"`
	MonsterCritShieldBase float64                       `json:"monsterCritShieldBase"`
	ElementChart          map[string]map[string]float64 `json:"elementChart"`

	// ---- P2 修正第 3 輪新增：怪物命中/迴避等級基線斜率、AGI→攻速、DEX→詠唱縮減換算參數。
	// 命名沿用本檔一貫轉換規則（去掉 battle_ 前綴後轉 camelCase）。 ----
	MonsterHitPerLevel  float64 `json:"monsterHitPerLevel"`
	MonsterFleePerLevel float64 `json:"monsterFleePerLevel"`
	AspdReference       float64 `json:"aspdReference"`
	AttackCooldownMinMs int     `json:"attackCooldownMinMs"`
	CastMinMs           int     `json:"castMinMs"`

	// ---- P5（CONTRACT §6）新增：暴擊倍率改浮動範圍、屬性相剋加成 %。既有 CritMultiplier 保留
	// 欄位相容，前端起這輪不再使用它（改用 CritMultMin/Max 均勻抽），見 config.go 對應欄位註解。----
	CritMultMin      float64 `json:"critMultMin"`
	CritMultMax      float64 `json:"critMultMax"`
	WeaknessBonusPct float64 `json:"weaknessBonusPct"`

	// ---- P7（CONTRACT §1、WIRE）新增：五行＋光暗＋同屬性相剋的三個百分比，供 ENGINE 的
	// elementMultiplier() 鏡像 elements.go ElementMultiplier() 同一份規則。----
	ElementAdvantagePct    float64 `json:"elementAdvantagePct"`
	ElementDisadvantagePct float64 `json:"elementDisadvantagePct"`
	ElementSamePct         float64 `json:"elementSamePct"`

	// ScaleMode DORPG P6（WIRE：「config 新增 scaleMode: "level"|"power"（純顯示／除錯）」）——
	// 純粹讓前端偵錯／顯示用，引擎本身不依這個欄位分支（怪物數值在後端就已經算好送過去）。
	ScaleMode string `json:"scaleMode"`

	// AiStrategies DORPG P9（WIRE：「config 新增 aiStrategies: { [id]: { params } }（引擎預設 ⊕
	// DB 覆寫，只含 is_active 的 id）」）——buildWireConfig() 本身不連 DB，這個欄位由呼叫端
	// （BattleBootstrap）算好後另外賦值（見該函式）。
	AiStrategies map[string]wireStrategyParams `json:"aiStrategies"`
}

func buildWireConfig(cfg Config) wireBattleConfig {
	return wireBattleConfig{
		AttackCooldownMs:      cfg.BattleAttackCooldownMs,
		ChargeMinMs:           cfg.BattleChargeMinMs,
		ChargeFullMs:          cfg.BattleChargeFullMs,
		ChargeMaxMultiplier:   cfg.BattleChargeMaxMultiplier,
		GuardDamageMultiplier: cfg.BattleGuardMultiplier,
		RecoveryMs:            cfg.BattleRecoveryMs,
		DefaultCastMs:         cfg.BattleDefaultCastMs,
		EscapeJudgeMs:         cfg.BattleEscapeJudgeMs,
		EnemyActIntervalMs:    [2]int{cfg.BattleEnemyActMinMs, cfg.BattleEnemyActMaxMs},
		AllyActIntervalMs:     [2]int{cfg.BattleAllyActMinMs, cfg.BattleAllyActMaxMs},
		HitRate:               cfg.BattleHitRate,
		CritRate:              cfg.BattleCritRate,
		CritMultiplier:        cfg.BattleCritMultiplier,
		ResolveDelayMs:        cfg.BattleResolveDelayMs,

		BaseMissPct:           cfg.BattleBaseMissPct,
		HitFleeScale:          cfg.BattleHitFleeScale,
		MissMinPct:            cfg.BattleMissMinPct,
		MissMaxPct:            cfg.BattleMissMaxPct,
		MonsterHitBase:        cfg.BattleMonsterHitBase,
		MonsterFleeBase:       cfg.BattleMonsterFleeBase,
		MonsterCritPct:        cfg.BattleMonsterCritPct,
		MonsterCritShieldBase: cfg.BattleMonsterCritShieldBase,
		ElementChart:          cfg.BattleElementChart,

		MonsterHitPerLevel:  cfg.BattleMonsterHitPerLevel,
		MonsterFleePerLevel: cfg.BattleMonsterFleePerLevel,
		AspdReference:       cfg.BattleAspdReference,
		AttackCooldownMinMs: cfg.BattleAttackCooldownMinMs,
		CastMinMs:           cfg.BattleCastMinMs,

		CritMultMin:      cfg.CritMultMin,
		CritMultMax:      cfg.CritMultMax,
		WeaknessBonusPct: cfg.BattleWeaknessBonusPct,

		ElementAdvantagePct:    cfg.BattleElementAdvantagePct,
		ElementDisadvantagePct: cfg.BattleElementDisadvantagePct,
		ElementSamePct:         cfg.BattleElementSamePct,

		ScaleMode: cfg.BattleScaleMode,
	}
}

func toWireSkill(s SkillRow) wireSkill {
	return wireSkill{
		ID: s.ID, Name: s.Name, IconURL: kitAssetURL(s.IconID), CooldownMs: s.CooldownMs,
		Kind: s.Kind, Target: s.Target, MPCost: s.MPCost, Coefficient: s.Coefficient,
		Flat: s.Flat, Element: s.Element, Weapon: s.Weapon, CastMs: s.CastMs,
	}
}

// toWireSkillLegacy P5：未選職業時的既有 is_default 技能填法。新欄位（level/maxLevel/...）用
// 頂層既有欄位（Coefficient/Flat/MPCost）合成一個 EffectAtLevel，不吃 s.Effect JSONB——既有
// 5 個技能列這個欄位是預設空物件，語意上沒有分級數值，讀了只會是全 0。Level/MaxLevel 固定 1：
// 這些技能沒有「配點升級」概念，永遠視為「已在唯一等級」。
func toWireSkillLegacy(s SkillRow) wireSkill {
	w := toWireSkill(s)
	w.Level = 1
	w.MaxLevel = 1
	w.DisplayText = s.DisplayText
	w.DmgType = s.DmgType
	w.Implemented = s.Implemented
	// Hits 審查#1：既有技能沒有等級/連段概念，頂層鏡射固定 1（跟下面 Effect.Hits 一致）。
	w.Hits = 1
	w.Effect = EffectAtLevel{
		Kind: s.Kind, Target: s.Target, Element: s.Element,
		Coef: s.Coefficient, Flat: float64(s.Flat), Hits: 1, MPCost: float64(s.MPCost),
	}
	return w
}

// toWireSkillLeveled P5：已選職業時的職業技能填法，effect 依玩家目前等級展開（ExpandEffect，
// 見 skills.go）。呼叫端保證只在 level>=1 時呼叫（WIRE：「該職業 level≥1 的技能...填入」）。
//
// 審查#1【高・CONFIRMED】根因修復：這裡曾經把頂層 Coefficient/Flat/MPCost 設成 SkillRow 的
// 原始欄位（s.Coefficient/s.Flat/s.MPCost，語意是「rpg_skills 資料列本身」的基準值，不是任何
// 特定等級的展開值），只有巢狀 Effect 欄位才是 ExpandEffect() 依 level 展開後的即時值——但
// apps/web/src/lib/dorpg/fromApi.ts mapSkill() 讀的是頂層 coefficient/flat/mpCost（跟既有 5 個
// 一般技能同一套欄位，見 types.ts Skill 型別「damage/heal/shield 的實際戰鬥結算仍讀上面的頂層
// coefficient/flat/hits...」的註解），engine/combat.ts 的傷害結算與 dispatch.ts 的 MP 扣除
// 也只讀這批頂層欄位，從未讀 Effect.coef/flat/hits。結果：玩家把技能練到 Lv10，戰鬥裡打出來的
// 數值仍然是 Lv1 基準值（升級沒有任何效果），而且舊版 wireSkill 根本沒有頂層 Hits 欄位，連段技能
// （hits>1，例如 lk_a4/ar_a4/ar_b1/mg_a5/mg_b5）永遠只觸發 1 次攻擊事件。
// 修法：頂層 Coefficient/Flat/MPCost/Hits 全部改吃 ExpandEffect(s, level) 的展開值（跟 Effect
// 欄位算的是同一份數字，只是一個攤平在頂層供既有戰鬥邏輯讀、一個保留巢狀物件供 FRONTEND 顯示／
// buff-debuff 讀取），兩邊数字保證一致（見 battle_test.go 的回歸測試）。
func toWireSkillLeveled(s SkillRow, level int) wireSkill {
	e := ExpandEffect(s, level)
	w := wireSkill{
		ID: s.ID, Name: s.Name, IconURL: kitAssetURL(s.IconID), CooldownMs: s.CooldownMs,
		Kind: s.Kind, Target: s.Target,
		MPCost: roundInt(e.MPCost), Coefficient: e.Coef, Flat: roundInt(e.Flat),
		Element: s.Element, Weapon: s.Weapon, CastMs: s.CastMs,
		Level: level, MaxLevel: s.MaxLevel, DisplayText: s.DisplayText, DmgType: s.DmgType,
		Implemented: s.Implemented, Hits: e.Hits, Effect: e, Tier: s.Tier,
	}
	// DORPG P10（CONTRACT §3、WIRE）：僅 kind=taunt 才帶這個子物件，理由同 skills.go
	// skillDTOsFromLevels 的 Taunt 欄位（同一份 ExpandEffect 結果拆出來，不重算）。
	if s.Kind == "taunt" {
		w.Taunt = &wireTaunt{DurationMs: e.DurationMs, DamageTakenPct: e.DamageTakenPct, Retarget: e.Retarget}
	}
	return w
}

func toWireItem(it ItemRow) wireItem {
	return wireItem{
		ID: it.ID, Name: it.Name, IconURL: kitAssetURL(it.IconID),
		Quantity: it.DefaultQuantity, Kind: it.Kind, Amount: it.Amount,
	}
}

func roundInt(f float64) int { return int(math.Round(f)) }

// BattleRouter 掛 /rpg/battle（main.go 內 Mount 路徑，跟既有 /rpg 同一個已登入群組）。
func (h *Handler) BattleRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireEntry)
	r.Get("/encounters", h.BattleEncounters)
	r.Get("/bootstrap", h.BattleBootstrap)
	r.Post("/report", h.BattleReport)
	return r
}

// RanksRouter DORPG P11（WIRE：「GET /rpg/ranks」）掛 /rpg/ranks——獨立的靜態路徑（不是
// "/rpg" 或 "/rpg/battle" 底下的子路徑，WIRE 給的字面路徑就是 /rpg/ranks），比照 BattleRouter
// 套用同一個套件私有 requireEntry 白名單（main.go 直接 Mount 這個 Router，見該檔）。
func (h *Handler) RanksRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireEntry)
	r.Get("/", h.GetRanks)
	return r
}

// GetRanks GET /rpg/ranks：九級強度表（WIRE：「不含 summon／level_curve」，見 RankDTO）。
func (h *Handler) GetRanks(w http.ResponseWriter, r *http.Request) {
	ranks, err := h.listRanks(r.Context())
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errRanksNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load ranks")
		return
	}
	dtos := make([]RankDTO, 0, len(ranks))
	for _, rk := range ranks {
		dtos = append(dtos, toRankDTO(rk))
	}
	respondJSON(w, http.StatusOK, map[string]any{"ranks": dtos})
}

// loadPlayerBattleStats 兩個端點（BattleEncounters/BattleBootstrap）都要「玩家這場戰鬥要用的
// 數值」，抽成共用函式避免重複「角色列→exp→Base Lv→Compute→套保底」五步查詢。回傳的
// PlayerBattleStats 已套用 battle_player_min_atk/min_hp 保底——角色摘要列跟實際戰鬥用同一組
// 數字，不會出現「選單顯示的血量」跟「戰鬥裡的血量」對不上的情況。
//
// P5：回傳的等級（第三個回傳值、也是 PlayerBattleStats.BaseLevel）改用「有效等級」
// EffectiveLevel(test_level, 真實 Base Lv)——CONTRACT §2 明講「戰鬥 bootstrap」是必須套用
// test_level 的場合之一。同時依角色目前職業決定 Compute 的 WeaponType（atk_branch）與被動
// 技能加成（Passives），未選職業維持既有全域行為。
func (h *Handler) loadPlayerBattleStats(ctx context.Context, uid string, cfg Config) (PlayerBattleStats, character, int, error) {
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
		return PlayerBattleStats{}, character{}, 0, err
	}
	_, effLevel, err := h.loadEffectiveLevel(ctx, uid, cfg, ch.TestLevel)
	if err != nil {
		return PlayerBattleStats{}, character{}, 0, err
	}
	stats := Stats{Str: ch.Str, Agi: ch.Agi, Vit: ch.Vit, Dex: ch.Dex, Int: ch.Int, Luk: ch.Luk}
	in := ComputeInput{BaseLevel: effLevel, JobLevel: ch.JobLevel, Stats: stats}
	if ch.JobID != nil {
		job, err := h.getJobByID(ctx, *ch.JobID)
		if err == nil {
			in.WeaponType = job.AtkBranch
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return PlayerBattleStats{}, character{}, 0, err
		}
	}
	passives, err := h.loadPassives(ctx, uid, ch.JobID)
	if err != nil {
		return PlayerBattleStats{}, character{}, 0, err
	}
	in.Passives = passives

	// DORPG P7/P8：目前裝備的武器＋防具/飾品彙總後套進 Compute，讓戰鬥用的 PlayerBattleStats
	// （Atk/Matk/Def/Mdef/Rating）反映裝備效果（CONTRACT §2/§3）。migration 183/184 未套用時
	// 視為「還沒有裝備系統」，不讓這支既有端點因此 500（比照 buildCharacterView 同款容錯，見
	// loadPlayerEquipment 內部處理）。
	equipSnap, err := h.loadPlayerEquipment(ctx, uid)
	if err != nil {
		return PlayerBattleStats{}, character{}, 0, err
	}
	in.Equip = equipSnap.Equip()

	d := Compute(cfg, in)
	return PlayerBattleStatsFrom(cfg, effLevel, d), ch, effLevel, nil
}

// --- 缺表統一回應（契約 §3.4：503 帶固定訊息，不噴 SQL） ---

func respondIfMissingRelation(w http.ResponseWriter, err error) bool {
	if isMissingRelation(err) {
		respondErr(w, http.StatusServiceUnavailable, errContentNotReady)
		return true
	}
	return false
}

type wireEncounterInfo struct {
	Code          string `json:"code"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`
	SceneID       string `json:"scene_id"`
	SceneImageURL string `json:"scene_image_url"`
	SceneKind     string `json:"scene_kind"`
	Difficulty    int    `json:"difficulty"`
	CanEscape     bool   `json:"can_escape"`
	// MonsterLevel DORPG P6（WIRE：「GET /rpg/battle/encounters：每場新增 monster_level」）——
	// EncounterPicker 用它顯示「怪物 Lv.N」，跟 battle_scale_mode 是否為 "level" 無關（永遠送）。
	// DORPG P11 起：level_mode="player" 的場（20 場強度挑戰）這裡改回玩家有效等級，見 WIRE
	// 「monster_level 在 level_mode=player 時回玩家有效等級（伺服器算好）」與呼叫端
	// resolveEncounterMonsterLevel。
	MonsterLevel int `json:"monster_level"`

	// --- DORPG P11（CONTRACT §1/§2、WIRE）新增：怪物強度九級系統，「強度挑戰」對戰列表分組用。
	// legacy 六場：ScalingMode="legacy"、LevelMode="fixed"、Rank/RankLabel/BadgeColor/
	// MonsterCount 皆 nil、Group="story"。20 場強度挑戰：ScalingMode="rank"、LevelMode=
	// "player"、Group="rank"，其餘欄位由 rpg_monster_ranks 查表填入。---
	ScalingMode  string  `json:"scaling_mode"`
	LevelMode    string  `json:"level_mode"`
	Rank         *string `json:"rank"`
	RankLabel    *string `json:"rank_label"`
	BadgeColor   *string `json:"badge_color"`
	MonsterCount *int    `json:"monster_count"`
	Group        string  `json:"group"` // "story"|"rank"，前端分組用
}

// encounterGroup DORPG P11（WIRE：「group: 'story'|'rank'...legacy 六場＝story」）：純粹由
// scaling_mode 推導，不是 DB 欄位——兩者永遠一一對應，沒有另存一份的必要。
func encounterGroup(scalingMode string) string {
	if scalingMode == "rank" {
		return "rank"
	}
	return "story"
}

// buildWireEncounterInfo DORPG P11：BattleEncounters（列表）與 BattleBootstrap（單場）共用的
// wireEncounterInfo 組裝——避免兩處各自手寫一次 rank/rankLabel/badgeColor/group 的邏輯而漂移。
// sceneImageURL/monsterLevel 由呼叫端算好傳入（前者要查 scenes map，後者依 level_mode 可能要
// 換成玩家有效等級，兩邊呼叫端手上已有的資料不同，抽進來反而要多傳兩個參數的意義不大）。
func buildWireEncounterInfo(e EncounterRow, sceneImageURL string, monsterLevel int, ranksByID map[string]RankRow) wireEncounterInfo {
	info := wireEncounterInfo{
		Code: e.Code, Title: e.Title, Subtitle: e.Subtitle, SceneID: e.SceneID,
		SceneImageURL: sceneImageURL, SceneKind: e.SceneKind, Difficulty: e.Difficulty, CanEscape: e.CanEscape,
		MonsterLevel: monsterLevel,
		ScalingMode:  e.ScalingMode, LevelMode: e.LevelMode, MonsterCount: e.MonsterCount,
		Group: encounterGroup(e.ScalingMode),
	}
	if e.Rank != nil {
		info.Rank = e.Rank
		if rk, ok := ranksByID[*e.Rank]; ok {
			label, color := rk.Label, rk.BadgeColor
			info.RankLabel = &label
			info.BadgeColor = &color
		}
	}
	return info
}

type wireEncounterMonsterInfo struct {
	Slot      string `json:"slot"`
	Name      string `json:"name"`
	PosterURL string `json:"poster_url"`
	IsBoss    bool   `json:"is_boss"`
}

type wireEncounterStats struct {
	Plays       int    `json:"plays"`
	Wins        int    `json:"wins"`
	BestMs      *int   `json:"best_ms"`
	LastOutcome string `json:"last_outcome"`
}

type wireEncounterSummary struct {
	wireEncounterInfo
	Monsters []wireEncounterMonsterInfo `json:"monsters"`
	Stats    wireEncounterStats         `json:"stats"`
}

func stringSetKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// GET /rpg/battle/encounters
func (h *Handler) BattleEncounters(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, _ := ctx.Value(auth.CtxKeyUserID).(string)

	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	encounters, err := h.listEncounters(ctx, true)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load encounters")
		return
	}

	sceneIDs := map[string]bool{}
	monsterIDs := map[string]bool{}
	for _, e := range encounters {
		sceneIDs[e.SceneID] = true
		for _, m := range e.Monsters {
			monsterIDs[m.MonsterID] = true
		}
	}
	monsters, err := h.getMonstersByIDs(ctx, stringSetKeys(monsterIDs))
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load monsters")
		return
	}
	// 審查1 CONFIRMED：原本逐一 h.getScene(ctx,id) 是 N+1（現有 6 場遭遇對應 6 個不同場景，
	// 每次打這支端點多 6 次往返），而且把 42P01（缺表）以外的所有錯誤一律靜默吞掉——若
	// rpg_scenes 因故不可用（連線中斷、逾時），會回 200 但整頁場景圖消失，而不是契約要求的
	// 503。改成比照 getMonstersByIDs 的批次查詢，並區分錯誤種類：缺表 503、其餘 DB 錯誤 500，
	// 只有「查無該筆」（不在回傳的 map 裡）才允許留空字串。
	scenes, err := h.getScenesByIDs(ctx, stringSetKeys(sceneIDs))
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load scenes")
		return
	}

	// DORPG P11：批次撈全部遭遇引用到的 rank（legacy 六場 e.Rank 是 nil，不會進這個集合），
	// 用來填 wireEncounterInfo 的 rank_label/badge_color（見 buildWireEncounterInfo）。
	rankIDs := map[string]bool{}
	for _, e := range encounters {
		if e.Rank != nil && *e.Rank != "" {
			rankIDs[*e.Rank] = true
		}
	}
	ranksByID, err := h.getRanksByIDs(ctx, stringSetKeys(rankIDs))
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errRanksNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load monster ranks")
		return
	}

	stats, err := h.loadUserBattleStats(ctx, uid)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load battle stats")
		return
	}

	pbs, ch, baseLevel, err := h.loadPlayerBattleStats(ctx, uid, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	// P5：free_points 欄位不再是真相（見 handler.go buildCharacterView），這裡改現算——
	// 不這樣改的話，玩家改用新版 /rpg/allocate 加點後這個摘要會凍結在部署當下的舊數字
	// （新版 Allocate 不再遞減這個 DB 欄位）。baseLevel 這裡其實已經是 loadPlayerBattleStats
	// 回傳的「有效等級」（P5 改動，見該函式註解），跟配點總量公式用的是同一個等級基準。
	statsNow := Stats{Str: ch.Str, Agi: ch.Agi, Vit: ch.Vit, Dex: ch.Dex, Int: ch.Int, Luk: ch.Luk}
	statFree := TotalStatPoints(cfg, baseLevel) - TotalSpentStats(cfg, statsNow)
	if statFree < 0 {
		statFree = 0
	}

	list := make([]wireEncounterSummary, 0, len(encounters))
	for _, e := range encounters {
		sc := scenes[e.SceneID]
		ms := make([]wireEncounterMonsterInfo, 0, len(e.Monsters))
		for _, em := range e.Monsters {
			mr := monsters[em.MonsterID]
			ms = append(ms, wireEncounterMonsterInfo{Slot: em.Slot, Name: mr.Name, PosterURL: mr.PosterURL, IsBoss: mr.IsBoss})
		}
		st := stats[e.Code]
		// DORPG P11：level_mode="player" 的場（強度挑戰）monster_level 回玩家有效等級，其餘
		// （legacy 六場，level_mode="fixed"）維持 e.MonsterLevel 原樣，零改動。
		monsterLevel := e.MonsterLevel
		if e.LevelMode == "player" {
			monsterLevel = baseLevel
		}
		list = append(list, wireEncounterSummary{
			wireEncounterInfo: buildWireEncounterInfo(e, sc.ImageURL, monsterLevel, ranksByID),
			Monsters:          ms,
			Stats:             wireEncounterStats{Plays: st.Plays, Wins: st.Wins, BestMs: st.BestMs, LastOutcome: st.LastOutcome},
		})
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"encounters": list,
		"character": map[string]any{
			"base_level":   baseLevel,
			"free_points":  statFree,
			"power":        PlayerPower(pbs),
			"max_hp":       roundInt(pbs.HPMax),
			"max_mp":       roundInt(pbs.MPMax),
			"unspent_hint": statFree > 0,
		},
	})
}

// getLoadout 讀 player_characters.loadout（migration 176 新欄位；handler.go 的既有
// character/scanCharacter 沒有這欄，故另外查一次——那兩個是 handler.go 唯讀不可改的既有型別）。
func (h *Handler) getLoadout(ctx context.Context, userID string) ([]string, error) {
	var loadout []string
	err := h.db.QueryRow(ctx, `SELECT loadout FROM player_characters WHERE user_id=$1`, userID).Scan(&loadout)
	if loadout == nil {
		loadout = []string{}
	}
	return loadout, err
}

// buildSkillSlots P5：已選職業（jobID!=nil）→ 該職業 level>=1 的技能依 path→tier 排序填入
// （最多 10 格，見 CONTRACT §4／WIRE）；未選職業 → 維持既有契約：「玩家 loadout 非空就照它、
// 否則取 is_default 的技能依 sort_order 補；不足補 null」（兩種來源互斥，不會混用），8 格上限
// 不變，其餘 2 格恆為 null。
//
// cfg/pbs：P2 修正第 1 輪——DB 存的 flat 是「參考玩家」身上的絕對值，組進 wire 之前要先用
// ScaleSkill 依這場戰鬥的實際玩家 HPMax 縮放（見 scaling.go 檔頭），只用在未選職業的既有路徑；
// 職業技能的真正數值在 effect JSONB（ExpandEffect 展開），本輪不對它套用參考 HP 縮放（已知
// 簡化，留給之後決定是否需要）。
func (h *Handler) buildSkillSlots(ctx context.Context, cfg Config, pbs PlayerBattleStats, loadout []string, jobID *string, userID string) ([10]*wireSkill, error) {
	var slots [10]*wireSkill

	if jobID != nil {
		jobSkills, err := h.listSkillsByJob(ctx, *jobID)
		if err != nil {
			return slots, err
		}
		ids := make([]string, len(jobSkills))
		for i, s := range jobSkills {
			ids[i] = s.ID
		}
		levels, err := h.getPlayerSkillLevels(ctx, userID, ids)
		if err != nil {
			return slots, err
		}
		i := 0
		for _, s := range jobSkills { // listSkillsByJob 已經 ORDER BY path, tier, sort_order
			if i >= 10 {
				break
			}
			lvl := levels[s.ID]
			if lvl < 1 {
				continue // 未配點的職業技能不佔欄位（WIRE：「level>=1 的技能...填入」）
			}
			// INTEGRATOR 對齊：WIRE.md「passive（不出現在技能欄；後端已算進 stats）」——passive
			// 技能即使已配點（level>=1）也不得佔用技能欄位，數值已由 loadPassives()/Compute() 算進
			// stats。原本這裡漏了這個 continue，會讓已點的被動技能誤佔一格送給前端。
			if s.Kind == "passive" {
				continue
			}
			w := toWireSkillLeveled(s, lvl)
			slots[i] = &w
			i++
		}
		return slots, nil
	}

	// 未選職業：維持現行 is_default 技能填法（8 格上限，其餘 2 格沿用 null）。
	if len(loadout) > 0 {
		ids := loadout
		if len(ids) > 8 {
			ids = ids[:8]
		}
		bySkillID, err := h.getSkillsByIDs(ctx, ids)
		if err != nil {
			return slots, err
		}
		for i, id := range ids {
			if sr, ok := bySkillID[id]; ok && sr.IsActive {
				w := toWireSkillLegacy(ScaleSkill(cfg, pbs, sr))
				slots[i] = &w
			}
		}
		return slots, nil
	}
	defaults, err := h.listSkills(ctx, true)
	if err != nil {
		return slots, err
	}
	i := 0
	for _, sr := range defaults {
		if !sr.IsDefault || i >= 8 {
			continue
		}
		w := toWireSkillLegacy(ScaleSkill(cfg, pbs, sr))
		slots[i] = &w
		i++
	}
	return slots, nil
}

// shouldBuildSummonPool 審查 CONFIRMED 修法：召喚池只該在 scaling_mode="rank"（P11 新增的
// 20 場強度挑戰）出現，legacy 六場（scaling_mode="legacy"）即使場上怪物剛好也有
// A/SA/S/SS 這種「有 summon.waves」的 rank（例如 taipei101_boss 的 DOR-MON-A），也不該被
// buildSummonPool 撈去建召喚池——CONTRACT 明講既有六場零改動。抽成純函式方便不連 DB 測試。
func shouldBuildSummonPool(enc EncounterRow) bool {
	return enc.ScalingMode == "rank"
}

// buildSummonPool DORPG P11（CONTRACT §1、WIRE：「summonPool: [{ summonerEnemyId, atHpPct,
// enemies: EnemyWire[] }]」）：對每一位「自己的 rank 在 rpg_monster_ranks.summon 定義了
// waves」的敵人（目前只有 A/SA/S/SS），把每一波要召喚的怪物預先算好完整數值與 sprite——引擎
// 收到後只需要在該召喚者 HP% 跨越 atHpPct 門檻時，把對應的 wireSummonWave.Enemies 塞進空槽
// （tick 迴圈本身不在本輪 BACKEND 所有權內，見 WIRE「引擎（TS）」章節）。
//
// 分組規則（CONTRACT §1「一波可多筆」，SS 20% 門檻同時召喚 2×B＋1×A）：同一召喚者、同一
// at_hp_pct 的多筆 SummonWave 合併成一個 wireSummonWave（同一時間點觸發一次事件、一次進場動畫，
// 而不是同一召喚者同一時刻收到兩個各自獨立的 summon 事件）。enemies[].id 用 wave 在
// rank.Summon.Waves 陣列裡「原始位置」編號（1-based）＋該位置內的第幾隻（1-based），保證合併
// 後仍然唯一——例如 SS 的 20% 門檻是第 5、第 6 筆 wave，id 會是 <summoner>_w5_1/_w5_2（B×2）
// 與 <summoner>_w6_1（A×1），不是「合併後的組編號」。
//
// 召喚怪的等級＝召喚者這場的等級（monsterLevel 參數，跟一般敵人同一個 N）、強度＝該波
// wave.Rank 的向量（不是召喚者自己的 rank）——CONTRACT：「召喚怪等級＝召喚者等級、強度＝其
// rank 向量」。encounterScale＝enc.PowerScale × wave.PowerScale（P11 修正新增：召喚怪另外疊乘
// 自己波次的 PowerScale 折減，見 ranks.go SummonWave.PowerScale 註解——模擬證實召喚怪用完整
// rank 向量太強，需要獨立於 rank.*_mult 之外的旋鈕），slotScale 固定 1.0（DB 沒有為召喚波次
// 另外存個別 slot power_scale 欄位，本輪不新增）。同一 at_hp_pct 合併多筆 wave 進同一個
// wireSummonWave 時（見下方 groupKey 分組），每一筆 need 仍各自帶著自己的 n.wave，套用時各自
// 乘各自的 PowerScale，不會被合併群組共用同一個折減值。
func (h *Handler) buildSummonPool(ctx context.Context, cfg Config, enc EncounterRow, monsterRows map[string]MonsterRow, ranksByID map[string]RankRow, monsterLevel int) ([]wireSummonWave, error) {
	// 審查 CONFIRMED：召喚池是 rpg_monster_ranks.summon（migration 189）新加的機制，只有
	// scaling_mode="rank" 的 20 場強度挑戰在契約設計上會用到。legacy 六場的怪物（例如
	// taipei101_boss 的 DOR-MON-A）剛好也有 rank="A"，若這裡不分場次一律照 monRow.Rank 查
	// summon.waves，六場既有故事戰鬥會無中生有冒出召喚怪，違反 CONTRACT「既有六場零改動」。
	// 用抽出的純函式 shouldBuildSummonPool 判斷，讓這條規則本身可以不連 DB 單獨測試。
	if !shouldBuildSummonPool(enc) {
		return []wireSummonWave{}, nil
	}
	type need struct {
		summonerID string
		waveIndex  int // 1-based，rank.Summon.Waves 裡的原始位置
		wave       SummonWave
	}
	var needs []need
	extraRankIDs := map[string]bool{}
	extraMonsterIDs := map[string]bool{}
	for _, em := range enc.Monsters {
		mr, ok := monsterRows[em.MonsterID]
		if !ok {
			continue
		}
		rk, ok := ranksByID[mr.Rank]
		if !ok || len(rk.Summon.Waves) == 0 {
			continue
		}
		for wi, wv := range rk.Summon.Waves {
			needs = append(needs, need{summonerID: em.Slot, waveIndex: wi + 1, wave: wv})
			if _, ok := ranksByID[wv.Rank]; !ok {
				extraRankIDs[wv.Rank] = true
			}
			ids := wv.MonsterIDs
			if len(ids) == 0 {
				ids = []string{"DOR-MON-R-" + wv.Rank}
			}
			for _, id := range ids {
				if _, ok := monsterRows[id]; !ok {
					extraMonsterIDs[id] = true
				}
			}
		}
	}
	if len(needs) == 0 {
		return []wireSummonWave{}, nil
	}

	// 補齊主要敵人清單以外、召喚波次才會用到的 rank/monster 資料（多數情況下 wave.Rank 就是
	// 場上某隻既有敵人的 rank，這裡只查缺的那幾筆，避免重複往返）。
	if len(extraRankIDs) > 0 {
		more, err := h.getRanksByIDs(ctx, stringSetKeys(extraRankIDs))
		if err != nil {
			return nil, err
		}
		for k, v := range more {
			ranksByID[k] = v
		}
	}
	if len(extraMonsterIDs) > 0 {
		more, err := h.getMonstersByIDs(ctx, stringSetKeys(extraMonsterIDs))
		if err != nil {
			return nil, err
		}
		for k, v := range more {
			monsterRows[k] = v
		}
	}

	type groupKey struct {
		summonerID string
		atHpPct    int
	}
	groups := map[groupKey]*wireSummonWave{}
	var order []groupKey

	for _, n := range needs {
		waveRank, ok := ranksByID[n.wave.Rank]
		if !ok {
			continue // 髒資料：summon.waves[].rank 指到查無資料的級別，略過該波而非整場 500
		}
		// 審查 CONFIRMED：Validate() 只把關「新寫入」的 summon.waves[].count（1–5），既有髒資料
		// 或繞過 Validate 直接寫進 DB 的列仍可能是 0 或負值——make([]string, n.wave.Count) 對
		// 負值會直接 panic（makeslice: len out of range）變成整個 bootstrap 500。count<=0 視同
		// 這波沒有怪，略過；>5 一律夾回 5（防禦，對齊 Validate 的值域上限），而不是讓髒資料放大
		// 召喚規模。
		count := n.wave.Count
		if count <= 0 {
			continue
		}
		if count > 5 {
			count = 5
		}
		ids := n.wave.MonsterIDs
		if len(ids) == 0 {
			ids = make([]string, count)
			for k := range ids {
				ids[k] = "DOR-MON-R-" + n.wave.Rank
			}
		}
		key := groupKey{summonerID: n.summonerID, atHpPct: n.wave.AtHpPct}
		grp, exists := groups[key]
		if !exists {
			grp = &wireSummonWave{SummonerEnemyID: n.summonerID, AtHpPct: n.wave.AtHpPct}
			groups[key] = grp
			order = append(order, key)
		}
		limit := count
		for j, mid := range ids {
			if j >= limit {
				break // monster_ids 給的數量超過 count 時只取前 count 個
			}
			monRow, ok := monsterRows[mid]
			if !ok {
				continue // 髒資料：monster_ids/DOR-MON-R-<rank> 查無此怪，略過該隻而非整波失敗
			}
			sm := ScaleMonsterByRank(cfg, monRow, waveRank, enc.PowerScale*n.wave.PowerScale, 1.0, monsterLevel)
			weak := monRow.WeakElements
			if weak == nil {
				weak = []string{}
			}
			grp.Enemies = append(grp.Enemies, wireEnemy{
				ID:   fmt.Sprintf("%s_w%d_%d", n.summonerID, n.waveIndex, j+1),
				Name: monRow.Name, Level: sm.Level,
				HP: sm.HPMax, HPMax: sm.HPMax,
				// Slot 留空、Row 保守給 "front"：實際站位由引擎在觸發當下依空槽位決定
				// （WIRE：「依空槽位數放入...front_left→front_right→rear_left→rear_right→
				// front_center 順序取空位」），bootstrap 階段還不知道屆時哪些槽位是空的。
				Slot: "", Row: "front", ImageURL: monRow.PosterURL,
				// 審查 CONFIRMED：monster_ids 指定特定怪物時，那隻怪自己的 monRow.Rank 可能跟
				// wave.Rank 不同（例如 wave.Rank=S 但 monster_ids 指到一隻 rank=A 的怪，強度仍
				// 照 waveRank 算——見上面 ScaleMonsterByRank(cfg, monRow, waveRank, ...)）。Rank/
				// RankLabel/BadgeColor 三欄本來就該同一個來源，避免前端顯示的分級徽章跟實際數值
				// 對不上，統一都用 waveRank。
				Rank: waveRank.Rank, RankLabel: waveRank.Label, BadgeColor: waveRank.BadgeColor,
				Attribute: monRow.Attribute, Size: monRow.Size, Race: monRow.Race,
				Stats:          &ActorStats{HPMax: float64(sm.HPMax), MPMax: 0, Atk: float64(sm.Atk), Matk: float64(sm.Matk), Def: float64(sm.Def), Mdef: float64(sm.Mdef)},
				ThreatPriority: monRow.Threat,
				Rating:         &sm.Rating,
				WeakElements:   weak,
				CanEscape:      enc.CanEscape,
				IsSummoned:     true,
			})
		}
	}

	out := make([]wireSummonWave, 0, len(order))
	for _, key := range order {
		out = append(out, *groups[key])
	}
	return out, nil
}

// GET /rpg/battle/bootstrap?code=<code>
func (h *Handler) BattleBootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, _ := ctx.Value(auth.CtxKeyUserID).(string)
	code := r.URL.Query().Get("code")
	if code == "" {
		respondErr(w, http.StatusBadRequest, "缺少 code")
		return
	}

	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}

	enc, err := h.getEncounterByCode(ctx, code)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusNotFound, "找不到這場遭遇")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load encounter")
		return
	}
	scene, err := h.getScene(ctx, enc.SceneID)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusNotFound, "場景不存在")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load scene")
		return
	}

	pbs, ch, baseLevel, err := h.loadPlayerBattleStats(ctx, uid, cfg)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}

	var displayName string
	if err := h.db.QueryRow(ctx, `SELECT COALESCE(name, handle) FROM users WHERE id=$1`, uid).Scan(&displayName); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	// 玩家頭像（D4）：is_player_portrait=TRUE 那一列的 portrait_id；查無資料（後台誤刪）保留
	// null，前端本來就允許空白插槽。
	//
	// 審查3 CONFIRMED（原本的寫法）：`if err==nil {...} else if !errors.Is(err, pgx.ErrNoRows)
	// && respondIfMissingRelation(w, err) { return }` 只處理了「成功」與「缺表 42P01」兩種情形
	// ——任何第三種錯誤（例如連線瞬斷、context deadline exceeded）會讓 `!errors.Is(...)` 為
	// true，但 respondIfMissingRelation 因為不是 42P01 而回傳 false，整個 else-if 判斷式變成
	// false，掉進沒有任何分支處理的縫隙：不寫回應、不 return，函式繼續往下組出 200 OK（只是
	// 缺頭像），真正的 DB 故障被完全隱藏。改成三段式 switch，讓「非預期錯誤」有明確的 500 分支。
	var playerPortraitURL *string
	pc, err := h.getPlayerPortraitCompanion(ctx)
	switch {
	case err == nil:
		u := charPortraitURL(pc.PortraitID)
		playerPortraitURL = &u
	case errors.Is(err, pgx.ErrNoRows):
		// 保留 null。
	default:
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load portrait")
		return
	}

	// 玩家目前裝備的武器（DORPG P7）：未裝備／migration 183 未套用時 buildPlayerWeaponWire 回
	// nil——INTEGRATOR 補：在這裡退回職業預設視覺（比照 buildCompanionWeaponWire 對傭兵的既有
	// 處理，沒有真正的武器實體時 id/name/typeId 留空、只有 visual 有意義、Profile 中性值），而
	// 不是整個送 null。理由：P7 之前 wirePartyMember.Weapon 本來就是 job.Weapon 這個字串（見
	// git blame，本輪直接被物件形狀取代），前端目前沒有另外實作「jobId＋Jobs 清單」的查表退回
	// 機制（ENGINE/FRONTEND 交接記錄皆承認尚未做），若這裡真的送 null，武器系統上線當下（甚至
	// migration 183 套用前）每一位玩家在戰鬥畫面都會顯示成引擎寫死的預設劍，等同讓魔法師/弓箭手
	// 這些非劍職業戰鬥視覺整批倒退——這裡補回職業預設視覺，讓「未裝備」跟 P7 之前的既有行為
	// 完全一致；真正裝備武器後 type.visual 才會覆蓋它（CONTRACT §3「武器視覺＝type.visual
	// （覆蓋職業預設）」，語意不變，只是把「職業預設」這個底線值找回來）。
	// DORPG P8：同一份查詢也帶出目前裝備的防具/飾品（migration 184 未套用時視為皆未裝備，見
	// loadPlayerEquipment 內部容錯），組出 equipmentEffects（只彙總防具與飾品，不含武器，見
	// PlayerEquipmentSnapshot.EquipmentEffects 註解）。
	equipSnap, err := h.loadPlayerEquipment(ctx, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load equipment")
		return
	}
	weaponRow, wtype := equipSnap.Weapon, equipSnap.WeaponType
	var job *JobRow
	var playerGuardTaunt bool
	if ch.JobID != nil {
		// DORPG P10：改用 getJobByIDFull——下面要讀 job.Traits 疊進 equipmentEffects。
		if j, jerr := h.getJobByIDFull(ctx, *ch.JobID); jerr == nil {
			job = &j
		}
		// 查無職業（理論上不會發生，職業被刪除）：job 維持 nil，resolvePlayerWeaponWire 保守
		// 退回 nil，前端仍有引擎寫死的 'sword' 兜底，不讓整支 API 因此 500。

		// DORPG P10（CONTRACT §3）：guardTaunt＝玩家目前職業技能裡「守護本能」（guard_taunt
		// 被動）≥1 級。查詢失敗（理論上不會發生，職業/技能表存在才走得到這裡）保守視為 false，
		// 不讓整支 bootstrap 因此 500——守護狀態只是少一個判定來源，不影響戰鬥能不能開打。
		if jobSkillsForGuard, jerr := h.listSkillsByJob(ctx, *ch.JobID); jerr == nil {
			ids := make([]string, len(jobSkillsForGuard))
			for i, s := range jobSkillsForGuard {
				ids[i] = s.ID
			}
			if levels, lerr := h.getPlayerSkillLevels(ctx, uid, ids); lerr == nil {
				playerGuardTaunt = hasGuardTauntPassive(jobSkillsForGuard, levels)
			}
		}
	}
	playerWeaponWire := resolvePlayerWeaponWire(weaponRow, wtype, job)
	// DORPG P10（CONTRACT §1「套用到玩家與同職業傭兵的戰鬥 equipmentEffects.damageTakenPct
	// （相加，仍受 −60 夾限）」）：職業特性疊加進裝備彙總的 damageTakenPct，再重新夾一次
	// （clampDamageTakenPct，見該函式註解）。job 為 nil（未選職業）時 jobTraitsDamageTakenPct
	// 吃零值 JobTraits 回 0，等同零改動。
	playerEquipBonus := equipSnap.EquipmentEffects()
	var playerJobTraits JobTraits
	if job != nil {
		playerJobTraits = job.Traits
	}
	playerEquipBonus.DamageTakenPct = clampDamageTakenPct(playerEquipBonus.DamageTakenPct + jobTraitsDamageTakenPct(playerJobTraits))
	playerEquipmentEffects := toWireEquipmentEffects(playerEquipBonus)

	// DORPG P9（CONTRACT §1、WIRE）：玩家成員的 strategyId＝auto_strategy_id；空字串（理論上
	// 不會發生，DB 欄位 NOT NULL DEFAULT 'balanced'）保守退回 DefaultStrategyID。
	playerStrategyID := ch.AutoStrategyID
	if playerStrategyID == "" {
		playerStrategyID = DefaultStrategyID
	}

	party := []wirePartyMember{{
		ID: uid, Name: displayName, Level: baseLevel,
		HP: roundInt(pbs.HPMax), HPMax: roundInt(pbs.HPMax),
		MP: roundInt(pbs.MPMax), MPMax: roundInt(pbs.MPMax),
		PortraitURL: playerPortraitURL,
		Stats:       &ActorStats{HPMax: pbs.HPMax, MPMax: pbs.MPMax, Atk: pbs.Atk, Matk: pbs.Matk, Def: pbs.Def, Mdef: pbs.Mdef},
		Rating:      &pbs.Rating,
		Weapon:      playerWeaponWire,
		JobID:       ch.JobID,

		EquipmentEffects: playerEquipmentEffects,
		StrategyID:       playerStrategyID,
		GuardTaunt:       playerGuardTaunt,
		JobTraits:        wireJobTraits{DamageTakenPct: jobTraitsDamageTakenPct(playerJobTraits)},
	}}

	// DORPG P6（CONTRACT §3.2）：隊伍成員改由 player_party（或預設小咪）建構——每位傭兵用自己
	// 腳本的 level/stats/skill_levels 算出自己的 Compute() 衍生值×倍率與自己的 CombatRating，
	// 不再沿用玩家戰力（ScaleCompanion/CompanionRating 兩支 P2 舊函式保留給 "power" 模式以外
	// 沒有腳本概念的呼叫端，這裡不再使用）。DORPG P9：每位傭兵再加上自己腳本的裝備（weapon/
	// equipmentEffects/strategyId），解析方式與 tavern.go 的展示路徑共用同一組純函式。
	members, err := h.resolvePartyForBattle(ctx, uid)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load party")
		return
	}
	// DORPG P9 N+1 修復（2026-09-19，見 presets.go loadGearCatalog 檔頭說明）：原本這裡對每位
	// 隊友各自呼叫 resolvePresetEquipmentSlots(ctx,...)、逐格打 DB。改成先收集全部隊友腳本的
	// 裝備 item_id 一次性批次載入，下面迴圈裡 resolvePresetEquipmentSlots 才不再碰 DB。
	var memberWeaponIDs, memberArmorIDs []string
	for _, m := range members {
		wIDs, aIDs := presetEquipmentIDs(m.Preset.Equipment)
		memberWeaponIDs = append(memberWeaponIDs, wIDs...)
		memberArmorIDs = append(memberArmorIDs, aIDs...)
	}
	memberWeapons, memberArmor, err := h.loadGearCatalog(ctx, dedupeNonEmpty(memberWeaponIDs), dedupeNonEmpty(memberArmorIDs))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load companion equipment")
		return
	}
	for _, m := range members {
		eqSlots := resolvePresetEquipmentSlots(m.Preset.Equipment, memberWeapons, memberArmor)
		d := computeCompanionDerived(cfg, m.Job, m.JobSkills, m.Preset.Level, m.Preset.Stats, m.Preset.SkillLevels, presetEquip(eqSlots))
		actor := presetActorStats(m.Companion, d)
		rating := presetCombatRating(d)
		jobID := m.Job.ID
		compWeaponRow, compWeaponType := presetWeaponRowAndType(eqSlots)
		strategyID := m.Preset.StrategyID
		if strategyID == "" {
			strategyID = DefaultStrategyID
		}

		// DORPG P10（CONTRACT §1/§3）：companion 版的「疊加職業特性」與「守護狀態旗標」，理由
		// 同上方玩家那份（m.Job 由 tavern.go resolvePartyForBattle 用 getJobByIDFull 查出，已經
		// 含 Traits，見該函式修改註解）。guardTaunt 用這位傭兵自己目前腳本的技能等級判斷——
		// 系統預設腳本本輪沒有任何一位投資 hk_c2，這裡仍是通用邏輯，之後腳本編輯器讓玩家自己在
		// 阿深身上點守護本能一樣會自動生效，不必回來改這段。
		compEquipBonus := presetEquipmentEffects(eqSlots)
		compEquipBonus.DamageTakenPct = clampDamageTakenPct(compEquipBonus.DamageTakenPct + jobTraitsDamageTakenPct(m.Job.Traits))
		compGuardTaunt := hasGuardTauntPassive(m.JobSkills, m.Preset.SkillLevels)

		party = append(party, wirePartyMember{
			ID: m.Companion.ID, Name: m.Companion.Name, Level: m.Preset.Level,
			HP: roundInt(actor.HPMax), HPMax: roundInt(actor.HPMax),
			MP: roundInt(actor.MPMax), MPMax: roundInt(actor.MPMax),
			PortraitURL: func() *string { u := charPortraitURL(m.Companion.PortraitID); return &u }(),
			Stats:       &actor,
			Weapon:      resolveCompanionWeaponWire(compWeaponRow, compWeaponType, m.Companion.Weapon),
			Rating:      &rating,
			JobID:       &jobID,
			Skills:      buildCompanionSkillsWire(m.JobSkills, m.Preset.SkillLevels),
			PresetName:  m.Preset.Name,

			EquipmentEffects: toWireEquipmentEffects(compEquipBonus),
			StrategyID:       strategyID,
			GuardTaunt:       compGuardTaunt,
			JobTraits:        wireJobTraits{DamageTakenPct: jobTraitsDamageTakenPct(m.Job.Traits)},
		})
	}

	// 怪物編組：先批次撈全部 monster row，AtkMultShares 用「同場所有存活怪」的 atk_mult 算份額
	// （bootstrap 當下全部存活，見契約 D2）。
	monsterIDs := make([]string, len(enc.Monsters))
	for i, em := range enc.Monsters {
		monsterIDs[i] = em.MonsterID
	}
	monsterRows, err := h.getMonstersByIDs(ctx, monsterIDs)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load monsters")
		return
	}
	atkMults := make([]float64, len(enc.Monsters))
	for i, em := range enc.Monsters {
		if mr, ok := monsterRows[em.MonsterID]; ok {
			atkMults[i] = mr.AtkMult
		}
	}
	shares := AtkMultShares(atkMults)

	// DORPG P11：這場遭遇實際使用的怪物等級 N——level_mode="player" 時＝玩家有效等級，
	// "fixed"（legacy 六場）維持 enc.MonsterLevel 原樣（零改動）。
	monsterLevel := enc.MonsterLevel
	if enc.LevelMode == "player" {
		monsterLevel = baseLevel
	}

	// DORPG P11：批次撈這場出現的怪物自己的 rank（不是 enc.Rank——舊六場的怪物也各自有
	// A~E 的 rank 欄位，rankLabel/badgeColor 對所有場次都適用，不限 scaling_mode="rank"）。
	rankIDSet := map[string]bool{}
	for _, mr := range monsterRows {
		if mr.Rank != "" {
			rankIDSet[mr.Rank] = true
		}
	}
	ranksByID, err := h.getRanksByIDs(ctx, stringSetKeys(rankIDSet))
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errRanksNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load monster ranks")
		return
	}
	// scaling_mode="rank" 這場自己的向量（用 enc.Rank 查表，理論上等於場上每隻怪的 mr.Rank，
	// 20 場強度挑戰同級同怪；用 enc.Rank 而非某一隻怪的 rank 是為了不假設槽位順序）。
	var encRankRow RankRow
	if enc.ScalingMode == "rank" && enc.Rank != nil {
		if rk, ok := ranksByID[*enc.Rank]; ok {
			encRankRow = rk
		} else {
			respondErr(w, http.StatusInternalServerError, "找不到這場遭遇的怪物強度資料")
			return
		}
	}

	enemies := make([]wireEnemy, 0, len(enc.Monsters))
	bestThreat, bestSlotRank, initialTargetID := -1, 999, ""
	for i, em := range enc.Monsters {
		mr, ok := monsterRows[em.MonsterID]
		if !ok {
			continue // 髒資料：編組指到不存在的 monster_id，略過該槽位而非整場 500
		}
		// DORPG P11：scaling_mode="rank" 優先於 cfg.BattleScaleMode——強度挑戰的怪物數值
		// 一律吃 rpg_monster_ranks 向量，不受後台「參數設定→戰鬥」的全域縮放模式影響。
		// scaling_mode="legacy"（既有六場）完全維持 P6 起的既有分派，零改動：依
		// cfg.BattleScaleMode 分派——"level"（本輪起預設）用怪物自己的 rpg_encounters.
		// monster_level 當基準（ScaleMonsterByLevel，跟玩家戰力無關）；"power"（P2 既有路徑）
		// 完全不動，仍以玩家戰力縮放；"fixed" 仍是保留列舉值，落到這裡的 default 分支等同
		// "power"（Config.Validate() 已允許選它但本輪未實作絕對值路徑）。
		var sm ScaledMonster
		switch {
		case enc.ScalingMode == "rank":
			sm = ScaleMonsterByRank(cfg, mr, encRankRow, enc.PowerScale, em.PowerScale, monsterLevel)
		case cfg.BattleScaleMode == "level":
			sm = ScaleMonsterByLevel(cfg, mr, enc.PowerScale, em.PowerScale, monsterLevel)
		default:
			sm = ScaleMonster(cfg, pbs, mr, enc.PowerScale, em.PowerScale, shares[i])
		}
		weakElements := mr.WeakElements
		if weakElements == nil {
			weakElements = []string{}
		}
		enemy := wireEnemy{
			ID: em.Slot, Name: mr.Name, Level: sm.Level,
			HP: sm.HPMax, HPMax: sm.HPMax, Slot: em.Slot, Row: enemyRowFromSlot(em.Slot), ImageURL: mr.PosterURL,
			Rank: mr.Rank, Attribute: mr.Attribute, Size: mr.Size, Race: mr.Race,
			Stats:          &ActorStats{HPMax: float64(sm.HPMax), MPMax: 0, Atk: float64(sm.Atk), Matk: float64(sm.Matk), Def: float64(sm.Def), Mdef: float64(sm.Mdef)},
			ThreatPriority: mr.Threat,
			Rating:         &sm.Rating,
			WeakElements:   weakElements,  // P5：CONTRACT §6 屬性相剋
			CanEscape:      enc.CanEscape, // DDL 只有 encounter 層級的 can_escape，套到每隻怪身上（契約 D：BOSS 場整場不能逃）
			IsSummoned:     false,         // DORPG P11：一般敵人恆為 false，召喚怪見 buildSummonPool
		}
		if rk, ok := ranksByID[mr.Rank]; ok {
			enemy.RankLabel = rk.Label
			enemy.BadgeColor = rk.BadgeColor
		}
		enemies = append(enemies, enemy)

		rank := slotOrder[em.Slot]
		if mr.Threat > bestThreat || (mr.Threat == bestThreat && rank < bestSlotRank) {
			bestThreat, bestSlotRank, initialTargetID = mr.Threat, rank, em.Slot
		}
	}

	// DORPG P11（CONTRACT §1、WIRE）：召喚池——對每隻「自己的 rank 有 summon.waves」的敵人
	// （目前只有 A/SA/S/SS），預先算好每一波要放進場的敵人。
	summonPool, err := h.buildSummonPool(ctx, cfg, enc, monsterRows, ranksByID, monsterLevel)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to build summon pool")
		return
	}

	loadout, err := h.getLoadout(ctx, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load loadout")
		return
	}
	skillSlots, err := h.buildSkillSlots(ctx, cfg, pbs, loadout, ch.JobID, uid)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load skills")
		return
	}
	itemRows, err := h.listDefaultQuantityItems(ctx)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load items")
		return
	}
	// P2 修正第 1 輪：hp/mp 道具的 amount 同技能 flat，是「參考玩家」身上的絕對值，組進 wire
	// 之前先用 ScaleItem 依這場戰鬥的實際玩家 HPMax/MPMax 縮放（revive 道具 ScaleItem 內部不動）。
	items := make([]wireItem, 0, len(itemRows))
	for _, it := range itemRows {
		items = append(items, toWireItem(ScaleItem(cfg, pbs, it)))
	}

	sample := wireBattleSample{
		Party:   party,
		Enemies: enemies,
		Scene: wireScene{
			ID: scene.ID, Name: scene.Name, ImageURL: scene.ImageURL, Slots: scene.Slots,
		},
		Skills:          skillSlots,
		Items:           items,
		InitialTargetID: initialTargetID,
		SceneKind:       enc.SceneKind,
		EscapeChance:    enc.EscapeChance,
		SummonPool:      summonPool,
	}

	// DORPG P9（WIRE）：config.aiStrategies——只含目前 is_active 的策略，供引擎 resolveStrategy()
	// 跟自己的內建預設參數 merge。
	strategies, err := h.listActiveStrategies(ctx)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load strategies")
		return
	}
	wireCfg := buildWireConfig(cfg)
	wireCfg.AiStrategies = buildAiStrategiesWire(strategies)

	respondJSON(w, http.StatusOK, map[string]any{
		"encounter": buildWireEncounterInfo(enc, scene.ImageURL, monsterLevel, ranksByID),
		"sample":    sample,
		"config":    wireCfg,
		// scalingMode/levelMode/monsterLevel DORPG P11（WIRE：「頂層新增...」，camelCase 對齊
		// wireBattleSample 的既有慣例，跟上面 encounter 那份 snake_case 是同一份資訊的兩種殼，
		// 理由同 wireEnemy.Rank／wireEncounterInfo.rank 的既有雙殼慣例）。
		"scalingMode": enc.ScalingMode, "levelMode": enc.LevelMode, "monsterLevel": monsterLevel,
		// autoBattle DORPG P9（WIRE：「頂層新增 autoBattle: boolean」）：玩家角色列的開關，戰鬥中
		// dispatch.SET_AUTO_BATTLE 本地即時生效由引擎自己處理，這裡只送 bootstrap 當下的持久化值。
		"autoBattle": ch.AutoBattle,
		// P5：free_points 不再是真相（見 handler.go buildCharacterView），現算避免新版
		// /rpg/allocate 不再遞減 DB 欄位後這裡凍結在舊數字（同 BattleEncounters 的修法）。
		"hints": map[string]any{"free_points": func() int {
			statsNow := Stats{Str: ch.Str, Agi: ch.Agi, Vit: ch.Vit, Dex: ch.Dex, Int: ch.Int, Luk: ch.Luk}
			free := TotalStatPoints(cfg, baseLevel) - TotalSpentStats(cfg, statsNow)
			if free < 0 {
				return 0
			}
			return free
		}()},
	})
}

// POST /rpg/battle/report：純遙測（D5），健全性檢查後寫 rpg_battle_logs，204 no body。
func (h *Handler) BattleReport(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var in BattleLogInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := in.sanitize(); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	n, err := h.countBattleLogsToday(ctx, uid)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if n >= maxDailyBattleLogs {
		// 契約：超過每日上限直接 204 但不寫入，log 一行——這是遙測濫用防護，不是使用者看得到
		// 的錯誤，回 204 讓前端照常往下走（不擋 UI），不回 4xx。
		log.Printf("rpg battle report: user=%s 超過每日上限(%d)，本筆不寫入", uid, maxDailyBattleLogs)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.insertBattleLog(ctx, uid, in); err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
