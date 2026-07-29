// Package monopoly 環台大富翁 Phase 1：盤面遊戲（擲骰前進 + 繞圈）。
// 比照 internal/partner 的三層結構（model/repository/service/handler）。
package monopoly

// boardSize 盤面總格數：0=START，1..45 為格子，繞一圈=46 格（由前端 BOARD_COORDS 常數決定座標）。
const boardSize = 46

// chancePositions / destinyPositions 機會格／命運格（固定位置，由底圖決定）。
// Phase 1 停在這兩類格子只回一個 placeholder 旗標（draw_pending），真正抽卡是 Phase 3 才做。
var chancePositions = map[int]bool{6: true, 15: true, 25: true, 35: true}
var destinyPositions = map[int]bool{10: true, 18: true, 30: true, 39: true}

func landedOnFor(pos int) string {
	if chancePositions[pos] {
		return "chance"
	}
	if destinyPositions[pos] {
		return "destiny"
	}
	return "normal"
}

// PlayerState GET /monopoly/state 回應：目前棋子位置 + GP 餘額 + 目前擲骰成本（後台可調）。
type PlayerState struct {
	Position      int `json:"position"`
	LapsCompleted int `json:"laps_completed"`
	GPBalance     int `json:"gp_balance"`
	DiceGPCost    int `json:"dice_gp_cost"`
}

// RollResult POST /monopoly/roll 回應：一次擲骰的完整結果。
type RollResult struct {
	Roll        int         `json:"roll"` // 伺服器決定的點數 1..6（前端動畫最終須停在這個值）
	From        int         `json:"from"`
	To          int         `json:"to"`
	LapsGained  int         `json:"laps_gained"`   // 本次是否繞圈（可能一次擲骰跨圈，理論上恆為 0 或 1，但不假設上限）
	LandedOn    string      `json:"landed_on"`     // normal | chance | destiny
	LapRewardGP int         `json:"lap_reward_gp"` // 本次因繞圈實際發放的 GP（0=沒發）
	GPBalance   int         `json:"gp_balance"`    // 扣款+發獎+抽卡獎勵後的最終餘額
	DrawPending bool        `json:"draw_pending"`  // true=本次實際抽到獎勵（見 DrawResult）；停在機會/命運格但池空/卡池空則仍為 false
	DrawResult  *DrawResult `json:"draw_result,omitempty"`
}

// DrawResult 機會/命運抽卡結果（A2）。nil 代表本次未觸發抽卡（未停在機會/命運格，或該池/該主題暫無可抽項目）。
type DrawResult struct {
	Type        string `json:"type"` // gp | dp | vip_days | knowledge_card | sticker | redemption_code
	Title       string `json:"title,omitempty"`
	Body        string `json:"body,omitempty"` // 知識卡正文 / 兌換碼說明
	ImageURL    string `json:"image_url,omitempty"`
	Rarity      string `json:"rarity,omitempty"`
	Amount      int    `json:"amount,omitempty"` // gp/dp/vip_days 的數量
	IsDuplicate bool   `json:"is_duplicate,omitempty"`
	ConvertedGP int    `json:"converted_gp,omitempty"` // 重複卡/兌換碼耗盡 fallback 轉發的 GP
	Code        string `json:"code,omitempty"`         // 兌換碼
	Kind        string `json:"kind,omitempty"`         // 兌換碼種類：line_point | coupon
	IsFallback  bool   `json:"is_fallback,omitempty"`  // true=兌換碼庫存耗盡，已改發 converted_gp

	// 知識卡展示用欄位（未擁有前禁止外流，見 GetKnowledgeCards 的防劇透規則；抽卡當下必為剛抽到/已擁有，可放心回傳）
	MainCategory string `json:"main_category,omitempty"`
	Subtopic     string `json:"subtopic,omitempty"`
	PlayerAction string `json:"player_action,omitempty"`
	RiskNote     string `json:"risk_note,omitempty"`
	SourceOrg    string `json:"source_org,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
}

// KnowledgeCardEntry GET /monopoly/knowledge 圖鑑單張卡片。防劇透：未擁有(Owned=false)時只回前 5 個
// 欄位，展示用欄位一律留空（omitempty 不輸出），前端顯示「？」卡背。
type KnowledgeCardEntry struct {
	ID            string `json:"id"`
	Theme         string `json:"theme"` // training | care
	MainCategory  string `json:"main_category"`
	Rarity        string `json:"rarity"`
	Owned         bool   `json:"owned"`
	ObtainedCount int    `json:"obtained_count"`

	Code           string `json:"code,omitempty"`
	Subtopic       string `json:"subtopic,omitempty"`
	Title          string `json:"title,omitempty"`
	Body           string `json:"body,omitempty"`
	PlayerAction   string `json:"player_action,omitempty"`
	Timing         string `json:"timing,omitempty"`
	Importance     string `json:"importance,omitempty"`
	HandlingLevel  string `json:"handling_level,omitempty"`
	GameEffectHint string `json:"game_effect_hint,omitempty"`
	RiskNote       string `json:"risk_note,omitempty"`
	SourceOrg      string `json:"source_org,omitempty"`
	SourceDoc      string `json:"source_doc,omitempty"`
	SourceURL      string `json:"source_url,omitempty"`
	ImageURL       string `json:"image_url,omitempty"`
}

// KnowledgeCounts 圖鑑蒐集進度（依主題分）。
type KnowledgeCounts struct {
	TrainingTotal int `json:"training_total"`
	TrainingOwned int `json:"training_owned"`
	CareTotal     int `json:"care_total"`
	CareOwned     int `json:"care_owned"`
}

// KnowledgeGallery GET /monopoly/knowledge 回應。
type KnowledgeGallery struct {
	Counts KnowledgeCounts      `json:"counts"`
	Cards  []KnowledgeCardEntry `json:"cards"`
}

// StickerPieceEntry GET /monopoly/stickers 九宮格單片。與知識卡不同，貼紙不需防劇透——灰階片本來就
// 要顯示讓玩家看到缺哪片，所以一律回 gray_url，不因 Owned=false 而隱藏欄位。
type StickerPieceEntry struct {
	Position      int    `json:"position"`
	Title         string `json:"title"`
	Rarity        string `json:"rarity"`
	GrayURL       string `json:"gray_url"`
	Owned         bool   `json:"owned"`
	ObtainedCount int    `json:"obtained_count"`
}

// StickerGallery GET /monopoly/stickers 回應：完賽公仔九宮格收集狀態。Title/FigureURL 來自
// app_settings（monopoly_figure_title/monopoly_figure_color_url，見 105_finisher_figure_stickers.sql）。
// LineOA/LandingURL 為集滿後的兌換引導（monopoly_figure_line_oa/monopoly_figure_landing_url，見
// 107_monopoly_figure_redeem.sql）；RedemptionStatus 為該使用者目前兌換申請狀態
// （空字串=尚未申請｜pending｜fulfilled｜rejected，見 Repository.GetFigureRedemptionStatus）。
type StickerGallery struct {
	Title            string              `json:"title"`
	FigureURL        string              `json:"figure_url"`
	Total            int                 `json:"total"`
	Owned            int                 `json:"owned"`
	Pieces           []StickerPieceEntry `json:"pieces"`
	LineOA           string              `json:"line_oa"`
	LandingURL       string              `json:"landing_url"`
	RedemptionStatus string              `json:"redemption_status"`
}
