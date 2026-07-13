// Phase B3：後端排程主動觸發 + 管理員立即發起。
// 三個來源共用 fireCollective 建立 collective 事件實例：
//  1. client 觸發（RaceTrigger，見 event_race.go，觸發者累積移動達門檻）
//  2. 管理員「立即發起」（RaceFireNow，測試/人工介入用）
//  3. 伺服器排程（RunScheduleLoop，讀 event_race_schedules 到點觸發）
//
// 後兩者無「觸發者」可相對挑選對象規則，統一以該賽事「目前在跑」名單（近 2 分鐘心跳）為對象。
package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/realtime"
)

// NoteScheduleActivity 記錄「近期有人在跑」的訊號。主要由 /track/ping 心跳呼叫（見 main.go 注入的
// onActivity callback），RaceContext/RaceJoin 亦順手呼叫作保底。RunScheduleLoop 只在此視窗內才查
// DB → 平時(無人在跑)不碰 Neon，讓 compute 休眠。
func (h *Handler) NoteScheduleActivity() {
	h.scheduleActiveUntil.Store(time.Now().Add(15 * time.Minute).Unix())
}

// fireCollective 建立一個 collective 模式的事件實例（含 event_race_progress 進度列）並 WS 廣播邀請。
// 呼叫前需已確認 def 為 collective 模式且 goalTarget>0（各呼叫端守門邏輯不同，此函式不重複判斷）。
// initiatorUserID 為 nil 代表 server/admin 發起（無實際觸發跑者）。
func (h *Handler) fireCollective(ctx context.Context, def RaceEventDef, raceID string, goalTarget float64, goalWindowS int, audienceUserIDs []string, initiatorUserID *string) (string, error) {
	if goalWindowS <= 0 {
		goalWindowS = 1800 // 未設定時保守給 30 分鐘視窗，避免 goal_deadline 立即過期
	}
	joinDeadline := time.Now().Add(time.Duration(def.JoinWindowS) * time.Second)
	goalDeadline := time.Now().Add(time.Duration(goalWindowS) * time.Second)

	var instID string
	if err := h.db.QueryRow(ctx, `
		INSERT INTO event_race_instances (def_id, race_id, initiator_user_id, join_deadline, target_user_ids, mode, goal_deadline)
		VALUES ($1,$2,$3,$4,$5,'collective',$6) RETURNING id`,
		def.ID, raceID, initiatorUserID, joinDeadline, audienceUserIDs, goalDeadline).Scan(&instID); err != nil {
		return "", err
	}
	if _, err := h.db.Exec(ctx, `
		INSERT INTO event_race_progress (instance_id, faction, current, target)
		VALUES ($1, '', 0, $2) ON CONFLICT (instance_id, faction) DO NOTHING`,
		instID, goalTarget); err != nil {
		return "", err
	}

	var initiatorName string
	if initiatorUserID != nil {
		_ = h.db.QueryRow(ctx, `SELECT COALESCE(name,'跑者') FROM users WHERE id=$1`, *initiatorUserID).Scan(&initiatorName)
	}

	// WS 邀請（整賽事廣播；client 依 target_user_ids 判斷是否為自己）—— payload 形狀與 RaceTrigger 的
	// individual 路徑一致（含 B2 的 mode/goal_target/instance_id），差別只在 mode 固定為 collective。
	hub := h.rt.GetOrCreateHub(raceID)
	_ = hub.Publish(ctx, &realtime.Message{
		Type: "event_race_invite",
		Payload: map[string]any{
			"instance_id":       instID,
			"target_user_ids":   audienceUserIDs,
			"initiator_name":    initiatorName,
			"name":              def.Name,
			"message":           def.Message,
			"mode":              "collective",
			"goal_target":       goalTarget,
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
	return instID, nil
}

// raceTrackingAudience 該賽事「目前在跑」名單（近 2 分鐘有心跳、報名未取消），隨機取 limit 位（0=不限）。
// 供管理員立即發起、伺服器排程共用（無「觸發者」可相對挑選對象規則，直接以在跑者為對象）。
func (h *Handler) raceTrackingAudience(ctx context.Context, raceID string, limit int) ([]string, error) {
	q := `
		SELECT lt.user_id::text
		FROM live_tracking lt
		JOIN registrations reg ON reg.user_id = lt.user_id AND reg.race_id = $1 AND reg.status <> 'cancelled'
		WHERE lt.last_seen > NOW() - INTERVAL '2 minutes'
		ORDER BY random()`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := h.db.Query(ctx, q, raceID)
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

// POST /admin/event-races/{id}/fire {"race_id":"..."} — 管理員立即發起一次 collective 事件
// （測試/人工介入用）。對象＝該賽事目前在跑名單；無人在跑則不建立實例（invited:0）。
func (h *Handler) RaceFireNow(w http.ResponseWriter, r *http.Request) {
	defID := chi.URLParam(r, "id")
	var body struct {
		RaceID string `json:"race_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RaceID == "" {
		respondErr(w, http.StatusBadRequest, "請提供 race_id")
		return
	}
	ctx := r.Context()

	def, err := scanRaceDef(h.db.QueryRow(ctx, `SELECT `+raceDefCols+` FROM event_race_defs WHERE id=$1`, defID))
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "事件不存在")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	var mode string
	var goalTarget float64
	var goalWindowS int
	_ = h.db.QueryRow(ctx, `SELECT mode, COALESCE(goal_target,0), COALESCE(goal_window_s,0) FROM event_race_defs WHERE id=$1`, defID).
		Scan(&mode, &goalTarget, &goalWindowS)
	if mode != "collective" || goalTarget <= 0 {
		respondErr(w, http.StatusBadRequest, "此事件非共享目標模式或未設定有效目標")
		return
	}

	audience, err := h.raceTrackingAudience(ctx, body.RaceID, def.TargetCount)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if len(audience) == 0 {
		respondJSON(w, http.StatusOK, map[string]any{"invited": 0, "message": "此賽事目前無人在跑"})
		return
	}

	instID, err := h.fireCollective(ctx, def, body.RaceID, goalTarget, goalWindowS, audience, nil)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"instance_id": instID, "invited": len(audience)})
}

// RunScheduleLoop 背景排程：到點且尚未觸發的 event_race_schedules → 依在跑名單建立 collective 事件實例。
// Neon 省睡眠：只在近期有人在跑（scheduleActiveUntil 視窗內，由 /track/ping 等心跳推進）才查 DB；
// 平時 early-continue、完全不碰 DB，讓 compute 休眠。ctx 取消即結束。
func (h *Handler) RunScheduleLoop(ctx context.Context) {
	fire := func() {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		rows, err := h.db.Query(cctx, `
			SELECT id::text, def_id::text, COALESCE(race_id::text,'')
			FROM event_race_schedules
			WHERE fired_at IS NULL AND event_at <= NOW() AND event_at > NOW() - INTERVAL '10 minutes'`)
		cancel()
		if err != nil {
			return
		}
		type sched struct{ id, defID, raceID string }
		var list []sched
		for rows.Next() {
			var s sched
			if rows.Scan(&s.id, &s.defID, &s.raceID) == nil {
				list = append(list, s)
			}
		}
		rows.Close()

		for _, s := range list {
			// 每筆排程各自包一個有限 timeout 的 child context：避免任一次 DB 呼叫卡住（鎖等待/網路分區）
			// 就讓整個 RunScheduleLoop goroutine 永久停擺、之後所有 tick 全部失效。
			func(s sched) {
				cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()

				// 單次搶發：只有搶到 fired_at 更新的那次才會實際觸發，天然防多實例重複（多台背景 loop 同時跑）。
				var claimed string
				if err := h.db.QueryRow(cctx, `
					UPDATE event_race_schedules SET fired_at=NOW() WHERE id=$1 AND fired_at IS NULL
					RETURNING id`, s.id).Scan(&claimed); err != nil {
					return // 沒搶到（已被其他實例處理）或查詢失敗
				}

				def, err := scanRaceDef(h.db.QueryRow(cctx, `SELECT `+raceDefCols+` FROM event_race_defs WHERE id=$1`, s.defID))
				if err != nil {
					return
				}
				raceID := s.raceID
				if raceID == "" {
					raceID = def.RaceID // 排程未指定賽事 → 退回 def 綁定的賽事
				}
				if raceID == "" {
					return // def 亦未綁賽事 → 無法決定在跑名單，略過
				}

				var mode string
				var goalTarget float64
				var goalWindowS int
				_ = h.db.QueryRow(cctx, `SELECT mode, COALESCE(goal_target,0), COALESCE(goal_window_s,0) FROM event_race_defs WHERE id=$1`, s.defID).
					Scan(&mode, &goalTarget, &goalWindowS)
				if mode != "collective" || goalTarget <= 0 {
					return // 排程指向的 def 非共享目標模式或未設定有效目標 → 略過
				}

				audience, err := h.raceTrackingAudience(cctx, raceID, def.TargetCount)
				if err != nil || len(audience) == 0 {
					return // 無人在跑 → 略過，不建立實例
				}
				_, _ = h.fireCollective(cctx, def, raceID, goalTarget, goalWindowS, audience, nil)
			}(s)
		}
	}

	fire() // 啟動時無條件跑一次（補停機期間到點的排程）
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Now().Unix() > h.scheduleActiveUntil.Load() {
				continue // 近期無人在跑 → 不碰 DB
			}
			fire()
		}
	}
}
