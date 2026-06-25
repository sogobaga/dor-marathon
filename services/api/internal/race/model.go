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
	Status       string     `json:"status"` // soon|open|live|done
	Distances    []int      `json:"distances"`
	GroupType    string     `json:"group_type"` // faction|club|distance
	GroupMode    string     `json:"group_mode"` // random|self
	SlotsTotal   int        `json:"slots_total"`
	EntryFee     int        `json:"entry_fee"` // 分（NT$ × 100）
	StartDate    time.Time  `json:"start_date"`
	EndDate      time.Time  `json:"end_date"`
	Config       RaceConfig `json:"config"`
	CreatedBy    string     `json:"created_by,omitempty"` // organizer userID
	ReviewStatus string     `json:"review_status"`        // pending|approved|rejected
	ReviewNote   string     `json:"review_note,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
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
	ID       string     `json:"id"`
	UserID   string     `json:"user_id"`
	RaceID   string     `json:"race_id"`
	Distance int        `json:"distance"`
	Faction  string     `json:"faction,omitempty"`
	Status   string     `json:"status"` // pending|paid|cancelled
	PaidAt   *time.Time `json:"paid_at,omitempty"`
	Amount   int        `json:"amount"`
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
