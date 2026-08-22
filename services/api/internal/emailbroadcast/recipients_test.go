package emailbroadcast

import (
	"reflect"
	"testing"
)

func TestCleanRecipientEmails_TrimsLowercasesDedupes(t *testing.T) {
	valid, invalid := cleanRecipientEmails([]string{
		"  Foo@Example.com  ", "foo@example.com", "FOO@EXAMPLE.COM", "bar@example.com",
	})
	wantValid := []string{"foo@example.com", "bar@example.com"}
	if !reflect.DeepEqual(valid, wantValid) {
		t.Fatalf("valid = %v, want %v", valid, wantValid)
	}
	if len(invalid) != 0 {
		t.Fatalf("expected no invalid entries, got %v", invalid)
	}
}

func TestCleanRecipientEmails_SkipsBlankEntries(t *testing.T) {
	valid, invalid := cleanRecipientEmails([]string{"", "   ", "a@b.com", ""})
	if !reflect.DeepEqual(valid, []string{"a@b.com"}) {
		t.Fatalf("valid = %v, want [a@b.com]", valid)
	}
	if len(invalid) != 0 {
		t.Fatalf("expected no invalid entries, got %v", invalid)
	}
}

func TestCleanRecipientEmails_FlagsMalformedAsInvalid(t *testing.T) {
	valid, invalid := cleanRecipientEmails([]string{
		"not-an-email", "missing-domain@", "@missing-local.com", "no-at-sign.com", "no-dot@domain",
		"good@example.com",
	})
	if !reflect.DeepEqual(valid, []string{"good@example.com"}) {
		t.Fatalf("valid = %v, want [good@example.com]", valid)
	}
	wantInvalid := []string{"not-an-email", "missing-domain@", "@missing-local.com", "no-at-sign.com", "no-dot@domain"}
	if !reflect.DeepEqual(invalid, wantInvalid) {
		t.Fatalf("invalid = %v, want %v", invalid, wantInvalid)
	}
}

func TestCleanRecipientEmails_PreservesFirstSeenOrder(t *testing.T) {
	valid, _ := cleanRecipientEmails([]string{"c@x.com", "a@x.com", "b@x.com", "a@x.com"})
	want := []string{"c@x.com", "a@x.com", "b@x.com"}
	if !reflect.DeepEqual(valid, want) {
		t.Fatalf("valid = %v, want %v", valid, want)
	}
}

func TestCleanRecipientEmails_EmptyInput(t *testing.T) {
	valid, invalid := cleanRecipientEmails(nil)
	if len(valid) != 0 || len(invalid) != 0 {
		t.Fatalf("expected empty valid/invalid for nil input, got valid=%v invalid=%v", valid, invalid)
	}
}

func TestCustomAudienceLabel(t *testing.T) {
	if got := customAudienceLabel(0); got != "custom:0人" {
		t.Fatalf("customAudienceLabel(0) = %q, want custom:0人", got)
	}
	if got := customAudienceLabel(42); got != "custom:42人" {
		t.Fatalf("customAudienceLabel(42) = %q, want custom:42人", got)
	}
}

func TestMaxCustomRecipients_IsFivehundred(t *testing.T) {
	// 防呆上限鎖在 500——若之後刻意調整，這個測試會提醒同步檢查前端/文件的說法。
	if maxCustomRecipients != 500 {
		t.Fatalf("maxCustomRecipients = %d, want 500", maxCustomRecipients)
	}
}
