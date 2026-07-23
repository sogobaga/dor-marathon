// 取消報名：使用者線上申請 → 後台審核 → 核准後才執行。
//
// 產品設計（見 migrations/095_registration_cancel.sql 開頭註解，勿更動語意）：
//   - 退費依「距賽事天數」分級，費率鎖在「申請當下」（快照存下來，審核時直接用，不重算）。
//   - 核准執行是本功能最容易出錯的地方：分級退費＝部分退款，不可掛在「全額退款才結算」的既有邏輯上；
//     不論退多少錢（含 0 元）都必須完成狀態轉換與庫存回補，且要保證只回補一次——見 Repository.SettleCancellation
//     的大註解（repository.go）。
package race

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/auth"
	"github.com/dor/api/internal/payment"
)

const maxCancelReasonRunes = 500

// truncateReason 用 []rune 截斷（不可用 byte 切，會切壞多位元組的中文字）。
func truncateReason(s string) string {
	s = strings.TrimSpace(s)
	rs := []rune(s)
	if len(rs) > maxCancelReasonRunes {
		rs = rs[:maxCancelReasonRunes]
	}
	return string(rs)
}

// RefundCreatorFunc 由 payment.Handler.CreateRefund 實作／注入，見 Handler.SetRefundCreator。
// race 套件刻意不重寫打綠界那段——核准取消申請時複用既有的退款核心邏輯。
type RefundCreatorFunc func(ctx context.Context, orderID string, amountCents int, reason, operatorAdminID string) (payment.CreateRefundResult, error)

// SetRefundCreator 晚繫結注入退款核心（見 Service.refundCreator 欄位註解：payment.NewHandler 需要
// raceSvc 當 OrderMarker，兩者互相依賴，只能等 main.go 兩邊都建構好之後再用 setter 接起來）。
func (h *Handler) SetRefundCreator(fn RefundCreatorFunc) {
	h.svc.SetRefundCreator(fn)
}

// --- 型別 ---

// CancelRequestRow 取消申請單筆（使用者/後台共用的資料形狀，後台額外附帶使用者與賽事資訊）。
type CancelRequestRow struct {
	ID             string `json:"id"`
	RegistrationID string `json:"registration_id"`
	OrderID        string `json:"order_id,omitempty"`
	UserID         string `json:"user_id"`
	UserName       string `json:"user_name,omitempty"`
	UserEmail      string `json:"user_email,omitempty"`
	RaceTitle      string `json:"race_title,omitempty"`
	Status         string `json:"status"` // pending|processing|approved|rejected（processing 是核准流程的
	// CAS 前置鎖，正常情況下極短暫；只有核准流程中途失敗才會停留在這個狀態，代表需人工介入——見
	// beginCancelRequestProcessing 與 ApproveCancelRequest 註解）
	Reason            string     `json:"reason"`
	DaysBeforeRace    int        `json:"days_before_race"`
	RefundRatio       int        `json:"refund_ratio"`
	RefundAmountCents int        `json:"refund_amount_cents"`
	OrderTotalCents   int        `json:"order_total_cents"`
	ReviewedBy        string     `json:"reviewed_by,omitempty"`
	ReviewedAt        *time.Time `json:"reviewed_at,omitempty"`
	ReviewNote        string     `json:"review_note,omitempty"`
	RefundID          string     `json:"refund_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// --- Repository ---

// regCancelInfo 建立取消申請前，用來重新計算＋驗證的報名/訂單/賽事資訊。
type regCancelInfo struct {
	RegStatus   string
	UserID      string
	RaceStart   time.Time
	RaceConfig  []byte
	OrderID     string
	OrderStatus string
	OrderTotal  int
	HasPending  bool // 是否已有一筆 pending 或 processing 的取消申請（processing 也要擋，見 getRegistrationForCancel 查詢）
}

func (r *Repository) getRegistrationForCancel(ctx context.Context, regID string) (*regCancelInfo, error) {
	info := &regCancelInfo{}
	err := r.db.QueryRow(ctx, `
		SELECT reg.status, reg.user_id, rc.start_date, rc.config,
		       COALESCE(o.id::text,''), COALESCE(o.status,''), COALESCE(o.total_cents,0),
		       EXISTS(SELECT 1 FROM registration_cancel_requests WHERE registration_id=reg.id AND status IN ('pending','processing'))
		FROM registrations reg
		JOIN races rc ON rc.id = reg.race_id
		LEFT JOIN orders o ON o.registration_id = reg.id
		WHERE reg.id = $1`, regID).
		Scan(&info.RegStatus, &info.UserID, &info.RaceStart, &info.RaceConfig,
			&info.OrderID, &info.OrderStatus, &info.OrderTotal, &info.HasPending)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return info, nil
}

// cancelRequestInsert 建立取消申請的欄位（含申請當下的快照）。
type cancelRequestInsert struct {
	RegistrationID    string
	OrderID           string // 可為空字串（無訂單）
	UserID            string
	Reason            string
	DaysBeforeRace    int
	RefundRatio       int
	RefundAmountCents int
	OrderTotalCents   int
}

func (r *Repository) insertCancelRequest(ctx context.Context, in cancelRequestInsert) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO registration_cancel_requests
			(registration_id, order_id, user_id, reason, days_before_race, refund_ratio, refund_amount_cents, order_total_cents)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5, $6, $7, $8)
		RETURNING id::text`,
		in.RegistrationID, in.OrderID, in.UserID, in.Reason,
		in.DaysBeforeRace, in.RefundRatio, in.RefundAmountCents, in.OrderTotalCents).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrCancelRequestPending
		}
		return "", err
	}
	return id, nil
}

// deleteCancelRequestByRegistration 使用者撤回自己的待審申請。回傳是否真的刪到列。
func (r *Repository) deleteCancelRequestByRegistration(ctx context.Context, regID, userID string) (bool, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		DELETE FROM registration_cancel_requests
		WHERE registration_id=$1 AND user_id=$2 AND status='pending'
		RETURNING id::text`, regID, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

const cancelRequestCols = `
	cr.id::text, cr.registration_id::text, COALESCE(cr.order_id::text,''), cr.user_id::text,
	u.name, u.email, rc.title, cr.status, cr.reason,
	COALESCE(cr.days_before_race,0), COALESCE(cr.refund_ratio,0), COALESCE(cr.refund_amount_cents,0), COALESCE(cr.order_total_cents,0),
	COALESCE(cr.reviewed_by::text,''), cr.reviewed_at, cr.review_note,
	COALESCE(cr.refund_id::text,''), cr.created_at`

func scanCancelRequestRow(row pgx.Row) (*CancelRequestRow, error) {
	c := &CancelRequestRow{}
	err := row.Scan(&c.ID, &c.RegistrationID, &c.OrderID, &c.UserID,
		&c.UserName, &c.UserEmail, &c.RaceTitle, &c.Status, &c.Reason,
		&c.DaysBeforeRace, &c.RefundRatio, &c.RefundAmountCents, &c.OrderTotalCents,
		&c.ReviewedBy, &c.ReviewedAt, &c.ReviewNote, &c.RefundID, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// listCancelRequests 後台清單（status=""＝全部）。
func (r *Repository) listCancelRequests(ctx context.Context, status string) ([]CancelRequestRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+cancelRequestCols+`
		FROM registration_cancel_requests cr
		JOIN registrations reg ON reg.id = cr.registration_id
		JOIN races rc ON rc.id = reg.race_id
		JOIN users u ON u.id = cr.user_id
		WHERE ($1 = '' OR cr.status = $1)
		ORDER BY cr.created_at DESC`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CancelRequestRow{}
	for rows.Next() {
		c, err := scanCancelRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// getCancelRequestByID 單筆（審核用）。
func (r *Repository) getCancelRequestByID(ctx context.Context, id string) (*CancelRequestRow, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+cancelRequestCols+`
		FROM registration_cancel_requests cr
		JOIN registrations reg ON reg.id = cr.registration_id
		JOIN races rc ON rc.id = reg.race_id
		JOIN users u ON u.id = cr.user_id
		WHERE cr.id = $1`, id)
	c, err := scanCancelRequestRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// attachCancelRequestRefund 退款一旦建立（不論最終狀態是 success/manual_required/failed/unknown）就
// 立刻把 refund_id 記到申請單上，不等最後核准流程整段做完再一起寫——這樣即使後面的結算/狀態更新步驟
// 出了意外（例如 DB 短暫斷線），這筆退款紀錄的關聯也不會遺失，後台仍查得到。刻意不檢查 status，
// 核准流程本來就只會在還是 pending 時呼叫到這裡。
func (r *Repository) attachCancelRequestRefund(ctx context.Context, id, refundID string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE registration_cancel_requests SET refund_id=$2::uuid, updated_at=NOW() WHERE id=$1`, id, refundID)
	return err
}

// beginCancelRequestProcessing 核准流程的 CAS 前置鎖：pending → processing。這是「同一筆取消申請、
// 不論被核准端點呼叫幾次，退款邏輯只會被觸發一次」的 DB 層保證——ApproveCancelRequest 必須先搶到
// 這把鎖才能繼續往下呼叫 refundCreator；搶不到（RowsAffected=0）代表：
//   - 另一個並發的核准請求正在處理同一筆申請（雙擊/併發），或
//   - 先前一次核准流程中途失敗（例如 attach refund_id 或 SettleCancellation 因暫時性錯誤出錯），
//     留下處於 processing 的殘局。
//
// 兩種情況都【一律拒絕重入】，不嘗試自動恢復重試——自動恢復需要額外的逾時/所有權判斷才能安全區分
// 「還在處理中」與「處理到一半死掉了」，而在會動到真金退款的路徑上，寧可讓卡住的申請停在 processing
// 需要人工介入（直接查 DB／人工建退款後修正狀態），也不要讓系統自動重試而有機會重複退款。
func (r *Repository) beginCancelRequestProcessing(ctx context.Context, id string) (bool, error) {
	var scanned string
	err := r.db.QueryRow(ctx, `
		UPDATE registration_cancel_requests
		SET status='processing', updated_at=NOW()
		WHERE id=$1 AND status='pending'
		RETURNING id::text`, id).Scan(&scanned)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// approveCancelRequestRow CAS：僅從 processing 轉 approved，回傳是否真的轉成功。呼叫前一定要先經過
// beginCancelRequestProcessing 把狀態從 pending 轉成 processing——這裡故意不接受從 pending 直接轉
// approved，避免有其他呼叫路徑繞過前置鎖直接核准。
func (r *Repository) approveCancelRequestRow(ctx context.Context, id, adminID, note, refundID string) (bool, error) {
	var scanned string
	err := r.db.QueryRow(ctx, `
		UPDATE registration_cancel_requests
		SET status='approved', reviewed_by=$2, reviewed_at=NOW(), review_note=$3, refund_id=NULLIF($4,'')::uuid, updated_at=NOW()
		WHERE id=$1 AND status='processing'
		RETURNING id::text`, id, adminID, note, refundID).Scan(&scanned)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// rejectCancelRequestRow CAS：僅從 pending 轉 rejected，回傳是否真的轉成功。
func (r *Repository) rejectCancelRequestRow(ctx context.Context, id, adminID, note string) (bool, error) {
	var scanned string
	err := r.db.QueryRow(ctx, `
		UPDATE registration_cancel_requests
		SET status='rejected', reviewed_by=$2, reviewed_at=NOW(), review_note=$3, updated_at=NOW()
		WHERE id=$1 AND status='pending'
		RETURNING id::text`, id, adminID, note).Scan(&scanned)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// --- Service ---

// CreateCancelRequest 使用者申請取消報名。伺服器端重新計算並快照（絕不信任前端傳來的金額/比例）。
func (s *Service) CreateCancelRequest(ctx context.Context, userID, regID, reason string) (*CancelRequestRow, error) {
	info, err := s.repo.getRegistrationForCancel(ctx, regID)
	if err != nil {
		return nil, fmt.Errorf("load registration: %w", err)
	}
	if info == nil || info.UserID != userID {
		// 不透露「報名存在但不是你的」，統一回 not found。
		return nil, ErrRegistrationNotFound
	}
	if info.RegStatus != "paid" && info.RegStatus != "pending" {
		return nil, ErrCancelRegistrationState
	}
	if info.HasPending {
		return nil, ErrCancelRequestPending
	}

	var cfg RaceConfig
	_ = json.Unmarshal(info.RaceConfig, &cfg) // 壞掉的 config 視同沒有覆寫，不阻擋申請
	sysDefault := appsettings.GetString(ctx, s.repo.db, "cancellation_policy", "")
	policy := ResolveCancellationPolicy(cfg.CancellationPolicy, sysDefault)

	// 未付款訂單沒有錢可退：即使政策算出 ratio>0，退費基準金額也要是 0（見 ComputeCancellation 註解）。
	effectiveAmount := 0
	if info.OrderStatus == "paid" {
		effectiveAmount = info.OrderTotal
	}
	calc := ComputeCancellation(info.RaceStart, time.Now(), effectiveAmount, policy)
	if !calc.CanCancel {
		return nil, fmt.Errorf("%w：%s", ErrCancelDeadlinePassed, calc.BlockedReason)
	}

	id, err := s.repo.insertCancelRequest(ctx, cancelRequestInsert{
		RegistrationID:    regID,
		OrderID:           info.OrderID,
		UserID:            userID,
		Reason:            truncateReason(reason),
		DaysBeforeRace:    calc.DaysBefore,
		RefundRatio:       calc.Ratio,
		RefundAmountCents: calc.RefundAmountCents,
		OrderTotalCents:   info.OrderTotal,
	})
	if err != nil {
		return nil, err
	}
	return s.repo.getCancelRequestByID(ctx, id)
}

// WithdrawCancelRequest 使用者撤回自己的待審申請。
func (s *Service) WithdrawCancelRequest(ctx context.Context, userID, regID string) error {
	found, err := s.repo.deleteCancelRequestByRegistration(ctx, regID, userID)
	if err != nil {
		return err
	}
	if !found {
		return ErrCancelRequestNotFound
	}
	return nil
}

// ListCancelRequests 後台清單。
func (s *Service) ListCancelRequests(ctx context.Context, status string) ([]CancelRequestRow, error) {
	return s.repo.listCancelRequests(ctx, status)
}

// RejectCancelRequest 後台駁回：不做任何退款或狀態變更。
func (s *Service) RejectCancelRequest(ctx context.Context, id, adminID, note string) error {
	ok, err := s.repo.rejectCancelRequestRow(ctx, id, adminID, truncateReason(note))
	if err != nil {
		return err
	}
	if !ok {
		return ErrCancelRequestNotFound
	}
	return nil
}

// CancelApproveResult 核准取消申請的結果，供 handler 組回應用。
type CancelApproveResult struct {
	OrderStatus string `json:"order_status"` // refunded|cancelled|"" (無對應訂單)
	RefundID    string `json:"refund_id,omitempty"`
	RefundNote  string `json:"refund_note,omitempty"`
}

// ApproveCancelRequest 後台核准：執行取消結算（見 Repository.SettleCancellation 的冪等保證），
// 退款金額 > 0 時複用既有退款核心（CreateRefund）建立退款。
//
// 跨請求冪等（防止同一筆申請被重試核准而重複退款——退款是真金流，退兩次＝真的退兩次錢）：
//  0. 呼叫 refundCreator 之前，先用 beginCancelRequestProcessing 做 CAS 前置鎖（pending→processing）。
//     這是「退款只會被觸發一次」的 DB 層保證：搶不到鎖（另一個併發請求正在處理、或前一次核准中途失敗
//     留下的殘局）就直接拒絕重入，回 ErrCancelRequestNotFound，【完全不會】再呼叫一次 refundCreator。
//     0b. 第二道保險：若 cr.RefundID 在搶到鎖的當下就已經非空（正常流程不會發生——pending 狀態下不可能
//     已經有 refund_id；只有人工修過 DB 才可能出現），直接沿用既有的 refund_id、跳過重新呼叫
//     refundCreator，不信任「這筆申請看起來還沒退過款」的表面狀態。
//
// 執行順序其餘部分維持原設計：
//  1. 建立退款（若需要）——此時訂單狀態仍是 paid，CreateRefund 內部「訂單必須是 paid」的檢查才會通過；
//     退款建立無論成功、失敗、或轉人工，都【不會】讓本次核准失敗（見下方 refundNote 處理），且一旦建立
//     就立刻 attachCancelRequestRefund 持久化 refund_id，不等後面的步驟一起做完才存。
//  2. 再呼叫 SettleCancellation 完成狀態轉換＋回補——不論第 1 步的結果如何都會執行，保證「名額一定會
//     放出來」。SettleCancellation 以 registrations.status 的 CAS 為唯一閘門，即使退款流程內部又觸發了
//     既有的 finalizeIfFullyRefunded→MarkOrderRefunded（例如退款比例剛好是 100%），閘門也只會開一次，
//     回補不會重複執行——完整推理見 repository.go SettleCancellation 函式上方的大註解。
//  3. 最後才把申請單從 processing 轉 approved。
//
// 若第 1～3 步中途任何一步失敗而整個函式回傳錯誤，申請單會停在 processing（不是 pending），之後再次
// 呼叫本函式會在第 0 步就被 CAS 擋下，不會重打退款；需要人工介入才能把卡住的申請往下推進。
func (s *Service) ApproveCancelRequest(ctx context.Context, id, adminID, note string) (*CancelApproveResult, error) {
	cr, err := s.repo.getCancelRequestByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load cancel request: %w", err)
	}
	if cr == nil || cr.Status != "pending" {
		return nil, ErrCancelRequestNotFound
	}

	locked, err := s.repo.beginCancelRequestProcessing(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("lock cancel request for processing: %w", err)
	}
	if !locked {
		// 搶不到鎖：已經在處理中，或先前一次核准中途失敗停在 processing。一律拒絕重入，不重打退款。
		return nil, ErrCancelRequestNotFound
	}

	refundID := ""
	refundNote := ""
	// targetOrderStatus 預設 cancelled（含「退 0 元、無需退款」）。只有在退款「真正成功」
	// （success；manual_done 是後續人工結案才會出現的狀態，本函式此處拿不到）時才改標 refunded——
	// 錢還沒真的退成功時（failed/manual_required/unknown，或系統未設定退款服務）維持 cancelled，
	// 讓帳上狀態誠實反映「已取消但退款尚未完成」，可被後續人工追蹤/重試，不會假裝已退款。
	targetOrderStatus := "cancelled"
	if cr.RefundAmountCents > 0 && cr.OrderID != "" {
		switch {
		case cr.RefundID != "":
			// 第二道保險：理論上不會發生（剛從 pending CAS 搶到 processing 鎖，pending 狀態下
			// refund_id 不該已經有值），但若真的發生，寧可信任既有的 refund_id、跳過重新建立退款，
			// 也不要冒重複退款的風險。這裡沒有重新查詢該筆既有退款紀錄的實際結果，保守維持
			// targetOrderStatus=cancelled，避免在不確定退款是否成功時就標成 refunded。
			refundID = cr.RefundID
			refundNote = "偵測到此申請先前已建立過退款紀錄，略過重新建立退款（請人工核對該筆退款實際結果）"
		case s.refundCreator == nil:
			refundNote = "系統未設定退款服務，請人工建立退款"
		default:
			reason := fmt.Sprintf("使用者取消申請核准退款（取消申請 %s）", cr.ID)
			res, rErr := s.refundCreator(ctx, cr.OrderID, cr.RefundAmountCents, reason, adminID)
			if rErr != nil {
				// 退款建立失敗（例如未滿 24h 轉人工判斷之外的硬性錯誤：查無已付款交易、關帳時段…）
				// 不可讓整個核准失敗——取消結算仍要往下走，名額要放出來。留言讓後台知道要人工處理。
				refundNote = "自動建立退款失敗，需人工處理：" + rErr.Error()
			} else {
				refundID = res.RefundID
				refundNote = "退款狀態：" + res.Status
				if res.Note != "" {
					refundNote += "（" + res.Note + "）"
				}
				if res.Status == "success" || res.Status == "manual_done" {
					targetOrderStatus = "refunded"
				}
			}
		}
	}
	if refundID != "" {
		// 退款紀錄一旦建立就立刻掛回申請單，不等下面的結算/最終核准狀態一起寫完才存——即使後面任何
		// 一步意外失敗，這筆退款的關聯也不會遺失（見 attachCancelRequestRefund 註解）。
		if err := s.repo.attachCancelRequestRefund(ctx, id, refundID); err != nil {
			return nil, fmt.Errorf("attach refund to cancel request: %w", err)
		}
	}

	if cr.RegistrationID != "" {
		if _, err := s.repo.SettleCancellation(ctx, cr.RegistrationID, targetOrderStatus); err != nil {
			return nil, fmt.Errorf("settle cancellation: %w", err)
		}
	}

	finalNote := truncateReason(note)
	if refundNote != "" {
		finalNote = truncateReason(finalNote + "\n[系統] " + refundNote)
	}
	ok, err := s.repo.approveCancelRequestRow(ctx, id, adminID, finalNote, refundID)
	if err != nil {
		return nil, fmt.Errorf("mark cancel request approved: %w", err)
	}
	if !ok {
		// 已驗證過 processing 才走到這裡，理論上不會發生（除非申請單被人工直接改了狀態的極端情況）。
		return nil, ErrCancelRequestNotFound
	}

	return &CancelApproveResult{OrderStatus: targetOrderStatus, RefundID: refundID, RefundNote: refundNote}, nil
}

// --- Handler：使用者端 ---

// CreateCancelRequest POST /api/v1/profile/registrations/{registrationID}/cancel-request  {"reason":"..."}
func (h *Handler) CreateCancelRequest(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	regID := chi.URLParam(r, "registrationID")
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	row, err := h.svc.CreateCancelRequest(r.Context(), userID, regID, req.Reason)
	switch {
	case errors.Is(err, ErrRegistrationNotFound):
		respondErr(w, http.StatusNotFound, "registration not found")
	case errors.Is(err, ErrCancelRegistrationState):
		respondErr(w, http.StatusConflict, ErrCancelRegistrationState.Error())
	case errors.Is(err, ErrCancelRequestPending):
		respondErr(w, http.StatusConflict, ErrCancelRequestPending.Error())
	case errors.Is(err, ErrCancelDeadlinePassed):
		respondErr(w, http.StatusConflict, err.Error())
	case err != nil:
		respondErr(w, http.StatusInternalServerError, "failed to create cancel request")
	default:
		respondJSON(w, http.StatusOK, row)
	}
}

// WithdrawCancelRequest DELETE /api/v1/profile/registrations/{registrationID}/cancel-request
func (h *Handler) WithdrawCancelRequest(w http.ResponseWriter, r *http.Request) {
	userID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	if userID == "" {
		respondErr(w, http.StatusUnauthorized, "login required")
		return
	}
	regID := chi.URLParam(r, "registrationID")
	err := h.svc.WithdrawCancelRequest(r.Context(), userID, regID)
	switch {
	case errors.Is(err, ErrCancelRequestNotFound):
		respondErr(w, http.StatusNotFound, "沒有待審的取消申請")
	case err != nil:
		respondErr(w, http.StatusInternalServerError, "failed to withdraw cancel request")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- Handler：後台審核 ---

// CancelRequestAdminRouter 取消申請審核路由（掛載在 /api/v1/admin/cancel-requests，沿用 orders 權限）。
func (h *Handler) CancelRequestAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.AdminListCancelRequests)
	r.Patch("/{id}/approve", h.AdminApproveCancelRequest)
	r.Patch("/{id}/reject", h.AdminRejectCancelRequest)
	return r
}

// GET /api/v1/admin/cancel-requests?status=pending
func (h *Handler) AdminListCancelRequests(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := h.svc.ListCancelRequests(r.Context(), status)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list cancel requests")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"cancel_requests": list, "count": len(list)})
}

// PATCH /api/v1/admin/cancel-requests/{id}/approve
func (h *Handler) AdminApproveCancelRequest(w http.ResponseWriter, r *http.Request) {
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	id := chi.URLParam(r, "id")
	var req struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	result, err := h.svc.ApproveCancelRequest(r.Context(), id, adminID, req.Note)
	switch {
	case errors.Is(err, ErrCancelRequestNotFound):
		// 涵蓋三種情況：申請不存在、已審核完畢、或正在處理中／前次核准中途失敗卡在 processing
		// （見 Service.ApproveCancelRequest 的跨請求冪等鎖）——一律回一樣的訊息，不區分細節。
		respondErr(w, http.StatusNotFound, "取消申請不存在或已審核")
	case err != nil:
		respondErr(w, http.StatusInternalServerError, "failed to approve cancel request")
	default:
		respondJSON(w, http.StatusOK, result)
	}
}

// PATCH /api/v1/admin/cancel-requests/{id}/reject  {"note":"..."}
func (h *Handler) AdminRejectCancelRequest(w http.ResponseWriter, r *http.Request) {
	adminID, _ := r.Context().Value(auth.CtxKeyUserID).(string)
	id := chi.URLParam(r, "id")
	var req struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	err := h.svc.RejectCancelRequest(r.Context(), id, adminID, req.Note)
	switch {
	case errors.Is(err, ErrCancelRequestNotFound):
		respondErr(w, http.StatusNotFound, "取消申請不存在或已審核")
	case err != nil:
		respondErr(w, http.StatusInternalServerError, "failed to reject cancel request")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
