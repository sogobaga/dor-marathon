package race

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ---- needsForceRefreshOnMiss 純函式：查無 slug 時是否該多做一次同步刷新 ----

func TestNeedsForceRefreshOnMiss(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		valid bool
		at    time.Time
		grace time.Duration
		want  bool
	}{
		{"從未成功載入過必定強制刷新", false, time.Time{}, raceMetaMissRefreshGrace, true},
		{"剛載入不久內查無=表格是權威的、不刷新", true, now, raceMetaMissRefreshGrace, false},
		{"剛好達到grace邊界=刷新", true, now.Add(-raceMetaMissRefreshGrace), raceMetaMissRefreshGrace, true},
		{"未達grace邊界前一秒=不刷新", true, now.Add(-raceMetaMissRefreshGrace + time.Second), raceMetaMissRefreshGrace, false},
		{"表格已經很舊(遠超過grace)=刷新", true, now.Add(-time.Hour), raceMetaMissRefreshGrace, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsForceRefreshOnMiss(tc.valid, tc.at, now, tc.grace); got != tc.want {
				t.Errorf("needsForceRefreshOnMiss(valid=%v, at=%v, now=%v, grace=%v) = %v, want %v",
					tc.valid, tc.at, now, tc.grace, got, tc.want)
			}
		})
	}
}

// ---- Service.GetRaceMeta：串起 ttlcache + needsForceRefreshOnMiss 的整體行為，用假 load（無 DB）----

// newTestMetaService 建一個只掛了 raceMetaCache 的最小 Service（其餘欄位在 GetRaceMeta 路徑用不到），
// 供不碰 DB 的整合測試使用。load 由呼叫端提供，可用來模擬「map 隨時間變化」（例如剛核准上線的新賽事）。
func newTestMetaService(load func(ctx context.Context) (map[string]RaceMeta, error)) *Service {
	return &Service{raceMetaCache: newRaceMetaCache(load)}
}

func TestGetRaceMeta_HitDoesNotTriggerExtraLoad(t *testing.T) {
	var calls int
	s := newTestMetaService(func(ctx context.Context) (map[string]RaceMeta, error) {
		calls++
		return map[string]RaceMeta{"demo": {Slug: "demo", Title: "示範賽事"}}, nil
	})

	meta, ok := s.GetRaceMeta(context.Background(), "demo")
	if !ok {
		t.Fatal("GetRaceMeta(\"demo\") ok = false, want true")
	}
	if meta.Title != "示範賽事" {
		t.Fatalf("meta.Title = %q, want 示範賽事", meta.Title)
	}
	if calls != 1 {
		t.Fatalf("load called %d times for a cache hit, want 1 (只有首次同步載入)", calls)
	}
}

func TestGetRaceMeta_FreshMissDoesNot404ThroughExtraLoad(t *testing.T) {
	// 表格剛載入（valid 且 age=0 < 60s grace）：查無的 slug 應直接回 not-found，不多花一次刷新。
	var calls int
	s := newTestMetaService(func(ctx context.Context) (map[string]RaceMeta, error) {
		calls++
		return map[string]RaceMeta{"demo": {Slug: "demo"}}, nil
	})

	s.GetRaceMeta(context.Background(), "demo") // 觸發首次載入，calls=1，快取剛變 fresh

	_, ok := s.GetRaceMeta(context.Background(), "does-not-exist")
	if ok {
		t.Fatal("GetRaceMeta(\"does-not-exist\") ok = true, want false")
	}
	if calls != 1 {
		t.Fatalf("load called %d times, want still 1（表格剛載入、grace 內查無不該多刷新）", calls)
	}
}

func TestGetRaceMeta_StaleMissForcesOneRefreshThenFound(t *testing.T) {
	// 模擬「賽事剛核准上線，10 分鐘 TTL 還沒到、快取表已經很舊」的情境：查無時應強制刷新一次，
	// 刷新後查到就回傳。
	//
	// 第 2 次 load 刻意加一點延遲：Get() 對「有舊值可用但已過期」的情況，回傳舊值的同時會另起一個
	// 背景 goroutine 做刷新（見 ttlcache.Cache.Get）。若這裡的假 load 完全不花時間，背景刷新有極
	// 小機率在本測試碼呼叫 Peek() 之前就搶先跑完，讓 Peek() 看到「剛好也是最新」的狀態、進而不觸發
	// GetRaceMeta 的強制刷新分支，使斷言結果隨排程運氣不同（flaky）。真實情境下 DB 查詢至少要幾
	// 毫秒，這個排程窗口不可能被真的搶到；這裡加短暫延遲只是讓測試本身穩定重現同一條路徑。
	gen := 0
	s := newTestMetaService(func(ctx context.Context) (map[string]RaceMeta, error) {
		gen++
		if gen == 1 {
			return map[string]RaceMeta{}, nil // 舊表：還沒有這場賽事
		}
		time.Sleep(20 * time.Millisecond)
		return map[string]RaceMeta{"new-race": {Slug: "new-race", Title: "新賽事"}}, nil
	})

	// 首次載入拿到空表（模擬尚未收錄）。
	if _, ok := s.GetRaceMeta(context.Background(), "new-race"); ok {
		t.Fatal("首次載入時 new-race 不該存在")
	}
	// 手動把 at 撥回超過 grace，模擬「表格已經很舊」（不用真的等 60 秒）。
	s.raceMetaCache.Invalidate()

	meta, ok := s.GetRaceMeta(context.Background(), "new-race")
	if !ok {
		t.Fatal("表格過舊時查無應強制刷新一次，刷新後應該找到 new-race")
	}
	if meta.Title != "新賽事" {
		t.Fatalf("meta.Title = %q, want 新賽事", meta.Title)
	}
	if gen != 2 {
		t.Fatalf("load 總共被呼叫 %d 次, want 2（初次 + 強制刷新各一次）", gen)
	}
}

func TestGetRaceMeta_StaleMissStillNotFoundStaysNotFound(t *testing.T) {
	var calls int
	s := newTestMetaService(func(ctx context.Context) (map[string]RaceMeta, error) {
		calls++
		return map[string]RaceMeta{}, nil // 永遠是空表：這個 slug 真的不存在
	})
	s.GetRaceMeta(context.Background(), "ghost") // calls=1
	s.raceMetaCache.Invalidate()                 // 模擬表格已過舊

	_, ok := s.GetRaceMeta(context.Background(), "ghost")
	if ok {
		t.Fatal("ghost 真的不存在，強制刷新後仍應回 not-found")
	}
	if calls != 2 {
		t.Fatalf("load 被呼叫 %d 次, want 2（初次 + 強制刷新各一次，之後不再重試）", calls)
	}
}

func TestGetRaceMeta_LoadErrorOnFirstCallReturnsNotFound(t *testing.T) {
	s := newTestMetaService(func(ctx context.Context) (map[string]RaceMeta, error) {
		return nil, errors.New("db down")
	})
	if _, ok := s.GetRaceMeta(context.Background(), "anything"); ok {
		t.Fatal("首次載入即失敗（從未成功過）應回 not-found，而不是 panic 或誤回 true")
	}
}
