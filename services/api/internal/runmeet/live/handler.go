package live

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/dor/api/internal/middleware"
)

// handler.go：熱路徑 HTTP 端點（POST /{id}/pos、POST /{id}/leave）。
//
// ⚠️ 零 PostgreSQL 查詢：Handler 只持有 *Store（Redis），沒有任何資料庫連線——「熱路徑不碰 DB」是
// 型別保證，不是紀律（見 live_arch_test.go）。RequireAuth 的 session 快取除外（上層群組既有成本）。
// ⚠️ 不記 log：請求 body／座標都不得出現在任何 log（本檔不 import 任何 logger）。

// Handler /api/v1/run-meet-live/* 的 HTTP 層。
type Handler struct{ store *Store }

// NewHandler 以 Redis client 建立 handler（rdb 為 nil 時所有端點回 503，fail-closed）。
func NewHandler(rdb *redis.Client) *Handler { return &Handler{store: NewStore(rdb)} }

// NewHandlerWithStore 以既有 Store 建立 handler（runmeet 共用同一個 Store，測試也用）。
func NewHandlerWithStore(s *Store) *Handler { return &Handler{store: s} }

// Router 掛在 main.go 已登入群組下的 /run-meet-live。
//
// 這個子路由刻意**不**經 runmeet 的 requireEntry（它每請求查 DB）。RequireAuth 由上層群組提供；
// 這裡的 requireUser 是縱深防禦：萬一有人把子路由掛到 RequireAuth 之外，所有請求會 401，
// 而不是讓 RateLimit 的 UserOrIP 靜默退回 IP 維度（50 人同出口 IP 共用一個 60/分的桶，現場直接被限流）。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(requireUser)
	r.Post("/{id}/pos", h.Pos)
	r.Post("/{id}/leave", h.Leave)
	return r
}

func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if middleware.GetUserID(r.Context()) == "" {
			WriteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- 共用工具（runmeet 的 /live/start 也用）---

// WriteJSON 寫 JSON 回應（Cache-Control: no-store：位置資料不得被任何中介快取）。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError 回 {"error":"<code>"}。
func WriteError(w http.ResponseWriter, status int, code string) {
	WriteJSON(w, status, map[string]string{"error": code})
}

// ParseMeetID 路徑 {id} → 規範 UUID 字串（契約 §3：handler 第一行；非法 → 404，Redis key 一律用規範字串）。
func ParseMeetID(r *http.Request) (string, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return "", false
	}
	return id.String(), true
}

// UserUUID 從 ctx 取已登入使用者並收斂成規範 UUID（Redis key 與撤銷掛鉤用同一個寫法）。
func UserUUID(r *http.Request) (string, bool) {
	s := middleware.GetUserID(r.Context())
	if s == "" {
		return "", false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return "", false
	}
	return id.String(), true
}

// ReadJSON 讀 body（上限 MaxBodyBytes）並解成 dst；未知欄位忽略。失敗回 (status, code)，成功回 (0, "")。
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) (int, string) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return http.StatusRequestEntityTooLarge, "payload_too_large"
		}
		return http.StatusBadRequest, "bad_request"
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return http.StatusBadRequest, "bad_request"
	}
	return 0, ""
}

// ValidSID client 每次 start 隨機產生的工作階段碼：16–32 個 [A-Za-z0-9]（不含 `|`，
// Lua 以 `n|sid|expMs` 儲存，分隔字元不得出現在 sid 內）。
func ValidSID(s string) bool {
	if len(s) < 16 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// ReauthInS = grant 剩餘秒數 − 300（最小 0）；client 在 ≤ 0 時靜默 start(reauth=true)。
func ReauthInS(expMs, nowMs int64) int {
	s := (expMs-nowMs)/1000 - int64(ReauthMargin/time.Second)
	if s < 0 {
		return 0
	}
	return int(s)
}

// --- POST /{id}/pos ---

type posRequest struct {
	PV  int     `json:"pv"`
	SID string  `json:"sid"`
	RV  int     `json:"rv"`
	P   *posFix `json:"p"`
}

// posFix 一個定位點。四個欄位全必填（p 本身可省略＝只心跳／只輪詢）。
type posFix struct {
	La *float64 `json:"la"`
	Ln *float64 `json:"ln"`
	Ac *float64 `json:"ac"`
	Fa *float64 `json:"fa"`
}

// validate la∈[-90,90]、ln∈[-180,180]、ac∈[0,10000]、fa∈[0,600]（不含 NaN／Inf）。
func (f *posFix) validate() (laE5, lnE5 int64, acc, fa int, ok bool) {
	if f == nil || f.La == nil || f.Ln == nil || f.Ac == nil || f.Fa == nil {
		return 0, 0, 0, 0, false
	}
	in := func(v, lo, hi float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= lo && v <= hi }
	if !in(*f.La, -90, 90) || !in(*f.Ln, -180, 180) || !in(*f.Ac, 0, 10000) || !in(*f.Fa, 0, 600) {
		return 0, 0, 0, 0, false
	}
	return int64(math.Round(*f.La * 1e5)), int64(math.Round(*f.Ln * 1e5)),
		int(math.Round(*f.Ac)), int(math.Round(*f.Fa)), true
}

// posResponse 契約 §3.2 的 200 回應。
//
// ⚠️ 結構白名單：沒有 user_id／account_code／email，也沒有任何歷史或軌跡欄位；他人只以短編號 n 出現
// （契約驗收 P1-5）。新增欄位前先想清楚這點。
type posResponse struct {
	PV        int           `json:"pv"`
	T         int64         `json:"t"`
	IV        int           `json:"iv"`
	Live      int           `json:"live"`
	RV        int           `json:"rv"`
	Own       string        `json:"own"`
	ReauthInS int           `json:"reauth_in_s"`
	Roster    []RosterEntry `json:"roster,omitempty"` // 僅當請求帶的 rv ≠ 伺服器 rv
	// P 是 any：need_own_fix／ok 時是 []Peer（空時序列化成 []）；presence_only 時為 nil → 整個欄位省略
	// （不限地點團「永不含 p」）。*[]Peer 或 omitempty 的 slice 都做不到「空陣列要輸出、nil 要省略」。
	P any `json:"p,omitempty"`
}

// Pos POST /api/v1/run-meet-live/{id}/pos
func (h *Handler) Pos(w http.ResponseWriter, r *http.Request) {
	meetID, ok := ParseMeetID(r)
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	uid, ok := UserUUID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req posRequest
	if status, code := ReadJSON(w, r, &req); status != 0 {
		WriteError(w, status, code)
		return
	}
	if req.PV != ProtocolVersion {
		WriteError(w, http.StatusUpgradeRequired, "upgrade_required")
		return
	}
	if !ValidSID(req.SID) {
		WriteError(w, http.StatusBadRequest, "bad_sid")
		return
	}
	p := PosParams{UID: uid, SID: req.SID, ClientRV: req.RV}
	if req.P != nil {
		la, ln, ac, fa, valid := req.P.validate()
		if !valid {
			WriteError(w, http.StatusBadRequest, "bad_position")
			return
		}
		p.HasP, p.LaE5, p.LnE5, p.Acc, p.FaS = true, la, ln, ac, fa
	}

	res, err := h.store.Pos(r.Context(), meetID, p)
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "redis_unavailable")
		return
	}
	switch res.Outcome {
	case OutcomeOK, OutcomeNeedOwnFix, OutcomePresenceOnly:
		out := posResponse{
			PV: ProtocolVersion, T: res.NowMs, IV: IntervalMs(res.Live), Live: res.Live, RV: res.RV,
			Own: string(res.Outcome), ReauthInS: ReauthInS(res.ExpMs, res.NowMs),
		}
		if res.RosterSent {
			out.Roster = res.Roster
		}
		if res.Outcome != OutcomePresenceOnly {
			peers := res.Peers
			if res.Outcome == OutcomeNeedOwnFix || peers == nil {
				peers = []Peer{} // 互惠規則：沒分享就沒得看——p 一律空陣列
			}
			out.P = peers
		}
		WriteJSON(w, http.StatusOK, out)
	case OutcomeTooFast:
		retry := (res.RetryAfterMs + 999) / 1000
		if retry < 1 {
			retry = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retry, 10))
		WriteError(w, http.StatusTooManyRequests, "too_fast")
	default:
		status, code := StatusForOutcome(res.Outcome)
		WriteError(w, status, code)
	}
}

// StatusForOutcome 非成功結果 → (HTTP 狀態, error 碼)（契約 §3.1／§3.2 的錯誤表；start 與 pos 共用）。
func StatusForOutcome(o Outcome) (int, string) {
	switch o {
	case OutcomeGrantMissing:
		return http.StatusConflict, "grant_missing"
	case OutcomeSIDMismatch:
		return http.StatusConflict, "sid_mismatch"
	case OutcomeRevoked, OutcomeRevokedRecent:
		return http.StatusForbidden, "revoked"
	case OutcomeDead:
		return http.StatusGone, "meet_over"
	case OutcomeKilled:
		return http.StatusGone, "killed"
	case OutcomeLiveFull:
		return http.StatusTooManyRequests, "live_full"
	case OutcomeTooFast:
		return http.StatusTooManyRequests, "too_fast"
	}
	return http.StatusServiceUnavailable, "redis_unavailable"
}

// --- POST /{id}/leave ---

type leaveRequest struct {
	PV  int    `json:"pv"`
	SID string `json:"sid"`
}

// Leave POST /api/v1/run-meet-live/{id}/leave → 204。
//
// 僅 sid 相符才刪 grant＋pos／hb；sid 不符也回 204（fire-and-forget，不洩漏任何狀態）；**不寫墓碑**
// （M5）。client 用 fetch(..., {keepalive:true})，失敗無妨。
func (h *Handler) Leave(w http.ResponseWriter, r *http.Request) {
	meetID, ok := ParseMeetID(r)
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found")
		return
	}
	uid, ok := UserUUID(r)
	if !ok {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req leaveRequest
	if status, code := ReadJSON(w, r, &req); status != 0 {
		WriteError(w, status, code)
		return
	}
	if req.PV != ProtocolVersion {
		WriteError(w, http.StatusUpgradeRequired, "upgrade_required")
		return
	}
	if !ValidSID(req.SID) {
		WriteError(w, http.StatusBadRequest, "bad_sid")
		return
	}
	if _, err := h.store.Leave(r.Context(), meetID, uid, req.SID); err != nil {
		WriteError(w, http.StatusServiceUnavailable, "redis_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
