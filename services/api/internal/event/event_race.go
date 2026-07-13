// Phase B：賽事多人連動事件。
// 觸發者跑步累積移動達門檻 → 依「相對觸發者」的對象規則挑同賽事報名者 → WS 邀請 →
// 限時 join → 各自達標 → 各自發獎（含每人每日上限）。全域任務閘門見 taskGateOpen。
package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/realtime"
)

// --- 對象規則型錄（前端鏡像；同類互斥、跨類交集）---

type RelOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var GroupRelOptions = []RelOption{{"any", "不限"}, {"same", "同組跑者"}, {"diff", "非同組跑者"}}
var FollowRelOptions = []RelOption{{"any", "不限"}, {"following", "觸發者追蹤的對象"}, {"follower", "追蹤觸發者的對象"}}
var GenderRelOptions = []RelOption{{"any", "不限"}, {"same", "同性別"}, {"diff", "異性別"}}

func validRel(k string, opts []RelOption) bool {
	for _, o := range opts {
		if o.Key == k {
			return true
		}
	}
	return false
}

// RaceEventDef 賽事多人事件定義
type RaceEventDef struct {
	ID                   string             `json:"id,omitempty"`
	Name                 string             `json:"name"`
	Description          string             `json:"description,omitempty"`
	Enabled              bool               `json:"enabled"`
	RaceID               string             `json:"race_id,omitempty"` // "" = 適用所有賽事
	Weight               int                `json:"weight"`
	TriggerMinM          int                `json:"trigger_min_m"`
	InitiatorCooldownSec int                `json:"initiator_cooldown_sec"`
	TargetCount          int                `json:"target_count"`
	GroupRel             string             `json:"group_rel"`
	FollowRel            string             `json:"follow_rel"`
	GenderRel            string             `json:"gender_rel"`
	JoinWindowS          int                `json:"join_window_s"`
	CompletionType       string             `json:"completion_type"`
	CompletionParams     map[string]float64 `json:"completion_params"`
	Message              string             `json:"message"`
	ImageURL             string             `json:"image_url"`       // 預設圖（時段未設定時回退）
	ImageDayURL          string             `json:"image_day_url"`   // 白天 06:00–17:00
	ImageDuskURL         string             `json:"image_dusk_url"`  // 黃昏 17:00–19:00
	ImageNightURL        string             `json:"image_night_url"` // 晚上 19:00–06:00
	RewardExp            int                `json:"reward_exp"`
	RewardDp             int                `json:"reward_dp"`
	PerUserDailyCap      int                `json:"per_user_daily_cap"`
}

const raceDefCols = `id, name, description, enabled, COALESCE(race_id::text,''), weight, trigger_min_m,
	initiator_cooldown_sec, target_count, group_rel, follow_rel, gender_rel, join_window_s,
	completion_type, completion_params, message, image_url, image_day_url, image_dusk_url, image_night_url,
	reward_exp, reward_dp, per_user_daily_cap`

func scanRaceDef(row pgx.Row) (RaceEventDef, error) {
	var d RaceEventDef
	var desc *string
	var cp []byte
	err := row.Scan(&d.ID, &d.Name, &desc, &d.Enabled, &d.RaceID, &d.Weight, &d.TriggerMinM,
		&d.InitiatorCooldownSec, &d.TargetCount, &d.GroupRel, &d.FollowRel, &d.GenderRel, &d.JoinWindowS,
		&d.CompletionType, &cp, &d.Message, &d.ImageURL, &d.ImageDayURL, &d.ImageDuskURL, &d.ImageNightURL,
		&d.RewardExp, &d.RewardDp, &d.PerUserDailyCap)
	if err != nil {
		return d, err
	}
	if desc != nil {
		d.Description = *desc
	}
	_ = json.Unmarshal(cp, &d.CompletionParams)
	if d.CompletionParams == nil {
		d.CompletionParams = map[string]float64{}
	}
	return d, nil
}

// --- Admin CRUD（掛 /admin/event-races，需 event_tasks 權限）---

func (h *Handler) RaceAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.RaceDefList)
	r.Post("/", h.RaceDefCreate)
	r.Put("/{id}", h.RaceDefUpdate)
	r.Delete("/{id}", h.RaceDefDelete)
	return r
}

func (h *Handler) RaceDefList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT `+raceDefCols+` FROM event_race_defs ORDER BY created_at DESC`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	defs := []RaceEventDef{}
	for rows.Next() {
		d, err := scanRaceDef(rows)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		defs = append(defs, d)
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"defs":               defs,
		"completion_catalog": CompletionCatalog,
		"group_rel_options":  GroupRelOptions,
		"follow_rel_options": FollowRelOptions,
		"gender_rel_options": GenderRelOptions,
	})
}

func (h *Handler) parseRaceDef(w http.ResponseWriter, r *http.Request) (*RaceEventDef, bool) {
	var d RaceEventDef
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return nil, false
	}
	if d.Name == "" || !validCompletion(d.CompletionType) ||
		!validRel(d.GroupRel, GroupRelOptions) || !validRel(d.FollowRel, FollowRelOptions) || !validRel(d.GenderRel, GenderRelOptions) {
		respondErr(w, http.StatusBadRequest, "名稱必填、完成類型與對象規則需有效")
		return nil, false
	}
	if d.Weight <= 0 {
		d.Weight = 100
	}
	if d.TriggerMinM <= 0 {
		d.TriggerMinM = 1000
	}
	if d.InitiatorCooldownSec < 0 {
		d.InitiatorCooldownSec = 0
	}
	if d.TargetCount < 0 {
		d.TargetCount = 0
	}
	if d.JoinWindowS <= 0 {
		d.JoinWindowS = 60
	}
	if d.CompletionParams == nil {
		d.CompletionParams = map[string]float64{}
	}
	return &d, true
}

func (h *Handler) RaceDefCreate(w http.ResponseWriter, r *http.Request) {
	d, ok := h.parseRaceDef(w, r)
	if !ok {
		return
	}
	cp, _ := json.Marshal(d.CompletionParams)
	out, err := scanRaceDef(h.db.QueryRow(r.Context(), `
		INSERT INTO event_race_defs (name, description, enabled, race_id, weight, trigger_min_m,
			initiator_cooldown_sec, target_count, group_rel, follow_rel, gender_rel, join_window_s,
			completion_type, completion_params, message, image_url, image_day_url, image_dusk_url, image_night_url,
			reward_exp, reward_dp, per_user_daily_cap)
		VALUES ($1,NULLIF($2,''),$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
		RETURNING `+raceDefCols,
		d.Name, d.Description, d.Enabled, d.RaceID, d.Weight, d.TriggerMinM,
		d.InitiatorCooldownSec, d.TargetCount, d.GroupRel, d.FollowRel, d.GenderRel, d.JoinWindowS,
		d.CompletionType, cp, d.Message, d.ImageURL, d.ImageDayURL, d.ImageDuskURL, d.ImageNightURL,
		d.RewardExp, d.RewardDp, d.PerUserDailyCap))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "建立失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"def": out})
}

func (h *Handler) RaceDefUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	d, ok := h.parseRaceDef(w, r)
	if !ok {
		return
	}
	cp, _ := json.Marshal(d.CompletionParams)
	out, err := scanRaceDef(h.db.QueryRow(r.Context(), `
		UPDATE event_race_defs SET name=$2, description=NULLIF($3,''), enabled=$4, race_id=NULLIF($5,'')::uuid,
			weight=$6, trigger_min_m=$7, initiator_cooldown_sec=$8, target_count=$9,
			group_rel=$10, follow_rel=$11, gender_rel=$12, join_window_s=$13,
			completion_type=$14, completion_params=$15, message=$16,
			image_url=$17, image_day_url=$18, image_dusk_url=$19, image_night_url=$20,
			reward_exp=$21, reward_dp=$22, per_user_daily_cap=$23, updated_at=NOW()
		WHERE id=$1 RETURNING `+raceDefCols,
		id, d.Name, d.Description, d.Enabled, d.RaceID, d.Weight, d.TriggerMinM,
		d.InitiatorCooldownSec, d.TargetCount, d.GroupRel, d.FollowRel, d.GenderRel, d.JoinWindowS,
		d.CompletionType, cp, d.Message, d.ImageURL, d.ImageDayURL, d.ImageDuskURL, d.ImageNightURL,
		d.RewardExp, d.RewardDp, d.PerUserDailyCap))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "更新失敗")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"def": out})
}

func (h *Handler) RaceDefDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.db.Exec(r.Context(), `DELETE FROM event_race_defs WHERE id=$1`, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "刪除失敗")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Runner（掛 /events/race，需登入）---

func (h *Handler) RaceRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/context", h.RaceContext)
	r.Post("/trigger", h.RaceTrigger)
	r.Post("/instances/{id}/join", h.RaceJoin)
	r.Post("/instances/{id}/complete", h.RaceComplete)
	r.Post("/instances/{id}/fail", h.RaceFail)
	return r
}

// GET /events/race/context — 目前登入者「進行中且已報名」的賽事（供 /track 綁定 WS 與回報里程）
func (h *Handler) RaceContext(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	rows, err := h.db.Query(r.Context(), `
		SELECT rc.id::text, rc.title FROM registrations reg JOIN races rc ON rc.id = reg.race_id
		WHERE reg.user_id=$1 AND reg.status<>'cancelled' AND NOW() BETWEEN rc.start_date AND rc.end_date
		ORDER BY rc.start_date`, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	type raceLite struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	out := []raceLite{}
	for rows.Next() {
		var rl raceLite
		if err := rows.Scan(&rl.ID, &rl.Title); err == nil {
			out = append(out, rl)
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"races": out})
}

// RunExpiryLoop 背景清理：每分鐘把「逾時仍未完成」的多人事件參與者標為 expired。
// 冪等（WHERE status='joined'）→ 多實例同時跑也安全；不發獎（逾時＝未完成）。ctx 取消即結束。
func (h *Handler) RunExpiryLoop(ctx context.Context) {
	expire := func() {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, _ = h.db.Exec(cctx, `UPDATE event_race_participants SET status='expired' WHERE status='joined' AND deadline < NOW()`)
		cancel()
	}
	expire() // 啟動時清一次(補停機期間逾時者)；之後只在近期有人加入多人賽局時才打 DB → 平時讓 Neon 休眠
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Now().Unix() > h.raceActiveUntil.Load() {
				continue // 近期無人加入 → 不碰 DB
			}
			expire()
		}
	}
}

type raceTriggerReq struct {
	RaceID   string  `json:"race_id"`
	MovedM   float64 `json:"moved_m"`
	ElapsedS int     `json:"elapsed_s"`
}

// POST /events/race/trigger — 觸發者回報里程里程碑；符合則挑對象、建實例、WS 邀請
func (h *Handler) RaceTrigger(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	var req raceTriggerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RaceID == "" {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()

	// 觸發者需為此賽事的有效報名者
	var reg bool
	if err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM registrations WHERE user_id=$1 AND race_id=$2 AND status<>'cancelled')`,
		uid, req.RaceID).Scan(&reg); err != nil || !reg {
		respondJSON(w, http.StatusOK, map[string]any{"triggered": false})
		return
	}

	// 候選定義：啟用、綁此賽事或不綁賽事、里程門檻已達、且觸發者未在該定義冷卻內
	rows, err := h.db.Query(ctx, `
		SELECT `+raceDefCols+` FROM event_race_defs d
		WHERE d.enabled AND (d.race_id IS NULL OR d.race_id=$1) AND d.trigger_min_m <= $2
		  AND NOT EXISTS (
		      SELECT 1 FROM event_race_instances i
		      WHERE i.def_id=d.id AND i.initiator_user_id=$3
		        AND i.triggered_at > NOW() - make_interval(secs => d.initiator_cooldown_sec))`,
		req.RaceID, req.MovedM, uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	var cands []RaceEventDef
	for rows.Next() {
		if d, err := scanRaceDef(rows); err == nil {
			cands = append(cands, d)
		}
	}
	rows.Close()
	if len(cands) == 0 {
		respondJSON(w, http.StatusOK, map[string]any{"triggered": false})
		return
	}
	def := pickWeighted(cands)

	// 依對象規則挑同賽事報名者（相對觸發者；同類互斥、跨類交集）＋閘門過濾
	targets, err := h.selectAudience(ctx, req.RaceID, uid, def)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if len(targets) == 0 {
		respondJSON(w, http.StatusOK, map[string]any{"triggered": false, "reason": "no_audience"})
		return
	}

	// 建實例
	joinDeadline := time.Now().Add(time.Duration(def.JoinWindowS) * time.Second)
	var instID string
	if err := h.db.QueryRow(ctx, `
		INSERT INTO event_race_instances (def_id, race_id, initiator_user_id, join_deadline, target_user_ids)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		def.ID, req.RaceID, uid, joinDeadline, targets).Scan(&instID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	var initiatorName string
	_ = h.db.QueryRow(ctx, `SELECT COALESCE(name,'跑者') FROM users WHERE id=$1`, uid).Scan(&initiatorName)

	// WS 邀請（整賽事廣播；client 依 target_user_ids 判斷是否為自己）
	hub := h.rt.GetOrCreateHub(req.RaceID)
	_ = hub.Publish(ctx, &realtime.Message{
		Type: "event_race_invite",
		Payload: map[string]any{
			"instance_id":       instID,
			"target_user_ids":   targets,
			"initiator_name":    initiatorName,
			"name":              def.Name,
			"message":           def.Message,
			"completion_type":   def.CompletionType,
			"completion_params": def.CompletionParams,
			"join_window_s":     def.JoinWindowS,
			"reward_exp":        def.RewardExp,
			"reward_dp":         def.RewardDp,
			"image_url":         def.ImageURL,
			"image_day_url":     def.ImageDayURL,
			"image_dusk_url":    def.ImageDuskURL,
			"image_night_url":   def.ImageNightURL,
			"join_deadline":     joinDeadline.UnixMilli(),
		},
	})
	respondJSON(w, http.StatusOK, map[string]any{"triggered": true, "instance_id": instID, "targets": len(targets)})
}

// pickWeighted 依 weight 加權隨機挑一個定義
func pickWeighted(defs []RaceEventDef) RaceEventDef {
	total := 0
	for _, d := range defs {
		total += d.Weight
	}
	if total <= 0 {
		return defs[rand.Intn(len(defs))]
	}
	x := rand.Intn(total)
	for _, d := range defs {
		if x < d.Weight {
			return d
		}
		x -= d.Weight
	}
	return defs[len(defs)-1]
}

// selectAudience 依對象規則挑同賽事報名者（排除觸發者、閘門開啟者），隨機取 target_count 位。
func (h *Handler) selectAudience(ctx context.Context, raceID, initiatorID string, def RaceEventDef) ([]string, error) {
	q := `
		SELECT reg.user_id::text
		FROM registrations reg
		WHERE reg.race_id=$1 AND reg.status<>'cancelled' AND reg.user_id<>$2`

	switch def.GroupRel {
	case "same":
		q += ` AND reg.group_id IS NOT DISTINCT FROM (SELECT group_id FROM registrations WHERE race_id=$1 AND user_id=$2)`
	case "diff":
		q += ` AND reg.group_id IS DISTINCT FROM (SELECT group_id FROM registrations WHERE race_id=$1 AND user_id=$2)`
	}
	switch def.FollowRel {
	case "following": // 觸發者追蹤的對象
		q += ` AND EXISTS (SELECT 1 FROM follows f WHERE f.follower_id=$2 AND f.followee_id=reg.user_id)`
	case "follower": // 追蹤觸發者的對象
		q += ` AND EXISTS (SELECT 1 FROM follows f WHERE f.follower_id=reg.user_id AND f.followee_id=$2)`
	}
	switch def.GenderRel {
	case "same":
		q += ` AND (SELECT gender FROM user_profiles WHERE user_id=reg.user_id) IS NOT DISTINCT FROM (SELECT gender FROM user_profiles WHERE user_id=$2)`
	case "diff":
		q += ` AND (SELECT gender FROM user_profiles WHERE user_id=reg.user_id) IS DISTINCT FROM (SELECT gender FROM user_profiles WHERE user_id=$2)`
	}
	// 全域閘門：近 floor 秒內無任何任務觸發/加入（與 taskGateOpen/RaceJoin 同一地板，避免發出無法接受的邀請）
	floor, _ := h.eventWaitBounds(ctx)
	q += `
		AND NOT EXISTS (SELECT 1 FROM event_task_occurrences o WHERE o.user_id=reg.user_id AND o.triggered_at > NOW()-make_interval(secs => $3))
		AND NOT EXISTS (SELECT 1 FROM event_race_participants p WHERE p.user_id=reg.user_id AND p.joined_at > NOW()-make_interval(secs => $3))
		ORDER BY random()`
	if def.TargetCount > 0 {
		q += fmt.Sprintf(" LIMIT %d", def.TargetCount)
	}

	rows, err := h.db.Query(ctx, q, raceID, initiatorID, floor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// POST /events/race/instances/{id}/join — 收邀者加入（限名單內、限時窗、閘門開啟）
func (h *Handler) RaceJoin(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	instID := chi.URLParam(r, "id")
	ctx := r.Context()

	// 讀實例 + 定義（驗證名單、時窗）
	var defID string
	var joinDeadline time.Time
	var targets []string
	var ctype string
	var cpRaw []byte
	var rexp, rdp int
	var name, message string
	err := h.db.QueryRow(ctx, `
		SELECT i.def_id::text, i.join_deadline, i.target_user_ids,
		       d.completion_type, d.completion_params, d.reward_exp, d.reward_dp, d.name, d.message
		FROM event_race_instances i JOIN event_race_defs d ON d.id=i.def_id
		WHERE i.id=$1`, instID).Scan(&defID, &joinDeadline, &targets, &ctype, &cpRaw, &rexp, &rdp, &name, &message)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "事件不存在")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if time.Now().After(joinDeadline) {
		respondJSON(w, http.StatusOK, map[string]any{"joined": false, "message": "加入時間已過"})
		return
	}
	if !contains(targets, uid) {
		respondErr(w, http.StatusForbidden, "你不在此事件邀請名單")
		return
	}
	if open, _ := h.taskGateOpen(ctx, uid); !open {
		respondJSON(w, http.StatusOK, map[string]any{"joined": false, "message": "任務冷卻中"})
		return
	}

	var params map[string]float64
	_ = json.Unmarshal(cpRaw, &params)
	limitS := params["limit_s"]
	if limitS <= 0 {
		limitS = 180
	}
	deadline := time.Now().Add(time.Duration(limitS) * time.Second)

	// 建立參與（每人每實例唯一）
	_, err = h.db.Exec(ctx, `
		INSERT INTO event_race_participants (instance_id, user_id, deadline, reward_exp, reward_dp)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (instance_id, user_id) DO NOTHING`,
		instID, uid, deadline, rexp, rdp)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	// 近期有人加入多人賽局 → 這段期間 RunExpiryLoop 才需打 DB 清死線；平時不碰 Neon、讓 compute 休眠。
	h.raceActiveUntil.Store(time.Now().Add(15 * time.Minute).Unix())
	respondJSON(w, http.StatusOK, map[string]any{
		"joined": true, "name": name, "message": message,
		"completion_type": ctype, "completion_params": params,
		"reward_exp": rexp, "reward_dp": rdp, "deadline": deadline.UnixMilli(),
	})
}

// POST /events/race/instances/{id}/complete — 驗證完成 + 發獎（冪等、每人每日上限）
func (h *Handler) RaceComplete(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	instID := chi.URLParam(r, "id")
	var req completeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()

	var partID, status, ctype, defID string
	var awarded bool
	var rexp, rdp, cap int
	var cpRaw []byte
	err := h.db.QueryRow(ctx, `
		SELECT p.id::text, p.status, p.awarded, p.reward_exp, p.reward_dp,
		       d.completion_type, d.completion_params, d.id::text, d.per_user_daily_cap
		FROM event_race_participants p
		JOIN event_race_instances i ON i.id=p.instance_id
		JOIN event_race_defs d ON d.id=i.def_id
		WHERE p.instance_id=$1 AND p.user_id=$2`, instID, uid).
		Scan(&partID, &status, &awarded, &rexp, &rdp, &ctype, &cpRaw, &defID, &cap)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "你未加入此事件")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if awarded {
		respondJSON(w, http.StatusOK, map[string]any{"completed": true, "reward_exp": rexp, "reward_dp": rdp})
		return
	}
	if status != "joined" {
		respondErr(w, http.StatusBadRequest, "此事件已結束")
		return
	}
	var params map[string]float64
	_ = json.Unmarshal(cpRaw, &params)

	// pace_shift：Phase B 無「觸發快照」可算基準，改用 client 回報的平均配速（夾範圍防極值；0 則判未達成）
	if ctype == "pace_shift" && req.BaselineSpk > 0 {
		req.BaselineSpk = clampBaselineSpk(req.BaselineSpk)
	}

	// 互動型：依完成度分級（可能 0★）+ 完美 bonus；其餘：pass/fail 全額
	var giveExp, giveDp, stars, bonusExp, bonusDp int
	if isInteraction(ctype) {
		giveExp, giveDp, stars, bonusExp, bonusDp = gradeInteraction(ctype, params, req, rexp, rdp)
	} else {
		if !validateCompletion(ctype, params, req) {
			respondJSON(w, http.StatusOK, map[string]any{"completed": false, "message": "尚未達成完成條件"})
			return
		}
		giveExp, giveDp, stars = rexp, rdp, 3
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx)

	// 每人每日上限：交易內先鎖 (user,def)、再計數，避免並發同 def 不同 instance 超發（TOCTOU）
	if cap > 0 {
		_, _ = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1||$2))`, uid, defID)
		var todayCnt int
		_ = tx.QueryRow(ctx, `
			SELECT count(*) FROM event_race_participants p JOIN event_race_instances i ON i.id=p.instance_id
			WHERE p.user_id=$1 AND i.def_id=$2 AND p.awarded AND (p.reward_exp>0 OR p.reward_dp>0) AND p.completed_at::date = CURRENT_DATE`,
			uid, defID).Scan(&todayCnt)
		if todayCnt >= cap {
			giveExp, giveDp, bonusExp, bonusDp = 0, 0, 0, 0
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE event_race_participants SET status='completed', completed_at=NOW(), awarded=TRUE,
			moved_m=$2, reward_exp=$3, reward_dp=$4
		WHERE id=$1 AND NOT awarded`, partID, req.MovedM, giveExp, giveDp)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if tag.RowsAffected() == 1 && (giveExp > 0 || giveDp > 0) {
		if _, err := tx.Exec(ctx, `UPDATE users SET exp=exp+$1, dp=dp+$2 WHERE id=$3`, giveExp, giveDp, uid); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"completed": true, "reward_exp": giveExp, "reward_dp": giveDp, "stars": stars, "bonus_exp": bonusExp, "bonus_dp": bonusDp, "capped": giveExp == 0 && rexp > 0})
}

// POST /events/race/instances/{id}/fail — 逾時/放棄
func (h *Handler) RaceFail(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	instID := chi.URLParam(r, "id")
	_, _ = h.db.Exec(r.Context(),
		`UPDATE event_race_participants SET status='failed' WHERE instance_id=$1 AND user_id=$2 AND status='joined'`, instID, uid)
	w.WriteHeader(http.StatusNoContent)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
