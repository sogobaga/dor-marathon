package gpsrawlog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dor/api/internal/auth"
)

// TestUploadRawPoints_Unauthorized：未登入（context 內沒有 userID）→ 401，且完全不碰 DB（h.db 給 nil
// 也不會 panic——這行必須排在任何 DB 存取之前，見 handler.go UploadRawPoints 的檢查順序）。
func TestUploadRawPoints_Unauthorized(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/some-run-id/raw-points", strings.NewReader(`{}`))
	rw := httptest.NewRecorder()
	h.UploadRawPoints(rw, req)
	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

// TestUploadRawPoints_InvalidJSON：登入但 body 不是合法 JSON → 400，且不碰 DB（json 解碼失敗發生在
// email 查詢之前）。
func TestUploadRawPoints_InvalidJSON(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/some-run-id/raw-points", strings.NewReader(`not json`))
	req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, "u1"))
	rw := httptest.NewRecorder()
	h.UploadRawPoints(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid json, got %d", rw.Code)
	}
}

// TestUploadRawPoints_ValidationFailure：合法 JSON 但欄位不合法（列長度不對）→ 400，同樣不碰 DB
// （Validate() 發生在 lookupEmail 之前，見 handler.go 的檢查順序）。
func TestUploadRawPoints_ValidationFailure(t *testing.T) {
	h := NewHandler(nil)
	body := `{"v":1,"rows":[[1000,25.0,121.5]]}` // 只有 3 個欄位，應為 7
	req := httptest.NewRequest(http.MethodPost, "/some-run-id/raw-points", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, "u1"))
	rw := httptest.NewRecorder()
	h.UploadRawPoints(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for validation failure, got %d", rw.Code)
	}
}

// TestUploadRawPoints_WrongVersionValidationFailure：v 不是 1 → 400（不碰 DB）。
func TestUploadRawPoints_WrongVersionValidationFailure(t *testing.T) {
	h := NewHandler(nil)
	body := `{"v":2,"rows":[]}`
	req := httptest.NewRequest(http.MethodPost, "/x/raw-points", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, "u1"))
	rw := httptest.NewRecorder()
	h.UploadRawPoints(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong version, got %d", rw.Code)
	}
}
