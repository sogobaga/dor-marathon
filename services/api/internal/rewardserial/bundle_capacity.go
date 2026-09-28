package rewardserial

// bundle_capacity.go 組合型序號組（is_bundle=true，migration 150）容量計算——GroupCapacities/
// GroupCapacityOf 的組合型分支（見 capacity.go GroupCapacity.IsBundle 文件），與
// activityreward/roll.go 發放引擎共用同一顆純函式 BundlePacksFromStock，確保「能不能發／還能發幾包」
// 全站只有一個算法（2026-09-28 對抗性稽核根因：自檢排程 ops/selfcheck_serial_reward.go 與低庫存告警
// race/personal_progress.go 都只認得非組合型「LEFT JOIN reward_serials 數列」的算法，組合包 parent 自己
// 不持有 reward_serials，被兩邊都算成容量 0，誤報「庫存吃緊」）。
//
// 子面額組（children）CRUD 已強制 use_limit_type=single（見
// rewardserial.Service.validateBundleChildMeta 該函式文件「為什麼組合包子面額組不支援共用碼」的說明），
// 故這裡直接數 status='available'／status<>'void' 張數即可，不必像非組合型那樣換算 issue_count／
// use_limit_count——這與 activityreward/roll.go bundlePackAvailable（發放引擎實際查庫存、判斷要不要把
// 這個組合包候選面額列入加權池用）完全同一套定義。
import (
	"context"
	"fmt"
)

// BundlePacksFromStock 純函式：組合型序號組目前／原始能湊滿幾包＝ min(floor(avail[i]/count[i])) over i
// （migration 150 語意）。avail/count 需等長（防呆：只算到較短者）；count[i]<=0 視為 1（理論上不會發生，
// CRUD 已擋 count>=1）。無子項（皆空）回 0，不 panic。
//
// 匯出供 activityreward/roll.go bundlePackAvailable 直接呼叫（單一實作，不在兩個套件各自重寫一次同樣的
// 算式）；bundleGroupCapacities 分別餵入「目前可用」與「非 void（原始上限）」兩組張數各算一次，得到
// Remaining 與 Total（見 capacity.go GroupCapacity 文件）。
//
// 「unlimited 子項」防呆設計說明（本函式不需要、也沒有實作特別分支）：組合包子項 CRUD 已強制
// use_limit_type=single，理論上不會出現 unlimited 子項；若日後真的要支援，模型上只需要讓呼叫端把該子項
// 的 avail 換成一個遠大於其餘任何子項張數的哨兵值（代表「這個子項不會是瓶頸」），min 運算本身完全不需要
// 額外分支就能得出正確答案——見本檔測試 TestBundlePacksFromStock「其中一子項視同 unlimited（極大庫存）」
// 案例示範這個建模方式。
func BundlePacksFromStock(avail, count []int) int {
	n := len(avail)
	if len(count) < n {
		n = len(count)
	}
	if n == 0 {
		return 0
	}
	best := -1
	for i := 0; i < n; i++ {
		c := count[i]
		if c <= 0 {
			c = 1
		}
		packs := avail[i] / c
		if best == -1 || packs < best {
			best = packs
		}
	}
	if best < 0 {
		return 0
	}
	return best
}

// bundleChildStock 一個組合子項在 bundleGroupCapacities 查詢中的原始列資料。
type bundleChildStock struct {
	groupID, name string
	perPack       int
	avail, total  int // avail=可用（status='available'）張數；total=非 void 張數（含已發，原始上限）
}

// bundleGroupCapacities 批次查詢一批組合型序號組（is_bundle=true）的容量，供 GroupCapacities 內部呼叫
// （不對外匯出——呼叫端一律經由 GroupCapacities/GroupCapacityOf 取得，不需要自己先判斷 is_bundle 再走
// 不同 API，見 capacity.go 檔頭）。查無子項的 parent id（理論上不會發生，CRUD 已擋組合型須至少 1 個子項）
// 不會出現在回傳的 map 中，比照 GroupCapacities 對查無 id 的既有慣例。
func bundleGroupCapacities(ctx context.Context, db DBTX, groupIDs []string) (map[string]GroupCapacity, error) {
	rows, err := db.Query(ctx, `
		SELECT i.parent_group_id::text, i.child_group_id::text, COALESCE(g.name,''), i.count,
		       COUNT(s.id) FILTER (WHERE s.status='available'),
		       COUNT(s.id) FILTER (WHERE s.status<>'void')
		FROM reward_serial_group_items i
		JOIN reward_serial_groups g ON g.id = i.child_group_id
		LEFT JOIN reward_serials s ON s.group_id = i.child_group_id
		WHERE i.parent_group_id = ANY($1::uuid[])
		GROUP BY i.id, i.parent_group_id, i.child_group_id, g.name, i.count, i.sort_order
		ORDER BY i.parent_group_id, i.sort_order, i.id`, groupIDs)
	if err != nil {
		return nil, fmt.Errorf("bundle group capacities: %w", err)
	}
	byParent := map[string][]bundleChildStock{}
	for rows.Next() {
		var parentID string
		var c bundleChildStock
		if err := rows.Scan(&parentID, &c.groupID, &c.name, &c.perPack, &c.avail, &c.total); err != nil {
			rows.Close()
			return nil, err
		}
		byParent[parentID] = append(byParent[parentID], c)
	}
	scanErr := rows.Err()
	rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}

	out := map[string]GroupCapacity{}
	for parentID, children := range byParent {
		availList := make([]int, len(children))
		totalList := make([]int, len(children))
		perPackList := make([]int, len(children))
		comps := make([]BundleComponent, len(children))
		for i, c := range children {
			availList[i] = c.avail
			totalList[i] = c.total
			perPackList[i] = c.perPack
			comps[i] = BundleComponent{GroupID: c.groupID, Name: c.name, PerPack: c.perPack, ChildRemaining: c.avail}
		}
		remaining := BundlePacksFromStock(availList, perPackList)
		total := BundlePacksFromStock(totalList, perPackList)
		issued := total - remaining
		if issued < 0 {
			issued = 0 // 理論上不會發生（Remaining<=Total 恆成立，見 capacity.go Total 文件），防呆保底
		}
		out[parentID] = GroupCapacity{IsBundle: true, Remaining: remaining, Total: total, Issued: issued, Components: comps}
	}
	return out, nil
}
