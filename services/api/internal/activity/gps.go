package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/gpscalib"
	"github.com/dor/api/internal/notify"
	"github.com/dor/api/internal/stamina"
)

// ErrGPSEnqueueFailed：這趟 GPS 距離達標、已寫入 gps_runs，但推進 Redis Stream（worker 消費用，
// 見 SaveGPSRun）失敗——已對應補償刪除該筆 gps_runs（不留孤兒列），前端應視為「上傳失敗、
// 可重試」而非資料有問題（見 UploadGPS 的 503 對應）。
var ErrGPSEnqueueFailed = errors.New("推入活動佇列失敗，請稍後再試")

// 防弊參數
const (
	gpsMaxAccuracyM   = 65.0  // 精度差於此（公尺）的點不列入距離計算（城市訊號較差，放寬）
	gpsMinSegMeters   = 5.0   // 太短的位移不做超速判定（避免 GPS 飄移誤判）
	gpsMinDistKm      = 0.005 // 短於此（公里=5m）視為移動距離不足，不計算/不記錄/不判異常
	gpsFastRatioFlag  = 0.30  // 超速距離占比達此且總距離足夠 → 判定疑似載具
	gpsRatioMinDistKm = 0.3   // 套用「超速占比」判定所需的最低總距離（避免短程單一跳點誤判）
	// gpsGapMaxS/gpsGapMaxMeters：訊號斷點排除規則（2026-09-03 事故：步行後搭捷運，地下無 GPS，
	// 長時間斷點讓兩點間直線距離除以經過時間仍低於極限速度、未被超速規則攔下）。與速度規則是
	// OR 的關係——dt 超過門檻「且」d 也超過門檻才視為無效（避免把單純訊號差、但實際仍在移動的
	// 短距離斷點也排除掉）。見 computeRun 內 gapInvalid 判定與檔頭 migrations/166 說明。
	gpsGapMaxS      = 60.0  // 秒；此門檻本身不觸發排除，需與下方距離門檻同時成立
	gpsGapMaxMeters = 250.0 // 公尺
)

type gpsPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	T   int64   `json:"t"`   // epoch 毫秒
	Acc float64 `json:"acc"` // 精度（公尺）
}

type gpsRunReq struct {
	RaceID        string     `json:"race_id"`
	StartedAt     string     `json:"started_at"`
	EndedAt       string     `json:"ended_at"`
	Points        []gpsPoint `json:"points"`
	ClientVersion string     `json:"client_version"` // App/前端版號（量測用途，見 gpscalib acc_p50/p90 同批欄位）
	// PetIDs 寵物雲端馬拉松歸戶（migration 174，D3(a)／D5）：這趟跑步「一起跑」的寵物
	// （registration_pets.id），前端在結束跑步、上傳前的「這趟狗狗有一起跑嗎？」卡片勾選後帶入。
	// 選填；必須全部屬於呼叫者名下的寵物報名紀錄（見 SaveGPSRun 對 Repository.ValidateOwnedPets 的呼叫），
	// 否則整筆上傳回 400，不做「部分接受、忽略無效 ID」——避免冒用他人寵物歸戶出不實的寵物里程。
	PetIDs []string `json:"pet_ids,omitempty"`
}

type gpsRunResult struct {
	DistanceKm    float64 `json:"distance_km"`
	DurationS     int     `json:"duration_s"`
	AvgPaceS      int     `json:"avg_pace_s"`
	Flagged       bool    `json:"flagged"`
	FlagReason    string  `json:"flag_reason,omitempty"`
	AnomalySegs   int     `json:"anomaly_segments"`
	ExpAwarded    bool    `json:"exp_awarded"`         // 未標記才進活動管線發里程 EXP
	TooShort      bool    `json:"too_short"`           // 移動距離不足，無法計算（非異常）
	Duplicate     bool    `json:"duplicate,omitempty"` // 這一趟已上傳過（同 user+起跑時間）→ 冪等 no-op，未重複記錄/發獎
	KmPaces       []int   `json:"km_paces,omitempty"`  // 每公里分段配速（秒/km）；前端結束畫面「分段」與「結果卡均配速」同源
	RawDistanceKm float64 `json:"raw_distance_km"`     // 校正前原始距離（見 internal/gpscalib）；未套校正時與 DistanceKm 相同
	CalibFactor   float64 `json:"calib_factor"`        // 上傳當下生效的係數；未套校正恆為 1.0
	// ExcludedKm/ExcludedSegments：被排除區段（超速∪訊號斷點，見 gpsGapMaxS/gpsGapMaxMeters）的原始
	// 直線距離加總／段數；不套校正係數 k（排除的是「無效」距離，校正只對「有效」距離有意義）。前端
	// 據此顯示「⚠️ 已排除 Xkm」提示——這趟仍照常存檔，只是排除掉的部分不計入距離/EXP。
	ExcludedKm       float64 `json:"excluded_km"`
	ExcludedSegments int     `json:"excluded_segments"`
}

func haversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// runCalc 是 computeRun 的計算結果——SaveGPSRun 唯一的純計算核心，見該函式與 computeRun 的註解。
type runCalc struct {
	RawKm          float64 // 原始有效距離（未套校正；防弊判定與 gps_runs.distance_km 用這個）
	DistanceKm     float64 // 校正後距離（計入賽事/發獎/顯示用這個）
	DurationS      int
	RawAvgPaceS    int // 原始平均配速：防弊判定 + gps_runs.avg_pace_s 用這個
	AvgPaceS       int // 校正後平均配速：ActivityEvent/SP 扣血/回應用這個
	Flagged        bool
	FlagReason     string
	Anomalies      int
	KmSplits       []int // 每公里分段配速（秒/km），依校正後距離的公里邊界切
	UsedPointCount int
	AccP50, AccP90 *float64
	ExcludedM      float64      // 被排除區段的原始直線距離加總（公尺，未套 k；見 gpsGapMaxS/gpsGapMaxMeters）
	ExcludedSegs   int          // 被排除的區段數（超速規則∪斷點規則，同一區段只算一次）
	BreakBefore    map[int]bool // 斷點：key 為「已接受點序列」中的序號（0-based），true 代表該點前另起一段 polyline（見 encodePolylineSegments）
}

// computeRun 是 SaveGPSRun 的純計算核心（無 DB/Redis 依賴，方便單元測試，見 gps_test.go）：從一批
// 軌跡點與校正係數 k 算出距離/配速/防弊判定/每公里分段。**不變量**：Flagged/FlagReason/Anomalies/
// RawKm/RawAvgPaceS 完全不受 k 影響（防弊判定一律用原始值）——校正只改變 DistanceKm/AvgPaceS/
// KmSplits 這三項「計入賽事/發獎/顯示」用的輸出，任何 k 值都不能讓一趟原本會被標記的軌跡變成不
// 標記，也不能讓原本正常的軌跡被標記。
func computeRun(points []gpsPoint, k float64) (runCalc, error) {
	maxSpeed := 1000.0 / float64(minPaceSecPerKm) // 公尺/秒（= 2:00/km 對應速度）
	var distM, fastDistM, excludedM float64
	var anomalies, usedPointCount, excludedSegs int
	var accs []float64
	var prev *gpsPoint
	// 每公里分段配速（秒/km）：校正後距離每跨一整公里記一段（與前端 fireCheer 判定同一個公里
	// 邊界），供「平均配速區間」任務改用「任一公里落在區間即算」判定（比整段均配速好達成）。
	// 伺服器端由軌跡重算 → 可信、不易偽造。
	var kmSplits []int
	kmTarget := 1000.0
	lastKmT := points[0].T
	breakBefore := map[int]bool{}
	for i := range points {
		p := &points[i]
		if p.Acc > 0 && p.Acc > gpsMaxAccuracyM {
			continue // 精度太差，略過
		}
		ordinal := usedPointCount // 這個點在「已接受序列」中的序號（0-based）——breakBefore 用這個索引
		usedPointCount++
		if p.Acc > 0 {
			accs = append(accs, p.Acc)
		}
		if prev != nil {
			d := haversineM(prev.Lat, prev.Lng, p.Lat, p.Lng)
			dt := float64(p.T-prev.T) / 1000.0
			if dt > 0 {
				speedInvalid := d > gpsMinSegMeters && d/dt > maxSpeed
				// 訊號斷點：長時間沒有訊號（dt 夠大）且遠端點離得夠遠（d 夠大）才視為無效——單純
				// dt 大但 d 小（原地停留/訊號差但沒怎麼移動）不算，避免誤傷。
				gapInvalid := dt > gpsGapMaxS && d > gpsGapMaxMeters
				if speedInvalid {
					anomalies++                // 超過人類極限速度的區段（疑似載具/GPS 跳點）
					fastDistM += maxSpeed * dt // 以極限速度估計超速距離（供占比判定）——但不列入有效里程
				}
				if speedInvalid || gapInvalid {
					// 無效區段（超速∪訊號斷點）：距離不計入 distM、km 分段不推進；但遠端點 p 仍會
					// 在迴圈末尾成為新的 prev（下一段從這個真實新位置起算），且標記為 polyline 斷點
					// （見 encodePolylineSegments）——斷點規則本身不算作弊，不進 fastDistM/anomalies。
					excludedM += d
					excludedSegs++
					breakBefore[ordinal] = true
				} else {
					distM += d                // 只有「正常速度」才算有效距離；超速/斷點段完全不計（不刷里程、不推進課表）
					for distM*k >= kmTarget { // 跨過整公里（校正後）→ 記這一段配速
						if splitS := int(float64(p.T-lastKmT) / 1000.0); splitS > 0 {
							kmSplits = append(kmSplits, splitS)
						}
						lastKmT = p.T
						kmTarget += 1000
					}
				}
			}
		}
		prev = p
	}

	rawKm := distM / 1000.0
	distanceKm := distM * k / 1000.0
	durationS := int((points[len(points)-1].T - points[0].T) / 1000)
	if durationS <= 0 {
		return runCalc{}, fmt.Errorf("時間區間無效")
	}
	rawAvgPaceS := 0
	if rawKm > 0 {
		rawAvgPaceS = int(float64(durationS) / rawKm)
	}
	avgPaceS := 0
	if distanceKm > 0 {
		avgPaceS = int(float64(durationS) / distanceKm)
	}

	// 防弊判定（先算，一律用原始值）：只抓「過快」（疑似騎車/搭車等載具），不抓過慢（走路、慢跑皆正常）。
	// 占比以「原始移動 rawM＝有效+超速」為分母，避免有效距離被排除後占比失真。
	rawM := distM + fastDistM
	fastRatio := 0.0
	if rawM > 0 {
		fastRatio = fastDistM / rawM
	}
	var reasons []string
	if rawAvgPaceS > 0 && rawAvgPaceS < minPaceSecPerKm {
		reasons = append(reasons, "平均配速快於 2:00/km（疑似使用交通工具）")
	}
	// 超速占比：需有足夠原始移動才判定，避免短程單一 GPS 跳點被誤判為異常
	if rawM/1000.0 >= gpsRatioMinDistKm && fastRatio >= gpsFastRatioFlag {
		reasons = append(reasons, fmt.Sprintf("逾三成距離超過人體極限速度（%d 段，疑似載具）", anomalies))
	}
	flagged := len(reasons) > 0
	flagReason := strings.Join(reasons, "；")

	accP50, accP90 := accPercentiles(accs)

	return runCalc{
		RawKm: rawKm, DistanceKm: distanceKm, DurationS: durationS,
		RawAvgPaceS: rawAvgPaceS, AvgPaceS: avgPaceS,
		Flagged: flagged, FlagReason: flagReason, Anomalies: anomalies,
		KmSplits: kmSplits, UsedPointCount: usedPointCount, AccP50: accP50, AccP90: accP90,
		ExcludedM: excludedM, ExcludedSegs: excludedSegs, BreakBefore: breakBefore,
	}, nil
}

// polylineSegmentSep 多段 polyline 的分隔符。⚠️ 必須是 encoded polyline 字元集（ASCII 63~126，'?'～'~'）
// 以外的字元：v772 曾用 '|'（ASCII 124，正好在字元集內），舊軌跡裡合法出現的 '|' 被前端當分隔符錯切，
// 解碼成 (0,0) 飛到非洲外海（2026-09-04 使用者回報「定位壞了」；106 筆中 59 筆含 '|'）。';'（ASCII 59）不在字元集內。
const polylineSegmentSep = ";"

// encodePolylineSegments 是 SaveGPSRun 軌跡壓縮的純函式核心（無 DB 依賴，方便單元測試，見
// gps_test.go）：依 breakBefore 標記的斷點把一條軌跡切成多段，各段分別做 Douglas-Peucker 簡化
// (5m) + encode，用 polylineSegmentSep（';'）串接——無效區段（超速/訊號斷點，見 computeRun 的 gapInvalid/speedInvalid）
// 不會被畫成一條直線。breakBefore[i]==true 代表 points[i]（無效區段的遠端點）前另起一段，即 i 是
// 新一段的第一個點。沒有任何斷點時退化成單一 polyline，與舊資料格式（無分隔符）相容。少於 2 個點的
// 子段仍會被 encode（單點也是合法、可還原的 encoded polyline，只是不會畫出線段）——刻意不捨棄，
// 讓「排除了什麼」在 polyline 的段落切分上如實呈現。
func encodePolylineSegments(points []gpsPoint, breakBefore map[int]bool) string {
	var segments []string
	var cur [][2]float64
	flush := func() {
		if len(cur) == 0 {
			return
		}
		segments = append(segments, encodePolyline(simplifyPath(cur, 5)))
		cur = nil
	}
	for i, p := range points {
		if breakBefore[i] {
			flush()
		}
		cur = append(cur, [2]float64{p.Lat, p.Lng})
	}
	flush()
	return strings.Join(segments, polylineSegmentSep)
}

// enqueueActivityEvent 把一筆活動事件序列化後推進 Redis Stream（worker 端非同步消費落地 DB，
// 見 services/worker/main.go）。抽成獨立函式（吃 activityStreamXAdder 介面而非具體
// *redis.Client）方便單元測試注入假的失敗客戶端，驗證 SaveGPSRun 的補償刪除路徑（見 gps_test.go）
// 不必牽動真正的 Redis 連線。
func enqueueActivityEvent(ctx context.Context, rdb activityStreamXAdder, evt ActivityEvent) error {
	b, _ := json.Marshal(evt)
	return rdb.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, Values: map[string]any{"data": string(b)}}).Err()
}

// SaveGPSRun 伺服器端重算 + 防弊；未標記者推入活動管線（記錄+里程EXP）。
//
// GPS 距離校正（見 internal/gpscalib）：k 是這位使用者「上傳當下生效」的校正係數（入口非白名單/
// 使用者關閉/尚未 active 一律回 1.0，即完全等同校正前行為）。實際計算全部委派給純函式
// computeRun——防弊判定與校正互不影響的不變量在那裡說明並在 gps_test.go 驗證。gps_runs 表恆存
// 原始值（distance_km/avg_pace_s），校正後的值另存 calib_distance_km/calib_factor——原始檔案表
// 任何時候都能重建真相。
func (s *Service) SaveGPSRun(ctx context.Context, userID string, req gpsRunReq) (*gpsRunResult, error) {
	if len(req.Points) < 2 {
		return nil, fmt.Errorf("軌跡點不足")
	}

	// 寵物歸戶（migration 174，D3(a)）：去重後驗證全部屬於呼叫者名下——放在最前面盡早失敗，
	// 避免白算一輪 computeRun 卻因寵物名單有誤整筆打回。
	req.PetIDs = dedupeStrings(req.PetIDs)
	if err := s.repo.ValidateOwnedPets(ctx, userID, req.PetIDs); err != nil {
		return nil, err
	}

	k, _ := gpscalib.EffectiveFactor(ctx, s.repo.db, userID)
	calc, err := computeRun(req.Points, k)
	if err != nil {
		return nil, err
	}
	rawKm, distanceKm, durationS := calc.RawKm, calc.DistanceKm, calc.DurationS
	rawAvgPaceS, avgPaceS := calc.RawAvgPaceS, calc.AvgPaceS
	flagged, flagReason, anomalies, kmSplits := calc.Flagged, calc.FlagReason, calc.Anomalies, calc.KmSplits
	excludedKm := round3(calc.ExcludedM / 1000.0) // 不套校正係數 k——排除的是「無效」距離，見 gpsRunResult 欄位註解

	// 有效距離不足「且」未判定為載具 → 單純距離不足（走幾步），不記錄、不發 EXP（用原始值判定）。
	// 若是載具（整趟超速被排除、有效距離趨近 0）則不走這裡，仍以 flagged 記錄一筆（歷史看得到、且不發獎）。
	if rawKm < gpsMinDistKm && !flagged {
		return &gpsRunResult{
			DistanceKm: round2(rawKm), DurationS: durationS, AvgPaceS: rawAvgPaceS, TooShort: true,
			RawDistanceKm: round2(rawKm), CalibFactor: k,
			ExcludedKm: excludedKm, ExcludedSegments: calc.ExcludedSegs,
		}, nil
	}

	started, _ := time.Parse(time.RFC3339, req.StartedAt)
	ended, _ := time.Parse(time.RFC3339, req.EndedAt)
	// 軌跡壓縮：精度過差的點剔除 → 依斷點切段 → 各段分別 Douglas-Peucker 簡化(5m) + encode，
	// 用 ';' 串接（見 encodePolylineSegments／polylineSegmentSep）——無效區段（超速/訊號斷點）不畫線連過去；
	// 舊資料（無分隔符）維持單一 polyline 格式相容。
	usedPts := make([]gpsPoint, 0, len(req.Points))
	for _, p := range req.Points {
		if p.Acc == 0 || p.Acc <= gpsMaxAccuracyM {
			usedPts = append(usedPts, p)
		}
	}
	polyline := encodePolylineSegments(usedPts, calc.BreakBefore)
	id, inserted, err := s.repo.InsertGPSRun(ctx, userID, req.RaceID, started, ended,
		round2(rawKm), durationS, rawAvgPaceS, flagged, flagReason, len(req.Points), polyline, kmSplits,
		k, round2(distanceKm), req.ClientVersion, calc.AccP50, calc.AccP90, calc.UsedPointCount,
		excludedKm, calc.ExcludedSegs, req.PetIDs)
	if err != nil {
		return nil, err
	}
	if !inserted {
		// 這一趟已上傳過（同 user + 起跑時間）——冪等：不重複記錄、不重複發里程 EXP/扣 SP，直接回結果。
		// 防「手機跳掉切回 → 出現『有一趟未上傳的跑步』提示 → 再上傳同一份快取軌跡」造成兩筆重複活動。
		return &gpsRunResult{
			DistanceKm: round2(distanceKm), DurationS: durationS, AvgPaceS: avgPaceS,
			Flagged: flagged, FlagReason: flagReason, AnomalySegs: anomalies, ExpAwarded: false, Duplicate: true,
			RawDistanceKm: round2(rawKm), CalibFactor: k,
			ExcludedKm: excludedKm, ExcludedSegments: calc.ExcludedSegs,
		}, nil
	}

	// 未標記 → 進既有活動管線（記錄活動 + 日常里程 EXP，皆用校正後的值）。
	// H4（2026-09-07 audit）：XAdd 失敗過去被吃掉（略過錯誤直接回成功）——這一筆會靜靜卡在
	// gps_runs 裡「已入庫但沒有對應活動/EXP」，且因 uq_gps_runs_user_start 冪等索引，使用者
	// 重傳同一趟只會被當成重複擋下、永遠補不回來。現在改成：XAdd 失敗 → 記錄告警 + 補償刪除
	// 這筆 gps_runs（讓它可以重傳）+ 回錯誤（503，前端可重試，資料仍在手機上）；ChargeSP 這類
	// 有副作用的操作一律延後到 XAdd 確認成功之後才做，避免「入隊失敗但體力已經扣了」。
	if !flagged && distanceKm > 0 {
		evt := ActivityEvent{
			UserID:        userID,
			RaceID:        req.RaceID,
			DistanceKm:    round2(distanceKm),
			DurationS:     durationS,
			AvgPaceS:      avgPaceS,
			RecordedAt:    ended.Format(time.RFC3339),
			KmPaces:       kmSplits,
			RawDistanceKm: round2(rawKm),
			CalibFactor:   k,
			PetIDs:        req.PetIDs,
		}
		if err := enqueueActivityEvent(ctx, s.rdb, evt); err != nil {
			log.Error().Err(err).Str("user_id", userID).Str("gps_run_id", id).
				Msg("SaveGPSRun: XAdd enqueue failed, rolling back gps_runs")
			notify.Alert("gps_enqueue_failed", "GPS 上傳寫入活動佇列失敗（已回滾）",
				fmt.Sprintf("user_id=%s gps_run_id=%s distance_km=%.2f err=%v", userID, id, round2(distanceKm), err))
			if delErr := s.repo.DeleteGPSRun(context.Background(), id); delErr != nil {
				// 補償刪除本身也失敗——這下真的留下孤兒列了，另外告警讓人工介入，不要吞掉。
				log.Error().Err(delErr).Str("gps_run_id", id).
					Msg("SaveGPSRun: compensating DeleteGPSRun also failed, orphan row left behind")
				notify.Alert("gps_enqueue_failed_delete_failed", "GPS 補償刪除也失敗，留下孤兒列，需人工處理",
					fmt.Sprintf("gps_run_id=%s user_id=%s delete_err=%v", id, userID, delErr))
			}
			return nil, ErrGPSEnqueueFailed
		}
		// finding 2（2026-09-08 第二次稽核）：標記已成功入隊（migrations/172 gps_runs.enqueued_at）。
		// 若這裡的 UPDATE 本身失敗（極罕見：DB 抖動），不影響這次上傳結果——至多讓
		// RequeueUnenqueued 的孤兒列掃描白跑一次（該掃描查無對應活動才會補送，見 ListUnenqueuedGPS
		// 註解），不會造成重複入帳；因此只記錄，不因此讓這次上傳失敗。
		if err := s.repo.markGPSEnqueued(ctx, id); err != nil {
			log.Error().Err(err).Str("gps_run_id", id).
				Msg("SaveGPSRun: markGPSEnqueued failed (non-fatal, enqueue itself succeeded)")
		}
		// 體力值 SP：跑步完成後扣血（依距離×強度；扣到 0 凍結 6 小時；僅未標記、且 XAdd 確認成功才扣）
		stamina.ChargeSP(ctx, s.repo.db, userID, round2(distanceKm), avgPaceS)
	}

	// 這一趟成功入庫（無論 flagged 與否）：候選配對可能新增了一筆，非同步重算校正係數
	// （debounce 5 秒，見 gpscalib.RecomputeAsync）——不阻塞這次上傳的回應。
	gpscalib.RecomputeAsync(s.repo.db, userID)

	return &gpsRunResult{
		DistanceKm: round2(distanceKm), DurationS: durationS, AvgPaceS: avgPaceS,
		Flagged: flagged, FlagReason: flagReason, AnomalySegs: anomalies, ExpAwarded: !flagged,
		KmPaces: kmSplits, RawDistanceKm: round2(rawKm), CalibFactor: k,
		ExcludedKm: excludedKm, ExcludedSegments: calc.ExcludedSegs,
	}, nil
}

// accPercentiles 回傳 accs 的 p50/p90（皆為 nil 代表無精度資料，如全部點都沒帶 acc 欄位）。
// 量測用途（見 gps_runs.acc_p50/acc_p90），本期無任何邏輯讀取，僅供後續分層分析。
func accPercentiles(accs []float64) (p50, p90 *float64) {
	if len(accs) == 0 {
		return nil, nil
	}
	s := append([]float64(nil), accs...)
	sort.Float64s(s)
	at := func(q float64) *float64 {
		i := int(q * float64(len(s)-1))
		v := s[i]
		return &v
	}
	return at(0.5), at(0.9)
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// dedupeStrings 去除空字串與重複值，保留原始順序（GPS 上傳 pet_ids 用：避免前端誤帶重複 ID
// 導致 Repository.ValidateOwnedPets 的筆數比對誤判失敗，見 SaveGPSRun）。
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// HistAvgPace 該會員歷史日常平均配速（秒/km；無資料回 0）
func (r *Repository) HistAvgPace(ctx context.Context, userID string) int {
	var v float64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(AVG(avg_pace_s),0) FROM activities WHERE user_id=$1 AND NOT flagged AND avg_pace_s > 0`,
		userID).Scan(&v)
	return int(v)
}

// InsertGPSRun 寫入 GPS 軌跡（壓縮 polyline）+ 防弊結果。
// 冪等：靠 uq_gps_runs_user_start(user_id, started_at) 唯一索引 + ON CONFLICT DO NOTHING——
// 同一 user 的同一起跑時間已存在時不再插入、回 inserted=false，呼叫端據此不重複進活動管線/發獎。
// 回傳新增列的 id（inserted=false 時為空字串）——SaveGPSRun 需要這個 id 在 XAdd 失敗時
// 呼叫 DeleteGPSRun 補償回滾（見該函式與 H4 的說明）。
func (r *Repository) InsertGPSRun(ctx context.Context, userID, raceID string, started, ended time.Time,
	distanceKm float64, durationS, avgPaceS int, flagged bool, flagReason string, pointCount int, polyline string, kmPaces []int,
	calibFactor, calibDistanceKm float64, clientVersion string, accP50, accP90 *float64, usedPointCount int,
	excludedKm float64, excludedSegments int, petIDs []string) (id string, inserted bool, err error) {
	var rid interface{}
	if raceID != "" {
		rid = raceID
	}
	if petIDs == nil {
		petIDs = []string{} // gps_runs.pet_ids NOT NULL DEFAULT '{}'（migration 174），避免傳 NULL 進去
	}
	err = r.db.QueryRow(ctx, `
		INSERT INTO gps_runs (user_id, race_id, started_at, ended_at, distance_km, duration_s,
		                      avg_pace_s, flagged, flag_reason, point_count, polyline, km_paces,
		                      calib_factor, calib_distance_km, client_version, acc_p50, acc_p90, used_point_count,
		                      excluded_km, excluded_segments, pet_ids)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,NULLIF($15,''),$16,$17,$18,$19,$20,$21)
		ON CONFLICT (user_id, started_at) DO NOTHING
		RETURNING id`,
		userID, rid, started, ended, distanceKm, durationS, avgPaceS, flagged, flagReason, pointCount, polyline, kmPaces,
		calibFactor, calibDistanceKm, clientVersion, accP50, accP90, usedPointCount,
		excludedKm, excludedSegments, petIDs).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil // 同一趟已上傳過 → 冪等 no-op
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

// DeleteGPSRun 補償刪除：SaveGPSRun 寫入 gps_runs 後，若推入活動佇列（XAdd）失敗，
// 用這個回滾剛剛那筆——避免留下「已入庫、卻永遠不會被 worker 消費」的孤兒列，也避免使用者
// 重新上傳同一趟時被 uq_gps_runs_user_start 冪等擋下、以為已經成功卻其實從未發過 EXP/SP。
func (r *Repository) DeleteGPSRun(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM gps_runs WHERE id = $1`, id)
	return err
}

// unenqueuedGPSRun 是 ListUnenqueuedGPS 一列的完整欄位（RequeueUnenqueued 據此重建 ActivityEvent，
// 欄位口徑與 SaveGPSRun/AdminApproveGPS 組出的事件一致，見兩處的 gpsReviewResult／calc 欄位對照）。
type unenqueuedGPSRun struct {
	ID, UserID, RaceID         string
	RawDistanceKm, CalibFactor float64
	CalibDistanceKm            *float64 // NULL 時退化成 RawDistanceKm（等同係數 1.0），比照 reviewGPS
	DurationS, RawAvgPaceS     int
	EndedAt                    time.Time
	KmPaces                    []int
	Flagged                    bool
	PetIDs                     []string // 寵物歸戶（migration 174）；讀回 gps_runs.pet_ids，補送時原樣帶回 ActivityEvent
}

// ListUnenqueuedGPS 找出「已寫入 gps_runs，卻因程序中斷（INSERT 與 XAdd 之間當機）而漏推入活動
// 佇列」的孤兒列（finding 2，2026-09-08 第二次稽核；migrations/172 gps_runs.enqueued_at）——
// SaveGPSRun/AdminApproveGPS 走的是「先寫 DB、後 XAdd」，兩步之間若整個程序被殺掉，DB 這筆已經
// 落地但活動事件永遠不會被送出，且因 uq_gps_runs_user_start 冪等索引，使用者重傳同一趟只會被當
// 重複擋下、永遠補不回來——RequeueUnenqueued 是這個缺口的背景兜底掃描。
//
// 篩選條件：
//   - enqueued_at IS NULL（尚未成功入隊，見 markGPSEnqueued）。
//   - created_at < 門檻：給正常路徑（含 finding 1 的 revert 重試）足夠時間先走完，避免跟「還在
//     處理中」的請求搶著入隊同一筆。
//   - (NOT flagged OR review_action = 'approved')：flagged 且尚待審（review_action IS NULL）的
//     一律排除——這類本就不該自動入隊，要等管理者核准；flagged 且已駁回（'rejected'）也排除——
//     永遠不該入隊。只有「未標記的一般上傳」或「已核准」兩種狀態才是入隊的合法候選，跟
//     SaveGPSRun／AdminApproveGPS 本身「誰會呼叫 enqueueActivityEvent」的判斷完全對齊。
//   - 額外防線：NOT EXISTS 對應活動（同 user、source IS NULL、recorded_at=ended_at 取到秒——activities.recorded_at
//     是由 RFC3339（無毫秒）字串寫入、gps_runs.ended_at 保留毫秒，兩邊必須 date_trunc('second') 才對得上，
//     否則這道防線永遠不命中、補送會重複入帳（審查抓到）；配對慣例同
//     gps_recall.go 檔頭註解）——activities 表對 GPS 來源（source IS NULL）沒有有效的唯一約束
//     （見 migrations/113_gps_run_idempotency.sql 檔頭說明：idx_activities_source_ext(source,
//     external_id) 對 NULL 形同虛設），若只因為 markGPSEnqueued 這個「標記」寫入失敗（XAdd 本身
//     其實已成功、活動也已建立）就誤判成「未入隊」而重新 XAdd，worker 會真的插入第二筆重複活動、
//     且不會被任何去重邏輯攔下（resolveCrossSourceDups 只處理跨來源，不處理同源 GPS 重複）。
//     這道 NOT EXISTS 保證只有「真的還沒有對應活動」的孤兒列才會被重新入隊。
func (r *Repository) ListUnenqueuedGPS(ctx context.Context, olderThan time.Duration, limit int) ([]unenqueuedGPSRun, error) {
	rows, err := r.db.Query(ctx, `
		SELECT g.id::text, g.user_id::text, COALESCE(g.race_id::text,''), g.distance_km, g.duration_s,
		       g.avg_pace_s, g.ended_at, g.calib_distance_km, g.calib_factor, g.km_paces, g.flagged, g.pet_ids
		FROM gps_runs g
		WHERE g.enqueued_at IS NULL
		  AND g.created_at < $1
		  AND (NOT g.flagged OR g.review_action = 'approved')
		  AND NOT EXISTS (
		    SELECT 1 FROM activities a
		    WHERE a.user_id = g.user_id AND a.source IS NULL AND date_trunc('second', a.recorded_at) = date_trunc('second', g.ended_at))
		ORDER BY g.created_at ASC
		LIMIT $2`, time.Now().Add(-olderThan), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []unenqueuedGPSRun{}
	for rows.Next() {
		var g unenqueuedGPSRun
		var calibDist *float64
		if err := rows.Scan(&g.ID, &g.UserID, &g.RaceID, &g.RawDistanceKm, &g.DurationS,
			&g.RawAvgPaceS, &g.EndedAt, &calibDist, &g.CalibFactor, &g.KmPaces, &g.Flagged, &g.PetIDs); err != nil {
			return nil, err
		}
		g.CalibDistanceKm = calibDist
		out = append(out, g)
	}
	return out, rows.Err()
}

// gpsRequeueOlderThan/gpsRequeueLimit：RequeueUnenqueued 每輪的篩選門檻與上限（見
// ListUnenqueuedGPS 註解）。15 分鐘遠高於正常上傳/核准流程的入隊耗時（毫秒等級），足夠讓「還在
// 處理中」的請求先走完，避免搶跑；200 筆足夠涵蓋單輪異常量，掃描本身走 hourly ops selfcheck loop
// （見 internal/ops/selfcheck.go），下一輪還會再掃一次剩下的。
const (
	gpsRequeueOlderThan = 15 * time.Minute
	gpsRequeueLimit     = 200
)

// RequeueUnenqueued 背景兜底：補送 ListUnenqueuedGPS 找出的孤兒列（見該函式與 finding 2 註解）。
// 供 internal/ops/selfcheck.go 的每小時排程呼叫（比照 EinvoiceReporter.SweepPending 的小介面
// 注入模式，見該檔案 GPSRequeuer）。單筆失敗只記錄+告警並繼續下一筆，不中止整批——enqueued_at
// 仍是 NULL，下一輪排程會再試一次。
func (s *Service) RequeueUnenqueued(ctx context.Context) {
	runs, err := s.repo.ListUnenqueuedGPS(ctx, gpsRequeueOlderThan, gpsRequeueLimit)
	if err != nil {
		log.Error().Err(err).Msg("RequeueUnenqueued: ListUnenqueuedGPS failed")
		return
	}
	for _, g := range runs {
		avgPaceS := g.RawAvgPaceS
		calibDistanceKm := g.RawDistanceKm
		if g.CalibDistanceKm != nil && *g.CalibDistanceKm > 0 {
			calibDistanceKm = *g.CalibDistanceKm
			avgPaceS = int(float64(g.DurationS) / calibDistanceKm)
		}
		evt := ActivityEvent{
			UserID: g.UserID, RaceID: g.RaceID, DistanceKm: round2(calibDistanceKm),
			DurationS: g.DurationS, AvgPaceS: avgPaceS, RecordedAt: g.EndedAt.Format(time.RFC3339),
			RawDistanceKm: round2(g.RawDistanceKm), CalibFactor: g.CalibFactor,
			PetIDs: g.PetIDs,
		}
		if !g.Flagged {
			// 只有「一般上傳、未標記」路徑的原始事件才帶每公里分段配速，比照 SaveGPSRun 的組法；
			// AdminApproveGPS 核准路徑本就不帶這欄，這裡維持跟各自「原本會產生的事件」一致，不
			// 在補送時無中生有多帶欄位。
			evt.KmPaces = g.KmPaces
		}
		if err := enqueueActivityEvent(ctx, s.rdb, evt); err != nil {
			log.Error().Err(err).Str("gps_run_id", g.ID).Str("user_id", g.UserID).
				Msg("RequeueUnenqueued: XAdd failed, will retry next round")
			notify.Alert("gps_requeue_failed", "GPS 孤兒列補送入隊失敗",
				fmt.Sprintf("gps_run_id=%s user_id=%s err=%v", g.ID, g.UserID, err))
			continue
		}
		if err := s.repo.markGPSEnqueued(ctx, g.ID); err != nil {
			log.Error().Err(err).Str("gps_run_id", g.ID).
				Msg("RequeueUnenqueued: markGPSEnqueued failed after successful XAdd")
			notify.Alert("gps_requeue_mark_failed", "GPS 孤兒列補送入隊成功但標記失敗（下一輪可能重複掃到，仍受 NOT EXISTS 防重複保護）",
				fmt.Sprintf("gps_run_id=%s user_id=%s err=%v", g.ID, g.UserID, err))
		}
	}
}

// POST /api/v1/activities/gps — 上傳網頁 GPS 跑步軌跡
func (h *Handler) UploadGPS(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		http.Error(w, `{"error":"login required"}`, http.StatusUnauthorized)
		return
	}

	// 單一登入 Layer 2：access token 短效但仍可能在有效期內被 stale 裝置拿來上傳，
	// 直接查帳號目前 session_epoch 比對 token 的 sev claim；不符 → 已被更新的登入取代，
	// 回 401（讓前端走既有 401→refresh(也會 401)→登出 流程），不進 SaveGPSRun。
	// 查詢失敗（DB 暫時性錯誤）則不因此擋上傳，沿用既有「查無/查失敗＝放行」的防呆原則。
	sev, _ := r.Context().Value(auth.CtxKeySessionEpoch).(int)
	if cur, err := h.svc.repo.CurrentSessionEpoch(r.Context(), userID); err == nil && sev != cur {
		http.Error(w, `{"error":"session superseded"}`, http.StatusUnauthorized)
		return
	}

	var req gpsRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	// 軌跡點數上限：20000 點約等於全馬 5 小時、每秒一點的軌跡量，仍有餘裕；
	// 後續 Douglas-Peucker 簡化最壞情況 O(N²) 且同步跑在 handler 內，超量請求先擋下避免拖垮伺服器。
	if len(req.Points) > 20000 {
		http.Error(w, `{"error":"軌跡點數超過上限"}`, http.StatusRequestEntityTooLarge)
		return
	}
	res, err := h.svc.SaveGPSRun(r.Context(), userID, req)
	if errors.Is(err, ErrGPSEnqueueFailed) {
		// H4：入隊失敗已於 SaveGPSRun 內補償回滾，這裡回 503 讓前端走既有「重試」流程——
		// 資料仍在手機本地快取，不是驗證錯誤（400），不能讓前端誤以為這趟資料本身有問題而丟棄。
		http.Error(w, `{"error":"上傳暫時失敗，請稍後再試（資料未遺失，仍在手機上）"}`, http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	b, _ := json.Marshal(map[string]any{"result": res})
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}
