package appsettings

import (
	"context"
	"sync/atomic"
	"testing"
)

// key 變更回呼（OnChange／NotifyChange）：團練同步跑緊急關閉旗標的接線點（契約 §4 M2）。
// 純記憶體邏輯，不碰 DB。

func TestOnChangeFiresOnlyForTheRegisteredKey(t *testing.T) {
	var got []string
	off := OnChange("test_key_a", func(_ context.Context, v string) { got = append(got, v) })
	defer off()

	NotifyChange(context.Background(), "test_key_a", "hidden")
	NotifyChange(context.Background(), "test_key_b", "ignored") // 別的 key 不觸發
	NotifyChange(context.Background(), "test_key_a", "")        // 空字串（清除設定）也要通知
	if len(got) != 2 || got[0] != "hidden" || got[1] != "" {
		t.Fatalf("got %q, want [hidden \"\"]", got)
	}
}

func TestOnChangeSupportsMultipleCallbacksAndUnregister(t *testing.T) {
	var a, b int32
	offA := OnChange("test_key_multi", func(context.Context, string) { atomic.AddInt32(&a, 1) })
	offB := OnChange("test_key_multi", func(context.Context, string) { atomic.AddInt32(&b, 1) })
	defer offB()

	NotifyChange(context.Background(), "test_key_multi", "x")
	if atomic.LoadInt32(&a) != 1 || atomic.LoadInt32(&b) != 1 {
		t.Fatalf("both callbacks must fire: a=%d b=%d", a, b)
	}
	offA()
	NotifyChange(context.Background(), "test_key_multi", "x")
	if atomic.LoadInt32(&a) != 1 || atomic.LoadInt32(&b) != 2 {
		t.Fatalf("after unregistering a: a=%d b=%d", a, b)
	}
	offA() // 重複取消註冊不得 panic
}

// 回呼 panic 不得影響後台儲存設定這個主流程，也不得擋住其他回呼。
func TestNotifyChangeRecoversFromPanicAndContinues(t *testing.T) {
	var after int32
	off1 := OnChange("test_key_panic", func(context.Context, string) { panic("boom") })
	defer off1()
	off2 := OnChange("test_key_panic", func(context.Context, string) { atomic.AddInt32(&after, 1) })
	defer off2()

	NotifyChange(context.Background(), "test_key_panic", "x") // 不得 panic
	if atomic.LoadInt32(&after) != 1 {
		t.Fatal("a panicking callback must not prevent the others from running")
	}
}

// 後台請求即使在寫入後立刻被取消，回呼（例如寫 kill 旗標）也必須跑得完：ctx 與請求取消脫鉤，且帶逾時。
func TestNotifyChangeDetachesFromRequestCancellation(t *testing.T) {
	var canceled, hasDeadline bool
	off := OnChange("test_key_ctx", func(ctx context.Context, _ string) {
		canceled = ctx.Err() != nil
		_, hasDeadline = ctx.Deadline()
	})
	defer off()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	NotifyChange(ctx, "test_key_ctx", "x")
	if canceled {
		t.Fatal("callback context must not inherit the request's cancellation")
	}
	if !hasDeadline {
		t.Fatal("callback context must carry a timeout")
	}
}

func TestNotifyChangeWithoutCallbacksIsNoop(t *testing.T) {
	NotifyChange(context.Background(), "test_key_nobody", "x") // 不得 panic、不得阻塞
}

// 團練同步跑的六個設定鍵必須登記在 specs（否則後台 PUT /admin/app-settings/{key} 會回 400 unknown setting）。
func TestRunmeetLiveKeysAreRegistered(t *testing.T) {
	for _, key := range []string{
		"runmeet_live_entry_state", "runmeet_live_whitelist", "runmeet_live_max",
		"runmeet_live_pre_minutes", "runmeet_live_default_hours", "runmeet_live_grace_minutes",
	} {
		if known, _ := ValidateValue(key, ""); !known {
			t.Errorf("%s is not registered in specs", key)
		}
	}
	if known, _ := ValidateValue("runmeet_live_nonexistent", ""); known {
		t.Error("ValidateValue must report unknown keys")
	}
}
