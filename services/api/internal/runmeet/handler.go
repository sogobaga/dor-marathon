package runmeet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/middleware"
	"github.com/dor/api/internal/realtime"
)

// uuidRE 路徑/body 帶入的 id 必須是合法 UUID（比照 partner/handler.go:16）：
// 不合法直接擋，避免把非 UUID 字串丟給 Postgres 的 uuid 欄位比較（型別錯誤 → 500 + log 噪音）。
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool { return uuidRE.MatchString(s) }

type Handler struct {
	db   *pgxpool.Pool
	repo *Repository
	rdb  *redis.Client
	rt   *realtime.Manager
}

func NewHandler(db *pgxpool.Pool, rdb *redis.Client, rt *realtime.Manager) *Handler {
	return &Handler{db: db, repo: NewRepository(db), rdb: rdb, rt: rt}
}

// --- 回應工具（每套件各自複製一份，比照 partner/handler.go:237）---

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func respondErr(w http.ResponseWriter, code int, msg string) {
	respondJSON(w, code, map[string]string{"error": msg})
}

// respondAPIErr 把 apiErr 轉成對應 status + 中文訊息；非 apiErr（DB 錯等）一律 500 + 英文短句，
// 不外洩內部錯誤內容。
func respondAPIErr(w http.ResponseWriter, err error) {
	var e *apiErr
	if errors.As(err, &e) {
		respondErr(w, e.Status, e.Msg)
		return
	}
	if errors.Is(err, ErrQuotaExhausted) {
		respondErr(w, http.StatusConflict, "本月發起次數已用完。")
		return
	}
	respondErr(w, http.StatusInternalServerError, "failed")
}

// --- 入口閘門 ---

// ResolveEntry 團練邀請入口可見性（hidden|locked|shown）。供 profile 的 DashboardInfo 使用。
// 超管恆放行（比照 monopoly/gps_calib 既有慣例：超管永遠看得到功能本身）。
func ResolveEntry(ctx context.Context, db *pgxpool.Pool, email, code string, isSuperAdmin bool) string {
	if isSuperAdmin {
		return "shown"
	}
	return entryFrom(
		appsettings.GetString(ctx, db, EntryStateKey, "hidden"),
		appsettings.GetString(ctx, db, EntryWhitelistKey, ""),
		email, code)
}

// entryFrom 純函式版（設定值由呼叫端傳入），可單元測試。
// 「locked」在前端是顯示但不可按，對應到後端一樣不放行——requireEntry 只認 "shown"。
func entryFrom(state, whitelist, email, code string) string {
	switch state {
	case "open":
		return "shown"
	case "locked":
		return "locked"
	case "whitelist":
		if whitelisted(whitelist, email, code) {
			return "shown"
		}
		return "hidden"
	default: // hidden 或未設定 → fail-closed
		return "hidden"
	}
}

// whitelisted 比照 profile.personalWhitelisted / gpscalib.whitelisted：
// 換行/逗號/分號/空白分隔，可填帳號編碼（# 可省）或 email，大小寫不敏感。
func whitelisted(list, email, code string) bool {
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

// DashboardSummary 給 profile 的 DashboardInfo 用：入口旗標 + 本月剩餘發起次數。
//
// ⚠️ Dashboard 是熱路徑：入口非 shown 時**完全不查 DB**（直接回 0），只有真的看得到入口的
// 帳號才多一次 users 主鍵查詢。規格 1.7 否決的是「跨表 COUNT 待審申請數」，不是這個。
func DashboardSummary(ctx context.Context, db *pgxpool.Pool, userID, email, code string, isSuperAdmin, isVIP bool) (entry string, remaining int) {
	entry = ResolveEntry(ctx, db, email, code, isSuperAdmin)
	if entry != "shown" {
		return entry, 0
	}
	var month *string
	var used int
	if err := db.QueryRow(ctx, `SELECT run_meet_month, COALESCE(run_meet_used,0) FROM users WHERE id=$1`, userID).
		Scan(&month, &used); err != nil {
		return entry, 0
	}
	cap := QuotaCap(isVIP,
		appsettings.GetInt(ctx, db, keyQuotaNormal, defQuotaNormal),
		appsettings.GetInt(ctx, db, keyQuotaVIP, defQuotaVIP))
	if month == nil || *month != QuotaMonth(time.Now()) {
		return entry, cap // 跨月自動重置（CAS 在下次建立時才真的寫入）
	}
	if remaining = cap - used; remaining < 0 {
		remaining = 0
	}
	return entry, remaining
}

// requireEntry 後端強制入口白名單（SEC-H5：前端隱藏 ≠ 後端有擋）。
// 獨立實作、不 import profile（避免循環依賴），比照 gpscalib/monopoly 既有前例。
func (h *Handler) requireEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		if uid == "" {
			respondErr(w, http.StatusUnauthorized, "login required")
			return
		}
		email, code, isSuper, _, err := h.repo.UserFlags(r.Context(), uid)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to resolve access")
			return
		}
		if ResolveEntry(r.Context(), h.db, email, code, isSuper) != "shown" {
			respondErr(w, http.StatusForbidden, errEntryClosed.Msg)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- 路由 ---

// Router 前台（掛在 /api/v1/run-meets，需 RequireAuth）。第一行即 requireEntry。
//
// ⚠️ 上傳路徑必須是扁平的 /images（不是 /{id}/images）：全域 MaxBodyBytes 的 skip 清單是
// strings.HasPrefix 前綴比對（見 middleware/bodylimit.go），巢狀路徑無法用前綴排除。
// chi 的 radix tree 靜態段優先於 param 段，POST /run-meets/images 與 GET /run-meets/{id} 可共存。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireEntry)

	// 靜態路徑必須寫在 {id} 之前（可讀性；chi 本身已保證靜態優先）
	r.Get("/", h.List)
	r.Get("/mine", h.Mine)
	r.Get("/quota", h.Quota)
	r.Get("/place-suggest", h.PlaceSuggest)
	r.With(h.limit("runmeet_create", 5, time.Hour)).Post("/", h.Create)
	r.With(h.limit("runmeet_image", 20, time.Hour)).Post("/images", h.UploadImage)

	r.Get("/{id}", h.Detail)
	r.With(h.limit("runmeet_write", 20, time.Minute)).Put("/{id}", h.Update)
	r.With(h.limit("runmeet_write", 20, time.Minute)).Post("/{id}/status", h.SetStatus)
	r.With(h.limit("runmeet_write", 20, time.Minute)).Post("/{id}/visibility", h.Visibility)
	r.With(h.limit("runmeet_write", 20, time.Minute)).Delete("/{id}", h.Delete)

	// 密碼端點：mount 級限流掛在**具體 route** 上（不是 r.Use）——掛 r.Use 時路由尚未匹配，
	// chi.URLParam 取不到 {id}，自訂維度會回空字串而 middleware.RateLimit 對空維度直接放行
	// ＝完全不限流的假防護（見 middleware/ratelimit.go 的 d == "" 分支）。
	// 每團每人的失敗計數另在 handler 內自行 INCR（見 Unlock）。
	r.With(h.limit("runmeet_unlock", 10, time.Minute)).Post("/{id}/unlock", h.Unlock)

	r.With(h.limit("runmeet_join", 10, time.Minute)).Post("/{id}/join", h.Join)
	r.With(h.limit("runmeet_join", 10, time.Minute)).Delete("/{id}/join", h.Leave)

	r.Get("/{id}/members", h.Members)
	r.With(h.limit("runmeet_decide", 60, time.Minute)).Post("/{id}/members/approve-batch", h.ApproveBatch)
	r.With(h.limit("runmeet_decide", 60, time.Minute)).Post("/{id}/members/{uid}/approve", h.ApproveOne)
	r.With(h.limit("runmeet_decide", 60, time.Minute)).Post("/{id}/members/{uid}/reject", h.RejectOne)
	r.With(h.limit("runmeet_decide", 60, time.Minute)).Post("/{id}/members/{uid}/kick", h.KickOne)
	r.With(h.limit("runmeet_write", 20, time.Minute)).Delete("/{id}/members/{uid}/ban", h.UnbanOne)

	r.Get("/{id}/comments", h.ListComments)
	r.With(h.limit("runmeet_comment", 20, time.Minute)).Post("/{id}/comments", h.CreateComment)
	r.With(h.limit("runmeet_write", 20, time.Minute)).Delete("/{id}/comments/{cid}", h.DeleteComment)
	r.Get("/{id}/comments/{cid}/replies", h.ListReplies)
	r.With(h.limit("runmeet_react", 60, time.Minute)).Put("/{id}/comments/{cid}/reaction", h.PutCommentReaction)
	r.With(h.limit("runmeet_react", 60, time.Minute)).Delete("/{id}/comments/{cid}/reaction", h.PutCommentReaction)
	r.With(h.limit("runmeet_react", 60, time.Minute)).Put("/{id}/reaction", h.PutReaction)
	r.With(h.limit("runmeet_react", 60, time.Minute)).Delete("/{id}/reaction", h.DeleteReaction)

	r.With(h.limit("runmeet_report", 10, time.Hour)).Post("/{id}/report", h.Report)
	return r
}

func (h *Handler) limit(action string, n int, window time.Duration) func(http.Handler) http.Handler {
	return middleware.RateLimit(h.rdb, action, n, window, middleware.UserOrIP)
}

// --- 共用小工具 ---

func uid(r *http.Request) string {
	s, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	return s
}

func meetIDParam(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	return id, isValidUUID(id)
}

// cursorLimit 讀 ?limit=（討論串游標分頁用；沒有 offset）。缺省或不合法一律回 defLimit，
// 夾在 [1,maxLimit]——前端「展開留言」流程會依情境帶不同 limit（初次展開 20、之後續抓 10）。
func cursorLimit(r *http.Request, defLimit, maxLimit int) int {
	v, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || v <= 0 {
		return defLimit
	}
	if v > maxLimit {
		return maxLimit
	}
	return v
}

func pageParams(r *http.Request, defLimit, maxLimit int) (limit, offset int) {
	limit = defLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	return
}

// notify 即時推播（全站 data_updated，topic=runmeet；前端 siteRealtimeStore 需同步登記 topic，
// 否則會被靜默丟棄）。fire-and-forget，失敗不影響主流程。
func (h *Handler) notify(ctx context.Context, userIDs ...string) {
	if h.rt == nil || len(userIDs) == 0 {
		return
	}
	h.rt.PublishData(ctx, "runmeet", userIDs)
}

// --- DTO 組裝（地點分層的唯一出口，改這裡前先讀 model.go 檔頭）---

// buildCard 組公開層卡片。
//
// ⚠️ isAdmin 必須一路傳到底：後台在處理檢舉/下架時，看的常常正是「私密、且管理員不是成員」
// 的團——若這裡把 isAdmin 寫死成 false，HasDetailAccess 會回 false，buildDetail 接著把
// description 清空、image_urls 清成空陣列，管理員在後台看到的是一個「沒填說明也沒圖」的團，
// 等於無法據以判斷該不該下架（而 lat/lng 卻正常顯示，錯得很不直觀）。
func (h *Handler) buildCard(m *meetRow, viewer string, withBand, isAdmin bool) CardView {
	isOwner := m.OwnerID == viewer
	hasAccess := HasDetailAccess(m.IsPrivate, isOwner, m.MyStatus, m.Unlocked, isAdmin)

	// 封面：先判權限（私密團未解鎖不給，excerpt 仍給——需求 2(c) 明寫列表要有摘要），
	// 再判 show_cover 偏好（migration 162）。順序寫死在 resolveCoverURL 裡，這裡不得自己拆開判。
	cover := resolveCoverURL(hasAccess, m.ShowCover, m.ImageURLs)
	// is_ended／phase 統一由 effectiveEnd 判定（2026-09-04 owner 決策 phase 2）：有 ends_at 用
	// ends_at，舊資料退回 meet_at——見 model.go effectiveEnd／meetPhase 檔頭。
	now := time.Now()
	end := effectiveEnd(m.MeetAt, m.EndsAt)
	c := CardView{
		ID:               m.ID,
		Title:            m.Title,
		MeetAt:           m.MeetAt,
		EndsAt:           m.EndsAt,
		Region:           m.Region,
		PlaceLabel:       m.PlaceLabel,
		NoLocation:       m.NoLocation,
		Capacity:         m.Capacity,
		MemberCount:      m.MemberCount,
		IsPrivate:        m.IsPrivate,
		ApprovalRequired: m.ApprovalRequired,
		Excerpt:          excerpt(m.Description, excerptRunes),
		CoverURL:         cover,
		ShowCover:        m.ShowCover,
		Status:           m.Status,
		IsEnded:          !end.After(now),
		Phase:            meetPhase(m.MeetAt, end, now),
		Owner:            OwnerView{ID: m.OwnerID, Name: m.OwnerName, AvatarURL: m.OwnerAvatar},
		MyState:          MyState(isOwner, m.MyStatus),
		ReactionCount:    m.ReactionCount,
		CommentCount:     m.CommentCount,
		MyReaction:       m.MyReaction,
		HasAccess:        hasAccess,
	}
	if withBand {
		c.DistanceBand = DistanceBand(m.distanceM)
	}
	// HiddenByOwner 只給發起人／後台視角（比照 PendingCount 的既有模式）：這是發起人管理面板
	// 專用的旗標，一般訪客本來就看不到被隱藏的團（GetMeet/ListMeets 已在 SQL 層擋掉），
	// 沒有機會走到這個欄位有意義的分支，恆 false 也不算洩漏。
	if isOwner || isAdmin {
		c.HiddenByOwner = m.HiddenByOwner
	}
	return c
}

// locationNote 未加入者在地點欄位下方看到的固定提示。
const locationNote = "成功加入後才會顯示完整詳細地點"

// buildDetail 依觀看者身分回**不同型別**的詳情。
// ⚠️ 非成員拿到的是 PublicDetailView——結構上就沒有 lat/lng/meeting_detail 三個欄位，
// 序列化出來的 JSON 不會有這些 key（不是回 null、也不是回 0）。
func (h *Handler) buildDetail(m *meetRow, viewer string, isAdmin bool) any {
	isOwner := m.OwnerID == viewer
	base := detailBase{
		CardView:    h.buildCard(m, viewer, false, isAdmin),
		Description: m.Description,
		ImageURLs:   m.ImageURLs,
		ImageLimit:  m.ImageLimit,
		CanComment:  canComment(isOwner, m.MyStatus, m.Status, effectiveEnd(m.MeetAt, m.EndsAt)),
	}
	if base.ImageURLs == nil {
		base.ImageURLs = []string{} // 前端契約：image_urls 恆為陣列，不會是 null
	}
	// 私密團未解鎖：完整說明與圖片一律不給（列表 excerpt 仍給，需求 2(c)）。
	// isAdmin 已在 buildCard 內納入 HasDetailAccess，所以後台不會被這段清空。
	if !base.HasAccess {
		base.ImageURLs = []string{}
		base.Description = ""
	}
	if isOwner || isAdmin {
		base.PendingCount = m.PendingCount
	}
	if CanSeePreciseLocation(isOwner, m.MyStatus, isAdmin) {
		return MemberDetailView{detailBase: base, LocationLocked: false,
			Lat: m.Lat, Lng: m.Lng, MeetingDetail: m.MeetingDetail}
	}
	return PublicDetailView{detailBase: base, LocationLocked: true, LocationNote: locationNote}
}

// canComment 留言權：joined/owner，且未超過「結束後 7 天」的唯讀界線，且團未取消。
//
// ⚠️ end 是呼叫端算好的「這個團練實際結束時刻」（傳 effectiveEnd(m.MeetAt, m.EndsAt)，見
// model.go effectiveEnd），不是 meet_at——2026-09-04 owner 決策 phase 2：「結束後 7 天」的
// 7 天要從真正的結束時間算起，一個 ends_at 訂 3 小時後的團練，不該在開跑當下就開始倒數
// 留言唯讀期。參數刻意不叫 effectiveEnd，避免與同名的套件層級函式互相遮蔽。
func canComment(isOwner bool, myStatus *string, status string, end time.Time) bool {
	if !isOwner && (myStatus == nil || *myStatus != MemberJoined) {
		return false
	}
	if status == StatusCancelled {
		return false
	}
	return time.Now().Before(end.Add(endedCommentDays * 24 * time.Hour))
}

// --- 前台 handlers ---

// GET /run-meets
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	u := uid(r)
	q := r.URL.Query()
	limit, offset := pageParams(r, 20, 50)

	f := ListFilter{
		Q:        q.Get("q"),
		Region:   q.Get("region"),
		Privacy:  q.Get("privacy"),
		Approval: q.Get("approval"),
		HasSlot:  q.Get("has_slot") == "1",
		Ended:    q.Get("ended") == "1",
		Sort:     q.Get("sort"),
		Limit:    limit,
		Offset:   offset,
	}

	// 「搜尋附近的團練」。使用者位置只當查詢參數：不寫入 DB、不進 log。
	withBand := false
	if ls, ns := q.Get("near_lat"), q.Get("near_lng"); ls != "" && ns != "" {
		lat, e1 := strconv.ParseFloat(ls, 64)
		lng, e2 := strconv.ParseFloat(ns, 64)
		if e1 != nil || e2 != nil || !validCoord(lat, lng) {
			respondAPIErr(w, errBadCoord)
			return
		}
		// ⚠️ 半徑只接受與 distance_band 邊界重合的離散值（1/3/5/10 km），任意值一律吸附。
		// 這條是資安要求不是輸入清理：radius_km 若可連續調整，「這個團練有沒有出現在結果裡」
		// 就成了「距離 < X 嗎」的布林神諭，對 X 二分搜尋 25 次即可把距離收斂到公尺級，
		// 再換三組 near 座標聯立就解得出精確集合點（見 geo.go snapRadiusKm 的長註解）。
		radius := snapRadiusKm(10)
		if rs := q.Get("radius_km"); rs != "" {
			if v, err := strconv.ParseFloat(rs, 64); err == nil && v > 0 {
				radius = snapRadiusKm(v)
			}
		}
		f.NearLat, f.NearLng, f.RadiusKm = &lat, &lng, radius
		withBand = true
	}

	s := loadSettings(r.Context(), h.db)
	rows, total, err := h.repo.ListMeets(r.Context(), u, f, s.EndedVisibleDays)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	items := make([]CardView, 0, len(rows))
	for i := range rows {
		items = append(items, h.buildCard(&rows[i], u, withBand, false))
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// GET /run-meets/mine → {owned, joined, pending}
func (h *Handler) Mine(w http.ResponseWriter, r *http.Request) {
	u := uid(r)
	owned, joined, pending, err := h.repo.Mine(r.Context(), u)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	conv := func(rows []meetRow) []CardView {
		out := make([]CardView, 0, len(rows))
		for i := range rows {
			out = append(out, h.buildCard(&rows[i], u, false, false))
		}
		return out
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"owned": conv(owned), "joined": conv(joined), "pending": conv(pending)})
}

// GET /run-meets/quota
func (h *Handler) Quota(w http.ResponseWriter, r *http.Request) {
	u := uid(r)
	_, _, _, isVIP, err := h.repo.UserFlags(r.Context(), u)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	s := loadSettings(r.Context(), h.db)
	now := time.Now()
	month := QuotaMonth(now)
	dbMonth, used, err := h.repo.QuotaOf(r.Context(), u)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	if dbMonth != month {
		used = 0 // 跨月自動重置（CAS 在下次建立時才真的寫入）
	}
	cap := QuotaCap(isVIP, s.QuotaNormal, s.QuotaVIP)
	remaining := cap - used
	if remaining < 0 {
		remaining = 0
	}
	respondJSON(w, http.StatusOK, QuotaView{
		Month: month, Cap: cap, Used: used, Remaining: remaining,
		IsVIP: isVIP, RequiresVIP: s.RequiresVIP,
		ImageLimit: ImageLimit(isVIP, s.ImagesNormal, s.ImagesVIP),
		// VIP 權益數字給前端做文案（後台可調，前端不得寫死 10 / 4）
		VIPCap: s.QuotaVIP, VIPImageLimit: s.ImagesVIP,
		CapacityMax: s.CapacityMax,
		ResetsAt:    QuotaResetAt(now),
	})
}

// GET /run-meets/place-suggest?q=&lat=&lng=
func (h *Handler) PlaceSuggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var lat, lng *float64
	if ls, ns := q.Get("lat"), q.Get("lng"); ls != "" && ns != "" {
		a, e1 := strconv.ParseFloat(ls, 64)
		b, e2 := strconv.ParseFloat(ns, 64)
		if e1 != nil || e2 != nil || !validCoord(a, b) {
			respondAPIErr(w, errBadCoord)
			return
		}
		lat, lng = &a, &b
	}
	items, err := h.repo.PlaceSuggest(r.Context(), q.Get("q"), lat, lng)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

// POST /run-meets
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	u := uid(r)
	var in MeetInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	_, _, _, isVIP, err := h.repo.UserFlags(r.Context(), u)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	s := loadSettings(r.Context(), h.db)
	if s.RequiresVIP && !isVIP {
		// 政策開關（runmeet_create_requires_vip=1）：不消耗任何次數，直接引導升級。
		respondAPIErr(w, errRequiresVIP)
		return
	}
	imgLimit := ImageLimit(isVIP, s.ImagesNormal, s.ImagesVIP)
	if err := validateMeetInput(&in, time.Now(), s.CapacityMax, imgLimit, s.ImagesVIP, true); err != nil {
		respondAPIErr(w, err)
		return
	}
	if len(in.ClientToken) > 64 {
		respondAPIErr(w, errBadJSON)
		return
	}
	if err := h.repo.VerifyImageOwnership(r.Context(), u, in.ImageURLs); err != nil {
		respondAPIErr(w, err)
		return
	}

	now := time.Now()
	quotaCap := QuotaCap(isVIP, s.QuotaNormal, s.QuotaVIP)
	id, used, dup, err := h.repo.CreateMeet(r.Context(), u, &in, imgLimit, quotaCap, QuotaMonth(now))
	if errors.Is(err, ErrQuotaExhausted) {
		respondAPIErr(w, errQuotaUsedUp(quotaCap, isVIP, QuotaResetAt(now).Month(), s.QuotaVIP))
		return
	}
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	_ = dup // 冪等命中時同樣回 200 + 既有 id（前端無需區分）
	m, err := h.repo.GetMeet(r.Context(), u, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notify(r.Context(), u)
	respondJSON(w, http.StatusOK, map[string]any{
		"meet": h.buildDetail(&m, u, false), "used": used, "remaining": maxInt(quotaCap-used, 0)})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// GET /run-meets/{id}
//
// 私密團未解鎖時回 403 + 摘要卡（規格 4.3），讓前端能渲染「請輸入密碼」而不是空白頁。
// 卡片只含公開層欄位（地點只有 region/place_label）。
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	m, err := h.repo.GetMeet(r.Context(), u, id)
	if err != nil {
		// 已軟刪回 410 + {"error":"deleted"}，讓前端能顯示倒數導頁文案（規格：連結是發起人
		// 主動分享出去的，收到連結的人本來就知道它存在過，不構成新洩漏）；其餘讓 GetMeet
		// 找不到列的原因（不存在／後台下架／發起人隱藏且非當事人）維持 404，不外洩差異。
		if errIsNotFound(err) {
			if deleted, derr := h.repo.IsDeleted(r.Context(), id); derr == nil && deleted {
				respondJSON(w, http.StatusGone, map[string]string{"error": "deleted"})
				return
			}
		}
		respondAPIErr(w, err)
		return
	}
	isOwner := m.OwnerID == u
	if !HasDetailAccess(m.IsPrivate, isOwner, m.MyStatus, m.Unlocked, false) {
		respondJSON(w, http.StatusForbidden, map[string]any{
			"error": errLocked.Msg, "locked": true, "card": h.buildCard(&m, u, false, false)})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"meet": h.buildDetail(&m, u, false)})
}

// PUT /run-meets/{id}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	var in MeetInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	s := loadSettings(r.Context(), h.db)
	cur, err := h.repo.GetMeet(r.Context(), u, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	if cur.OwnerID != u {
		respondAPIErr(w, errNotOwner)
		return
	}
	// 編輯上限＝max(建立當下快照, 現行身分上限)，見 quota.go EffectiveImageLimit 的註解：
	// 快照負責「VIP 到期仍能編輯既有多圖團」，現行上限負責「後台調高後既有團練也跟著放寬」。
	_, _, _, isVIP, err := h.repo.UserFlags(r.Context(), u)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	editLimit := EffectiveImageLimit(cur.ImageLimit, isVIP, s.ImagesNormal, s.ImagesVIP)
	if err := validateMeetInput(&in, time.Now(), s.CapacityMax, editLimit, s.ImagesVIP, false); err != nil {
		respondAPIErr(w, err)
		return
	}
	if err := h.repo.VerifyImageOwnership(r.Context(), u, in.ImageURLs); err != nil {
		respondAPIErr(w, err)
		return
	}
	res, err := h.repo.UpdateMeet(r.Context(), u, id, &in, editLimit)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	m, err := h.repo.GetMeet(r.Context(), u, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]any{
		"meet": h.buildDetail(&m, u, false), "pending_kept": res.PendingKept})
}

// notifyMembers 推播給該團所有 joined/pending 成員（含發起人）。
func (h *Handler) notifyMembers(ctx context.Context, meetID string) {
	if h.rt == nil {
		return
	}
	rows, err := h.db.Query(ctx, `
		SELECT user_id FROM run_meet_members WHERE meet_id=$1 AND status IN ('joined','pending')`, meetID)
	if err != nil {
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			ids = append(ids, s)
		}
	}
	h.notify(ctx, ids...)
}

// statusAction 前端傳入的 action 字串 → 目標 status（純函式，可單元測試）。
// 刻意用白名單而不是直接把 action 當 status 值送進 repository：action 是 API 契約用字
// （動詞：open/close/cancel），status 是資料庫欄位值（名詞：open/closed/cancelled），
// 兩者故意不共用同一組字串，避免前端傳入奇怪字串時被當成合法 status 值直接打進 SQL 參數。
func statusAction(action string) (status string, ok bool) {
	switch action {
	case "open":
		return StatusOpen, true
	case "close":
		return StatusClosed, true
	case "cancel":
		return StatusCancelled, true
	}
	return "", false
}

// POST /run-meets/{id}/status  {"action":"open"|"close"|"cancel"}
//
// 三個動作共用一支端點、一支 repository 方法（SetStatus）；合法轉換表見 model.go
// CanTransition——open⇄closed（關閉可逆）、open/closed→cancelled（中止）、cancelled→open（恢復）。
// ⚠️ 不碰 run_meet_used：「開啟後關閉一樣消耗一次」（見 quota.go 檔頭）。
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	status, ok := statusAction(body.Action)
	if !ok {
		respondAPIErr(w, errBadJSON)
		return
	}
	u := uid(r)
	// rejected＝轉為 cancelled 時被一併婉拒的待審申請者；closed/open 恆空（見 SetStatus 註解）。
	rejected, err := h.repo.SetStatus(r.Context(), u, id, status)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notifyMembers(r.Context(), id)
	h.notify(r.Context(), rejected...) // 已不在 joined/pending 名單，必須單獨推
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status, "rejected": len(rejected)})
}

// POST /run-meets/{id}/visibility  {"hidden":true|false}（僅發起人；hidden_by_owner，可逆）
func (h *Handler) Visibility(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	var body struct {
		Hidden bool `json:"hidden"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	u := uid(r)
	if err := h.repo.SetVisibility(r.Context(), u, id, body.Hidden); err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notify(r.Context(), u)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "hidden": body.Hidden})
}

// DELETE /run-meets/{id}（軟刪；同樣不返還配額）
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	if err := h.repo.SoftDelete(r.Context(), u, id); err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notify(r.Context(), u)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /run-meets/images
func (h *Handler) UploadImage(w http.ResponseWriter, r *http.Request) {
	u := uid(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1024)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		respondAPIErr(w, errImageTooLarge)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		respondAPIErr(w, errImageMissing)
		return
	}
	defer file.Close()

	out, mime, err := processUpload(file)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	id, err := h.repo.InsertImage(r.Context(), u, mime, out)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	respondJSON(w, http.StatusOK, map[string]string{"id": id, "url": "/api/v1/images/" + id})
}

// --- 私密團密碼 ---

// pwFailScript 每團每人密碼失敗計數：INCR + 首次 EXPIRE 原子執行（沿用 middleware/ratelimit.go
// 的 Lua 做法，避免「已 INCR 但沒 TTL」永久鎖死）。
var pwFailScript = redis.NewScript(`
local n = redis.call("INCR", KEYS[1])
if n == 1 then
	redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return n
`)

const (
	pwFailMax    = 5
	pwFailWindow = 600 // 秒
)

// POST /run-meets/{id}/unlock  {"password":"..."}
//
// 防暴力破解四層（規格 1.4）：
//  1. route 級 middleware.RateLimit（runmeet_unlock 10/min，掛在具體 route 不是 r.Use）
//  2. 每團每人失敗計數（本函式內自行 INCR；不可用 middleware——r.Use 層取不到 {id}）
//  3. 統一錯誤 + 統一時序（團不存在也對 dummy hash 跑一次 bcrypt，見 Repository.VerifyPassword）
//  4. Redis 不可用時**不 fail-open**：改成固定 500ms 延遲。這是與既有 RateLimit（rdb==nil 放行）
//     的刻意分歧——密碼面 fail-open 風險高於一般端點；配合 bcrypt cost 10（約 60–100ms）
//     把單連線速率壓到 ~100 次/分。
func (h *Handler) Unlock(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}

	key := "ratelimit:runmeet_pwfail:" + u + ":" + id
	if h.rdb == nil {
		time.Sleep(500 * time.Millisecond)
	} else {
		n, err := h.rdb.Get(r.Context(), key).Int64()
		if err == nil && n >= pwFailMax {
			w.Header().Set("Retry-After", strconv.Itoa(pwFailWindow))
			respondAPIErr(w, errPasswordTooMany)
			return
		}
		if err != nil && !errors.Is(err, redis.Nil) {
			time.Sleep(500 * time.Millisecond) // Redis 出錯：降級成延遲，不放行
		}
	}

	okPw, err := h.repo.VerifyPassword(r.Context(), id, body.Password)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	if !okPw {
		if h.rdb != nil {
			_, _ = pwFailScript.Run(r.Context(), h.rdb, []string{key}, pwFailWindow).Int64()
		}
		// 團不存在／已下架／密碼錯誤一律回同一句（不外洩「這個團存不存在」）
		respondAPIErr(w, errPasswordWrong)
		return
	}
	if h.rdb != nil {
		h.rdb.Del(r.Context(), key)
	}
	if err := h.repo.GrantAccess(r.Context(), id, u); err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- 加入／退出 ---

// POST /run-meets/{id}/join  {"note":"..."}
func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // note 選填，body 可為空
	note, err := normalizeText(body.Note, MaxApplyNoteRunes, false)
	if err != nil {
		respondAPIErr(w, errTooLong)
		return
	}

	m, err := h.repo.GetMeet(r.Context(), u, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	// 私密團必須先 unlock（或已是成員）——密碼是加入的前置條件
	if !HasDetailAccess(m.IsPrivate, m.OwnerID == u, m.MyStatus, m.Unlocked, false) {
		respondAPIErr(w, errLocked)
		return
	}

	s := loadSettings(r.Context(), h.db)
	res, err := h.repo.Join(r.Context(), u, id, note, s)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notify(r.Context(), u, m.OwnerID)
	respondJSON(w, http.StatusOK, res)
}

// DELETE /run-meets/{id}/join（撤回申請／自行退出）
func (h *Handler) Leave(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	state, err := h.repo.LeaveOrWithdraw(r.Context(), u, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notifyMembers(r.Context(), id)
	h.notify(r.Context(), u)
	respondJSON(w, http.StatusOK, map[string]string{"state": state})
}

// --- 成員管理 ---

// GET /run-meets/{id}/members?status=joined|pending
// joined 清單：joined 成員與發起人可看；pending 清單：只有發起人可看。
func (h *Handler) Members(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	m, err := h.repo.GetMeet(r.Context(), u, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	isOwner := m.OwnerID == u
	status := r.URL.Query().Get("status")
	if status == "" {
		status = MemberJoined
	}
	if status != MemberJoined && status != MemberPending {
		respondAPIErr(w, errBadID)
		return
	}
	if status == MemberPending && !isOwner {
		respondAPIErr(w, errNotOwner)
		return
	}
	if !isOwner && (m.MyStatus == nil || *m.MyStatus != MemberJoined) {
		respondAPIErr(w, errNotMember)
		return
	}
	items, err := h.repo.ListMembers(r.Context(), id, status)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

// memberAction 三個「指定操作對象」端點的共用外殼。
// ⚠️ 對象一律取自路徑 {uid}，操作者一律取自 context；owner 驗證在 repository 的鎖內做。
func (h *Handler) memberAction(w http.ResponseWriter, r *http.Request,
	fn func(ctx context.Context, ownerID, meetID, targetID string) error) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	target := chi.URLParam(r, "uid")
	if !isValidUUID(target) {
		respondAPIErr(w, errBadID)
		return
	}
	if err := fn(r.Context(), uid(r), id, target); err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notify(r.Context(), target)
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) ApproveOne(w http.ResponseWriter, r *http.Request) {
	h.memberAction(w, r, h.repo.Approve)
}
func (h *Handler) RejectOne(w http.ResponseWriter, r *http.Request) {
	h.memberAction(w, r, h.repo.Reject)
}
func (h *Handler) KickOne(w http.ResponseWriter, r *http.Request) {
	h.memberAction(w, r, h.repo.Kick)
}
func (h *Handler) UnbanOne(w http.ResponseWriter, r *http.Request) {
	h.memberAction(w, r, h.repo.Unban)
}

// POST /run-meets/{id}/members/approve-batch  {"user_ids":[...]}
// ⚠️ 一人一交易、逐筆執行，回 per-item 結果。刻意不包成單一大交易——一人失敗全滾回體驗更差
// （名額只剩 3 個卻同意 5 人時，應該是「成功 3 筆、2 筆 409」而不是「全部沒發生」）。
func (h *Handler) ApproveBatch(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	u := uid(r)
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	if len(body.UserIDs) == 0 || len(body.UserIDs) > 50 {
		respondAPIErr(w, errBadJSON)
		return
	}
	type item struct {
		UserID string `json:"user_id"`
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
	}
	results := make([]item, 0, len(body.UserIDs))
	approved, failed := 0, 0
	for _, target := range body.UserIDs {
		if !isValidUUID(target) {
			results = append(results, item{UserID: target, Error: errBadID.Msg})
			failed++
			continue
		}
		if err := h.repo.Approve(r.Context(), u, id, target); err != nil {
			msg := "failed"
			var e *apiErr
			if errors.As(err, &e) {
				msg = e.Msg
			}
			results = append(results, item{UserID: target, Error: msg})
			failed++
			continue
		}
		results = append(results, item{UserID: target, OK: true})
		approved++
		h.notify(r.Context(), target)
	}
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]any{
		"approved": approved, "failed": failed, "results": results})
}

// --- 留言與心情 ---

// requireMember 留言/心情共用的授權：joined 或 owner。回 meetRow 供後續判斷。
func (h *Handler) requireMember(r *http.Request, id string) (meetRow, error) {
	u := uid(r)
	m, err := h.repo.GetMeet(r.Context(), u, id)
	if err != nil {
		return m, err
	}
	if m.OwnerID == u {
		return m, nil
	}
	if m.MyStatus == nil || *m.MyStatus != MemberJoined {
		return m, errNotMember
	}
	return m, nil
}

// GET /run-meets/{id}/comments?limit=&cursor=
// 頂層留言（parent_id IS NULL）游標分頁，依 created_at DESC, id DESC；每則隨附最早
// defReplyPreview 則回覆與 reply_count（migration 159：留言升級為討論串）。
func (h *Handler) ListComments(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	m, err := h.requireMember(r, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	limit := cursorLimit(r, defCommentPageLimit, maxCommentPageLimit)
	items, next, total, err := h.repo.ListComments(r.Context(), id, uid(r), m.OwnerID, limit,
		r.URL.Query().Get("cursor"), defReplyPreview)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next, "total": total})
}

// GET /run-meets/{id}/comments/{cid}/replies?limit=&cursor=
// 某頂層留言的回覆，依 created_at ASC, id ASC 游標分頁（正序：對話由舊到新才讀得順）。
func (h *Handler) ListReplies(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	cid := chi.URLParam(r, "cid")
	if !isValidUUID(cid) {
		respondAPIErr(w, errBadID)
		return
	}
	m, err := h.requireMember(r, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	limit := cursorLimit(r, defCommentPageLimit, maxCommentPageLimit)
	items, next, err := h.repo.ListReplies(r.Context(), id, cid, uid(r), m.OwnerID, limit,
		r.URL.Query().Get("cursor"))
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// POST /run-meets/{id}/comments  {"body":"...", "parent_id": "uuid"|null}
// parent_id 非 null＝回覆；只允許一層，父留言必須是同團練的頂層留言且未刪除（見 thread.go
// validateReplyParent，由 repository.CreateComment 在交易內判定）。
func (h *Handler) CreateComment(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	m, err := h.requireMember(r, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	u := uid(r)
	if !canComment(m.OwnerID == u, m.MyStatus, m.Status, effectiveEnd(m.MeetAt, m.EndsAt)) {
		// 已結束 7 天後留言區唯讀（規格 5.6；回覆同樣受這條唯讀期限制）
		respondAPIErr(w, newErr(http.StatusConflict, "這個團練已結束超過 7 天，留言區已關閉。"))
		return
	}
	var body struct {
		Body     string  `json:"body"`
		ParentID *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	if body.ParentID != nil && !isValidUUID(*body.ParentID) {
		respondAPIErr(w, errBadID)
		return
	}
	text, err := normalizeText(body.Body, MaxCommentRunes, true)
	if err != nil {
		respondAPIErr(w, errCommentLen)
		return
	}
	if text == "" {
		respondAPIErr(w, errCommentEmpty)
		return
	}
	s := loadSettings(r.Context(), h.db)
	c, err := h.repo.CreateComment(r.Context(), id, u, text, body.ParentID, s.CommentDailyCap)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]any{"comment": c})
}

// PUT /run-meets/{id}/comments/{cid}/reaction  {"kind":"like|fire|muscle|pray|heart"}
// body {"kind":null} 或 DELETE 同路徑＝取消。已結束團練仍可按表情（canReact 不含 canComment
// 的唯讀期判定，見 thread.go）；對已軟刪的留言一律 409。
func (h *Handler) PutCommentReaction(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	cid := chi.URLParam(r, "cid")
	if !isValidUUID(cid) {
		respondAPIErr(w, errBadID)
		return
	}
	m, err := h.requireMember(r, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	u := uid(r)
	if !canReact(m.OwnerID == u, m.MyStatus, m.Status) {
		respondAPIErr(w, errMeetCancelled)
		return
	}

	var body struct {
		Kind *string `json:"kind"`
	}
	if r.Method == http.MethodPut {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			respondAPIErr(w, errBadJSON)
			return
		}
	}

	var reactions []ReactionCountView
	var my *string
	if r.Method == http.MethodDelete || body.Kind == nil {
		reactions, my, err = h.repo.RemoveCommentReaction(r.Context(), id, cid, u)
	} else {
		reactions, my, err = h.repo.SetCommentReaction(r.Context(), id, cid, u, *body.Kind)
	}
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"reactions": reactions, "my_reaction": my})
}

// DELETE /run-meets/{id}/comments/{cid}（作者本人或發起人；軟刪）
func (h *Handler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	cid := chi.URLParam(r, "cid")
	if !isValidUUID(cid) {
		respondAPIErr(w, errBadID)
		return
	}
	m, err := h.requireMember(r, id)
	if err != nil {
		respondAPIErr(w, err)
		return
	}
	u := uid(r)
	if err := h.repo.DeleteComment(r.Context(), id, cid, u, m.OwnerID == u); err != nil {
		respondAPIErr(w, err)
		return
	}
	h.notifyMembers(r.Context(), id)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// PUT /run-meets/{id}/reaction  {"kind":"like"}
func (h *Handler) PutReaction(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	if _, err := h.requireMember(r, id); err != nil {
		respondAPIErr(w, err)
		return
	}
	var body struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	if err := h.repo.SetReaction(r.Context(), id, uid(r), body.Kind); err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /run-meets/{id}/reaction
func (h *Handler) DeleteReaction(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	if _, err := h.requireMember(r, id); err != nil {
		respondAPIErr(w, err)
		return
	}
	if err := h.repo.RemoveReaction(r.Context(), id, uid(r)); err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /run-meets/{id}/report  {"comment_id":"...","reason":"..."}
// 任何過入口閘門的登入者都能檢舉（不需為成員）——全站第一個 UGC，檢舉管道要夠寬。
func (h *Handler) Report(w http.ResponseWriter, r *http.Request) {
	id, ok := meetIDParam(r)
	if !ok {
		respondAPIErr(w, errBadID)
		return
	}
	var body struct {
		CommentID string `json:"comment_id"`
		Reason    string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondAPIErr(w, errBadJSON)
		return
	}
	reason, err := normalizeText(body.Reason, MaxReportReasonRunes, true)
	if err != nil {
		respondAPIErr(w, errTooLong)
		return
	}
	if _, err := h.repo.GetMeet(r.Context(), uid(r), id); err != nil {
		respondAPIErr(w, err)
		return
	}
	var commentID *string
	if body.CommentID != "" {
		if !isValidUUID(body.CommentID) {
			respondAPIErr(w, errBadID)
			return
		}
		owner, err := h.repo.CommentMeetID(r.Context(), body.CommentID)
		if err != nil {
			respondAPIErr(w, err)
			return
		}
		if owner != id {
			respondAPIErr(w, errBadID) // 留言不屬於這個團
			return
		}
		commentID = &body.CommentID
	}
	if err := h.repo.CreateReport(r.Context(), id, commentID, uid(r), reason); err != nil {
		respondAPIErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
