package virtualrunner

import (
	"fmt"
	"math/rand"
)

// 綽號生成器：虛擬選手不再用「姓＋名」的真人姓名格式（避免看起來像在冒用真實人物），改生成
// 帶跑步 App 暱稱生態感的綽號——中文中二風（前綴×後綴組合＋少量自嘲幽默款）約 70%、
// 英文風（形容詞×名詞＋多種大小寫/底線/數字尾綴變化）約 30%。
//
// RandomNickname 為唯一對外入口，純函式、呼叫端注入 rng 以便測試用固定種子驗證分布；
// 不再吃 gender 參數（綽號中性），gender 仍照舊只存 user_profiles 供推薦排序等用途。

// zhChanceOfHumor Chinese 分支中，走「自嘲幽默款」獨立池（而非前綴×後綴組合）的機率；
// 保持「少量」混入、不搶掉組合式的主體地位。
const zhChanceOfHumor = 0.15

// enChanceOfChinese 整體走中文分支的機率（≈70%），其餘落入英文分支（≈30%）。
const enChanceOfChinese = 0.7

// chinesePrefixes 中二風前綴（34 個，皆 2 字），語感取自玄幻/熱血動漫常見詞彙，純虛構組合詞，
// 不指向任何真實人物或既有 IP 角色名稱。
var chinesePrefixes = []string{
	"疾風", "暗影", "烈焰", "蒼穹", "星夜", "破曉", "銀月", "雷霆", "孤狼", "幻影",
	"緋紅", "夜梟", "風暴", "凜冬", "流星", "薄暮", "斷罪", "深淵", "熾天", "逐日",
	"赤焰", "寒冰", "蒼狼", "黑曜", "幽冥", "逆風", "狂沙", "紫電", "白夜", "焚天",
	"滅世", "天穹", "極光", "蝕月",
}

// chineseSuffixes 中二風後綴（22 個，2-3 字），刻意挑跑步相關詞（跑者/衝刺者/追風者/夜跑人）
// 混入奇幻職業詞（劍客/騎士/獵手），貼近真實跑步 App 暱稱「中二但扣題跑步」的生態。
var chineseSuffixes = []string{
	"行者", "跑者", "之翼", "獵手", "騎士", "旅人", "劍客", "殘影", "傳說", "衝刺者",
	"追風者", "夜跑人", "破軍", "狂徒", "使者", "遊俠", "先鋒", "王者", "刺客", "幻者",
	"追夢人", "逐風者",
}

// humorNicknames 自嘲幽默款獨立池（17 個），不經前綴×後綴組合、整條直接採用——真實跑步社群
// 暱稱常見的另一種風格（不是耍帥而是自嘲），混入少量增加整體真實感。
var humorNicknames = []string{
	"追風的蝸牛", "奔跑的火鍋", "配速刺客", "破PB的男人", "佛系跑者",
	"週末戰士", "龜速前進中", "心率爆表王", "補給站戰神", "賽道邊緣人",
	"呼吸困難但快樂", "膝蓋在抗議", "芭樂配速", "跑到懷疑人生", "收操魔人",
	"起跑線常客", "關門時間求生者",
}

// englishAdjectives 英文風形容詞池（22 個）。
var englishAdjectives = []string{
	"Shadow", "Storm", "Night", "Blaze", "Frost", "Phantom", "Crimson", "Silent", "Neon", "Zero",
	"Iron", "Solar", "Lunar", "Rapid", "Feral", "Wild", "Cosmic", "Electric", "Savage", "Golden",
	"Silver", "Midnight",
}

// englishNouns 英文風名詞池（17 個）。
var englishNouns = []string{
	"Runner", "Wolf", "Chaser", "Striker", "Wing", "Blade", "Falcon", "Pacer", "Dash", "Ghost",
	"Fox", "Hawk", "Viper", "Bolt", "Strider", "Rider", "Racer",
}

// RandomNickname 隨機產出一個跑者綽號：≈70% 中文中二風（少量自嘲幽默款）、≈30% 英文風。
// 純函式，呼叫端注入 rng 以便測試用固定種子驗證輸出分布與長度上限。
func RandomNickname(rng *rand.Rand) string {
	if rng.Float64() < enChanceOfChinese {
		return randomChineseNickname(rng)
	}
	return randomEnglishNickname(rng)
}

// randomChineseNickname 中文分支：多數走「前綴×後綴」組合式（三種拼法之一），少量（≈15%）
// 直接採用 humorNicknames 整條自嘲款。組合式最長 prefix(2)+"之"(1)+suffix(3)=6 字，
// 遠低於 10 字上限；humorNicknames 最長 7 字，同樣在上限內。
func randomChineseNickname(rng *rand.Rand) string {
	if rng.Float64() < zhChanceOfHumor {
		return humorNicknames[rng.Intn(len(humorNicknames))]
	}
	prefix := chinesePrefixes[rng.Intn(len(chinesePrefixes))]
	suffix := chineseSuffixes[rng.Intn(len(chineseSuffixes))]
	switch rng.Intn(3) {
	case 0:
		return prefix + suffix // A+B：疾風行者
	case 1:
		return prefix + "之" + suffix // A之B：疾風之行者
	default:
		return suffix + "·" + prefix // B·A：行者·疾風
	}
}

// randomEnglishNickname 英文分支：形容詞×名詞，混用 4 種常見暱稱格式——SpaceCase／CamelCase／
// 加底線／單詞+兩位數字尾綴（如 Runner_77，模擬帳號被搶註後常見的加數字妥協）。最長組合
// "Midnight"+"Striker" 底線=16 字元，"MidnightStriker"+"_99" 數字尾綴=18 字元，皆在 20 字元上限內。
func randomEnglishNickname(rng *rand.Rand) string {
	adj := englishAdjectives[rng.Intn(len(englishAdjectives))]
	noun := englishNouns[rng.Intn(len(englishNouns))]
	switch rng.Intn(4) {
	case 0:
		return adj + " " + noun // SpaceCase: "Shadow Runner"
	case 1:
		return adj + noun // CamelCase: "ShadowRunner"
	case 2:
		return adj + "_" + noun // 加底線: "Shadow_Runner"
	default:
		return fmt.Sprintf("%s_%d", noun, 10+rng.Intn(90)) // 單詞+兩位數字: "Runner_77"
	}
}
