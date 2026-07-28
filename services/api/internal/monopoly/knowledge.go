package monopoly

// knowledge.go 知識卡圖鑑讀取（供 Phase 2a 圖鑑頁）。純讀取，不異動任何資料。

import (
	"context"
	"fmt"
)

// GetKnowledgeCards 回傳使用者的知識卡蒐集進度 + 全部卡片列表（依 theme, sort_order 排序）。
// 防劇透：每張卡一律回 id/theme/main_category/rarity/owned/obtained_count；只有 owned=true 才
// 額外附上展示欄位（title/body/...），未擁有的卡片不落地展示內容到回應裡（不是前端隱藏，是後端
// 根本不回傳），前端就只能顯示「？」卡背。
func (r *Repository) GetKnowledgeCards(ctx context.Context, userID string) (*KnowledgeGallery, error) {
	counts := KnowledgeCounts{}
	countRows, err := r.db.Query(ctx, `
		SELECT c.theme, COUNT(*) AS total, COUNT(u.card_id) AS owned
		FROM knowledge_card_defs c
		LEFT JOIN user_knowledge_cards u ON u.card_id = c.id AND u.user_id = $1
		WHERE c.is_active = true
		GROUP BY c.theme`, userID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge counts: %w", err)
	}
	for countRows.Next() {
		var theme string
		var total, owned int
		if err := countRows.Scan(&theme, &total, &owned); err != nil {
			countRows.Close()
			return nil, fmt.Errorf("scan knowledge counts: %w", err)
		}
		switch theme {
		case "training":
			counts.TrainingTotal, counts.TrainingOwned = total, owned
		case "care":
			counts.CareTotal, counts.CareOwned = total, owned
		}
	}
	countRows.Close()
	if err := countRows.Err(); err != nil {
		return nil, err
	}

	cardRows, err := r.db.Query(ctx, `
		SELECT c.id, c.theme, c.main_category, c.rarity,
		       COALESCE(u.obtained_count, 0) AS obtained_count,
		       (u.card_id IS NOT NULL) AS owned,
		       c.code, c.subtopic, c.title, c.body, c.player_action, c.timing, c.importance,
		       c.handling_level, c.game_effect_hint, c.risk_note, c.source_org, c.source_doc, c.source_url, c.image_url
		FROM knowledge_card_defs c
		LEFT JOIN user_knowledge_cards u ON u.card_id = c.id AND u.user_id = $1
		WHERE c.is_active = true
		ORDER BY c.theme, c.sort_order`, userID)
	if err != nil {
		return nil, fmt.Errorf("load knowledge cards: %w", err)
	}
	defer cardRows.Close()

	var cards []KnowledgeCardEntry
	for cardRows.Next() {
		var e KnowledgeCardEntry
		var code, subtopic, title, body, playerAction, timing, importance string
		var handlingLevel, gameEffectHint, riskNote, sourceOrg, sourceDoc, sourceURL, imageURL string
		if err := cardRows.Scan(
			&e.ID, &e.Theme, &e.MainCategory, &e.Rarity, &e.ObtainedCount, &e.Owned,
			&code, &subtopic, &title, &body, &playerAction, &timing, &importance,
			&handlingLevel, &gameEffectHint, &riskNote, &sourceOrg, &sourceDoc, &sourceURL, &imageURL,
		); err != nil {
			return nil, fmt.Errorf("scan knowledge card: %w", err)
		}
		if e.Owned {
			e.Code, e.Subtopic, e.Title, e.Body = code, subtopic, title, body
			e.PlayerAction, e.Timing, e.Importance = playerAction, timing, importance
			e.HandlingLevel, e.GameEffectHint, e.RiskNote = handlingLevel, gameEffectHint, riskNote
			e.SourceOrg, e.SourceDoc, e.SourceURL, e.ImageURL = sourceOrg, sourceDoc, sourceURL, imageURL
		}
		cards = append(cards, e)
	}
	if err := cardRows.Err(); err != nil {
		return nil, err
	}

	return &KnowledgeGallery{Counts: counts, Cards: cards}, nil
}
