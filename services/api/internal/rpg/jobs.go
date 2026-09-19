// jobs.go：DORPG P5 職業端點——GET /rpg/jobs（六職業清單，唯讀）、PUT /rpg/job（切換/清除玩家目前
// 職業，測試階段可隨時切換，見 CONTRACT §1）。掛在既有 Handler.Router() 底下，套用既有
// requireEntry 白名單（handler.go）。
package rpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// GET /rpg/jobs：六職業清單，唯讀、不吃任何參數。DORPG P10 起用 listJobsFull（見下方）帶出
// paths/traits，前端不必另外呼叫別的端點才看得到重騎士的第三條路線。
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.listJobsFull(r.Context())
	if err != nil {
		if respondIfMissingRelationMsg(w, err, errJobsNotReady) {
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to load jobs")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// ---------------------------------------------------------------------------
// DORPG P10（CONTRACT §1/§2、WIRE）：path_c/traits 兩個新欄位不在 content_repo.go 既有的
// jobCols/scanJob 查詢範圍內（該檔屬於另一個角色的所有權，見 content.go JobRow.PathC 檔頭
// 說明）——這裡另外查一次 rpg_jobs 合併出「完整」JobRow，而不是去改那支既有函式。
// ---------------------------------------------------------------------------

// jobExtras migration 187 新增的四個欄位，查詢結果的中繼型別（不直接對外，見 mergeJobExtras）。
type jobExtras struct {
	PathC  *JobPathRow
	Traits JobTraits
}

// scanJobExtras 把一列 `path_c_id, path_c_name, path_c_desc, traits` 的掃描結果組成 jobExtras。
// pcID 為 nil＝這個職業沒有第三條路線（PathC 保持 nil，不是空字串三元組）；traitsRaw 壞掉的 JSON
// 視為沒有特性，不讓整支 API 500（比照 presets.go scanPreset 對 stats/skill_levels 的既有慣例）。
func scanJobExtras(pcID, pcName, pcDesc *string, traitsRaw []byte) jobExtras {
	var e jobExtras
	if pcID != nil {
		name, desc := "", ""
		if pcName != nil {
			name = *pcName
		}
		if pcDesc != nil {
			desc = *pcDesc
		}
		e.PathC = &JobPathRow{ID: *pcID, Name: name, Desc: desc}
	}
	if len(traitsRaw) > 0 {
		_ = json.Unmarshal(traitsRaw, &e.Traits)
	}
	return e
}

// loadJobExtras 單一職業版本（getJobByIDFull 用）。⚠️ migration 187 未套用時 path_c_id/traits
// 欄位不存在，這裡會拿到 42703（欄位不存在，不是 42P01 缺表）——沿用既有的
// respondIfMissingRelationMsg 系列接不住這個錯碼，這是刻意的：比照 migration 180 檔頭「既有
// 表格加欄位」的部署順序慣例（db-before-code-push.md），使用者必須先套用 187 才能上線這輪
// 程式碼，不是這裡要優雅降級 503 的情境。
func (h *Handler) loadJobExtras(ctx context.Context, id string) (jobExtras, error) {
	var pcID, pcName, pcDesc *string
	var traitsRaw []byte
	err := h.db.QueryRow(ctx, `SELECT path_c_id, path_c_name, path_c_desc, traits FROM rpg_jobs WHERE id=$1`, id).
		Scan(&pcID, &pcName, &pcDesc, &traitsRaw)
	if err != nil {
		return jobExtras{}, err
	}
	return scanJobExtras(pcID, pcName, pcDesc, traitsRaw), nil
}

// loadAllJobExtras listJobsFull 用：一次查全部六職業，比逐筆呼叫 loadJobExtras 少 5 次往返。
func (h *Handler) loadAllJobExtras(ctx context.Context) (map[string]jobExtras, error) {
	rows, err := h.db.Query(ctx, `SELECT id, path_c_id, path_c_name, path_c_desc, traits FROM rpg_jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]jobExtras{}
	for rows.Next() {
		var id string
		var pcID, pcName, pcDesc *string
		var traitsRaw []byte
		if err := rows.Scan(&id, &pcID, &pcName, &pcDesc, &traitsRaw); err != nil {
			return nil, err
		}
		out[id] = scanJobExtras(pcID, pcName, pcDesc, traitsRaw)
	}
	return out, rows.Err()
}

// buildJobPaths DORPG P10（WIRE JobDTO.paths）：從 PathA/PathB/PathC 組出依 key 排序（a→b→c）
// 的路線陣列——PathA/PathB 一律存在，PathC 為 nil 時（除 heavy_knight 外的五個職業）切片只有
// 兩個元素。
func buildJobPaths(j JobRow) []JobPathKeyRow {
	paths := []JobPathKeyRow{
		{ID: j.PathA.ID, Key: "a", Name: j.PathA.Name, Desc: j.PathA.Desc},
		{ID: j.PathB.ID, Key: "b", Name: j.PathB.Name, Desc: j.PathB.Desc},
	}
	if j.PathC != nil {
		paths = append(paths, JobPathKeyRow{ID: j.PathC.ID, Key: "c", Name: j.PathC.Name, Desc: j.PathC.Desc})
	}
	return paths
}

// mergeJobExtras 把 content_repo.go scanJob() 查出的基本 JobRow 與上面查到的 P10 新欄位合併，
// 順便算出 Paths。
func mergeJobExtras(j JobRow, e jobExtras) JobRow {
	j.PathC = e.PathC
	j.Traits = e.Traits
	j.Paths = buildJobPaths(j)
	return j
}

// getJobByIDFull／listJobsFull DORPG P10：對外回傳 JobDTO 的唯一入口（/rpg/me、/rpg/jobs、
// /rpg/tavern、/rpg/skills、後台）——一律用這兩支取代 content_repo.go 的 getJobByID／listJobs，
// 確保 paths/traits 不會在某些端點缺席、某些端點才有。純粹「查詢職業本身、且不外洩 JobDTO」的
// 內部用途（例如 loadPlayerBattleStats 只要 job.AtkBranch、jobAndSkillsForCompanion 只要
// job.ID/AtkBranch 拿去驗證裝備/算 Compute）不需要改用這兩支，繼續用原本的 getJobByID／listJobs
// 省一次查詢即可。
func (h *Handler) getJobByIDFull(ctx context.Context, id string) (JobRow, error) {
	j, err := h.getJobByID(ctx, id)
	if err != nil {
		return JobRow{}, err
	}
	e, err := h.loadJobExtras(ctx, id)
	if err != nil {
		return JobRow{}, err
	}
	return mergeJobExtras(j, e), nil
}

func (h *Handler) listJobsFull(ctx context.Context) ([]JobRow, error) {
	jobs, err := h.listJobs(ctx)
	if err != nil {
		return nil, err
	}
	extras, err := h.loadAllJobExtras(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]JobRow, len(jobs))
	for i, j := range jobs {
		out[i] = mergeJobExtras(j, extras[j.ID])
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// DORPG P10（CONTRACT §1/§5、WIRE）：後台 PUT /admin/rpg/jobs——六職業本身仍是固定 6 筆
// （migration 180 seed），本輪只開放編輯既有列（新增 path_c_*/traits 兩組欄位，也一併開放
// P5 時代就有、當時「本輪沒有後台 CRUD」而從沒開放編輯過的 name/tagline/description/path_a/
// path_b/weapon/atk_branch/recommended_stats/sort_order），不做新增/刪除——路由掛載
// （main.go Mount "/admin/rpg/jobs"）不在本輪 BACKEND 所有權內，由 INTEGRATOR 補上一行
// r.With(perm("rpg")).Mount("/admin/rpg/jobs", rpgHandler.AdminJobsRouter())，比照既有
// AdminMonstersRouter 等一模一樣的掛法。
// ---------------------------------------------------------------------------

// adminJobPutRequest PUT /admin/rpg/jobs body——形狀對齊 JobRow（GET 那支回的同一個 DTO），
// PathC 用同一個 JobPathRow 結構，ID 空字串＝清除這條路線（目前只有 heavy_knight 會填非空值）。
type adminJobPutRequest struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Tagline          string     `json:"tagline"`
	Description      string     `json:"description"`
	PathA            JobPathRow `json:"path_a"`
	PathB            JobPathRow `json:"path_b"`
	PathC            JobPathRow `json:"path_c"`
	Weapon           string     `json:"weapon"`
	AtkBranch        string     `json:"atk_branch"`
	RecommendedStats string     `json:"recommended_stats"`
	SortOrder        int        `json:"sort_order"`
	Traits           JobTraits  `json:"traits"`
}

// validate 比照既有 XxxRow.Validate() 的寬鬆風格（content.go 檔頭）：只擋會讓查詢/前端渲染壞掉
// 的欄位，atk_branch 沿用既有慣例不設值域（P5～P9 從未對這欄位做值域檢查，Compute() 收到未知字串
// 時自行退回預設 WeaponType，不會壞）。
func (b adminJobPutRequest) validate() error {
	if b.ID == "" || b.Name == "" {
		return fmt.Errorf("id/name 不可為空")
	}
	if !validWeaponKinds[b.Weapon] {
		return fmt.Errorf("weapon 不合法")
	}
	return b.Traits.Validate()
}

// upsertJobRow 只 UPDATE（不 INSERT）：六職業的 id 集合固定，migration 180 已經 seed 過，後台
// PUT 一個不存在的 id 應該當成「找不到」而不是靜靜長出一筆新職業（AdminPutJob 依 RowsAffected
// 判斷）。
func (h *Handler) upsertJobRow(ctx context.Context, b adminJobPutRequest) (bool, error) {
	traitsRaw, err := json.Marshal(b.Traits)
	if err != nil {
		return false, err
	}
	var pathCID, pathCName, pathCDesc *string
	if b.PathC.ID != "" {
		pathCID, pathCName, pathCDesc = &b.PathC.ID, &b.PathC.Name, &b.PathC.Desc
	}
	ct, err := h.db.Exec(ctx, `
		UPDATE rpg_jobs SET
			name=$2, tagline=$3, description=$4,
			path_a_id=$5, path_a_name=$6, path_a_desc=$7,
			path_b_id=$8, path_b_name=$9, path_b_desc=$10,
			path_c_id=$11, path_c_name=$12, path_c_desc=$13,
			weapon=$14, atk_branch=$15, recommended_stats=$16, sort_order=$17,
			traits=$18, updated_at=NOW()
		WHERE id=$1`,
		b.ID, b.Name, b.Tagline, b.Description,
		b.PathA.ID, b.PathA.Name, b.PathA.Desc,
		b.PathB.ID, b.PathB.Name, b.PathB.Desc,
		pathCID, pathCName, pathCDesc,
		b.Weapon, b.AtkBranch, b.RecommendedStats, b.SortOrder,
		traitsRaw)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
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
	// 根因修復：原本這裡把角色列丟掉，後面判斷「要不要清五部位防具」時沒有舊 job_id 可比對，
	// 只能無條件清——導致玩家送出「跟目前職業相同」的 PUT（前端沒擋、或直接打 API）也會被清空
	// 防具。保留 ch，下面用 ch.JobID（舊）跟 body.JobID（新）比對是否真的換了職業。
	ch, err := h.getOrCreateCharacter(ctx, uid, cfg)
	if err != nil {
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

	// DORPG P8（CONTRACT §1/WIRE PUT /rpg/job）：換職業（含清除職業）時卸下五部位防具
	// （armorSlotsClearedOnJobChange，飾品 accessory1/accessory2 刻意不在裡面、保留）——防具的
	// 裝備規則本來就是「job_id＝目前職業」，換職業後舊職業的防具必定不再合法，不像武器需要先查
	// 目前裝備的 type.job_id 才能判斷是否要卸（見上方 shouldUnequipOnJobChange）。
	//
	// 根因修復【CONFIRMED】：原本這段不管 body.JobID 是否與目前 job_id 相同都無條件執行——玩家
	// 對「目前職業」重複送出同一個 PUT（例如前端沒擋、或使用者直接打 API）就會把五件防具清空，
	// 但那根本不是換職業，是誤觸的空操作，不該有副作用。改成只有「舊 job_id 與新 job_id 實際
	// 不同」（shouldClearArmorOnJobChange，含舊為 nil/新有值、或反過來）才清；同一職業一律略過，
	// 沒有裝備的格子刪 0 列本來就視為成功（冪等），不受這次修法影響。migration 184 未套用時
	// player_equipment 表仍存在（183 就建了），這幾個 slot 值本來就不可能出現在舊約束下，DELETE
	// 天生是 no-op，不需要額外判斷 isMissingRelation。slot 清單用 = ANY($2) 參數化（pgx 原生支援
	// []string 對應 Postgres text[]），跟下面單元測試共用同一份 slice，不必各自維護一份 SQL 字面值。
	oldJobID := ""
	if ch.JobID != nil {
		oldJobID = *ch.JobID
	}
	newJobID := ""
	if body.JobID != nil {
		newJobID = *body.JobID
	}
	if shouldClearArmorOnJobChange(oldJobID, newJobID) {
		if _, err := tx.Exec(ctx, `DELETE FROM player_equipment WHERE user_id=$1 AND slot = ANY($2)`, uid, armorSlotsClearedOnJobChange); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to save")
			return
		}
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

// shouldClearArmorOnJobChange 純函式抽出來方便單元測試（同上 shouldUnequipOnJobChange）：
// oldJob/newJob 用空字串代表「沒有職業」（PutJob 呼叫端把 *string 攤平成字串，nil→""），只要
// 兩者不同（含一邊是「沒有職業」）就該清五部位防具；同一個非空職業維持原樣。
func shouldClearArmorOnJobChange(oldJob, newJob string) bool {
	return oldJob != newJob
}

// armorSlotsClearedOnJobChange DORPG P8（CONTRACT §1「換職業時自動卸下五部位防具，飾品保留」）：
// PutJob 換職業時無條件卸下的格子——accessory1/accessory2 刻意不在這裡，任何人漏改都會被
// TestArmorSlotsClearedOnJobChange_ExcludesAccessoriesAndWeapon 抓到。
var armorSlotsClearedOnJobChange = []string{"helmet", "gloves", "armor", "legs", "boots"}

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
