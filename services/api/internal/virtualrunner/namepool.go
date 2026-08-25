package virtualrunner

import (
	"fmt"
	"math/rand"
	"strings"
)

// 綽號生成器：虛擬選手綽號要像「真實跑步 App 帳號」，不是奇幻中二風前綴×後綴組合
// （舊版「疾風之翼」那套已棄用——太模板化，且曾出過「疾風」+「之」模板撞上「之翼」後綴
// 黏出「疾風之之翼」的重複字 bug）。新版分兩支：
//
//   - 中文（≈60%）：不刻意「像名字」，更像在「講一件事」——食物+價格的碎念、日常牢騷、
//     天氣、目標宣言，可以跟跑步完全無關，重點是有趣（例：「貢丸湯10元」「今天天氣好熱」
//     「目標Sub3」）。多數（≈70%）從精選固定池整條取用；少數（≈30%）用三種輕量模板
//     （食物+隨機價格／目標Sub+隨機數字／隨機溫度牢騷）擴充組合空間，且刻意不使用任何
//     「之／的／·」黏接前後綴，從根本避開舊版的重複字 bug。
//   - 英文（≈40%）：不是「形容詞+名詞」的正確英文片語，而是「像真實帳號」的亂拼字串——
//     子音+母音亂拼／純子音亂拼／台式羅馬拼音片段鏈，混搭數字尾綴、底線、句點分隔，
//     全小寫（例：「fificc1429」「zyx1273」「dd.claire.99」）。
//
// RandomNickname 為唯一對外入口，純函式、呼叫端注入 rng 以便測試用固定種子驗證分布；
// 不吃 gender 參數（綽號中性），gender 仍照舊只存 user_profiles 供推薦排序等用途。

// zhChance 整體走中文分支的機率（≈60%），其餘落入英文分支（≈40%）。
const zhChance = 0.6

// zhTemplateChance 中文分支中，走「模板變化」（食物+價格／目標Sub+數字／溫度牢騷）而非
// 固定池整條取用的機率；固定池佔多數（≈70%）維持「精選好笑」的品質，模板只用來補
// 組合空間，避免玩家很快就把固定池的每一句都看過一輪。
const zhTemplateChance = 0.3

// --- 中文固定池：五個主題分類，皆整條直接採用、不做任何拼接（不會有黏接重複字風險）---
// 每條皆 <=12 字，遠低於下方 12 字上限。分類只是方便維護/歸類，實際挑選時五類合併成一個池
// （見 chineseFixedPool），彼此權重相同。

// foodMemes 食物梗（20 條）：小吃/飲料的價格碎念、口味偏好，貼近真實社群「講一件事」的調性。
var foodMemes = []string{
	"貢丸湯10元", "珍奶半糖去冰", "蛋餅加蛋", "巷口鹹酥雞", "滷肉飯加蛋",
	"雞排不要辣", "豆漿油條配", "麻辣燙秤重中", "手搖飲買一送一", "陽春麵加滷蛋",
	"魯味老闆多加點", "臭豆腐控", "生煎包燙口", "肉圓沾醬狂", "米粉湯加大腸",
	"蚵仔煎不要香菜", "刈包夾滿滿", "蔥抓餅加蛋加起司", "地瓜球外酥內軟", "大腸包小腸信徒",
}

// dailyComplaints 日常牢騷（20 條）：跟跑步無關，純講生活小事——天氣、上班、手機電量。
var dailyComplaints = []string{
	"今天天氣好熱", "週一不想上班", "冷氣開23度", "好想睡回籠覺", "手機只剩1%",
	"外送遲到中", "颱風假許願中", "捷運差點睡過站", "老闆已讀不回", "月底吃土中",
	"根本沒有週末", "又忘記帶傘", "睡前忍不住滑手機", "廢話少說先睡", "早八根本反人類",
	"週五症候群發作", "訊息已讀不回我", "電量只剩10%", "鬧鐘按掉又睡著", "星期一結界打不破",
}

// runningJokes 跑步自嘲（20 條）：真實跑者社群常見的「不是耍帥而是自嘲」調性。
var runningJokes = []string{
	"起跑線常客", "關門時間求生者", "配速全靠感覺", "月跑量負成長", "GPS又飄了",
	"只跑不拉筋", "賽前才練跑", "跑鞋比我認真", "訓練計畫吃灰", "破風全靠意志力",
	"最後一公里靠信仰", "抽筋預備軍", "膝蓋在抗議中", "補給包收藏家", "起床氣選手",
	"配速全憑玄學", "PB停在三年前", "賽道邊緣人", "收操魔人", "龜速前進中",
}

// goalDeclarations 目標宣言（10 條）：固定版本，數字版另見 randomChineseTemplate 的 subGoalOptions。
var goalDeclarations = []string{
	"目標破PB", "今年一定全馬", "先求完賽", "目標不受傷", "報名先衝再說",
	"目標全馬破四", "半馬破二一", "目標完賽就好", "今年拚一場全馬", "目標不要抽筋",
}

// whimsicalNicknames 奇趣款（15 條）：跟跑步無關也無妨，重點是畫面感／有趣。
var whimsicalNicknames = []string{
	"奔跑的火鍋", "追風的蝸牛", "巷口的橘貓", "全糖信仰者", "夜市巡邏員",
	"路燈數量統計員", "便利商店常客", "電風扇轉速前段班", "沙發馬鈴薯復健中", "貓咪的跑腿",
	"奔跑的地瓜", "夜跑遇到浣熊", "巷口的柴犬", "追垃圾車選手", "落跑的珍珠奶茶",
}

// chineseFixedPool 上述五類合併成單一池，供 randomChineseNickname 均勻抽取（85 條）。
var chineseFixedPool = concatStrings(foodMemes, dailyComplaints, runningJokes, goalDeclarations, whimsicalNicknames)

func concatStrings(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// --- 中文模板：輕量變化，擴充組合空間；三種皆不使用「之／的／·」等黏接字，
// 純粹是「詞 + 隨機數字」或「固定句型 + 隨機數字」，結構上不會產生重複字黏接 ---

// foodPriceItems 供「食物+隨機價格」模板使用的品項（不含金額，金額由 foodPriceMin~Max 隨機帶入）。
var foodPriceItems = []string{
	"貢丸湯", "珍奶", "雞排", "滷肉飯", "鹹酥雞",
	"豆漿", "刈包", "蔥抓餅", "車輪餅", "蚵仔煎",
	"肉圓", "米粉湯", "臭豆腐", "生煎包", "黑輪",
}

const (
	foodPriceMin = 5  // 元，含
	foodPriceMax = 99 // 元，含
)

// subGoalOptions 「目標Sub+數字」模板的數字部分（Sub3~Sub5 含半馬/全馬跑者常見的破關目標）。
var subGoalOptions = []string{"3", "3.5", "4", "4.5", "5"}

// tempComplainSuffixes 「今天N度＋牢騷」模板的牢騷後半段。
var tempComplainSuffixes = []string{"快融化", "根本烤箱", "想罷工", "懶得動", "快中暑了", "出門要勇氣"}

const (
	tempComplainMin = 30 // 度，含
	tempComplainMax = 39 // 度，含
)

// randomChineseTemplate 三種模板之一：食物+隨機價格／目標Sub+隨機數字／今天N度+牢騷。
func randomChineseTemplate(rng *rand.Rand) string {
	switch rng.Intn(3) {
	case 0:
		item := foodPriceItems[rng.Intn(len(foodPriceItems))]
		price := foodPriceMin + rng.Intn(foodPriceMax-foodPriceMin+1)
		return fmt.Sprintf("%s%d元", item, price)
	case 1:
		return "目標Sub" + subGoalOptions[rng.Intn(len(subGoalOptions))]
	default:
		temp := tempComplainMin + rng.Intn(tempComplainMax-tempComplainMin+1)
		suffix := tempComplainSuffixes[rng.Intn(len(tempComplainSuffixes))]
		return fmt.Sprintf("今天%d度%s", temp, suffix)
	}
}

// --- 英文：亂拼帳號感字串的底層素材 ---

// englishConsonants / englishVowels 供音節式亂拼（子音+母音組一個音節，如 "fi"/"zy"）。
const englishConsonants = "bcdfghjklmnpqrstvwxyz"
const englishVowels = "aeiou"

// romanFragments 台式羅馬拼音常見片段，供拼帳號時混搭（如 "mei"+"hsi"="meihsi"）；
// 純語感取材、非對應任何真實特定人物。
var romanFragments = []string{
	"mei", "hsi", "chia", "wei", "yun", "ling", "hsin", "chen", "kai", "ting",
	"hui", "jia", "yu", "szu", "pei", "ying", "chun", "fang", "hao", "jun",
	"chye", "ang", "wen", "an", "yi", "ru",
}

// englishNamelikeFragments 英文名字風格片段，供句點三段式（如 "dd.claire.99"）的中段使用；
// 純常見英文小名詞彙，非指代真實特定人物。
var englishNamelikeFragments = []string{
	"claire", "emma", "jenny", "amy", "cindy", "joyce", "wendy", "vivi", "kiki", "momo",
	"lulu", "coco", "nana", "mia", "zoe",
}

// randSyllableWord 音節式亂拼：2-3 個「子音+母音」音節（4 或 6 字元），25% 機率把前 2 字元
// 疊字（模擬 fifi/dd 這類疊音暱稱），疊字後最長 8 字元。
func randSyllableWord(rng *rand.Rand) string {
	n := 2 + rng.Intn(2) // 2 或 3 音節
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(englishConsonants[rng.Intn(len(englishConsonants))])
		b.WriteByte(englishVowels[rng.Intn(len(englishVowels))])
	}
	word := b.String()
	if rng.Float64() < 0.25 {
		word = word[:2] + word
	}
	return word
}

// randConsonantCluster 純子音亂拼（3-4 字元，如 "zyx"/"dfkm"），模擬鍵盤隨手敲的帳號風格。
func randConsonantCluster(rng *rand.Rand) string {
	n := 3 + rng.Intn(2) // 3 或 4 個子音
	b := make([]byte, n)
	for i := range b {
		b[i] = englishConsonants[rng.Intn(len(englishConsonants))]
	}
	return string(b)
}

// randRomanChain 羅馬拼音片段鏈：2 段相接（如 "mei"+"hsi"="meihsi"），4-8 字元。
func randRomanChain(rng *rand.Rand) string {
	return romanFragments[rng.Intn(len(romanFragments))] + romanFragments[rng.Intn(len(romanFragments))]
}

// randBaseWord 三種底層生成法均勻擇一（音節亂拼／純子音亂拼／羅馬拼音片段鏈），3-8 字元；
// 供「詞+數字尾綴」格式使用——就算底層取到最短的純子音亂拼，加上數字尾綴後總長仍落在
// 合理範圍（見 randomEnglishNickname 各分支長度註解）。
func randBaseWord(rng *rand.Rand) string {
	switch rng.Intn(3) {
	case 0:
		return randSyllableWord(rng)
	case 1:
		return randConsonantCluster(rng)
	default:
		return randRomanChain(rng)
	}
}

// randPureWord 「純字串」格式專用（不接數字尾綴），限縮成音節亂拼／羅馬拼音片段鏈兩種
// （皆 4-8 字元），刻意排除純子音亂拼——避免單獨呈現時過短（3-4 字元讀起來太像亂碼）。
func randPureWord(rng *rand.Rand) string {
	if rng.Float64() < 0.5 {
		return randSyllableWord(rng)
	}
	return randRomanChain(rng)
}

// randShortSegment 供底線分隔格式（如 "chye_ang"）使用的短段：純子音亂拼或單一羅馬拼音
// 片段，2-4 字元；兩段接起來（含底線）落在 5-9 字元，維持帳號感的簡短調性。
func randShortSegment(rng *rand.Rand) string {
	if rng.Float64() < 0.5 {
		return randConsonantCluster(rng)
	}
	return romanFragments[rng.Intn(len(romanFragments))]
}

// randDigitSuffix 產生 digits 位數的隨機數字字串（如 digits=2 → "10".."99"、digits=4 → "1000".."9999"）。
func randDigitSuffix(rng *rand.Rand, digits int) string {
	max := 1
	for i := 0; i < digits; i++ {
		max *= 10
	}
	min := max / 10
	return fmt.Sprintf("%d", min+rng.Intn(max-min))
}

// randomEnglishNickname 英文分支：拼帳號感的隨機字串，4 種格式輪流輸出——純字串、
// 詞尾加 2-4 位數字尾綴（模擬帳號被搶註後常見的「加數字妥協」，如 fificc1429／zyx73）、
// 底線兩段（chye_ang）、句點三段式（dd.claire.99，模擬「短暱稱.小名.數字尾」的常見排法）。
// 全小寫，長度上限依各分支最長組合估算，皆遠低於 16 字元的門檻：
//
//	case 0（純字串）        最長 8 字元
//	case 1（詞+2~4位數字）  最長 8+4=12 字元
//	case 2（底線兩段）      最長 4+1+4=9 字元
//	case 3（句點三段式）    最長 2+1+6+1+2=12 字元
func randomEnglishNickname(rng *rand.Rand) string {
	switch rng.Intn(4) {
	case 0:
		return randPureWord(rng)
	case 1:
		digits := 2 + rng.Intn(3) // 2、3 或 4 位數字
		return randBaseWord(rng) + randDigitSuffix(rng, digits)
	case 2:
		return randShortSegment(rng) + "_" + randShortSegment(rng)
	default:
		tag := randConsonantCluster(rng)[:2] // 固定截前 2 字元，模擬 "dd" 這類兩字母暱稱
		frag := englishNamelikeFragments[rng.Intn(len(englishNamelikeFragments))]
		return tag + "." + frag + "." + randDigitSuffix(rng, 2)
	}
}

// --- 對外入口 ---

// connectorRunes 中文組字可能出現的「連接字」；若生成結果中這些字元恰好相鄰重複
// （如「之之」「的的」「··」），視為黏接異常。新版設計本身已不再用這類字黏接前後綴，
// 這道檢查是防呆——保留給未來若有人改動模板、不慎重新踩到舊版「疾風之之翼」那種
// 重複字 bug 時，能在生成階段直接擋下並重抽，而不是讓異常字串流入資料庫。
var connectorRunes = []rune{'之', '的', '·'}

// hasAdjacentDuplicateConnector 回傳 s 是否含相鄰重複的連接字。
func hasAdjacentDuplicateConnector(s string) bool {
	runes := []rune(s)
	for i := 1; i < len(runes); i++ {
		if runes[i] != runes[i-1] {
			continue
		}
		for _, c := range connectorRunes {
			if runes[i] == c {
				return true
			}
		}
	}
	return false
}

// maxConnectorRetries 命中黏接異常時的最大重抽次數；理論上目前的池/模板設計不會觸發
// （沒有任何分支會產生相鄰重複的連接字），這裡設上限只是避免萬一真的踩到時無窮迴圈。
const maxConnectorRetries = 20

// RandomNickname 隨機產出一個跑者綽號：≈60% 中文（固定池為主、輕量模板補組合空間）、
// ≈40% 英文（帳號感亂拼字串）。純函式，呼叫端注入 rng 以便測試用固定種子驗證輸出分布與
// 長度上限；含黏接防呆（見 hasAdjacentDuplicateConnector）。
func RandomNickname(rng *rand.Rand) string {
	name := randomNicknameOnce(rng)
	for attempt := 0; attempt < maxConnectorRetries && hasAdjacentDuplicateConnector(name); attempt++ {
		name = randomNicknameOnce(rng)
	}
	return name
}

func randomNicknameOnce(rng *rand.Rand) string {
	if rng.Float64() >= zhChance {
		return randomEnglishNickname(rng)
	}
	if rng.Float64() < zhTemplateChance {
		return randomChineseTemplate(rng)
	}
	return chineseFixedPool[rng.Intn(len(chineseFixedPool))]
}
