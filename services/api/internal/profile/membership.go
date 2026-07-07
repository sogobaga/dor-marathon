package profile

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// --- 帳號專屬編碼 ---

const codeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ" // 去易混淆字（無 0/1/I/O）

func genAccountCode(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	out := make([]byte, n)
	for i := range b {
		out[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(out)
}

// getOrCreateAccountCode：null 時產生唯一 8 碼並寫回
func (h *Handler) getOrCreateAccountCode(ctx context.Context, userID string) (string, error) {
	var code *string
	if err := h.db.QueryRow(ctx, `SELECT account_code FROM users WHERE id=$1`, userID).Scan(&code); err != nil {
		return "", err
	}
	if code != nil && *code != "" {
		return *code, nil
	}
	for i := 0; i < 8; i++ {
		c := genAccountCode(8)
		ct, err := h.db.Exec(ctx, `UPDATE users SET account_code=$1 WHERE id=$2 AND account_code IS NULL`, c, userID)
		if err == nil && ct.RowsAffected() == 1 {
			return c, nil
		}
		// 衝突或併發：重讀一次
		if err == nil {
			if err2 := h.db.QueryRow(ctx, `SELECT account_code FROM users WHERE id=$1`, userID).Scan(&code); err2 == nil && code != nil && *code != "" {
				return *code, nil
			}
		}
	}
	return "", nil
}

// --- 等級 ---

type LevelConfig struct {
	Level       int    `json:"level"`
	Title       string `json:"title"`
	ExpRequired int    `json:"exp_required"`
}

func (h *Handler) levelConfigList(ctx context.Context) ([]LevelConfig, error) {
	rows, err := h.db.Query(ctx, `SELECT level, COALESCE(title,''), exp_required FROM level_config ORDER BY exp_required`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LevelConfig{}
	for rows.Next() {
		var l LevelConfig
		if err := rows.Scan(&l.Level, &l.Title, &l.ExpRequired); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// computeLevel 依 exp 與門檻表推導目前等級、本級門檻、下一級門檻（nil=已達頂級）
func computeLevel(exp int, levels []LevelConfig) (level int, title string, floor int, next *int) {
	level, title, floor = 1, "", 0
	for i := range levels {
		if exp >= levels[i].ExpRequired {
			level, title, floor = levels[i].Level, levels[i].Title, levels[i].ExpRequired
		} else {
			v := levels[i].ExpRequired
			return level, title, floor, &v
		}
	}
	return level, title, floor, nil
}

// --- Dashboard ---

type DashboardInfo struct {
	Name           string     `json:"name"`
	Nickname       string     `json:"nickname"`
	Handle         string     `json:"handle"`
	AvatarURL      string     `json:"avatar_url"`
	AccountCode    string     `json:"account_code"`
	Exp            int        `json:"exp"`
	Dp             int        `json:"dp"` // DP 幣餘額
	Level          int        `json:"level"`
	LevelTitle     string     `json:"level_title"`
	LevelFloor     int        `json:"level_floor"`    // 本級門檻 EXP
	NextLevelExp   *int       `json:"next_level_exp"` // 下一級門檻（null=已頂級）
	IsVIP          bool       `json:"is_vip"`
	VIPExpiresAt   *time.Time `json:"vip_expires_at,omitempty"`
	TotalKm        float64    `json:"total_km"`
	RaceCount      int        `json:"race_count"`      // 報名場數（未取消）
	OngoingCount   int        `json:"ongoing_count"`   // 進行中場數
	CompletedCount int        `json:"completed_count"` // 已完成場數
	FollowingCount int        `json:"following_count"`
	FollowerCount  int        `json:"follower_count"`
	// PersonalEntry 個人任務入口的可見性（後端依系統設定 + 白名單解析後給前端）：
	// hidden 不顯示 / locked 顯示但不能按 / shown 顯示且可按。
	PersonalEntry string `json:"personal_entry"`
}

// GET /api/v1/profile/dashboard
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	code, err := h.getOrCreateAccountCode(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	var d DashboardInfo
	d.AccountCode = code
	var email string
	if err := h.db.QueryRow(r.Context(), `
		SELECT u.name, u.handle, COALESCE(u.avatar_url,''), u.exp, u.dp, u.vip_expires_at,
		       COALESCE((SELECT SUM(distance_km) FROM activities WHERE user_id=u.id AND NOT flagged),0),
		       COALESCE(p.nickname,''),
		       (SELECT COUNT(*) FROM registrations rg WHERE rg.user_id=u.id AND rg.status<>'cancelled'),
		       COALESCE(u.email,'')
		FROM users u LEFT JOIN user_profiles p ON p.user_id=u.id
		WHERE u.id=$1`, userID).
		Scan(&d.Name, &d.Handle, &d.AvatarURL, &d.Exp, &d.Dp, &d.VIPExpiresAt, &d.TotalKm, &d.Nickname, &d.RaceCount, &email); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load dashboard")
		return
	}
	d.PersonalEntry = resolvePersonalEntry(r.Context(), h.db, email, code)
	levels, err := h.levelConfigList(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	d.Level, d.LevelTitle, d.LevelFloor, d.NextLevelExp = computeLevel(d.Exp, levels)
	d.IsVIP = d.VIPExpiresAt != nil && d.VIPExpiresAt.After(time.Now())
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM follows WHERE follower_id=$1`, userID).Scan(&d.FollowingCount)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM follows WHERE followee_id=$1`, userID).Scan(&d.FollowerCount)
	// 進行中（賽事期間內）
	h.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM registrations rg JOIN races r ON r.id=rg.race_id
		WHERE rg.user_id=$1 AND rg.status<>'cancelled' AND NOW() BETWEEN r.start_date AND r.end_date`, userID).Scan(&d.OngoingCount)
	// 已完成（累積里程達分組目標）
	h.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FROM (
			SELECT reg.race_id
			FROM registrations reg
			JOIN races r ON r.id=reg.race_id
			JOIN race_groups g ON g.id=reg.group_id AND g.target_distance_km > 0
			LEFT JOIN activities a ON a.user_id=reg.user_id AND NOT a.flagged
			                      AND a.recorded_at BETWEEN r.start_date AND r.end_date
			WHERE reg.user_id=$1 AND reg.status<>'cancelled'
			GROUP BY reg.race_id, g.target_distance_km
			HAVING COALESCE(SUM(a.distance_km),0) >= g.target_distance_km
		) t`, userID).Scan(&d.CompletedCount)
	respondJSON(w, http.StatusOK, map[string]any{"dashboard": d})
}

// resolvePersonalEntry 依系統設定 personal_entry_state（+ 白名單）解析出「這個使用者」該看到的個人任務入口狀態。
// 回 hidden / locked / shown。白名單留在後端解析，避免整包帳號送到前端外流。
func resolvePersonalEntry(ctx context.Context, db *pgxpool.Pool, email, code string) string {
	switch appsettings.GetString(ctx, db, "personal_entry_state", "hidden") {
	case "open":
		return "shown"
	case "locked":
		return "locked"
	case "whitelist":
		if personalWhitelisted(appsettings.GetString(ctx, db, "personal_entry_whitelist", ""), email, code) {
			return "shown"
		}
		return "hidden"
	default: // hidden 或未設定
		return "hidden"
	}
}

// personalWhitelisted 白名單以換行/逗號/分號/空白分隔，可填帳號編碼（#可省）或 email，大小寫不敏感。
func personalWhitelisted(list, email, code string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	code = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(code), "#"))
	for _, tok := range strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' ' || r == '\t'
	}) {
		t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tok), "#"))
		if t == "" {
			continue
		}
		if (email != "" && t == email) || (code != "" && t == code) {
			return true
		}
	}
	return false
}

// --- 追蹤系統 ---

// POST /api/v1/profile/follow  body: {"user_id":"..."}
func (h *Handler) Follow(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID == "" {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.UserID == userID {
		respondErr(w, http.StatusBadRequest, "不能追蹤自己")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`INSERT INTO follows (follower_id, followee_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		userID, body.UserID); err != nil {
		respondErr(w, http.StatusBadRequest, "追蹤失敗（帳號不存在？）")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"following": true})
}

// DELETE /api/v1/profile/follow/{userID}
func (h *Handler) Unfollow(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	target := chi.URLParam(r, "userID")
	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM follows WHERE follower_id=$1 AND followee_id=$2`, userID, target); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type FollowRow struct {
	UserID      string `json:"user_id"`
	Nickname    string `json:"nickname"`
	AccountCode string `json:"account_code"`
	AvatarURL   string `json:"avatar_url"`
}

// GET /api/v1/profile/follows
func (h *Handler) Follows(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	rows, err := h.db.Query(r.Context(), `
		SELECT u.id::text, COALESCE(NULLIF(p.nickname,''), u.handle), COALESCE(u.account_code,''), COALESCE(u.avatar_url,'')
		FROM follows f
		JOIN users u ON u.id = f.followee_id
		LEFT JOIN user_profiles p ON p.user_id = u.id
		WHERE f.follower_id = $1
		ORDER BY f.created_at DESC`, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	defer rows.Close()
	out := []FollowRow{}
	for rows.Next() {
		var fr FollowRow
		if err := rows.Scan(&fr.UserID, &fr.Nickname, &fr.AccountCode, &fr.AvatarURL); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, fr)
	}
	var followingCount, followerCount int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM follows WHERE follower_id=$1`, userID).Scan(&followingCount)
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM follows WHERE followee_id=$1`, userID).Scan(&followerCount)
	respondJSON(w, http.StatusOK, map[string]any{"following": out, "following_count": followingCount, "follower_count": followerCount})
}

// --- 後台：等級門檻 ---

// GET /api/v1/admin/membership/level-config
func (h *Handler) GetLevelConfig(w http.ResponseWriter, r *http.Request) {
	levels, err := h.levelConfigList(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"levels": levels})
}

// PUT /api/v1/admin/membership/level-config —— 整批取代
func (h *Handler) PutLevelConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Levels []LevelConfig `json:"levels"`
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
	if _, err := tx.Exec(r.Context(), `DELETE FROM level_config`); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	for _, l := range body.Levels {
		if l.Level <= 0 {
			continue
		}
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO level_config (level, title, exp_required) VALUES ($1,NULLIF($2,''),$3)`,
			l.Level, l.Title, l.ExpRequired); err != nil {
			respondErr(w, http.StatusBadRequest, "level config 寫入失敗（檢查 level 是否重複）")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	h.GetLevelConfig(w, r)
}

// --- 後台：EXP 規則 ---

type ExpRules struct {
	PerCollectiveTask int `json:"per_collective_task"` // 全體任務完成
	PerGroupTask      int `json:"per_group_task"`      // 分組任務完成
	PerIndividualTask int `json:"per_individual_task"` // 個人任務完成
	PerKm             int `json:"per_km"`              // 日常每公里
	// DP 平行費率（取得來源同 EXP，獨立設定）
	DpPerCollectiveTask int `json:"dp_per_collective_task"`
	DpPerGroupTask      int `json:"dp_per_group_task"`
	DpPerIndividualTask int `json:"dp_per_individual_task"`
	DpPerKm             int `json:"dp_per_km"`
	// 里程獎勵風控：單趟上限（整公里）＋防造假最快合理配速（秒/公里）
	MileageCapKm    int `json:"mileage_cap_km"`
	MileageMinPaceS int `json:"mileage_min_pace_s"`
}

// GET /api/v1/admin/membership/exp-rules
func (h *Handler) GetExpRules(w http.ResponseWriter, r *http.Request) {
	var e ExpRules
	if err := h.db.QueryRow(r.Context(),
		`SELECT per_collective_task, per_group_task, per_individual_task, per_km,
		        dp_per_collective_task, dp_per_group_task, dp_per_individual_task, dp_per_km,
		        mileage_cap_km, mileage_min_pace_s
		 FROM exp_rules WHERE id=TRUE`).
		Scan(&e.PerCollectiveTask, &e.PerGroupTask, &e.PerIndividualTask, &e.PerKm,
			&e.DpPerCollectiveTask, &e.DpPerGroupTask, &e.DpPerIndividualTask, &e.DpPerKm,
			&e.MileageCapKm, &e.MileageMinPaceS); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"exp_rules": e})
}

// PUT /api/v1/admin/membership/exp-rules
func (h *Handler) PutExpRules(w http.ResponseWriter, r *http.Request) {
	var e ExpRules
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`UPDATE exp_rules SET per_collective_task=$1, per_group_task=$2, per_individual_task=$3, per_km=$4,
		        dp_per_collective_task=$5, dp_per_group_task=$6, dp_per_individual_task=$7, dp_per_km=$8,
		        mileage_cap_km=$9, mileage_min_pace_s=$10 WHERE id=TRUE`,
		e.PerCollectiveTask, e.PerGroupTask, e.PerIndividualTask, e.PerKm,
		e.DpPerCollectiveTask, e.DpPerGroupTask, e.DpPerIndividualTask, e.DpPerKm,
		e.MileageCapKm, e.MileageMinPaceS); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"exp_rules": e})
}

// --- 後台：會員 VIP / EXP ---

// PUT /api/v1/admin/members/{userID}/vip  body: {"vip_expires_at":"2026-12-31T00:00:00Z"} 空字串=清除
func (h *Handler) AdminSetVIP(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var body struct {
		VIPExpiresAt string `json:"vip_expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ct, err := h.db.Exec(r.Context(),
		`UPDATE users SET vip_expires_at=NULLIF($1,'')::timestamptz WHERE id=$2`, body.VIPExpiresAt, userID)
	if err != nil {
		respondErr(w, http.StatusBadRequest, "日期格式錯誤或更新失敗")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "member not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"vip_expires_at": body.VIPExpiresAt})
}

// PUT /api/v1/admin/members/{userID}/exp  body: {"set":300} 或 {"delta":50}
func (h *Handler) AdminSetExp(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var body struct {
		Set   *int `json:"set"`
		Delta *int `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	var newExp int
	var err error
	if body.Set != nil {
		v := *body.Set
		if v < 0 {
			v = 0
		}
		err = h.db.QueryRow(r.Context(), `UPDATE users SET exp=$1 WHERE id=$2 RETURNING exp`, v, userID).Scan(&newExp)
	} else if body.Delta != nil {
		err = h.db.QueryRow(r.Context(),
			`UPDATE users SET exp=GREATEST(0, exp + $1) WHERE id=$2 RETURNING exp`, *body.Delta, userID).Scan(&newExp)
	} else {
		respondErr(w, http.StatusBadRequest, "需提供 set 或 delta")
		return
	}
	if err != nil {
		respondErr(w, http.StatusNotFound, "member not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"exp": newExp})
}
