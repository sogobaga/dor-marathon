package monopoly

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Router 掛 /monopoly（需登入，由呼叫端的 RequireAuth 群組保證）。另掛 requireEntry：
// 修 SEC-H5——先前入口白名單只有前端 MemberPanel 擋 UI，非白名單會員直接打 API 仍可繞過
// （含免費刷 GP 擲骰、申請兌換實體公仔）。requireEntry 在後端把「入口狀態」複查一次，
// 非 shown（且非 super_admin）一律 403，語意對齊 profile/membership.go 的 resolveEntry。
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(h.requireEntry)
	r.Get("/state", h.State)
	r.Post("/roll", h.Roll)
	r.Get("/knowledge", h.Knowledge)
	r.Get("/stickers", h.Stickers)
	r.Post("/figure/redeem", h.RedeemFigure)
	return r
}

// requireEntry 後端強制 monopoly 入口白名單（SEC-H5）。獨立實作於 monopoly 套件內（而非
// import profile 的未匯出 resolveEntry），語意保持一致：讀 app_settings
// monopoly_entry_state/monopoly_entry_whitelist + 該使用者 email/account_code/is_super_admin，
// super_admin 一律放行；state=open 放行；state=whitelist 命中白名單放行；其餘
// （含 hidden/locked/未設定）一律 403 ——「locked」在前端是顯示但不可按，對應到後端一樣不放行。
func (h *Handler) requireEntry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
		if uid == "" {
			respondErr(w, http.StatusUnauthorized, "login required")
			return
		}
		var email, code string
		var isSuperAdmin bool
		if err := h.svc.repo.db.QueryRow(r.Context(), `
			SELECT COALESCE(email,''), COALESCE(account_code,''), is_super_admin
			FROM users WHERE id=$1`, uid).Scan(&email, &code, &isSuperAdmin); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to resolve access")
			return
		}
		if !monopolyEntryAllowed(r.Context(), h.svc.repo.db, email, code, isSuperAdmin) {
			respondErr(w, http.StatusForbidden, "monopoly not available")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// monopolyEntryAllowed 判定該使用者是否為「shown」狀態，可存取 monopoly API。
func monopolyEntryAllowed(ctx context.Context, db *pgxpool.Pool, email, code string, isSuperAdmin bool) bool {
	if isSuperAdmin {
		return true
	}
	switch appsettings.GetString(ctx, db, "monopoly_entry_state", "hidden") {
	case "open":
		return true
	case "whitelist":
		return monopolyWhitelisted(appsettings.GetString(ctx, db, "monopoly_entry_whitelist", ""), email, code)
	default: // hidden 或 locked 或未設定 → 不放行
		return false
	}
}

// monopolyWhitelisted 與 profile/membership.go 的 personalWhitelisted 同語意：白名單以
// 換行/逗號/分號/空白分隔，可填帳號編碼（#可省）或 email，大小寫不敏感。
func monopolyWhitelisted(list, email, code string) bool {
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

// GET /api/v1/monopoly/state
func (h *Handler) State(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	st, err := h.svc.GetState(r.Context(), uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get monopoly state")
		return
	}
	respondJSON(w, http.StatusOK, st)
}

// POST /api/v1/monopoly/roll —— 扣 GP、伺服器決定點數、移動棋子；GP 不足直接 409、絕不扣款。
func (h *Handler) Roll(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	res, err := h.svc.Roll(r.Context(), uid)
	if errors.Is(err, ErrInsufficientGP) {
		respondErr(w, http.StatusConflict, "GP 不足")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "擲骰失敗，請稍後再試")
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// GET /api/v1/monopoly/knowledge —— 知識卡圖鑑（供 Phase 2a 圖鑑頁）；防劇透邏輯見
// Repository.GetKnowledgeCards：未擁有的卡片只回 id/theme/main_category/rarity/owned/obtained_count。
func (h *Handler) Knowledge(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	gallery, err := h.svc.GetKnowledgeCards(r.Context(), uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get knowledge cards")
		return
	}
	respondJSON(w, http.StatusOK, gallery)
}

// GET /api/v1/monopoly/stickers —— 完賽公仔九宮格收集狀態（Phase 2b）；貼紙不需防劇透，見
// Repository.GetStickerPieces：全部 pieces 一律回 gray_url，owned 只影響收集狀態顯示。
func (h *Handler) Stickers(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	gallery, err := h.svc.GetStickers(r.Context(), uid)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to get stickers")
		return
	}
	respondJSON(w, http.StatusOK, gallery)
}

// POST /api/v1/monopoly/figure/redeem —— 完賽公仔兌換申請（A）：伺服器驗證已集滿全部九宮格貼紙才
// 可申請，未集滿回 400。已申請過則冪等回現況（見 Service.RedeemFigure / Repository.EnsureFigureRedemption）。
func (h *Handler) RedeemFigure(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if uid == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	status, err := h.svc.RedeemFigure(r.Context(), uid)
	if errors.Is(err, ErrStickersIncomplete) {
		respondErr(w, http.StatusBadRequest, "尚未集滿")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to submit redemption")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": status})
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
