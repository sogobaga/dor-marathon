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

// TestEcpayAESEncryptKnownOutput 自我回歸：固定 HashKey/HashIV + 固定明文，防日後不小心改壞
// UrlEncode 規則／padding／key-iv 取法等演算法細節卻沒發現（AES-CBC 在固定 key/iv 下是決定性的，
// 沒有隨機性，因此輸出可以寫死比對）。這組 Base64 是用目前實作跑出來的結果；這個測試若壞掉，
// 代表加密的位元輸出變了——先判斷是「刻意修正貼近綠界規格」還是「不小心改壞」，不要直接更新期望值。
func TestEcpayAESEncryptKnownOutput(t *testing.T) {
	const plaintext = `{"MerchantID":"3002607","Note":"固定測試 100%"}`
	const wantB64 = "e5At4EjZSE50Pts+oFxqQ93f3o+EpvwdqhaMzEyaTxn3FUSxZu/+5Ix60tJ/9ZGSqS5xyeX01lron8kSMRwg7rCoPHbOhz5ATlfwpRz1n2nprdjS+7pTUVYJd7NneBxwzDmqmPjMgR1O/XCFwXFpdg=="

	got, err := ecpayAESEncrypt(testBindHashKey, testBindHashIV, plaintext)
	if err != nil {
		t.Fatalf("ecpayAESEncrypt: %v", err)
	}
	if got != wantB64 {
		t.Fatalf("known-output regression mismatch:\n want: %s\n got:  %s", wantB64, got)
	}
}
