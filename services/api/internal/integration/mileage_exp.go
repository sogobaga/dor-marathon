package integration

import (
	"context"
	"fmt"
	"time"

	"github.com/dor/api/internal/referral"
)

// AwardMileageExp 去重感知、冪等地發放里程 EXP/DP/total_km。供 Strava/Terra 匯入成功
// (ImportActivity 回傳 Status=="inserted") 後呼叫；activityID 為剛插入的 activities.id。
//
// ⚠️ 與 services/worker/main.go 的 Worker.awardMileageDedup 是同一語意的獨立實作——worker
// 是獨立的 Go module、不能 import 這裡的 internal package，故兩邊「各自維護一份」。修改本函式的
// 判斷邏輯（去重規則／exp_rules 算法／交易語意）時，請同步修改另一份，保持完全一致的行為。
//
// 語意：
//  1. per-user 序列化：SELECT pg_advisory_xact_lock(hashtext(userID))（交易內），避免 GPS worker
//     與本 API（Strava/Terra）同時對同一使用者發放而雙發。
//  2. 冪等：FOR UPDATE 鎖定該筆 activity 並讀出 exp_awarded；已為 true 代表發過了，直接 return。
//  3. 去重：查該使用者是否存在「時間重疊且已發放(exp_awarded=true)」的其他活動（比照
//     resolveCrossSourceDups 的重疊判定——⚠️ GPS(source IS NULL) 的 recorded_at 存的是「結束時間」，
//     Strava/Terra(source 非 NULL) 存的是「開始時間」，兩者語意不同，比對前都要正規化成 [start,end)
//     區間再判重疊，否則會誤判/漏判）。若存在 → 這趟已被別的來源計過 → 本筆不發
//     EXP/DP/total_km，且刻意保留 exp_awarded=false（不標記，避免誤判為「已處理」）。
//  4. 發放（無重疊）：讀 exp_rules（per_km/dp_per_km/mileage_cap_km/mileage_min_pace_s，算法同
//     worker 既有 awardMileageExp：floor(distance) → 套單趟上限 → 套配速防造假）→
//     UPDATE users.total_km/exp/dp → INSERT mileage_exp_events → UPDATE activities.exp_awarded=true。
//     全部在同一交易內；total_km 也只在「無重疊」時加，確保一趟只加一次。
func (r *Repository) AwardMileageExp(ctx context.Context, activityID, userID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("award mileage exp: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // 已 Commit 後為 no-op

	// ① per-user 序列化：防同一使用者被多來源（GPS worker / 這裡）同時發放
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, userID); err != nil {
		return fmt.Errorf("award mileage exp: advisory lock: %w", err)
	}

	// ② 冪等：鎖定本筆活動；一併撈 source 才能正規化本筆的 [start,end) 區間
	var awarded bool
	var distanceKm float64
	var durationS int
	var recordedAt time.Time
	var source *string
	if err := tx.QueryRow(ctx, `
		SELECT exp_awarded, distance_km, duration_s, recorded_at, source
		FROM activities WHERE id=$1 AND user_id=$2 FOR UPDATE`, activityID, userID).
		Scan(&awarded, &distanceKm, &durationS, &recordedAt, &source); err != nil {
		return fmt.Errorf("award mileage exp: load activity: %w", err)
	}
	if awarded {
		return nil // 已發過，不重發
	}

	// 正規化本筆時間為 [thisStart, thisEnd)：GPS(source IS NULL) 的 recorded_at 是結束時間，
	// 其餘來源(strava/garmin/coros)的 recorded_at 是開始時間（比照 resolveCrossSourceDups）。
	thisStart := recordedAt
	if source == nil {
		thisStart = recordedAt.Add(-time.Duration(durationS) * time.Second)
	}
	thisEnd := thisStart.Add(time.Duration(durationS) * time.Second)

	// ③ 去重：是否已有時間重疊、且已發放的其他活動（任何來源）。候選列同樣依 source 正規化成
	// [candStart, candEnd) 再判重疊（candStart < thisEnd AND candEnd > thisStart）。
	var dup bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM activities a
			WHERE a.user_id=$1 AND a.exp_awarded=true AND a.id<>$2
			  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END) < $3
			  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END)
			      + make_interval(secs=>a.duration_s) > $4
		)`, userID, activityID, thisEnd, thisStart).Scan(&dup); err != nil {
		return fmt.Errorf("award mileage exp: overlap check: %w", err)
	}
	if dup {
		// 這趟已被時間重疊的另一筆活動計過（不論來源）；本筆不發 EXP/DP/total_km，
		// exp_awarded 維持 false（不動），直接 commit 結束（沒有其他寫入）。
		return tx.Commit(ctx)
	}

	// ④ 發放：讀 exp_rules，算法同 worker 既有 awardMileageExp
	var perKm, dpPerKm, capKm, minPaceS int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(per_km,0), COALESCE(dp_per_km,0),
		       COALESCE(mileage_cap_km,21), COALESCE(mileage_min_pace_s,120)
		FROM exp_rules WHERE id=TRUE`).Scan(&perKm, &dpPerKm, &capKm, &minPaceS); err != nil {
		return fmt.Errorf("award mileage exp: load exp_rules: %w", err)
	}

	rewardKm := 0
	if distanceKm >= 1 && durationS > 0 && (perKm > 0 || dpPerKm > 0) {
		rewardKm = int(distanceKm) // floor(單趟距離)
		if capKm > 0 && rewardKm > capKm {
			rewardKm = capKm // 單趟上限
		}
		if minPaceS > 0 {
			if maxByTime := durationS / minPaceS; rewardKm > maxByTime { // 配速防造假
				rewardKm = maxByTime
			}
		}
		if rewardKm < 0 {
			rewardKm = 0
		}
	}
	expAmt := rewardKm * perKm
	dpAmt := rewardKm * dpPerKm

	// total_km 一趟只加一次（走到這裡即代表本趟首次被計入，不論最終 rewardKm 是否 > 0）
	// RETURNING 新 total_km，供下面推薦達標判斷（referral.Reward 只在 >=10km 時才可能發獎）用，
	// 不需另外查一次。
	var newTotalKm float64
	if err := tx.QueryRow(ctx,
		`UPDATE users SET total_km = total_km + $1, exp = exp + $2, dp = dp + $3, updated_at = NOW() WHERE id=$4 RETURNING total_km`,
		distanceKm, expAmt, dpAmt, userID).Scan(&newTotalKm); err != nil {
		return fmt.Errorf("award mileage exp: update user: %w", err)
	}
	// 推薦/推廣連結系統：同一交易內判斷這位使用者是否因此跨過 10km 門檻，若是且他是被推薦來的
	// 新朋友、且尚未發過獎 → 對推薦人/被推薦人雙向 +VIP 天數（見 internal/referral.Reward，一次性冪等）。
	if err := referral.Reward(ctx, tx, userID, newTotalKm); err != nil {
		return fmt.Errorf("award mileage exp: referral reward: %w", err)
	}
	if rewardKm > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO mileage_exp_events (user_id, exp_amount, dp_amount, km_added, distance_km, recorded_at)
			 VALUES ($1,$2,$3,$4,$5,$6)`, userID, expAmt, dpAmt, rewardKm, distanceKm, recordedAt); err != nil {
			return fmt.Errorf("award mileage exp: insert event: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
		return fmt.Errorf("award mileage exp: mark awarded: %w", err)
	}
	return tx.Commit(ctx)
}
