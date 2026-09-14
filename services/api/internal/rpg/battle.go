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
	// ⚠️ 不能加 omitempty：Go 的 omitempty 對 bool 是「等於零值(false)就省略」，會讓
	// canEscape=false（契約最在意的那個值，BOSS 場整場不能逃）被吃掉、前端收到 undefined
	// 又落回預設 true——這裡刻意每筆都明確送 true/false，比省略省一點 bytes 更重要。
	CanEscape bool `json:"canEscape"`
}

type wireBattleSample struct {
	Party           []wirePartyMember `json:"party"`
	Enemies         []wireEnemy       `json:"enemies"`
	Scene           wireScene         `json:"scene"`
	Skills          [8]*wireSkill     `json:"skills"`
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
	}
}

func toWireSkill(s SkillRow) wireSkill {
	return wireSkill{
		ID: s.ID, Name: s.Name, IconURL: kitAssetURL(s.IconID), CooldownMs: s.CooldownMs,
		Kind: s.Kind, Target: s.Target, MPCost: s.MPCost, Coefficient: s.Coefficient,
		Flat: s.Flat, Element: s.Element, Weapon: s.Weapon, CastMs: s.CastMs,
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
func (h *Handler) loadPlayerBattleStats(ctx context.Context, uid string, cfg Config) (PlayerBattleStats, character, int, error) {
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
		return PlayerBattleStats{}, character{}, 0, err
	}
	var exp int
	if err := h.db.QueryRow(ctx, `SELECT COALESCE(exp,0) FROM users WHERE id=$1`, uid).Scan(&exp); err != nil {
		return PlayerBattleStats{}, character{}, 0, err
	}
	baseLevel, err := baseLevelFromExp(ctx, h.db, exp)
	if err != nil {
		return PlayerBattleStats{}, character{}, 0, err
	}
	stats := Stats{Str: ch.Str, Agi: ch.Agi, Vit: ch.Vit, Dex: ch.Dex, Int: ch.Int, Luk: ch.Luk}
	d := Compute(cfg, ComputeInput{BaseLevel: baseLevel, JobLevel: ch.JobLevel, Stats: stats})
	return PlayerBattleStatsFrom(cfg, baseLevel, d), ch, baseLevel, nil
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
			},
			Monsters: ms,
			Stats:    wireEncounterStats{Plays: st.Plays, Wins: st.Wins, BestMs: st.BestMs, LastOutcome: st.LastOutcome},
		})
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"encounters": list,
		"character": map[string]any{
			"base_level":   baseLevel,
			"free_points":  ch.FreePoints,
			"power":        PlayerPower(pbs),
			"max_hp":       roundInt(pbs.HPMax),
			"max_mp":       roundInt(pbs.MPMax),
			"unspent_hint": ch.FreePoints > 0,
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

// buildSkillSlots 契約：「玩家 loadout 非空就照它、否則取 is_default 的技能依 sort_order 補；
// 不足補 null」——兩種來源互斥，不會混用（loadout 有值時完全不看 is_default）。
//
// cfg/pbs：P2 修正第 1 輪——DB 存的 flat 是「參考玩家」身上的絕對值，組進 wire 之前要先用
// ScaleSkill 依這場戰鬥的實際玩家 HPMax 縮放（見 scaling.go 檔頭），heal/shield 以外的技能
// ScaleSkill 會原樣回傳，這裡不需要另外判斷 kind。
func (h *Handler) buildSkillSlots(ctx context.Context, cfg Config, pbs PlayerBattleStats, loadout []string) ([8]*wireSkill, error) {
	var slots [8]*wireSkill
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
				w := toWireSkill(ScaleSkill(cfg, pbs, sr))
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
		w := toWireSkill(ScaleSkill(cfg, pbs, sr))
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

	party := []wirePartyMember{{
		ID: uid, Name: displayName, Level: baseLevel,
		HP: roundInt(pbs.HPMax), HPMax: roundInt(pbs.HPMax),
		MP: roundInt(pbs.MPMax), MPMax: roundInt(pbs.MPMax),
		PortraitURL: playerPortraitURL,
		Stats:       &ActorStats{HPMax: pbs.HPMax, MPMax: pbs.MPMax, Atk: pbs.Atk, Matk: pbs.Matk, Def: pbs.Def, Mdef: pbs.Mdef},
		Rating:      &pbs.Rating,
	}}

	companions, err := h.listPartyCompanions(ctx, 4)
	if err != nil {
		if respondIfMissingRelation(w, err) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load companions")
		return
	}
	for _, c := range companions {
		cs := ScaleCompanion(cfg, pbs, c)
		cr := CompanionRating(cfg, pbs.Rating, c)
		party = append(party, wirePartyMember{
			ID: c.ID, Name: c.Name, Level: baseLevel + c.LevelOffset,
			HP: roundInt(cs.HPMax), HPMax: roundInt(cs.HPMax),
			MP: roundInt(cs.MPMax), MPMax: roundInt(cs.MPMax),
			PortraitURL: func() *string { u := charPortraitURL(c.PortraitID); return &u }(),
			Stats:       &cs,
			Weapon:      c.Weapon,
			Rating:      &cr,
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
		// TODO(P3)：cfg.BattleScaleMode 目前只有 "power" 真的被使用；"level"/"fixed" 是
		// Config.Validate() 已經開放的列舉值，但這裡還沒有依模式切換分支（見 scaling.go 檔頭）。
		sm := ScaleMonster(cfg, pbs, mr, enc.PowerScale, em.PowerScale, shares[i])
		enemy := wireEnemy{
			ID: em.Slot, Name: mr.Name, Level: sm.Level,
			HP: sm.HPMax, HPMax: sm.HPMax, Slot: em.Slot, ImageURL: mr.PosterURL,
			Rank: mr.Rank, Attribute: mr.Attribute, Size: mr.Size, Race: mr.Race,
			Stats:          &ActorStats{HPMax: float64(sm.HPMax), MPMax: 0, Atk: float64(sm.Atk), Matk: float64(sm.Matk), Def: float64(sm.Def), Mdef: float64(sm.Mdef)},
			ThreatPriority: mr.Threat,
			Rating:         &sm.Rating,
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
	skillSlots, err := h.buildSkillSlots(ctx, cfg, pbs, loadout)
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
		},
		"sample": sample,
		"config": buildWireConfig(cfg),
		"hints":  map[string]any{"free_points": ch.FreePoints},
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
