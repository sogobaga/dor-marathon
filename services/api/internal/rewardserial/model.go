// Package rewardserial 活動獎勵系統 P1：序號庫存管理（後台）。
// 合作商家 → 活動序號組（面額/期限/使用次數限制/每次配發數/對應活動）→ 序號（全系統唯一，手動或 .csv 匯入）。
// 設計見 memory activity-reward-system；P1 只管序號庫存，即時獎勵 roll(P2)/玩家錢包(P3)/到期提醒(P4) 於後續套件接續。
package rewardserial

import "time"

// Merchant 合作商家
type Merchant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// Group 活動序號組
type Group struct {
	ID              string     `json:"id"`
	MerchantID      *string    `json:"merchant_id"`
	MerchantName    string     `json:"merchant_name,omitempty"` // 列表用，join reward_merchants 帶出
	Name            string     `json:"name"`
	ItemLabel       string     `json:"item_label"` // 面額/品項（如「100元」「咖啡兌換」）
	IsLinePoint     bool       `json:"is_line_point"`
	// FaceValue 結構化面額（migration 149）：如 1000/500，取代靠 name/item_label 字串解析數字（見
	// race/reward_preview.go largestNumberIn）。組合包（activityreward.RewardItem.Bundle）的
	// bundle_total = Σ(FaceValue × count) 靠此欄位計算，不能繼續賭字串裡一定能解析出數字。0=未設。
	FaceValue       int        `json:"face_value"`
	ValidFrom       *time.Time `json:"valid_from"`      // 開始時間；null=即刻可用
	ValidUntil      *time.Time `json:"valid_until"`     // 使用期限；null=無期限
	UseLimitType    string     `json:"use_limit_type"`  // single|repeat|unlimited
	UseLimitCount   *int       `json:"use_limit_count"` // use_limit_type=repeat 時的次數
	GrantCount      int        `json:"grant_count"`     // 每次中獎配發幾枚序號
	AppliesAllRaces bool       `json:"applies_all_races"`
	RaceIDs         []string   `json:"race_ids"`    // applies_all_races=false 時的指定活動（可複選）
	UsageNote       string     `json:"usage_note"`  // 獎勵詳情：使用說明（活動獎勵系統 P2，見 migration 127）
	IconURL         string     `json:"icon_url"`    // 獎勵詳情：獎勵圖示
	Description     string     `json:"description"` // 獎勵詳情：活動/獎勵說明
	CreatedAt       time.Time  `json:"created_at"`
	AvailableCount  int        `json:"available_count"` // 統計：未發送
	IssuedCount     int        `json:"issued_count"`    // 統計：已發送
	VoidCount       int        `json:"void_count"`      // 統計：已註銷
	TotalCount      int        `json:"total_count"`
}

// GroupInput 建立/更新序號組的輸入
type GroupInput struct {
	MerchantID      *string  `json:"merchant_id"`
	Name            string   `json:"name"`
	ItemLabel       string   `json:"item_label"`
	IsLinePoint     bool     `json:"is_line_point"`
	FaceValue       int      `json:"face_value"` // 結構化面額（migration 149）：如 1000；0=未設
	ValidFrom       *string  `json:"valid_from"`  // 開始時間；RFC3339；空字串/未帶=即刻可用
	ValidUntil      *string  `json:"valid_until"` // RFC3339；空字串/未帶=無期限
	UseLimitType    string   `json:"use_limit_type"`
	UseLimitCount   *int     `json:"use_limit_count"`
	GrantCount      int      `json:"grant_count"`
	AppliesAllRaces bool     `json:"applies_all_races"`
	RaceIDs         []string `json:"race_ids"`
	UsageNote       string   `json:"usage_note"`  // 獎勵詳情：使用說明（活動獎勵系統 P2）
	IconURL         string   `json:"icon_url"`    // 獎勵詳情：獎勵圖示
	Description     string   `json:"description"` // 獎勵詳情：活動/獎勵說明
}

// Serial 序號
type Serial struct {
	ID        string     `json:"id"`
	GroupID   string     `json:"group_id"`
	Code      string     `json:"code"`
	Link      string     `json:"link"`
	Status    string     `json:"status"` // available(未發送)|issued(已發送)|void(註銷)
	Used      bool       `json:"used"`
	UsedAt    *time.Time `json:"used_at"`
	IssuedTo  *string    `json:"issued_to"`
	IssuedAt  *time.Time `json:"issued_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// ImportInput 一筆待匯入序號
type ImportInput struct {
	Code string `json:"code"`
	Link string `json:"link"`
}

// ImportResult 匯入結果：全系統唯一去重，撞碼（跨任何序號組，含本次批次內重複）一律跳過不建立。
type ImportResult struct {
	Imported   int      `json:"imported"`
	Skipped    int      `json:"skipped"`
	Duplicates []string `json:"duplicates"`
}
