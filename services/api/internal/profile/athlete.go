package profile

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/dor/api/internal/auth"
)

// 性別相異加分（報名推薦：男→優先女、女→優先男）
const genderBonus = 35.0

// --- 設定 ---

type AthleteMetricConfig struct {
	MetricKey    string  `json:"metric_key"`
	Weight       int     `json:"weight"`
	RefLo        float64 `json:"ref_lo"`
	RefHi        float64 `json:"ref_hi"`
	DisplayOrder int     `json:"display_order"`
}
type AthleteLevel struct {
	MinScore int    `json:"min_score"`
	Name     string `json:"name"`
}

func (h *Handler) loadAthleteConfig(ctx context.Context) ([]AthleteMetricConfig, []AthleteLevel, error) {
	rows, err := h.db.Query(ctx, `SELECT metric_key, weight, ref_lo, ref_hi, display_order FROM athlete_metric_config ORDER BY display_order`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	metrics := []AthleteMetricConfig{}
	for rows.Next() {
		var m AthleteMetricConfig
		if err := rows.Scan(&m.MetricKey, &m.Weight, &m.RefLo, &m.RefHi, &m.DisplayOrder); err != nil {
			return nil, nil, err
		}
		metrics = append(metrics, m)
	}
	lrows, err := h.db.Query(ctx, `SELECT min_score, name FROM athlete_levels ORDER BY min_score`)
	if err != nil {
		return nil, nil, err
	}
	defer lrows.Close()
	levels := []AthleteLevel{}
	for lrows.Next() {
		var l AthleteLevel
		if err := lrows.Scan(&l.MinScore, &l.Name); err != nil {
			return nil, nil, err
		}
		levels = append(levels, l)
	}
	return metrics, levels, nil
}

// --- 聚合與計算 ---

type athleteAgg struct {
	volume   float64
	count    int
	totalDur int
	longest  float64
	first    time.Time
	last     time.Time
}

// AthleteStats 單一使用者的選手指標與分級
type AthleteStats struct {
	VolumeKm    float64 `json:"volume_km"`
	Activities  int     `json:"activities"`
	PaceS       int     `json:"pace_s"`
	AvgDistKm   float64 `json:"avg_dist_km"`
	LongestKm   float64 `json:"longest_km"`
	MonthlyFreq float64 `json:"monthly_freq"`
	Score       int     `json:"score"`
	Level       string  `json:"level"`
}

// aggregateActivities 取多位使用者的活動聚合（未 flagged）
func (h *Handler) aggregateActivities(ctx context.Context, userIDs []string) (map[string]athleteAgg, error) {
	out := map[string]athleteAgg{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := h.db.Query(ctx, `
		SELECT user_id::text, COALESCE(SUM(distance_km),0), COUNT(*), COALESCE(SUM(duration_s),0),
		       COALESCE(MAX(distance_km),0), MIN(recorded_at), MAX(recorded_at)
		FROM activities WHERE NOT flagged AND user_id = ANY($1::uuid[])
		GROUP BY user_id`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var a athleteAgg
		var first, last *time.Time
		if err := rows.Scan(&id, &a.volume, &a.count, &a.totalDur, &a.longest, &first, &last); err != nil {
			return nil, err
		}
		if first != nil {
			a.first = *first
		}
		if last != nil {
			a.last = *last
		}
		out[id] = a
	}
	return out, rows.Err()
}

func metricRawValue(key string, a athleteAgg) float64 {
	switch key {
	case "volume":
		return a.volume
	case "pace":
		if a.volume > 0 {
			return float64(a.totalDur) / a.volume
		}
		return 0
	case "avg_dist":
		if a.count > 0 {
			return a.volume / float64(a.count)
		}
		return 0
	case "longest":
		return a.longest
	case "monthly_freq":
		months := 1.0
		if a.count > 1 {
			d := a.last.Sub(a.first).Hours() / 24 / 30
			if d > 1 {
				months = d
			}
		}
		return float64(a.count) / months
	}
	return 0
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// computeAthlete 由聚合 + 設定算出指標、composite 分數與等級
func computeAthlete(a athleteAgg, metrics []AthleteMetricConfig, levels []AthleteLevel) AthleteStats {
	s := AthleteStats{
		VolumeKm:    math.Round(a.volume*10) / 10,
		Activities:  a.count,
		PaceS:       int(metricRawValue("pace", a)),
		AvgDistKm:   math.Round(metricRawValue("avg_dist", a)*10) / 10,
		LongestKm:   math.Round(a.longest*10) / 10,
		MonthlyFreq: math.Round(metricRawValue("monthly_freq", a)*10) / 10,
	}
	var sum, wsum float64
	for _, m := range metrics {
		if m.Weight <= 0 || m.RefHi == m.RefLo {
			continue
		}
		v := metricRawValue(m.MetricKey, a)
		var norm float64
		if m.MetricKey == "pace" {
			// 越低越好；無資料(0) 視為 0 分
			if v <= 0 {
				norm = 0
			} else {
				norm = clamp01((m.RefHi - v) / (m.RefHi - m.RefLo))
			}
		} else {
			norm = clamp01((v - m.RefLo) / (m.RefHi - m.RefLo))
		}
		sum += float64(m.Weight) * norm
		wsum += float64(m.Weight)
	}
	composite := 0.0
	if wsum > 0 {
		composite = sum / wsum * 100
	}
	s.Score = int(math.Round(composite))
	for _, l := range levels {
		if s.Score >= l.MinScore {
			s.Level = l.Name
		}
	}
	return s
}

// athleteStatsFor 單一使用者的選手分級（後台會員詳情用）
func (h *Handler) athleteStatsFor(ctx context.Context, userID string) (AthleteStats, error) {
	metrics, levels, err := h.loadAthleteConfig(ctx)
	if err != nil {
		return AthleteStats{}, err
	}
	aggs, err := h.aggregateActivities(ctx, []string{userID})
	if err != nil {
		return AthleteStats{}, err
	}
	return computeAthlete(aggs[userID], metrics, levels), nil
}

// --- 後台設定端點 ---

// GET /api/v1/admin/membership/athlete-config
func (h *Handler) GetAthleteConfig(w http.ResponseWriter, r *http.Request) {
	metrics, levels, err := h.loadAthleteConfig(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"metrics": metrics, "levels": levels})
}

// PUT /api/v1/admin/membership/athlete-config —— 整批取代
func (h *Handler) PutAthleteConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Metrics []AthleteMetricConfig `json:"metrics"`
		Levels  []AthleteLevel        `json:"levels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer tx.Rollback(r.Context())
	for _, m := range body.Metrics {
		if _, err := tx.Exec(r.Context(),
			`UPDATE athlete_metric_config SET weight=$1, ref_lo=$2, ref_hi=$3 WHERE metric_key=$4`,
			m.Weight, m.RefLo, m.RefHi, m.MetricKey); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
	}
	if len(body.Levels) > 0 {
		if _, err := tx.Exec(r.Context(), `DELETE FROM athlete_levels`); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed")
			return
		}
		for _, l := range body.Levels {
			if l.Name == "" {
				continue
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO athlete_levels (min_score, name) VALUES ($1,$2)`, l.MinScore, l.Name); err != nil {
				respondErr(w, http.StatusBadRequest, "level 寫入失敗（min_score 重複？）")
				return
			}
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	h.rt.PublishData(r.Context(), "dashboard", nil)
	h.GetAthleteConfig(w, r)
}

// --- 報名頁推薦 ---

type RecommendRow struct {
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	AvatarURL   string `json:"avatar_url"`
	AccountCode string `json:"account_code"`
}

// GET /api/v1/profile/recommendations/{raceID}
// 追蹤者中也報名此賽事者，依「性別相異優先 + 與自己程度相近」加權，取前三。
func (h *Handler) RaceRecommendations(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	raceID := chi.URLParam(r, "raceID")

	// 我的性別
	var myGender string
	h.db.QueryRow(r.Context(), `SELECT COALESCE(gender,'') FROM user_profiles WHERE user_id=$1`, userID).Scan(&myGender)

	// 追蹤者中報名此賽事者
	rows, err := h.db.Query(r.Context(), `
		SELECT u.id::text, COALESCE(NULLIF(p.nickname,''), u.handle), COALESCE(u.avatar_url,''),
		       COALESCE(u.account_code,''), COALESCE(p.gender,'')
		FROM follows f
		JOIN registrations reg ON reg.user_id = f.followee_id AND reg.race_id = $2 AND reg.status <> 'cancelled'
		JOIN users u ON u.id = f.followee_id
		LEFT JOIN user_profiles p ON p.user_id = u.id
		WHERE f.follower_id = $1`, userID, raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	type cand struct {
		row    RecommendRow
		gender string
	}
	var cands []cand
	ids := []string{userID}
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.row.UserID, &c.row.Nickname, &c.row.AvatarURL, &c.row.AccountCode, &c.gender); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		cands = append(cands, c)
		ids = append(ids, c.row.UserID)
	}

	if len(cands) == 0 {
		respondJSON(w, http.StatusOK, map[string]any{"recommendations": []RecommendRow{}})
		return
	}

	metrics, levels, err := h.loadAthleteConfig(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	aggs, err := h.aggregateActivities(r.Context(), ids)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	myScore := float64(computeAthlete(aggs[userID], metrics, levels).Score)

	type scored struct {
		row   RecommendRow
		score float64
	}
	out := make([]scored, 0, len(cands))
	oppositeOf := map[string]string{"male": "female", "female": "male"}
	for _, c := range cands {
		theirScore := float64(computeAthlete(aggs[c.row.UserID], metrics, levels).Score)
		similarity := 100 - math.Abs(myScore-theirScore) // 越相近越高
		total := similarity
		if myGender != "" && c.gender == oppositeOf[myGender] {
			total += genderBonus
		}
		out = append(out, scored{c.row, total})
	}
	// 依分數遞減排序，取前三
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].score > out[i].score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	top := []RecommendRow{}
	for i := 0; i < len(out) && i < 3; i++ {
		top = append(top, out[i].row)
	}
	respondJSON(w, http.StatusOK, map[string]any{"recommendations": top})
}
