package reward

// WheelItem 轉盤獎項定義
type WheelItem struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`   // line | sticker | again | miss
	Label  string `json:"label"`
	Amount int    `json:"amount"`
	Weight int    `json:"weight"` // 抽中機率權重
}

// SpinResult 單次抽獎結果
type SpinResult struct {
	Item        WheelItem `json:"item"`
	StickerNo   int       `json:"sticker_no,omitempty"` // 若 kind=sticker，抽到的格子編號
	StickerName string    `json:"sticker_name,omitempty"`
	CanSpinAgain bool     `json:"can_spin_again"` // kind=again 時為 true
}

// StickerCard 九宮格集點狀態
type StickerCard struct {
	RaceID   string   `json:"race_id"`
	Stickers []Sticker `json:"stickers"`
	Complete bool     `json:"complete"`
}

type Sticker struct {
	No    int    `json:"no"`
	Name  string `json:"name"`
	Owned bool   `json:"owned"`
}

// defaultWheelPool 預設轉盤獎項（與原型一致）
// 合作方可透過 race.config.wheel_pool 覆蓋
var defaultWheelPool = []WheelItem{
	{ID: "lp50",  Kind: "line",    Label: "LINE Points", Amount: 50,  Weight: 26},
	{ID: "lp100", Kind: "line",    Label: "LINE Points", Amount: 100, Weight: 14},
	{ID: "lp300", Kind: "line",    Label: "LINE Points", Amount: 300, Weight: 4},
	{ID: "card",  Kind: "sticker", Label: "九宮格集點卡",   Amount: 1,   Weight: 30},
	{ID: "card2", Kind: "sticker", Label: "集點卡 ×2",     Amount: 2,   Weight: 8},
	{ID: "again", Kind: "again",   Label: "再轉一次",       Amount: 0,   Weight: 10},
	{ID: "miss",  Kind: "miss",    Label: "銘謝惠顧",       Amount: 0,   Weight: 8},
}

// defaultStickerNames 九宮格貼紙名稱（與原型一致）
var defaultStickerNames = []string{
	"", // 0 佔位
	"逃亡者", "獵人", "誘餌", "訊號", "救援", "潛伏", "追擊", "戰報", "公仔",
}
