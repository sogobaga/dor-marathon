// Phase B1：共享累積目標（co-op / 眾志成城）。
// collective 模式的賽事多人事件：受邀者各自 join 後，靠回報移動量共同累積同一份進度（faction=空字串），
// 達標時（第一個把 current 推過 target 的請求）一次性把當下所有 joined 參與者結算為 completed 並發獎。
// individual 既有流程（event_race.go）完全不受影響。
package event

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/realtime"
)

type raceContributeReq struct {
	DeltaM float64 `json:"delta_m"`
}

// POST /events/race/instances/{id}/contribute — collective 模式：回報移動量貢獻累積目標；
// 達標時（搶到結算鎖的那次請求）一次性把全體 joined 參與者結算為 completed 並發獎。
func (h *Handler) RaceContribute(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	instID := chi.URLParam(r, "id")
	var req raceContributeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeltaM <= 0 {
		respondErr(w, http.StatusBadRequest, "invalid_delta")
		return
	}
	ctx := r.Context()

	// 讀 instance + def 快照（mode/goal_deadline/獎勵/上限）：純快速失敗用，不含 progress。
	// progress 列的權威讀取＋上鎖挪到交易內第一個動作（見下方），確保全域鎖順序一律為
	// progress→participant，與 settleCollectiveInstance（先鎖 progress 再鎖所有 participant）一致，
	// 避免 contribute 先鎖 participant、settlement 先鎖 progress 造成 deadlock。
	var mode, raceID, defID string
	var goalDeadline *time.Time
	var rexp, rdp, cap int
	err := h.db.QueryRow(ctx, `
		SELECT i.mode, i.race_id::text, i.def_id::text, i.goal_deadline,
		       d.reward_exp, d.reward_dp, d.per_user_daily_cap
		FROM event_race_instances i
		JOIN event_race_defs d ON d.id = i.def_id
		WHERE i.id = $1`, instID).
		Scan(&mode, &raceID, &defID, &goalDeadline, &rexp, &rdp, &cap)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "事件不存在")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if mode != "collective" {
		respondErr(w, http.StatusBadRequest, "not_collective")
		return
	}
	if goalDeadline == nil || time.Now().After(*goalDeadline) {
		respondErr(w, http.StatusGone, "window_closed")
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(ctx)

	// 交易內第一個動作：鎖 progress 列（權威讀取 current/target/reached_at）。
	// 同一 instance 的貢獻會因此在 progress 列上序列化——這是可接受且正確的（達標搶鎖本就要求序列化）。
	var progCurrent, target float64
	var reachedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT current, target, reached_at FROM event_race_progress
		WHERE instance_id=$1 AND faction='' FOR UPDATE`, instID).Scan(&progCurrent, &target, &reachedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusBadRequest, "not_collective") // 理論上 collective 觸發時必建；查無此列視為非法狀態
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if reachedAt != nil {
		respondErr(w, http.StatusConflict, "already_reached")
		return
	}

	// 鎖住此參與者列：必須已 join；同時取上次貢獻時間戳供防弊上限計算。
	var lastAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT last_contributed_at FROM event_race_participants
		WHERE instance_id=$1 AND user_id=$2 AND status='joined' FOR UPDATE`, instID, uid).Scan(&lastAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusForbidden, "not_joined")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// 防弊：單次上限 = min(回報值, 距上次貢獻秒數 * 7.7m/s)；無上次時戳則保守給 300m。
	capM := 300.0
	if lastAt != nil {
		if secs := time.Since(*lastAt).Seconds(); secs > 0 {
			capM = secs * 7.7
		} else {
			capM = 0
		}
	}
	delta := req.DeltaM
	if delta > capM {
		delta = capM
	}

	if _, err := tx.Exec(ctx, `
		UPDATE event_race_participants SET contributed = contributed + $3, last_contributed_at = NOW()
		WHERE instance_id=$1 AND user_id=$2`, instID, uid, delta); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// progress 累加：此時已持有該列鎖（上方 FOR UPDATE 一路持有至 commit/rollback），單純更新即可。
	var newCurrent float64
	if err := tx.QueryRow(ctx, `
		UPDATE event_race_progress SET current = current + $2
		WHERE instance_id=$1 AND faction=''
		RETURNING current`, instID, delta).Scan(&newCurrent); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// 達標搶鎖：只有第一個把 current 推過 target 的請求會搶到（reached_at IS NULL 才更新），確保只結算一次。
	reachedNow := false
	if newCurrent >= target {
		var claimed string
		err := tx.QueryRow(ctx, `
			UPDATE event_race_progress SET reached_at = NOW()
			WHERE instance_id=$1 AND faction='' AND reached_at IS NULL AND current >= target
			RETURNING instance_id::text`, instID).Scan(&claimed)
		if err == nil {
			reachedNow = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}

	if reachedNow {
		if err := h.settleCollectiveInstance(ctx, tx, instID, defID, cap); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}

	// 廣播（交易外，避免鎖持有過久）：沿用 invite 同一顆 race Hub。
	var participantsCount int
	_ = h.db.QueryRow(ctx, `SELECT count(*) FROM event_race_participants WHERE instance_id=$1`, instID).Scan(&participantsCount)
	hub := h.rt.GetOrCreateHub(raceID)
	_ = hub.Publish(ctx, &realtime.Message{
		Type: "group_goal_progress",
		Payload: map[string]any{
			"instance_id":  instID,
			"current":      newCurrent,
			"target":       target,
			"participants": participantsCount,
			"reached":      reachedNow,
		},
	})
	if reachedNow {
		_ = hub.Publish(ctx, &realtime.Message{
			Type: "group_goal_reached",
			Payload: map[string]any{
				"instance_id": instID,
				"reward_exp":  rexp,
				"reward_dp":   rdp,
			},
		})
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"current":      newCurrent,
		"target":       target,
		"reached":      reachedNow,
		"participants": participantsCount,
	})
}

// settleCollectiveInstance 達標後的全員一次性結算：把此 instance 所有「已 join 且未發獎」的參與者
// 標記 completed+awarded，並各自發獎（重用 event_race.go RaceComplete 的每人每日上限/advisory lock 模式）。
// 必須在呼叫端已持有的交易 tx 內執行，確保與達標搶鎖同一交易邊界（要嘛全員一起結算成功，要嘛整批回滾重試）。
func (h *Handler) settleCollectiveInstance(ctx context.Context, tx pgx.Tx, instID, defID string, cap int) error {
	rows, err := tx.Query(ctx, `
		SELECT user_id::text, reward_exp, reward_dp FROM event_race_participants
		WHERE instance_id=$1 AND status='joined' AND NOT awarded FOR UPDATE`, instID)
	if err != nil {
		return err
	}
	type awardee struct {
		userID    string
		rexp, rdp int
	}
	var list []awardee
	for rows.Next() {
		var a awardee
		if err := rows.Scan(&a.userID, &a.rexp, &a.rdp); err != nil {
			rows.Close()
			return err
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	for _, a := range list {
		giveExp, giveDp := a.rexp, a.rdp
		if cap > 0 {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1||$2))`, a.userID, defID); err != nil {
				return err
			}
			var todayCnt int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM event_race_participants p JOIN event_race_instances i ON i.id=p.instance_id
				WHERE p.user_id=$1 AND i.def_id=$2 AND p.awarded AND (p.reward_exp>0 OR p.reward_dp>0) AND p.completed_at::date = CURRENT_DATE`,
				a.userID, defID).Scan(&todayCnt); err != nil {
				return err
			}
			if todayCnt >= cap {
				giveExp, giveDp = 0, 0
			}
		}

		tag, err := tx.Exec(ctx, `
			UPDATE event_race_participants SET status='completed', completed_at=NOW(), awarded=TRUE,
				reward_exp=$3, reward_dp=$4
			WHERE instance_id=$1 AND user_id=$2 AND NOT awarded`, instID, a.userID, giveExp, giveDp)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 && (giveExp > 0 || giveDp > 0) {
			if _, err := tx.Exec(ctx, `UPDATE users SET exp=exp+$1, dp=dp+$2 WHERE id=$3`, giveExp, giveDp, a.userID); err != nil {
				return err
			}
		}
	}
	return nil
}
