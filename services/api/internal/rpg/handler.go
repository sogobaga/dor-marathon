// handler.go：遊戲化角色數值 HTTP 端點——會員自助 GET /rpg/me（查詢/lazy-create 角色列）、
// POST /rpg/allocate（加點）。整個 Router() 掛套件私有 requireEntry：非白名單（且非 VVIP/
// super_admin）一律 403（SEC-H5 同款：前端 UI 隱藏不等於後端有擋，比照 monopoly/gpscalib 前例）。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
)

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]any{"error": msg})
}

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// requireEntry 後端強制入口白名單（SEC-H5）：非 shown 一律 403，不論前端有沒有把按鈕藏起來。
func (h *Handler) requireEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		if uid == "" {
			respondErr(w, http.StatusUnauthorized, "login required")
			return
		}
		id, err := resolveIdentity(r.Context(), h.db, uid)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to resolve access")
			return
		}
		state := appsettings.GetString(r.Context(), h.db, EntryStateKey, "hidden")
		wl := appsettings.GetString(r.Context(), h.db, EntryWhitelistKey, "")
		if ResolveEntry(state, wl, id.IsVVIP, id.Email, id.Code, id.IsSuperAdmin) != "shown" {
			respondErr(w, http.StatusForbidden, "not available")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Router 掛 /rpg（main.go 內 Mount 路徑）。P5 新增職業／測試等級／技能端點，皆吃既有
// requireEntry（WIRE：「全部沿用 requireEntry 白名單閘門與既有 RateLimit」）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireEntry)
	r.Get("/me", h.Me)
	r.Post("/allocate", h.Allocate)
	r.Get("/jobs", h.Jobs)
	r.Put("/job", h.PutJob)
	r.Put("/test-level", h.PutTestLevel)
	r.Post("/stats/reset", h.StatsReset)
	r.Get("/skills", h.Skills)
	r.Post("/skills/allocate", h.SkillsAllocate)
	r.Post("/skills/reset", h.SkillsReset)
	// DORPG P6（CONTRACT §3.3）：酒館／隊伍／傭兵腳本，沿用同一組 requireEntry 白名單。
	r.Get("/tavern", h.Tavern)
	r.Put("/party", h.PutParty)
	r.Post("/presets", h.CreatePreset)
	r.Put("/presets/{id}", h.UpdatePreset)
	r.Delete("/presets/{id}", h.DeletePreset)
	r.Post("/presets/validate", h.PresetsValidate)
	// DORPG P7（CONTRACT §2/§5、WIRE）：武器裝備，沿用同一組 requireEntry 白名單。
	r.Get("/equipment", h.GetEquipment)
	r.Put("/equipment/weapon", h.PutEquipmentWeapon)
	// DORPG P8（CONTRACT §1/§4、WIRE）：五部位防具＋兩格飾品，"/equipment/weapon" 是靜態路徑，
	// chi 對同一層路由優先比對靜態片段再退回 {slot} 萬用字元，兩條路由可以並存不衝突。
	r.Put("/equipment/{slot}", h.PutEquipmentSlot)
	// DORPG P9（CONTRACT §2/§4/§5、WIRE）：傭兵裝備＋AI 戰鬥策略＋玩家自動戰鬥，沿用同一組
	// requireEntry 白名單；"/tavern/gear" 是 "/tavern" 底下的靜態子路徑，兩者不衝突。
	r.Get("/tavern/gear", h.TavernGear)
	r.Put("/auto-battle", h.PutAutoBattle)
	return r
}

func (h *Handler) loadConfig(ctx context.Context) (Config, error) {
	raw := appsettings.GetString(ctx, h.db, "rpg_config", "")
	return ParseConfig(raw)
}

// character 角色列（player_characters）的 DB 對應。P5 新增 UserID（skills.go/jobs.go 需要拿使用者
// id 去查職業技能樹/被動加成，不必每個呼叫端另外多帶一個參數）、JobID、TestLevel。DORPG P9
// 新增 AutoBattle/AutoStrategyID（migration 186）。
type character struct {
	UserID                       string
	Str, Agi, Vit, Dex, Int, Luk int
	FreePoints                   int
	JobLevel                     int
	JobExp                       int
	JobID                        *string
	TestLevel                    *int
	AutoBattle                   bool
	AutoStrategyID               string
}

const characterCols = `user_id, str_pt, agi_pt, vit_pt, dex_pt, int_pt, luk_pt, free_points, job_level, job_exp, job_id, test_level, auto_battle, auto_strategy_id`

func scanCharacter(row pgx.Row) (character, error) {
	var c character
	err := row.Scan(&c.UserID, &c.Str, &c.Agi, &c.Vit, &c.Dex, &c.Int, &c.Luk, &c.FreePoints, &c.JobLevel, &c.JobExp,
		&c.JobID, &c.TestLevel, &c.AutoBattle, &c.AutoStrategyID)
	return c, err
}

// getOrCreateCharacter 查角色列；查無則依 cfg 的初始值 lazy-create（D5：「Lazy-create the
// character row on first /me for allowed users」）。ON CONFLICT DO NOTHING + 重讀一次應付併發
// （兩個分頁同時第一次打開角色頁）。job_id/test_level 新建角色一律 NULL（未選職業／不使用測試
// 等級），INSERT 不需要特別指定就會吃到欄位預設值。
func (h *Handler) getOrCreateCharacter(ctx context.Context, userID string, cfg Config) (character, error) {
	row := h.db.QueryRow(ctx, `SELECT `+characterCols+` FROM player_characters WHERE user_id=$1`, userID)
	c, err := scanCharacter(row)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return character{}, err
	}
	_, err = h.db.Exec(ctx, `
		INSERT INTO player_characters (user_id, str_pt, agi_pt, vit_pt, dex_pt, int_pt, luk_pt, free_points, job_level, job_exp)
		VALUES ($1,$2,$2,$2,$2,$2,$2,$3,1,0)
		ON CONFLICT (user_id) DO NOTHING`, userID, cfg.InitialStat, cfg.InitialFreePoints)
	if err != nil {
		return character{}, err
	}
	row = h.db.QueryRow(ctx, `SELECT `+characterCols+` FROM player_characters WHERE user_id=$1`, userID)
	return scanCharacter(row)
}

// --- Wire 型別（json tag 對齊前端 apps/web/src/lib/api.ts 的 RpgMe/RpgCharacter，勿自行改名）---

type meResponse struct {
	Enabled   bool           `json:"enabled"`
	Character *characterView `json:"character,omitempty"` // enabled=false 時省略
}

type characterView struct {
	BaseLevel  int            `json:"base_level"`
	JobLevel   int            `json:"job_level"`
	JobExp     int            `json:"job_exp"`
	Stats      Stats          `json:"stats"`
	FreePoints int            `json:"free_points"` // P5：推導值，等同 stat_points_free（WIRE，不再讀 DB 欄位）
	NextCost   map[string]int `json:"next_cost"`   // 已達上限的素質不出現在這裡（Partial<RpgStats>）
	MaxHP      int            `json:"max_hp"`
	MaxMP      int            `json:"max_mp"`
	Derived    Derived        `json:"derived"`

	// --- P5 新增（WIRE：GET /rpg/me 新增欄位）---
	Job              *JobRow `json:"job"`
	TestLevel        *int    `json:"test_level"`
	EffectiveLevel   int     `json:"effective_level"`
	StatPointsTotal  int     `json:"stat_points_total"`
	StatPointsFree   int     `json:"stat_points_free"`
	StatCap          int     `json:"stat_cap"`
	SkillPointsTotal int     `json:"skill_points_total"`
	SkillPointsFree  int     `json:"skill_points_free"`

	// Weapon DORPG P7（WIRE：/rpg/me 新增欄位）：目前裝備的武器，nil＝未裝備。衍生值
	// （上面的 Derived）已經套用它的效果，這裡只是給前端顯示用。
	Weapon *weaponDTO `json:"weapon"`

	// Equipment/EquipBonus DORPG P8（WIRE：/rpg/me 新增欄位）：八格裝備各格 item 名稱或 null
	// （比 GET /rpg/equipment 的 equipped 精簡，只給名字顯示用）＋彙總後的裝備加成；衍生值
	// （上面的 Derived）已經含全部裝備（武器＋防具＋飾品）。
	Equipment  meEquipmentWire `json:"equipment"`
	EquipBonus EquipBonusDTO   `json:"equip_bonus"`

	// AutoBattle DORPG P9（WIRE：「/rpg/me 新增 auto_battle: { enabled, strategy_id }」）：唯讀
	// 顯示用（角色頁「自動戰鬥：開／關・策略」一行），實際持久化走 PUT /rpg/auto-battle。
	AutoBattle autoBattleWire `json:"auto_battle"`
}

// autoBattleWire WIRE：/rpg/me 的 auto_battle 欄位、PUT /rpg/auto-battle 的回應形狀。
type autoBattleWire struct {
	Enabled    bool   `json:"enabled"`
	StrategyID string `json:"strategy_id"`
}

// meEquipmentWire WIRE：/rpg/me 的 equipment 欄位——八格各自 item 名稱或 null（不像 GET
// /rpg/equipment 的 equipped 需要完整 DTO 給裝備畫面用，這裡只給角色頁摘要顯示）。
type meEquipmentWire struct {
	Weapon     *string `json:"weapon"`
	Helmet     *string `json:"helmet"`
	Gloves     *string `json:"gloves"`
	Armor      *string `json:"armor"`
	Legs       *string `json:"legs"`
	Boots      *string `json:"boots"`
	Accessory1 *string `json:"accessory1"`
	Accessory2 *string `json:"accessory2"`
}

// buildCharacterView 角色列 + Config + 真實 Base Level → 完整衍生數值（Me/Allocate/PutJob/
// PutTestLevel/StatsReset 共用）。P5 改為 Handler 方法：需要查目前職業（決定 WeaponType／
// atk_branch 顯示）與已配點被動技能（Compute 的 Passives 輸入），純函式時代已無法勝任。
// baseLevel 是真實等級（顯示用，回應的 base_level 欄位维持顯示真實值）；effective level（拿去
// 算配點/技能點總量與 Compute 用哪個等級）一律用 EffectiveLevel(ch.TestLevel, baseLevel) 推導
// （CONTRACT §2）。
func (h *Handler) buildCharacterView(ctx context.Context, cfg Config, baseLevel int, ch character) (characterView, error) {
	// 審查#2 CONFIRMED：test_level_enabled 關閉時，透過 effectiveTestLevel() 忽略 ch.TestLevel
	// （即使 DB 裡還留著舊值）——這裡是 /rpg/me 顯示與配點/技能點總量計算共用的唯一入口，不修
	// 這裡的話，關掉開關對已經設定過測試等級的玩家完全沒有效果（見 compute.go 函式註解）。
	effLevel := EffectiveLevel(effectiveTestLevel(cfg, ch.TestLevel), baseLevel)
	stats := Stats{Str: ch.Str, Agi: ch.Agi, Vit: ch.Vit, Dex: ch.Dex, Int: ch.Int, Luk: ch.Luk}

	var job *JobRow
	if ch.JobID != nil {
		j, err := h.getJobByID(ctx, *ch.JobID)
		switch {
		case err == nil:
			job = &j
		case errors.Is(err, pgx.ErrNoRows):
			// 職業被刪除／查無（本輪沒有職業 CRUD，理論上不會發生）——保守視為未選職業。
		default:
			return characterView{}, err
		}
	}

	in := ComputeInput{BaseLevel: effLevel, JobLevel: ch.JobLevel, Stats: stats}
	if job != nil {
		in.WeaponType = job.AtkBranch
	}
	passives, err := h.loadPassives(ctx, ch.UserID, ch.JobID)
	if err != nil {
		return characterView{}, err
	}
	in.Passives = passives

	// DORPG P7/P8：目前裝備的武器＋防具/飾品彙總後套進 Compute（CONTRACT §2/§3），nil＝完全沒有
	// 裝備＝零改動。查無資料（髒資料/物品被刪）已經在 loadPlayerEquipment 內部保守處理成
	// 「視為未裝備」（migration 183/184 未套用時同樣視為未裝備，/rpg/me 其餘部分正常運作，既有
	// 玩家角色頁不該因為武器/防具表還沒套用就整頁 500）。
	equipSnap, err := h.loadPlayerEquipment(ctx, ch.UserID)
	if err != nil {
		return characterView{}, err
	}
	in.Equip = equipSnap.Equip()

	var weaponDTOOut *weaponDTO
	if equipSnap.Weapon != nil {
		dto := toWeaponDTO(*equipSnap.Weapon, effLevel, equipSnap.Weapon.ID)
		weaponDTOOut = &dto
	}

	d := Compute(cfg, in)

	// DORPG P8（WIRE：/rpg/me equipment/equip_bonus）：八格各自 item 名稱或 null＋彙總後的裝備
	// 加成，供角色頁摘要顯示（比 GET /rpg/equipment 的完整 equipped DTO 精簡）。
	nameOf := func(row *ArmorRow) *string {
		if row == nil {
			return nil
		}
		n := row.Name
		return &n
	}
	var weaponNameOut *string
	if equipSnap.Weapon != nil {
		n := equipSnap.Weapon.Name
		weaponNameOut = &n
	}
	equipmentOut := meEquipmentWire{
		Weapon:     weaponNameOut,
		Helmet:     nameOf(equipSnap.Armor["helmet"]),
		Gloves:     nameOf(equipSnap.Armor["gloves"]),
		Armor:      nameOf(equipSnap.Armor["armor"]),
		Legs:       nameOf(equipSnap.Armor["legs"]),
		Boots:      nameOf(equipSnap.Armor["boots"]),
		Accessory1: nameOf(equipSnap.Armor["accessory1"]),
		Accessory2: nameOf(equipSnap.Armor["accessory2"]),
	}
	var equipBonusOut EquipBonusDTO
	if in.Equip != nil {
		equipBonusOut = ToEquipBonusDTO(*in.Equip)
	}

	statTotal := TotalStatPoints(cfg, effLevel)
	statFree := statTotal - d.TotalSpent
	if statFree < 0 {
		// 防呆：測試等級被調低導致「已花費的點數」超過新的總量時，不倒扣成負的可配點數
		// （UI 顯示「可配點數 0」比顯示負數更合理，且 /rpg/allocate 本來就會用 StatCap 另外
		// 擋住超額配點，這裡只影響顯示）。
		statFree = 0
	}
	statCap := StatCap(cfg, effLevel)

	skillTotal := TotalSkillPoints(cfg, effLevel)
	skillSpent, err := h.sumSkillPointsSpent(ctx, ch.UserID, ch.JobID)
	if err != nil {
		return characterView{}, err
	}
	skillFree := skillTotal - skillSpent
	if skillFree < 0 {
		skillFree = 0
	}

	return characterView{
		BaseLevel:  baseLevel,
		JobLevel:   ch.JobLevel,
		JobExp:     ch.JobExp,
		Stats:      stats,
		FreePoints: statFree,
		NextCost:   d.NextCost,
		MaxHP:      d.MaxHP,
		MaxMP:      d.MaxMP,
		Derived:    d,

		Job:              job,
		TestLevel:        ch.TestLevel,
		EffectiveLevel:   effLevel,
		StatPointsTotal:  statTotal,
		StatPointsFree:   statFree,
		StatCap:          statCap,
		SkillPointsTotal: skillTotal,
		SkillPointsFree:  skillFree,
		Weapon:           weaponDTOOut,
		Equipment:        equipmentOut,
		EquipBonus:       equipBonusOut,
		AutoBattle:       autoBattleWire{Enabled: ch.AutoBattle, StrategyID: ch.AutoStrategyID},
	}, nil
}

// GET /rpg/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	h.respondMe(w, r, uid)
}

// respondMe 查角色列（lazy-create）+ Base Level（依 users.exp 換算）→ 回完整 /me payload。
// Allocate/PutJob/PutTestLevel/StatsReset 成功後也呼叫這支，回傳格式與 GET /me 完全一致（D5）。
func (h *Handler) respondMe(w http.ResponseWriter, r *http.Request, userID string) {
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
	baseLevel, _, err := h.loadEffectiveLevel(ctx, userID, cfg, ch.TestLevel)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level config")
		return
	}
	view, err := h.buildCharacterView(ctx, cfg, baseLevel, ch)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errJobsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	respondJSON(w, http.StatusOK, meResponse{Enabled: true, Character: &view})
}

// allocateRequest points 與 mode 二選一（WIRE）：mode="max" 時 points 必須是 0（省略）；
// 否則 points 必須落在 1..10（既有行為）。
type allocateRequest struct {
	Stat   string `json:"stat"`
	Points int    `json:"points"`
	Mode   string `json:"mode"`
}

// statColumns 白名單：req.Stat 只會查出這 6 個固定欄位名之一，絕不會把使用者輸入直接拼進 SQL。
var statColumns = map[string]string{
	"str": "str_pt", "agi": "agi_pt", "vit": "vit_pt", "dex": "dex_pt", "int": "int_pt", "luk": "luk_pt",
}

// POST /rpg/allocate {"stat":"str","points":1..10} 或 {"stat":"str","mode":"max"}：依序計費
// （第 n 點成本可能不同，見 compute.go pointCost）、SELECT...FOR UPDATE 鎖六圍整列（P5：判斷
// 「這次加點花多少」需要當下全部六圍的總花費，不能只鎖被加點的那一欄）防同一使用者連點造成
// 的併發超花；點數不足或會超過 stat_cap（=min(max_stat,有效等級)，CONTRACT §3）一律拒絕，
// 成功寫 player_stat_log 稽核列。P5 起 free_points 欄位不再寫入（不再是真相，見
// buildCharacterView），可配點數改用 TotalStatPoints(cfg,effLevel)-已花費 現算。
func (h *Handler) Allocate(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req allocateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	col, ok := statColumns[req.Stat]
	if !ok {
		respondErr(w, http.StatusBadRequest, "invalid stat")
		return
	}
	maxMode := req.Mode == "max"
	if req.Mode != "" && !maxMode {
		respondErr(w, http.StatusBadRequest, `mode must be "max"`)
		return
	}
	if maxMode && req.Points != 0 {
		respondErr(w, http.StatusBadRequest, "points 與 mode 只能擇一")
		return
	}
	if !maxMode && (req.Points < 1 || req.Points > 10) {
		respondErr(w, http.StatusBadRequest, "points must be within 1..10")
		return
	}
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	// 防禦性 lazy-create：正常流程使用者一定先呼叫過 /me 才看得到配點按鈕，這裡防角色列不存在時
	// 下面的 SELECT...FOR UPDATE 直接落空。
	if _, err := h.getOrCreateCharacter(ctx, uid, cfg); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	// baseLevel 只需要 users.exp，不受這支端點的鎖影響，交易外查詢即可（審查#6 只在意
	// test_level 跟六圍要不要鎖在同一個交易內一起讀，baseLevel 本身不是併發爭用的對象）。
	baseLevel, err := h.baseLevelForUser(ctx, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level config")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 成功路徑會先 Commit，Rollback 在那之後為 no-op

	// 審查#6【極低・PLAUSIBLE】根因修復：effLevel/statCap 曾經在交易「外」用 getOrCreateCharacter
	// 讀到的 ch.TestLevel 算好，六圍卻是交易「內」FOR UPDATE 才鎖、重新讀一次——兩次讀取之間若
	// 另一個分頁同時呼叫 PUT /rpg/test-level 把等級改掉，這裡用的 statCap 就是依「舊」等級算出的
	// 過期值，跟這次真正鎖住、即將寫入的六圍不是同一個時間點的快照。修法：test_level 跟六圍改成
	// 同一個 SELECT...FOR UPDATE 一次讀出，effLevel/statCap 都在鎖之後才算。
	var cur Stats
	var testLevel *int
	if err := tx.QueryRow(ctx, `SELECT str_pt, agi_pt, vit_pt, dex_pt, int_pt, luk_pt, test_level FROM player_characters WHERE user_id=$1 FOR UPDATE`, uid).
		Scan(&cur.Str, &cur.Agi, &cur.Vit, &cur.Dex, &cur.Int, &cur.Luk, &testLevel); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	effLevel := EffectiveLevel(effectiveTestLevel(cfg, testLevel), baseLevel)
	statCap := StatCap(cfg, effLevel)
	current := statFieldValue(cur, req.Stat)
	pointsFree := TotalStatPoints(cfg, effLevel) - TotalSpentStats(cfg, cur)
	if pointsFree < 0 {
		pointsFree = 0
	}

	cost := 0
	newVal := current
	if maxMode {
		for newVal < statCap {
			c := pointCost(cfg, newVal)
			if cost+c > pointsFree {
				break
			}
			cost += c
			newVal++
		}
		// mode=max：卡在 0 點沒有變化也視為成功（伺服器盡力而為），不特別報錯。
	} else {
		for i := 0; i < req.Points; i++ {
			if newVal >= statCap {
				respondErr(w, http.StatusBadRequest, "stat_cap")
				return
			}
			c := pointCost(cfg, newVal)
			if cost+c > pointsFree {
				respondErr(w, http.StatusBadRequest, "可配點數不足")
				return
			}
			cost += c
			newVal++
		}
	}

	if newVal != current {
		updateQuery := fmt.Sprintf(`UPDATE player_characters SET %s=$1, updated_at=NOW() WHERE user_id=$2`, col)
		if _, err := tx.Exec(ctx, updateQuery, newVal, uid); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to save")
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO player_stat_log (user_id, stat, from_value, to_value, cost) VALUES ($1,$2,$3,$4,$5)`,
			uid, req.Stat, current, newVal, cost); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to log")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to commit")
		return
	}
	h.respondMe(w, r, uid)
}

// putAutoBattleRequest WIRE PUT /rpg/auto-battle body：{ enabled, strategy_id }。
type putAutoBattleRequest struct {
	Enabled    bool   `json:"enabled"`
	StrategyID string `json:"strategy_id"`
}

// PutAutoBattle DORPG P9（CONTRACT §1、WIRE）：玩家自動戰鬥開關＋策略持久化在
// player_characters；戰鬥中另有引擎本地的 dispatch SET_AUTO_BATTLE 即時生效（不透過這支端點），
// 前端在切換當下另外呼叫這裡把選擇存起來。strategy_id 空字串／未知/停用一律 unknown_strategy
// ——這支寫入端點要求呼叫端一定送一個合法值，不像腳本驗證那樣把空字串容忍成
// DefaultStrategyID（CONTRACT §1「未知 id 一律退回 balanced」是引擎執行期的容錯，不代表這支
// API 可以收爛資料）。
func (h *Handler) PutAutoBattle(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body putAutoBattleRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	if _, err := h.getOrCreateCharacter(ctx, uid, cfg); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	strategyErr, err := h.checkStrategyID(ctx, body.StrategyID)
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errStrategiesNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load strategy")
		return
	}
	if strategyErr != nil {
		respondErr(w, http.StatusBadRequest, strategyErr.Code)
		return
	}
	if _, err := h.db.Exec(ctx, `UPDATE player_characters SET auto_battle=$1, auto_strategy_id=$2, updated_at=NOW() WHERE user_id=$3`,
		body.Enabled, body.StrategyID, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	respondJSON(w, http.StatusOK, autoBattleWire{Enabled: body.Enabled, StrategyID: body.StrategyID})
}
