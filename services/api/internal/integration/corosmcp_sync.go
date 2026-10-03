package integration

// COROS MCP 第二階段：匯入、去重、自動同步、型號標示。契約：docs/integration/COROS_MCP_STAGE2_CONTRACT.md。
//
// 原則（契約第 0 點）：
//   - 一律走 Repository.ImportActivity ＋ 與 terra.go importTerra 完全相同條件的三段尾巴
//     （gpscalib.RecomputeAsync／stamina.ChargeSP／AwardMileageExp），不 raw INSERT、不改其他 importer。
//   - 活動以 source='coros'、external_id='mcp:'+labelId 落地：沿用所有 'coros' 清單（去重排名、gpscalib、
//     COROS 標籤、外部資料閘門）；'mcp:' 前綴與 Terra（summary_id）、Partner（純 labelId）不撞鍵。
//   - 不保存 COROS 回傳的 Location 與起點座標（解析器根本不讀這兩行）、不下載 FIT。
//   - 不新增任何排程：同步只由使用者動作觸發（手動 /import、打開 DOR 時的 Dashboard 自動同步）。

import (
	"context"
	"errors"
	"fmt"
	"math"
	mrand "math/rand"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/integration/entrygate"
)

const (
	corosMcpSourceCoros = "coros"
	corosMcpExtIDPrefix = "mcp:"
	// corosMcpAutoSyncWindow：自動同步每人最短間隔（Redis SET NX EX 1500 ＝ 25 分鐘）。手動匯入的節流窗口
	// （5 分鐘）與 in-flight 鎖、冷卻等見 corosmcp_throttle.go。
	corosMcpAutoSyncWindow = 1500 * time.Second
	corosMcpAutoSyncDays   = 3                // 自動同步回看天數
	corosMcpAutoSyncTO     = 60 * time.Second // 自動同步整體逾時（不含觸發前的隨機延遲）
	corosMcpMaxImportDays  = 30               // worker 去重只看 45 天，回補上限 30 天（比照 Terra）
	corosMcpManualDays     = 3                // 手動匯入回看天數（GA 契約 §3.1；首次連接 30 天）
	// corosMcpSPMaxAge：只有活動結束時間距今 ≤24 小時才扣體力 SP（見 importMcpActivity）。
	corosMcpSPMaxAge       = 24 * time.Hour
	corosMcpChunkDays      = 10 // querySportRecords 單次最多查 10 天
	corosMcpRecordsLimit   = 50 // 單次回傳上限；回傳筆數＝上限時記 warn（可能被截斷）
	corosMcpDeviceMaxRunes = 60 // activities.device_name VARCHAR(60)
)

var (
	errCorosMcpNotConnected = errors.New("coros mcp: not connected")
	errCorosMcpAnomaly      = errors.New("COROS 拒絕這次呼叫（參數格式不符規格）")

	// 台灣時區：COROS 的 yyyyMMdd 日期篩選以使用者本地日期計；DOR 使用者在台灣，固定用 UTC+8。
	corosMcpTZ = time.FixedZone("UTC+8", 8*3600)
)

// DOR 計入的 COROS 運動代碼（與 corosMcpDORSportTypes 同一份，轉成 set 供 O(1) 判斷）：
// 100 戶外跑、101 室內跑、102 越野跑、103 田徑場跑、104 健行、900 走路。
var corosMcpSportTypeSet = func() map[int]bool {
	m := map[int]bool{}
	for _, t := range corosMcpDORSportTypes {
		m[t] = true
	}
	return m
}()

// corosMcpWalkSportTypes：DOR 計入的 COROS 運動代碼中屬於「步行類」的（104 健行、900 走路）。
var corosMcpWalkSportTypes = map[int]bool{104: true, 900: true}

// --- querySportRecords 文字解析（純函式）---

// corosMcpRecord 單筆 querySportRecords 解析結果。刻意沒有 Location／座標欄位（契約：不保存）。
type corosMcpRecord struct {
	Title      string // 「N. 」後的標題行（運動名稱＋日期），只做除錯
	LabelID    string // 原樣字串（17–18 位數，絕不轉數字）
	SportType  int
	StartUnix  int64
	EndUnix    int64
	DurationS  int     // Duration（mm:ss 或 h:mm:ss）換算秒；解析不到＝0
	DistanceKm float64 // 單位非 km／m（例如 sets）或缺少＝0
	AvgHR      *int    // 缺＝nil
}

var (
	// 編號行：「1. Outdoor Run — 2026-09-29」。只有行首的「N. 」算分段點。
	corosMcpItemStartRe = regexp.MustCompile(`^\s*(\d{1,3})\.\s+(.*)$`)
	corosMcpStartTsRe   = regexp.MustCompile(`(?i)startTimestamp\s*[=:]\s*(\d{9,13})`)
	corosMcpEndTsRe     = regexp.MustCompile(`(?i)endTimestamp\s*[=:]\s*(\d{9,13})`)
	corosMcpDurationRe  = regexp.MustCompile(`(?i)\bDuration:\s*(\d{1,3}):(\d{2})(?::(\d{2}))?`)
	corosMcpDistanceRe  = regexp.MustCompile(`(?i)\bDistance:\s*([0-9][0-9,]*(?:\.[0-9]+)?)\s*([A-Za-z]+)?`)
	corosMcpAvgHRRe     = regexp.MustCompile(`(?i)\bAvg\s*HR:\s*(\d{2,3})`)
	corosMcpRecLabelRe  = regexp.MustCompile(`(?i)\bLabel\s*_?Id:\s*([0-9A-Za-z_-]{4,})`)
	corosMcpRecSportRe  = regexp.MustCompile(`(?i)\bSport\s*_?Type:\s*(\d{1,6})`)
)

// corosMcpTsToUnix：COROS 說明是 unix 秒；萬一給到毫秒（13 位）就換成秒。
func corosMcpTsToUnix(raw string) int64 {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	if n > 1e11 {
		n /= 1000
	}
	return n
}

// parseCorosMcpSportRecords 解析 querySportRecords 的文字回應（外層引號先解開）。沒有 LabelId 的分段
// （標題「Workout Records (N)」等）略過；欄位缺漏的分段仍回傳（零值），由對應步驟歸類成 skipped_invalid。
func parseCorosMcpSportRecords(text string) []corosMcpRecord {
	text = corosMcpUnwrapText(text)
	var out []corosMcpRecord
	var cur []string
	title := ""
	flush := func() {
		if cur == nil {
			return
		}
		if rec, ok := parseCorosMcpRecordBlock(title, strings.Join(cur, "\n")); ok {
			out = append(out, rec)
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := corosMcpItemStartRe.FindStringSubmatch(line); m != nil {
			flush()
			title = strings.TrimSpace(m[2])
			cur = []string{}
			continue
		}
		if cur != nil {
			cur = append(cur, line)
		}
	}
	flush()
	return out
}

func parseCorosMcpRecordBlock(title, block string) (corosMcpRecord, bool) {
	m := corosMcpRecLabelRe.FindStringSubmatch(block)
	if m == nil {
		return corosMcpRecord{}, false
	}
	rec := corosMcpRecord{Title: title, LabelID: m[1]}
	if m := corosMcpRecSportRe.FindStringSubmatch(block); m != nil {
		rec.SportType, _ = strconv.Atoi(m[1])
	}
	if m := corosMcpStartTsRe.FindStringSubmatch(block); m != nil {
		rec.StartUnix = corosMcpTsToUnix(m[1])
	}
	if m := corosMcpEndTsRe.FindStringSubmatch(block); m != nil {
		rec.EndUnix = corosMcpTsToUnix(m[1])
	}
	if m := corosMcpDurationRe.FindStringSubmatch(block); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if m[3] != "" { // h:mm:ss
			c, _ := strconv.Atoi(m[3])
			rec.DurationS = a*3600 + b*60 + c
		} else { // mm:ss
			rec.DurationS = a*60 + b
		}
	}
	if m := corosMcpDistanceRe.FindStringSubmatch(block); m != nil {
		v, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
		if err == nil {
			switch strings.ToLower(m[2]) {
			case "km":
				rec.DistanceKm = v
			case "m":
				rec.DistanceKm = v / 1000
			default: // sets／次數等非距離單位 → 0，後續歸 skipped_invalid
			}
		}
	}
	if m := corosMcpAvgHRRe.FindStringSubmatch(block); m != nil {
		if hr, err := strconv.Atoi(m[1]); err == nil && hr > 0 {
			rec.AvgHR = &hr
		}
	}
	return rec, true
}

// parseCorosMcpDeviceName 解析 queryDevices 文字，取清單第一支裝置名稱（「1. 」後那行，去頭尾空白、最長 60 字）。
// 活動本身不含型號，所以多支裝置時一律取第一支（已知限制）；解析不到回 nil（寫入 NULL）。
func parseCorosMcpDeviceName(text string) *string {
	text = corosMcpUnwrapText(text)
	if corosMcpIsAnomaly(text) {
		return nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := corosMcpItemStartRe.FindStringSubmatch(line); m != nil {
			name := strings.TrimSpace(m[2])
			if name == "" {
				return nil
			}
			if r := []rune(name); len(r) > corosMcpDeviceMaxRunes {
				name = strings.TrimSpace(string(r[:corosMcpDeviceMaxRunes]))
			}
			return &name
		}
	}
	return nil
}

// --- 對應（純函式）---

// 略過原因（mapCorosMcpRecord 回傳）。
const (
	corosMcpSkipNone          = ""
	corosMcpSkipNonRunning    = "non_running"
	corosMcpSkipInvalid       = "invalid"
	corosMcpSkipBeforeConnect = "before_connect"
)

// mapCorosMcpRecord 把單筆解析結果對應成 NormalizedActivity；skip 非空代表不該匯入（原因見常數）。
// 判斷順序依契約：運動代碼白名單 → distance／duration／開始時間有效 → start 早於連線 floor。
func mapCorosMcpRecord(userID string, floor time.Time, device *string, rec corosMcpRecord) (*NormalizedActivity, string) {
	if !corosMcpSportTypeSet[rec.SportType] {
		return nil, corosMcpSkipNonRunning
	}
	if rec.DistanceKm <= 0 || rec.DurationS <= 0 || rec.StartUnix <= 0 || rec.LabelID == "" {
		return nil, corosMcpSkipInvalid
	}
	recordedAt := time.Unix(rec.StartUnix, 0).UTC()
	// 連接當下以前的資料一律不抓（比照 strava/coros/terra 的 floor 保護；floor＝coros_mcp 連線列 created_at）
	if !floor.IsZero() && recordedAt.Before(floor) {
		return nil, corosMcpSkipBeforeConnect
	}
	na := &NormalizedActivity{
		UserID:      userID,
		Source:      corosMcpSourceCoros,
		ExternalID:  corosMcpExtIDPrefix + rec.LabelID,
		Fingerprint: fingerprintOf(rec.StartUnix, rec.DistanceKm*1000, rec.DurationS),
		DistanceKm:  rec.DistanceKm,
		DurationS:   rec.DurationS,
		AvgPaceS:    int(math.Round(float64(rec.DurationS) / rec.DistanceKm)), // 不用 provider 配速
		AvgHR:       rec.AvgHR,
		RecordedAt:  recordedAt, // 外部來源 recorded_at 存「開始」時間（UTC）
		DeviceName:  device,
	}
	if el := int(rec.EndUnix - rec.StartUnix); rec.EndUnix > 0 && el > 0 {
		na.ElapsedS = &el
	}
	// 健行（104）、走路（900）→ 步行類：合理性檢查的配速門檻是 4:00/km（跑步類 2:30/km，見 plausibility.go）。
	if corosMcpWalkSportTypes[rec.SportType] {
		na.Kind = KindWalk
	}
	return na, corosMcpSkipNone
}

// --- 同步核心 ---

// CorosMcpSyncResult 同步結果（POST /import 與自動同步共用；欄位名稱是前台契約）。
type CorosMcpSyncResult struct {
	Fetched              int     `json:"fetched"`
	Imported             int     `json:"imported"`
	Duplicate            int     `json:"duplicate"`
	Exists               int     `json:"exists"`
	SkippedBeforeConnect int     `json:"skipped_before_connect"`
	SkippedNonRunning    int     `json:"skipped_non_running"`
	SkippedInvalid       int     `json:"skipped_invalid"`
	Errors               int     `json:"errors"`
	DeviceName           *string `json:"device_name"`
}

// corosMcpSportRecordsRangeArgs 組 querySportRecords 參數：schema 要求全部 10 個欄位都出現（不用就給 null），
// 日期必須 yyyyMMdd（見 corosMcpSportRecordsArgs 註解）。from／to 取台灣日期，含頭含尾。
func corosMcpSportRecordsRangeArgs(from, to time.Time, limit int) map[string]any {
	args := map[string]any{}
	for _, k := range []string{"startDate", "endDate", "sportTypeCodes", "minDistanceKm", "maxDistanceKm",
		"minDurationMinutes", "maxDurationMinutes", "maxAveragePace", "locationKeyword", "limit"} {
		args[k] = nil
	}
	args["startDate"] = from.In(corosMcpTZ).Format("20060102")
	args["endDate"] = to.In(corosMcpTZ).Format("20060102")
	args["sportTypeCodes"] = corosMcpDORSportTypes
	args["limit"] = limit
	return args
}

// corosMcpDayChunks 把 [from,to]（台灣日期）切成每段 ≤corosMcpChunkDays 天的 [start,end] 日期段。
func corosMcpDayChunks(from, to time.Time) [][2]time.Time {
	s := from.In(corosMcpTZ)
	s = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, corosMcpTZ)
	e := to.In(corosMcpTZ)
	e = time.Date(e.Year(), e.Month(), e.Day(), 0, 0, 0, 0, corosMcpTZ)
	var out [][2]time.Time
	for cur := s; !cur.After(e); cur = cur.AddDate(0, 0, corosMcpChunkDays) {
		ce := cur.AddDate(0, 0, corosMcpChunkDays-1)
		if ce.After(e) {
			ce = e
		}
		out = append(out, [2]time.Time{cur, ce})
	}
	return out
}

// fetchDeviceName 呼叫 queryDevices 一次取第一支裝置名稱。失敗只記 warn、回 nil（型號是加分項，不擋匯入）。
func (h *CorosMcpHandler) fetchDeviceName(ctx context.Context, conn *corosMcpConnection) *string {
	res, err := h.toolsCall(ctx, conn, "queryDevices", map[string]any{})
	if err != nil {
		log.Warn().Err(err).Str("user", conn.UserID).Msg("coros mcp sync: queryDevices failed (device_name stays NULL)")
		return nil
	}
	return parseCorosMcpDeviceName(corosMcpResultText(res))
}

// fetchSportRecords 分段（≤10 天）查 querySportRecords 並解析；anomaly 視為整次同步失敗（呼叫端據此不寫任何東西）。
// 以 labelId 去重（段與段日期不重疊，這只是防呆）。
func (h *CorosMcpHandler) fetchSportRecords(ctx context.Context, conn *corosMcpConnection, from, to time.Time) ([]corosMcpRecord, error) {
	seen := map[string]bool{}
	var recs []corosMcpRecord
	for _, ch := range corosMcpDayChunks(from, to) {
		args := corosMcpSportRecordsRangeArgs(ch[0], ch[1], corosMcpRecordsLimit)
		res, err := h.toolsCall(ctx, conn, "querySportRecords", args)
		if err != nil {
			return nil, err
		}
		text := corosMcpResultText(res)
		if corosMcpIsAnomaly(text) {
			return nil, errCorosMcpAnomaly
		}
		part := parseCorosMcpSportRecords(text)
		if len(part) >= corosMcpRecordsLimit {
			log.Warn().Str("user", conn.UserID).Int("n", len(part)).Msg("coros mcp sync: querySportRecords hit limit, older records in this chunk may be truncated")
		}
		for _, r := range part {
			if !seen[r.LabelID] {
				seen[r.LabelID] = true
				recs = append(recs, r)
			}
		}
	}
	return recs, nil
}

// importMcpActivity 落地單筆：ImportActivity（含合理性檢查、直連優先去重）→ AfterImport 尾巴
// （S6，importtail.go）。
//   - SkipGPSCalib：mcp 列已被 gpscalib 候選排除（GA 契約 §3.4），匯入後重算校正沒有意義。
//   - MaxSPAge＝24 小時：補抓／重新授權補同步撈回來的舊活動不再扣體力（SP 恢復是從「現在」起算，事後才扣
//     等於重複懲罰；稽核 low「SP 以匯入時間而非跑步時間扣血」）。
func (h *CorosMcpHandler) importMcpActivity(ctx context.Context, na *NormalizedActivity) (ImportResult, error) {
	res, err := h.repo.ImportActivity(ctx, na)
	if err != nil {
		return res, err
	}
	h.repo.AfterImport(ctx, na, res, TailOptions{SkipGPSCalib: true, MaxSPAge: corosMcpSPMaxAge})
	return res, nil
}

// corosMcpSyncOpts 單次同步的差異設定。
type corosMcpSyncOpts struct {
	// MinInterval：第二道節流（不依賴 Redis）——last_synced_at 距今不足就回 errCorosMcpTooSoon；0＝不檢查。
	MinInterval time.Duration
	// CatchUp：起點改用「重新授權補同步」公式（corosMcpCatchUpFrom），忽略傳入的 from。
	CatchUp bool
}

// corosMcpTooSoonError 帶「還要等多久」的 errCorosMcpTooSoon。
type corosMcpTooSoonError struct{ RetryAfter time.Duration }

func (e *corosMcpTooSoonError) Error() string { return errCorosMcpTooSoon.Error() }
func (e *corosMcpTooSoonError) Unwrap() error { return errCorosMcpTooSoon }

// corosMcpCooldownError 帶「還要冷卻多久」的 errCorosMcpCooldown。
type corosMcpCooldownError struct{ RetryAfter time.Duration }

func (e *corosMcpCooldownError) Error() string { return errCorosMcpCooldown.Error() }
func (e *corosMcpCooldownError) Unwrap() error { return errCorosMcpCooldown }

// retryAfterSeconds 取錯誤鏈裡的「還要等幾秒」（至少 1）；沒有就用 fallback。
func retryAfterSeconds(err error, fallback time.Duration) int {
	var ts *corosMcpTooSoonError
	var cd *corosMcpCooldownError
	d := fallback
	switch {
	case errors.As(err, &ts):
		d = ts.RetryAfter
	case errors.As(err, &cd):
		d = cd.RetryAfter
	}
	if s := int(d.Seconds()) + 1; s > 1 {
		return s
	}
	return 1
}

// syncCorosMcp 同步核心（手動 /import、自動同步、重新授權補同步共用）：取連線（無→errCorosMcpNotConnected；
// 已標記需要重新授權→errCorosMcpReconnect，不打 COROS）→ 第二道節流 → 必要時換 token（errCorosMcpReconnect
// 照舊往上傳）→ queryDevices → 分段 querySportRecords → 逐筆（由舊到新，讓重疊偵測與 EXP 差額補償順序固定）
// 對應＋匯入＋尾巴 → 無錯誤時更新 last_synced_at。from 會被夾到連線 floor（created_at）之後——重新授權不再重設
// created_at，所以 floor 是「第一次連接」的時間。
//
// 呼叫端負責 in-flight 鎖（見 runSync、Import）。
func (h *CorosMcpHandler) syncCorosMcp(ctx context.Context, userID string, from, to time.Time, opts corosMcpSyncOpts) (CorosMcpSyncResult, error) {
	var out CorosMcpSyncResult
	conn, err := h.getConnection(ctx, userID)
	if err != nil {
		return out, err
	}
	if conn == nil {
		return out, errCorosMcpNotConnected
	}
	if conn.ReauthRequiredAt != nil {
		return out, errCorosMcpReconnect // 已知授權失效：等使用者重新授權，不白打 COROS
	}
	if opts.MinInterval > 0 && conn.LastSyncedAt != nil {
		if since := h.timeNow().Sub(*conn.LastSyncedAt); since < opts.MinInterval {
			return out, &corosMcpTooSoonError{RetryAfter: opts.MinInterval - since}
		}
	}
	switch {
	case opts.CatchUp:
		from = corosMcpCatchUpFrom(to, conn.ConnectedAt, conn.LastSyncedAt)
	case from.IsZero():
		// 自動同步：從上次成功同步往回補（Stage 2 審查：只看 3 天會漏掉「超過 3 天沒開 DOR」期間的跑步）
		from = corosMcpAutoFrom(to, conn.LastSyncedAt)
	}
	floor := conn.ConnectedAt
	if from.Before(floor) {
		from = floor
	}
	if err := h.ensureFreshToken(ctx, conn); err != nil {
		return out, err
	}
	out.DeviceName = h.fetchDeviceName(ctx, conn)
	recs, err := h.fetchSportRecords(ctx, conn, from, to)
	if err != nil {
		return out, err
	}
	out.Fetched = len(recs)
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].StartUnix < recs[j].StartUnix })
	for _, rec := range recs {
		na, skip := mapCorosMcpRecord(userID, floor, out.DeviceName, rec)
		switch skip {
		case corosMcpSkipNonRunning:
			out.SkippedNonRunning++
			continue
		case corosMcpSkipInvalid:
			out.SkippedInvalid++
			continue
		case corosMcpSkipBeforeConnect:
			out.SkippedBeforeConnect++
			continue
		}
		// 使用者若在同步途中按了「中斷連線」（Disconnect 已刪 mcp: 列與連線列），這裡停手，不再寫入孤兒列
		// （Stage 2 審查：背景自動同步或手動匯入可能與中斷同時進行）。
		if !h.corosMcpStillConnected(ctx, userID) {
			log.Info().Str("user", userID).Msg("coros mcp sync: connection removed mid-sync, stopping")
			return out, errCorosMcpNotConnected
		}
		res, err := h.importMcpActivity(ctx, na)
		if err != nil {
			log.Error().Err(err).Str("user", userID).Msg("coros mcp sync: import activity failed")
			out.Errors++
			continue
		}
		switch res.Status {
		case "inserted":
			out.Imported++
		case "duplicate":
			out.Duplicate++
		case "skipped": // 合理性檢查判定不該匯入（未來時間、距離／時間過短）
			out.SkippedInvalid++
		default:
			out.Exists++
		}
	}
	if out.Errors == 0 {
		if err := h.touchLastSynced(ctx, userID, time.Now()); err != nil {
			log.Warn().Err(err).Str("user", userID).Msg("coros mcp sync: touch last_synced_at failed")
		}
	}
	return out, nil
}

// corosMcpAutoFrom 純函式：自動同步的起點。沒同步過→往回 30 天（之後再被連線 floor 夾住）；同步過→取
// 「now−3 天」與「上次成功同步−1 天」較早者（多 1 天緩衝台灣日界），最多往回 30 天。重抓的部分由
// ON CONFLICT (source, external_id) 擋掉，不會重複寫入。
func corosMcpAutoFrom(now time.Time, lastSynced *time.Time) time.Time {
	earliest := now.AddDate(0, 0, -corosMcpMaxImportDays)
	if lastSynced == nil {
		return earliest
	}
	from := now.AddDate(0, 0, -corosMcpAutoSyncDays)
	if ls := lastSynced.Add(-24 * time.Hour); ls.Before(from) {
		from = ls
	}
	if from.Before(earliest) {
		from = earliest
	}
	return from
}

// corosMcpCatchUpFrom 純函式：重新授權成功後「補同步」的起點（GA 契約 §2.4）＝ max(floor, last_synced_at − 1 天)，
// 上限往回 30 天；從未成功同步過→往回 30 天（再被 floor 夾住）。floor 為零值＝不夾。
func corosMcpCatchUpFrom(now, floor time.Time, lastSynced *time.Time) time.Time {
	earliest := now.AddDate(0, 0, -corosMcpMaxImportDays)
	from := earliest
	if lastSynced != nil {
		from = lastSynced.Add(-24 * time.Hour)
	}
	if from.Before(earliest) {
		from = earliest
	}
	if !floor.IsZero() && from.Before(floor) {
		from = floor
	}
	return from
}

// corosMcpStillConnected：MCP 連線列是否還在（同步途中每筆寫入前檢查；查詢失敗時當作仍連線，交給後續步驟處理）。
func (h *CorosMcpHandler) corosMcpStillConnected(ctx context.Context, userID string) bool {
	var ok bool
	if err := h.repo.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_integrations WHERE user_id=$1 AND provider=$2)`, userID, providerCorosMcp).Scan(&ok); err != nil {
		return true
	}
	return ok
}

func (h *CorosMcpHandler) touchLastSynced(ctx context.Context, userID string, at time.Time) error {
	_, err := h.repo.db.Exec(ctx,
		`UPDATE user_integrations SET last_synced_at=$1 WHERE user_id=$2 AND provider=$3`, at, userID, providerCorosMcp)
	return err
}

// latestDeviceName /status 的 device_name 來源：該使用者最新一筆 MCP 匯入活動帶的型號（沒有則 nil）。
// 選擇「最新活動的型號」而非「即時呼叫 queryDevices」，讓 /status 不必打 COROS、也不依賴 token 是否有效。
func (h *CorosMcpHandler) latestDeviceName(ctx context.Context, userID string) *string {
	var name *string
	err := h.repo.db.QueryRow(ctx, `
		SELECT device_name FROM activities
		WHERE user_id=$1 AND source='coros' AND external_id LIKE 'mcp:%' AND device_name IS NOT NULL
		ORDER BY recorded_at DESC LIMIT 1`, userID).Scan(&name)
	if err != nil {
		return nil
	}
	return name
}

// runSync 同步的共用外殼（自動同步、重新授權補同步）：in-flight 鎖（Redis，跨副本；占用中回 errCorosMcpBusy）→
// 冷卻檢查（COROS 429／5xx 後 30 分鐘，回 errCorosMcpCooldown）→ syncCorosMcp → 錯誤分類（429／5xx → 設冷卻）→ 釋放鎖。
func (h *CorosMcpHandler) runSync(ctx context.Context, userID string, from, to time.Time, opts corosMcpSyncOpts) (CorosMcpSyncResult, error) {
	release, ok := h.acquireSyncLock(ctx, userID)
	if !ok {
		return CorosMcpSyncResult{}, errCorosMcpBusy
	}
	defer release()
	if left := h.cooldownLeft(ctx, userID); left > 0 {
		return CorosMcpSyncResult{}, &corosMcpCooldownError{RetryAfter: left}
	}
	res, err := h.syncCorosMcp(ctx, userID, from, to, opts)
	if isCorosThrottleErr(err) {
		h.setCooldown(ctx, userID)
	}
	return res, err
}

// --- 手動匯入 ---

// corosMcpImportResponse POST /import 回應：同步結果＋實際使用的 days。
type corosMcpImportResponse struct {
	Days int `json:"days"`
	CorosMcpSyncResult
}

// corosMcpManualDaysFor 手動匯入的回看天數上限（GA 契約 §3.1）：首次連接（從未成功同步）30 天，其餘 3 天。
// 傳入的 days（query 參數）夾在 [1, 上限]；沒傳＝上限。
func corosMcpManualDaysFor(lastSynced *time.Time, requested int, hasRequested bool) int {
	max := corosMcpManualDays
	if lastSynced == nil {
		max = corosMcpMaxImportDays
	}
	if !hasRequested {
		return max
	}
	if requested < 1 {
		return 1
	}
	if requested > max {
		return max
	}
	return requested
}

// POST /import?days=N
// 入口閘門同 /connect。節流（契約 §3.1）：每人 ≥5 分鐘（Redis，跨副本；last_synced_at 第二道）、與自動同步共用
// in-flight 鎖、COROS 429／5xx 後冷卻 30 分鐘。回看天數：3 天（首次連接 30 天）。
// 409 {"error":"not_connected"|"reconnect_required"}、429 {"error":"rate_limited"|"sync_in_progress"|"cooldown",
// "retry_after_s":N}、403 {"error":"forbidden"}、502 {"error":"…"}（COROS 失敗，含 anomaly）、200 corosMcpImportResponse。
func (h *CorosMcpHandler) Import(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.requireEntry(w, r)
	if !ok {
		return
	}
	conn, err := h.getConnectionMeta(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if conn == nil {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "not_connected"})
		return
	}
	if conn.NeedsReauth(h.timeNow()) {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "reconnect_required"})
		return
	}
	if left := h.cooldownLeft(r.Context(), userID); left > 0 {
		respondJSON(w, http.StatusTooManyRequests, map[string]any{"error": "cooldown", "retry_after_s": retryAfterSeconds(nil, left)})
		return
	}
	release, got := h.acquireSyncLock(r.Context(), userID)
	if !got {
		respondJSON(w, http.StatusTooManyRequests, map[string]any{"error": "sync_in_progress", "retry_after_s": 10})
		return
	}
	defer release()
	if allow, retryAfter := h.claimManual(r.Context(), userID); !allow {
		respondJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "retry_after_s": retryAfter})
		return
	}
	requested, hasRequested := 0, false
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			requested, hasRequested = n, true
		}
	}
	days := corosMcpManualDaysFor(conn.LastSyncedAt, requested, hasRequested)
	now := time.Now()
	res, err := h.syncCorosMcp(r.Context(), userID, now.AddDate(0, 0, -days), now, corosMcpSyncOpts{MinInterval: corosMcpManualWindow})
	if err != nil {
		if isCorosThrottleErr(err) {
			h.setCooldown(r.Context(), userID)
		}
		switch {
		case errors.Is(err, errCorosMcpReconnect):
			respondJSON(w, http.StatusConflict, map[string]string{"error": "reconnect_required"})
		case errors.Is(err, errCorosMcpNotConnected):
			respondJSON(w, http.StatusConflict, map[string]string{"error": "not_connected"})
		case errors.Is(err, errCorosMcpTooSoon):
			respondJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "retry_after_s": retryAfterSeconds(err, corosMcpManualWindow)})
		default:
			log.Error().Err(err).Str("user", userID).Str("code", corosMcpErrorCode(err)).Msg("coros mcp manual import failed")
			respondErr(w, http.StatusBadGateway, "向 COROS 取得活動失敗")
		}
		return
	}
	log.Info().Str("user", userID).Int("days", days).Int("fetched", res.Fetched).Int("imported", res.Imported).
		Int("duplicate", res.Duplicate).Int("exists", res.Exists).Int("errors", res.Errors).Msg("coros mcp manual import done")
	respondJSON(w, http.StatusOK, corosMcpImportResponse{Days: days, CorosMcpSyncResult: res})
}

// --- 自動同步（Dashboard 觸發）---

// autoSyncEnabled 自動同步總開關（app_settings coros_mcp_autosync_enabled；缺鍵＝開，0＝停）。repo 為 nil（單元測試）時視為開。
func (h *CorosMcpHandler) autoSyncEnabled(ctx context.Context) bool {
	if h.repo == nil || h.repo.db == nil {
		return true
	}
	return appsettings.GetInt(ctx, h.repo.db, corosMcpAutoSyncKey, 1) != 0
}

// entryEmergency 入口是否處於緊急關閉（hidden）。repo 為 nil 時視為否。
func (h *CorosMcpHandler) entryEmergency(ctx context.Context) bool {
	if h.repo == nil || h.repo.db == nil {
		return false
	}
	return entrygate.Emergency(ctx, h.repo.db, corosMcpEntryStateKey)
}

// autoSyncOnce 自動同步的同步版本（測試與 goroutine 共用），依序：
//  1. 總開關關閉（kill switch）或入口緊急關閉 → 不動作；
//  2. 全域並發上限（本進程同時最多 corosMcpAutoConcurrency 個）：滿了略過，**不扣使用者名額**——使用者下次開 DOR 還有機會；
//  3. 搶這位使用者的 25 分鐘名額（Redis SET NX EX 1500，跨副本；搶不到＝完全不動作）；
//  4. 隨機延遲 0–30 秒（打散「後台廣播 dashboard 失效」造成的同時湧入）；
//  5. runSync：in-flight 鎖 → 冷卻檢查 → 第二道節流（last_synced_at 距今 <25 分鐘不打 COROS）→ 同步。
//
// 回傳 ran＝是否真的打了 COROS。未連接、太快、同步中、冷卻中都回 (false, …, nil)。
func (h *CorosMcpHandler) autoSyncOnce(ctx context.Context, userID string) (bool, CorosMcpSyncResult, error) {
	if !h.autoSyncEnabled(ctx) || h.entryEmergency(ctx) {
		return false, CorosMcpSyncResult{}, nil
	}
	sem := h.sem()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default:
		return false, CorosMcpSyncResult{}, nil // 全域並發已滿：略過，不扣名額
	}
	if !h.claimAutoSync(ctx, userID) {
		return false, CorosMcpSyncResult{}, nil
	}
	if h.autoJitterMax > 0 {
		jitter := time.Duration(mrand.Int63n(int64(h.autoJitterMax)))
		select {
		case <-time.After(jitter):
		case <-ctx.Done():
			return false, CorosMcpSyncResult{}, ctx.Err()
		}
	}
	sctx, cancel := context.WithTimeout(ctx, corosMcpAutoSyncTO)
	defer cancel()
	// from 傳零值＝由 syncCorosMcp 依 last_synced_at 算起點（corosMcpAutoFrom）
	res, err := h.runSync(sctx, userID, time.Time{}, h.timeNow(), corosMcpSyncOpts{MinInterval: corosMcpAutoSyncWindow})
	switch {
	case errors.Is(err, errCorosMcpNotConnected), errors.Is(err, errCorosMcpTooSoon),
		errors.Is(err, errCorosMcpBusy), errors.Is(err, errCorosMcpCooldown):
		return false, res, nil
	case errors.Is(err, errCorosMcpReconnect):
		// 需要重新授權（已標記，或換 token 不可能成功）：沒有打 COROS 同步；錯誤照回，呼叫端以 Debug 記錄即可。
		return false, res, err
	}
	return true, res, err
}

// CorosMcpAutoSync 供 profile Dashboard 呼叫（只有「已連線且入口 shown」的使用者才會被呼叫）：開 goroutine 背景跑、
// panic recover、失敗只記 log；立即返回，絕不阻塞或改變 Dashboard 回應。
func (h *CorosMcpHandler) CorosMcpAutoSync(userID string) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Str("user", userID).Msg("coros mcp autosync panic recovered")
			}
		}()
		ran, res, err := h.autoSyncOnce(context.Background(), userID)
		if err != nil {
			// 需要重新授權是常態（約 30 天一次）——Debug 即可，免得開放全員後每 25 分鐘每人一行 Warn。
			if errors.Is(err, errCorosMcpReconnect) {
				log.Debug().Str("user", userID).Msg("coros mcp autosync skipped: reauthorization required")
				return
			}
			log.Warn().Err(err).Str("user", userID).Str("code", corosMcpErrorCode(err)).Msg("coros mcp autosync failed")
			return
		}
		if ran {
			log.Info().Str("user", userID).Int("imported", res.Imported).Int("duplicate", res.Duplicate).
				Int("exists", res.Exists).Msg(fmt.Sprintf("coros mcp autosync done (fetched %d)", res.Fetched))
		}
	}()
}

// reauthCatchUp 重新授權成功後立即補同步（GA 契約 §2.4）：起點 max(floor, last_synced_at − 1 天)、上限 30 天；
// 背景執行。遵守自動同步總開關（關閉時使用者仍可按「匯入數據」手動補）、in-flight 鎖與冷卻。
func (h *CorosMcpHandler) reauthCatchUp(userID string) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Str("user", userID).Msg("coros mcp reauth catch-up panic recovered")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), corosMcpAutoSyncTO)
		defer cancel()
		if !h.autoSyncEnabled(ctx) || h.entryEmergency(ctx) {
			return
		}
		res, err := h.runSync(ctx, userID, time.Time{}, h.timeNow(), corosMcpSyncOpts{CatchUp: true})
		switch {
		case err == nil:
			log.Info().Str("user", userID).Int("imported", res.Imported).Int("duplicate", res.Duplicate).
				Int("exists", res.Exists).Msg(fmt.Sprintf("coros mcp reauth catch-up done (fetched %d)", res.Fetched))
		case errors.Is(err, errCorosMcpBusy), errors.Is(err, errCorosMcpCooldown), errors.Is(err, errCorosMcpNotConnected):
			log.Debug().Err(err).Str("user", userID).Msg("coros mcp reauth catch-up skipped")
		default:
			log.Warn().Err(err).Str("user", userID).Str("code", corosMcpErrorCode(err)).Msg("coros mcp reauth catch-up failed")
		}
	}()
}
