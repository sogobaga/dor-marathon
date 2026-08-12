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

	default:
		// 未知型別（如後台先建了程式尚未支援的型別；理論上已被 Validate 擋下）：安全跳過，不 panic。
		return nil, nil
	}
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
	var validUntil *time.Time
	err := db.QueryRow(ctx, `
		SELECT g.grant_count, COALESCE(g.item_label,''), COALESCE(m.name,''), COALESCE(g.usage_note,''),
		       COALESCE(g.icon_url,''), COALESCE(g.description,''), g.valid_until
		FROM reward_serial_groups g LEFT JOIN reward_merchants m ON m.id = g.merchant_id
		WHERE g.id = $1`, groupID).Scan(&grantCount, &itemLabel, &merchantName, &usageNote, &iconURL, &description, &validUntil)
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
				 code, link, item_label, merchant_name, usage_note, icon_url, description, valid_until)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			userID, sourceType, raceIDArg, regIDArg, serialID, groupID,
			code, link, itemLabel, merchantName, usageNote, iconURL, description, validUntil,
		); err != nil {
			return granted, fmt.Errorf("insert user_reward: %w", err)
		}

		granted = append(granted, GrantedReward{Type: "serial", ItemLabel: itemLabel, Code: code})
	}
	return granted, nil
}
