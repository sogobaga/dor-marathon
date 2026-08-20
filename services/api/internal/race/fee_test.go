package race

import "testing"

// TestEffectiveGroupFee_FourQuadrants 覆蓋 uniform/per_group × (該組有獨立價/NULL) 四象限，
// 是報名分組獨立定價（migration 136）的唯一計價事實來源，所有計價點都必須走這個函式。
func TestEffectiveGroupFee_FourQuadrants(t *testing.T) {
	groupPrice := 8000 // NT$80

	cases := []struct {
		name string
		race *Race
		grp  *RaceGroup
		want int
	}{
		{
			name: "uniform + 該組設有獨立價 → 仍用預設報名費（uniform 模式完全忽略組價）",
			race: &Race{FeeMode: "uniform", EntryFee: 10000},
			grp:  &RaceGroup{EntryFeeCents: &groupPrice},
			want: 10000,
		},
		{
			name: "uniform + 該組未設定 → 用預設報名費",
			race: &Race{FeeMode: "uniform", EntryFee: 10000},
			grp:  &RaceGroup{EntryFeeCents: nil},
			want: 10000,
		},
		{
			name: "per_group + 該組設有獨立價 → 用該組獨立價",
			race: &Race{FeeMode: "per_group", EntryFee: 10000},
			grp:  &RaceGroup{EntryFeeCents: &groupPrice},
			want: 8000,
		},
		{
			name: "per_group + 該組未設定(NULL) → 回退預設報名費",
			race: &Race{FeeMode: "per_group", EntryFee: 10000},
			grp:  &RaceGroup{EntryFeeCents: nil},
			want: 10000,
		},
		{
			name: "per_group + group 為 nil（分組對抗/個人挑戰報名前尚未指派）→ 回退預設報名費",
			race: &Race{FeeMode: "per_group", EntryFee: 10000},
			grp:  nil,
			want: 10000,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EffectiveGroupFee(c.race, c.grp)
			if got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

// TestEffectiveGroupFee_CouponZeroFeeGate 驗證「報名費 0 元時優惠券不可用」這條判定
//（見 repository.go RegisterWithOrder 的 `entryFee > 0` 閘門）在各種有效組價下的行為一致：
// 只要 EffectiveGroupFee 算出 0，該閘門就該擋下用券；算出 >0 就該放行。
func TestEffectiveGroupFee_CouponZeroFeeGate(t *testing.T) {
	zero := 0
	nonZero := 5000

	cases := []struct {
		name        string
		race        *Race
		grp         *RaceGroup
		wantAllowed bool // entryFee > 0 ?
	}{
		{
			name:        "uniform 免費賽事 → 擋下用券",
			race:        &Race{FeeMode: "uniform", EntryFee: 0},
			grp:         nil,
			wantAllowed: false,
		},
		{
			name:        "per_group 該組獨立價為 0 → 擋下用券（即使預設報名費非 0）",
			race:        &Race{FeeMode: "per_group", EntryFee: 10000},
			grp:         &RaceGroup{EntryFeeCents: &zero},
			wantAllowed: false,
		},
		{
			name:        "per_group 該組獨立價 > 0 → 放行用券",
			race:        &Race{FeeMode: "per_group", EntryFee: 0},
			grp:         &RaceGroup{EntryFeeCents: &nonZero},
			wantAllowed: true,
		},
		{
			name:        "per_group 該組 NULL、回退預設報名費 > 0 → 放行用券",
			race:        &Race{FeeMode: "per_group", EntryFee: 10000},
			grp:         nil,
			wantAllowed: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fee := EffectiveGroupFee(c.race, c.grp)
			allowed := fee > 0
			if allowed != c.wantAllowed {
				t.Fatalf("fee=%d allowed=%v, want %v", fee, allowed, c.wantAllowed)
			}
		})
	}
}
