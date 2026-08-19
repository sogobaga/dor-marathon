package payment

import (
	"bytes"
	"testing"
)

// 站內付2.0測試 HashKey/HashIV（沿用 config.go 的公開測試碼預設值，非站內付2.0 專屬——見
// internal/config/config.go ECPayBindHashKey/ECPayBindHashIV 上方註解）。
const (
	testBindHashKey = "pwFHCqoQZGmho4w6"
	testBindHashIV  = "EkRm7iFT261dpevs"
)

// TestEcpayAESRoundTrip 含中文與特殊字元的 JSON 經 encrypt → decrypt 後必須完全還原。
func TestEcpayAESRoundTrip(t *testing.T) {
	const plaintext = `{"MerchantMemberID":"U123","Note":"測試會員 & <script> \"quote's\" 100% 半形/全形／","CardLast4":"1234","Amount":1999}`

	cipherB64, err := ecpayAESEncrypt(testBindHashKey, testBindHashIV, plaintext)
	if err != nil {
		t.Fatalf("ecpayAESEncrypt: %v", err)
	}
	if cipherB64 == "" {
		t.Fatal("ecpayAESEncrypt returned empty ciphertext")
	}

	got, err := ecpayAESDecrypt(testBindHashKey, testBindHashIV, cipherB64)
	if err != nil {
		t.Fatalf("ecpayAESDecrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("roundtrip mismatch:\n want: %s\n got:  %s", plaintext, got)
	}
}

// TestPKCS7PadUnpad 涵蓋「長度剛好是 16 倍數」與「非 16 倍數」兩種情況——PKCS7 規定即使明文長度
// 剛好是 block size 的倍數，也必須補上「一整個 block」的 padding（而不是不補），否則 unpad 時無法
// 分辨「剛好沒有 padding」與「真的有 padding」。
func TestPKCS7PadUnpad(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"exact multiple of block size", []byte("0123456789ABCDEF")}, // 16 bytes
		{"not multiple of block size", []byte("hello world")},        // 11 bytes
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			padded := pkcs7Pad(c.data, 16)
			if len(padded)%16 != 0 {
				t.Fatalf("padded length %d is not a multiple of 16 (input len %d)", len(padded), len(c.data))
			}
			if len(padded) <= len(c.data) {
				t.Fatalf("padded length %d did not grow beyond input len %d — PKCS7 must always add padding", len(padded), len(c.data))
			}
			unpadded, err := pkcs7Unpad(padded, 16)
			if err != nil {
				t.Fatalf("pkcs7Unpad: %v", err)
			}
			if !bytes.Equal(unpadded, c.data) {
				t.Fatalf("unpad mismatch: want %q got %q", c.data, unpadded)
			}
		})
	}
}

// TestEcpayAESOfficialVectors 用「綠界官方 ECPay-API-Skill」提供的測試向量
// （test-vectors/aes-encryption.json，HashKey/HashIV 為官方向量專用測試值）逐 byte 驗證：
//   - 加密方向：我們的輸出必須與官方 expected_base64 完全一致（跨語言互通的黃金驗證——
//     這組向量曾抓到本檔第一版把 aesUrlEncode 誤寫成 CheckMacValue 式編碼的 bug）。
//   - 解密方向：官方密文解回來必須等於原始明文。
//
// 此測試若壞掉，代表加密位元輸出與綠界端不一致——先對照官方向量判斷是哪個環節
// （UrlEncode 規則／padding／key-iv 取法）被改壞，不要直接更新期望值。
func TestEcpayAESOfficialVectors(t *testing.T) {
	const vecHashKey = "ejCk326UnaZWKisg"
	const vecHashIV = "q9jcZX8Ib9LM8wYk"

	cases := []struct {
		name      string
		plaintext string
		wantB64   string
	}{
		{
			"基本（插入順序 JSON key）",
			`{"MerchantID":"2000132","BarCode":"/1234567"}`,
			"XeEOdHpTRvxKEqs/JD9RSd16s7VtpyWVCN6AV44pKTW3DVa6yI7vKmjBRp2eulDhXoru/qBqFDBH3fEqlkMn3bbJfJBfGAq+v+SvttutYnc=",
		},
		{
			"基本（字母序 JSON key）",
			`{"BarCode":"/1234567","MerchantID":"2000132"}`,
			"r0JSyF9wVmywUav725b3rdJs3xp/ekrC/7PGb18zhKyXkPsamV9l4rPnBkaaraPcHtMSwrmSPP3wuS7b8g/aAKGs0iGiknpgpbdXKXvFrYM=",
		},
		{
			"特殊字元 !*'()~（驗證 aesUrlEncode 與 CMV 編碼不混用）",
			`{"Name":"test!*'()~value"}`,
			"uvI4yrErM37XNQkXGAgRgBuDOiJoVs72Xn/rum9Ejl1DSna4HyLSoY7764PmhTR7JXb9jJWLSjCGcZEDeFiABg==",
		},
		{
			"PKCS7 16-byte 邊界（URL encode 後剛好 32 bytes，需補整個 padding block）",
			`{"N":"1234567890"}`,
			"gVwWJnIpl1m3ZDypcRAjiCctilYnQhHn4h8OzJP5IxQPov7HuysXX+jPONvrHS7Z",
		},
		{
			"UTF-8 中文",
			`{"MerchantID":"2000132","ItemName":"綠界科技測試商品"}`,
			"XeEOdHpTRvxKEqs/JD9RSd16s7VtpyWVCN6AV44pKTVKsXddZRgV+Cle9oeB2PqsEC2O0oDi4kObiCtdGznG9aAX69Kj0//VjGXhieBYZ3RuGW9v20xQyBevaBwtOvg1lYjlDw6jsgfToGMUvlGsIJ2DO6/tbXjNZumnRgj2GCSj7LLDRBU3KlkUWji16nO1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ecpayAESEncrypt(vecHashKey, vecHashIV, c.plaintext)
			if err != nil {
				t.Fatalf("ecpayAESEncrypt: %v", err)
			}
			if got != c.wantB64 {
				t.Fatalf("官方向量加密不一致:\n want: %s\n got:  %s", c.wantB64, got)
			}
			back, err := ecpayAESDecrypt(vecHashKey, vecHashIV, c.wantB64)
			if err != nil {
				t.Fatalf("ecpayAESDecrypt: %v", err)
			}
			if back != c.plaintext {
				t.Fatalf("官方向量解密不一致:\n want: %s\n got:  %s", c.plaintext, back)
			}
		})
	}
}

// TestEcpayBindURLEncodeOfficialVectors 直接驗證 aesUrlEncode 輸出與官方 expected_url_encoded 一致
// （url-encode-comparison.json／aes-encryption.json 的中間值）。
func TestEcpayBindURLEncodeOfficialVectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"Name":"test!*'()~value"}`, "%7B%22Name%22%3A%22test%21%2A%27%28%29%7Evalue%22%7D"},
		{`{"MerchantID":"2000132","BarCode":"/1234567"}`, "%7B%22MerchantID%22%3A%222000132%22%2C%22BarCode%22%3A%22%2F1234567%22%7D"},
		{`{"N":"1234567890"}`, "%7B%22N%22%3A%221234567890%22%7D"},
	}
	for _, c := range cases {
		if got := ecpayBindURLEncode(c.in); got != c.want {
			t.Fatalf("aesUrlEncode 與官方向量不一致:\n in:   %s\n want: %s\n got:  %s", c.in, c.want, got)
		}
	}
}
