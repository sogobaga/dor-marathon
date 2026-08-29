package runcheer

import (
	"strings"
	"testing"
)

func TestMessageReqValidate(t *testing.T) {
	longText := strings.Repeat("字", 120)  // 剛好 120 字（合法上限）
	tooLong := strings.Repeat("字", 121)   // 121 字（超過上限）

	cases := []struct {
		name    string
		req     messageReq
		wantErr bool
	}{
		{"合法 before", messageReq{Phase: "before", Text: "加油!!你已經完成{done}囉!!", SortOrder: 1}, false},
		{"合法 after", messageReq{Phase: "after", Text: "努力撐住,還剩下{remain}。", SortOrder: 0}, false},
		{"phase 空字串", messageReq{Phase: "", Text: "文案", SortOrder: 0}, true},
		{"phase 未知值", messageReq{Phase: "middle", Text: "文案", SortOrder: 0}, true},
		{"text 空字串", messageReq{Phase: "before", Text: "", SortOrder: 0}, true},
		{"text 只有空白（trim 後為空）", messageReq{Phase: "before", Text: "   ", SortOrder: 0}, true},
		{"text 剛好 1 字元", messageReq{Phase: "before", Text: "a", SortOrder: 0}, false},
		{"text 剛好 120 字元（rune 計數）", messageReq{Phase: "before", Text: longText, SortOrder: 0}, false},
		{"text 121 字元超過上限", messageReq{Phase: "before", Text: tooLong, SortOrder: 0}, true},
		{"sort_order 為 0 合法", messageReq{Phase: "before", Text: "文案", SortOrder: 0}, false},
		{"sort_order 為負數不合法", messageReq{Phase: "before", Text: "文案", SortOrder: -1}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.req.validate()
			if c.wantErr && got == "" {
				t.Errorf("validate() = %q, want non-empty error message", got)
			}
			if !c.wantErr && got != "" {
				t.Errorf("validate() = %q, want no error", got)
			}
		})
	}
}

func TestValidPhase(t *testing.T) {
	if !validPhase("before") || !validPhase("after") {
		t.Error("before/after should be valid phases")
	}
	if validPhase("") || validPhase("BEFORE") || validPhase("mid") {
		t.Error("unknown phase strings should be invalid")
	}
}

func TestIsValidUUID(t *testing.T) {
	cases := map[string]bool{
		"550e8400-e29b-41d4-a716-446655440000": true,
		"550E8400-E29B-41D4-A716-446655440000": true, // 大寫也合法
		"":                                     false,
		"not-a-uuid":                           false,
		"550e8400-e29b-41d4-a716":              false, // 太短
		"550e8400e29b41d4a716446655440000":     false, // 缺少連字號
	}
	for id, want := range cases {
		if got := isValidUUID(id); got != want {
			t.Errorf("isValidUUID(%q) = %v, want %v", id, got, want)
		}
	}
}
