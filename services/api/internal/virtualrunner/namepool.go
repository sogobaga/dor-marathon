package virtualrunner

import "math/rand"

// 台灣常見姓名池：姓 30+，男女名各 40+（依 gender 取對應池）。名字皆取 2 字常見組合，
// 純虛構、不指向任何真實人物；只用來讓後台建立的虛擬選手看起來像真人跑者。
// randSurname 佔全部姓氏族群前段，randGivenName 依性別各自獨立池，兩者隨機組合成全名。

var surnames = []string{
	"陳", "林", "黃", "張", "李", "王", "吳", "劉", "蔡", "楊",
	"許", "鄭", "謝", "郭", "洪", "曾", "邱", "廖", "賴", "徐",
	"周", "葉", "蘇", "莊", "呂", "江", "何", "蕭", "羅", "高",
	"潘", "朱", "簡", "鍾", "游", "詹",
}

var maleGivenNames = []string{
	"志明", "家豪", "俊傑", "建宏", "承翰", "冠宇", "柏宇", "彥廷", "宗翰", "信宏",
	"育晨", "睿祺", "子軒", "亦辰", "柏翰", "俊宏", "文彬", "家瑋", "建志", "宇軒",
	"冠廷", "承恩", "柏彰", "宗憲", "育宏", "志豪", "俊宇", "冠霖", "彥宇", "柏承",
	"浩宇", "浩然", "彥霖", "宇晨", "柏勳", "承叡", "冠佑", "宗霖", "睿廷", "致遠",
	"孝廉", "明軒", "冠儒", "士豪", "彥澤",
}

var femaleGivenNames = []string{
	"淑芬", "美玲", "雅婷", "怡君", "佳蓉", "詩涵", "靜宜", "思穎", "品妍", "心妤",
	"語彤", "宜蓁", "珮瑜", "昀蓁", "佩璇", "雨萱", "子涵", "詠晴", "沛柔", "芷若",
	"欣妤", "姿穎", "柔安", "芯瑜", "婉婷", "昕妍", "依珊", "恩慈", "芸熙", "若涵",
	"玟萱", "悅寧", "宥蓁", "昱潔", "詩晴", "筠婕", "予涵", "語晨", "亭妤", "品萱",
	"珈妤", "映彤", "曉雯", "珮岑", "書妍",
}

// GivenNamePool 依性別回傳對應的名字池；gender 非 male/female（呼叫端應先用 ValidGender 擋掉）
// 時保守回傳女名池，不 panic。
func GivenNamePool(gender string) []string {
	if gender == "male" {
		return maleGivenNames
	}
	return femaleGivenNames
}

// RandomName 隨機組出一個「姓＋名」的全名，依 gender 取對應名字池。純函式，呼叫端注入 rng
// 以便測試可用固定種子驗證輸出必屬於池內組合。
func RandomName(gender string, rng *rand.Rand) string {
	given := GivenNamePool(gender)
	return surnames[rng.Intn(len(surnames))] + given[rng.Intn(len(given))]
}
