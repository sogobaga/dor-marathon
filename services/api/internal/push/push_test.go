package push

import (
	"net"
	"testing"
)

// M6 資安修補：push_subscriptions.endpoint 白名單比對邏輯的單元測試。
// 重點覆蓋兩類攻擊性輸入：(1) 尾碼混淆（"evilpush.apple.com" 沒有前置的點卻包含
// "push.apple.com" 字串）(2) 路徑/後綴混淆（"fcm.googleapis.com.attacker.com"）。

func TestIsAllowedPushHost(t *testing.T) {
	cases := []struct {
		name string
		host string
		want bool
	}{
		// 完全相符（無 "*." 前綴的三個）
		{"fcm exact", "fcm.googleapis.com", true},
		{"android exact", "android.googleapis.com", true},
		{"mozilla updates exact", "updates.push.services.mozilla.com", true},
		{"fcm case-insensitive", "FCM.GoogleAPIs.com", true},
		{"fcm trailing dot (FQDN)", "fcm.googleapis.com.", true},

		// fcm/android 不允許子網域（規格沒有 "*." 前綴）
		{"fcm subdomain rejected", "evil.fcm.googleapis.com", false},
		{"android subdomain rejected", "evil.android.googleapis.com", false},

		// 萬用字元網域：本身與任一層子網域皆放行
		{"mozilla push bare", "push.services.mozilla.com", true},
		{"mozilla push subdomain", "wpush.prod.push.services.mozilla.com", true},
		{"windows notify bare", "notify.windows.com", true},
		{"windows notify subdomain", "db3.notify.windows.com", true},
		{"apple push bare", "push.apple.com", true},
		{"apple push subdomain (web)", "web.push.apple.com", true},
		{"apple push subdomain (other)", "1-courier.push.apple.com", true},

		// 尾碼混淆：字串裡含合法網域但不是真正的子網域邊界
		{"suffix confusion no dot", "evilpush.apple.com", false},
		{"suffix confusion appended", "fcm.googleapis.com.attacker.com", false},
		{"prefix confusion", "attacker.com/fcm.googleapis.com", false},

		// 完全不相關的網域
		{"unrelated domain", "example.com", false},
		{"internal hostname", "internal-metadata", false},
		{"empty host", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllowedPushHost(tc.host); got != tc.want {
				t.Errorf("isAllowedPushHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestValidatePushEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{"valid fcm", "https://fcm.googleapis.com/fcm/send/abc123", false},
		{"valid apple subdomain", "https://web.push.apple.com/v1/abc", false},
		{"valid mozilla", "https://updates.push.services.mozilla.com/wpush/v2/abc", false},
		{"http not https", "http://fcm.googleapis.com/fcm/send/abc", true},
		{"disallowed host", "https://evil.example.com/fcm/send/abc", true},
		{"internal ip literal", "https://127.0.0.1/steal", true},
		{"metadata service", "https://169.254.169.254/latest/meta-data/", true},
		{"malformed url", "://not-a-url", true},
		{"empty", "", true},
		// scheme-relative / javascript 等不合法 scheme
		{"javascript scheme", "javascript:alert(1)", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePushEndpoint(tc.endpoint)
			if (err != nil) != tc.wantErr {
				t.Errorf("validatePushEndpoint(%q) err = %v, wantErr %v", tc.endpoint, err, tc.wantErr)
			}
		})
	}
}

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"public v4", "8.8.8.8", true},
		{"loopback v4", "127.0.0.1", false},
		{"loopback v6", "::1", false},
		{"rfc1918 10/8", "10.0.0.5", false},
		{"rfc1918 172.16/12", "172.16.0.5", false},
		{"rfc1918 192.168/16", "192.168.1.5", false},
		{"link-local v4", "169.254.169.254", false}, // 雲端 metadata 服務常見位址
		{"link-local v6", "fe80::1", false},
		{"cgnat lower bound", "100.64.0.1", false},
		{"cgnat upper bound", "100.127.255.254", false},
		{"just below cgnat", "100.63.255.255", true},
		{"just above cgnat", "100.128.0.1", true},
		{"unique-local v6", "fc00::1", false},
		{"unspecified v4", "0.0.0.0", false},
		{"multicast v4", "224.0.0.1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) failed", tc.ip)
			}
			if got := isPublicIP(ip); got != tc.want {
				t.Errorf("isPublicIP(%q) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}
