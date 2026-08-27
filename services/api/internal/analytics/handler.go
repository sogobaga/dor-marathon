package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// apiStaleAfter：GET /admin/analytics/report 回傳的 stale 旗標門檻（任務規格：「超過 48h 未算」）。
// 與 schedule.go 的 catchUpStaleAfter（25h，排程啟動補跑判斷）刻意不同——後者是「多久沒算就該儘快
// 補跑」，前者是「多久沒算就該在後台 UI 上提醒管理者這份報告可能過時」，兩者用途不同、門檻沒有必要
// 綁在一起。
const apiStaleAfter = 48 * time.Hour

// recomputeTimeout：POST /admin/analytics/recompute 的硬性逾時（任務規格：20s 內完成）。
const recomputeTimeout = 20 * time.Second

// Handler 會員活躍度分析：GET 讀最新一筆存檔報告／POST 立即重算並存檔；背景每日排程見 schedule.go。
type Handler struct {
	db *pgxpool.Pool

	mu          sync.Mutex
	lastRunDate string // 台灣日期 YYYY-MM-DD：schedule.go 排程當日冪等的 in-memory 標記
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// GetReport GET /admin/analytics/report：讀最新一筆存檔報告（不即時重算——即時重算走 POST
// recompute）。表尚無任何一列時（migration 剛上線、排程尚未跑過、也沒人手動觸發過）回傳
// report:null、stale:true，讓前台顯示「尚無資料，請觸發重新計算」而不是回一個容易誤讀的 404。
func (h *Handler) GetReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw, computedAt, err := LatestReport(ctx, h.db)
	if errors.Is(err, ErrNoReport) {
		respondJSON(w, http.StatusOK, map[string]any{"report": nil, "stale": true})
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load report")
		return
	}
	stale := time.Since(computedAt) > apiStaleAfter
	respondJSON(w, http.StatusOK, map[string]any{"report": json.RawMessage(raw), "stale": stale})
}

// Recompute POST /admin/analytics/recompute：立即重算六大區塊並存檔，回傳同一份 report（一律
// stale:false，因為剛算完）。20 秒硬性逾時（recomputeTimeout）：BuildReport 六個區塊共用同一個
// 帶逾時的 ctx，逾時後尚未跑到的區塊會在各自的 db 查詢上收到 context deadline exceeded，被
// BuildReport 的單區塊容錯機制接住、以空區塊落地，不會讓整個 HTTP 請求掛住超過 20 秒（實際觀察
// 下，本站現有會員規模的六大區塊全表 population 掃描應能在秒級完成，20 秒是充裕寬限；若未來
// 會員數大幅成長導致經常性逾時，需要的是把幾個 population LEFT JOIN 查詢拆成分批或加物化視圖，
// 而不是單純拉長這個常數）。
func (h *Handler) Recompute(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), recomputeTimeout)
	defer cancel()

	rpt := BuildReport(ctx, h.db)
	if err := SaveReport(ctx, h.db, rpt); err != nil {
		log.Error().Err(err).Msg("member analytics: recompute save failed")
		respondErr(w, http.StatusInternalServerError, "failed to save report: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"report": rpt, "stale": false})
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
