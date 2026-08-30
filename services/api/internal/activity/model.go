package activity

import "time"

// Activity 跑步活動（來自前端上傳）
type Activity struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	RaceID     string    `json:"race_id,omitempty"`
	MissionDay int       `json:"mission_day,omitempty"`
	DistanceKm float64   `json:"distance_km"`
	DurationS  int       `json:"duration_s"` // 總秒數
	AvgPaceS   int       `json:"avg_pace_s"` // 秒/公里
	RecordedAt time.Time `json:"recorded_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// UploadRequest 前端上傳格式
type UploadRequest struct {
	RaceID     string  `json:"race_id"`      // 可為空（非賽事跑步）
	MissionDay int     `json:"mission_day"`  // 對應今日任務（0 = 無）
	DistanceKm float64 `json:"distance_km"`
	DurationS  int     `json:"duration_s"`
	RecordedAt string  `json:"recorded_at"` // ISO8601，e.g. "2026-06-25T08:30:00Z"
}

// UploadResult API 回應
type UploadResult struct {
	Activity        *Activity        `json:"activity"`
	MissionResult   *MissionResult   `json:"mission_result,omitempty"`
	RankingUpdate   *RankingUpdate   `json:"ranking_update,omitempty"`
}

// MissionResult 任務完成結果
type MissionResult struct {
	Day         int     `json:"day"`
	Completed   bool    `json:"completed"`
	RescueCount int     `json:"rescue_count"` // 解救隊友數
	PaceValid   bool    `json:"pace_valid"`   // 配速是否符合任務要求
	ExtraKm     float64 `json:"extra_km"`     // 超過基礎里程的距離
}

// RankingUpdate 排行榜更新結果
type RankingUpdate struct {
	OldRank    int     `json:"old_rank"`
	NewRank    int     `json:"new_rank"`
	TotalKm    float64 `json:"total_km"`
	AddedKm    float64 `json:"added_km"`
}

// ActivityEvent 推送到 Redis Streams 的事件格式
type ActivityEvent struct {
	UserID     string  `json:"user_id"`
	RaceID     string  `json:"race_id"`
	MissionDay int     `json:"mission_day"`
	DistanceKm float64 `json:"distance_km"`
	DurationS  int     `json:"duration_s"`
	AvgPaceS   int     `json:"avg_pace_s"`
	RecordedAt string  `json:"recorded_at"`
	KmPaces    []int   `json:"km_paces,omitempty"` // 每公里分段配速(秒/km)；GPS 追蹤才有，Strava/手動為空
	// GPS 距離校正（見 internal/gpscalib）：RawDistanceKm 為伺服器重算的原始距離（未套校正）、
	// CalibFactor 為上傳當下生效的係數；DistanceKm 已是 round2(RawDistanceKm*CalibFactor)。兩者
	// 皆 omitempty——非 GPS 來源（後台補里程/GPS 審核核准）不帶這兩欄時，worker 端會 fallback
	// RawDistanceKm=DistanceKm、CalibFactor=1.0（見 services/worker/main.go processOne）。
	RawDistanceKm float64 `json:"raw_distance_km,omitempty"`
	CalibFactor   float64 `json:"calib_factor,omitempty"`
}
