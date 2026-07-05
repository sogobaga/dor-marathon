package profile

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/auth"
)

// 跨來源（App GPS / Strava）重複活動去重的「使用者互動」層：偏好來源設定、首次彈窗提示/確認。
// 實際週期性去重由 worker resolveCrossSourceDups 執行；此處提供「立即重解」與 UI 端點。

// reResolveUser 立即重解某使用者的跨來源重複：先解除他既有的 cross_source_duplicate 標記，
// 再依 source（gps|strava）重新配對、flag 非該來源那筆。用於玩家手動選擇/切換偏好時（免等 worker）。
func reResolveUser(ctx context.Context, db *pgxpool.Pool, userID, source string) {
	if source != "gps" && source != "strava" {
		return
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE activities SET flagged=FALSE, flag_reason=NULL, dup_of=NULL
		WHERE user_id=$1 AND flag_reason='cross_source_duplicate'`, userID); err != nil {
		return
	}
	if _, err := tx.Exec(ctx, `
		WITH pairs AS (
			SELECT g.id AS gps_id, s.id AS strava_id
			FROM activities g
			JOIN activities s ON s.user_id=g.user_id AND s.source='strava' AND NOT s.flagged
			WHERE g.user_id=$1 AND g.source IS NULL AND NOT g.flagged AND g.duration_s>0 AND s.duration_s>0
			  AND (g.recorded_at - make_interval(secs=>g.duration_s)) < (s.recorded_at + make_interval(secs=>s.duration_s))
			  AND s.recorded_at < g.recorded_at
		)
		UPDATE activities a SET flagged=TRUE, flag_reason='cross_source_duplicate',
			dup_of = CASE WHEN $2='gps' THEN pairs.gps_id ELSE pairs.strava_id END
		FROM pairs
		WHERE ($2='gps' AND a.id=pairs.strava_id) OR ($2='strava' AND a.id=pairs.gps_id)`, userID, source); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

// SetDataSource POST /api/v1/profile/data-source {source} — 設定偏好資料來源 + 立即重解
func (h *Handler) SetDataSource(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Source != "gps" && req.Source != "strava") {
		respondErr(w, http.StatusBadRequest, "source 需為 gps 或 strava")
		return
	}
	if _, err := h.db.Exec(r.Context(), `
		INSERT INTO user_profiles (user_id, preferred_data_source, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (user_id) DO UPDATE SET preferred_data_source=$2, updated_at=NOW()`, userID, req.Source); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	reResolveUser(r.Context(), h.db, userID, req.Source)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "preferred_data_source": req.Source})
}

type dedupSide struct {
	Source     string    `json:"source"` // gps|strava
	DistanceKm float64   `json:"distance_km"`
	DurationS  int       `json:"duration_s"`
	RecordedAt time.Time `json:"recorded_at"`
}

// DedupNotice GET /api/v1/profile/dedup-notice — 未提示過且有跨來源重複 → 回一組讓玩家選；否則 notice=null
func (h *Handler) DedupNotice(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var prompted bool
	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(dedup_prompted,FALSE) FROM user_profiles WHERE user_id=$1`, userID).Scan(&prompted)
	if prompted {
		respondJSON(w, http.StatusOK, map[string]any{"notice": nil})
		return
	}
	var loserSrc, winSrc string
	var loserKm, winKm float64
	var loserDur, winDur int
	var loserAt, winAt time.Time
	err := h.db.QueryRow(r.Context(), `
		SELECT COALESCE(loser.source,'gps'), loser.distance_km, loser.duration_s, loser.recorded_at,
		       COALESCE(win.source,'gps'), win.distance_km, win.duration_s, win.recorded_at
		FROM activities loser JOIN activities win ON win.id = loser.dup_of
		WHERE loser.user_id=$1 AND loser.flag_reason='cross_source_duplicate'
		ORDER BY loser.recorded_at DESC LIMIT 1`, userID).
		Scan(&loserSrc, &loserKm, &loserDur, &loserAt, &winSrc, &winKm, &winDur, &winAt)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"notice": nil})
		return
	}
	sides := map[string]dedupSide{
		loserSrc: {Source: loserSrc, DistanceKm: loserKm, DurationS: loserDur, RecordedAt: loserAt},
		winSrc:   {Source: winSrc, DistanceKm: winKm, DurationS: winDur, RecordedAt: winAt},
	}
	var cur string
	_ = h.db.QueryRow(r.Context(), `SELECT COALESCE(preferred_data_source,'gps') FROM user_profiles WHERE user_id=$1`, userID).Scan(&cur)
	if cur == "" {
		cur = "gps"
	}
	respondJSON(w, http.StatusOK, map[string]any{"notice": map[string]any{
		"gps":                sides["gps"],
		"strava":             sides["strava"],
		"current_preference": cur,
	}})
}

// DedupResolve POST /api/v1/profile/dedup-resolve {choice, remember} — 首次彈窗確認
func (h *Handler) DedupResolve(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req struct {
		Choice   string `json:"choice"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Choice != "gps" && req.Choice != "strava") {
		respondErr(w, http.StatusBadRequest, "choice 需為 gps 或 strava")
		return
	}
	// remember＝把選擇設為偏好（未來新配對自動照此）；不論如何都標「已提示」（彈窗一次性）
	if req.Remember {
		_, _ = h.db.Exec(r.Context(), `
			INSERT INTO user_profiles (user_id, preferred_data_source, dedup_prompted, updated_at) VALUES ($1,$2,TRUE,NOW())
			ON CONFLICT (user_id) DO UPDATE SET preferred_data_source=$2, dedup_prompted=TRUE, updated_at=NOW()`, userID, req.Choice)
	} else {
		_, _ = h.db.Exec(r.Context(), `
			INSERT INTO user_profiles (user_id, dedup_prompted, updated_at) VALUES ($1,TRUE,NOW())
			ON CONFLICT (user_id) DO UPDATE SET dedup_prompted=TRUE, updated_at=NOW()`, userID)
	}
	// 現有配對依 choice 重解（不記住時偏好不變，但這組仍照玩家選擇）
	reResolveUser(r.Context(), h.db, userID, req.Choice)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}
