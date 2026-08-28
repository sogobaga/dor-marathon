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
	ID           string  `json:"id"`
	MerchantID   *string `json:"merchant_id"`
	MerchantName string  `json:"merchant_name,omitempty"` // 列表用，join reward_merchants 帶出
	Name         string  `json:"name"`
	ItemLabel    string  `json:"item_label"` // 面額/品項（如「100元」「咖啡兌換」）
	IsLinePoint  bool    `json:"is_line_point"`
	// FaceValue 結構化面額（migration 149）：如 1000/500，取代靠 name/item_label 字串解析數字（見
	// race/reward_preview.go largestNumberIn）。一般序號組：管理員手動填、存於 DB 該欄位。組合型序號組
	// （IsBundle=true，migration 150）：不存靜態值（CRUD 寫入時強制歸零），改由 Repository 查詢時動態算
	// Σ(子面額組 FaceValue × count)——組合定義本身才是真值，快照容易跟子項改動脫鉤。
	FaceValue int `json:"face_value"`
	// IsBundle 組合型序號組（migration 150）：true＝不自己存 reward_serials，而是由 BundleItems 定義成
	// 「子面額組 × 數量」的固定組合（如「LINE POINTS 3000」= LINE POINTS 1000 × 3）。中獎抽到組合型序號組
	// 時，發放引擎（internal/activityreward/roll.go grantSerialBundle）會對每個子項原子搶對應數量的序號，
	// all-or-nothing 全發或全不發，全部綁同一 bundle_id 進 user_rewards。
	IsBundle bool `json:"is_bundle"`
	// BundleItems IsBundle=true 時的組合定義（子面額組×數量），讀自 reward_serial_group_items；一般序號組
	// 恆為空陣列（不可能有列，CRUD 驗證擋下）。
	BundleItems     []GroupBundleItem `json:"bundle_items"`
	ValidFrom       *time.Time        `json:"valid_from"`      // 開始時間；null=即刻可用
	ValidUntil      *time.Time        `json:"valid_until"`     // 使用期限；null=無期限
	UseLimitType    string            `json:"use_limit_type"`  // single|repeat|unlimited
	UseLimitCount   *int              `json:"use_limit_count"` // use_limit_type=repeat 時的次數
	GrantCount      int               `json:"grant_count"`     // 每次中獎配發幾枚序號
	AppliesAllRaces bool              `json:"applies_all_races"`
	RaceIDs         []string          `json:"race_ids"`    // applies_all_races=false 時的指定活動（可複選）
	UsageNote       string            `json:"usage_note"`  // 獎勵詳情：使用說明（活動獎勵系統 P2，見 migration 127）
	IconURL         string            `json:"icon_url"`    // 獎勵詳情：獎勵圖示
	Description     string            `json:"description"` // 獎勵詳情：活動/獎勵說明
	CreatedAt       time.Time         `json:"created_at"`
	// AvailableCount 統計：一般序號組＝未發送序號張數；組合型＝目前能湊滿幾包（min(floor(子面額組可用
	// 張數/該子項所需數量))，見 Repository.hydrateGroups）。
	AvailableCount int `json:"available_count"`
	IssuedCount    int `json:"issued_count"` // 統計：已發送（組合型：本身無自有序號，恆為 0）
	VoidCount      int `json:"void_count"`   // 統計：已註銷（組合型：恆為 0）
	TotalCount     int `json:"total_count"`  // 統計：總數（組合型：恆為 0）
}

// GroupBundleItem 組合型序號組（is_bundle=true，migration 150）的一個子項：子面額組 × 數量。子面額組
// （ChildGroupID）須為非組合型（不可巢狀）且與其餘子項同一商家，由 Service.validateBundleItems 驗證。
type GroupBundleItem struct {
	ChildGroupID string `json:"child_group_id"`
	Count        int    `json:"count"` // 這個子面額組發幾張（≥1）
}

// GroupInput 建立/更新序號組的輸入
type GroupInput struct {
	MerchantID  *string `json:"merchant_id"`
	Name        string  `json:"name"`
	ItemLabel   string  `json:"item_label"`
	IsLinePoint bool    `json:"is_line_point"`
	FaceValue   int     `json:"face_value"` // 結構化面額（migration 149）：如 1000；0=未設；IsBundle=true 時忽略（動態算）
	IsBundle    bool    `json:"is_bundle"`  // 組合型序號組（migration 150）
	// BundleItems IsBundle=true 時必填（≥1 子項）；IsBundle=false 時須為空（見 Service.validateGroupInput）。
	BundleItems     []GroupBundleItem `json:"bundle_items"`
	ValidFrom       *string           `json:"valid_from"`  // 開始時間；RFC3339；空字串/未帶=即刻可用
	ValidUntil      *string           `json:"valid_until"` // RFC3339；空字串/未帶=無期限
	UseLimitType    string            `json:"use_limit_type"`
	UseLimitCount   *int              `json:"use_limit_count"`
	GrantCount      int               `json:"grant_count"`
	AppliesAllRaces bool              `json:"applies_all_races"`
	RaceIDs         []string          `json:"race_ids"`
	UsageNote       string            `json:"usage_note"`  // 獎勵詳情：使用說明（活動獎勵系統 P2）
	IconURL         string            `json:"icon_url"`    // 獎勵詳情：獎勵圖示
	Description     string            `json:"description"` // 獎勵詳情：活動/獎勵說明
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

// SerialIDsInput 批次操作（刪除／批次註銷）輸入：一批序號 id。單筆操作亦透過同一端點，ids 傳 1 個即可
// （2026-08-29 拍板：刪除與批次註銷不另開單筆端點，統一走批次形狀）。
type SerialIDsInput struct {
	IDs []string `json:"ids"`
}

// SerialDeleteResult 批次刪除序號的結果。安全邊界：只允許刪除 status IN ('available','void') 的序號；
// issued（已發送，user_rewards.serial_id 可能已外鍵引用）一律拒絕，Reasons 列出被拒數量與原因，供前端
// 顯示「已選 N 筆：成功 n／跳過 n（原因）」。
type SerialDeleteResult struct {
	Deleted int      `json:"deleted"`
	Skipped int      `json:"skipped"`
	Reasons []string `json:"reasons"` // 人類可讀的跳過原因彙總，如「已發送的序號不可刪除（2 筆）」
}

// SerialVoidBatchResult 批次註銷序號的結果。沿用單筆 VoidSerial 的既有語意（不限制當前狀態，含已是
// void 的再次註銷視為成功/冪等）；查無此序號（跨組或不存在）計入 Skipped。
type SerialVoidBatchResult struct {
	Voided  int      `json:"voided"`
	Skipped int      `json:"skipped"`
	Reasons []string `json:"reasons"`
}
