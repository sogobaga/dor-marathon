package rewardserial

// capacity.go 序號組容量「單一真相」（migration 178：use_limit_type 從「持有人可核銷幾次」改為「同一列
// 序號可以發給幾位不同得主」，見該 migration 檔頭根因說明）。全站任何需要判斷「這組序號還能不能發」的
// 地方——即時獎勵發放（activityreward.claimSerialsFromGroup／groupAvailableCount）、事件驅動低庫存告警
// （race/personal_progress.go checkAndNotifyLowStock）、排程巡檢自檢（ops/selfcheck_serial_reward.go）、
// 後台序號組列表統計——都必須呼叫這裡的 GroupCapacity/GroupCapacities，不得各自重新計算，否則同一組
// 序號會在不同地方出現互相矛盾的「剩餘庫存」數字（这正是本 migration 要修的根因類問題的翻版）。
//
// 三種 use_limit_type 的容量定義：
//   - single    ：一列一位得主（既有行為，完全不變）。Remaining=可用（status='available'）列數；
//     Total=非 void 列數；Issued=已發送（status='issued'）列數。
//   - repeat(N) ：一列可發給 N 位不同得主，issue_count 累加到 N 才耗盡。Remaining=Σ每列剩餘可發人數
//     （逐列在 0 以上夾住，避免異常資料出現負值貢獻）；Total=非 void 列數×N；Issued=Σ每列已發人數。
//   - unlimited ：一列可發給任意人數，永不耗盡。Unlimited=true（只要仍有至少 1 列非 void）；Remaining
//     沒有意義，固定回 0（呼叫端一律先看 Unlimited 再看 Remaining，見各呼叫端註解）；Issued=Σ每列已發
//     人數。
//
// status='void'（已註銷）的列在任何 use_limit_type 下都不佔容量、不計入任何統計——註銷即視為不存在。
import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DBTX 本檔案查詢容量需要的介面子集：*pgxpool.Pool 與 pgx.Tx 皆滿足，且 activityreward.Execer（額外多帶
// Exec 方法）依 Go 介面方法集合結構相容規則可直接當 DBTX 傳入，不需另外轉型——讓 roll.go 在既有交易
// （pgx.Tx）內查容量時，容量計算與扣庫存的 UPDATE 落在同一交易可見範圍，維持既有交易邊界不變。
type DBTX interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// GroupCapacity 一個序號組目前的容量快照，含組合型（is_bundle=true，見 IsBundle）——這裡是全站「這組
// 序號還能不能發」的單一真相，組合型與非組合型都要能從 GroupCapacities/GroupCapacityOf 查到正確答案，
// 呼叫端不必自己先判斷 is_bundle 再走不同 API（2026-09-28 對抗性稽核抓到的根因：自檢排程/低庫存告警
// 只認得非組合型的算法，組合包一律被 LEFT JOIN 湊成全零，見 bundle_capacity.go 檔頭）。
type GroupCapacity struct {
	// Remaining 還能再發給幾位得主：非組合型 single/repeat 才有意義（unlimited 固定 0，見 Unlimited）；
	// 組合型（IsBundle=true）＝目前「可用」子面額張數換算出的包數，見 bundle_capacity.go
	// bundleGroupCapacities：Remaining = min_i floor(child_i.可用張數 / PerPack_i)。
	Remaining int
	Unlimited bool // true＝unlimited 型且仍有至少 1 列非 void 序號，可無限發放；組合型恆 false（見 IsBundle 文件）
	// Total 容量上限：非組合型 single=列數；repeat=列數×N；unlimited 恆 0，無上限可言。組合型（IsBundle=
	// true）＝子面額張數換算出的「原始」包數上限（含已發，非目前可用）：Total = min_i floor(child_i.非void
	// 張數 / PerPack_i)——與 Remaining 用同一顆純函式 BundlePacksFromStock 算，只是餵進去的是非 void 張數
	// 而非可用張數，兩者才有可比性（複合 Remaining<=Total 恆成立）。
	Total int
	// Issued 已發給幾位得主：非組合型 single=已發送列數；repeat/unlimited=Σ列.issue_count。組合型（IsBundle=
	// true）＝Total-Remaining（衍生值：組合包沒有自己的 issue_count 可加總，只能反推「原本能湊幾包」與
	// 「現在還能湊幾包」的差距，語意等同「已經发出的包數」，因為每次中獎 grantSerialBundle 都是整包發放，
	// 不會有「發到一半」的中繼狀態）。
	Issued int
	// IsBundle true＝這是組合型序號組（is_bundle=true，migration 150）的容量快照：組合包自己不持有
	// reward_serials，Remaining/Total/Issued 改用上述「湊幾包」語意；Components 是組合明細，供呼叫端（如
	// 自檢報表）組出人類可讀的說明，不必自己重新查 reward_serial_group_items。false 時 Components 恆為
	// nil。子面額組（children）CRUD 已強制 use_limit_type=single（見
	// rewardserial.Service.validateBundleChildMeta），故這裡只需要數子面額組的張數，不必換算
	// issue_count／use_limit_count。
	IsBundle   bool
	Components []BundleComponent
}

// BundleComponent 組合型序號組（IsBundle=true）容量明細的一個子項快照，供呼叫端組讀者可讀的說明字串
// （如自檢報表「組合包=3×LINE POINTS 1000，子面額剩 5 張」）使用。
type BundleComponent struct {
	GroupID        string // 子面額組 id
	Name           string // 子面額組名稱（reward_serial_groups.name）
	PerPack        int    // 每包需要這個子面額幾張（reward_serial_group_items.count）
	ChildRemaining int    // 該子面額組目前可用（status='available'）張數
}

// computeGroupCapacity 純函式：依 use_limit_type 把序號列彙總統計換算成 GroupCapacity（見本檔頭三種
// 定義）。n/availRows/issuedRows/issueSum/repeatRemaining 皆已在 SQL 端排除 status='void' 的列。不碰
// DB，方便單元測試涵蓋三種類型與邊界資料。useLimitType 不是 single/repeat/unlimited 三者之一（理論上
// 不會發生，CRUD 已擋，見 rewardserial.Service 的 validUseLimitTypes）時保底當 single，維持既有行為。
func computeGroupCapacity(useLimitType string, useLimitCount, n, availRows, issuedRows, issueSum, repeatRemaining int) GroupCapacity {
	switch useLimitType {
	case "unlimited":
		return GroupCapacity{Unlimited: n > 0, Issued: issueSum}
	case "repeat":
		return GroupCapacity{Remaining: repeatRemaining, Total: n * useLimitCount, Issued: issueSum}
	default: // single（含未知型別的保底）
		return GroupCapacity{Remaining: availRows, Total: n, Issued: issuedRows}
	}
}

// GroupCapacities 批次查詢多個序號組的容量（避免 N+1；後台列表／自檢排程掃多場賽事時務必用這支，
// 不要在迴圈裡逐一呼叫 GroupCapacityOf）。查無的 group id（如剛好被刪除）不會出現在回傳的 map 中，
// 由呼叫端視為「查無→容量全零」自行處理（比照既有 groupAvailableCount 的「序號組不存在→跳過」慣例）。
//
// 組合型（is_bundle=true）與非組合型混在同一份 groupIDs 內查也沒問題：下方主查詢的 LEFT JOIN
// reward_serials 對組合包 parent 必定湊出全零聚合（組合包自己不持有序號），此處偵測到 is_bundle=true
// 就先跳過、收集 id，主查詢的 rows 讀完關閉後再用 bundleGroupCapacities 批次另外查一次（見該函式），
// 避免對組合包誤用非組合型的容量公式（2026-09-28 對抗性稽核根因：自檢排程曾把組合包容量查成 0，見
// bundle_capacity.go 檔頭）。
func GroupCapacities(ctx context.Context, db DBTX, groupIDs []string) (map[string]GroupCapacity, error) {
	out := map[string]GroupCapacity{}
	if len(groupIDs) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, `
		SELECT g.id::text, g.is_bundle, g.use_limit_type, COALESCE(g.use_limit_count,0),
		       COUNT(s.id) FILTER (WHERE s.status <> 'void'),
		       COUNT(s.id) FILTER (WHERE s.status = 'available'),
		       COUNT(s.id) FILTER (WHERE s.status = 'issued'),
		       COALESCE(SUM(s.issue_count) FILTER (WHERE s.status <> 'void'), 0),
		       COALESCE(SUM(GREATEST(0, g.use_limit_count - s.issue_count))
		                FILTER (WHERE s.status <> 'void' AND g.use_limit_type = 'repeat'), 0)
		FROM reward_serial_groups g
		LEFT JOIN reward_serials s ON s.group_id = g.id
		WHERE g.id = ANY($1::uuid[])
		GROUP BY g.id, g.is_bundle, g.use_limit_type, g.use_limit_count`, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("group capacities: %w", err)
	}
	var bundleIDs []string
	for rows.Next() {
		var id, useLimitType string
		var isBundle bool
		var useLimitCount, n, availRows, issuedRows, issueSum, repeatRemaining int
		if err := rows.Scan(&id, &isBundle, &useLimitType, &useLimitCount, &n, &availRows, &issuedRows, &issueSum, &repeatRemaining); err != nil {
			rows.Close()
			return nil, err
		}
		if isBundle {
			bundleIDs = append(bundleIDs, id)
			continue
		}
		out[id] = computeGroupCapacity(useLimitType, useLimitCount, n, availRows, issuedRows, issueSum, repeatRemaining)
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if len(bundleIDs) > 0 {
		bundleCaps, err := bundleGroupCapacities(ctx, db, bundleIDs)
		if err != nil {
			return nil, err
		}
		for id, c := range bundleCaps {
			out[id] = c
		}
	}
	return out, nil
}

// GroupCapacityOf 單一序號組容量的便利包裝（內部就是呼叫 GroupCapacities 傳一個元素的 slice）。查無此
// 序號組（如剛好被刪除）或 groupID 為空 → 回傳零值，不視為錯誤。多個 id 要查時請直接呼叫 GroupCapacities
// 批次查，不要在迴圈裡呼叫本函式造成 N+1。
//
// ⚠️ 命名為 GroupCapacityOf 而非 GroupCapacity：Go 不允許同一套件內型別與函式同名，型別已優先取得
// GroupCapacity 這個名字（見上方 struct 與呼叫端既有用法），本函式（單筆查詢）改用這個名字。
func GroupCapacityOf(ctx context.Context, db DBTX, groupID string) (GroupCapacity, error) {
	if groupID == "" {
		return GroupCapacity{}, nil
	}
	m, err := GroupCapacities(ctx, db, []string{groupID})
	if err != nil {
		return GroupCapacity{}, err
	}
	return m[groupID], nil
}
