package monopoly

// draw.go 機會/命運抽卡引擎（A2）。resolveDraw 由 Repository.Roll 在既有交易內呼叫（見 repository.go），
// 靠 Roll 已鎖住的 monopoly_player_state 列序列化同一使用者的併發請求——本檔案內一律不開新交易、
// 不做「事後補抽」，所有 SQL 都吃呼叫端傳入的 pgx.Tx。

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/vip"
	"github.com/dor/api/internal/wallet"
)

// rarityDrawWeight 知識卡/貼紙依稀有度的抽獎權重（common 遠高於 rare，符合直覺）。
var rarityDrawWeight = map[string]int{"common": 10, "rare": 3}

// rarityWeight 未知稀有度給保底權重 1，避免因設定/資料誤植（拼字錯的 rarity 值）讓整批候選被排除
// 導致無法抽獎；不可 panic、不可讓抽獎流程因單一髒資料整個失敗。
func rarityWeight(rarity string) int {
	if w, ok := rarityDrawWeight[rarity]; ok && w > 0 {
		return w
	}
	return 1
}

// dupGPForRarity 回傳重複收集品（知識卡/貼紙）依稀有度轉發的 GP。若 rarity 不在設定 map 內
// （髒資料，如未來新增未對照的稀有度），退回 common 費率，避免玩家抽到重複卻完全沒有補償。
// 用 comma-ok 判斷「鍵是否存在」而非「值是否為 0」，保留後台刻意把某稀有度設成 0 的意圖。
func dupGPForRarity(dupGP map[string]int, rarity string) int {
	if gp, ok := dupGP[rarity]; ok {
		return gp
	}
	return dupGP["common"]
}

// pickWeighted 加權隨機挑一個元素，全程 crypto/rand（不可用 math/rand——防作弊關鍵，比照
// secureRoll 的作法）：權重加總 → 在 [0,total) 均勻取一點 → 依序累加權重定位落點所在的區間。
// items 為空或權重總和 <=0 時回 nil, nil（呼叫端視為「沒有可抽的東西」，不是錯誤）。
func pickWeighted[T any](items []T, weightOf func(T) int) (*T, error) {
	total := 0
	for _, it := range items {
		if w := weightOf(it); w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return nil, nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(total)))
	if err != nil {
		return nil, fmt.Errorf("weighted pick rand: %w", err)
	}
	target := n.Int64()
	var acc int64
	for i := range items {
		w := weightOf(items[i])
		if w <= 0 {
			continue
		}
		acc += int64(w)
		if target < acc {
			return &items[i], nil
		}
	}
	// 理論上不會走到（累加到 total 必定 > target），保底回最後一個候選避免因浮點/型別誤差回傳 nil。
	return &items[len(items)-1], nil
}

// poolEntry monopoly_pool_entries 一列（只取抽獎需要的欄位）。
type poolEntry struct {
	RewardType         string
	Weight             int
	Amount             int
	RedemptionBatchKey string
}

func (r *Repository) loadPoolEntries(ctx context.Context, tx pgx.Tx, pool string) ([]poolEntry, error) {
	rows, err := tx.Query(ctx, `
		SELECT reward_type, weight, amount, redemption_batch_key
		FROM monopoly_pool_entries
		WHERE pool=$1 AND is_active AND weight>0`, pool)
	if err != nil {
		return nil, fmt.Errorf("load pool entries: %w", err)
	}
	defer rows.Close()
	var out []poolEntry
	for rows.Next() {
		var e poolEntry
		if err := rows.Scan(&e.RewardType, &e.Weight, &e.Amount, &e.RedemptionBatchKey); err != nil {
			return nil, fmt.Errorf("scan pool entry: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// resolveDraw 停在機會/命運格後的抽獎主流程：加權挑一個 reward_type → 依型別分派發放 → 寫
// monopoly_draws 流水。回傳 nil, nil 代表「本次視為未抽到」（池內無可用項目，或知識卡/貼紙該類別
// 暫無 active 資料）——這種情況刻意不補償、不寫流水，語意等同「這個池目前是空的」。
func (r *Repository) resolveDraw(ctx context.Context, tx pgx.Tx, userID, pool string, dupGP map[string]int, redeemFallbackGP int) (*DrawResult, error) {
	entries, err := r.loadPoolEntries(ctx, tx, pool)
	if err != nil {
		return nil, err
	}
	entry, err := pickWeighted(entries, func(e poolEntry) int { return e.Weight })
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	// drawID 先產生，供本次發放的 gp_events/流水關聯，也是 monopoly_draws 的主鍵。
	var drawID string
	if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&drawID); err != nil {
		return nil, fmt.Errorf("gen draw id: %w", err)
	}

	dr := &DrawResult{Type: entry.RewardType}
	refType := ""
	var refID *string
	amount := entry.Amount
	isDuplicate := false
	convertedGP := 0

	switch entry.RewardType {
	case "gp":
		if err := wallet.AwardGP(ctx, tx, userID, amount, "monopoly_draw", "monopoly_draw", &drawID); err != nil {
			return nil, fmt.Errorf("award draw gp: %w", err)
		}
		dr.Amount = amount

	case "dp":
		if err := wallet.AwardDP(ctx, tx, userID, amount, "monopoly_draw", "monopoly_draw", &drawID); err != nil {
			return nil, fmt.Errorf("award draw dp: %w", err)
		}
		dr.Amount = amount

	case "vip_days":
		if err := vip.Extend(ctx, tx, userID, amount); err != nil {
			return nil, fmt.Errorf("extend vip: %w", err)
		}
		dr.Amount = amount

	case "knowledge_card":
		theme := "training"
		if pool == "destiny" {
			theme = "care"
		}
		card, err := r.drawKnowledgeCard(ctx, tx, userID, drawID, theme, dupGP)
		if err != nil {
			return nil, err
		}
		if card == nil {
			// 該主題目前無 active 卡片：視為未抽到，不補償（與「池無 entry」一致，見函式註解）。
			return nil, nil
		}
		refType, refID = "knowledge_card", &card.id
		amount, isDuplicate, convertedGP = 0, card.isDuplicate, card.convertedGP
		dr.Title, dr.Body, dr.ImageURL, dr.Rarity = card.title, card.body, card.imageURL, card.rarity
		dr.MainCategory, dr.Subtopic = card.mainCategory, card.subtopic
		dr.PlayerAction, dr.RiskNote, dr.SourceOrg, dr.SourceURL = card.playerAction, card.riskNote, card.sourceOrg, card.sourceURL
		dr.IsDuplicate, dr.ConvertedGP = isDuplicate, convertedGP

	case "sticker":
		piece, err := r.drawSticker(ctx, tx, userID, drawID, dupGP)
		if err != nil {
			return nil, err
		}
		if piece == nil {
			return nil, nil
		}
		refType, refID = "sticker", &piece.id
		amount, isDuplicate, convertedGP = 0, piece.isDuplicate, piece.convertedGP
		dr.Title, dr.ImageURL, dr.Rarity = piece.title, piece.imageURL, piece.rarity
		dr.IsDuplicate, dr.ConvertedGP = isDuplicate, convertedGP

	case "redemption_code":
		codeID, code, kind, label, err := r.claimRedemptionCode(ctx, tx, userID, entry.RedemptionBatchKey)
		if err != nil {
			return nil, err
		}
		if code == "" {
			// 庫存耗盡 fallback：改發固定 GP，並在 DrawResult 標示 is_fallback 供前端顯示「已改發 N GP」。
			convertedGP = redeemFallbackGP
			if err := wallet.AwardGP(ctx, tx, userID, convertedGP, "monopoly_draw_fallback", "monopoly_draw", &drawID); err != nil {
				return nil, fmt.Errorf("award redemption fallback gp: %w", err)
			}
			dr.IsFallback = true
			dr.ConvertedGP = convertedGP
			amount = 0
		} else {
			refType, refID = "redemption_code", &codeID
			dr.Code, dr.Title, dr.Kind = code, label, kind
			amount = 0
		}

	default:
		// 未知獎勵型別（例如後台先建了程式尚未支援的型別）：安全跳過，不寫流水、不 panic。
		return nil, nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO monopoly_draws (id, user_id, pool, reward_type, reward_ref_type, reward_ref_id, amount, is_duplicate, converted_gp)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		drawID, userID, pool, entry.RewardType, refType, refID, amount, isDuplicate, convertedGP,
	); err != nil {
		return nil, fmt.Errorf("insert draw record: %w", err)
	}

	return dr, nil
}

// collectibleResult drawKnowledgeCard/drawSticker 共用的內部回傳形狀。
type collectibleResult struct {
	id, title, body, imageURL, rarity, mainCategory, subtopic string
	playerAction, riskNote, sourceOrg, sourceURL              string
	isDuplicate                                               bool
	convertedGP                                               int
}

// drawKnowledgeCard 依稀有度加權挑一張 theme 主題的 active 知識卡；已擁有(user_knowledge_cards 已有列)
// 視為重複→依 dupGP[rarity] 轉 GP 並累加 obtained_count，未擁有則發卡（INSERT）。回傳 nil, nil 代表
// 該主題目前沒有任何 active 卡片可抽。
func (r *Repository) drawKnowledgeCard(ctx context.Context, tx pgx.Tx, userID, drawID, theme string, dupGP map[string]int) (*collectibleResult, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, title, body, image_url, rarity, main_category, subtopic, player_action, risk_note, source_org, source_url
		FROM knowledge_card_defs WHERE theme=$1 AND is_active=true`, theme)
	if err != nil {
		return nil, fmt.Errorf("load knowledge cards: %w", err)
	}
	var cands []collectibleResult
	for rows.Next() {
		var c collectibleResult
		if err := rows.Scan(&c.id, &c.title, &c.body, &c.imageURL, &c.rarity, &c.mainCategory, &c.subtopic, &c.playerAction, &c.riskNote, &c.sourceOrg, &c.sourceURL); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan knowledge card: %w", err)
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, nil
	}

	picked, err := pickWeighted(cands, func(c collectibleResult) int { return rarityWeight(c.rarity) })
	if err != nil {
		return nil, err
	}
	if picked == nil {
		return nil, nil
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO user_knowledge_cards (user_id, card_id) VALUES ($1,$2)
		ON CONFLICT (user_id, card_id) DO NOTHING`, userID, picked.id)
	if err != nil {
		return nil, fmt.Errorf("insert user knowledge card: %w", err)
	}
	picked.isDuplicate = tag.RowsAffected() == 0
	if picked.isDuplicate {
		if _, err := tx.Exec(ctx, `
			UPDATE user_knowledge_cards SET obtained_count=obtained_count+1, last_obtained_at=NOW()
			WHERE user_id=$1 AND card_id=$2`, userID, picked.id); err != nil {
			return nil, fmt.Errorf("bump user knowledge card: %w", err)
		}
		picked.convertedGP = dupGPForRarity(dupGP, picked.rarity)
		if picked.convertedGP > 0 {
			if err := wallet.AwardGP(ctx, tx, userID, picked.convertedGP, "monopoly_draw_dup", "monopoly_draw", &drawID); err != nil {
				return nil, fmt.Errorf("award dup gp: %w", err)
			}
		}
	}
	return picked, nil
}

// drawSticker 完賽公仔九宮格拼圖抽獎，邏輯與 drawKnowledgeCard 對稱（先支援、預設池目前沒有此
// reward_type，但要能跑）：不分主題，從所有 active sticker_pieces 依稀有度加權挑一片。
func (r *Repository) drawSticker(ctx context.Context, tx pgx.Tx, userID, drawID string, dupGP map[string]int) (*collectibleResult, error) {
	rows, err := tx.Query(ctx, `SELECT id, title, image_url, rarity FROM sticker_pieces WHERE is_active=true`)
	if err != nil {
		return nil, fmt.Errorf("load sticker pieces: %w", err)
	}
	var cands []collectibleResult
	for rows.Next() {
		var c collectibleResult
		if err := rows.Scan(&c.id, &c.title, &c.imageURL, &c.rarity); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan sticker piece: %w", err)
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, nil
	}

	picked, err := pickWeighted(cands, func(c collectibleResult) int { return rarityWeight(c.rarity) })
	if err != nil {
		return nil, err
	}
	if picked == nil {
		return nil, nil
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO user_stickers (user_id, piece_id) VALUES ($1,$2)
		ON CONFLICT (user_id, piece_id) DO NOTHING`, userID, picked.id)
	if err != nil {
		return nil, fmt.Errorf("insert user sticker: %w", err)
	}
	picked.isDuplicate = tag.RowsAffected() == 0
	if picked.isDuplicate {
		if _, err := tx.Exec(ctx, `
			UPDATE user_stickers SET obtained_count=obtained_count+1, last_obtained_at=NOW()
			WHERE user_id=$1 AND piece_id=$2`, userID, picked.id); err != nil {
			return nil, fmt.Errorf("bump user sticker: %w", err)
		}
		picked.convertedGP = dupGPForRarity(dupGP, picked.rarity)
		if picked.convertedGP > 0 {
			if err := wallet.AwardGP(ctx, tx, userID, picked.convertedGP, "monopoly_draw_dup", "monopoly_draw", &drawID); err != nil {
				return nil, fmt.Errorf("award dup gp: %w", err)
			}
		}
	}
	return picked, nil
}

// claimRedemptionCode 從 batchKey 對應的兌換碼庫存池取一筆未用碼並標記已用。FOR UPDATE SKIP LOCKED
// 確保併發請求不會把同一碼發給兩個人（拿不到就跳過被鎖住的列，去搶下一筆，而不是卡住等待）。
// 回傳 code=="" 代表庫存耗盡（非錯誤，err 為 nil），呼叫端走 fallback。
func (r *Repository) claimRedemptionCode(ctx context.Context, tx pgx.Tx, userID, batchKey string) (id, code, kind, label string, err error) {
	err = tx.QueryRow(ctx, `
		UPDATE monopoly_redemption_codes SET is_used=true, used_by=$2, used_at=NOW()
		WHERE id = (
			SELECT id FROM monopoly_redemption_codes
			WHERE batch_key=$1 AND is_used=false
			ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		RETURNING id, code, kind, label`,
		batchKey, userID,
	).Scan(&id, &code, &kind, &label)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", "", nil
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("claim redemption code: %w", err)
	}
	return id, code, kind, label, nil
}
