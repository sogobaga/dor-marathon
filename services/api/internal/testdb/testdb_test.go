package testdb

import "testing"

// 純函式：主機判斷。整合測試會寫入並刪除 app_settings 的列，所以只准連本機（拋棄式容器）。
func TestRequireThrowawayHost(t *testing.T) {
	for _, ok := range []string{
		"", "localhost", "LOCALHOST", "db.localhost", "127.0.0.1", "127.1.2.3", "::1", "[::1]", "/var/run/postgresql", "/tmp",
	} {
		if err := RequireThrowawayHost(ok); err != nil {
			t.Errorf("host %q must be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"ep-cool-darkness-123456.ap-southeast-1.aws.neon.tech", // 雲端資料庫
		"db.example.com", "10.0.0.5", "192.168.1.20", "172.17.0.2", // 內網（容器網路）IP
		"postgres", "rml-e2e-pg", // docker 服務名稱
		"0.0.0.0", "8.8.8.8", "::", "fe80::1", "localhost.example.com", "notlocalhost",
	} {
		err := RequireThrowawayHost(bad)
		if err == nil {
			t.Errorf("host %q must be refused", bad)
		}
	}
}
