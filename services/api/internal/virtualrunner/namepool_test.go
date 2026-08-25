package virtualrunner

import (
	"strings"
	"testing"

	"math/rand"
)

// TestNamePool_Sizes 契約要求各池的下限——用長度斷言鎖住，未來誤刪詞條時測試會先炸掉。
// 中文固定池合計 >=80（5 類主題各自也要有一定規模，避免其中一類被整組刪光）；模板素材池
// （食物品項/目標數字/溫度牢騷）與英文素材池（羅馬拼音片段/英文小名片段/子音/母音）也各有下限。
func TestNamePool_Sizes(t *testing.T) {
	if len(chineseFixedPool) < 80 {
		t.Fatalf("chineseFixedPool 需 >=80，實際 %d", len(chineseFixedPool))
	}
	if len(foodMemes) < 15 {
		t.Fatalf("foodMemes 需 >=15，實際 %d", len(foodMemes))
	}
	if len(dailyComplaints) < 15 {
		t.Fatalf("dailyComplaints 需 >=15，實際 %d", len(dailyComplaints))
	}
	if len(runningJokes) < 15 {
		t.Fatalf("runningJokes 需 >=15，實際 %d", len(runningJokes))
	}
	if len(goalDeclarations) < 5 {
		t.Fatalf("goalDeclarations 需 >=5，實際 %d", len(goalDeclarations))
	}
	if len(whimsicalNicknames) < 10 {
		t.Fatalf("whimsicalNicknames 需 >=10，實際 %d", len(whimsicalNicknames))
	}
	if len(foodPriceItems) < 10 {
		t.Fatalf("foodPriceItems 需 >=10，實際 %d", len(foodPriceItems))
	}
	if len(subGoalOptions) < 3 {
		t.Fatalf("subGoalOptions 需 >=3，實際 %d", len(subGoalOptions))
	}
	if len(tempComplainSuffixes) < 5 {
		t.Fatalf("tempComplainSuffixes 需 >=5，實際 %d", len(tempComplainSuffixes))
	}
	if len(romanFragments) < 20 {
		t.Fatalf("romanFragments 需 >=20，實際 %d", len(romanFragments))
	}
	if len(englishNamelikeFragments) < 10 {
		t.Fatalf("englishNamelikeFragments 需 >=10，實際 %d", len(englishNamelikeFragments))
	}
	if len(englishConsonants) < 15 {
		t.Fatalf("englishConsonants 需 >=15，實際 %d", len(englishConsonants))
	}
	if len(englishVowels) < 5 {
		t.Fatalf("englishVowels 需 >=5，實際 %d", len(englishVowels))
	}
}

// TestNamePool_ComboSpaceLowerBound 契約要求組合空間 >5000 種。
//
// 中文：固定池（85 條整條直接採用）＋ 三種模板（食物+價格／目標Sub+數字／今天N度+牢騷）。
//
// 英文：改用亂拼演算法而非固定池組合，精確枚舉所有格式分支不切實際；這裡只證明其中一個
// 分支（case 1：randBaseWord 取純子音亂拼分支 3 字元 + 4 位數字尾綴）單獨的組合數就已經
// 遠超過門檻，不需要把其他 3 種格式、音節/羅馬拼音鏈分支也算進來，斷言留有充分餘裕。
func TestNamePool_ComboSpaceLowerBound(t *testing.T) {
	zhFixed := len(chineseFixedPool)
	zhFoodPrice := len(foodPriceItems) * (foodPriceMax - foodPriceMin + 1)
	zhSubGoal := len(subGoalOptions)
	zhTemp := (tempComplainMax - tempComplainMin + 1) * len(tempComplainSuffixes)
	zhTotal := zhFixed + zhFoodPrice + zhSubGoal + zhTemp

	consonants := len(englishConsonants)
	fourDigitRange := 9000 // 1000..9999
	enLowerBound := consonants * consonants * consonants * fourDigitRange

	total := zhTotal + enLowerBound
	if total <= 5000 {
		t.Fatalf("組合空間需 >5000，實際 %d（中文 %d[固定 %d+食物價格 %d+目標 %d+溫度 %d] + 英文下界 %d）",
			total, zhTotal, zhFixed, zhFoodPrice, zhSubGoal, zhTemp, enLowerBound)
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
	check("chineseFixedPool", chineseFixedPool)
	check("foodMemes", foodMemes)
	check("dailyComplaints", dailyComplaints)
	check("runningJokes", runningJokes)
	check("goalDeclarations", goalDeclarations)
	check("whimsicalNicknames", whimsicalNicknames)
	check("foodPriceItems", foodPriceItems)
	check("subGoalOptions", subGoalOptions)
	check("tempComplainSuffixes", tempComplainSuffixes)
	check("romanFragments", romanFragments)
	check("englishNamelikeFragments", englishNamelikeFragments)
}

// TestNamePool_FixedEntriesWithinLengthCap 固定池/模板素材本身（非隨機生成）也要守住上限，
// 避免有人手誤打出超長句子——中文固定池條目 <=12 字，模板素材（食物品項/羅馬拼音片段/
// 英文小名片段）留更多餘裕給隨機部分（數字/分隔符），各自 <=8 字元。
func TestNamePool_FixedEntriesWithinLengthCap(t *testing.T) {
	for _, v := range chineseFixedPool {
		if n := len([]rune(v)); n > 12 {
			t.Errorf("chineseFixedPool 條目 %q 長度 %d 超過 12 字上限", v, n)
		}
	}
	for _, v := range foodPriceItems {
		if n := len([]rune(v)); n > 8 {
			t.Errorf("foodPriceItems 條目 %q 長度 %d 超過 8 字上限", v, n)
		}
	}
	for _, v := range romanFragments {
		if n := len(v); n > 8 {
			t.Errorf("romanFragments 條目 %q 長度 %d 超過 8 字元上限", v, n)
		}
	}
	for _, v := range englishNamelikeFragments {
		if n := len(v); n > 8 {
			t.Errorf("englishNamelikeFragments 條目 %q 長度 %d 超過 8 字元上限", v, n)
		}
	}
}

// TestRandomNickname_LengthAndNotEmpty 大樣本掃過 RandomNickname 的輸出：非空、中文 <=12 字、
// 英文 <=16 字元（nickname 欄位 VARCHAR(50) 很寬裕，但排行榜顯示要好看，故收窄上限）。
// 用 isASCII 判斷中英分支（英文分支只用小寫字母/數字/底線/句點組成，皆為 ASCII）。
func TestRandomNickname_LengthAndNotEmpty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		name := RandomNickname(rng)
		if name == "" {
			t.Fatal("RandomNickname 不應回傳空字串")
		}
		if isASCII(name) {
			if len([]rune(name)) > 16 {
				t.Fatalf("英文綽號 %q 長度 %d 超過 16 字元上限", name, len([]rune(name)))
			}
			continue
		}
		if len([]rune(name)) > 12 {
			t.Fatalf("中文綽號 %q 長度 %d 超過 12 字上限", name, len([]rune(name)))
		}
	}
}

// TestRandomNickname_LanguageRatio 用固定種子大樣本統計中英比例，寬鬆區間驗證約 60/40 分佈
// （55%-65% 中文），避免鎖死成精確比例導致實作微調就測試炸掉。
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
	if ratio < 0.55 || ratio > 0.65 {
		t.Fatalf("中文綽號比例需落在 55%%-65%%，實際 %.1f%%（%d/%d）", ratio*100, zhCount, n)
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

// TestHasAdjacentDuplicateConnector 直接單元測試黏接防呆函式：命中「之之/的的/··」等
// 相鄰重複連接字要偵測到；一般字串（含單獨出現、不相鄰的連接字）不該誤判。
func TestHasAdjacentDuplicateConnector(t *testing.T) {
	shouldDetect := []string{"疾風之之翼", "破的的PB", "行者··疾風", "之之"}
	for _, s := range shouldDetect {
		if !hasAdjacentDuplicateConnector(s) {
			t.Errorf("%q 應被偵測為黏接異常，卻沒有", s)
		}
	}
	shouldNotDetect := []string{
		"疾風之翼", "奔跑的火鍋", "追風的蝸牛", "行者·疾風", "貢丸湯10元",
		"目標Sub3", "fificc1429", "dd.claire.99", "chye_ang", "",
	}
	for _, s := range shouldNotDetect {
		if hasAdjacentDuplicateConnector(s) {
			t.Errorf("%q 不應被判為黏接異常", s)
		}
	}
}

// TestRandomNickname_NoAdjacentDuplicateConnectors 大樣本掃過 RandomNickname 輸出，確認
// 不會出現「之之/的的/··」這類相鄰重複連接字——這是舊版「疾風之之翼」bug 的迴歸測試：
// 新版設計本身已不再用這類字黏接前後綴，這裡驗證的是「設計正確」加上「防呆有效」雙重保證。
func TestRandomNickname_NoAdjacentDuplicateConnectors(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	bad := []string{"之之", "的的", "··"}
	for i := 0; i < 20000; i++ {
		name := RandomNickname(rng)
		for _, b := range bad {
			if strings.Contains(name, b) {
				t.Fatalf("RandomNickname 產出 %q 含黏接異常子字串 %q", name, b)
			}
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
