package race

import "testing"

// 寵物雲端馬拉松成績規則（2026-09-09 owner request，migration 174，D4）：涵蓋 EffectivePetScoreMode
// 所有 (petKind, mode, forOwner, forPet) 組合的表格測試，以及 ComposeScore 三種規則 + 未知字串的
// 防呆退化。純函式測試，不需要 DB。

func TestEffectivePetScoreMode(t *testing.T) {
	cases := []struct {
		name             string
		petKind, mode    string
		forOwner, forPet bool
		want             string
	}{
		{"非寵物賽事恆owner_even_if_mode_set", "", PetScoreModeOwnerPetSum, true, true, PetScoreModeOwner},
		{"非寵物賽事恆owner_flags無關", "", PetScoreModePet, false, false, PetScoreModeOwner},
		{"寵物賽事_預設空字串_雙勾選", "dog", "", true, true, ""},
		{"寵物賽事_pet模式_雙勾選", "dog", PetScoreModePet, true, true, PetScoreModePet},
		{"寵物賽事_加總模式_雙勾選", "dog", PetScoreModeOwnerPetSum, true, true, PetScoreModeOwnerPetSum},
		{"for_pet關閉_強制owner_即使設定加總", "dog", PetScoreModeOwnerPetSum, true, false, PetScoreModeOwner},
		{"for_pet關閉_強制owner_即使設定pet", "dog", PetScoreModePet, true, false, PetScoreModeOwner},
		{"for_owner關閉且加總模式_退化成pet", "dog", PetScoreModeOwnerPetSum, false, true, PetScoreModePet},
		{"for_owner關閉但pet模式_維持pet", "dog", PetScoreModePet, false, true, PetScoreModePet},
		{"for_owner關閉但空字串模式_維持空字串", "dog", "", false, true, ""},
		{"雙關閉_for_pet優先判定owner", "dog", PetScoreModeOwnerPetSum, false, false, PetScoreModeOwner},
		{"貓賽事同dog邏輯", "cat", PetScoreModeOwnerPetSum, true, true, PetScoreModeOwnerPetSum},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EffectivePetScoreMode(c.petKind, c.mode, c.forOwner, c.forPet)
			if got != c.want {
				t.Fatalf("EffectivePetScoreMode(%q,%q,%v,%v) = %q, want %q",
					c.petKind, c.mode, c.forOwner, c.forPet, got, c.want)
			}
		})
	}
}

func TestComposeScore(t *testing.T) {
	cases := []struct {
		name           string
		mode           string
		ownerKm, petKm float64
		want           float64
	}{
		{"owner模式只看飼主", PetScoreModeOwner, 10, 5, 10},
		{"空字串等同owner", "", 10, 5, 10},
		{"pet模式只看狗狗", PetScoreModePet, 10, 5, 5},
		{"加總模式相加", PetScoreModeOwnerPetSum, 10, 5, 15},
		{"未知字串退化成owner_最保守", "bogus", 10, 5, 10},
		{"零值", PetScoreModeOwnerPetSum, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ComposeScore(c.mode, c.ownerKm, c.petKm); got != c.want {
				t.Fatalf("ComposeScore(%q,%v,%v) = %v, want %v", c.mode, c.ownerKm, c.petKm, got, c.want)
			}
		})
	}
}

// TestEffectivePetScoreMode_NonPetRaceIgnoresGroupFlags 是 D6「非寵物賽事零行為改變」的直接斷言：
// 不論 for_owner/for_pet/mode 怎麼組合，petKind=="" 一律回 owner，等同 migration 174 之前的行為
// （呼叫端也應該在 petKind=="" 時完全跳過 pet_activities 查詢，見各呼叫端註解）。
func TestEffectivePetScoreMode_NonPetRaceIgnoresGroupFlags(t *testing.T) {
	for _, mode := range []string{"", PetScoreModePet, PetScoreModeOwnerPetSum, "unknown"} {
		for _, fo := range []bool{true, false} {
			for _, fp := range []bool{true, false} {
				if got := EffectivePetScoreMode("", mode, fo, fp); got != PetScoreModeOwner {
					t.Fatalf("EffectivePetScoreMode(\"\",%q,%v,%v) = %q, want owner", mode, fo, fp, got)
				}
			}
		}
	}
}

// TestWirePetScoreMode 鎖住 2026-09-09 review 抓到的 critical bug 的修法：EffectivePetScoreMode 的
// 內部 sentinel "owner" 絕不能原樣序列化成 JSON 的 pet_score_mode 欄位（前端一律用「非空字串＝寵物
// 賽事」判斷要不要秀飼主/狗狗拆分，見 RaceDetailScreen.tsx／RaceRankingScreen.tsx），否則每一場
// 非寵物賽事都會被誤判成寵物賽事。"pet"／"owner_pet_sum"／"" 三個合法 wire 值必須原樣通過。
func TestWirePetScoreMode(t *testing.T) {
	cases := []struct{ in, want string }{
		{PetScoreModeOwner, ""},
		{"", ""},
		{PetScoreModePet, PetScoreModePet},
		{PetScoreModeOwnerPetSum, PetScoreModeOwnerPetSum},
	}
	for _, c := range cases {
		if got := wirePetScoreMode(c.in); got != c.want {
			t.Fatalf("wirePetScoreMode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
