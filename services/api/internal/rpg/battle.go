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
}

type wireItem struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IconURL  string `json:"iconUrl"`
	Quantity int    `json:"quantity"`
	Kind     string `json:"kind"`
	Amount   int    `json:"amount"`
}

type wirePartyMember struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Level       int           `json:"level"`
	HP          int           `json:"hp"`
	HPMax       int           `json:"hpMax"`
	MP          int           `json:"mp"`
	MPMax       int           `json:"mpMax"`
	PortraitURL *string       `json:"portraitUrl"`
	Stats       *ActorStats   `json:"stats,omitempty"`
	Weapon      string        `json:"weapon,omitempty"`
	Rating      *CombatRating `json:"rating,omitempty"` // P2：命中/暴擊評級，見 scaling.go CombatRating
	JobID       *string       `json:"jobId,omitempty"`  // P5：目前職業（未選職業 omit，維持現行預設）

	// --- DORPG P6（WIRE：「party member（隊友）」新增欄位）---
	// Skills 隊友 AI 可用技能（該傭兵目前腳本 level>=1 且非 passive、implemented=true 的技能，
	// 已用 ExpandEffect 展開，見 tavern.go buildCompanionSkillsWire）。玩家（party[0]）不填這個
	// 欄位——玩家的技能欄是既有的 Skills（[10]*wireSkill，見 wireBattleSample.Skills），跟隊友
	// 這個「AI 決策用」的清單是兩回事，故意不共用同一個欄位名。
	Skills []wireSkill `json:"skills,omitempty"`
	// PresetName 這位隊友目前套用的腳本名稱（"預設"＝系統預設腳本）。玩家（party[0]）不填。
	PresetName string `json:"presetName,omitempty"`
}

type wireEnemy struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	Level          int           `json:"level"`
	HP             int           `json:"hp"`
	HPMax          int           `json:"hpMax"`
	Slot           string        `json:"slot"`
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

	// ScaleMode DORPG P6（WIRE：「config 新增 scaleMode: "level"|"power"（純顯示／除錯）」）——
	// 純粹讓前端偵錯／顯示用，引擎本身不依這個欄位分支（怪物數值在後端就已經算好送過去）。
	ScaleMode string `json:"scaleMode"`
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
	return wireSkill{
		ID: s.ID, Name: s.Name, IconURL: kitAssetURL(s.IconID), CooldownMs: s.CooldownMs,
		Kind: s.Kind, Target: s.Target,
		MPCost: roundInt(e.MPCost), Coefficient: e.Coef, Flat: roundInt(e.Flat),
		Element: s.Element, Weapon: s.Weapon, CastMs: s.CastMs,
		Level: level, MaxLevel: s.MaxLevel, DisplayText: s.DisplayText, DmgType: s.DmgType,
		Implemented: s.Implemented, Hits: e.Hits, Effect: e, Tier: s.Tier,
	}
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
	MonsterLevel int `json:"monster_level"`
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
		list = append(list, wireEncounterSummary{
			wireEncounterInfo: wireEncounterInfo{
				Code: e.Code, Title: e.Title, Subtitle: e.Subtitle, SceneID: e.SceneID,
				SceneImageURL: sc.ImageURL, SceneKind: e.SceneKind, Difficulty: e.Difficulty, CanEscape: e.CanEscape,
				MonsterLevel: e.MonsterLevel,
			},
			Monsters: ms,
			Stats:    wireEncounterStats{Plays: st.Plays, Wins: st.Wins, BestMs: st.BestMs, LastOutcome: st.LastOutcome},
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

	// 玩家武器視覺：P5 起依目前職業決定（未選職業維持現行預設——不設定這個欄位，前端沿用既有
	// fallback，見 WIRE）。查無職業（理論上不會發生，見 loadPlayerBattleStats 同樣的容錯）時
	// 一併保守視為未選職業。
	var playerWeapon string
	if ch.JobID != nil {
		if job, jerr := h.getJobByID(ctx, *ch.JobID); jerr == nil {
			playerWeapon = job.Weapon
		} else if !errors.Is(jerr, pgx.ErrNoRows) {
			respondErr(w, http.StatusInternalServerError, "failed to load job")
			return
		}
	}

	party := []wirePartyMember{{
		ID: uid, Name: displayName, Level: baseLevel,
		HP: roundInt(pbs.HPMax), HPMax: roundInt(pbs.HPMax),
		MP: roundInt(pbs.MPMax), MPMax: roundInt(pbs.MPMax),
		PortraitURL: playerPortraitURL,
		Stats:       &ActorStats{HPMax: pbs.HPMax, MPMax: pbs.MPMax, Atk: pbs.Atk, Matk: pbs.Matk, Def: pbs.Def, Mdef: pbs.Mdef},
		Rating:      &pbs.Rating,
		Weapon:      playerWeapon,
		JobID:       ch.JobID,
	}}

	// DORPG P6（CONTRACT §3.2）：隊伍成員改由 player_party（或預設小咪）建構——每位傭兵用自己
	// 腳本的 level/stats/skill_levels 算出自己的 Compute() 衍生值×倍率與自己的 CombatRating，
	// 不再沿用玩家戰力（ScaleCompanion/CompanionRating 兩支 P2 舊函式保留給 "power" 模式以外
	// 沒有腳本概念的呼叫端，這裡不再使用）。
	members, err := h.resolvePartyForBattle(ctx, uid)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load party")
		return
	}
	for _, m := range members {
		d := computeCompanionDerived(cfg, m.Job, m.JobSkills, m.Preset.Level, m.Preset.Stats, m.Preset.SkillLevels)
		actor := presetActorStats(m.Companion, d)
		rating := presetCombatRating(d)
		jobID := m.Job.ID
		party = append(party, wirePartyMember{
			ID: m.Companion.ID, Name: m.Companion.Name, Level: m.Preset.Level,
			HP: roundInt(actor.HPMax), HPMax: roundInt(actor.HPMax),
			MP: roundInt(actor.MPMax), MPMax: roundInt(actor.MPMax),
			PortraitURL: func() *string { u := charPortraitURL(m.Companion.PortraitID); return &u }(),
			Stats:       &actor,
			Weapon:      m.Companion.Weapon,
			Rating:      &rating,
			JobID:       &jobID,
			Skills:      buildCompanionSkillsWire(m.JobSkills, m.Preset.SkillLevels),
			PresetName:  m.Preset.Name,
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

	enemies := make([]wireEnemy, 0, len(enc.Monsters))
	bestThreat, bestSlotRank, initialTargetID := -1, 999, ""
	for i, em := range enc.Monsters {
		mr, ok := monsterRows[em.MonsterID]
		if !ok {
			continue // 髒資料：編組指到不存在的 monster_id，略過該槽位而非整場 500
		}
		// DORPG P6：依 cfg.BattleScaleMode 分派——"level"（本輪起預設）用怪物自己的
		// rpg_encounters.monster_level 當基準（ScaleMonsterByLevel，跟玩家戰力無關）；
		// "power"（P2 既有路徑）完全不動，仍以玩家戰力縮放；"fixed" 仍是保留列舉值，落到
		// 這裡的 default 分支等同 "power"（Config.Validate() 已允許選它但本輪未實作絕對值路徑）。
		var sm ScaledMonster
		if cfg.BattleScaleMode == "level" {
			sm = ScaleMonsterByLevel(cfg, mr, enc.PowerScale, em.PowerScale, enc.MonsterLevel)
		} else {
			sm = ScaleMonster(cfg, pbs, mr, enc.PowerScale, em.PowerScale, shares[i])
		}
		weakElements := mr.WeakElements
		if weakElements == nil {
			weakElements = []string{}
		}
		enemy := wireEnemy{
			ID: em.Slot, Name: mr.Name, Level: sm.Level,
			HP: sm.HPMax, HPMax: sm.HPMax, Slot: em.Slot, ImageURL: mr.PosterURL,
			Rank: mr.Rank, Attribute: mr.Attribute, Size: mr.Size, Race: mr.Race,
			Stats:          &ActorStats{HPMax: float64(sm.HPMax), MPMax: 0, Atk: float64(sm.Atk), Matk: float64(sm.Matk), Def: float64(sm.Def), Mdef: float64(sm.Mdef)},
			ThreatPriority: mr.Threat,
			Rating:         &sm.Rating,
			WeakElements:   weakElements,  // P5：CONTRACT §6 屬性相剋
			CanEscape:      enc.CanEscape, // DDL 只有 encounter 層級的 can_escape，套到每隻怪身上（契約 D：BOSS 場整場不能逃）
		}
		enemies = append(enemies, enemy)

		rank := slotOrder[em.Slot]
		if mr.Threat > bestThreat || (mr.Threat == bestThreat && rank < bestSlotRank) {
			bestThreat, bestSlotRank, initialTargetID = mr.Threat, rank, em.Slot
		}
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
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"encounter": wireEncounterInfo{
			Code: enc.Code, Title: enc.Title, Subtitle: enc.Subtitle, SceneID: enc.SceneID,
			SceneImageURL: scene.ImageURL, SceneKind: enc.SceneKind, Difficulty: enc.Difficulty, CanEscape: enc.CanEscape,
			MonsterLevel: enc.MonsterLevel,
		},
		"sample": sample,
		"config": buildWireConfig(cfg),
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
