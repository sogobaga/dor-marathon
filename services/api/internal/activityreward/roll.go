package activityreward

// roll.go 完成觸發即時獎勵 roll 引擎。冪等由呼叫端的 CAS 完成判定保證（見套件註解），本檔案一律不開
// 新交易、不做「事後補抽」，所有 SQL 都吃呼叫端傳入的 Execer（通常是外層已在進行中的 pgx.Tx）。
// 亂數全程 crypto/rand（防作弊關鍵，比照 internal/monopoly/draw.go 的 pickWeighted 作法）。

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/notify"
	"github.com/dor/api/internal/vip"
	"github.com/dor/api/internal/wallet"
)

// RollAndGrant 依 cfg 逐項獨立機率 roll 即時獎勵並發放：exp 內聯 UPDATE users、dp/gp 走
// wallet.AwardDP/AwardGP、vip 走 vip.Extend（皆直接入帳，不進 user_rewards）；serial 是「以商家為單位的
// 兩層抽獎」——ProbBP 先判定該商家中不中獎，中了才在該商家旗下有庫存的面額中依權重抽一組實際配發
// 並寫入 user_rewards（見 grantSerialTwoLayer）。cfg 為 nil 或無項目 → no-op，回傳空結果。
//
// 回傳兩批東西：granted 是所有「實際中獎且成功發放」的項目（serial 中獎時可能一次貢獻多筆，見
// claimSerialsFromGroup 的 grant_count）；issuedGroupIDs 是這次呼叫「實際發過序號」的序號組 id（去重）
// ——刻意只回傳 id、不在此處判斷是否低庫存：呼叫端必須在外層交易 Commit 成功後，用不受本交易可見性限制
// 的連線重新查詢這些組的真實庫存才能正確判斷「是否該發低庫存告警」（見套件頂部說明、
// race.MarkAttemptCompletedAndGrant）。serial 類商家中獎但旗下所有面額當下都無庫存時，該項目直接跳過
// （不算錯誤，其餘項目照常各自獨立 roll）。
func RollAndGrant(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID string, cfg *RewardConfig) ([]GrantedReward, []string, error) {
	if cfg == nil || len(cfg.Items) == 0 {
		return nil, nil, nil
	}
	var granted []GrantedReward
	var issuedGroupIDs []string
	seenGroups := map[string]bool{}
	for i := range cfg.Items {
		item := &cfg.Items[i]
		hit, err := rollHit(item.ProbBP)
		if err != nil {
			return granted, issuedGroupIDs, fmt.Errorf("roll item %d (%s): %w", i, item.Type, err)
		}
		if !hit {
			continue
		}
		if item.Type == "serial" {
			if len(item.Bundle) > 0 {
				// 固定組合包（migration 149）：與兩層抽獎（grantSerialTwoLayer）是互斥的兩條路徑——
				// item.Bundle 非空才會走這裡，validate.go 已擋掉兩者同時非空的設定（見 Validate）。
				gs, bundleGroupIDs, err := grantSerialBundle(ctx, db, userID, sourceType, sourceRaceID, sourceRegID, item)
				if err != nil {
					return granted, issuedGroupIDs, fmt.Errorf("grant item %d (serial bundle): %w", i, err)
				}
				granted = append(granted, gs...)
				for _, gid := range bundleGroupIDs {
					if gid != "" && !seenGroups[gid] {
						seenGroups[gid] = true
						issuedGroupIDs = append(issuedGroupIDs, gid)
					}
				}
				continue
			}
			gs, issuedGroupID, err := grantSerialTwoLayer(ctx, db, userID, sourceType, sourceRaceID, sourceRegID, item)
			if err != nil {
				return granted, issuedGroupIDs, fmt.Errorf("grant item %d (serial): %w", i, err)
			}
			granted = append(granted, gs...)
			if issuedGroupID != "" && !seenGroups[issuedGroupID] {
				seenGroups[issuedGroupID] = true
				issuedGroupIDs = append(issuedGroupIDs, issuedGroupID)
			}
			continue
		}
		gs, err := grantItem(ctx, db, userID, sourceType, sourceRaceID, sourceRegID, item)
		if err != nil {
			return granted, issuedGroupIDs, fmt.Errorf("grant item %d (%s): %w", i, item.Type, err)
		}
		granted = append(granted, gs...)
	}
	return granted, issuedGroupIDs, nil
}

// rollHit crypto/rand 取 n∈[0,10000)；n < probBP 才中獎。probBP<=0 永不中；probBP>=10000 必中
// （不必消耗亂數源，也避免呼叫 rand.Int 時 max<=0 的邊界問題）。
func rollHit(probBP int) (bool, error) {
	if probBP <= 0 {
		return false, nil
	}
	if probBP >= 10000 {
		return true, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return false, fmt.Errorf("roll rand: %w", err)
	}
	return n.Int64() < int64(probBP), nil
}

// randRange 均勻隨機取 [lo,hi] 含端點的整數。lo==hi 直接回傳該值；lo>hi 視為設定錯誤（理論上已被
// Validate 擋下），保底回 lo 而不 panic。
func randRange(lo, hi int) (int, error) {
	if lo >= hi {
		return lo, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo)+1))
	if err != nil {
		return 0, fmt.Errorf("range rand: %w", err)
	}
	return lo + int(n.Int64()), nil
}

// pickWeightedDenomIndex 加權隨機挑一個候選面額在 denoms 中的索引，全程 crypto/rand（防作弊關鍵，
// 比照 internal/monopoly/draw.go pickWeighted 的手法：權重加總 → 在 [0,total) 均勻取一點 → 依序累加
// 權重定位落點所在區間）。denoms 為空或權重總和 <=0 時回 -1,nil（呼叫端視為「沒有可抽的面額」，不是
// 錯誤）。呼叫端負責保證傳入的 denoms 皆為呼叫當下「有庫存」的候選（見 grantSerialTwoLayer）。
func pickWeightedDenomIndex(denoms []RewardDenom) (int, error) {
	total := 0
	for _, d := range denoms {
		if d.Weight > 0 {
			total += d.Weight
		}
	}
	if total <= 0 {
		return -1, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(total)))
	if err != nil {
		return -1, fmt.Errorf("weighted denom pick rand: %w", err)
	}
	target := n.Int64()
	var acc int64
	for i, d := range denoms {
		if d.Weight <= 0 {
			continue
		}
		acc += int64(d.Weight)
		if target < acc {
			return i, nil
		}
	}
	// 理論上不會走到（累加到 total 必定 > target），保底回最後一個候選避免因型別誤差回傳 -1。
	return len(denoms) - 1, nil
}

// refArg 把 sourceRegID/sourceRaceID 轉成 wallet.AwardGP/AwardDP 要的 refID（優先用更精確的
// regID，沒有才退回 raceID）；皆為空字串時回 nil（無關聯物件）。
func refArg(sourceRaceID, sourceRegID string) *string {
	ref := sourceRegID
	if ref == "" {
		ref = sourceRaceID
	}
	if ref == "" {
		return nil
	}
	return &ref
}

// grantItem 依項目型別發放單一獎勵（exp/dp/gp/vip；serial 兩層抽獎由 RollAndGrant 直接呼叫
// grantSerialTwoLayer，不經過本函式，因為 serial 需要多回傳實際發出序號的 group id）。回傳空 slice
// 代表「roll 中了但實際沒有可發的東西」（例如區間算出 0），不視為錯誤，呼叫端不會計入 granted 清單。
// 每種型別中獎恰好貢獻一筆 GrantedReward。
func grantItem(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID string, item *RewardItem) ([]GrantedReward, error) {
	ref := refArg(sourceRaceID, sourceRegID)
	switch item.Type {
	case "exp":
		amount, err := randRange(item.Min, item.Max)
		if err != nil {
			return nil, err
		}
		if amount <= 0 {
			return nil, nil
		}
		// EXP 無共用 leaf：比照既有慣例（settlement.go/event_race_goal.go/explore.go 等）一律內聯 UPDATE。
		if _, err := db.Exec(ctx, `UPDATE users SET exp = exp + $1 WHERE id = $2`, amount, userID); err != nil {
			return nil, fmt.Errorf("award exp: %w", err)
		}
		return []GrantedReward{{Type: "exp", Amount: amount}}, nil

	case "dp":
		amount, err := randRange(item.Min, item.Max)
		if err != nil {
			return nil, err
		}
		if amount <= 0 {
			return nil, nil
		}
		if err := wallet.AwardDP(ctx, db, userID, amount, "activity_reward", sourceType, ref); err != nil {
			return nil, fmt.Errorf("award dp: %w", err)
		}
		return []GrantedReward{{Type: "dp", Amount: amount}}, nil

	case "gp":
		amount, err := randRange(item.Min, item.Max)
		if err != nil {
			return nil, err
		}
		if amount <= 0 {
			return nil, nil
		}
		if err := wallet.AwardGP(ctx, db, userID, amount, "activity_reward", sourceType, ref); err != nil {
			return nil, fmt.Errorf("award gp: %w", err)
		}
		return []GrantedReward{{Type: "gp", Amount: amount}}, nil

	case "vip":
		if item.Days <= 0 {
			return nil, nil
		}
		if err := vip.Extend(ctx, db, userID, item.Days); err != nil {
			return nil, fmt.Errorf("extend vip: %w", err)
		}
		return []GrantedReward{{Type: "vip", Days: item.Days}}, nil

	case "coupon":
		return grantCoupon(ctx, db, userID, sourceType, sourceRaceID, sourceRegID, item)

	default:
		// 未知型別（如後台先建了程式尚未支援的型別；理論上已被 Validate 擋下）：安全跳過，不 panic。
		return nil, nil
	}
}

// couponValidUntil 依券種期限模式計算這次中獎發出的券的到期時間。fixed 模式：到期日缺漏或已早於 now
// →(nil,false)代表「當下不可發」；否則直接採用該到期日。days 模式：天數缺漏或 <=0（資料異常保底）
// →(nil,false)；否則 now+N 天。抽成純函式（僅依賴傳入參數，不觸碰 DB／時鐘以外狀態）方便單元測試
// fixed 過期／days 天數換算等邊界情況，供 grantCoupon 呼叫。
func couponValidUntil(expiryMode string, expiresAt *time.Time, validDays *int, now time.Time) (*time.Time, bool) {
	if expiryMode == "fixed" {
		if expiresAt == nil || expiresAt.Before(now) {
			return nil, false
		}
		t := *expiresAt
		return &t, true
	}
	days := 0
	if validDays != nil {
		days = *validDays
	}
	if days <= 0 {
		return nil, false
	}
	t := now.AddDate(0, 0, days)
	return &t, true
}

// grantCoupon coupon 類（migration 138 活動優惠券）：讀券種當下設定，必須 enabled 且（fixed 模式時）
// 尚未過期才實際發放——後台表單雖只列出「當下可選」的券種，但賽事的 reward_config 是存檔快照，
// 券種可能在賽事建立後才被停用/到期，此處是最終把關；不合格一律安靜跳過（不影響其他獎勵項目），
// 比照 grantSerialTwoLayer 商家旗下無庫存時的處理方式。中獎當下把面額/名稱/到期日 denormalize 寫入
// user_rewards（kind='coupon'），供玩家錢包顯示、報名折抵時直接讀該筆記錄無需再 join 券種表。
func grantCoupon(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID string, item *RewardItem) ([]GrantedReward, error) {
	if item.CouponDefID == "" {
		return nil, nil
	}
	var name string
	var amountCents int
	var expiryMode string
	var expiresAt *time.Time
	var validDays *int
	var enabled bool
	err := db.QueryRow(ctx, `
		SELECT name, amount_cents, expiry_mode, expires_at, valid_days, enabled
		FROM event_coupon_defs WHERE id=$1`, item.CouponDefID,
	).Scan(&name, &amountCents, &expiryMode, &expiresAt, &validDays, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // 券種不存在（如已被刪除）：跳過，不視為錯誤
	}
	if err != nil {
		return nil, fmt.Errorf("load coupon def: %w", err)
	}
	if !enabled {
		return nil, nil // 已停用：跳過
	}

	validUntil, ok := couponValidUntil(expiryMode, expiresAt, validDays, time.Now())
	if !ok {
		return nil, nil // 券種當下不可發（fixed 已過期/未設到期日，或 days 天數不合法）：跳過
	}

	var raceIDArg, regIDArg any
	if sourceRaceID != "" {
		raceIDArg = sourceRaceID
	}
	if sourceRegID != "" {
		regIDArg = sourceRegID
	}

	if _, err := db.Exec(ctx, `
		INSERT INTO user_rewards
			(user_id, source_type, source_race_id, source_reg_id, kind, coupon_def_id, amount_cents, item_label, valid_until)
		VALUES ($1,$2,$3,$4,'coupon',$5,$6,$7,$8)`,
		userID, sourceType, raceIDArg, regIDArg, item.CouponDefID, amountCents, name, validUntil,
	); err != nil {
		return nil, fmt.Errorf("insert coupon user_reward: %w", err)
	}
	return []GrantedReward{{Type: "coupon", Amount: amountCents, ItemLabel: name}}, nil
}

// grantSerialTwoLayer serial 類「以商家為單位的兩層抽獎」第二層：item.ProbBP 判定的「該商家中不中獎」
// 已經由 RollAndGrant 呼叫 rollHit 做完（本函式只在「中獎」時才會被呼叫），這裡只處理「中獎後要抽哪一組
// 面額、實際配發」：
//  1. 取 item.validDenominations()（含舊格式 SerialGroupID 的向後相容回退）當候選面額。
//  2. 對每個候選面額在傳入的 tx 內查目前 available 數，只留下「有庫存」的候選組成加權池——缺貨面額
//     不進池，避免抽到注定落空的面額而浪費一次抽獎。
//  3. 加權抽一組（pickWeightedDenomIndex，crypto/rand），對抽中的面額原子搶碼（claimSerialsFromGroup，
//     沿用 UPDATE...FOR UPDATE SKIP LOCKED...RETURNING 手法）。
//  4. 併發回退：若該面額一枚都搶不到（claimSerialsFromGroup 回傳空 slice）——代表「查庫存」與「搶碼」
//     之間被另一筆併發交易把這組搶完了——把該面額移出池、從剩餘有庫存面額重新加權抽一組再試，直到
//     搶到或池空為止。只有「一枚都沒搶到」才觸發回退；只要搶到至少一枚就視為此商家本次已發放完畢
//     （即使 grant_count>1 中途扣光也不回頭改抽別組，維持舊版「庫存中途不足→跳過剩餘配額」的語意）。
//  5. 池空（商家旗下所有候選面額當下都已無庫存或搶碼全部撲空）→ 不發、不視為錯誤。
//
// 同一 serial item（=同一商家）最多只會發出一組面額的序號。回傳的第二個值是「實際發出序號的 group
// id」（沒發成功則為空字串）；本函式刻意不在這裡判斷是否低庫存——理由見套件頂部 model.go 說明。
func grantSerialTwoLayer(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID string, item *RewardItem) ([]GrantedReward, string, error) {
	pool := item.validDenominations()
	if len(pool) == 0 {
		return nil, "", nil
	}

	// 只保留「目前查得有庫存」的面額才進加權池。
	weighted := make([]RewardDenom, 0, len(pool))
	for _, d := range pool {
		var avail int
		if err := db.QueryRow(ctx,
			`SELECT COUNT(*) FILTER (WHERE status='available') FROM reward_serials WHERE group_id=$1`, d.GroupID,
		).Scan(&avail); err != nil {
			return nil, "", fmt.Errorf("check denom stock %s: %w", d.GroupID, err)
		}
		if avail > 0 {
			weighted = append(weighted, d)
		}
	}

	for len(weighted) > 0 {
		idx, err := pickWeightedDenomIndex(weighted)
		if err != nil {
			return nil, "", err
		}
		if idx < 0 {
			break // 理論上不會發生（weighted 內每筆 Weight 皆 >0，見 validDenominations），保底跳出不發
		}
		groupID := weighted[idx].GroupID

		granted, err := claimSerialsFromGroup(ctx, db, userID, sourceType, sourceRaceID, sourceRegID, groupID)
		if err != nil {
			return nil, "", err
		}
		if len(granted) == 0 {
			// 併發撞空：查庫存到搶碼之間被別筆交易搶完，剔除該面額、從剩餘候選重抽（不視為錯誤）。
			weighted = append(weighted[:idx], weighted[idx+1:]...)
			continue
		}
		return granted, groupID, nil
	}
	return nil, "", nil
}

// claimSerialsFromGroup 從 groupID 指定的序號組取該組 grant_count 枚可用序號逐一配發（P1 後台可設「每次
// 中獎配發幾枚序號」，預設 1）：每一枚都各自用 UPDATE...RETURNING 搭配子查詢 FOR UPDATE SKIP LOCKED，
// 單一陳述式內完成「挑一筆可用序號＋標記已發送」，避免併發搶碼衝突（比照 internal/monopoly/draw.go
// claimRedemptionCode 的作法），並各自 INSERT 一筆 user_rewards、各回一筆 GrantedReward。
// 庫存中途不足（某次 claim 查無可用序號）→ 跳過剩餘配額、不視為錯誤，已成功發出的照留；整組一枚都
// 沒搶到（第一枚就落空）時回傳空 slice——呼叫端 grantSerialTwoLayer 靠這個「空 slice」訊號判斷要不要
// 把這個面額從加權池移除、重抽別組。
func claimSerialsFromGroup(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID, groupID string) ([]GrantedReward, error) {
	if groupID == "" {
		return nil, nil
	}

	// 一次查齊 grant_count（配發枚數）與顯示欄位（含 join 商家名稱），避免每枚都重查一次。
	// grant_count 欄位理論上 NOT NULL DEFAULT 1（見 migration 126），<1 視為髒資料保底當 1，
	// 避免因異常資料完全不發獎。序號組不存在（如已被刪除）→ 視為跳過，不算錯誤。
	var grantCount int
	var itemLabel, merchantName, usageNote, iconURL, description string
	var validFrom, validUntil *time.Time
	err := db.QueryRow(ctx, `
		SELECT g.grant_count, COALESCE(g.item_label,''), COALESCE(m.name,''), COALESCE(g.usage_note,''),
		       COALESCE(g.icon_url,''), COALESCE(g.description,''), g.valid_from, g.valid_until
		FROM reward_serial_groups g LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		WHERE g.id = $1`, groupID).Scan(&grantCount, &itemLabel, &merchantName, &usageNote, &iconURL, &description, &validFrom, &validUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // 序號組不存在：跳過，不視為錯誤
	}
	if err != nil {
		return nil, fmt.Errorf("load serial group: %w", err)
	}
	if grantCount < 1 {
		grantCount = 1
	}

	var raceIDArg, regIDArg any
	if sourceRaceID != "" {
		raceIDArg = sourceRaceID
	}
	if sourceRegID != "" {
		regIDArg = sourceRegID
	}

	// denormalize 序號組顯示欄位（含 join 商家名稱）寫進每筆 user_rewards，供玩家活動獎勵錢包(P3)直接
	// 顯示、不必每次都再 join 三張表；即使序號組事後被改名/刪除，玩家錢包內容也不會跟著變動。
	var granted []GrantedReward
	for i := 0; i < grantCount; i++ {
		var serialID, code, link string
		err := db.QueryRow(ctx, `
			UPDATE reward_serials SET status='issued', issued_to=$1, issued_at=NOW()
			WHERE id = (
				SELECT id FROM reward_serials
				WHERE group_id=$2 AND status='available'
				ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
			)
			RETURNING id, code, COALESCE(link,'')`, userID, groupID).Scan(&serialID, &code, &link)
		if errors.Is(err, pgx.ErrNoRows) {
			break // 庫存中途不足：跳過剩餘配額，不視為錯誤；已成功發出的照留
		}
		if err != nil {
			return granted, fmt.Errorf("claim serial: %w", err)
		}

		if _, err := db.Exec(ctx, `
			INSERT INTO user_rewards
				(user_id, source_type, source_race_id, source_reg_id, serial_id, group_id,
				 code, link, item_label, merchant_name, usage_note, icon_url, description, valid_from, valid_until)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			userID, sourceType, raceIDArg, regIDArg, serialID, groupID,
			code, link, itemLabel, merchantName, usageNote, iconURL, description, validFrom, validUntil,
		); err != nil {
			return granted, fmt.Errorf("insert user_reward: %w", err)
		}

		granted = append(granted, GrantedReward{Type: "serial", ItemLabel: itemLabel, Code: code})
	}
	return granted, nil
}

// grantSerialBundle serial 類【固定組合包，migration 149】：item.Bundle 非空時的發放路徑，與兩層抽獎
// grantSerialTwoLayer 互斥（RollAndGrant 依 len(item.Bundle)>0 二擇一呼叫；validate.go 已擋掉兩者同時
// 設定）。ProbBP 中獎判定已由 RollAndGrant 呼叫 rollHit 做完（本函式只在「中獎」時才會被呼叫）——中獎後
// 不是「加權抽一個」而是「全發」：對每個 entry 各自搶 entry.Count 張序號，全部歸同一次發放（同一
// bundle_id）。
//
// all-or-nothing（使用者拍板）：明確拆成兩個階段，不是「邊搶邊發現不夠再回滾」——
//  1. 鎖定＋確認：對每個 entry 用 SELECT...FOR UPDATE SKIP LOCKED 一次鎖住最多 entry.Count 筆該組目前
//     available 的序號（lockAvailableSerialIDs），鎖到幾筆就是「目前真的搶得到」幾筆；只鎖不改狀態。
//     firstInsufficientBundleEntry 純函式判斷是否每個 entry 都鎖到足夠張數。
//  2. 只有全部 entry 都足夠，才進入實際配發：對每個 entry 已鎖定的序號逐一 UPDATE 標記 issued＋INSERT
//     user_rewards（此時序號已經是本交易獨佔鎖住的，不會再被搶走，必定成功）。
//     任一 entry 不足 → 完全不進入配發階段、直接 notify.Alert 告警＋回傳 error；階段 1 已經 SELECT FOR
//     UPDATE 鎖住的序號（若有）維持「僅本交易可見的鎖定」，本函式全程不自己開/關交易，呼叫鏈最上層（如
//     personal_progress.go MarkAttemptCompletedAndGrant）的 defer tx.Rollback(ctx) 會在收到本函式回傳
//     的 error 後整個 Rollback，釋放這些鎖、確保序號狀態完全沒被動過——不會有「這組扣了、那組沒扣」的
//     部分發放。notify.Alert 內部用獨立 context/goroutine 送出 Telegram，不受本交易稍後 Rollback 影響
//     （見 notify.Alert 文件註解），告警本身不會因為交易回滾而消失。
//
// 同商家把關：Validate 只能做結構驗證（entry 數 1-20、group_id/count 合法），無法在不查 DB 的情況下確認
// 每個 group_id 實際隸屬哪個商家，這項規則因此挪到本函式執行期——查出每個 entry 的 merchant_id 後跨
// entry 比對，不一致視為設定錯誤直接 fail（一般 error，非 all-or-nothing 的「庫存不足」情境，不觸發
// serial_bundle_shortage 告警——這是後台設定錯了，不是業主庫存問題，回報方式應該不同）。
//
// 冪等：與其餘發放路徑一致——RollAndGrant 只會在呼叫端已用 CAS 確認「這次呼叫確定是首次判定完成」時才
// 呼叫一次（見套件頂部說明），本函式不自建冪等機制，也不查詢/依賴「這個 item 是否已經發過」；不會與序號
// 組自身的 grant_count（兩層抽獎專用設定，見 claimSerialsFromGroup）混淆——組合包完全不讀 grant_count，
// 每個 entry 發幾張只看 entry.Count。
//
// 回傳的第二個值是本次組合包「每個 entry 各自的 group id」（validate.go 已擋掉同一 bundle 內重複
// group_id，實務上不會重複），供呼叫端（RollAndGrant）併入 issuedGroupIDs，供 commit 後的低庫存檢查。
func grantSerialBundle(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID string, item *RewardItem) ([]GrantedReward, []string, error) {
	if len(item.Bundle) == 0 {
		return nil, nil, nil
	}

	// bundle_id 由 DB 產生一次，本次組合包所有 entry 發出的序號共用同一個值（前台錢包 group by 併卡，見
	// migration 149 idx_user_rewards_bundle）。
	var bundleID string
	if err := db.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&bundleID); err != nil {
		return nil, nil, fmt.Errorf("gen bundle id: %w", err)
	}

	type bundleEntryMeta struct {
		groupID, itemLabel, merchantName, usageNote, iconURL, description string
		merchantID                                                       *string
		validFrom, validUntil                                            *time.Time
		faceValue, count                                                 int
	}
	metas := make([]bundleEntryMeta, len(item.Bundle))
	var commonMerchant *string
	merchantMismatch := false
	for i, e := range item.Bundle {
		m := bundleEntryMeta{groupID: e.GroupID, count: e.Count}
		err := db.QueryRow(ctx, `
			SELECT g.merchant_id, COALESCE(g.item_label,''), COALESCE(mc.name,''), COALESCE(g.usage_note,''),
			       COALESCE(g.icon_url,''), COALESCE(g.description,''), g.valid_from, g.valid_until, g.face_value
			FROM reward_serial_groups g LEFT JOIN reward_merchants mc ON mc.id = g.merchant_id
			WHERE g.id = $1`, e.GroupID,
		).Scan(&m.merchantID, &m.itemLabel, &m.merchantName, &m.usageNote, &m.iconURL, &m.description,
			&m.validFrom, &m.validUntil, &m.faceValue)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("grant serial bundle: group %s not found", e.GroupID)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("load bundle entry group %s: %w", e.GroupID, err)
		}
		if i == 0 {
			commonMerchant = m.merchantID
		} else if !samePtrString(commonMerchant, m.merchantID) {
			merchantMismatch = true
		}
		metas[i] = m
	}
	if merchantMismatch {
		return nil, nil, fmt.Errorf("grant serial bundle: entries reference different merchants (item merchant_id=%s)", item.MerchantID)
	}

	// 面額防呆（2026-08-28 對抗性審查發現）：組合包總額/標籤只信 DB 的 face_value 結構化欄位，若任一
	// entry 的序號組 face_value<=0（管理員漏設），會算出 bundle_total=0 → 玩家錢包顯示「LINE POINTS 0」、
	// 與後台當初靠名稱解析看到的數字不一致，且無人察覺。此處視為設定錯誤：整包不發、告警（獨立 kind，
	// 非 shortage——這不是庫存問題而是設定缺漏，補庫存無用，要補的是序號組面額）。序號在階段 1 才鎖定，
	// 此刻尚未動任何序號狀態，直接 return 不會有殘留。
	for _, m := range metas {
		if m.faceValue <= 0 {
			notify.Alert("serial_bundle_facevalue_missing",
				"組合包序號組未設定面額",
				fmt.Sprintf("序號組 %s（%s）的 face_value 未設定，組合包無法計算總額，本次未發放。請到序號/獎勵管理補上面額。", m.groupID, m.itemLabel))
			return nil, nil, fmt.Errorf("grant serial bundle: group %s has face_value<=0 (unset), refuse to grant", m.groupID)
		}
	}

	faceValueByGroup := make(map[string]int, len(metas))
	for _, m := range metas {
		faceValueByGroup[m.groupID] = m.faceValue
	}
	bundleTotal := computeBundleTotal(item.Bundle, faceValueByGroup)
	// bundleLabel：優先用面額組共同的商家名稱（"{商家名} {總額}"，如「LINE POINTS 3500」）；商家未指定
	// 時退回固定前綴「LINE POINTS」（P1 組合包當下唯一實際用例即 LINE POINTS，見 apps/web api.ts
	// RewardBundleEntry 註解）。與 race/reward_preview.go 前台預覽卡片沿用同一套 FormatBundleLabel，
	// 確保玩家事前看到的名稱跟實際中獎後拿到的一致。
	bundleLabel := FormatBundleLabel(metas[0].merchantName, bundleTotal)

	// 階段 1：鎖定＋確認每個 entry 的庫存足不足，只鎖不改狀態（見函式文件註解）。
	need := make([]int, len(metas))
	lockedIDs := make([][]string, len(metas))
	for i, m := range metas {
		need[i] = m.count
		ids, err := lockAvailableSerialIDs(ctx, db, m.groupID, m.count)
		if err != nil {
			return nil, nil, fmt.Errorf("lock bundle stock for group %s: %w", m.groupID, err)
		}
		lockedIDs[i] = ids
	}
	locked := make([]int, len(metas))
	for i, ids := range lockedIDs {
		locked[i] = len(ids)
	}
	if idx := firstInsufficientBundleEntry(need, locked); idx >= 0 {
		m := metas[idx]
		notify.Alert("serial_bundle_shortage", "序號組合包庫存不足，整包未發放",
			fmt.Sprintf("merchant=%s bundle_label=%s group_id=%s item_label=%s 需求=%d 目前可鎖定=%d",
				metas[0].merchantName, bundleLabel, m.groupID, m.itemLabel, m.count, locked[idx]))
		return nil, nil, fmt.Errorf("grant serial bundle: insufficient stock in group %s (need %d, got %d)", m.groupID, m.count, locked[idx])
	}

	// 階段 2：全部 entry 都確認足夠，才實際配發——此時的序號都已是本交易獨佔鎖住的，逐一標記 issued 必定
	// 成功（見函式文件註解）。
	var raceIDArg, regIDArg any
	if sourceRaceID != "" {
		raceIDArg = sourceRaceID
	}
	if sourceRegID != "" {
		regIDArg = sourceRegID
	}

	var granted []GrantedReward
	var groupIDs []string
	for i, m := range metas {
		groupIDs = append(groupIDs, m.groupID)
		for _, serialID := range lockedIDs[i] {
			var code, link string
			if err := db.QueryRow(ctx, `
				UPDATE reward_serials SET status='issued', issued_to=$1, issued_at=NOW()
				WHERE id=$2
				RETURNING code, COALESCE(link,'')`, userID, serialID).Scan(&code, &link); err != nil {
				return nil, nil, fmt.Errorf("claim bundle serial %s: %w", serialID, err)
			}

			if _, err := db.Exec(ctx, `
				INSERT INTO user_rewards
					(user_id, source_type, source_race_id, source_reg_id, serial_id, group_id,
					 code, link, item_label, merchant_name, usage_note, icon_url, description, valid_from, valid_until,
					 bundle_id, bundle_label, bundle_total)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
				userID, sourceType, raceIDArg, regIDArg, serialID, m.groupID,
				code, link, m.itemLabel, m.merchantName, m.usageNote, m.iconURL, m.description, m.validFrom, m.validUntil,
				bundleID, bundleLabel, bundleTotal,
			); err != nil {
				return nil, nil, fmt.Errorf("insert bundle user_reward: %w", err)
			}

			granted = append(granted, GrantedReward{Type: "serial", ItemLabel: m.itemLabel, Code: code})
		}
	}
	return granted, groupIDs, nil
}

// lockAvailableSerialIDs 用 SELECT...FOR UPDATE SKIP LOCKED 一次鎖住 groupID 這組目前最多 count 筆
// status='available' 的序號 id（只鎖不改狀態），供 grantSerialBundle 階段 1「確認庫存」使用。SKIP LOCKED
// 確保不會跟其他併發交易搶同一批序號而卡住；回傳的 slice 長度可能小於 count（代表目前搶得到的就這麼多），
// 由呼叫端（firstInsufficientBundleEntry）判斷是否足夠。ORDER BY created_at 與既有 claimSerialsFromGroup
// 同一套「先進先出」慣例。
func lockAvailableSerialIDs(ctx context.Context, db Execer, groupID string, count int) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT id FROM reward_serials
		WHERE group_id=$1 AND status='available'
		ORDER BY created_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, groupID, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// firstInsufficientBundleEntry 純函式：need[i] 是第 i 個 entry 需要的張數，locked[i] 是階段 1 實際鎖到
// 的張數；依序找出第一個「鎖到的不足所需」的索引，全部足夠則回 -1。這是 all-or-nothing 判斷的核心規則
// ——只要有一個 entry 不足，整包就不該發——抽成不碰 DB 的純函式方便單元測試邊界情況（全部剛好足夠／
// 只有最後一個 entry 短缺／第一個 entry 就短缺／need 與 locked 長度不一致的防呆）。
func firstInsufficientBundleEntry(need, locked []int) int {
	for i, n := range need {
		got := 0
		if i < len(locked) {
			got = locked[i]
		}
		if got < n {
			return i
		}
	}
	return -1
}

// computeBundleTotal 純函式：Σ(faceValueByGroup[entry.GroupID] × entry.Count)，即組合包的合併總面額
// （bundle_total，見 migration 149 契約）。查不到面額（faceValueByGroup 缺該 group_id，理論上不會發生，
// 因為呼叫端一定會先把每個 entry 涉及的 group 都查過一輪才會走到這裡）視為面額 0，不 panic。
func computeBundleTotal(entries []BundleEntry, faceValueByGroup map[string]int) int {
	total := 0
	for _, e := range entries {
		total += faceValueByGroup[e.GroupID] * e.Count
	}
	return total
}

// FormatBundleLabel 組合包顯示名稱："{商家名} {總額}"（如「LINE POINTS 3500」）；商家名稱為空時退回固定
// 前綴「LINE POINTS」（P1 組合包當下唯一實際用例，見 apps/web api.ts RewardBundleEntry 註解），避免顯示
// 空白標籤。匯出供 race/reward_preview.go 前台預覽卡片沿用同一套算法——玩家事前在「活動獎勵」頁籤看到的
// 卡片名稱，必須跟實際中獎後 user_rewards.bundle_label 一致，不要兩邊各算一套导致對不上。
func FormatBundleLabel(merchantName string, total int) string {
	prefix := merchantName
	if prefix == "" {
		prefix = "LINE POINTS"
	}
	return fmt.Sprintf("%s %d", prefix, total)
}

// samePtrString 比較兩個可能為 nil 的字串指標是否代表同一值：皆 nil 視為相同（都未指定商家）；一 nil
// 一非 nil 視為不同；皆非 nil 則比較實際值。供 grantSerialBundle 判斷 bundle 內各 entry 的 merchant_id
// 是否一致。
func samePtrString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
