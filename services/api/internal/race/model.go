package race

import (
	"encoding/json"
	"time"
)

// Race 資料庫模型
type Race struct {
	ID           string     `json:"id"`
	Slug         string     `json:"slug"`
	Title        string     `json:"title"`
	Subtitle     string     `json:"subtitle"`
	World        string     `json:"world"`
	Blurb        string     `json:"blurb"`
	HeroImageURL string     `json:"hero_image_url"`
	Status       string     `json:"status"`     // soon|open|live|done
	EventMode    string     `json:"event_mode"` // general|competition|faction_battle
	GoalType     string     `json:"goal_type"`  // cumulative|distance（競賽完賽目標）
	Distances    []int      `json:"distances"`
	GroupType    string     `json:"group_type"` // faction|club|distance
	GroupMode    string     `json:"group_mode"` // random|self
	SlotsTotal   int        `json:"slots_total"`
	EntryFee     int        `json:"entry_fee"` // 分（NT$ × 100）
	RegStart     *time.Time `json:"registration_start,omitempty"` // 報名開始
	RegEnd       *time.Time `json:"registration_end,omitempty"`   // 報名截止
	StartDate    time.Time  `json:"start_date"`                   // 競賽時間 起
	EndDate      time.Time  `json:"end_date"`                     // 競賽時間 迄
	Config         RaceConfig `json:"config"`
	RequiredFields []string   `json:"required_fields"` // 報名必填欄位：real_name|nickname|phone|address|birthday|gender
	BrochureTitle  string     `json:"brochure_title"`  // 簡章大主標
	ControlStatus  string     `json:"control_status"`  // active|paused|suspended|closed|hidden|testing（admin 手動）
	StartingSoonDays int      `json:"starting_soon_days"` // 賽事即將開始 倒數天數
	AllowTeamGroups  bool     `json:"allow_team_groups"`  // 競賽模式：是否開放前台自建跑團分組
	DisplayStatus  string     `json:"display_status"`  // 計算欄位（讀取時填）：upcoming_reg|registering|reg_closed|starting_soon|racing|ended|paused|suspended
	CanRegister    bool       `json:"can_register"`    // 計算欄位
	CreatedBy      string     `json:"created_by,omitempty"` // organizer userID
	ReviewStatus   string     `json:"review_status"`        // pending|approved|rejected
	ReviewNote     string     `json:"review_note,omitempty"`
	CertificateBgURL string   `json:"certificate_bg_url"`   // 完賽證明底圖（空=預設設計）
	CreatedAt      time.Time  `json:"created_at"`
}

// ComputeDisplay 依現在時間推導顯示狀態與是否可報名（control=active/testing/hidden 才走時間規則）。
func (r *Race) ComputeDisplay(now time.Time) (string, bool) {
	switch r.ControlStatus {
	case "paused":
		return "paused", false
	case "suspended":
		return "suspended", false
	}
	days := r.StartingSoonDays
	if days <= 0 {
		days = 5
	}
	startingSoon := r.StartDate.AddDate(0, 0, -days)

	// 報名期間是否開放（nil 視為不限該側）
	regOpen := (r.RegStart == nil || !now.Before(*r.RegStart)) &&
		(r.RegEnd == nil || !now.After(*r.RegEnd))

	var display string
	switch {
	case now.After(r.EndDate) || now.Equal(r.EndDate):
		display = "ended"
	case !now.Before(r.StartDate): // now >= start → 賽事進行中
		display = "racing"
	case regOpen: // 報名期間開放優先（即使已進入賽前 N 天）
		display = "registering"
	case !now.Before(startingSoon): // 報名已截止但在賽前 N 天內
		display = "starting_soon"
	case r.RegEnd != nil && now.After(*r.RegEnd):
		display = "reg_closed"
	case r.RegStart != nil && now.Before(*r.RegStart):
		display = "upcoming_reg"
	default:
		display = "registering"
	}

	canRegister := display == "registering" &&
		(r.ControlStatus == "active" || r.ControlStatus == "testing" || r.ControlStatus == "hidden")
	return display, canRegister
}

// FillDisplay 將計算欄位填入 race（讀取後呼叫）
func (r *Race) FillDisplay(now time.Time) {
	r.DisplayStatus, r.CanRegister = r.ComputeDisplay(now)
}

// RaceGroup 分組（一般/競賽=選手自選，分組對抗=隨機分配）
type RaceGroup struct {
	ID               string   `json:"id,omitempty"`
	RaceID           string   `json:"race_id,omitempty"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	DisplayOrder     int      `json:"display_order"`
	SlotLimit        *int     `json:"slot_limit,omitempty"`    // nil=不限
	SlotsTaken       int      `json:"slots_taken"`
	GenderLimit      string   `json:"gender_limit"`            // any|male|female
	AgeMin           *int     `json:"age_min,omitempty"`
	AgeMax           *int     `json:"age_max,omitempty"`
	TargetDistanceKm *float64 `json:"target_distance_km,omitempty"`
	RequiresKey      bool     `json:"requires_key"`            // 需要「跑團鑰匙」才能加入
	GroupKey         string   `json:"group_key,omitempty"`     // 鑰匙明碼：後台/建立時可帶；公開回傳一律清空
	CreatedBy        string   `json:"created_by,omitempty"`    // 自建者 userID（空=官方建立）
	IsUserCreated    bool     `json:"is_user_created"`         // created_by 非空
	ExpReward        int      `json:"exp_reward"`              // 完成此分組可獲得的 EXP
}

// CreateTeamGroupRequest 前台跑團成員自建分組 payload
type CreateTeamGroupRequest struct {
	RaceID           string   `json:"-"`
	UserID           string   `json:"-"`
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	TargetDistanceKm *float64 `json:"target_distance_km,omitempty"`
	RequiresKey      bool     `json:"requires_key"`
	GroupKey         string   `json:"group_key,omitempty"`
}

// --- 賽事任務系統 ---

// MetricSpec 任務指標規格（後端為單一真實來源，前端鏡像）
type MetricSpec struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Unit    string `json:"unit"`
	Kind    string `json:"kind"`     // threshold | range
	HasData bool   `json:"has_data"` // 目前 activities 是否有資料源
}

// MetricKind
const (
	MetricThreshold = "threshold" // 實際值 >= target_value 即完成
	MetricRange     = "range"     // 實際值落在 [range_lo, range_hi] 即完成
)

// TaskScope
const (
	ScopeRaceCollective  = "race_collective"  // 全部參賽者集體加總
	ScopeGroupTeam       = "group_team"       // 分組團體加總
	ScopeGroupIndividual = "group_individual" // 分組個人各自
)

// MetricCatalog 9 種任務指標（key → 規格）。爬升/心率目前無資料源（HasData=false）。
var MetricCatalog = map[string]MetricSpec{
	"cumulative_distance": {"cumulative_distance", "累計總里程", "km", MetricThreshold, true},
	"single_distance":     {"single_distance", "單次里程", "km", MetricThreshold, true},
	"daily_distance":      {"daily_distance", "每日里程", "km", MetricThreshold, true},
	"streak_days":         {"streak_days", "連續進行任務天數", "天", MetricThreshold, true},
	"weekly_distance":     {"weekly_distance", "每週總里程", "km", MetricThreshold, true},
	"avg_pace_range":      {"avg_pace_range", "平均配速區間", "秒/km", MetricRange, true},
	"cumulative_ascent":   {"cumulative_ascent", "累積爬升海拔", "m", MetricThreshold, false},
	"single_ascent":       {"single_ascent", "單次爬升海拔", "m", MetricThreshold, false},
	"avg_hr_range":        {"avg_hr_range", "平均心率區間", "bpm", MetricRange, false},
}

// MetricCatalogList 依固定順序回傳 catalog（API/驗證用）
func MetricCatalogList() []MetricSpec {
	order := []string{
		"cumulative_distance", "single_distance", "daily_distance", "streak_days",
		"weekly_distance", "avg_pace_range", "cumulative_ascent", "single_ascent", "avg_hr_range",
	}
	out := make([]MetricSpec, 0, len(order))
	for _, k := range order {
		out = append(out, MetricCatalog[k])
	}
	return out
}

// ValidMetric 是否為已知指標
func ValidMetric(k string) bool {
	_, ok := MetricCatalog[k]
	return ok
}

// RaceTask 賽事任務（三層 scope）。GroupIndex 仿 RaceSupply：建立時對應 Groups 陣列索引。
type RaceTask struct {
	ID           string   `json:"id,omitempty"`
	RaceID       string   `json:"race_id,omitempty"`
	Scope        string   `json:"scope"`                 // race_collective | group_team | group_individual
	GroupID      string   `json:"group_id,omitempty"`    // 回傳時填實際 UUID（race_collective 為空）
	GroupIndex   *int     `json:"group_index,omitempty"` // 建立時用：對應 Groups 陣列索引
	MetricType   string   `json:"metric_type"`
	TargetValue  *float64 `json:"target_value,omitempty"` // threshold 用
	RangeLo      *float64 `json:"range_lo,omitempty"`     // range 用
	RangeHi      *float64 `json:"range_hi,omitempty"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	DisplayOrder int      `json:"display_order"`
}

// TaskModule 全站共用任務模組（範本）
type TaskModule struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	IsSystem    bool              `json:"is_system"`
	Items       []TaskModuleItem  `json:"items"`
}

// TaskModuleItem 模組內任務項目
type TaskModuleItem struct {
	ID           string   `json:"id,omitempty"`
	ModuleID     string   `json:"module_id,omitempty"`
	MetricType   string   `json:"metric_type"`
	TargetValue  *float64 `json:"target_value,omitempty"`
	RangeLo      *float64 `json:"range_lo,omitempty"`
	RangeHi      *float64 `json:"range_hi,omitempty"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	DisplayOrder int      `json:"display_order"`
}

// RaceAddon 加購項目
type RaceAddon struct {
	ID           string `json:"id,omitempty"`
	RaceID       string `json:"race_id,omitempty"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ImageURL     string `json:"image_url,omitempty"`
	PriceCents   int    `json:"price_cents"`
	PerUserLimit *int   `json:"per_user_limit,omitempty"` // nil=不限
	TotalStock   *int   `json:"total_stock,omitempty"`    // nil=不限
	SoldCount    int    `json:"sold_count"`
	DisplayOrder int    `json:"display_order"`
	Active       bool   `json:"active"`
}

// RaceSupply 物資（共用 or 分組 × 參賽 or 完賽）
// GroupIndex：建立時前端尚無 group UUID，用分組陣列索引對應（nil=賽事層級共用）。
type RaceSupply struct {
	ID           string `json:"id,omitempty"`
	RaceID       string `json:"race_id,omitempty"`
	GroupID      string `json:"group_id,omitempty"`    // 回傳時填實際 UUID（空=共用）
	GroupIndex   *int   `json:"group_index,omitempty"` // 建立時用：對應 Groups 陣列索引
	Kind         string `json:"kind"`                  // race_pack|finisher
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	ImageURL     string `json:"image_url,omitempty"`
	DisplayOrder int    `json:"display_order"`
}

// BrochureBlock 簡章內容區塊
type BrochureBlock struct {
	ID           string `json:"id,omitempty"`
	BlockType    string `json:"block_type"` // text | image | video
	Content      string `json:"content"`    // text:HTML / image:URL / video:YouTube
	Caption      string `json:"caption,omitempty"`
	DisplayOrder int    `json:"display_order"`
}

// CreateRaceRequest 後台新增賽事的巢狀 payload
type CreateRaceRequest struct {
	Race
	Groups        []RaceGroup     `json:"groups"`
	Addons        []RaceAddon     `json:"addons"`
	Supplies      []RaceSupply    `json:"supplies"`
	TestWhitelist []string        `json:"test_whitelist"` // 該賽事測試白名單 email
	Brochure      []BrochureBlock `json:"brochure"`
	Tasks         []RaceTask      `json:"tasks"`
}

// RaceDetail 含巢狀子資料（供後台編輯載入）
type RaceDetail struct {
	Race
	Groups        []RaceGroup     `json:"groups"`
	Addons        []RaceAddon     `json:"addons"`
	Supplies      []RaceSupply    `json:"supplies"`
	TestWhitelist []string        `json:"test_whitelist"`
	Brochure      []BrochureBlock `json:"brochure"`
	Tasks         []RaceTask      `json:"tasks"`
}

// GroupPreset 分組預設選單（可擴充）
type GroupPreset struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	DefaultDistanceKm *float64 `json:"default_distance_km,omitempty"`
	IsSystem          bool     `json:"is_system"`
}

// GroupStanding 競賽分組成績（讀自 race_group_standings 預聚合表）
type GroupStanding struct {
	GroupID      string  `json:"group_id"`
	GroupName    string  `json:"group_name"`
	TotalKm      float64 `json:"total_km"`
	MemberCount  int     `json:"member_count"`
	AvgKm        float64 `json:"avg_km"`
	AvgPaceS     int     `json:"avg_pace_s"`
	FinishTotalS int64   `json:"finish_total_s"`
}

// StandingRank 排行榜單筆（含名次）
type StandingRank struct {
	Rank int `json:"rank"`
	GroupStanding
}

// MyGroupRank 使用者所屬分組目前在兩個榜的名次
type MyGroupRank struct {
	GroupID        string  `json:"group_id"`
	GroupName      string  `json:"group_name"`
	CumulativeRank int     `json:"cumulative_rank"`
	FinishRank     int     `json:"finish_rank"`
	TotalKm        float64 `json:"total_km"`
}

// CompetitionRanking 競賽排行榜 API 回應
type CompetitionRanking struct {
	RaceID       string         `json:"race_id"`
	EventMode    string         `json:"event_mode"`
	GoalType     string         `json:"goal_type"`
	ByCumulative []StandingRank `json:"by_cumulative"` // 總累積里程榜（前 20）
	ByFinishTime []StandingRank `json:"by_finish_time"` // 完成累計總時間榜（前 20）
	MyGroup      *MyGroupRank   `json:"my_group,omitempty"`
}

// RaceConfig 儲存在 JSONB 欄位，定義陣營/公會/每日任務
type RaceConfig struct {
	Factions []FactionDef `json:"factions,omitempty"`
	Clubs    []ClubDef    `json:"clubs,omitempty"`
	Missions []MissionDef `json:"missions,omitempty"`
}

type FactionDef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"` // fug|hunt|violet|gold
}

type ClubDef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MissionDef 對應每一天的任務規格（admin 設定時填入）
type MissionDef struct {
	Day    int     `json:"day"`
	Title  string  `json:"title"`
	Tag    string  `json:"tag"`
	Type   string  `json:"type"`             // base|pace|rescue
	BaseKm float64 `json:"base_km"`
	PaceLo string  `json:"pace_lo,omitempty"` // e.g. "4:30"（分:秒）
	PaceHi string  `json:"pace_hi,omitempty"` // e.g. "5:30"
	Desc   string  `json:"desc"`
}

// Registration 報名記錄
type Registration struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	RaceID        string     `json:"race_id"`
	Distance      int        `json:"distance"`
	Faction       string     `json:"faction,omitempty"`
	GroupID       string     `json:"group_id,omitempty"`
	GroupRevealed bool       `json:"group_revealed"`
	GroupName     string     `json:"group_name,omitempty"` // 報名分組名稱（一般模式直接顯示）
	Status        string     `json:"status"`               // pending|paid|cancelled
	PaidAt        *time.Time `json:"paid_at,omitempty"`
	Amount        int        `json:"amount"`
}

// ParticipantInfo 報名時填的參賽者資料（也用於回填 user_profiles）
type ParticipantInfo struct {
	RealName string `json:"real_name"`
	Nickname string `json:"nickname"`
	Phone    string `json:"phone"`
	Address  string `json:"address"`
	Birthday string `json:"birthday"` // YYYY-MM-DD
	Gender   string `json:"gender"`   // male|female|other
}

// AddonSelection 報名時選購的加購項目
type AddonSelection struct {
	AddonID string `json:"addon_id"`
	Qty     int    `json:"qty"`
}

// RegisterRequest 前台報名 payload
type RegisterRequest struct {
	RaceID      string           `json:"-"`
	UserID      string           `json:"-"`
	GroupID     string           `json:"group_id"`           // 一般/競賽必填；分組對抗忽略（隨機）
	GroupKey    string           `json:"group_key,omitempty"` // 加入需鑰匙的分組時帶入
	Addons      []AddonSelection `json:"addons"`
	Participant ParticipantInfo  `json:"participant"`
	PromoCode   string           `json:"promo_code,omitempty"`
}

// PromoQuote 優惠序號折抵預覽（報名前即時試算）
type PromoQuote struct {
	Valid         bool   `json:"valid"`
	Code          string `json:"code,omitempty"`
	DiscountCents int    `json:"discount_cents"`
	PayableCents  int    `json:"payable_cents"`
	Free          bool   `json:"free"` // 應付 < 0.5 元 → 0 元免金流
	Reason        string `json:"reason,omitempty"`
}

// SignupRow 後台報名管理列表單筆
type SignupRow struct {
	ID            string    `json:"id"`
	UserName      string    `json:"user_name"`
	UserEmail     string    `json:"user_email"`
	GroupName     string    `json:"group_name"`
	Status        string    `json:"status"`
	GroupRevealed bool      `json:"group_revealed"`
	SnapRealName  string    `json:"snap_real_name"`
	SnapPhone     string    `json:"snap_phone"`
	CreatedAt     time.Time `json:"created_at"`
	OrderID       string    `json:"order_id,omitempty"`
	OrderTotal    int       `json:"order_total_cents"`
	OrderStatus   string    `json:"order_status,omitempty"`
}

// OrderRow 後台訂單管理列表單筆
type OrderRow struct {
	ID             string     `json:"id"`
	UserName       string     `json:"user_name"`
	UserEmail      string     `json:"user_email"`
	RaceTitle      string     `json:"race_title"`
	TotalCents     int        `json:"total_cents"`
	Status         string     `json:"status"`
	PaymentRef     string     `json:"payment_ref,omitempty"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	RegistrationID string     `json:"registration_id,omitempty"`
}

// OrderItemRow 訂單明細單筆
type OrderItemRow struct {
	ItemType       string `json:"item_type"` // entry|addon
	AddonName      string `json:"addon_name,omitempty"`
	Qty            int    `json:"qty"`
	UnitPriceCents int    `json:"unit_price_cents"`
	SubtotalCents  int    `json:"subtotal_cents"`
}

// OrderDetail 訂單 + 明細
type OrderDetail struct {
	OrderRow
	Items []OrderItemRow `json:"items"`
}

// MyRegLite 使用者在某賽事的精簡報名狀態（賽事列表附帶用）
type MyRegLite struct {
	Status        string `json:"status"`         // pending|paid|cancelled
	GroupRevealed bool   `json:"group_revealed"`
	GroupName     string `json:"group_name"` // 報名分組名稱（一般模式直接顯示；競賽模式當天才揭曉）
}

// Order 訂單
type Order struct {
	ID         string `json:"id"`
	TotalCents int    `json:"total_cents"`
	Status     string `json:"status"` // pending|paid|cancelled|refunded
}

// RegisterResult 報名結果
type RegisterResult struct {
	Registration  *Registration `json:"registration"`
	Order         *Order        `json:"order"`
	AssignedGroup string        `json:"assigned_group"` // 指派/選擇的分組名稱
	GroupRevealed bool          `json:"group_revealed"` // 分組是否已公布
	DiscountCents int           `json:"discount_cents"`
	PayableCents  int           `json:"payable_cents"`
	Paid          bool          `json:"paid"` // 是否已直接完成（0 元）
}

// RankEntry 排行榜單筆記錄
type RankEntry struct {
	Rank       int     `json:"rank"`
	UserID     string  `json:"user_id"`
	Handle     string  `json:"handle"`
	Name       string  `json:"name"`
	DistanceKm float64 `json:"distance_km"`
	Faction    string  `json:"faction,omitempty"`
}

// FactionStatus 陣營即時分數
type FactionStatus struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Color      string  `json:"color"`
	TotalKm    float64 `json:"total_km"`
	ScorePct   float64 `json:"score_pct"`   // 百分比（0–100）
	MemberCount int    `json:"member_count"`
}

// LiveStatus 賽事即時狀態（API 回應用）
type LiveStatus struct {
	RaceID   string          `json:"race_id"`
	Status   string          `json:"status"`
	DayNow   int             `json:"day_now"`   // 目前是第幾天（1-indexed）
	Factions []FactionStatus `json:"factions,omitempty"`
	WSCount  int             `json:"ws_count"`  // 在線人數（WebSocket）
}

// configToBytes 將 RaceConfig 序列化為 JSON bytes，供 pgx 存入 JSONB
func configToBytes(c RaceConfig) ([]byte, error) {
	return json.Marshal(c)
}

// bytesToConfig 從 JSONB bytes 反序列化
func bytesToConfig(b []byte) (RaceConfig, error) {
	var c RaceConfig
	if len(b) == 0 {
		return c, nil
	}
	err := json.Unmarshal(b, &c)
	return c, err
}
