package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/integration"
)

// gps_recall.go — 「已入帳異常活動回收」（owner 2026-09-03 決策）：在此之前系統完全沒有反向路徑——
// 一筆活動只要 exp_awarded=TRUE，total_km/exp/dp 就永久留在使用者身上，AdminRejectGPS 也只能動
// 「flagged=TRUE AND reviewed_at IS NULL」的待審列（見 gps_admin.go reviewGPS），對已核發完畢的
// 異常活動完全無能為力。本檔案新增：
//   1. GET  /admin/gps-runs/recent  — 依時間窗＋關鍵字列出（含解析出的對應 activity_id/exp_awarded）
//   2. POST /admin/gps-runs/{id}/recall — 標異常（保留列、不刪除）＋扣回已核發的 total_km/exp/dp＋
//      回傳唯讀提醒清單（稱號/推薦獎勵/報名完成/賽事排名/每日任務，皆不自動處理）
//
// gps_runs ↔ activities 的配對慣例（兩表無 FK，只能靠慣例關聯，見 SaveGPSRun/ActivityEvent）：
// 同一 user、activities.source IS NULL、activities.recorded_at == gps_runs.ended_at（GPS 活動的
// recorded_at 存的就是結束時間，兩者 duration_s 也相同）。找不到精確時間匹配時退而求其次比對
// duration_s 相同且 recorded_at 落在 ±5 分鐘內。

const (
	defaultRecallReason = "admin_anomaly"
	maxRecallReasonLen  = 40
)

var (
	// ErrGPSRunNotFound：回收目標 gps_runs 列不存在。
	ErrGPSRunNotFound = errors.New("找不到此筆 GPS 紀錄")
	// ErrRecallReasonBenign：reason 誤用良性重複標記——那類重複另有 dedup/差額補償流程處理
	// （見 worker/main.go、internal/integration/mileage_exp.go 的 benignFlagReasons），混用回收
	// 會讓 awardMileageDedup/AwardMileageExp 的 flagged 政策誤判。
	ErrRecallReasonBenign = errors.New("reason 不可為良性重複標記（multi_device_duplicate/cross_source_duplicate/duplicate），該類重複另有 dedup 流程處理")
	// ErrRecallReasonTooLong：activities.flag_reason 是 VARCHAR(40)（見 migrations/015_activity_dedup.sql）。
	ErrRecallReasonTooLong = fmt.Errorf("reason 長度不可超過 %d 字元", maxRecallReasonLen)
)

// ValidateRecallReason 驗證並正規化管理者輸入的回收原因：trim、空值帶預設值、長度上限（對齊
// activities.flag_reason VARCHAR(40)）、不可為良性標記。純函式，不碰 DB，方便單元測試
// （見 gps_recall_test.go）。
func ValidateRecallReason(raw string) (string, error) {
	reason := strings.TrimSpace(raw)
	if reason == "" {
		reason = defaultRecallReason
	}
	if utf8.RuneCountInString(reason) > maxRecallReasonLen {
		return "", ErrRecallReasonTooLong
	}
	if integration.IsBenignFlagReason(reason) {
		return "", ErrRecallReasonBenign
	}
	return reason, nil
}

// RecallRequest 對應 POST .../recall body（已驗證過的 reason）。
type RecallRequest struct {
	Reason             string
	ValidDistanceKm    *float64 // 稽核用途；有值時一併改寫 activities/gps_runs 的距離
	ActivityIDOverride *string  // 覆蓋「依慣例解析」的對應活動
}

// RecallReversed 本次回收實際扣回的金額。
type RecallReversed struct {
	TotalKm float64 `json:"total_km"`
	Exp     int     `json:"exp"`
	Dp      int     `json:"dp"`
	KmAdded int     `json:"km_added"`
}

// RecallResult POST .../recall 的回應。
type RecallResult struct {
	RunID           string         `json:"run_id"`
	ActivityID      *string        `json:"activity_id"`
	AlreadyRecalled bool           `json:"already_recalled"`
	Reversed        RecallReversed `json:"reversed"`
	Reason          string         `json:"reason"`
	Followups       []string       `json:"followups"`
}

// recallFollowupFacts 回收流程蒐集到的唯讀事實，跟訊息文字組裝（buildRecallFollowups）分開，
// 方便對訊息格式做不碰 DB 的單元測試。
type recallFollowupFacts struct {
	HasActivity          bool
	RaceID               string   // 空字串＝非賽事活動
	OverlapCount         int      // 與其他已發放活動時間重疊數（total_km 回收金額可能不精確的警訊，見下方 RecallGPSRun 註解）
	TitleCodes           []string // 活動當天解鎖的稱號
	ReferredRewarded     bool     // 當天以「被推薦人」身分觸發推薦獎勵
	ReferrerRewarded     bool     // 當天以「推薦人」身分觸發推薦獎勵
	CompletedRaceIDs     []string // 當天 status='completed' 的報名（race_id 清單）
	MissionCompletionIDs []string // 引用此活動的每日任務完成紀錄
}

// buildRecallFollowups 純函式：把蒐集到的事實組成人類可讀的提醒清單。全部只是「唯讀提醒」，
// 不代表已自動處理——稱號/推薦獎勵/報名完成/賽事排名/每日任務/SP 都不是本次回收的職責範圍
// （見 D2 規格），管理者需要另行判斷是否要人工複核或另案處理。
func buildRecallFollowups(f recallFollowupFacts) []string {
	out := []string{}
	if !f.HasActivity {
		out = append(out, "此筆 GPS 紀錄未對應到任何已入帳活動（可能上傳當下已被標記），無金額可收回，僅標記軌跡異常")
		return out
	}
	if f.RaceID != "" {
		out = append(out, "此活動屬於賽事 race_id="+f.RaceID+"，分組排名/完賽統計要等下次 worker 重算（或手動觸發）才會反映回收後的結果")
	}
	if f.OverlapCount > 0 {
		out = append(out, fmt.Sprintf("此活動與 %d 筆其他已發放活動時間重疊，total_km 回收金額以整筆活動距離估算；若當初是「差額補償」入帳，實際入帳金額可能較小，請人工複核", f.OverlapCount))
	}
	for _, code := range f.TitleCodes {
		out = append(out, "使用者當天解鎖稱號 "+code+"，未自動收回，如需撤銷請至稱號後台手動處理")
	}
	if f.ReferredRewarded {
		out = append(out, "使用者當天以「被推薦人」身分觸發推薦獎勵（VIP 天數已發放），未自動收回")
	}
	if f.ReferrerRewarded {
		out = append(out, "使用者當天以「推薦人」身分觸發推薦獎勵（VIP 天數已發給對方），未自動收回")
	}
	for _, rid := range f.CompletedRaceIDs {
		out = append(out, "使用者當天有報名 race_id="+rid+" 標記為已完成(status=completed)，未自動變更，請確認是否需一併複核")
	}
	for _, mid := range f.MissionCompletionIDs {
		out = append(out, "此活動綁定每日任務完成紀錄 mission_completions.id="+mid+"，未自動收回")
	}
	out = append(out, "體力值(SP)扣血不可逆，此次回收不會退回已扣的 SP")
	return out
}

// --- Repository ---

// ListRecentGPS 回收流程用清單：依時間窗＋關鍵字（使用者 id/email/name 子字串）列出 gps_runs，
// LEFT JOIN 出依慣例解析的對應 activities 列（見檔案頂端註解），查無對應活動時 ActivityID/
// ExpAwarded 皆為 nil。
func (r *Repository) ListRecentGPS(ctx context.Context, days, limit int, q string) ([]GPSRunSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id::text, g.user_id::text, COALESCE(u.name,''), COALESCE(u.email,''),
		       g.distance_km, COALESCE(g.calib_distance_km, g.distance_km), g.calib_factor,
		       g.duration_s, g.avg_pace_s, g.flagged, COALESCE(g.flag_reason,''), COALESCE(g.review_action,''),
		       g.started_at, g.ended_at, g.excluded_km, g.excluded_segments,
		       a.id::text, a.exp_awarded
		FROM gps_runs g
		JOIN users u ON u.id = g.user_id
		LEFT JOIN activities a ON a.user_id = g.user_id AND a.source IS NULL AND a.recorded_at = g.ended_at
		WHERE g.started_at >= NOW() - make_interval(days => $1::int)
		  AND ($2 = '' OR g.user_id::text ILIKE '%'||$2||'%' OR u.email ILIKE '%'||$2||'%' OR u.name ILIKE '%'||$2||'%')
		ORDER BY g.started_at DESC
		LIMIT $3`, days, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent gps: %w", err)
	}
	defer rows.Close()
	out := []GPSRunSummary{}
	for rows.Next() {
		var s GPSRunSummary
		if err := rows.Scan(&s.ID, &s.UserID, &s.UserName, &s.UserEmail,
			&s.DistanceKm, &s.CalibDistanceKm, &s.CalibFactor, &s.DurationS, &s.AvgPaceS,
			&s.Flagged, &s.FlagReason, &s.ReviewAction, &s.StartedAt, &s.EndedAt,
			&s.ExcludedKm, &s.ExcludedSegments, &s.ActivityID, &s.ExpAwarded); err != nil {
			return nil, err
		}
		out = append(out, *s.withCalibAvgPace())
	}
	return out, rows.Err()
}

// RecallGPSRun 已入帳異常活動的回收（owner 2026-09-03 決策，見檔案頂端註解）。整個流程在單一交易
// 內完成，且用 `pg_advisory_xact_lock(hashtext(user_id))`——與 worker.awardMileageDedup／
// integration.Repository.AwardMileageExp 的 per-user 序列化鎖完全同一把鎖（同一 hashtext(userID)
// key），避免回收跟並行的新發放交錯（例如回收進行中，剛好同一使用者又有一筆重疊活動被判定要
// 「差額補償」，兩邊各自以為自己看到的 exp_awarded/total_km 是最新值）。
//
// total_km 回收金額＝activities.distance_km（發放路徑的「無重疊」全額分支就是無條件加整筆
// distance_km，不論 EXP 是否因 <1km 或配速閘門而不發，見 worker/main.go 5a、mileage_exp.go 5a）。
// 若這筆活動當初其實是踩到「重疊差額補償」分支（5b）入帳（total_km 只加了 deltaKm，可能小於
// distance_km），mileage_exp_events 的歷史列不會留下 deltaKm 本身（只留 exp/dp/km_added 的差額與
// activities 自己的 distance_km），無法從既有資料精確反推——這裡改用「查詢時是否仍有時間重疊的
// 其他已發放活動」當代理警訊，抓到就在 followups 提醒人工複核，不試圖曲折反推一個可能仍不準的值。
func (r *Repository) RecallGPSRun(ctx context.Context, runID string, req RecallRequest) (*RecallResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("recall: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // 已 Commit 後為 no-op

	// 先查 user_id：gps_runs 不會被刪除，PK 查詢安全；advisory lock 要有 user_id 才能算 key，故
	// 這一步在拿到鎖之前，但緊接著就上鎖、且下面 FOR UPDATE 會再鎖一次本列，不存在漏洞窗口。
	var userID string
	if err := tx.QueryRow(ctx, `SELECT user_id::text FROM gps_runs WHERE id=$1`, runID).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGPSRunNotFound
		}
		return nil, fmt.Errorf("recall: load run user: %w", err)
	}

	// per-user 序列化：同一把鎖 key（hashtext(userID)）比照 awardMileageDedup/AwardMileageExp。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, userID); err != nil {
		return nil, fmt.Errorf("recall: advisory lock: %w", err)
	}

	// 鎖定 gps_runs 本列（D2 規格：Load gps_runs row FOR UPDATE）。
	var runDistanceKm float64
	var runDurationS int
	var runEndedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT distance_km, duration_s, ended_at FROM gps_runs WHERE id=$1 FOR UPDATE`, runID).
		Scan(&runDistanceKm, &runDurationS, &runEndedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGPSRunNotFound
		}
		return nil, fmt.Errorf("recall: load run: %w", err)
	}

	// 解析對應活動（override 優先，否則依慣例＋fallback，見檔案頂端註解）。
	// ⚠️ override 的 WHERE 子句帶 user_id=$2：若管理者傳入的 activity_id 不屬於這位使用者（打錯 id），
	// 這裡會查無此列而落入下方 pgx.ErrNoRows 分支，等同「視為未提供 override」，不會誤扣到別人身上；
	// 但也不會另外回傳錯誤提醒管理者「這個 id 打錯了」——回應的 activity_id 會是 null，管理者需自行
	// 核對 followups 的「無金額可收回」訊息是否符合預期。
	var activityID *string
	var actDistanceKm float64
	var actRecordedAt time.Time
	var actRaceID string
	if req.ActivityIDOverride != nil && *req.ActivityIDOverride != "" {
		var id string
		err := tx.QueryRow(ctx, `
			SELECT id::text, distance_km, recorded_at, COALESCE(race_id::text,'')
			FROM activities WHERE id=$1 AND user_id=$2`, *req.ActivityIDOverride, userID).
			Scan(&id, &actDistanceKm, &actRecordedAt, &actRaceID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("recall: load override activity: %w", err)
		}
		if err == nil {
			activityID = &id
		}
	} else {
		var id string
		err := tx.QueryRow(ctx, `
			SELECT id::text, distance_km, recorded_at, COALESCE(race_id::text,'')
			FROM activities WHERE user_id=$1 AND source IS NULL AND recorded_at=$2
			ORDER BY created_at LIMIT 1`, userID, runEndedAt).
			Scan(&id, &actDistanceKm, &actRecordedAt, &actRaceID)
		if errors.Is(err, pgx.ErrNoRows) {
			// fallback：同 user、source IS NULL、duration_s 相同、recorded_at 落在 ±5 分鐘內。
			err = tx.QueryRow(ctx, `
				SELECT id::text, distance_km, recorded_at, COALESCE(race_id::text,'')
				FROM activities
				WHERE user_id=$1 AND source IS NULL AND duration_s=$2
				  AND recorded_at BETWEEN $3::timestamptz - INTERVAL '5 minutes' AND $3::timestamptz + INTERVAL '5 minutes'
				ORDER BY abs(extract(epoch from (recorded_at - $3::timestamptz))) LIMIT 1`,
				userID, runDurationS, runEndedAt).
				Scan(&id, &actDistanceKm, &actRecordedAt, &actRaceID)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("recall: resolve activity: %w", err)
		}
		if err == nil {
			activityID = &id
		}
	}

	result := &RecallResult{RunID: runID, ActivityID: activityID, Reason: req.Reason, Followups: []string{}}

	if activityID != nil {
		// 冪等：已存在「回收標記列」(exp_amount<=0 AND km_added<=0 AND distance_km<0，本函式自己
		// 稍後寫入的那種列) 代表已經回收過，不可重複扣款。
		var already bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM mileage_exp_events
				WHERE activity_id=$1 AND exp_amount<=0 AND km_added<=0 AND distance_km<0
			)`, *activityID).Scan(&already); err != nil {
			return nil, fmt.Errorf("recall: idempotency check: %w", err)
		}
		if already {
			var curReason string
			_ = tx.QueryRow(ctx, `SELECT COALESCE(flag_reason,'') FROM gps_runs WHERE id=$1`, runID).Scan(&curReason)
			if curReason != "" {
				result.Reason = curReason
			}
			result.AlreadyRecalled = true
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("recall: commit (idempotent no-op): %w", err)
			}
			return result, nil
		}

		// 已入帳金額：本活動的列 + 舊制無 activity_id、靠 (user_id, recorded_at) 對應的列（見 deliverable B/C）。
		var sumExp, sumDp, sumKmAdded int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(SUM(exp_amount),0), COALESCE(SUM(dp_amount),0), COALESCE(SUM(km_added),0)
			FROM mileage_exp_events
			WHERE (activity_id=$1) OR (activity_id IS NULL AND user_id=$2 AND recorded_at=$3)`,
			*activityID, userID, actRecordedAt).Scan(&sumExp, &sumDp, &sumKmAdded); err != nil {
			return nil, fmt.Errorf("recall: sum events: %w", err)
		}

		reversedTotalKm := actDistanceKm // 見函式頂端註解：發放路徑全額分支無條件加整筆 distance_km

		if _, err := tx.Exec(ctx, `
			UPDATE users SET total_km = GREATEST(0, total_km - $1), exp = GREATEST(0, exp - $2),
			                  dp = GREATEST(0, dp - $3), updated_at = NOW()
			WHERE id=$4`, reversedTotalKm, sumExp, sumDp, userID); err != nil {
			return nil, fmt.Errorf("recall: update user: %w", err)
		}

		// 回收標記列：seen_at 直接設，讓「里程 EXP 彈窗」（profile/mileage.go）永遠不會顯示這筆負值
		// （該端點另有 exp_amount>0 OR dp_amount>0 的防線，這裡是雙保險）。
		if _, err := tx.Exec(ctx, `
			INSERT INTO mileage_exp_events (user_id, activity_id, exp_amount, dp_amount, km_added, distance_km, recorded_at, seen_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())`,
			userID, *activityID, -sumExp, -sumDp, -sumKmAdded, -reversedTotalKm, actRecordedAt); err != nil {
			return nil, fmt.Errorf("recall: insert reversal event: %w", err)
		}

		// activities：標異常、留 exp_awarded=TRUE（曾經入帳過的事實不變，只是被收回+排除全站計算）。
		if req.ValidDistanceKm != nil {
			if _, err := tx.Exec(ctx, `
				UPDATE activities SET flagged=TRUE, flag_reason=$2, distance_km=$3, raw_distance_km=$3
				WHERE id=$1`, *activityID, req.Reason, *req.ValidDistanceKm); err != nil {
				return nil, fmt.Errorf("recall: update activity: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE activities SET flagged=TRUE, flag_reason=$2 WHERE id=$1`,
				*activityID, req.Reason); err != nil {
				return nil, fmt.Errorf("recall: update activity: %w", err)
			}
		}

		result.Reversed = RecallReversed{TotalKm: reversedTotalKm, Exp: sumExp, Dp: sumDp, KmAdded: sumKmAdded}

		// --- 唯讀 followup 事實蒐集 ---
		facts := recallFollowupFacts{HasActivity: true, RaceID: actRaceID}

		thisStart := actRecordedAt.Add(-time.Duration(runDurationS) * time.Second)
		thisEnd := actRecordedAt
		_ = tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM activities a2
			WHERE a2.user_id=$1 AND a2.id<>$2 AND a2.exp_awarded=true
			  AND (CASE WHEN a2.source IS NULL THEN a2.recorded_at - make_interval(secs=>a2.duration_s) ELSE a2.recorded_at END) < $3
			  AND (CASE WHEN a2.source IS NULL THEN a2.recorded_at - make_interval(secs=>a2.duration_s) ELSE a2.recorded_at END)
			      + make_interval(secs=>a2.duration_s) > $4`,
			userID, *activityID, thisEnd, thisStart).Scan(&facts.OverlapCount)

		if titleRows, err := tx.Query(ctx, `
			SELECT title_code FROM user_titles WHERE user_id=$1 AND earned_at::date=$2::date`,
			userID, actRecordedAt); err == nil {
			for titleRows.Next() {
				var code string
				if titleRows.Scan(&code) == nil {
					facts.TitleCodes = append(facts.TitleCodes, code)
				}
			}
			titleRows.Close()
		}

		_ = tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM referrals WHERE referred_user_id=$1 AND rewarded_at::date=$2::date)`,
			userID, actRecordedAt).Scan(&facts.ReferredRewarded)
		_ = tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM referrals WHERE referrer_user_id=$1 AND rewarded_at::date=$2::date)`,
			userID, actRecordedAt).Scan(&facts.ReferrerRewarded)

		if regRows, err := tx.Query(ctx, `
			SELECT race_id::text FROM registrations WHERE user_id=$1 AND status='completed' AND completed_at::date=$2::date`,
			userID, actRecordedAt); err == nil {
			for regRows.Next() {
				var rid string
				if regRows.Scan(&rid) == nil {
					facts.CompletedRaceIDs = append(facts.CompletedRaceIDs, rid)
				}
			}
			regRows.Close()
		}

		if mcRows, err := tx.Query(ctx, `SELECT id::text FROM mission_completions WHERE activity_id=$1`, *activityID); err == nil {
			for mcRows.Next() {
				var mid string
				if mcRows.Scan(&mid) == nil {
					facts.MissionCompletionIDs = append(facts.MissionCompletionIDs, mid)
				}
			}
			mcRows.Close()
		}

		result.Followups = buildRecallFollowups(facts)
	} else {
		result.Followups = buildRecallFollowups(recallFollowupFacts{HasActivity: false})
	}

	// gps_runs：無論是否解析到對應活動都要標記（D2 規格）。
	if req.ValidDistanceKm != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE gps_runs SET flagged=TRUE, flag_reason=$2, reviewed_at=NOW(), review_action='rejected',
			                     distance_km=$3, calib_distance_km=$3
			WHERE id=$1`, runID, req.Reason, *req.ValidDistanceKm); err != nil {
			return nil, fmt.Errorf("recall: update gps_run: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE gps_runs SET flagged=TRUE, flag_reason=$2, reviewed_at=NOW(), review_action='rejected'
			WHERE id=$1`, runID, req.Reason); err != nil {
			return nil, fmt.Errorf("recall: update gps_run: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("recall: commit: %w", err)
	}
	return result, nil
}

// --- Service ---

// AdminListRecentGPS 回收流程用清單（見 ListRecentGPS）。
func (s *Service) AdminListRecentGPS(ctx context.Context, days, limit int, q string) ([]GPSRunSummary, error) {
	return s.repo.ListRecentGPS(ctx, days, limit, q)
}

// AdminRecallGPS 驗證 reason 後委派 Repository.RecallGPSRun，成功後記一行 Info log（不含健康
// 資料，只有 id/金額）。
func (s *Service) AdminRecallGPS(ctx context.Context, runID, rawReason string, validKm *float64, activityOverride *string) (*RecallResult, error) {
	reason, err := ValidateRecallReason(rawReason)
	if err != nil {
		return nil, err
	}
	res, err := s.repo.RecallGPSRun(ctx, runID, RecallRequest{
		Reason: reason, ValidDistanceKm: validKm, ActivityIDOverride: activityOverride,
	})
	if err != nil {
		return nil, err
	}
	actID := ""
	if res.ActivityID != nil {
		actID = *res.ActivityID
	}
	log.Info().
		Str("run_id", res.RunID).
		Str("activity_id", actID).
		Str("reason", res.Reason).
		Bool("already_recalled", res.AlreadyRecalled).
		Float64("reversed_total_km", res.Reversed.TotalKm).
		Int("reversed_exp", res.Reversed.Exp).
		Int("reversed_dp", res.Reversed.Dp).
		Msg("admin gps run recalled")
	return res, nil
}

// --- Handlers ---

// GET /admin/gps-runs/recent?days=7&limit=200&q=<user id / email / name substring>
func (h *Handler) AdminRecentGPSHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	if days <= 0 {
		days = 7
	}
	if days > 90 { // 防止全表級掃描
		days = 90
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	search := strings.TrimSpace(q.Get("q"))

	runs, err := h.svc.AdminListRecentGPS(r.Context(), days, limit, search)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// POST /admin/gps-runs/{id}/recall
func (h *Handler) AdminRecallGPSHandler(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Reason          string   `json:"reason"`
		ValidDistanceKm *float64 `json:"valid_distance_km"`
		ActivityID      *string  `json:"activity_id"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			respondErr(w, http.StatusBadRequest, "invalid json")
			return
		}
	}

	res, err := h.svc.AdminRecallGPS(r.Context(), id, body.Reason, body.ValidDistanceKm, body.ActivityID)
	if err != nil {
		switch {
		case errors.Is(err, ErrGPSRunNotFound):
			respondErr(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrRecallReasonBenign), errors.Is(err, ErrRecallReasonTooLong):
			respondErr(w, http.StatusBadRequest, err.Error())
		default:
			respondErr(w, http.StatusInternalServerError, "failed")
		}
		return
	}
	respondJSON(w, http.StatusOK, res)
}
