package organizer

import "time"

// Profile 合作方商業資料（補充 users 表的個人資訊）
type Profile struct {
	UserID       string     `json:"user_id"`
	CompanyName  string     `json:"company_name"`
	ContactName  string     `json:"contact_name"`
	ContactEmail string     `json:"contact_email"`
	ContactPhone string     `json:"contact_phone"`
	Website      string     `json:"website"`
	Description  string     `json:"description"`
	Verified     bool       `json:"verified"`      // 平台審核通過才能提交賽事
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// RaceSummary 合作方看到的賽事摘要（含審核狀態）
type RaceSummary struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`        // soon|open|live|done
	ReviewStatus string    `json:"review_status"` // pending|approved|rejected
	ReviewNote   string    `json:"review_note,omitempty"`
	StartDate    time.Time `json:"start_date"`
	EndDate      time.Time `json:"end_date"`
	SignupCount  int       `json:"signup_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// Dashboard 合作方總覽數據
type Dashboard struct {
	TotalRaces    int `json:"total_races"`
	PendingRaces  int `json:"pending_races"`
	ActiveRaces   int `json:"active_races"`
	TotalSignups  int `json:"total_signups"`
	TotalRevenue  int `json:"total_revenue"` // 分（NT$ × 100）
}

// ReviewAction admin 審核動作
type ReviewAction struct {
	RaceID string `json:"race_id"`
	Action string `json:"action"` // approve | reject
	Note   string `json:"note"`   // 退回原因（reject 時必填）
}
