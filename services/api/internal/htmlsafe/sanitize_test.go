package htmlsafe

import (
	"strings"
	"testing"
)

// TestSanitize_TableStructureSurvives 確保簡章/跑者充電站常見的表格排版（含數字型
// border/cellpadding 屬性）不會再被整段剝除。
func TestSanitize_TableStructureSurvives(t *testing.T) {
	in := `<table border="1" cellpadding="8"><tr><td>A</td><td>B</td></tr></table>`
	out := Sanitize(in)

	for _, want := range []string{"<table", "<tr>", "<td>A</td>", "<td>B</td>", "border=\"1\"", "cellpadding=\"8\""} {
		if !strings.Contains(out, want) {
			t.Errorf("Sanitize(%q) = %q; want it to contain %q", in, out, want)
		}
	}
}

// TestSanitize_TableColspanRowspanSurvive 確保 td/th 的數字型 colspan/rowspan 保留。
func TestSanitize_TableColspanRowspanSurvive(t *testing.T) {
	in := `<table><thead><tr><th colspan="2">標題</th></tr></thead><tbody><tr><td rowspan="2">A</td></tr></tbody></table>`
	out := Sanitize(in)

	for _, want := range []string{"<thead>", "<tbody>", `colspan="2"`, `rowspan="2"`} {
		if !strings.Contains(out, want) {
			t.Errorf("Sanitize(%q) = %q; want it to contain %q", in, out, want)
		}
	}
}

// TestSanitize_TableDangerousAttrsStripped 確保 onclick/style 等危險屬性即使掛在
// table/td 上也一律被剝除，只留下乾淨的標籤。
func TestSanitize_TableDangerousAttrsStripped(t *testing.T) {
	in := `<table onclick="alert(1)"><tr><td style="background:url(javascript:alert(1))" class="x" onmouseover="evil()">A</td></tr></table>`
	out := Sanitize(in)

	for _, bad := range []string{"onclick", "onmouseover", "style=", "class=", "javascript:", "alert("} {
		if strings.Contains(out, bad) {
			t.Errorf("Sanitize(%q) = %q; must NOT contain %q", in, out, bad)
		}
	}
	// 標籤結構本身仍應存活（只是屬性被清空）
	if !strings.Contains(out, "<table>") || !strings.Contains(out, "<td>A</td>") {
		t.Errorf("Sanitize(%q) = %q; expected table/td tags to survive with attrs stripped", in, out)
	}
}

// TestSanitize_TableNonIntegerAttrsStripped 確保非數字值（企圖夾帶 CSS/JS）即使用的是
// 白名單屬性名稱（border/cellpadding/colspan…）也會因不符 Integer 正則被剝除。
func TestSanitize_TableNonIntegerAttrsStripped(t *testing.T) {
	in := `<table border="1;background:url(x)"><tr><td colspan="2px">A</td></tr></table>`
	out := Sanitize(in)

	if strings.Contains(out, "border=") {
		t.Errorf("Sanitize(%q) = %q; non-integer border value must be stripped", in, out)
	}
	if strings.Contains(out, "colspan=") {
		t.Errorf("Sanitize(%q) = %q; non-integer colspan value must be stripped", in, out)
	}
}

// TestSanitize_ExistingWhitelistUnaffected 確保原本純排版標籤 + 連結白名單行為不受表格
// 白名單擴充影響（回歸測試）。
func TestSanitize_ExistingWhitelistUnaffected(t *testing.T) {
	in := `<p>哈囉 <b>世界</b><script>alert(1)</script><a href="https://example.com">連結</a></p>`
	out := Sanitize(in)

	if strings.Contains(out, "<script>") || strings.Contains(out, "alert(1)") {
		t.Errorf("Sanitize(%q) = %q; script tag must be stripped", in, out)
	}
	if !strings.Contains(out, `target="_blank"`) || !strings.Contains(out, "noopener") || !strings.Contains(out, "noreferrer") {
		t.Errorf("Sanitize(%q) = %q; external link must get target=_blank + rel containing noopener/noreferrer", in, out)
	}
}
