// jobs.go：DORPG P5 職業端點——GET /rpg/jobs（六職業清單，唯讀）、PUT /rpg/job（切換/清除玩家目前
// 職業，測試階段可隨時切換，見 CONTRACT §1）。掛在既有 Handler.Router() 底下，套用既有
// requireEntry 白名單（handler.go）。
package rpg

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
)

// errJobsNotReady migration 180 尚未套用時（rpg_jobs 表不存在）的固定 503 訊息，比照
// content_repo.go errContentNotReady 的既有慣例。
const errJobsNotReady = "職業/技能系統尚未初始化（migration 180 未套用）"

// respondIfMissingRelationMsg 同 battle.go respondIfMissingRelation，但訊息可自訂——P5 新表
// （rpg_jobs/player_skill_levels）用 errJobsNotReady，跟既有 P2 內容表的 errContentNotReady
// 區分，方便維運看訊息就知道要套哪一份 migration。
func respondIfMissingRelationMsg(w http.ResponseWriter, err error, msg string) bool {
	if isMissingRelation(err) {
		respondErr(w, http.StatusServiceUnavailable, msg)
		return true
	}
	return false
}

// GET /rpg/jobs：六職業清單，唯讀、不吃任何參數。
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.listJobs(r.Context())
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errJobsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load jobs")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

type putJobRequest struct {
	JobID *string `json:"job_id"`
}

// PUT /rpg/job {"job_id": string|null}：測試階段可隨時切換／清除職業（不鎖定，CONTRACT §1）。
// 切換不影響已配的六圍點數；技能點的「目前職業已花費」計算只看新職業的技能（見 content_repo.go
// sumSkillPointsSpent），舊職業的 player_skill_levels 列原封不動保留。
func (h *Handler) PutJob(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body putJobRequest
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
	if body.JobID != nil {
		if _, err := h.getJobByID(ctx, *body.JobID); err != nil {
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
	}
	// 審查#3【低】根因修復：job_id 更新與「自動卸下跨職業武器」原本是兩次獨立的非交易寫入
	// （各自呼叫一次 h.db.Exec/QueryRow）——中途卸下那步失敗時，job_id 已經寫入 DB 卻不會回滾，
	// 留下「已經換職業、舊職業武器卻還裝備著」的不一致狀態。改成兩步都在同一個 tx 內（比照
	// StatsReset/Allocate 既有的多步驟寫入慣例：h.db.Begin → defer Rollback → 全部成功才 Commit），
	// 任何一步失敗都讓整個異動一起復原。
	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 成功路徑會先 Commit，Rollback 在那之後為 no-op

	if _, err := tx.Exec(ctx, `UPDATE player_characters SET job_id=$1, updated_at=NOW() WHERE user_id=$2`, body.JobID, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}

	// DORPG P7（CONTRACT §2/WIRE PUT /rpg/job）：換職業（含清除職業）時，若目前裝備的武器
	// 不屬於新職業（type.job_id 不符，或直接沒選職業），自動卸下——避免玩家帶著跨職業武器繼續
	// 吃它的 Compute 加成。player_equipment 讀寫都改走 tx（上面 job_id 更新同一個交易）；
	// rpg_weapons/rpg_weapon_types 是唯讀查表資料，不受這支端點寫入影響，沿用既有
	// h.getWeaponWithType 走 h.db 即可，不必也綁進這個 tx。
	var equippedItemID string
	scanErr := tx.QueryRow(ctx, `SELECT item_id FROM player_equipment WHERE user_id=$1 AND slot='weapon'`, uid).Scan(&equippedItemID)
	switch {
	case scanErr == nil:
		_, wtype, werr := h.getWeaponWithType(ctx, equippedItemID)
		switch {
		case werr == nil:
			if shouldUnequipOnJobChange(wtype.JobID, body.JobID) {
				if _, err := tx.Exec(ctx, `DELETE FROM player_equipment WHERE user_id=$1 AND slot='weapon'`, uid); err != nil {
					respondErr(w, http.StatusInternalServerError, "failed to save")
					return
				}
			}
		case errors.Is(werr, pgx.ErrNoRows):
			// 髒資料：裝備指到一把已被刪除的武器，視為不需要卸下（沒有 job_id 可比對）。
		default:
			respondErr(w, http.StatusInternalServerError, "failed to load equipment")
			return
		}
	case errors.Is(scanErr, pgx.ErrNoRows):
		// 沒有裝備武器，不需要卸下。
	case isMissingRelation(scanErr):
		// migration 183 未套用（player_equipment 表不存在）：視為「還沒有武器系統」直接略過，
		// 不擋這支既有端點。
	default:
		respondErr(w, http.StatusInternalServerError, "failed to load equipment")
		return
	}

	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to commit")
		return
	}
	h.respondMe(w, r, uid)
}

// shouldUnequipOnJobChange 純函式抽出來方便單元測試（DB 讀寫本身留給 Neon 分支整合測試，見
// jobs_test.go 檔頭）：換成 newJobID（nil＝清除職業）後，目前裝備的武器若不屬於新職業就該卸下。
func shouldUnequipOnJobChange(equippedWeaponJobID string, newJobID *string) bool {
	return newJobID == nil || equippedWeaponJobID != *newJobID
}

// PUT /rpg/test-level {"level": number|null}（1~99）：測試用等級，NULL 清除（改用真實等級）。
//
// 審查#2【中・CONFIRMED】新增：開頭先檢查 cfg.TestLevelEnabled——這個端點過去只靠 requireEntry
// 白名單擋，但白名單本來就是拿來放寬給更多人測試用的，一旦放寬，白名單內任何人都能把自己的
// 等級設成 99，沒有第二道閘門。關閉時一律 403（不論這次是要設定新值還是清除成 nil，一律擋在
// 最前面，跟審查建議的「開頭 check」一致）；既有 test_level 的「生效」與否另外由
// compute.go effectiveTestLevel()／loadEffectiveLevel() 把關（見該函式註解），不是只擋這支寫入
// 端點就夠。
func (h *Handler) PutTestLevel(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var body struct {
		Level *int `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Level != nil && (*body.Level < 1 || *body.Level > 99) {
		respondErr(w, http.StatusBadRequest, "level must be within 1..99")
		return
	}
	ctx := r.Context()
	cfg, err := h.loadConfig(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load config")
		return
	}
	if !cfg.TestLevelEnabled {
		respondErr(w, http.StatusForbidden, "test_level_disabled")
		return
	}
	if _, err := h.getOrCreateCharacter(ctx, uid, cfg); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	if _, err := h.db.Exec(ctx, `UPDATE player_characters SET test_level=$1, updated_at=NOW() WHERE user_id=$2`, body.Level, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	h.respondMe(w, r, uid)
}

// POST /rpg/stats/reset：六圍回 InitialStat，寫 6 筆 player_stat_log（cost 負值，含未變動的
// 素質——WIRE 明講「寫 player_stat_log 6 筆」，跟 admin.go AdminResetCharacter 會略過未變動
// 的素質不同，這裡照字面實作，兩支端點本來就是各自獨立的稽核語意）。free_points 欄位不再是
// 真相（CONTRACT §3），這裡不寫入——回應的 free_points 由 buildCharacterView 依新公式推導。
func (h *Handler) StatsReset(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
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

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // 成功路徑會先 Commit，Rollback 在那之後為 no-op

	var cur Stats
	if err := tx.QueryRow(ctx, `SELECT str_pt, agi_pt, vit_pt, dex_pt, int_pt, luk_pt FROM player_characters WHERE user_id=$1 FOR UPDATE`, uid).
		Scan(&cur.Str, &cur.Agi, &cur.Vit, &cur.Dex, &cur.Int, &cur.Luk); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load character")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE player_characters SET str_pt=$1, agi_pt=$1, vit_pt=$1, dex_pt=$1, int_pt=$1, luk_pt=$1, updated_at=NOW()
		WHERE user_id=$2`, cfg.InitialStat, uid); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to reset")
		return
	}
	entries := []struct {
		key  string
		from int
	}{{"str", cur.Str}, {"agi", cur.Agi}, {"vit", cur.Vit}, {"dex", cur.Dex}, {"int", cur.Int}, {"luk", cur.Luk}}
	for _, e := range entries {
		cost := -spentBetween(cfg, cfg.InitialStat, e.from) // 負值＝退點；本來就在初始值時 cost=0
		if _, err := tx.Exec(ctx, `INSERT INTO player_stat_log (user_id, stat, from_value, to_value, cost) VALUES ($1,$2,$3,$4,$5)`,
			uid, e.key, e.from, cfg.InitialStat, cost); err != nil {
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

// statFieldValue 依 statColumns 的 key（str/agi/vit/dex/int/luk）取出 Stats 對應欄位目前值。
// handler.go Allocate 用於「鎖定六圍整列後，取出這次要加點的那一項」。
func statFieldValue(s Stats, key string) int {
	switch key {
	case "str":
		return s.Str
	case "agi":
		return s.Agi
	case "vit":
		return s.Vit
	case "dex":
		return s.Dex
	case "int":
		return s.Int
	case "luk":
		return s.Luk
	}
	return 0
}
