package race

import (
	"context"
	"errors"
	"testing"
)

// TestCreateVipOrder_ValidationRejectsBeforeTouchingDB 驗證 CreateVipOrder 的輸入檢查
// （userID/amountCents/plan）全部發生在 r.db.Begin 之前——因此用 db=nil 的 Repository 也能測，
// 不需要真的接 DB（本套件本來就沒有 DB 整合測試的地基）。
func TestCreateVipOrder_ValidationRejectsBeforeTouchingDB(t *testing.T) {
	r := &Repository{}
	ctx := context.Background()

	if _, _, err := r.CreateVipOrder(ctx, "", "monthly", 29900, ""); err == nil {
		t.Fatalf("expected error for empty userID, got nil")
	}
	if _, _, err := r.CreateVipOrder(ctx, "user-1", "monthly", 0, ""); err == nil {
		t.Fatalf("expected error for zero amountCents, got nil")
	}
	if _, _, err := r.CreateVipOrder(ctx, "user-1", "monthly", -100, ""); err == nil {
		t.Fatalf("expected error for negative amountCents, got nil")
	}
	if _, _, err := r.CreateVipOrder(ctx, "user-1", "weekly", 29900, ""); !errors.Is(err, ErrInvalidVipPlan) {
		t.Fatalf("expected ErrInvalidVipPlan for unknown plan, got %v", err)
	}
}
