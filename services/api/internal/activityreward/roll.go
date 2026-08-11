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
// wallet.AwardDP/AwardGP、vip 走 vip.Extend（皆直接入帳，不進 user_rewards）；serial 從指定序號組依該組
// grant_count 取對應枚數的可用序號配發並寫入 user_rewards（見 grantSerial）。cfg 為 nil 或無項目 →
// no-op，回傳空結果。回傳所有「實際中獎且成功發放」的項目（serial 中獎時可能一次貢獻多筆，見
// grantSerial）；serial 類查無可用序號時該項目直接跳過（不算錯誤，其餘項目照常各自獨立 roll）。
func RollAndGrant(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID string, cfg *RewardConfig) ([]GrantedReward, error) {
	if cfg == nil || len(cfg.Items) == 0 {
		return nil, nil
	}
	var granted []GrantedReward
	for i := range cfg.Items {
		item := &cfg.Items[i]
		hit, err := rollHit(item.ProbBP)
		if err != nil {
			return granted, fmt.Errorf("roll item %d (%s): %w", i, item.Type, err)
		}
		if !hit {
			continue
		}
		gs, err := grantItem(ctx, db, userID, sourceType, sourceRaceID, sourceRegID, item)
		if err != nil {
			return granted, fmt.Errorf("grant item %d (%s): %w", i, item.Type, err)
		}
		granted = append(granted, gs...)
	}
	return granted, nil
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

// grantItem 依項目型別發放單一獎勵；回傳空 slice 代表「roll 中了但實際沒有可發的東西」（例如區間算出
// 0、或 serial 組目前無庫存），不視為錯誤，呼叫端不會計入 granted 清單。除 serial 外每種型別中獎恰好
// 貢獻一筆 GrantedReward；serial 依序號組 grant_count 可能一次貢獻多筆（見 grantSerial）。
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

	case "serial":
		return grantSerial(ctx, db, userID, sourceType, sourceRaceID, sourceRegID, item.SerialGroupID)

	default:
		// 未知型別（如後台先建了程式尚未支援的型別；理論上已被 Validate 擋下）：安全跳過，不 panic。
		return nil, nil
	}
}

// grantSerial 從 groupID 指定的序號組取該組 grant_count 枚可用序號逐一配發（P1 後台可設「每次中獎配發
// 幾枚序號」，預設 1）：每一枚都各自用 UPDATE...RETURNING 搭配子查詢 FOR UPDATE SKIP LOCKED，單一陳述式
// 內完成「挑一筆可用序號＋標記已發送」，避免併發搶碼衝突（比照 internal/monopoly/draw.go
// claimRedemptionCode 的作法），並各自 INSERT 一筆 user_rewards、各回一筆 GrantedReward。
// 庫存中途不足（某次 claim 查無可用序號）→ 跳過剩餘配額、不視為錯誤，已成功發出的照留（決策③：
// 序號類中獎但組已發完→跳過不發，不影響其他項目繼續 roll）；整組一枚都沒有時回傳空 slice。
func grantSerial(ctx context.Context, db Execer, userID, sourceType, sourceRaceID, sourceRegID, groupID string) ([]GrantedReward, error) {
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
