package integration

// Garmin 直連（Activity API，推送模式）共用型別與常數。
//
// 推送內容只解析「需要的欄位」（白名單結構），其餘鍵一律忽略；落地到 integration_events 的 payload
// 也是同一份最小化結構（無座標、活動名稱、熱量）。數字／旗標欄位用寬鬆型別解析，型別怪異的欄位
// 視為缺值而不是整筆解析失敗（欄位在官方文件中本來就全是「可能缺」）。

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	garminProvider  = "garmin"
	garminViaDirect = "direct"
	garminViaTerra  = "terra"

	// 事件類型（integration_events.event_type）
	garminEvActivity     = "activity"
	garminEvDeregister   = "deregistration"
	garminEvPermission   = "permission"
	garminPushActivities = "activities"
	garminPushDeregs     = "deregistrations"
	garminPushPerms      = "userPermissionsChange"

	// 事件狀態（integration_events.status）
	garminStPending = "pending"
	garminStDone    = "done"
	garminStError   = "error"
	garminStDead    = "dead"

	// 權限字串（user/permissions 回傳值）
	garminPermActivityExport = "ACTIVITY_EXPORT"

	// 外部 id 前綴：直連匯入的活動 external_id 一律 "gc:" 開頭（與舊 Terra 列區分，見 DeleteGarminActivities／gpscalib／去重）。
	garminExtIDPrefix = "gc:"
	// activities.external_id 是 VARCHAR(64)：超過就改存雜湊。
	garminExtIDMax = 64

	// 欄位長度限制（對應資料表欄位）
	garminUserIDMax    = 64  // integration_events.provider_user_id／user_integrations.provider_user_id
	garminDedupeKeyMax = 160 // integration_events.dedupe_key
	garminDeviceMax    = 60  // activities.device_name

	// OD-4：平均心率要不要存。關閉時連事件 payload 都不保存（資料最小化），並同步政策稿。
	garminStoreAvgHR = true
)

// 事件處理結果詞彙（integration_events.result）。skipped_<原因> 的原因詞彙見 garminSkip*。
const (
	garminResInserted  = "inserted"
	garminResExists    = "exists"
	garminResDuplicate = "duplicate"
	garminResUnknown   = "unknown_user"
	garminResPurged    = "purged"
	garminResOK        = "ok"
)

// 略過原因（result 欄位以 "skipped_" 前綴存）。
const (
	garminSkipNonRunning  = "non_running"
	garminSkipBadPayload  = "bad_payload"
	garminSkipInvalid     = "invalid"
	garminSkipBefore      = "before_connect"
	garminSkipManual      = "manual_or_upload"
	garminSkipParent      = "parent_activity"
	garminSkipImplausible = "implausible"
)

// 重試退避：第 n 次失敗後的等待時間（n 從 1 起）；達到 garminMaxAttempts 次失敗即轉 dead。
var garminBackoff = []time.Duration{1 * time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

const (
	garminMaxAttempts = 5

	// 落地後的處理租約：就地處理期間不被掃描器搶走（與 migration 199 的欄位預設一致）。
	garminEventLease = 2 * time.Minute
	// 掃描器領取事件後的租約（領取者處理期間不被別人重領）。
	garminSweepLease = 3 * time.Minute
	garminSweepBatch = 200

	// 事件保存期限（OD-11）：done 14 天、dead／error（含久未處理的 pending）30 天。
	garminDoneRetentionDays = 14
	garminDeadRetentionDays = 30
)

// 活動類型白名單（比對前轉大寫；容忍首字大寫的 "Running" 之類寫法）。
// OD-1：跑步機／室內／虛擬跑要算（與 Terra 現況一致）；OD-3：輪椅類型不收；MULTI_SPORT 與其他一律不收。
var garminRunTypes = map[string]bool{
	"RUNNING": true, "STREET_RUNNING": true, "TRACK_RUNNING": true, "TRAIL_RUNNING": true,
	"ULTRA_RUN": true, "OBSTACLE_RUN": true, "INDOOR_RUNNING": true, "TREADMILL_RUNNING": true, "VIRTUAL_RUN": true,
}

var garminWalkTypes = map[string]bool{
	"WALKING": true, "CASUAL_WALKING": true, "SPEED_WALKING": true, "HIKING": true, "RUCKING": true,
}

// garminActivityKind 回傳 "run"／"walk"，不在白名單回 ""。
func garminActivityKind(activityType string) string {
	t := strings.ToUpper(strings.TrimSpace(activityType))
	switch {
	case garminRunTypes[t]:
		return "run"
	case garminWalkTypes[t]:
		return "walk"
	}
	return ""
}

// --- 寬鬆 JSON 型別 ---

// garminStr：字串，或 JSON 數字（保留字面文字，不經浮點）；null／其他型別視為空字串。
type garminStr string

func (s *garminStr) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	switch b[0] {
	case '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return nil
		}
		*s = garminStr(garminCleanString(v))
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var n json.Number
		if err := json.Unmarshal(b, &n); err != nil {
			return nil
		}
		*s = garminStr(n.String())
	}
	return nil
}

// garminCleanString 去掉控制字元（含 NUL）與非法 UTF-8 後 trim：這些值會進 TEXT／jsonb 欄位，PostgreSQL 不接受
// NUL（0x00），一個壞字元就會讓整批 INSERT 失敗、被 503 無限重送（毒訊息）。
func garminCleanString(v string) string {
	v = strings.ToValidUTF8(v, "")
	v = strings.Map(func(r rune) rune {
		// U+FFFD：encoding/json 把非法 UTF-8 位元組換成這個替代字元，同樣視為雜訊去掉
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, v)
	return strings.TrimSpace(v)
}

// garminNum：數字，或可轉成數字的字串；其餘視為缺值（OK=false）。NaN／Inf 一律視為缺值。
type garminNum struct {
	V  float64
	OK bool
}

func (n *garminNum) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	var f float64
	switch b[0] {
	case '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return nil
		}
		x, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return nil
		}
		f = x
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if err := json.Unmarshal(b, &f); err != nil {
			return nil
		}
	default:
		return nil
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	n.V, n.OK = f, true
	return nil
}

// garminBool：布林，或 "true"／"false" 字串、0／1 數字；其餘視為 false。
type garminBool bool

func (v *garminBool) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		return nil
	}
	switch b[0] {
	case 't':
		*v = true
	case 'f':
		*v = false
	case '"':
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			*v = garminBool(strings.EqualFold(strings.TrimSpace(s), "true") || strings.TrimSpace(s) == "1")
		}
	case '1':
		*v = true
	}
	return nil
}

// --- 推送白名單結構 ---

// garminActivityIn：activities 推送中單筆摘要的白名單解析（只列需要的鍵；座標、活動名稱、熱量、步頻、
// userAccessToken 等其餘鍵一律不解析、不保存）。CallbackURL 只用來偵測「這其實是 Ping 通知」（DG-16：忽略並計數）。
type garminActivityIn struct {
	UserID          garminStr  `json:"userId"`
	SummaryID       garminStr  `json:"summaryId"`
	ActivityID      garminStr  `json:"activityId"`
	ActivityType    garminStr  `json:"activityType"`
	StartTime       garminNum  `json:"startTimeInSeconds"`
	Duration        garminNum  `json:"durationInSeconds"`
	Distance        garminNum  `json:"distanceInMeters"`
	DeviceName      garminStr  `json:"deviceName"`
	ElevationGain   garminNum  `json:"totalElevationGainInMeters"`
	AvgHeartRate    garminNum  `json:"averageHeartRateInBeatsPerMinute"`
	Manual          garminBool `json:"manual"`
	IsWebUpload     garminBool `json:"isWebUpload"`
	IsParent        garminBool `json:"isParent"`
	ParentSummaryID garminStr  `json:"parentSummaryId"`
	CallbackURL     garminStr  `json:"callbackURL"`
}

// garminStoredActivity：落地到 integration_events.payload 的最小化活動（處理時只讀這份）。
type garminStoredActivity struct {
	SummaryID       string   `json:"summaryId,omitempty"`
	ActivityID      string   `json:"activityId,omitempty"`
	ActivityType    string   `json:"activityType"`
	StartTime       int64    `json:"startTimeInSeconds"`
	Duration        float64  `json:"durationInSeconds"`
	Distance        float64  `json:"distanceInMeters"`
	DeviceName      string   `json:"deviceName,omitempty"`
	ElevationGain   *float64 `json:"totalElevationGainInMeters,omitempty"`
	AvgHeartRate    *float64 `json:"averageHeartRateInBeatsPerMinute,omitempty"`
	Manual          bool     `json:"manual,omitempty"`
	IsWebUpload     bool     `json:"isWebUpload,omitempty"`
	IsParent        bool     `json:"isParent,omitempty"`
	ParentSummaryID string   `json:"parentSummaryId,omitempty"`
}

// garminDeregIn：deregistrations 推送的單筆（只有 userId）。
type garminDeregIn struct {
	UserID garminStr `json:"userId"`
}

// garminPermIn：userPermissionsChange 推送的單筆。permissions 內容不被信任（處理時一律以 API 取當下權限），
// 只保存事件資訊供對帳。
type garminPermIn struct {
	UserID      garminStr       `json:"userId"`
	SummaryID   garminStr       `json:"summaryId"`
	ChangeTime  garminNum       `json:"changeTimeInSeconds"`
	Permissions json.RawMessage `json:"permissions"`
}

// garminStoredPermission：permission 事件落地 payload。
type garminStoredPermission struct {
	SummaryID   string   `json:"summaryId,omitempty"`
	ChangeTime  int64    `json:"changeTimeInSeconds,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

// garminParsePermissions 容忍 permissions 是字串陣列、或其他形狀（回空）；最多取 16 個、每個 ≤40 字元且只含 [A-Z_]。
func garminParsePermissions(raw json.RawMessage) []string {
	var arr []string
	if len(raw) == 0 || json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	var out []string
	for _, p := range arr {
		p = strings.TrimSpace(p)
		if p == "" || len(p) > 40 || strings.Trim(p, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") != "" {
			continue
		}
		out = append(out, p)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// --- 事件列 ---

// garminEventIn：要寫入 integration_events 的一筆（已最小化）。
type garminEventIn struct {
	EventType      string
	ProviderUserID string
	DedupeKey      *string
	Payload        []byte // JSON
}

// garminEvent：integration_events 一列（領取／落地後回傳）。
type garminEvent struct {
	ID             string
	EventType      string
	ProviderUserID string
	DedupeKey      *string
	Payload        []byte
	Attempts       int
	ReceivedAt     time.Time
}

// garminEventStats：統計用（日報／維運）。
type garminEventStats struct {
	Received24h  int // 24 小時內收到（落地）的事件數
	Done24h      int
	Imported24h  int // 24 小時內 result='inserted' 的活動事件
	Skipped24h   int // 24 小時內 result 以 skipped_ 開頭
	PendingStale int // pending／error 且已逾期超過 10 分鐘者
	Dead         int // 目前 dead 的事件數（保存期限內）
}

var (
	// errGarminTruncated：推送 body 讀取中斷或 JSON 提前結束——必須回非 200 讓 Garmin 重送（R-C3）。
	errGarminTruncated = errors.New("garmin: push body truncated")
	// errGarminBadJSON：完整讀完但內容不是合法 JSON／不是物件——回 200 並記 log（避免毒訊息被無限重送）。
	errGarminBadJSON = errors.New("garmin: push body is not a valid json object")
	// errGarminTooLarge：解壓後超過上限。
	errGarminTooLarge = errors.New("garmin: push body too large")
	// errGarminBusy：同一使用者處理租約被別人持有（暫時性，稍後重試）。
	errGarminBusy = errors.New("garmin: user busy")
	// errGarminNotReady：處理器尚未就緒（骨架階段占位）；事件保持 pending，不計入失敗次數。
	errGarminNotReady = errors.New("garmin: processor not ready")
)
