package virtualrunner

import (
	"math/rand"
	"testing"
)

// TestNamePool_Sizes 契約要求「姓 30+，男女名各 40+」——用長度斷言鎖住這個下限，未來誤刪名字時
// 測試會先炸掉。
func TestNamePool_Sizes(t *testing.T) {
	if len(surnames) < 30 {
		t.Fatalf("surnames 需 >=30，實際 %d", len(surnames))
	}
	if len(maleGivenNames) < 40 {
		t.Fatalf("maleGivenNames 需 >=40，實際 %d", len(maleGivenNames))
	}
	if len(femaleGivenNames) < 40 {
		t.Fatalf("femaleGivenNames 需 >=40，實際 %d", len(femaleGivenNames))
	}
}

// TestNamePool_NoDuplicates 池內不應有重複姓名（重複代表複製貼上手誤，會讓某些名字出現機率加倍）。
func TestNamePool_NoDuplicates(t *testing.T) {
	check := func(name string, list []string) {
		t.Helper()
		seen := map[string]bool{}
		for _, v := range list {
			if seen[v] {
				t.Errorf("%s 池內有重複值：%q", name, v)
			}
			seen[v] = true
			if len([]rune(v)) == 0 {
				t.Errorf("%s 池內有空字串", name)
			}
		}
	}
	check("surnames", surnames)
	check("maleGivenNames", maleGivenNames)
	check("femaleGivenNames", femaleGivenNames)
}

// TestGivenNamePool_Gender 依性別回對應池；非 male 一律回女名池（保守預設，不 panic）。
func TestGivenNamePool_Gender(t *testing.T) {
	if &GivenNamePool("male")[0] != &maleGivenNames[0] {
		t.Fatal("gender=male 應回 maleGivenNames")
	}
	if &GivenNamePool("female")[0] != &femaleGivenNames[0] {
		t.Fatal("gender=female 應回 femaleGivenNames")
	}
	if &GivenNamePool("unknown")[0] != &femaleGivenNames[0] {
		t.Fatal("非 male 的未知值應保守回 femaleGivenNames，不 panic")
	}
}

// TestRandomName_ComposedFromPools 產出的全名必須恰為「一個姓 + 一個對應性別的名」組合，
// 而非任意字串；用固定種子跑多次覆蓋隨機分布。
func TestRandomName_ComposedFromPools(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		name := RandomName("male", rng)
		if !hasSurnamePrefix(name, surnames) {
			t.Fatalf("male 姓名 %q 不是以任何 surnames 池內姓氏開頭", name)
		}
		if !hasGivenSuffix(name, maleGivenNames) {
			t.Fatalf("male 姓名 %q 不是以任何 maleGivenNames 池內名字結尾", name)
		}
	}
	rng2 := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		name := RandomName("female", rng2)
		if !hasGivenSuffix(name, femaleGivenNames) {
			t.Fatalf("female 姓名 %q 不是以任何 femaleGivenNames 池內名字結尾", name)
		}
	}
}

func hasSurnamePrefix(name string, pool []string) bool {
	for _, s := range pool {
		if len(name) >= len(s) && name[:len(s)] == s {
			return true
		}
	}
	return false
}

func hasGivenSuffix(name string, pool []string) bool {
	for _, s := range pool {
		if len(name) >= len(s) && name[len(name)-len(s):] == s {
			return true
		}
	}
	return false
}
