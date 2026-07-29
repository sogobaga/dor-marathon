package monopoly

import (
	"context"
	"encoding/json"

	"github.com/dor/api/internal/appsettings"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetState(ctx context.Context, userID string) (*PlayerState, error) {
	st, err := s.repo.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	st.DiceGPCost = diceGPCost(ctx, s)
	return st, nil
}

func (s *Service) Roll(ctx context.Context, userID string) (*RollResult, error) {
	cost := diceGPCost(ctx, s)
	lapReward := appsettings.GetInt(ctx, s.repo.db, "monopoly_lap_reward_gp", 0)
	dupGP := monopolyDupGP(ctx, s)
	fallbackGP := appsettings.GetInt(ctx, s.repo.db, "monopoly_redeem_fallback_gp", 10)
	return s.repo.Roll(ctx, userID, cost, lapReward, dupGP, fallbackGP)
}

// GetKnowledgeCards 知識卡圖鑑（供 Phase 2a 圖鑑頁），純讀取，直接透傳 repo 結果。
func (s *Service) GetKnowledgeCards(ctx context.Context, userID string) (*KnowledgeGallery, error) {
	return s.repo.GetKnowledgeCards(ctx, userID)
}

// GetStickers 完賽公仔九宮格收集狀態（供 Phase 2b 收集頁）。pieces 由 repo 純讀取組裝；
// title/figure_url/line_oa/landing_url 讀 app_settings，查無設定時退回與
// 105_finisher_figure_stickers.sql／107_monopoly_figure_redeem.sql 種子值一致的硬編碼預設，避免設定
// 被誤刪時頁面整個壞掉。redemption_status 為該使用者目前兌換申請狀態（見 GetFigureRedemptionStatus）。
func (s *Service) GetStickers(ctx context.Context, userID string) (*StickerGallery, error) {
	pieces, err := s.repo.GetStickerPieces(ctx, userID)
	if err != nil {
		return nil, err
	}
	owned := 0
	for _, p := range pieces {
		if p.Owned {
			owned++
		}
	}
	title := appsettings.GetString(ctx, s.repo.db, "monopoly_figure_title", "完賽跑者公仔")
	figureURL := appsettings.GetString(ctx, s.repo.db, "monopoly_figure_color_url", "/source/ui/03_figure/runner_figure_color.png")
	lineOA := appsettings.GetString(ctx, s.repo.db, "monopoly_figure_line_oa", "@855xfwqe")
	landingURL := appsettings.GetString(ctx, s.repo.db, "monopoly_figure_landing_url", "https://runner-figure.bravelog.tw/")
	redemptionStatus, err := s.repo.GetFigureRedemptionStatus(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &StickerGallery{
		Title:            title,
		FigureURL:        figureURL,
		Total:            len(pieces),
		Owned:            owned,
		Pieces:           pieces,
		LineOA:           lineOA,
		LandingURL:       landingURL,
		RedemptionStatus: redemptionStatus,
	}, nil
}

// RedeemFigure 完賽公仔兌換申請（A）：伺服器驗證使用者已集滿全部九宮格貼紙才可申請（未滿回
// ErrStickersIncomplete）；已申請過則冪等回現況，不重複建立、不報錯（見 Repository.EnsureFigureRedemption）。
func (s *Service) RedeemFigure(ctx context.Context, userID string) (string, error) {
	pieces, err := s.repo.GetStickerPieces(ctx, userID)
	if err != nil {
		return "", err
	}
	owned := 0
	for _, p := range pieces {
		if p.Owned {
			owned++
		}
	}
	if len(pieces) == 0 || owned < len(pieces) {
		return "", ErrStickersIncomplete
	}
	return s.repo.EnsureFigureRedemption(ctx, userID)
}

// diceGPCost 讀後台可調的擲骰成本（預設 3 GP），與 race package 存取 s.repo.db 的慣例一致
// （appsettings.GetInt 只吃 *pgxpool.Pool，同套件內直接讀 repo 的 db 欄位，不需另外分層）。
func diceGPCost(ctx context.Context, s *Service) int {
	return appsettings.GetInt(ctx, s.repo.db, "monopoly_dice_gp_cost", 3)
}

// monopolyDupGP 讀後台可調的「重複卡片依稀有度轉 GP」對照（monopoly_dup_gp，JSON 字串如
// {"common":5,"rare":20}）。查無設定、JSON 格式錯誤、或解析出空 map 一律回退硬編碼預設值，
// 避免單一筆壞設定把整個抽卡流程卡死（appsettings 只存字串，沒有 schema 驗證這個 key 的內容）。
func monopolyDupGP(ctx context.Context, s *Service) map[string]int {
	def := map[string]int{"common": 5, "rare": 20}
	raw := appsettings.GetString(ctx, s.repo.db, "monopoly_dup_gp", "")
	if raw == "" {
		return def
	}
	var m map[string]int
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return def
	}
	return m
}
