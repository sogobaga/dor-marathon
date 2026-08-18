package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dor/api/internal/referral"
)

// benignFlagReasons：flagged=true 但屬於「同帳號跨裝置/跨來源/同源重複」的良性標記——不是作弊，
// 只是同一趟跑步被記了兩筆。這類活動仍可參與差額補償流程（見 AwardMileageExp）。
// 不在此集合中的 flag_reason（目前只有 cross_account_duplicate＝跨帳號洗數據作弊）一律不發放、
// 不補償、也不標記 exp_awarded——見 AwardMileageExp 開頭的 flagged 政策。
var benignFlagReasons = map[string]bool{
	"multi_device_duplicate": true, // 同帳號多裝置重複上傳同一趟
	"cross_source_duplicate": true, // resolveCrossSourceDups：同帳號 GPS/Strava/Terra 互相重複
	"duplicate":              true, // 同帳號、精確指紋相同的重複匯入
}

// computeRewardKm 算出單趟活動可折算的獎勵公里數：floor(distance) → 套單趟上限 capKm → 套配速防造假
// minPaceS。distanceKm<1 或 durationS<=0 或 perKm 與 dpPerKm 皆為 0 時直接回 0（不具備發放資格）。
// 供 AwardMileageExp 全額發放與差額補償共用（差額補償需要對本筆與每筆重疊已發放筆各自計算一次）。
//
// ⚠️ 與 services/worker/main.go 的同名函式是同一語意的獨立實作（worker 為獨立 Go module，理由同下方
// AwardMileageExp 註解）；修改演算法時請同步修改另一份。
func computeRewardKm(distanceKm float64, durationS, perKm, dpPerKm, capKm, minPaceS int) int {
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
	return rewardKm
}

// clampDeltaInt 回傳 max(a-b, 0)：差額補償「只補不扣」的整數版 clamp。
func clampDeltaInt(a, b int) int {
	if a > b {
		return a - b
	}
	return 0
}

// clampDeltaFloat 回傳 max(a-b, 0)：差額補償「只補不扣」的浮點版 clamp（total_km 用）。
func clampDeltaFloat(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return 0
}

// externalAwardHash 算出 (source, external_id) 的一次性、不可還原雜湊，供 external_award_ledger
// （migrations/131_external_award_ledger.sql）記錄「這筆外部活動的獎勵是否已處理過」。只存雜湊、
// 不存可識別的 Strava/Terra 活動內容本身，符合 DeleteProviderActivities 註解提到的刪除義務精神。
// 純 sha256、無需密鑰（不是敏感資料，只是去重用途的一次性指紋）。
//
// ⚠️ 與 services/worker/main.go 的同名函式是同一語意的獨立實作（worker 為獨立 Go module，理由同
// 上方 AwardMileageExp 註解）；修改時請同步修改另一份。
func externalAwardHash(source, externalID string) string {
	sum := sha256.Sum256([]byte(source + ":" + externalID))
	return hex.EncodeToString(sum[:])
}

// AwardMileageExp 去重感知、冪等地發放里程 EXP/DP/total_km。供 Strava/Terra 匯入成功
// (ImportActivity 回傳 Status=="inserted"，或 Status=="duplicate" 且 Reason=="multi_device_duplicate"
// 的良性同帳號重複) 後呼叫；activityID 為剛插入的 activities.id。
//
// ⚠️ 與 services/worker/main.go 的 Worker.awardMileageDedup 是同一語意的獨立實作——worker
// 是獨立的 Go module、不能 import 這裡的 internal package，故兩邊「各自維護一份」。修改本函式的
// 判斷邏輯（去重規則／exp_rules 算法／交易語意）時，請同步修改另一份，保持完全一致的行為。
//
// 語意：
//  1. per-user 序列化：SELECT pg_advisory_xact_lock(hashtext(userID))（交易內），避免 GPS worker
//     與本 API（Strava/Terra）同時對同一使用者發放而雙發。
//  2. 冪等：FOR UPDATE 鎖定該筆 activity 並讀出 exp_awarded；已為 true 代表發過了，直接 return。
//  3. flagged 政策（2026-08-16 新增，修補既有安全漏洞）：flagged=true 且 flag_reason 不在
//     benignFlagReasons（目前僅 cross_account_duplicate＝跨帳號洗數據作弊）→ 直接 return，
//     完全不發放、不補償、也不標記 exp_awarded。⚠️ 這同時是安全漏洞修補點：舊版
//     reconcileMileageExp 的 sweep 掃描與本函式都不檢查 flagged，導致 cross_account_duplicate
//     活動會在 2 分鐘後被 sweep 誤發 EXP/DP；此後 sweep 對這類活動永遠是 no-op。
//     flag_reason 屬於 benignFlagReasons（同帳號跨裝置/跨來源/同源重複，非作弊）者則與
//     unflagged 活動走同一套統一流程（見 4-5）。
//  4. 去重比對：查該使用者「時間重疊且已發放(exp_awarded=true)」的其他活動（比照
//     resolveCrossSourceDups 的重疊判定——⚠️ GPS(source IS NULL) 的 recorded_at 存的是「結束時間」，
//     Strava/Terra(source 非 NULL) 存的是「開始時間」，兩者語意不同，比對前都要正規化成 [start,end)
//     區間再判重疊，否則會誤判/漏判），撈出全部重疊已發放筆的 (distance_km, duration_s)（實務 0~2 筆）。
//     5a. 無重疊已發放筆 → 全額發放（floor(distance) → 套單趟上限 → 套配速防造假 →
//     UPDATE users.total_km/exp/dp → referral.Reward → INSERT mileage_exp_events →
//     UPDATE activities.exp_awarded=true）。unflagged 與 benign-flagged 皆可能走此路——
//     benign-flagged 走此路時即成為「這趟以此筆入帳」，之後原本重疊的那筆進來會因重疊而走
//     5b 差額比較，「以配對中最大值計」的語意自動成立。
//     5b. 有重疊已發放筆 → 差額補償（使用者 2026-08-16 拍板：以配對中最大值計、只補不扣）：
//     deltaReward = max(0, 本筆 computeRewardKm − 各重疊已發放筆 computeRewardKm 的最大值)；
//     deltaKm = max(0, 本筆 distance − 各重疊已發放筆 distance 的最大值)。deltaReward>0 或
//     deltaKm>0 時才 UPDATE users（加 deltaKm/deltaReward*perKm/deltaReward*dpPerKm）→
//     referral.Reward → （deltaReward>0 時）INSERT mileage_exp_events。
//     無論 delta 是否為 0，一律 UPDATE activities.exp_awarded=TRUE：語意為「此筆價值已入帳
//     完畢（直接發放或已與重疊筆比較補償過）」——讓 reconcile sweep 不再重複掃它，也讓它可作為
//     之後其他重疊筆比較時的 MAX 基準。
//     （舊版註解：「刻意保留 exp_awarded=false」僅適用於「整筆不發」的舊語意，本次已改為差額
//     補償，此筆一律標記為已處理，見上。）
//     全部在同一交易內；exp_rules 讀取在統一流程之前（全額發放與差額補償都需要 perKm/dpPerKm/
//     capKm/minPaceS）。
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

	// ② 冪等：鎖定本筆活動；一併撈 source/external_id（正規化 [start,end) 用、外部帳本用）與
	// flagged/flag_reason（政策判斷用）
	var awarded, flagged bool
	var distanceKm float64
	var durationS int
	var recordedAt time.Time
	var source *string
	var flagReason *string
	var externalID string
	if err := tx.QueryRow(ctx, `
		SELECT exp_awarded, distance_km, duration_s, recorded_at, source, flagged, flag_reason, COALESCE(external_id,'')
		FROM activities WHERE id=$1 AND user_id=$2 FOR UPDATE`, activityID, userID).
		Scan(&awarded, &distanceKm, &durationS, &recordedAt, &source, &flagged, &flagReason, &externalID); err != nil {
		return fmt.Errorf("award mileage exp: load activity: %w", err)
	}
	if awarded {
		return nil // 已發過，不重發
	}

	// ③ flagged 政策：非良性標記（如 cross_account_duplicate＝跨帳號作弊）一律不處理，見上方函式註解。
	if flagged && !(flagReason != nil && benignFlagReasons[*flagReason]) {
		return tx.Commit(ctx) // 尚無任何寫入，commit 等同 no-op，僅釋放鎖
	}

	// ③.5 durable 帳本去重（對抗式審查 CRITICAL-2 修補）：外部來源（Strava/Terra，source 非 nil 且
	// external_id 非空）活動若先前已經被本函式處理過一次（無論是全額發放或差額補償，見下方 5a/5b
	// 結尾的帳本寫入），即使原始 activities 列後來被刪除（見 DeleteProviderActivities，撤權/中斷
	// 連接的刪除義務）又重新匯入回來變成一筆全新的列（exp_awarded 重置為 false），也絕不可以再次
	// 走 5a/5b 重新 credit——activities 表本身的 exp_awarded / ON CONFLICT(source,external_id) 去重
	// 完全依賴列還「活著」，列被刪掉後這層防線就消失，這裡查一張獨立、不受活動列刪除影響的帳本
	// （external_award_ledger，migrations/131）補上。GPS 活動（source 為 nil）不會被刪除重匯，
	// 不需要、也不查這張表。
	var isExternal bool
	var extHash string
	if source != nil && externalID != "" {
		isExternal = true
		extHash = externalAwardHash(*source, externalID)
		var alreadyAwarded bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM external_award_ledger WHERE user_id=$1 AND source=$2 AND ext_hash=$3)`,
			userID, *source, extHash).Scan(&alreadyAwarded); err != nil {
			return fmt.Errorf("award mileage exp: ledger check: %w", err)
		}
		if alreadyAwarded {
			// 這筆外部活動先前（刪除前）已經處理過：只補標記，不再重複 credit 任何 total_km/exp/dp。
			if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
				return fmt.Errorf("award mileage exp: mark awarded (ledger dedup): %w", err)
			}
			return tx.Commit(ctx)
		}
	}

	// 正規化本筆時間為 [thisStart, thisEnd)：GPS(source IS NULL) 的 recorded_at 是結束時間，
	// 其餘來源(strava/garmin/coros)的 recorded_at 是開始時間（比照 resolveCrossSourceDups）。
	thisStart := recordedAt
	if source == nil {
		thisStart = recordedAt.Add(-time.Duration(durationS) * time.Second)
	}
	thisEnd := thisStart.Add(time.Duration(durationS) * time.Second)

	// ④ 讀 exp_rules：全額發放與差額補償都需要
	var perKm, dpPerKm, capKm, minPaceS int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(per_km,0), COALESCE(dp_per_km,0),
		       COALESCE(mileage_cap_km,21), COALESCE(mileage_min_pace_s,120)
		FROM exp_rules WHERE id=TRUE`).Scan(&perKm, &dpPerKm, &capKm, &minPaceS); err != nil {
		return fmt.Errorf("award mileage exp: load exp_rules: %w", err)
	}

	// ⑤ 撈出「時間重疊且已發放」的其他活動之 (distance_km, duration_s)（實務 0~2 筆）。候選列同樣
	// 依 source 正規化成 [candStart, candEnd) 再判重疊（candStart < thisEnd AND candEnd > thisStart）。
	rows, err := tx.Query(ctx, `
		SELECT a.distance_km, a.duration_s FROM activities a
		WHERE a.user_id=$1 AND a.exp_awarded=true AND a.id<>$2
		  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END) < $3
		  AND (CASE WHEN a.source IS NULL THEN a.recorded_at - make_interval(secs=>a.duration_s) ELSE a.recorded_at END)
		      + make_interval(secs=>a.duration_s) > $4`,
		userID, activityID, thisEnd, thisStart)
	if err != nil {
		return fmt.Errorf("award mileage exp: overlap query: %w", err)
	}
	var overlapKm []float64
	var overlapDur []int
	for rows.Next() {
		var km float64
		var dur int
		if err := rows.Scan(&km, &dur); err != nil {
			rows.Close()
			return fmt.Errorf("award mileage exp: scan overlap: %w", err)
		}
		overlapKm = append(overlapKm, km)
		overlapDur = append(overlapDur, dur)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("award mileage exp: overlap rows: %w", err)
	}

	thisReward := computeRewardKm(distanceKm, durationS, perKm, dpPerKm, capKm, minPaceS)

	if len(overlapKm) == 0 {
		// 無重疊已發放筆 → 全額發放（見函式註解 5a）
		expAmt := thisReward * perKm
		dpAmt := thisReward * dpPerKm

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
		if thisReward > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO mileage_exp_events (user_id, exp_amount, dp_amount, km_added, distance_km, recorded_at)
				 VALUES ($1,$2,$3,$4,$5,$6)`, userID, expAmt, dpAmt, thisReward, distanceKm, recordedAt); err != nil {
				return fmt.Errorf("award mileage exp: insert event: %w", err)
			}
		}
		if isExternal {
			// 首次處理這筆外部活動，寫入帳本：日後若原始列被刪除又重新匯入，會在 ③.5 被擋下不重發。
			if _, err := tx.Exec(ctx,
				`INSERT INTO external_award_ledger (user_id, source, ext_hash) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
				userID, *source, extHash); err != nil {
				return fmt.Errorf("award mileage exp: ledger insert: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
			return fmt.Errorf("award mileage exp: mark awarded: %w", err)
		}
		return tx.Commit(ctx)
	}

	// 有重疊已發放筆 → 差額補償（見函式註解 5b）：以配對中最大值計、只補不扣。
	maxOverlapReward := 0
	maxOverlapKm := 0.0
	for i, km := range overlapKm {
		if rk := computeRewardKm(km, overlapDur[i], perKm, dpPerKm, capKm, minPaceS); rk > maxOverlapReward {
			maxOverlapReward = rk
		}
		if km > maxOverlapKm {
			maxOverlapKm = km
		}
	}
	deltaReward := clampDeltaInt(thisReward, maxOverlapReward)
	deltaKm := clampDeltaFloat(distanceKm, maxOverlapKm)
	deltaExpAmt := deltaReward * perKm
	deltaDpAmt := deltaReward * dpPerKm

	if deltaReward > 0 || deltaKm > 0 {
		var newTotalKm float64
		if err := tx.QueryRow(ctx,
			`UPDATE users SET total_km = total_km + $1, exp = exp + $2, dp = dp + $3, updated_at = NOW() WHERE id=$4 RETURNING total_km`,
			deltaKm, deltaExpAmt, deltaDpAmt, userID).Scan(&newTotalKm); err != nil {
			return fmt.Errorf("award mileage exp: update user (delta): %w", err)
		}
		if err := referral.Reward(ctx, tx, userID, newTotalKm); err != nil {
			return fmt.Errorf("award mileage exp: referral reward (delta): %w", err)
		}
		if deltaReward > 0 {
			if _, err := tx.Exec(ctx,
				`INSERT INTO mileage_exp_events (user_id, exp_amount, dp_amount, km_added, distance_km, recorded_at)
				 VALUES ($1,$2,$3,$4,$5,$6)`, userID, deltaExpAmt, deltaDpAmt, deltaReward, distanceKm, recordedAt); err != nil {
				return fmt.Errorf("award mileage exp: insert event (delta): %w", err)
			}
		}
	}
	if isExternal {
		// 首次處理這筆外部活動（無論 delta 是否為 0），寫入帳本，理由同 5a 分支。
		if _, err := tx.Exec(ctx,
			`INSERT INTO external_award_ledger (user_id, source, ext_hash) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			userID, *source, extHash); err != nil {
			return fmt.Errorf("award mileage exp: ledger insert (delta): %w", err)
		}
	}
	// 無論 delta 是否為 0，一律標記 exp_awarded=TRUE（見函式註解 5b）。
	if _, err := tx.Exec(ctx, `UPDATE activities SET exp_awarded = TRUE WHERE id=$1`, activityID); err != nil {
		return fmt.Errorf("award mileage exp: mark awarded (delta): %w", err)
	}
	return tx.Commit(ctx)
}
