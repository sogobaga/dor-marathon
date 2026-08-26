// Profile 模組：個人資料、完賽紀錄、成就統計、後台會員管理
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/race"
	"github.com/dor/api/internal/realtime"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	db *pgxpool.Pool
	rt *realtime.Manager
}

func NewHandler(db *pgxpool.Pool, rt *realtime.Manager) *Handler {
	return &Handler{db: db, rt: rt}
}

// Router 個人資料路由（掛載在 /api/v1/profile，需登入）
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.GetMe)
	r.Put("/", h.UpdateMe)
	r.Get("/dashboard", h.Dashboard)
	r.Post("/follow", h.Follow)
	r.Delete("/follow/{userID}", h.Unfollow)
	r.Get("/follows", h.Follows)
	r.Get("/recommendations/{raceID}", h.RaceRecommendations)
	r.Get("/mileage-exp", h.GetMileageExp)
	r.Get("/mileage-config", h.MileageConfig)
	r.Post("/mileage-exp/seen", h.MarkMileageSeen)
	r.Post("/data-source", h.SetDataSource) // 偏好資料來源（跨來源去重）
	r.Get("/dedup-notice", h.DedupNotice)   // 首次去重彈窗
	r.Post("/dedup-resolve", h.DedupResolve)
	r.Get("/records", h.Records)
	r.Get("/stats", h.Stats)
	r.Get("/registrations", h.Registrations)
	r.Get("/orders/{orderID}", h.OrderDetail)
	r.Get("/vip/pricing", h.VipPricing)                     // VIP 方案定價（依促銷資格）
	r.Post("/vip/cancel", h.CancelVipSub)                   // 取消訂閱（不再續扣，權益至到期日）
	r.Post("/trial-notice-shown", h.MarkTrialNoticeShown)   // 標記試用到期彈窗已顯示（只跳一次）
	r.Get("/titles", h.Titles)                              // 稱號圖鑑
	r.Post("/titles/display", h.SetDisplayedTitle)          // 設定展示中稱號
	r.Post("/titles/seen", h.MarkTitlesSeen)                // 標記新解鎖稱號已看過
	r.Get("/achievements", h.GetAchievements)               // 成就總覽
	r.Get("/achievements/calendar", h.AchievementsCalendar) // 月曆里程
	r.Get("/achievements/day", h.AchievementsDay)           // 單日明細
	r.Post("/referral", h.GetOrCreateReferral)              // 產生/取得專屬推薦碼（需 total_km>=10）
	r.Get("/referral", h.GetReferral)                       // 只查現況（不產生）；供頁面重掛載回顯既有連結
	r.Get("/rewards", h.Rewards)                            // 活動獎勵系統 P3：玩家活動獎勵錢包（只回本人序號類獎勵）
	r.Post("/rewards/{id}/use", h.MarkRewardUsed)           // 標記某筆活動獎勵已使用（只能改自己的，冪等）
	return r
}

// AdminMembersRouter 後台會員管理路由（掛載在 /api/v1/admin/members，需 admin）
func (h *Handler) AdminMembersRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListMembers)
	r.Get("/{userID}", h.AdminGetMember)
	r.Put("/{userID}/team-group-permission", h.AdminSetTeamGroupPermission)
	r.Put("/{userID}/vip", h.AdminSetVIP)
	r.Put("/{userID}/exp", h.AdminSetExp)
	r.Put("/{userID}/gp", h.AdminSetGp)
	return r
}

// MembershipAdminRouter 後台等級/EXP 設定（掛載在 /api/v1/admin/membership，需 admin）
func (h *Handler) MembershipAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/level-config", h.GetLevelConfig)
	r.Put("/level-config", h.PutLevelConfig)
	r.Get("/exp-rules", h.GetExpRules)
	r.Put("/exp-rules", h.PutExpRules)
	r.Get("/athlete-config", h.GetAthleteConfig)
	r.Put("/athlete-config", h.PutAthleteConfig)
	return r
}

// Profile 個人資訊（user_profiles + users.email）
type Profile struct {
	UserID              string           `json:"user_id"`
	Email               string           `json:"email"`
	Name                string           `json:"name"`       // 顯示名稱（users.name）
	AvatarURL           string           `json:"avatar_url"` // users.avatar_url
	RealName            string           `json:"real_name"`
	Nickname            string           `json:"nickname"`
	Phone               string           `json:"phone"`
	Address             string           `json:"address"`
	Birthday            string           `json:"birthday"`              // YYYY-MM-DD，空=未填
	Gender              string           `json:"gender"`                // male|female|other|空
	PreferredDataSource string           `json:"preferred_data_source"` // gps|strava（跨來源去重偏好；預設 gps）
	Invoice             race.InvoiceInfo `json:"invoice"`               // 上次報名填的發票資訊，供前端預填；從未報名過則全空
}

// GET /api/v1/profile — 取得自己的個人資訊（無資料則回空白可編輯結構）
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	p, err := h.fetchProfile(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to fetch profile")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"profile": p})
}

// PUT /api/v1/profile — 更新（upsert）自己的個人資訊
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	var req Profile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Gender != "" && req.Gender != "male" && req.Gender != "female" && req.Gender != "other" {
		respondErr(w, http.StatusBadRequest, "invalid gender")
		return
	}
	if _, err := h.db.Exec(r.Context(), `
		INSERT INTO user_profiles (user_id, real_name, nickname, phone, address, birthday, gender, updated_at)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,'')::date, NULLIF($7,''), NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			real_name=EXCLUDED.real_name, nickname=EXCLUDED.nickname, phone=EXCLUDED.phone,
			address=EXCLUDED.address, birthday=EXCLUDED.birthday, gender=EXCLUDED.gender, updated_at=NOW()`,
		userID, req.RealName, req.Nickname, req.Phone, req.Address, req.Birthday, req.Gender,
	); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save profile")
		return
	}
	// 顯示名稱（非空才改，避免清空）與頭像（空字串=移除）
	if _, err := h.db.Exec(r.Context(),
		`UPDATE users SET name=COALESCE(NULLIF($2,''), name), avatar_url=NULLIF($3,''), updated_at=NOW() WHERE id=$1`,
		userID, req.Name, req.AvatarURL,
	); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save profile")
		return
	}
	p, err := h.fetchProfile(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "saved but reload failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"profile": p})
}

func (h *Handler) fetchProfile(ctx context.Context, userID string) (*Profile, error) {
	p := &Profile{UserID: userID, PreferredDataSource: "gps"}
	var birthday *time.Time
	err := h.db.QueryRow(ctx, `
		SELECT u.email, u.name, COALESCE(u.avatar_url,''),
		       COALESCE(p.real_name,''), COALESCE(p.nickname,''), COALESCE(p.phone,''),
		       COALESCE(p.address,''), p.birthday, COALESCE(p.gender,''), COALESCE(p.preferred_data_source,'gps'),
		       COALESCE(p.inv_buyer_type,''), COALESCE(p.inv_tax_id,''), COALESCE(p.inv_title,''),
		       COALESCE(p.inv_carrier_type,''), COALESCE(p.inv_carrier_id,''), COALESCE(p.inv_love_code,'')
		FROM users u LEFT JOIN user_profiles p ON p.user_id = u.id
		WHERE u.id = $1`, userID).
		Scan(&p.Email, &p.Name, &p.AvatarURL, &p.RealName, &p.Nickname, &p.Phone, &p.Address, &birthday, &p.Gender, &p.PreferredDataSource,
			&p.Invoice.BuyerType, &p.Invoice.TaxID, &p.Invoice.Title, &p.Invoice.CarrierType, &p.Invoice.CarrierID, &p.Invoice.LoveCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if birthday != nil {
		p.Birthday = birthday.Format("2006-01-02")
	}
	return p, nil
}

// MyRegistration 使用者自己的報名紀錄（含賽事/分組/訂單狀態）
type MyRegistration struct {
	RegistrationID string    `json:"registration_id"`
	RaceID         string    `json:"race_id"`
	RaceTitle      string    `json:"race_title"`
	RaceSlug       string    `json:"race_slug"`
	GroupName      string    `json:"group_name"`
	GroupRevealed  bool      `json:"group_revealed"`
	Status         string    `json:"status"` // pending|paid|cancelled
	CreatedAt      time.Time `json:"created_at"`
	OrderID        string    `json:"order_id,omitempty"`
	OrderTotal     int       `json:"order_total_cents"`
	OrderStatus    string    `json:"order_status,omitempty"`

	// 取消報名（見 race 套件 ComputeCancellation/ResolveCancellationPolicy）
	CanCancel            bool   `json:"can_cancel"`
	CancelBlockedReason  string `json:"cancel_blocked_reason"`
	RefundRatio          int    `json:"refund_ratio"`
	EstimatedRefundCents int    `json:"estimated_refund_cents"`
	CancelRequestStatus  string `json:"cancel_request_status"` // 無申請時為空字串；否則 pending|processing|approved|rejected
	// （processing 是後台核准流程中的短暫過渡狀態，正常情況幾乎看不到——見 race.beginCancelRequestProcessing）
}

// GET /api/v1/profile/registrations — 我的報名紀錄
func (h *Handler) Registrations(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT reg.id, reg.race_id, rc.title, rc.slug, COALESCE(g.name,''),
		       reg.group_revealed, reg.status, reg.created_at,
		       COALESCE(o.id::text,''), COALESCE(o.total_cents,0), COALESCE(o.status,''),
		       rc.start_date, rc.config, COALESCE(cr.status,'')
		FROM registrations reg
		JOIN races rc ON rc.id = reg.race_id
		LEFT JOIN race_groups g ON g.id = reg.group_id
		LEFT JOIN orders o ON o.registration_id = reg.id
		LEFT JOIN LATERAL (
			SELECT status FROM registration_cancel_requests
			WHERE registration_id = reg.id
			ORDER BY created_at DESC LIMIT 1
		) cr ON true
		WHERE reg.user_id = $1
		ORDER BY reg.created_at DESC`, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list registrations")
		return
	}
	defer rows.Close()

	// 系統預設取消政策只需查一次（不因每筆報名重查），個別賽事覆寫在迴圈內從各自的 config 解析。
	sysDefaultPolicy := appsettings.GetString(r.Context(), h.db, "cancellation_policy", "")

	out := []MyRegistration{}
	for rows.Next() {
		var m MyRegistration
		var startDate time.Time
		var configBytes []byte
		var crStatus string
		if err := rows.Scan(&m.RegistrationID, &m.RaceID, &m.RaceTitle, &m.RaceSlug, &m.GroupName,
			&m.GroupRevealed, &m.Status, &m.CreatedAt, &m.OrderID, &m.OrderTotal, &m.OrderStatus,
			&startDate, &configBytes, &crStatus); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		m.CancelRequestStatus = crStatus

		var cfg race.RaceConfig
		_ = json.Unmarshal(configBytes, &cfg) // 壞掉的 config 視同沒有覆寫
		// EffectiveCancellationPolicy：不退費賽事（config.refund_disabled）tiers 清空 → 預估退費 0 元，
		// 與 CreateCancelRequest 申請當下的快照計算走同一顆，兩邊不會兜不起來。
		policy := race.EffectiveCancellationPolicy(&cfg, sysDefaultPolicy)

		// 未付款訂單沒有錢可退：即使政策算出 ratio>0，估算退費金額也要是 0（與 CreateCancelRequest 一致）。
		effectiveAmount := 0
		if m.OrderStatus == "paid" {
			effectiveAmount = m.OrderTotal
		}
		calc := race.ComputeCancellation(startDate, time.Now(), effectiveAmount, policy)
		m.RefundRatio = calc.Ratio
		m.EstimatedRefundCents = calc.RefundAmountCents

		switch {
		case m.Status != "paid" && m.Status != "pending":
			m.CanCancel = false
			m.CancelBlockedReason = "此報名狀態無法申請取消"
		case crStatus == "pending" || crStatus == "processing":
			// processing 是後台核准流程的短暫過渡狀態（見 race.beginCancelRequestProcessing），與
			// pending 一樣視為「已有申請正在處理中」——這裡的判斷要跟 getRegistrationForCancel 的
			// HasPending 查詢及 DB 層 uq_cancel_requests_pending_per_registration 保持一致，
			// 否則前台會顯示「可以再次申請取消」，但實際送出時會被 DB 擋下（ErrCancelRequestPending）。
			m.CanCancel = false
			m.CancelBlockedReason = "已有取消申請正在審核中"
		case !calc.CanCancel:
			m.CanCancel = false
			m.CancelBlockedReason = calc.BlockedReason
		default:
			m.CanCancel = true
		}

		out = append(out, m)
	}
	respondJSON(w, http.StatusOK, map[string]any{"registrations": out, "count": len(out)})
}

// MyOrderItem 訂單明細單筆
type MyOrderItem struct {
	ItemType      string `json:"item_type"`
	AddonName     string `json:"addon_name,omitempty"`
	Qty           int    `json:"qty"`
	SubtotalCents int    `json:"subtotal_cents"`
}

// MyOrder 我的訂單（繳費頁面用）
type MyOrder struct {
	ID         string        `json:"id"`
	RaceTitle  string        `json:"race_title"`
	TotalCents int           `json:"total_cents"`
	Status     string        `json:"status"`
	PaymentRef string        `json:"payment_ref,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	Items      []MyOrderItem `json:"items"`
}

// GET /api/v1/profile/orders/{orderID} — 我的訂單明細（繳費資訊；僅本人訂單）
func (h *Handler) OrderDetail(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	orderID := chi.URLParam(r, "orderID")

	var o MyOrder
	// LEFT JOIN：VIP 訂閱訂單無賽事（orders.race_id 可為 NULL，見 migration 132），rc.title COALESCE 成空字串
	// 由前端 race_title 為空時顯示「VIP 訂閱」（見 ProfileScreen.tsx）。
	err := h.db.QueryRow(r.Context(), `
		SELECT o.id, COALESCE(rc.title,''), o.total_cents, o.status, COALESCE(o.payment_ref,''), o.created_at
		FROM orders o LEFT JOIN races rc ON rc.id = o.race_id
		WHERE o.id = $1 AND o.user_id = $2`, orderID, userID).
		Scan(&o.ID, &o.RaceTitle, &o.TotalCents, &o.Status, &o.PaymentRef, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "order not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get order")
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT oi.item_type, COALESCE(a.name,''), oi.qty, oi.subtotal_cents
		FROM order_items oi LEFT JOIN race_addons a ON a.id = oi.addon_id
		WHERE oi.order_id = $1 ORDER BY oi.item_type`, orderID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get items")
		return
	}
	defer rows.Close()
	o.Items = []MyOrderItem{}
	for rows.Next() {
		var it MyOrderItem
		if err := rows.Scan(&it.ItemType, &it.AddonName, &it.Qty, &it.SubtotalCents); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		o.Items = append(o.Items, it)
	}
	respondJSON(w, http.StatusOK, map[string]any{"order": o})
}

// MemberSummary 後台會員列表單筆
type MemberSummary struct {
	ID                 string     `json:"id"`
	Email              string     `json:"email"`
	Handle             string     `json:"handle"`
	Name               string     `json:"name"`
	Role               string     `json:"role"`
	RealName           string     `json:"real_name"`
	Phone              string     `json:"phone"`
	Gender             string     `json:"gender"`
	TotalKm            float64    `json:"total_km"`
	CanCreateTeamGroup bool       `json:"can_create_team_group"`
	CreatedAt          time.Time  `json:"created_at"`
	IsVIP              bool       `json:"is_vip"`
	VIPExpiresAt       *time.Time `json:"vip_expires_at,omitempty"`
	VipPlan            string     `json:"vip_plan"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"` // 最近一次登入（password/google/register），見 user_login_logs
	// 註冊來源歸因（見 migrations/147_signup_attribution.sql／internal/attribution）：歷史會員
	// （本 migration 上線前註冊）沒有這筆列，皆回空字串——前端顯示「—」。
	SignupSource  string `json:"signup_source"`             // referral|facebook|instagram|line|google|threads|tiktok|other|direct|""(無資料)
	SignupRefName string `json:"signup_ref_name,omitempty"` // 推薦人暱稱；非 referral 來源或推薦人已被刪除則為空
}

// MemberDetail 後台會員詳情（含完整個資與報名數）
type MemberDetail struct {
	MemberSummary
	Nickname     string             `json:"nickname"`
	Address      string             `json:"address"`
	Birthday     string             `json:"birthday"`
	RaceCount    int                `json:"race_count"`
	Exp          int                `json:"exp"`
	Gp           int                `json:"gp"` // GP 幣餘額（環台大富翁）
	Level        int                `json:"level"`
	LevelTitle   string             `json:"level_title"`
	IsVIP        bool               `json:"is_vip"`
	VIPExpiresAt *time.Time         `json:"vip_expires_at,omitempty"`
	Athlete      AthleteStats       `json:"athlete"`               // 選手分級（後台限定顯示）
	Attribution  *SignupAttribution `json:"attribution,omitempty"` // 完整來源歸因；歷史會員無資料則為 nil
}

// SignupAttribution 後台會員詳情的完整來源歸因（見 user_signup_attribution）。
type SignupAttribution struct {
	Source      string         `json:"source"`
	RefUserID   string         `json:"ref_user_id,omitempty"`
	RefName     string         `json:"ref_name,omitempty"` // 推薦人暱稱（handle），已刪除則空
	UTM         map[string]any `json:"utm,omitempty"`
	LandingURL  string         `json:"landing_url,omitempty"`
	ReferrerURL string         `json:"referrer_url,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// GET /api/v1/admin/members?q=&limit=&offset=&source=
func (h *Handler) AdminListMembers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	source := r.URL.Query().Get("source") // 選填：referral|facebook|instagram|line|google|threads|tiktok|other|direct|none（none=無歸因資料的歷史會員）
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	like := "%" + q + "%"
	rows, err := h.db.Query(r.Context(), `
		SELECT u.id, u.email, u.handle, u.name, u.role, u.total_km, u.created_at,
		       COALESCE(p.real_name,''), COALESCE(p.phone,''), COALESCE(p.gender,''), u.can_create_team_group,
		       u.vip_expires_at, COALESCE(u.vip_plan,''), u.last_login_at,
		       COALESCE(a.source,''), COALESCE(ref.handle,'')
		FROM users u
		LEFT JOIN user_profiles p ON p.user_id = u.id
		LEFT JOIN user_signup_attribution a ON a.user_id = u.id
		LEFT JOIN users ref ON ref.id = a.ref_user_id
		WHERE ($1 = '' OR u.email ILIKE $2 OR u.name ILIKE $2
		       OR COALESCE(p.real_name,'') ILIKE $2 OR COALESCE(p.phone,'') ILIKE $2)
		  AND ($5 = '' OR ($5 = 'none' AND a.source IS NULL) OR a.source = $5)
		ORDER BY u.created_at DESC
		LIMIT $3 OFFSET $4`, q, like, limit, offset, source)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	defer rows.Close()

	members := []MemberSummary{}
	for rows.Next() {
		var m MemberSummary
		if err := rows.Scan(&m.ID, &m.Email, &m.Handle, &m.Name, &m.Role, &m.TotalKm, &m.CreatedAt,
			&m.RealName, &m.Phone, &m.Gender, &m.CanCreateTeamGroup,
			&m.VIPExpiresAt, &m.VipPlan, &m.LastLoginAt,
			&m.SignupSource, &m.SignupRefName); err != nil {
			respondErr(w, http.StatusInternalServerError, "scan failed")
			return
		}
		m.IsVIP = m.VIPExpiresAt != nil && m.VIPExpiresAt.After(time.Now())
		if m.SignupSource != "referral" {
			m.SignupRefName = "" // 只有 referral 來源才顯示推薦人（JOIN 條件本身已保證，這裡保底）
		}
		members = append(members, m)
	}
	respondJSON(w, http.StatusOK, map[string]any{"members": members, "count": len(members)})
}

// GET /api/v1/admin/members/:userID
func (h *Handler) AdminGetMember(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var m MemberDetail
	var birthday *time.Time
	err := h.db.QueryRow(r.Context(), `
		SELECT u.id, u.email, u.handle, u.name, u.role, u.total_km, u.created_at, u.can_create_team_group,
		       COALESCE(p.real_name,''), COALESCE(p.nickname,''), COALESCE(p.phone,''),
		       COALESCE(p.address,''), p.birthday, COALESCE(p.gender,''),
		       (SELECT COUNT(*) FROM registrations rg WHERE rg.user_id = u.id),
		       u.exp, u.gp, u.vip_expires_at
		FROM users u LEFT JOIN user_profiles p ON p.user_id = u.id
		WHERE u.id = $1`, userID).
		Scan(&m.ID, &m.Email, &m.Handle, &m.Name, &m.Role, &m.TotalKm, &m.CreatedAt, &m.CanCreateTeamGroup,
			&m.RealName, &m.Nickname, &m.Phone, &m.Address, &birthday, &m.Gender, &m.RaceCount,
			&m.Exp, &m.Gp, &m.VIPExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		respondErr(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to fetch member")
		return
	}
	if birthday != nil {
		m.Birthday = birthday.Format("2006-01-02")
	}
	if levels, err := h.levelConfigList(r.Context()); err == nil {
		m.Level, m.LevelTitle, _, _ = computeLevel(m.Exp, levels)
	}
	m.IsVIP = m.VIPExpiresAt != nil && m.VIPExpiresAt.After(time.Now())
	if attr, err := h.signupAttributionFor(r.Context(), userID); err == nil && attr != nil {
		m.Attribution = attr
		m.SignupSource = attr.Source
		m.SignupRefName = attr.RefName
	}
	if a, err := h.athleteStatsFor(r.Context(), userID); err == nil {
		m.Athlete = a
	}
	respondJSON(w, http.StatusOK, map[string]any{"member": m})
}

// signupAttributionFor 查詢單一會員的完整來源歸因（user_signup_attribution，見 migrations/
// 147_signup_attribution.sql），供 AdminGetMember 顯示。查無資料（本 migration 上線前註冊的
// 歷史會員）回 (nil, nil)，不是錯誤。ref_name 為推薦人 handle（非 referral 來源或推薦人帳號
// 已被刪除則空字串）。
func (h *Handler) signupAttributionFor(ctx context.Context, userID string) (*SignupAttribution, error) {
	var a SignupAttribution
	var refUserID *string
	var refHandle string
	var utmBytes []byte
	var landingURL, referrerURL *string
	err := h.db.QueryRow(ctx, `
		SELECT a.source, a.ref_user_id, COALESCE(ref.handle,''), a.utm, a.landing_url, a.referrer_url, a.created_at
		FROM user_signup_attribution a
		LEFT JOIN users ref ON ref.id = a.ref_user_id
		WHERE a.user_id = $1`, userID).
		Scan(&a.Source, &refUserID, &refHandle, &utmBytes, &landingURL, &referrerURL, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetch signup attribution: %w", err)
	}
	if refUserID != nil {
		a.RefUserID = *refUserID
		a.RefName = refHandle
	}
	if landingURL != nil {
		a.LandingURL = *landingURL
	}
	if referrerURL != nil {
		a.ReferrerURL = *referrerURL
	}
	if len(utmBytes) > 0 {
		if err := json.Unmarshal(utmBytes, &a.UTM); err != nil {
			return nil, fmt.Errorf("parse signup attribution utm: %w", err)
		}
	}
	return &a, nil
}

// PUT /api/v1/admin/members/:userID/team-group-permission  {"allowed":true}
func (h *Handler) AdminSetTeamGroupPermission(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var body struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ct, err := h.db.Exec(r.Context(),
		`UPDATE users SET can_create_team_group=$1 WHERE id=$2`, body.Allowed, userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update permission")
		return
	}
	if ct.RowsAffected() == 0 {
		respondErr(w, http.StatusNotFound, "member not found")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"can_create_team_group": body.Allowed})
}

// RaceRecord 個人完賽紀錄
type RaceRecord struct {
	RaceID    string    `json:"race_id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Distance  int       `json:"distance"` // 報名組別
	TotalKm   float64   `json:"total_km"` // 實際完成里程
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	Faction   string    `json:"faction"`
	Status    string    `json:"status"` // completed | dnf（未完賽）
	Rank      int       `json:"rank"`   // 最終排名（從 DB activities 算）
}

type Stats struct {
	TotalKm    float64 `json:"total_km"`
	TotalRaces int     `json:"total_races"`
	Rescues    int     `json:"rescues"`
	BestPaceS  int     `json:"best_pace_s"` // 最佳配速（秒/公里）
}

// GET /api/v1/profile/records
func (h *Handler) Records(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	records, err := h.fetchRecords(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to fetch records")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"records": records,
		"count":   len(records),
	})
}

// GET /api/v1/profile/stats
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)

	stats, err := h.fetchStats(r.Context(), userID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to fetch stats")
		return
	}
	respondJSON(w, http.StatusOK, stats)
}

func (h *Handler) fetchRecords(ctx context.Context, userID string) ([]*RaceRecord, error) {
	rows, err := h.db.Query(ctx, `
		SELECT
		    r.id, r.slug, r.title, r.start_date, r.end_date,
		    reg.distance, COALESCE(reg.faction,'') as faction,
		    COALESCE(SUM(a.distance_km), 0) as total_km,
		    CASE WHEN COALESCE(SUM(a.distance_km),0) >= reg.distance THEN 'completed' ELSE 'dnf' END as status
		FROM registrations reg
		JOIN races r ON r.id = reg.race_id
		LEFT JOIN activities a ON a.user_id = reg.user_id AND NOT a.flagged
		                      AND a.recorded_at BETWEEN r.start_date AND r.end_date
		                      AND (a.source IS NULL OR (r.external_data AND a.source <> 'strava'))
		WHERE reg.user_id = $1 AND reg.status = 'paid'
		GROUP BY r.id, r.slug, r.title, r.start_date, r.end_date, reg.distance, reg.faction
		ORDER BY r.end_date DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []*RaceRecord
	for rows.Next() {
		rec := &RaceRecord{}
		if err := rows.Scan(
			&rec.RaceID, &rec.Slug, &rec.Title,
			&rec.StartDate, &rec.EndDate,
			&rec.Distance, &rec.Faction,
			&rec.TotalKm, &rec.Status,
		); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (h *Handler) fetchStats(ctx context.Context, userID string) (*Stats, error) {
	s := &Stats{}
	err := h.db.QueryRow(ctx, `
		SELECT
		    u.total_km,
		    COUNT(DISTINCT reg.race_id)                         as total_races,
		    COALESCE(SUM(mc.rescue_count), 0)                   as rescues,
		    COALESCE(MIN(a.avg_pace_s), 0)                      as best_pace_s
		FROM users u
		LEFT JOIN registrations reg ON reg.user_id = u.id AND reg.status = 'paid'
		LEFT JOIN mission_completions mc ON mc.user_id = u.id
		LEFT JOIN activities a ON a.user_id = u.id AND NOT a.flagged
		WHERE u.id = $1
		GROUP BY u.total_km
	`, userID).Scan(&s.TotalKm, &s.TotalRaces, &s.Rescues, &s.BestPaceS)
	if err != nil {
		return &Stats{}, nil // 新用戶沒有資料時回傳空統計
	}
	return s, nil
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
