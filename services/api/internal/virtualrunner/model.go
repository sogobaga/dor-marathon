// Package virtualrunner 虛擬選手：後台可建立/管理的機器人跑者帳號，用於賽事名額造勢/測試。
//
// 虛擬選手＝users(is_virtual=TRUE) 的特殊帳號：不建 user_identities（provider/provider_uid
// 天然缺席），因此永遠無法透過任何方式登入——這是刻意設計，不是漏洞。個人資訊落在 user_profiles
// （nickname/real_name＝跑者綽號，非真人姓名、gender），能力值/行為參數落在本套 virtual_runners，由
// vr_level_presets 提供 8 級能力值範本（建立時 ±5% 個體抖動，見 jitter.go）。
//
// 分層仿 internal/partner：model.go 型別＋驗證白名單、repository.go 純 DB 存取、
// admin.go 後台 HTTP 端點（含輸入驗證，無獨立 service 層——本套後台端點與 DB 操作是
// 一對一的簡單 CRUD/交易，不需要額外一層）、namepool.go 姓名池、jitter.go 能力值抖動。
package virtualrunner

import (
	"errors"
	"time"
)

// --- 白名單（Go 層驗證；比照全站慣例，enum 欄位不加 DB CHECK，只在此集中驗證）---

// validLevels 8 級能力等級代碼（migration 146 vr_level_presets 種子資料）；固定不可由 API 新增/移除，
// 只能用 PUT /presets/{level} 調整既有等級的參數。
var validLevels = map[string]bool{
	"beginner": true, "citizen": true, "advanced": true, "half_challenger": true,
	"half_finisher": true, "full_challenger": true, "full_finisher": true, "elite": true,
}

// cityList 虛擬選手所在城市白名單（migration 146 virtual_runners.city 註解）。也供批次隨機建立時
// 抽城市用（BatchCreate 未指定 city 時逐位隨機），與 validCities 同一份來源、避免兩處各自維護。
var cityList = []string{"taipei", "new_taipei", "taoyuan", "hsinchu", "taichung", "tainan", "kaohsiung"}

// windowHourList 活躍時段起始時（24hr 制）：清晨 4-6 點或晚間 19-22 點，模擬真人早/晚跑習慣。
var windowHourList = []int{4, 5, 6, 19, 20, 21, 22}

// genderList 僅 male/female——虛擬選手需要一個明確性別來配姓名池、user_profiles.gender 顯示，
// 不比照真人會員開放 other/NULL（真人帳號自願填寫；虛擬選手是後台代填，沒有「不想說」的語境）。
var genderList = []string{"male", "female"}

var validCities = toSet(cityList)
var validWindowHours = toIntSet(windowHourList)
var validGenders = toSet(genderList)

func toSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, v := range list {
		m[v] = true
	}
	return m
}

func toIntSet(list []int) map[int]bool {
	m := make(map[int]bool, len(list))
	for _, v := range list {
		m[v] = true
	}
	return m
}

// ValidLevel / ValidCity / ValidWindowHour / ValidGender / ValidDiligence 匯出的純驗證函式，
// 供 admin.go 呼叫，亦可在測試中直接驗證邊界。
func ValidLevel(level string) bool      { return validLevels[level] }
func ValidCity(city string) bool        { return validCities[city] }
func ValidWindowHour(hour int) bool     { return validWindowHours[hour] }
func ValidGender(gender string) bool    { return validGenders[gender] }
func ValidDiligence(diligence int) bool { return diligence >= 1 && diligence <= 5 }

// ValidPaceRange fast 必須嚴格小於 slow（fast＝配速較快、秒數較小的那端）。
func ValidPaceRange(fastS, slowS int) bool { return fastS > 0 && slowS > 0 && fastS < slowS }

// --- 錯誤 ---

// ErrGroupNotFound 用於 repository 內部（AssignUser 指定 group_id 時的最後防線）；admin.go 對外
// 端點是用 Repository.RaceExists/GroupExists 先行擋 404/400，正常請求路徑不會真的觸發這個錯誤。
var (
	ErrRunnerNotFound   = errors.New("virtual runner not found")
	ErrPresetNotFound   = errors.New("level preset not found")
	ErrHasRegistrations = errors.New("has_registrations") // DELETE 選手：仍有非 cancelled 報名
	ErrGroupNotFound    = errors.New("group not found")
)

// --- 型別 ---

// LevelPreset 等級範本（vr_level_presets）。
type LevelPreset struct {
	Level     string  `json:"level"`
	Label     string  `json:"label"`
	SortOrder int     `json:"sort_order"`
	AvgKm     float64 `json:"avg_km"`
	MonthlyKm float64 `json:"monthly_km"`
	PaceFastS int     `json:"pace_fast_s"`
	PaceSlowS int     `json:"pace_slow_s"`
}

// Runner 後台列表用單筆虛擬選手（virtual_runners JOIN users/user_profiles + 報名數）。
type Runner struct {
	UserID          string     `json:"user_id"`
	Name            string     `json:"name"`
	Gender          string     `json:"gender"`
	City            string     `json:"city"`
	Level           string     `json:"level"`
	Diligence       int        `json:"diligence"`
	WindowHour      int        `json:"window_hour"`
	AvgKm           float64    `json:"avg_km"`
	MonthlyKm       float64    `json:"monthly_km"`
	PaceFastS       int        `json:"pace_fast_s"`
	PaceSlowS       int        `json:"pace_slow_s"`
	Enabled         bool       `json:"enabled"`
	LastGeneratedAt *time.Time `json:"last_generated_at"`
	RaceCount       int        `json:"race_count"`
}

// Ability 一組能力值（供建立/更新時傳遞抖動後或使用者明給的能力值）。
type Ability struct {
	AvgKm     float64
	MonthlyKm float64
	PaceFastS int
	PaceSlowS int
}

// CreateRunnerInput 建立單一虛擬選手（POST ”）。
type CreateRunnerInput struct {
	Name       string
	Gender     string
	City       string
	Level      string
	Diligence  int
	WindowHour int
	Ability    Ability
}

// UpdateRunnerInput 更新虛擬選手（PUT /{userID}）；nil 代表該欄位不變。
// AbilityGiven=true 代表本次請求有明給至少一個能力值欄位——用來判斷「level 變更時是否要重新
// 從新 preset 帶入抖動」：level 有變更且本次沒有明給任何能力值時才重算（見 admin.go Update）。
type UpdateRunnerInput struct {
	Gender     *string
	City       *string
	Level      *string
	Diligence  *int
	WindowHour *int
	AvgKm      *float64
	MonthlyKm  *float64
	PaceFastS  *int
	PaceSlowS  *int
	Enabled    *bool
}

// AbilityGiven 本次請求是否明給了至少一項能力值欄位。
func (in UpdateRunnerInput) AbilityGiven() bool {
	return in.AvgKm != nil || in.MonthlyKm != nil || in.PaceFastS != nil || in.PaceSlowS != nil
}

// AssignedRunner GET /race/{raceID} 的 assigned[] 單筆。
type AssignedRunner struct {
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	Gender    string `json:"gender"`
	Level     string `json:"level"`
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name"`
	RegStatus string `json:"reg_status"`
}

// GroupSlot GET /race/{raceID} 的 groups[] 單筆，亦供 repository 內部隨機分組挑選使用。
type GroupSlot struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SlotLimit  *int   `json:"slot_limit"`
	SlotsTaken int    `json:"slots_taken"`
}

// HasCapacity 該組是否還有名額（slot_limit=nil 視為不限）。
func (g GroupSlot) HasCapacity() bool {
	return g.SlotLimit == nil || g.SlotsTaken < *g.SlotLimit
}

// AssignSkipReason 批次加入時單一使用者被跳過的原因。
type AssignSkipReason string

const (
	SkipDuplicate AssignSkipReason = "duplicate"
	SkipGroupFull AssignSkipReason = "group_full"
	SkipDisabled  AssignSkipReason = "disabled"
	SkipNotFound  AssignSkipReason = "not_found"
)

// AssignSkip 批次加入時被跳過的一筆。
type AssignSkip struct {
	UserID string           `json:"user_id"`
	Reason AssignSkipReason `json:"reason"`
}
