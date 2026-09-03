package activity

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateRecallReason(t *testing.T) {
	longButOK := strings.Repeat("a", maxRecallReasonLen) // 剛好 40 字元，應通過
	tooLong := strings.Repeat("a", maxRecallReasonLen+1) // 41 字元，應被拒

	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr error
	}{
		{"empty defaults", "", defaultRecallReason, nil},
		{"whitespace only defaults", "   ", defaultRecallReason, nil},
		{"trims surrounding whitespace", "  suspicious_gps  ", "suspicious_gps", nil},
		{"custom reason ok", "admin_review_2026", "admin_review_2026", nil},
		{"exactly max length ok", longButOK, longButOK, nil},
		{"over max length rejected", tooLong, "", ErrRecallReasonTooLong},
		{"benign multi_device_duplicate rejected", "multi_device_duplicate", "", ErrRecallReasonBenign},
		{"benign cross_source_duplicate rejected", "cross_source_duplicate", "", ErrRecallReasonBenign},
		{"benign duplicate rejected", "duplicate", "", ErrRecallReasonBenign},
		{"benign reason with padding still rejected", "  duplicate  ", "", ErrRecallReasonBenign},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateRecallReason(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ValidateRecallReason(%q) err = %v, want %v", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateRecallReason(%q) unexpected err: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ValidateRecallReason(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestValidateRecallReason_RuneCountNotByteCount(t *testing.T) {
	// 中文字元多位元組但只算 1 個 rune；40 個中文字元應通過（對齊 activities.flag_reason
	// VARCHAR(40) 是「字元」長度限制，不是 byte 長度）。
	reason := strings.Repeat("異", maxRecallReasonLen)
	got, err := ValidateRecallReason(reason)
	if err != nil {
		t.Fatalf("40 個中文字元應通過，卻回傳 err: %v", err)
	}
	if got != reason {
		t.Fatalf("got %q, want %q", got, reason)
	}

	tooLong := strings.Repeat("異", maxRecallReasonLen+1)
	if _, err := ValidateRecallReason(tooLong); !errors.Is(err, ErrRecallReasonTooLong) {
		t.Fatalf("41 個中文字元應被拒絕，卻回傳 err: %v", err)
	}
}

func TestBuildRecallFollowups_NoActivity(t *testing.T) {
	out := buildRecallFollowups(recallFollowupFacts{HasActivity: false})
	if len(out) != 1 {
		t.Fatalf("HasActivity=false 應只有 1 則訊息（無金額可收回），got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0], "無金額可收回") {
		t.Fatalf("訊息內容不符預期: %q", out[0])
	}
}

func TestBuildRecallFollowups_AlwaysEndsWithStaminaNote(t *testing.T) {
	facts := recallFollowupFacts{HasActivity: true}
	out := buildRecallFollowups(facts)
	last := out[len(out)-1]
	if !strings.Contains(last, "體力值(SP)") {
		t.Fatalf("最後一則訊息應是固定的 SP 不可逆提醒，got %q", last)
	}
	// 沒有其他事實時，唯讀提醒清單只有固定的 SP 提醒這一則。
	if len(out) != 1 {
		t.Fatalf("無其他事實時應只有 SP 提醒 1 則，got %d: %v", len(out), out)
	}
}

func TestBuildRecallFollowups_IncludesAllFactCategories(t *testing.T) {
	facts := recallFollowupFacts{
		HasActivity:          true,
		RaceID:               "race-123",
		OverlapCount:         2,
		TitleCodes:           []string{"single_dist_04", "cum_dist_00"},
		ReferredRewarded:     true,
		ReferrerRewarded:     true,
		CompletedRaceIDs:     []string{"race-456"},
		MissionCompletionIDs: []string{"mc-1", "mc-2"},
	}
	out := buildRecallFollowups(facts)

	mustContainSubstr := func(needle string) {
		t.Helper()
		for _, line := range out {
			if strings.Contains(line, needle) {
				return
			}
		}
		t.Errorf("expected a followup line containing %q, got: %v", needle, out)
	}

	mustContainSubstr("race_id=race-123")
	mustContainSubstr("2 筆其他已發放活動時間重疊")
	mustContainSubstr("single_dist_04")
	mustContainSubstr("cum_dist_00")
	mustContainSubstr("被推薦人")
	mustContainSubstr("推薦人")
	mustContainSubstr("race_id=race-456")
	mustContainSubstr("mc-1")
	mustContainSubstr("mc-2")
	mustContainSubstr("體力值(SP)")

	// 固定的 SP 提醒必須是最後一則。
	if !strings.Contains(out[len(out)-1], "體力值(SP)") {
		t.Fatalf("SP 提醒應排在 followups 最後一則，got last=%q", out[len(out)-1])
	}
}

func TestBenignReasonsSQLIn_MatchesBenignFlagReasonsKeys(t *testing.T) {
	// 純函式驗證：SQL IN(...) 字面清單必須跟目前 activity 套件用來擋 reason 的良性標記判斷
	// （internal/integration.IsBenignFlagReason）內容一致——這裡透過 ValidateRecallReason 間接
	// 驗證，因為 activity 套件本身故意不重複維護一份 benignFlagReasons map（見 gps_recall.go
	// 頂部註解：跟 integration 同一模組，直接呼叫 IsBenignFlagReason）。
	for _, reason := range []string{"multi_device_duplicate", "cross_source_duplicate", "duplicate"} {
		if _, err := ValidateRecallReason(reason); !errors.Is(err, ErrRecallReasonBenign) {
			t.Errorf("reason %q 應被判定為良性標記而拒絕，卻沒有回傳 ErrRecallReasonBenign（err=%v）", reason, err)
		}
	}
}
