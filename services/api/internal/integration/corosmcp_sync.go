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
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/gpscalib"
	"github.com/dor/api/internal/stamina"
)

const (
	corosMcpSourceCoros = "coros"
	corosMcpExtIDPrefix = "mcp:"
	// corosMcpImportWindow：手動匯入每人節流（比照 terra allowImport，記憶體）。
	corosMcpImportWindow = time.Minute
	// corosMcpAutoSyncWindow：自動同步每人最短間隔（Redis SET NX EX 1500 ＝ 25 分鐘）。
	corosMcpAutoSyncWindow = 1500 * time.Second
	corosMcpAutoSyncDays   = 3                // 自動同步回看天數
	corosMcpAutoSyncTO     = 60 * time.Second // 自動同步整體逾時
	corosMcpMaxImportDays  = 30               // worker 去重只看 45 天，回補上限 30 天（比照 Terra）
	corosMcpChunkDays      = 10               // querySportRecords 單次最多查 10 天
	corosMcpRecordsLimit   = 50               // 單次回傳上限；回傳筆數＝上限時記 warn（可能被截斷）
	corosMcpDeviceMaxRunes = 60               // activities.device_name VARCHAR(60)
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

// importMcpActivity 落地單筆：ImportActivity → 與 terra.go importTerra「完全相同條件」的三段尾巴。
func (h *CorosMcpHandler) importMcpActivity(ctx context.Context, na *NormalizedActivity) (ImportResult, error) {
	res, err := h.repo.ImportActivity(ctx, na)
	if err != nil {
		return res, err
	}
	// GPS 距離校正 T1 觸發點：非同步重算（debounce），不阻塞這次匯入。
	if res.Status == "inserted" || res.Status == "duplicate" {
		gpscalib.RecomputeAsync(h.repo.db, na.UserID)
	}
	// stamina.ChargeSP 維持「僅新匯入」才扣血：SP 是扣血動作，同一趟不能被扣兩次。
	if res.Status == "inserted" && na.DistanceKm > 0 {
		stamina.ChargeSP(ctx, h.repo.db, na.UserID, na.DistanceKm, na.AvgPaceS)
	}
	// AwardMileageExp 的呼叫條件比照 strava.go/coros.go/terra.go：新匯入，或同帳號跨裝置的良性重複
	// （multi_device_duplicate，會走差額補償流程）；其他 duplicate 原因交給函式內部的 flagged 政策擋。
	if (res.Status == "inserted" || (res.Status == "duplicate" && res.Reason == "multi_device_duplicate")) && na.DistanceKm > 0 {
		if err := h.repo.AwardMileageExp(ctx, res.ID, na.UserID); err != nil {
			log.Error().Err(err).Str("activity", res.ID).Msg("coros mcp award mileage exp failed")
		}
	}
	return res, nil
}

// syncCorosMcp 同步核心（手動 /import 與自動同步共用）：取連線（無→errCorosMcpNotConnected）→ 必要時換 token
// （errCorosMcpReconnect 照舊往上傳）→ queryDevices → 分段 querySportRecords → 逐筆（由舊到新，讓重疊偵測與
// EXP 差額補償順序固定）對應＋匯入＋三段尾巴 → 無錯誤時更新 last_synced_at。from 會被夾到連線 floor 之後。
func (h *CorosMcpHandler) syncCorosMcp(ctx context.Context, userID string, from, to time.Time) (CorosMcpSyncResult, error) {
	var out CorosMcpSyncResult
	conn, err := h.getConnection(ctx, userID)
	if err != nil {
		return out, err
	}
	if conn == nil {
		return out, errCorosMcpNotConnected
	}
	if from.IsZero() {
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

// --- 手動匯入 ---

// allowImport 每人 60 秒節流（記憶體，比照 terra allowImport）；呼叫即登記時間戳，避免慢請求期間連點。
// 回傳 (放行, 還需等待秒數)。
func (h *CorosMcpHandler) allowImport(userID string) (bool, int) {
	h.importMu.Lock()
	defer h.importMu.Unlock()
	if h.importLast == nil {
		h.importLast = map[string]time.Time{}
	}
	if last, ok := h.importLast[userID]; ok && time.Since(last) < corosMcpImportWindow {
		return false, int((corosMcpImportWindow - time.Since(last)).Seconds()) + 1
	}
	h.importLast[userID] = time.Now()
	return true, 0
}

// corosMcpImportResponse POST /import 回應：同步結果＋實際使用的 days。
type corosMcpImportResponse struct {
	Days int `json:"days"`
	CorosMcpSyncResult
}

// POST /import?days=N（1–30，預設 30）
// 409 {"error":"not_connected"|"reconnect_required"}、429 {"error":"rate_limited","retry_after_s":N}、
// 502 {"error":"…"}（COROS 失敗，含 anomaly）、200 corosMcpImportResponse。
func (h *CorosMcpHandler) Import(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := h.requireWhitelist(w, r)
	if !ok {
		return
	}
	conn, err := h.getConnection(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	if conn == nil {
		respondJSON(w, http.StatusConflict, map[string]string{"error": "not_connected"})
		return
	}
	if allow, retryAfter := h.allowImport(userID); !allow {
		respondJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "retry_after_s": retryAfter})
		return
	}
	days := corosMcpMaxImportDays
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			days = n
		}
	}
	if days < 1 {
		days = 1
	} else if days > corosMcpMaxImportDays {
		days = corosMcpMaxImportDays
	}
	now := time.Now()
	res, err := h.syncCorosMcp(r.Context(), userID, now.AddDate(0, 0, -days), now)
	if err != nil {
		switch {
		case errors.Is(err, errCorosMcpReconnect):
			respondJSON(w, http.StatusConflict, map[string]string{"error": "reconnect_required"})
		case errors.Is(err, errCorosMcpNotConnected):
			respondJSON(w, http.StatusConflict, map[string]string{"error": "not_connected"})
		default:
			log.Error().Err(err).Str("user", userID).Msg("coros mcp manual import failed")
			respondErr(w, http.StatusBadGateway, "向 COROS 取得活動失敗")
		}
		return
	}
	respondJSON(w, http.StatusOK, corosMcpImportResponse{Days: days, CorosMcpSyncResult: res})
}

// --- 自動同步（Dashboard 觸發）---

// claimAutoSync 搶「這個使用者這 25 分鐘的同步名額」：Redis SET NX EX 1500（鍵 coros_mcp:autosync:<uid>）；
// Redis 為 nil 或出錯時退回記憶體 map（單機部署／本機測試仍能節流，寧可嚴格也不 fail-open 放大 COROS 呼叫）。
func (h *CorosMcpHandler) claimAutoSync(ctx context.Context, userID string) bool {
	if h.rdb != nil {
		ok, err := h.rdb.SetNX(ctx, "coros_mcp:autosync:"+userID, "1", corosMcpAutoSyncWindow).Result()
		if err == nil {
			return ok
		}
		log.Warn().Err(err).Msg("coros mcp autosync: redis SETNX failed, falling back to memory")
	}
	h.autoMu.Lock()
	defer h.autoMu.Unlock()
	if h.autoLast == nil {
		h.autoLast = map[string]time.Time{}
	}
	if last, ok := h.autoLast[userID]; ok && time.Since(last) < corosMcpAutoSyncWindow {
		return false
	}
	h.autoLast[userID] = time.Now()
	return true
}

// autoSyncOnce 自動同步的同步版本（測試與 goroutine 共用）：搶到名額＋有連線才跑 syncCorosMcp(now−3 天, now)。
// 回傳 ran＝是否真的打了 COROS 同步。未連接直接結束（只查一次連線列，名額仍被占用，25 分鐘內不重查）。
func (h *CorosMcpHandler) autoSyncOnce(ctx context.Context, userID string) (bool, CorosMcpSyncResult, error) {
	if !h.claimAutoSync(ctx, userID) {
		return false, CorosMcpSyncResult{}, nil
	}
	// from 傳零值＝由 syncCorosMcp 依 last_synced_at 算起點（corosMcpAutoFrom）
	res, err := h.syncCorosMcp(ctx, userID, time.Time{}, time.Now())
	if errors.Is(err, errCorosMcpNotConnected) {
		return false, res, nil
	}
	return true, res, err
}

// CorosMcpAutoSync 供 profile Dashboard 呼叫（僅白名單 coros_mcp_entry=='shown' 時）：開 goroutine 背景跑，
// 60 秒逾時、panic recover、失敗只記 log；立即返回，絕不阻塞或改變 Dashboard 回應。
func (h *CorosMcpHandler) CorosMcpAutoSync(userID string) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error().Interface("panic", rec).Str("user", userID).Msg("coros mcp autosync panic recovered")
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), corosMcpAutoSyncTO)
		defer cancel()
		ran, res, err := h.autoSyncOnce(ctx, userID)
		if err != nil {
			log.Warn().Err(err).Str("user", userID).Msg("coros mcp autosync failed")
			return
		}
		if ran {
			log.Info().Str("user", userID).Int("imported", res.Imported).Int("duplicate", res.Duplicate).
				Int("exists", res.Exists).Msg(fmt.Sprintf("coros mcp autosync done (fetched %d)", res.Fetched))
		}
	}()
}
