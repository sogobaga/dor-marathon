package integration

// 活動對應與匯入：把 integration_events 的 activity 事件變成 DOR 活動。
//
// 規則（計畫 B.7）：
//   - 類型白名單（跑／走／健行；跑步機、室內、虛擬跑照算＝與 Terra 一致；輪椅、MULTI_SPORT 一律不收）；
//   - manual／isWebUpload＝true 拒收（非 Garmin 裝置產生，歸屬 Garmin 不實）；isParent＝true 的父活動略過
//     （其子活動帶 parentSummaryId，照一般活動處理）；
//   - recorded_at＝**開始時間**（全站外部來源慣例；GPS 列才是結束時間）；開始時間早於連接時間（floor＝created_at）略過；
//   - distance_km 是 DECIMAL(6,3)：≥1000 先擋（否則 INSERT 失敗、整批退避）；距離 <0.1 km 或時間 <60 秒略過；
//   - 配速自算（Garmin 摘要的 pace 欄位自相矛盾，不採用）；ElapsedS 留 nil（摘要沒有可信的 elapsed）；
//   - 不取活動名稱、座標、熱量（最小化；事件 payload 本來就沒有）。
//   ⚠️ 連線的「暫停」旗標（paused）絕不作為丟棄活動的條件（Garmin 端本來就不會再傳；見審查 R-C2）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

const garminSPMaxAge = 3 * time.Hour // OD-5：只有「結束時間距今 ≤3 小時」的匯入才扣體力（延遲抵達的舊活動不扣）

// garminExternalID：gc:<id>；超過 64 字元改存雜湊（gc:h:<sha256 前 40 hex>）。
func garminExternalID(id string) string {
	ext := garminExtIDPrefix + id
	if len(ext) <= garminExtIDMax {
		return ext
	}
	sum := sha256.Sum256([]byte(id))
	return garminExtIDPrefix + "h:" + hex.EncodeToString(sum[:])[:40]
}

// mapGarminActivity 純函式：事件 payload → NormalizedActivity。回傳 (nil, 略過原因)＝不匯入。
// floor＝連接時間（匯入起算點，零值＝不限）。
func mapGarminActivity(userID string, floor time.Time, e garminStoredActivity) (*NormalizedActivity, string) {
	if e.Manual || e.IsWebUpload {
		return nil, garminSkipManual
	}
	if e.IsParent {
		return nil, garminSkipParent
	}
	kind := garminActivityKind(e.ActivityType)
	if kind == "" {
		return nil, garminSkipNonRunning
	}
	id := e.SummaryID
	if id == "" {
		id = e.ActivityID
	}
	if id == "" || e.StartTime <= 0 {
		return nil, garminSkipBadPayload
	}
	recordedAt := time.Unix(e.StartTime, 0).UTC()
	if !floor.IsZero() && recordedAt.Before(floor) {
		return nil, garminSkipBefore
	}
	if e.Duration < 60 || e.Duration > 1e7 || math.IsNaN(e.Duration) {
		return nil, garminSkipInvalid
	}
	distKm := e.Distance / 1000.0
	if !(distKm >= 0.1 && distKm < 1000) {
		return nil, garminSkipInvalid
	}
	durS := int(math.Round(e.Duration))
	na := &NormalizedActivity{
		UserID:      userID,
		Source:      garminProvider,
		ExternalID:  garminExternalID(id),
		Fingerprint: fingerprintOf(e.StartTime, e.Distance, durS),
		DistanceKm:  distKm,
		DurationS:   durS,
		AvgPaceS:    int(math.Round(float64(durS) / distKm)),
		RecordedAt:  recordedAt,
		Kind:        KindRun,
	}
	if kind == "walk" {
		na.Kind = KindWalk
	}
	if e.ElevationGain != nil && *e.ElevationGain > 0 {
		v := *e.ElevationGain
		na.AscentM = &v
	}
	if garminStoreAvgHR && e.AvgHeartRate != nil && *e.AvgHeartRate >= 30 && *e.AvgHeartRate <= 250 {
		v := int(math.Round(*e.AvgHeartRate))
		na.AvgHR = &v
	}
	if name := truncateRunes(strings.TrimSpace(e.DeviceName), garminDeviceMax); name != "" && !strings.EqualFold(name, "unknown") {
		na.DeviceName = &name
	}
	return na, ""
}

// processActivityEvent 處理一筆 activity 事件（呼叫端已持有該使用者的處理鎖）。
// 回傳 result（寫進 integration_events.result）；error＝暫時性失敗（走退避重試）。
func (h *GarminHandler) processActivityEvent(ctx context.Context, ev garminEvent) (string, error) {
	var e garminStoredActivity
	if err := json.Unmarshal(ev.Payload, &e); err != nil {
		return "skipped_" + garminSkipBadPayload, nil
	}
	conn, err := h.store.GetGarminDirectByUserID(ctx, ev.ProviderUserID, false)
	if err != nil {
		return "", fmt.Errorf("lookup connection: %w", err)
	}
	if conn == nil {
		return garminResUnknown, nil // 連線已被中斷／清理：丟棄
	}
	// 每筆收到的活動事件都更新（代表資料管道活著；包含之後被略過的類型）。
	if err := h.store.TouchGarminSynced(ctx, conn.ID); err != nil {
		log.Debug().Err(err).Msg("garmin: touch last_synced_at failed")
	}
	h.goKeepAlive(ctx, conn)

	na, skip := mapGarminActivity(conn.UserID, conn.ConnectedAt, e)
	if na == nil {
		return "skipped_" + skip, nil
	}
	// 舊孿生探測：同使用者已有舊 Terra-garmin 列（裸 summaryId 或 garmin:<開始秒>）→ 視為已存在，不發獎勵。
	bare := e.SummaryID
	if bare == "" {
		bare = e.ActivityID
	}
	twin, err := h.store.LegacyTwinExists(ctx, conn.UserID, bare, e.StartTime)
	if err != nil {
		return "", fmt.Errorf("legacy twin probe: %w", err)
	}
	if twin {
		return garminResExists, nil
	}
	res, err := h.store.ImportActivity(ctx, na)
	if err != nil {
		return "", fmt.Errorf("import activity: %w", err)
	}
	switch res.Status {
	case "skipped":
		return "skipped_" + garminSkipImplausible, nil
	case "exists":
		return garminResExists, nil
	case "inserted", "duplicate":
		h.store.AfterImport(ctx, na, res, TailOptions{SkipGPSCalib: true, MaxSPAge: garminSPMaxAge})
		if res.Status == "inserted" {
			return garminResInserted, nil
		}
		return garminResDuplicate, nil
	}
	return "skipped_" + garminSkipInvalid, nil
}
