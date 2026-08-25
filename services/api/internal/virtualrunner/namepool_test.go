package virtualrunner

import (
	"strings"
	"testing"

	"math/rand"
)

// TestNamePool_Sizes 契約要求各池的下限——用長度斷言鎖住，未來誤刪詞條時測試會先炸掉：
// 中文前綴 30+、後綴 20+、自嘲幽默款 15+；英文形容詞 20+、名詞 15+。
func TestNamePool_Sizes(t *testing.T) {
	if len(chinesePrefixes) < 30 {
		t.Fatalf("chinesePrefixes 需 >=30，實際 %d", len(chinesePrefixes))
	}
	if len(chineseSuffixes) < 20 {
		t.Fatalf("chineseSuffixes 需 >=20，實際 %d", len(chineseSuffixes))
	}
	if len(humorNicknames) < 15 {
		t.Fatalf("humorNicknames 需 >=15，實際 %d", len(humorNicknames))
	}
	if len(englishAdjectives) < 20 {
		t.Fatalf("englishAdjectives 需 >=20，實際 %d", len(englishAdjectives))
	}
	if len(englishNouns) < 15 {
		t.Fatalf("englishNouns 需 >=15，實際 %d", len(englishNouns))
	}
}

// TestNamePool_ComboSpaceLowerBound 契約要求組合空間 >2000 種。中文組合式＝前綴×後綴×3 種拼法，
// 英文組合式（不含數字尾綴分支，其為 noun×90 種數字各自獨立）＝形容詞×名詞×3 種格式；
// 兩者相加已遠超過 2000，不需要把數字尾綴分支也算進來就能過門檻，斷言留有充分餘裕。
func TestNamePool_ComboSpaceLowerBound(t *testing.T) {
	zhCombos := len(chinesePrefixes) * len(chineseSuffixes) * 3
	enCombos := len(englishAdjectives) * len(englishNouns) * 3
	total := zhCombos + enCombos + len(humorNicknames)
	if total <= 2000 {
		t.Fatalf("組合空間需 >2000，實際 %d（中文 %d + 英文 %d + 幽默款 %d）", total, zhCombos, enCombos, len(humorNicknames))
	}
}

// TestNamePool_NoDuplicatesOrEmpty 池內不應有重複值或空字串（複製貼上手誤會讓某些詞機率加倍）。
func TestNamePool_NoDuplicatesOrEmpty(t *testing.T) {
	check := func(name string, list []string) {
		t.Helper()
		seen := map[string]bool{}
		for _, v := range list {
			if v == "" {
				t.Errorf("%s 池內有空字串", name)
			}
			if seen[v] {
				t.Errorf("%s 池內有重複值：%q", name, v)
			}
			seen[v] = true
		}
	}
	check("chinesePrefixes", chinesePrefixes)
	check("chineseSuffixes", chineseSuffixes)
	check("humorNicknames", humorNicknames)
	check("englishAdjectives", englishAdjectives)
	check("englishNouns", englishNouns)
}

// TestRandomNickname_LengthAndNotEmpty 大樣本掃過 RandomNickname 的輸出：非空、中文 <=10 字、
// 英文 <=20 字元（nickname 欄位 VARCHAR(50) 很寬裕，但排行榜顯示要好看，故收窄上限）。
// 用 isASCII 判斷中英分支（英文池只用 ASCII 字母/數字/空白/底線組成）。
func TestRandomNickname_LengthAndNotEmpty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		name := RandomNickname(rng)
		if name == "" {
			t.Fatal("RandomNickname 不應回傳空字串")
		}
		if isASCII(name) {
			if len([]rune(name)) > 20 {
				t.Fatalf("英文綽號 %q 長度 %d 超過 20 字元上限", name, len([]rune(name)))
			}
			continue
		}
		if len([]rune(name)) > 10 {
			t.Fatalf("中文綽號 %q 長度 %d 超過 10 字上限", name, len([]rune(name)))
		}
	}
}

// TestRandomNickname_LanguageRatio 用固定種子大樣本統計中英比例，寬鬆區間驗證約 70/30 分佈
// （60%-80% 中文），避免鎖死成精確比例導致實作微調就測試炸掉。
func TestRandomNickname_LanguageRatio(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const n = 10000
	zhCount := 0
	for i := 0; i < n; i++ {
		if !isASCII(RandomNickname(rng)) {
			zhCount++
		}
	}
	ratio := float64(zhCount) / float64(n)
	if ratio < 0.6 || ratio > 0.8 {
		t.Fatalf("中文綽號比例需落在 60%%-80%%，實際 %.1f%%（%d/%d）", ratio*100, zhCount, n)
	}
}

// TestRandomNickname_Deterministic 同一種子產生的序列應完全相同（rng 純注入、函式本身不含
// 額外隨機源，如全域 rand 或時間），供未來重構時當回歸基準。
func TestRandomNickname_Deterministic(t *testing.T) {
	seqA := generateSeq(rand.New(rand.NewSource(99)), 50)
	seqB := generateSeq(rand.New(rand.NewSource(99)), 50)
	for i := range seqA {
		if seqA[i] != seqB[i] {
			t.Fatalf("同種子第 %d 筆不一致：%q != %q", i, seqA[i], seqB[i])
		}
	}
}

func generateSeq(rng *rand.Rand, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = RandomNickname(rng)
	}
	return out
}

func isASCII(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r > 127 }) == -1
}
