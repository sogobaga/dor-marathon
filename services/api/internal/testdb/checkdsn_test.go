package testdb

// 整合測試連線字串的安全檢查（CheckDSN，以及 OpenSunsetSettings 對它的接線）：
//   - 本機規則：預設只接受 loopback／localhost／unix socket；ALLOW_REMOTE=1 才放行遠端。
//   - 同主機規則：測試連線目標的主機與 DATABASE_URL（正式庫）相同（去掉 "-pooler" 後比對）一律拒絕，ALLOW_REMOTE=1 也一樣。
//
// 全部不連線：CheckDSN 是純函式；OpenSunsetSettings 的接線測試只走到「拒絕」就結束（用會攔 Fatalf 的假 testing.TB），
// 而且用的主機名稱是保留的 .invalid／不可路由位址——萬一守門被拿掉也不會連到任何真實的資料庫。

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

const (
	// 假的「正式庫」：Neon 風格的 pooler 端點（密碼是假的）。
	fakeProdDSN = "postgres://dor_owner:prod-secret-pw@ep-cool-darkness-123456-pooler.ap-southeast-1.aws.neon.tech/neondb?sslmode=require"
)

func TestCheckDSN_LoopbackRule(t *testing.T) {
	local := []string{
		"postgres://postgres:pw@127.0.0.1:55432/dor_e2e?sslmode=disable",
		"postgres://postgres:pw@127.8.9.10/dor_e2e",
		"postgres://postgres:pw@localhost/dor_e2e",
		"postgres://postgres:pw@LOCALHOST:5432/dor_e2e",
		"postgres://postgres:pw@db.localhost/dor_e2e",
		"postgres://postgres:pw@[::1]:5432/dor_e2e",
		"postgres://postgres:pw@/dor_e2e?host=/var/run/postgresql",                           // unix socket 目錄
		"host=127.0.0.1 port=55432 user=postgres password=pw dbname=dor_e2e sslmode=disable", // key=value 寫法
		"postgres://postgres:pw@127.0.0.1:5432,localhost:5433/dor_e2e",                       // 多主機：全部都是本機
	}
	remote := []string{
		"postgres://u:S3cretPw9@ep-cool-darkness-123456.ap-southeast-1.aws.neon.tech/neondb",
		"postgres://u:S3cretPw9@db.example.com/x",
		"postgres://u:S3cretPw9@10.0.0.5:5432/x",
		"postgres://u:S3cretPw9@192.168.1.20/x",
		"postgres://u:S3cretPw9@172.17.0.2/x",
		"postgres://u:S3cretPw9@rml-e2e-pg/x", // docker 服務名稱
		"postgres://u:S3cretPw9@0.0.0.0/x",
		"postgres://u:S3cretPw9@localhost.example.com/x",
		"host=db.example.com user=u password=S3cretPw9 dbname=x",      // key=value 寫法的遠端主機
		"postgres://u:S3cretPw9@127.0.0.1:5432,db.example.com:5432/x", // 多主機：備援主機是遠端也要擋
	}
	for _, dsn := range local {
		for _, allowRemote := range []bool{false, true} {
			if err := CheckDSN(dsn, "", allowRemote); err != nil {
				t.Errorf("local DSN %q (allowRemote=%v) must be accepted: %v", dsn, allowRemote, err)
			}
		}
	}
	for _, dsn := range remote {
		err := CheckDSN(dsn, "", false)
		if err == nil {
			t.Errorf("remote DSN %q must be refused unless %s=1", dsn, EnvAllowRemote)
			continue
		}
		if !strings.Contains(err.Error(), EnvAllowRemote) {
			t.Errorf("the refusal should tell how to opt in (%s): %v", EnvAllowRemote, err)
		}
		if strings.Contains(err.Error(), "S3cretPw9") {
			t.Errorf("the refusal must not echo credentials: %v", err)
		}
		// ALLOW_REMOTE=1 放行遠端（只要不是正式庫的主機）
		if err := CheckDSN(dsn, "", true); err != nil {
			t.Errorf("remote DSN %q must be accepted with %s=1 when it is not the production host: %v", dsn, EnvAllowRemote, err)
		}
	}
	// 連線字串本身壞掉：拒絕，且錯誤不含密碼
	for _, bad := range []string{"", "postgres://u:supersecretpw@host:notaport/x", "://", "host=a port=b"} {
		if err := CheckDSN(bad, "", true); err == nil {
			t.Errorf("unparsable DSN %q must be refused", bad)
		} else if strings.Contains(err.Error(), "supersecretpw") {
			t.Errorf("the error must not echo the password: %v", err)
		}
	}
}

func TestCheckDSN_NeverTheProductionHost(t *testing.T) {
	// 與正式庫同一台主機：不論 ALLOW_REMOTE 都拒絕——包含 pooler／直連端點、大小寫、換庫名或使用者、key=value 寫法、多主機。
	same := map[string]string{
		"the same pooler endpoint":                       "postgres://u:pw@ep-cool-darkness-123456-pooler.ap-southeast-1.aws.neon.tech/other_db",
		"the direct endpoint of the same compute":        "postgres://u:pw@ep-cool-darkness-123456.ap-southeast-1.aws.neon.tech/neondb",
		"upper-case host":                                "postgres://u:pw@EP-COOL-DARKNESS-123456-POOLER.AP-SOUTHEAST-1.AWS.NEON.TECH/x",
		"different user, db and port, same host":         "postgres://other:other@ep-cool-darkness-123456.ap-southeast-1.aws.neon.tech:6543/scratch",
		"key=value form":                                 "host=ep-cool-darkness-123456.ap-southeast-1.aws.neon.tech user=u password=pw dbname=x",
		"multi-host where the second host is production": "postgres://u:pw@127.0.0.1:5432,ep-cool-darkness-123456-pooler.ap-southeast-1.aws.neon.tech:5432/x",
	}
	for name, dsn := range same {
		for _, allowRemote := range []bool{false, true} {
			err := CheckDSN(dsn, fakeProdDSN, allowRemote)
			if err == nil {
				t.Errorf("%s (allowRemote=%v): the production host must always be refused", name, allowRemote)
				continue
			}
			// 訊息不得洩漏正式庫的主機名稱或任何密碼
			for _, leak := range []string{"neon.tech", "ep-cool-darkness", "prod-secret-pw", "dor_owner"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%s (allowRemote=%v): the refusal leaks %q: %v", name, allowRemote, leak, err)
				}
			}
		}
	}
	// 對稱：正式庫用直連端點、測試用 pooler 端點，同樣是同一台
	prodDirect := "postgres://dor_owner:prod-secret-pw@ep-cool-darkness-123456.ap-southeast-1.aws.neon.tech/neondb"
	if err := CheckDSN("postgres://u:pw@ep-cool-darkness-123456-pooler.ap-southeast-1.aws.neon.tech/x", prodDirect, true); err == nil {
		t.Error("pooler test host vs direct production host must be refused")
	}

	// 不是同一台：放行（ALLOW_REMOTE=1 時）——例如另一個 Neon 分支、CI 的服務容器
	other := []string{
		"postgres://u:pw@ep-other-branch-999999.ap-southeast-1.aws.neon.tech/neondb",
		"postgres://u:pw@ep-cool-darkness-123457.ap-southeast-1.aws.neon.tech/neondb", // 只差一個數字
		"postgres://u:pw@ci-postgres/x",
	}
	for _, dsn := range other {
		if err := CheckDSN(dsn, fakeProdDSN, true); err != nil {
			t.Errorf("a different host %q must be accepted with %s=1: %v", dsn, EnvAllowRemote, err)
		}
		if err := CheckDSN(dsn, fakeProdDSN, false); err == nil {
			t.Errorf("a different but remote host %q is still refused without %s=1", dsn, EnvAllowRemote)
		}
	}
	// 本機的拋棄式資料庫＋行程帶著指向 Neon 的 DATABASE_URL：正常放行
	if err := CheckDSN("postgres://postgres:pw@127.0.0.1:55432/dor_e2e", fakeProdDSN, false); err != nil {
		t.Errorf("a loopback throwaway DB must be accepted when DATABASE_URL points elsewhere: %v", err)
	}
	// 兩邊都是 loopback 主機：同主機規則照樣成立（只比主機、不比連接埠），寧可誤擋
	if err := CheckDSN("postgres://postgres:pw@127.0.0.1:55432/dor_e2e", "postgres://u:pw@127.0.0.1:5432/prod", false); err == nil {
		t.Error("the same loopback host as DATABASE_URL must be refused (host-only comparison)")
	}
	// 沒有 DATABASE_URL（空字串）或解析不出主機：沒有東西可比，略過同主機規則（本機規則照常）
	for _, prod := range []string{"", "   ", "not a dsn at all", "sqlite:///x.db"} {
		if err := CheckDSN("postgres://u:pw@db.example.com/x", prod, true); err != nil {
			t.Errorf("an empty or unusable DATABASE_URL (%q) must not block the test DSN: %v", prod, err)
		}
	}
}

// ---------- OpenSunsetSettings 的接線：兩條規則都必須真的被呼叫，而且在連線之前 ----------

// fatalCatcher 攔下 Fatalf／Skipf（用 runtime.Goexit 終止所在的 goroutine，行為同真的 testing.T），讓測試能斷言「被拒絕了」。
// 內嵌 testing.TB：滿足介面的私有方法，其餘沒覆寫的方法委派給真的 *testing.T。
type fatalCatcher struct {
	testing.TB
	fatal   string
	skipped bool
}

func (f *fatalCatcher) Helper() {}
func (f *fatalCatcher) Fatalf(format string, args ...any) {
	f.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}
func (f *fatalCatcher) Skipf(format string, args ...any) {
	f.skipped = true
	runtime.Goexit()
}

// openCatching 在另一個 goroutine 跑 OpenSunsetSettings（Goexit 只會結束那個 goroutine，不會把真的測試標成失敗）。
func openCatching(t *testing.T) *fatalCatcher {
	t.Helper()
	f := &fatalCatcher{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		OpenSunsetSettings(f, "wearable_sunset_state", "wearable_sunset_date")
	}()
	<-done
	return f
}

func TestOpenSunsetSettings_RefusesRemoteHostsByDefault(t *testing.T) {
	// .invalid 是保留的頂層網域（永遠解析不出來）：萬一守門被拿掉，頂多是連線失敗，不會連到任何真實的資料庫
	t.Setenv(EnvDSN, "postgres://u:pw@ep-scratch.example.invalid/x?sslmode=disable&connect_timeout=1")
	t.Setenv(EnvAllowRemote, "")
	t.Setenv(EnvProdDSN, "")
	f := openCatching(t)
	if f.skipped || !strings.Contains(f.fatal, "refusing to run") {
		t.Fatalf("a remote host must be refused before any connection is attempted: skipped=%v fatal=%q", f.skipped, f.fatal)
	}
}

func TestOpenSunsetSettings_NeverConnectsToTheProductionHostEvenWithAllowRemote(t *testing.T) {
	t.Setenv(EnvAllowRemote, "1")
	t.Setenv(EnvProdDSN, "postgres://dor_owner:prod-secret-pw@ep-prod-pooler.example.invalid/neondb?sslmode=require")
	// 測試目標與正式庫同一台（直連端點 vs pooler 端點）：ALLOW_REMOTE=1 也必須拒絕
	t.Setenv(EnvDSN, "postgres://u:pw@ep-prod.example.invalid/neondb?sslmode=disable&connect_timeout=1")
	f := openCatching(t)
	if f.skipped || !strings.Contains(f.fatal, "refusing to run") {
		t.Fatalf("the production host must be refused even with %s=1: skipped=%v fatal=%q", EnvAllowRemote, f.skipped, f.fatal)
	}
	if strings.Contains(f.fatal, "example.invalid") || strings.Contains(f.fatal, "prod-secret-pw") {
		t.Fatalf("the refusal must not leak the production host or credentials: %q", f.fatal)
	}
}

func TestOpenSunsetSettings_SkipsWithoutADSN(t *testing.T) {
	t.Setenv(EnvDSN, "")
	f := openCatching(t)
	if !f.skipped || f.fatal != "" {
		t.Fatalf("no %s must be a Skip: skipped=%v fatal=%q", EnvDSN, f.skipped, f.fatal)
	}
}
