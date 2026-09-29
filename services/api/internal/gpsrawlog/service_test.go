package gpsrawlog

import (
	"strings"
	"testing"
)

func TestWhitelisted_CommaSeparated(t *testing.T) {
	if !whitelisted("a@example.com,b@example.com", "b@example.com") {
		t.Fatal("expected comma-separated match")
	}
}

func TestWhitelisted_CaseInsensitive(t *testing.T) {
	if !whitelisted("Sogobaga@Gmail.com", "sogobaga@gmail.com") {
		t.Fatal("expected case-insensitive match")
	}
	if !whitelisted("sogobaga@gmail.com", "SOGOBAGA@GMAIL.COM") {
		t.Fatal("expected case-insensitive match (caller email uppercase)")
	}
}

func TestWhitelisted_TrimsWhitespace(t *testing.T) {
	if !whitelisted(" a@example.com , b@example.com ", "b@example.com") {
		t.Fatal("expected whitespace to be trimmed around tokens")
	}
	if !whitelisted("a@example.com", "  a@example.com  ") {
		t.Fatal("expected whitespace to be trimmed on caller email")
	}
}

func TestWhitelisted_NewlineAndSemicolonSeparated(t *testing.T) {
	list := "a@example.com\nb@example.com;c@example.com"
	for _, e := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		if !whitelisted(list, e) {
			t.Fatalf("expected %q to be whitelisted", e)
		}
	}
}

func TestWhitelisted_NotFound(t *testing.T) {
	if whitelisted("a@example.com,b@example.com", "c@example.com") {
		t.Fatal("expected no match")
	}
}

func TestWhitelisted_EmptyEmailAlwaysFalse(t *testing.T) {
	if whitelisted("a@example.com", "") {
		t.Fatal("expected empty email to never match")
	}
	if whitelisted("a@example.com", "   ") {
		t.Fatal("expected blank email to never match")
	}
}

func TestWhitelisted_EmptyListAlwaysFalse(t *testing.T) {
	if whitelisted("", "a@example.com") {
		t.Fatal("expected empty whitelist to match nobody")
	}
}

func TestWhitelisted_DefaultOwnerEmail(t *testing.T) {
	// 契約 B：「缺鍵＝sogobaga@gmail.com」——確認預設值本身能通過 whitelisted() 解析（Allowed()
	// 在缺鍵時會把 defaultWhitelist 當成 list 傳進來）。
	if !whitelisted(defaultWhitelist, "sogobaga@gmail.com") {
		t.Fatal("expected default whitelist to contain the owner's email")
	}
}

func TestRetentionDeleteSQL(t *testing.T) {
	sql, days := retentionDeleteSQL()
	if days != 30 {
		t.Fatalf("expected retention days=30, got %d", days)
	}
	if sql == "" {
		t.Fatal("expected non-empty SQL")
	}
	// 斷言關鍵子句都在：目標表、時間比較欄位、參數化的 interval（不是字串拼接的字面值）。
	for _, want := range []string{"DELETE FROM gps_run_raw_points", "created_at <", "make_interval(days => $1)"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("expected SQL to contain %q, got: %s", want, sql)
		}
	}
}
