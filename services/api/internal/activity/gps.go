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
	gpsMaxAccuracyM = 40.0 // 精度差於此（公尺）的點不列入距離計算
	gpsMinSegMeters = 5.0  // 太短的位移不做超速判定（避免 GPS 飄移誤判）
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
	DistanceKm   float64 `json:"distance_km"`
	DurationS    int     `json:"duration_s"`
	AvgPaceS     int     `json:"avg_pace_s"`
	Flagged      bool    `json:"flagged"`
	FlagReason   string  `json:"flag_reason,omitempty"`
	AnomalySegs  int     `json:"anomaly_segments"`
	ExpAwarded   bool    `json:"exp_awarded"` // 未標記才進活動管線發里程 EXP
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
	var distM float64
	var anomalies int
	var prev *gpsPoint
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
					anomalies++ // 超過人類極限速度的區段
				}
				distM += d
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

	// 防弊判定
	var reasons []string
	if avgPaceS > 0 && avgPaceS < minPaceSecPerKm {
		reasons = append(reasons, "平均配速快於 2:00/km")
	}
	if hist := s.repo.HistAvgPace(ctx, userID); hist > 0 && avgPaceS > 0 && avgPaceS < hist/2 {
		reasons = append(reasons, "遠快於日常配速")
	}
	if anomalies > 0 {
		reasons = append(reasons, fmt.Sprintf("%d 個超速區段", anomalies))
	}
	flagged := len(reasons) > 0
	flagReason := strings.Join(reasons, "；")

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
		round2(distanceKm), durationS, avgPaceS, flagged, flagReason, len(req.Points), polyline); err != nil {
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
	distanceKm float64, durationS, avgPaceS int, flagged bool, flagReason string, pointCount int, polyline string) error {
	var rid interface{}
	if raceID != "" {
		rid = raceID
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO gps_runs (user_id, race_id, started_at, ended_at, distance_km, duration_s,
		                      avg_pace_s, flagged, flag_reason, point_count, polyline)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11)`,
		userID, rid, started, ended, distanceKm, durationS, avgPaceS, flagged, flagReason, pointCount, polyline)
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
