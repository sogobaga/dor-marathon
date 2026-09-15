package rewardserial

import "testing"

// TestComputeGroupCapacity 純函式：驗證 migration 178 三種 use_limit_type 的容量換算公式（見 capacity.go
// 檔頭三種定義）。涵蓋規格書驗證清單的關鍵情境：茶事漫漫組（repeat N=10）從「1 列已發 1 人」到「發到第
// 10 人耗盡」的每一步、unlimited 型永不耗盡、single 型行為完全不變、未知型別保底當 single。
func TestComputeGroupCapacity(t *testing.T) {
	cases := []struct {
		name                                                               string
		useLimitType                                                       string
		useLimitCount, n, availRows, issuedRows, issueSum, repeatRemaining int
		want                                                               GroupCapacity
	}{
		{
			name: "single：一列可用 → Remaining=可用列數", useLimitType: "single",
			n: 5, availRows: 3, issuedRows: 2, issueSum: 0, repeatRemaining: 0,
			want: GroupCapacity{Remaining: 3, Total: 5, Issued: 2},
		},
		{
			name: "single：全數已發送 → Remaining=0", useLimitType: "single",
			n: 4, availRows: 0, issuedRows: 4,
			want: GroupCapacity{Remaining: 0, Total: 4, Issued: 4},
		},
		{
			name:         "repeat：茶事漫漫組套 178 剛回填——1 列，N=10，已發 1 人 → Remaining=9",
			useLimitType: "repeat", useLimitCount: 10, n: 1, issueSum: 1, repeatRemaining: 9,
			want: GroupCapacity{Remaining: 9, Total: 10, Issued: 1},
		},
		{
			name:         "repeat：發到第 3 人（issue_count=3）→ Remaining=7",
			useLimitType: "repeat", useLimitCount: 10, n: 1, issueSum: 3, repeatRemaining: 7,
			want: GroupCapacity{Remaining: 7, Total: 10, Issued: 3},
		},
		{
			name:         "repeat：發滿第 10 人（issue_count=10，status 已變 issued）→ Remaining=0",
			useLimitType: "repeat", useLimitCount: 10, n: 1, issueSum: 10, repeatRemaining: 0,
			want: GroupCapacity{Remaining: 0, Total: 10, Issued: 10},
		},
		{
			name:         "repeat：多列（3 列 × N=5）→ Total=15",
			useLimitType: "repeat", useLimitCount: 5, n: 3, issueSum: 4, repeatRemaining: 11,
			want: GroupCapacity{Remaining: 11, Total: 15, Issued: 4},
		},
		{
			name:         "unlimited：仍有列 → Unlimited=true，Remaining 恆 0",
			useLimitType: "unlimited", n: 1, issueSum: 37,
			want: GroupCapacity{Unlimited: true, Issued: 37},
		},
		{
			name:         "unlimited：所有列皆已註銷（n=0，LEFT JOIN 無非-void 列）→ Unlimited=false",
			useLimitType: "unlimited", n: 0, issueSum: 0,
			want: GroupCapacity{Unlimited: false, Issued: 0},
		},
		{
			name:         "未知型別（理論不會發生，CRUD 已擋）→ 保底當 single",
			useLimitType: "weird", n: 2, availRows: 1, issuedRows: 1,
			want: GroupCapacity{Remaining: 1, Total: 2, Issued: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeGroupCapacity(c.useLimitType, c.useLimitCount, c.n, c.availRows, c.issuedRows, c.issueSum, c.repeatRemaining)
			if got != c.want {
				t.Fatalf("computeGroupCapacity(%q,...) = %+v, want %+v", c.useLimitType, got, c.want)
			}
		})
	}
}
