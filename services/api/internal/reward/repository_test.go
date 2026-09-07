package reward

import "testing"

// TestSpinLockKeyDistinct 確保 BeginSpinTx 用的 advisory lock 鍵對不同的 (userID, raceID) 組合
// 不會因為字串串接方式而混成同一把鎖（例如不加分隔符時 "ab"+"cd" 與 "a"+"bcd" 會撞成同一個字串），
// 這是 SEC 修補 M5（Spin 併發競態）能真正序列化「不同使用者/不同賽事」而不彼此誤鎖的前提。
func TestSpinLockKeyDistinct(t *testing.T) {
	cases := []struct{ u1, r1, u2, r2 string }{
		{"ab", "cd", "a", "bcd"},        // 邊界：拿掉分隔符會撞在一起的組合
		{"user-1", "race-1", "user-1", "race-2"}, // 同使用者、不同賽事
		{"user-1", "race-1", "user-2", "race-1"}, // 不同使用者、同賽事
	}
	for _, c := range cases {
		k1 := spinLockKey(c.u1, c.r1)
		k2 := spinLockKey(c.u2, c.r2)
		if k1 == k2 {
			t.Errorf("spinLockKey(%q,%q)=%q collided with spinLockKey(%q,%q)=%q", c.u1, c.r1, k1, c.u2, c.r2, k2)
		}
	}
	// 相同輸入必須得到相同鍵（advisory lock 才能真的鎖到同一把）。
	if spinLockKey("u", "r") != spinLockKey("u", "r") {
		t.Fatal("spinLockKey should be deterministic for the same input")
	}
}

// TestSpinConcurrency（SEC 修補 M5）本該驗證：同一 (userID, raceID) 兩個併發 Spin 在剩餘 1 次額度時
// 只有一個能成功、另一個拿到 ErrNoSpinsLeft。但真正的競態發生在 pg_advisory_xact_lock + 交易內重新
// COUNT 這段 SQL 行為上（見 service.go Spin／repository.go BeginSpinTx 註解），Repository.db 是具體的
// *pgxpool.Pool，本 repo 也沒有 sqlmock/pgxmock 可以忠實模擬 advisory lock 造成的「第二個請求必須等
// 第一個交易 COMMIT」這種跨連線阻塞語意——退而求其次用假的 mutex/counter 只能驗證測試本身寫對的邏輯，
// 驗證不到 repository.go 真正送出的 SQL 有沒有序列化，反而是一個會誤導人的假陽性測試，因此在此明確跳過，
// 待專案補上 DB 整合測試基礎設施（真實/testcontainers Postgres）後再補上真正的併發整合測試。
func TestSpinConcurrency(t *testing.T) {
	t.Skip("需要真實 Postgres 才能驗證 advisory lock 是否真的序列化併發 Spin；repo 無 DB 整合測試基礎設施，見上方註解")
}
