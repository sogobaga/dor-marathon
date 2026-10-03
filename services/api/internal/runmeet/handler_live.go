package runmeet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/middleware"
	"github.com/dor/api/internal/runmeet/live"
)

// handler_live.go：團練同步跑（Group Run Live）的 DB 面——POST /run-meets/{id}/live/start 與詳情 DTO 的
// live 區塊。契約：docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §2／§3.1／§5。
//
// ⚠️ 分工：熱路徑（/pos、/leave）在 internal/runmeet/live，**沒有任何 DB 存取**；碰 PostgreSQL 的
// 只有這支 /live/start（開跑時一次，6 次/分/人限流）與詳情 DTO。
// ⚠️ 不記 log：request body 與座標不得出現在任何 log（本檔不 import 任何 logger）。

// --- 入口閘門（live 專屬，與 runmeet_entry_* 是兩道獨立閘門；/live/start 掛在 Router() 內，requireEntry 照走）---

// liveEntryFrom 團練同步跑入口 hidden|locked|shown（純函式，可單元測試）。
//
//	hidden            → 對所有人（含超管）關閉。hidden 同時是緊急關閉：後台寫入時會 SET 全域 kill 旗標
//	                    （見 registerLiveKillSwitch），kill 對超管一視同仁；若入口判定讓超管繞過 hidden，
//	                    超管會看到可按的按鈕、按下去卻得到 410 killed。
//	超管（其餘狀態）   → 恆 shown（超管恆可用，契約 §0／§2）
//	其餘              → 沿用 entryFrom(state, whitelist, email, code)（locked 在前端是顯示但不可按，
//	                    後端一樣不放行——只有 "shown" 放行）
func liveEntryFrom(state, whitelist, email, code string, isSuperAdmin bool) string {
	if strings.TrimSpace(state) == "hidden" {
		return "hidden"
	}
	if isSuperAdmin {
		return "shown"
	}
	return entryFrom(state, whitelist, email, code)
}

// ResolveLiveEntry 團練同步跑入口可見性（hidden|locked|shown）。無列時入口狀態預設 whitelist、白名單為空
// （＝只有超管）；驗收後由後台改 open。
func ResolveLiveEntry(ctx context.Context, db *pgxpool.Pool, email, code string, isSuperAdmin bool) string {
	s := loadLiveSettings(ctx, db)
	return liveEntryFrom(s.EntryState, s.Whitelist, email, code, isSuperAdmin)
}

// loadLiveSettings 一次讀齊同步跑設定（appsettings 有 60 秒行程內快取；無列用程式預設，不需 migration）。
func loadLiveSettings(ctx context.Context, db *pgxpool.Pool) live.Settings {
	return live.Settings{
		EntryState:   appsettings.GetString(ctx, db, live.EntryStateKey, live.DefEntryState),
		Whitelist:    appsettings.GetString(ctx, db, live.EntryWhitelistKey, live.DefEntryWhitelist),
		Max:          appsettings.GetInt(ctx, db, live.MaxKey, live.DefMax),
		PreMinutes:   appsettings.GetInt(ctx, db, live.PreMinutesKey, live.DefPreMinutes),
		DefaultHours: appsettings.GetInt(ctx, db, live.DefaultHoursKey, live.DefDefaultHours),
		GraceMinutes: appsettings.GetInt(ctx, db, live.GraceMinutesKey, live.DefGraceMinutes),
	}.Normalize()
}

// --- requireEntry 已查過的使用者旗標：放進 request context，避免同一請求再查一次 users ---

type entryFlagsKey struct{}

type entryFlagsVal struct {
	email, code    string
	isSuper, isVIP bool
}

func withEntryFlags(ctx context.Context, email, code string, isSuper, isVIP bool) context.Context {
	return context.WithValue(ctx, entryFlagsKey{}, entryFlagsVal{email, code, isSuper, isVIP})
}

func entryFlagsFrom(ctx context.Context) (entryFlagsVal, bool) {
	v, ok := ctx.Value(entryFlagsKey{}).(entryFlagsVal)
	return v, ok
}

// --- DB 介面（讓 /live/start 可在沒有 PostgreSQL 的單元測試裡用 fake 驗證）---

// liveMeet /live/start 需要的團練欄位。
type liveMeet struct {
	ID         string
	OwnerID    string
	Title      string
	Status     string
	MeetAt     time.Time
	EndsAt     *time.Time
	NoLocation bool
	Lat, Lng   *float64
}

// liveStartRepo /live/start 的資料來源。真實實作 pgLiveRepo；測試用 fake。
type liveStartRepo interface {
	// UserFlags 入口閘門用的使用者屬性（email／帳號編碼／是否超管／是否 VIP）。
	UserFlags(ctx context.Context, uid string) (email, code string, isSuper, isVIP bool, err error)
	// LiveSettings 已 Normalize 的同步跑設定。
	LiveSettings(ctx context.Context) live.Settings
	// MeetForLive 讀團練；不存在／已刪除／後台下架一律回 errNotFound（hidden_by_owner 不影響 joined 成員）。
	MeetForLive(ctx context.Context, meetID string) (liveMeet, error)
	// MemberStatus run_meet_members.status；沒有這一列回 ""。
	MemberStatus(ctx context.Context, meetID, uid string) (string, error)
	// DisplayName 原始 name 與 handle（尚未消毒；name 空字串代表沒設定）。
	DisplayName(ctx context.Context, uid string) (name, handle string, err error)
	// InsertConsentAudit 寫一列 audit_logs（action=runmeet_live_consent）。meta 不得含座標。
	InsertConsentAudit(ctx context.Context, uid, meetID string, consentV int, presenceOnly bool, ip string) error
}

type pgLiveRepo struct {
	db   *pgxpool.Pool
	repo *Repository
}

func (p *pgLiveRepo) UserFlags(ctx context.Context, uid string) (string, string, bool, bool, error) {
	return p.repo.UserFlags(ctx, uid)
}

func (p *pgLiveRepo) LiveSettings(ctx context.Context) live.Settings {
	return loadLiveSettings(ctx, p.db)
}

func (p *pgLiveRepo) MeetForLive(ctx context.Context, meetID string) (liveMeet, error) {
	var m liveMeet
	err := p.db.QueryRow(ctx, `
		SELECT id, owner_id, title, status, meet_at, ends_at, no_location, lat, lng
		  FROM run_meets
		 WHERE id=$1 AND deleted_at IS NULL AND hidden_by_admin = FALSE`, meetID).
		Scan(&m.ID, &m.OwnerID, &m.Title, &m.Status, &m.MeetAt, &m.EndsAt, &m.NoLocation, &m.Lat, &m.Lng)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errNotFound
	}
	return m, err
}

func (p *pgLiveRepo) MemberStatus(ctx context.Context, meetID, uid string) (string, error) {
	var st string
	err := p.db.QueryRow(ctx,
		`SELECT status FROM run_meet_members WHERE meet_id=$1 AND user_id=$2`, meetID, uid).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return st, err
}

// DisplayName 回原始 name 與 handle，由 live.SanitizeDisplayName 決定最後顯示什麼：
// COALESCE(NULLIF(u.name,”), u.handle)（display-name-convention：禁讀個資暱稱），且 name 消毒後若變空
// 字串要能退回 handle——所以這裡不在 SQL 內 COALESCE。
func (p *pgLiveRepo) DisplayName(ctx context.Context, uid string) (string, string, error) {
	var name, handle string
	err := p.db.QueryRow(ctx,
		`SELECT COALESCE(name,''), COALESCE(handle,'') FROM users WHERE id=$1`, uid).Scan(&name, &handle)
	return name, handle, err
}

// consentAuditMeta 同意稽核列的 meta（契約 §3.1）：只有同意版號與是否 presence-only。
// ⚠️ **不含座標**（也不含 sid／名稱）——稽核表是長期保存、後台可查的，位置資料不得進去。
func consentAuditMeta(consentV int, presenceOnly bool) (string, error) {
	b, err := json.Marshal(map[string]any{"consent_v": consentV, "presence_only": presenceOnly})
	return string(b), err
}

// consentAuditAction／Resource audit_logs 的 action 與 resource 值（契約 §3.1）。
const (
	consentAuditAction   = "runmeet_live_consent"
	consentAuditResource = "run_meet"
)

// consentDedupeWindowMinutes 同一人對同一團練的同意稽核列，在這個視窗內只留一列。
//
// 為什麼要去重：同意稽核是「這個人同意過」的**證據**，不是每次 /live/start 的日誌。但 client 依契約會合法地
// 反覆送 reauth:false 的 start——Redis 失聯（503）時退避重試、頁面重新整理後自動接續（接續也會再帶 consent_v）、
// 使用者離開再回來——每人每分鐘最多 6 次（runmeet_live_start 限流），不去重的話稽核表會被灌爆，
// 而且全是重複的同一份證據。
const consentDedupeWindowMinutes = 15

// insertConsentAuditSQL 單一語句、原子去重：視窗內已有同 user_id + action + resource_id 的列就不寫。
// （audit_logs 有 idx_audit_logs_user；一般會員的稽核列很少，子查詢走 user_id 索引成本可忽略。）
//
// ⚠️ 順序不變：仍是「同意證據先於 grant」——去重只是「證據已經在了就不重複寫」，證據存在才會往下建 grant。
// 已知且可接受的競態：同一人同一毫秒內並行的兩個首次 start 可能各寫一列（READ COMMITTED 下兩個子查詢都沒看到
// 對方）；只會多一列重複證據，不影響正確性。
// 每個參數都顯式轉型：同一個 $n 在 SELECT 清單與 WHERE 各出現一次，不轉型時型別推導取決於語句剖析順序。
var insertConsentAuditSQL = fmt.Sprintf(`
	INSERT INTO audit_logs (user_id, action, resource, resource_id, meta, ip)
	SELECT $1::uuid, $2::text, $3::text, $4::uuid, $5::jsonb, $6::text
	 WHERE NOT EXISTS (
	       SELECT 1 FROM audit_logs
	        WHERE user_id = $1::uuid AND action = $2::text AND resource_id = $4::uuid
	          AND created_at > NOW() - INTERVAL '%d minutes')`, consentDedupeWindowMinutes)

func (p *pgLiveRepo) InsertConsentAudit(ctx context.Context, uid, meetID string, consentV int, presenceOnly bool, ip string) error {
	meta, err := consentAuditMeta(consentV, presenceOnly)
	if err != nil {
		return err
	}
	if len(ip) > 45 { // audit_logs.ip 是 VARCHAR(45)（IPv6 上限）
		ip = ip[:45]
	}
	// RowsAffected = 0 代表視窗內已有證據、被去重掉——對呼叫端而言與「寫入成功」相同（證據存在），不是錯誤。
	_, err = p.db.Exec(ctx, insertConsentAuditSQL, uid, consentAuditAction, consentAuditResource, meetID, meta, ip)
	return err
}

// --- POST /run-meets/{id}/live/start ---

type liveStartHandler struct {
	repo  liveStartRepo
	store *live.Store
}

type liveStartRequest struct {
	PV  int    `json:"pv"`
	SID string `json:"sid"`
	// ConsentV 同意條款版號。指標：缺欄位（nil）與 0 都視為沒同意。reauth=true 時可省略。
	ConsentV *int `json:"consent_v"`
	Reauth   bool `json:"reauth"`
}

type liveStartMeet struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Lat   *float64 `json:"lat,omitempty"` // 成員層；presence_only 團不帶
	Lng   *float64 `json:"lng,omitempty"`
}

// liveStartResponse 契約 §3.1 的 200 回應。
type liveStartResponse struct {
	PV           int                `json:"pv"`
	N            int                `json:"n"`
	SID          string             `json:"sid"`
	IV           int                `json:"iv"`
	PresenceOnly bool               `json:"presence_only"`
	GrantTTLS    int                `json:"grant_ttl_s"`
	ReauthInS    int                `json:"reauth_in_s"`
	Meet         liveStartMeet      `json:"meet"`
	RV           int                `json:"rv"`
	Roster       []live.RosterEntry `json:"roster"`
	Live         int                `json:"live"`
	MaxLive      int                `json:"max_live"`
	Stale        live.Stale         `json:"stale"`
	ServerNowMs  int64              `json:"server_now_ms"`
}

// LiveStart POST /api/v1/run-meets/{id}/live/start（掛在 Router() 內，requireEntry 照走；6 次/分/人）。
func (h *Handler) LiveStart(w http.ResponseWriter, r *http.Request) {
	if h.liveStart == nil {
		live.WriteError(w, http.StatusServiceUnavailable, "redis_unavailable")
		return
	}
	h.liveStart.handle(w, r)
}

// handle 伺服器順序（契約 §3.1）：
//
//	uuid → body／pv／sid／consent_v → checkedAtMs（Redis TIME，**在任何 DB 讀取之前**）→ 全域 live 入口 →
//	讀團練 → 成員資格 → 時窗 → 取顯示名稱並消毒 → 同意稽核 → Lua start
//
// ⚠️ checkedAtMs 必須早於所有 DB 讀取：Lua 以它對照撤銷墓碑（M1）。若晚於 DB 讀取，
// 「start 讀到 joined → Kick commit 並 Revoke → start 才取時間」就會得到比墓碑更晚的 checkedAtMs，
// 被踢者照樣拿到 grant。
func (s *liveStartHandler) handle(w http.ResponseWriter, r *http.Request) {
	meetID, ok := live.ParseMeetID(r)
	if !ok {
		live.WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	uid, ok := live.UserUUID(r)
	if !ok {
		live.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req liveStartRequest
	if status, code := live.ReadJSON(w, r, &req); status != 0 {
		live.WriteError(w, status, code)
		return
	}
	if req.PV != live.ProtocolVersion {
		live.WriteError(w, http.StatusUpgradeRequired, "upgrade_required")
		return
	}
	if !live.ValidSID(req.SID) {
		live.WriteError(w, http.StatusBadRequest, "bad_sid")
		return
	}
	// 首次（使用者按下開始後）必須帶同意版號；靜默重驗（reauth=true）不需要也不重複寫稽核。
	if !req.Reauth && (req.ConsentV == nil || *req.ConsentV < 1) {
		live.WriteError(w, http.StatusBadRequest, "consent_required")
		return
	}

	ctx := r.Context()
	checkedAtMs, err := s.store.NowMs(ctx)
	if err != nil {
		live.WriteError(w, http.StatusServiceUnavailable, "redis_unavailable")
		return
	}

	// 全域 live 入口（非 shown → 403 entry_closed）
	settings := s.repo.LiveSettings(ctx)
	email, code, isSuper, ok := entryFlagsOrLookup(ctx, s.repo, uid)
	if !ok {
		live.WriteError(w, http.StatusInternalServerError, "server_error")
		return
	}
	if liveEntryFrom(settings.EntryState, settings.Whitelist, email, code, isSuper) != "shown" {
		live.WriteError(w, http.StatusForbidden, "entry_closed")
		return
	}

	// 團練：不存在／已刪除／後台下架 → 404；已中止 → 410
	meet, err := s.repo.MeetForLive(ctx, meetID)
	if err != nil {
		if errIsNotFound(err) {
			live.WriteError(w, http.StatusNotFound, "not_found")
			return
		}
		live.WriteError(w, http.StatusInternalServerError, "server_error")
		return
	}
	if meet.Status == StatusCancelled {
		live.WriteError(w, http.StatusGone, "meet_over")
		return
	}

	// 成員資格：發起人或 status='joined'（pending／rejected／kicked／left／非成員一律 403）。
	// hidden_by_owner 不影響 joined 成員（與既有可見性規則一致）。
	if !strings.EqualFold(meet.OwnerID, uid) {
		st, err := s.repo.MemberStatus(ctx, meetID, uid)
		if err != nil {
			live.WriteError(w, http.StatusInternalServerError, "server_error")
			return
		}
		if st != MemberJoined {
			live.WriteError(w, http.StatusForbidden, "not_member")
			return
		}
	}

	// 時窗：now ∈ [opens_at, closes_at)。時間用 Redis TIME（與 checkedAtMs 同一個時鐘）。
	opens, closes := settings.Window(meet.MeetAt, meet.EndsAt)
	if !live.InWindow(time.UnixMilli(checkedAtMs), opens, closes) {
		live.WriteJSON(w, http.StatusConflict, map[string]any{
			"error": "outside_window", "opens_at": opens, "closes_at": closes})
		return
	}

	// 顯示名稱：COALESCE(NULLIF(u.name,''), u.handle) 並消毒（NFC、去控制／零寬／bidi、20 rune、空→handle→「跑者」）
	rawName, handle, err := s.repo.DisplayName(ctx, uid)
	if err != nil {
		live.WriteError(w, http.StatusInternalServerError, "server_error")
		return
	}
	name := live.SanitizeDisplayName(rawName, handle)

	// 同意稽核（M8）：首次才寫；寫不進去就不開始同步（沒有同意證據就不分享位置）。
	if !req.Reauth {
		if err := s.repo.InsertConsentAudit(ctx, uid, meetID, *req.ConsentV, meet.NoLocation, middleware.ClientIP(r)); err != nil {
			live.WriteError(w, http.StatusInternalServerError, "server_error")
			return
		}
	}

	res, err := s.store.Start(ctx, meetID, live.StartParams{
		UID: uid, SID: req.SID, Name: name,
		MaxLive: settings.Max, PresenceOnly: meet.NoLocation, CheckedAtMs: checkedAtMs,
	})
	if err != nil {
		live.WriteError(w, http.StatusServiceUnavailable, "redis_unavailable")
		return
	}
	if res.Outcome != live.OutcomeOK {
		status, code := live.StatusForOutcome(res.Outcome)
		live.WriteError(w, status, code)
		return
	}

	roster := res.Roster
	if roster == nil {
		roster = []live.RosterEntry{}
	}
	iv := live.IntervalMs(res.Live)
	out := liveStartResponse{
		PV: live.ProtocolVersion, N: res.N, SID: req.SID, IV: iv, PresenceOnly: meet.NoLocation,
		GrantTTLS: int((res.ExpMs - res.NowMs) / 1000), ReauthInS: live.ReauthInS(res.ExpMs, res.NowMs),
		Meet: liveStartMeet{ID: meetID, Title: meet.Title},
		RV:   res.RV, Roster: roster, Live: res.Live, MaxLive: settings.Max,
		Stale: live.StaleFor(iv), ServerNowMs: res.NowMs,
	}
	if !meet.NoLocation {
		out.Meet.Lat, out.Meet.Lng = meet.Lat, meet.Lng
	}
	live.WriteJSON(w, http.StatusOK, out)
}

// entryFlagsOrLookup 優先用 requireEntry 放進 context 的旗標（同一請求不重查 users），沒有才查 repo。
func entryFlagsOrLookup(ctx context.Context, repo liveStartRepo, uid string) (email, code string, isSuper, ok bool) {
	if f, found := entryFlagsFrom(ctx); found {
		return f.email, f.code, f.isSuper, true
	}
	email, code, isSuper, _, err := repo.UserFlags(ctx, uid)
	if err != nil {
		return "", "", false, false
	}
	return email, code, isSuper, true
}

// --- 詳情 DTO 的 live 區塊（只在 MemberDetailView）---

// buildLiveInfo 純函式：由團練列、設定與「入口是否 shown」組出 live 區塊。
func buildLiveInfo(m *meetRow, s live.Settings, enabled bool, now time.Time) *LiveInfo {
	opens, closes := s.Window(m.MeetAt, m.EndsAt)
	return &LiveInfo{Enabled: enabled, OpensAt: opens, ClosesAt: closes, ServerNow: now, PresenceOnly: m.NoLocation}
}

// liveSettings 詳情 DTO 用的 live 設定（測試可經 liveSettingsFn 覆寫；正式環境讀 app_settings 的行程內快取）。
func (h *Handler) liveSettings(ctx context.Context) live.Settings {
	if h.liveSettingsFn != nil {
		return h.liveSettingsFn(ctx)
	}
	return loadLiveSettings(ctx, h.db)
}

// detailView 在 buildDetail（純函式、無 DB）之上，替成員視角的詳情補上真實的 live 區塊：
// enabled = 該使用者的 live 入口為 shown；時窗吃現行設定。非成員視角（PublicDetailView）原樣回傳，
// 結構上沒有 live 欄位。
func (h *Handler) detailView(ctx context.Context, m *meetRow, viewer string) any {
	d := h.buildDetail(m, viewer, false)
	mv, ok := d.(MemberDetailView)
	if !ok {
		return d
	}
	s := h.liveSettings(ctx)
	enabled := false
	if f, found := entryFlagsFrom(ctx); found {
		enabled = liveEntryFrom(s.EntryState, s.Whitelist, f.email, f.code, f.isSuper) == "shown"
	} else if email, code, isSuper, _, err := h.repo.UserFlags(ctx, viewer); err == nil {
		enabled = liveEntryFrom(s.EntryState, s.Whitelist, email, code, isSuper) == "shown"
	}
	mv.Live = buildLiveInfo(m, s, enabled, time.Now())
	return mv
}
