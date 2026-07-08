package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/auth"
)

// 防弊參數
const (
	gpsMaxAccuracyM   = 65.0  // 精度差於此（公尺）的點不列入距離計算（城市訊號較差，放寬）
	gpsMinSegMeters   = 5.0   // 太短的位移不做超速判定（避免 GPS 飄移誤判）
	gpsMinDistKm      = 0.005 // 短於此（公里=5m）視為移動距離不足，不計算/不記錄/不判異常
	gpsFastRatioFlag  = 0.30  // 超速距離占比達此且總距離足夠 → 判定疑似載具
	gpsRatioMinDistKm = 0.3   // 套用「超速占比」判定所需的最低總距離（避免短程單一跳點誤判）
)

type gpsPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
	T   int64   `json:"t"`   // epoch 毫秒
	Acc float64 `json:"acc"` // 精度（公尺）
}

type gpsRunReq struct {
	RaceID    string     `json:"race_id"`
	StartedAt string     `json:"started_at"`
	EndedAt   string     `json:"ended_at"`
	Points    []gpsPoint `json:"points"`
}

type gpsRunResult struct {
	DistanceKm  float64 `json:"distance_km"`
	DurationS   int     `json:"duration_s"`
	AvgPaceS    int     `json:"avg_pace_s"`
	Flagged     bool    `json:"flagged"`
	FlagReason  string  `json:"flag_reason,omitempty"`
	AnomalySegs int     `json:"anomaly_segments"`
	ExpAwarded  bool    `json:"exp_awarded"` // 未標記才進活動管線發里程 EXP
	TooShort    bool    `json:"too_short"`   // 移動距離不足，無法計算（非異常）
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

// SaveGPSRun 伺服器端重算 + 防弊；未標記者推入活動管線（記錄+里程EXP）
func (s *Service) SaveGPSRun(ctx context.Context, userID string, req gpsRunReq) (*gpsRunResult, error) {
	if len(req.Points) < 2 {
		return nil, fmt.Errorf("軌跡點不足")
	}

	maxSpeed := 1000.0 / float64(minPaceSecPerKm) // 公尺/秒（= 2:00/km 對應速度）
	var distM, fastDistM float64
	var anomalies int
	var prev *gpsPoint
	// 每公里分段配速（秒/km）：距離每跨一整公里記一段，供「平均配速區間」任務改用「任一公里落在區間即算」判定
	//（比整段均配速好達成）。伺服器端由軌跡重算 → 可信、不易偽造。
	var kmSplits []int
	kmTarget := 1000.0
	lastKmT := req.Points[0].T
	for i := range req.Points {
		p := &req.Points[i]
		if p.Acc > 0 && p.Acc > gpsMaxAccuracyM {
			continue // 精度太差，略過
		}
		if prev != nil {
			d := haversineM(prev.Lat, prev.Lng, p.Lat, p.Lng)
			dt := float64(p.T-prev.T) / 1000.0
			if dt > 0 {
				if d > gpsMinSegMeters && d/dt > maxSpeed {
					anomalies++                // 超過人類極限速度的區段（疑似載具/GPS 跳點）
					fastDistM += maxSpeed * dt // 以極限速度估計超速距離（供占比判定）——但不列入有效里程
				} else {
					distM += d // 只有「正常速度」才算有效距離；超速段完全不計（不刷里程、不推進課表）
					for distM >= kmTarget { // 跨過整公里 → 記這一段配速
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

	distanceKm := distM / 1000.0
	durationS := int((req.Points[len(req.Points)-1].T - req.Points[0].T) / 1000)
	if durationS <= 0 {
		return nil, fmt.Errorf("時間區間無效")
	}
	avgPaceS := 0
	if distanceKm > 0 {
		avgPaceS = int(float64(durationS) / distanceKm)
	}

	// 防弊判定（先算）：只抓「過快」（疑似騎車/搭車等載具），不抓過慢（走路、慢跑皆正常）。
	// 占比以「原始移動 rawM＝有效+超速」為分母，避免有效距離被排除後占比失真。
	rawM := distM + fastDistM
	fastRatio := 0.0
	if rawM > 0 {
		fastRatio = fastDistM / rawM
	}
	var reasons []string
	if avgPaceS > 0 && avgPaceS < minPaceSecPerKm {
		reasons = append(reasons, "平均配速快於 2:00/km（疑似使用交通工具）")
	}
	// 超速占比：需有足夠原始移動才判定，避免短程單一 GPS 跳點被誤判為異常
	if rawM/1000.0 >= gpsRatioMinDistKm && fastRatio >= gpsFastRatioFlag {
		reasons = append(reasons, fmt.Sprintf("逾三成距離超過人體極限速度（%d 段，疑似載具）", anomalies))
	}
	flagged := len(reasons) > 0
	flagReason := strings.Join(reasons, "；")

	// 有效距離不足「且」未判定為載具 → 單純距離不足（走幾步），不記錄、不發 EXP。
	// 若是載具（整趟超速被排除、有效距離趨近 0）則不走這裡，仍以 flagged 記錄一筆（歷史看得到、且不發獎）。
	if distanceKm < gpsMinDistKm && !flagged {
		return &gpsRunResult{
			DistanceKm: round2(distanceKm), DurationS: durationS, AvgPaceS: avgPaceS, TooShort: true,
		}, nil
	}

	started, _ := time.Parse(time.RFC3339, req.StartedAt)
	ended, _ := time.Parse(time.RFC3339, req.EndedAt)
	// 軌跡壓縮：精度過差的點剔除 → Douglas-Peucker 簡化(5m) → encoded polyline（數百 bytes）
	latlng := make([][2]float64, 0, len(req.Points))
	for _, p := range req.Points {
		if p.Acc == 0 || p.Acc <= gpsMaxAccuracyM {
			latlng = append(latlng, [2]float64{p.Lat, p.Lng})
		}
	}
	polyline := encodePolyline(simplifyPath(latlng, 5))
	if err := s.repo.InsertGPSRun(ctx, userID, req.RaceID, started, ended,
		round2(distanceKm), durationS, avgPaceS, flagged, flagReason, len(req.Points), polyline, kmSplits); err != nil {
		return nil, err
	}

	// 未標記 → 進既有活動管線（記錄活動 + 日常里程 EXP）
	if !flagged && distanceKm > 0 {
		evt := ActivityEvent{
			UserID:     userID,
			RaceID:     req.RaceID,
			DistanceKm: round2(distanceKm),
			DurationS:  durationS,
			AvgPaceS:   avgPaceS,
			RecordedAt: ended.Format(time.RFC3339),
			KmPaces:    kmSplits,
		}
		b, _ := json.Marshal(evt)
		s.rdb.XAdd(ctx, &redis.XAddArgs{Stream: streamKey, Values: map[string]any{"data": string(b)}})
	}

	return &gpsRunResult{
		DistanceKm: round2(distanceKm), DurationS: durationS, AvgPaceS: avgPaceS,
		Flagged: flagged, FlagReason: flagReason, AnomalySegs: anomalies, ExpAwarded: !flagged,
	}, nil
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// HistAvgPace 該會員歷史日常平均配速（秒/km；無資料回 0）
func (r *Repository) HistAvgPace(ctx context.Context, userID string) int {
	var v float64
	_ = r.db.QueryRow(ctx,
		`SELECT COALESCE(AVG(avg_pace_s),0) FROM activities WHERE user_id=$1 AND NOT flagged AND avg_pace_s > 0`,
		userID).Scan(&v)
	return int(v)
}

// InsertGPSRun 寫入 GPS 軌跡（壓縮 polyline）+ 防弊結果
func (r *Repository) InsertGPSRun(ctx context.Context, userID, raceID string, started, ended time.Time,
	distanceKm float64, durationS, avgPaceS int, flagged bool, flagReason string, pointCount int, polyline string, kmPaces []int) error {
	var rid interface{}
	if raceID != "" {
		rid = raceID
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO gps_runs (user_id, race_id, started_at, ended_at, distance_km, duration_s,
		                      avg_pace_s, flagged, flag_reason, point_count, polyline, km_paces)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12)`,
		userID, rid, started, ended, distanceKm, durationS, avgPaceS, flagged, flagReason, pointCount, polyline, kmPaces)
	return err
}

// POST /api/v1/activities/gps — 上傳網頁 GPS 跑步軌跡
func (h *Handler) UploadGPS(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		http.Error(w, `{"error":"login required"}`, http.StatusUnauthorized)
		return
	}
	var req gpsRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	res, err := h.svc.SaveGPSRun(r.Context(), userID, req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	b, _ := json.Marshal(map[string]any{"result": res})
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}
