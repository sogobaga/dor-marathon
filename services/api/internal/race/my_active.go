// GET /api/v1/races/my-active — GPS 跑步追蹤頁「進行中活動/賽事」面板資料來源。
// 列出「這一筆 GPS 跑步、現在跑會被計入」的賽事/挑戰清單 + 使用者在各自的進度，
// 選取規則必須與 progress.go 的 LoadRaceActivities 完全一致（見該檔案註解）：
//   - 非 personal：reg.status<>'cancelled'、賽事已核准、且 NOW() 落在 [start_date, end_date] 賽事期間內。
//   - personal：reg.status='paid' AND challenge_started_at IS NOT NULL（進行中 attempt，唯一約束保證
//     同一使用者同一賽事至多一筆）、賽事已核准、且 NOW() <= end_date（「隨報隨進行」，無 start_date 起跑限制）。
package race

import (
	"context"
	"net/http"
	"time"

	"github.com/dor/api/internal/auth"
)

// MyActiveRace 單一「現在跑步會被計入」的賽事/挑戰 + 使用者在此賽事的相關進度。
type MyActiveRace struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	EventMode    string    `json:"event_mode"`
	StartDate    time.Time `json:"start_date"`
	EndDate      time.Time `json:"end_date"`
	MyTotalKm    float64   `json:"my_total_km"`
	MyActivities int       `json:"my_activities"`
	TasksDone    int       `json:"tasks_done"`  // 非 personal 適用；personal 恆為 0
	TasksTotal   int       `json:"tasks_total"` // 非 personal 適用；personal 恆為 0

	ChallengeRule     *ChallengeRule     `json:"challenge_rule,omitempty"`     // personal 專用；其餘 nil
	ChallengeProgress *ChallengeProgress `json:"challenge_progress,omitempty"` // personal 專用；其餘 nil
	AttemptNo         int                `json:"attempt_no,omitempty"`        // personal 專用（reg.attempt_no）
}

// activeRegRow 目前使用者「現在跑步會被計入」的候選賽事報名列（精簡欄位，供逐場組裝 MyActiveRace）。
type activeRegRow struct {
	RaceID             string
	ChallengeStartedAt *time.Time
	AttemptNo          int
}

// loadMyActiveRegistrations 選取規則見檔頭註解，WHERE/JOIN 條件與 LoadRaceActivities 的賽事選取分流
// 完全一致（此處多 scope 到單一使用者、且用 NOW() 而非活動時間戳判斷「現在」是否落在計入窗內）。
func (r *Repository) loadMyActiveRegistrations(ctx context.Context, userID string) ([]activeRegRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT rc.id::text, reg.challenge_started_at, COALESCE(reg.attempt_no,0)
		FROM races rc
		JOIN registrations reg ON reg.race_id = rc.id AND reg.user_id = $1
		     AND ( (rc.event_mode <> 'personal' AND reg.status <> 'cancelled' AND NOW() BETWEEN rc.start_date AND rc.end_date)
		        OR (rc.event_mode = 'personal' AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL AND NOW() <= rc.end_date) )
		WHERE rc.review_status = 'approved'
		ORDER BY rc.start_date DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []activeRegRow{}
	for rows.Next() {
		var row activeRegRow
		if err := rows.Scan(&row.RaceID, &row.ChallengeStartedAt, &row.AttemptNo); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// myRaceTotals 使用者在此賽事里程窗內的累積里程/筆數。窗口 gate 與 loadUserDailyActivities／
// loadUserRangeActivities 完全一致（personal 從 challenge_started_at 起算、external_data gate、排除
// flagged、<= end_date），只是把逐筆改成 SUM/COUNT，確保跟「我的里程」對得起來。
func (r *Repository) myRaceTotals(ctx context.Context, raceID, userID string) (totalKm float64, count int, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(a.distance_km),0), COUNT(*)
		FROM races rc
		LEFT JOIN registrations reg ON reg.race_id = rc.id AND reg.user_id = $2
		     AND rc.event_mode = 'personal' AND reg.status = 'paid' AND reg.challenge_started_at IS NOT NULL
		JOIN activities a ON a.user_id=$2 AND NOT a.flagged
		                  AND a.recorded_at <= rc.end_date
		                  AND a.recorded_at >= CASE WHEN rc.event_mode = 'personal'
		                                            THEN reg.challenge_started_at ELSE rc.start_date END
		                  AND (a.source IS NULL OR (rc.external_data AND a.source <> 'strava'))
		WHERE rc.id=$1`, raceID, userID).Scan(&totalKm, &count)
	return
}

// GetMyActiveRaces 組裝登入者「進行中活動/賽事」清單（GPS 追蹤頁面板用）。personal 只呼叫純讀取評估的
// evaluateChallengeRule，絕不呼叫 MarkAttemptCompletedAndGrant／GetPersonalProgress，不觸發完成結算/發獎。
// 非 personal 則直接呼叫 GetRaceProgress——與賽事詳情頁進度輪詢是同一顆函式，migration 134 起若使用者剛好
// 在此完成某個「個人額外挑戰」(group_individual scope 任務)，可能連帶觸發該任務的即時獎勵 CAS
// （見 progress.go MarkRaceTaskCompletedAndGrant）；因為呼叫時已帶入本函式呼叫者自己的 userID
// （逐場組裝、per-user 路徑，非跨使用者快取結果），不會有誤觸他人帳號的風險。此清單本身仍不回傳
// newly_granted（該欄位只在賽事詳情頁的 progress 輪詢消費，此處無對應前端彈窗），中獎與否不影響本清單內容。
func (s *Service) GetMyActiveRaces(ctx context.Context, userID string) ([]MyActiveRace, error) {
	regRows, err := s.repo.loadMyActiveRegistrations(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []MyActiveRace{}
	for _, rr := range regRows {
		race, err := s.repo.GetByID(ctx, rr.RaceID)
		if err != nil {
			return nil, err
		}
		if race == nil { // 極端競態（查詢後被刪）：略過這場，不中斷整批
			continue
		}

		totalKm, count, err := s.repo.myRaceTotals(ctx, rr.RaceID, userID)
		if err != nil {
			return nil, err
		}

		item := MyActiveRace{
			ID:           race.ID,
			Slug:         race.Slug,
			Title:        race.Title,
			EventMode:    race.EventMode,
			StartDate:    race.StartDate,
			EndDate:      race.EndDate,
			MyTotalKm:    round2(totalKm),
			MyActivities: count,
		}

		if race.EventMode == "personal" {
			item.ChallengeRule = race.ChallengeRule
			item.AttemptNo = rr.AttemptNo
			if race.ChallengeRule != nil && rr.ChallengeStartedAt != nil {
				_, progress, err := s.evaluateChallengeRule(ctx, race.ChallengeRule, userID, *rr.ChallengeStartedAt, race.ExternalData)
				if err != nil {
					return nil, err
				}
				item.ChallengeProgress = progress
			}
		} else {
			prog, err := s.GetRaceProgress(ctx, rr.RaceID, userID)
			if err != nil {
				return nil, err
			}
			item.TasksTotal = len(prog.Tasks)
			for _, t := range prog.Tasks {
				if t.Done {
					item.TasksDone++
				}
			}
		}

		out = append(out, item)
	}
	return out, nil
}

// GET /api/v1/races/my-active — 登入者「進行中活動/賽事」整合清單（需登入）
func (h *Handler) MyActiveRaces(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	races, err := h.svc.GetMyActiveRaces(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get active races")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"races": races})
}
