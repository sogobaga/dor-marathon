// member_handler.go：會員端「即時查驗」端點（POST /invoice/verify）。與 handler.go 的
// AdminHandler 分開一個型別/檔案——這支端點不需要 admin 權限、也不碰 order_invoices 表，純粹是
// 把使用者這次輸入的手機條碼/愛心碼丟給 ECPay CheckBarcode/CheckLoveCode 查一次（見 verify.go），
// 讓報名表單能在送出前就提醒「格式對但號碼打錯字」。掛載方式見 cmd/api/main.go：與其他需要登入
// 的會員端點同一組（RequireAuth）＋獨立 RateLimit（20/min，UserOrIP）。
package einvoice

import (
	"encoding/json"
	"net/http"
)

// MemberHandler 會員端電子發票查驗端點。
type MemberHandler struct {
	client *Client
	env    string
}

// NewMemberHandler 建構子。與 NewIssuer 各自持有獨立的 *Client 實例——兩者都是無狀態薄 wrapper，
// 共用同一組 Config 即可，不需要是同一個物件（比照 handler.go NewAdminHandler 對 Repository 的
// 說明）。
func NewMemberHandler(cfg Config) *MemberHandler {
	return &MemberHandler{client: NewClient(cfg), env: cfg.Env}
}

// Verify POST /invoice/verify {"type":"mobile"|"love_code","value":"..."}
// 回應 200 一律成功（唯讀查驗本身不算「失敗」，是否查到答案由 body 的 checked/exists 表達，
// 見 VerifyResult）——前端不需要另外處理錯誤 HTTP 狀態碼分支。
func (h *MemberHandler) Verify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type != "mobile" && req.Type != "love_code" {
		respondErr(w, http.StatusBadRequest, "type 僅限 mobile 或 love_code")
		return
	}
	result := verifyMobileOrLoveCode(r.Context(), h.client, h.env, req.Type, req.Value)
	respondJSON(w, http.StatusOK, result)
}
