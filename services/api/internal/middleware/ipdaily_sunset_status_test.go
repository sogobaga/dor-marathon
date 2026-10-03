package middleware

// 「Terra／Strava 串接結束公告」被擋下的 API 回應狀態碼（wearablesunset.RefusalStatus）不得被每日資安報告當成登入失敗：
// Record 把 401／403 計入 auth_fails（每日 08:00 報告列出 ≥20 次的 IP 為「異常登入失敗」）、404 計入 not_found。
// 用真的聚合器量，而不是解析原始碼；對照組 403 確認這個測試真的有在量東西。

import (
	"net/http"
	"testing"
	"time"

	"github.com/dor/api/internal/integration/wearablesunset"
)

func TestIPDaily_SunsetRefusalIsNotAnAuthFailure(t *testing.T) {
	a := NewIPDailyAggregate(nil)
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 25; i++ {
		a.Record(now, "203.0.113.7", "TW", wearablesunset.RefusalStatus)
	}
	c := a.data["203.0.113.7"]
	if c == nil || c.requests != 25 {
		t.Fatalf("the refusal requests must still be counted as requests: %+v", c)
	}
	if c.authFails != 0 || c.notFound != 0 {
		t.Fatalf("status %d must not count as auth_fails/not_found (got auth_fails=%d not_found=%d): a shared-NAT IP would show up as an abnormal login-failure IP in the daily report",
			wearablesunset.RefusalStatus, c.authFails, c.notFound)
	}

	// 對照組：403 會被算成登入失敗
	a.Record(now, "203.0.113.8", "TW", http.StatusForbidden)
	if g := a.data["203.0.113.8"]; g == nil || g.authFails != 1 {
		t.Fatalf("control: 403 must count as an auth failure, got %+v", g)
	}
}
