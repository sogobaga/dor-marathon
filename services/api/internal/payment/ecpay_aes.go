// 綠界站內付2.0（信用卡定期定額/幕後綁卡請款）Data 欄位的 AES-128-CBC 加解密。
//
// 這是與 payment.go 既有 AIO CheckMacValue（SHA256 簽章）完全不同、獨立的加密機制，兩者不共用
// 任何邏輯——AIO 是「用 HashKey/HashIV 算出一組雜湊值放進 CheckMacValue 欄位供驗證」，站內付2.0
// 是「用 HashKey/HashIV 當 AES 金鑰/IV，把整包請求/回應 JSON 加密後放進 Data 欄位傳輸」。
package payment

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// 綠界站內付2.0 端點基底。⚠️ 站內付2.0 橫跨兩個不同 domain，混用會直接 404（見官方文件與
// guides/02-payment-ecpg.md「Domain 警告」段）：
//   - ecpg(-stage).ecpay.com.tw：Token 取得／建立交易／綁卡系列 API（GetTokenbyBindingCard、
//     CreateBindCard、CreatePaymentWithCardID、GetMemberBindCard、DeleteMemberBindCard 皆在此）。
//   - ecpayment(-stage).ecpay.com.tw：查詢／請退款系列 API（QueryTrade、DoAction 等）。
//     QueryTrade（Phase D 對抗式審查修正：主動收斂 unknown 扣款狀態，見 vip_renewal.go）走這個
//     domain，但封包格式（MerchantID+RqHeader+Data 三層、AES-128-CBC 加密 Data）與 ecpg 系列完全
//     相同，只是 base URL 不同——因此 BindClient 需要同時持有兩個 base（見 NewBindClient／call 的
//     baseURL 參數化），不能只有單一 c.BaseURL。
const (
	ecpayBindStageURL = "https://ecpg-stage.ecpay.com.tw"
	ecpayBindProdURL  = "https://ecpg.ecpay.com.tw"

	ecpayQueryStageURL = "https://ecpayment-stage.ecpay.com.tw"
	ecpayQueryProdURL  = "https://ecpayment.ecpay.com.tw"
)

// ecpayAESEncrypt 依綠界站內付2.0 Data 欄位加密規格：
//
//	明文 JSON 字串 → UrlEncode → AES-128-CBC(PKCS7 padding, key=HashKey 前16 bytes, iv=HashIV 前16 bytes) → Base64
//
// HashKey/HashIV 皆為 16 字元 ASCII 字串，AES-128 金鑰/IV 剛好需要 16 bytes，直接取前 16 bytes 使用。
func ecpayAESEncrypt(hashKey, hashIV, plaintextJSON string) (string, error) {
	key, iv, err := ecpayAESKeyIV(hashKey, hashIV)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("ecpay aes encrypt: new cipher: %w", err)
	}

	encoded := ecpayBindURLEncode(plaintextJSON)
	padded := pkcs7Pad([]byte(encoded), aes.BlockSize)

	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// ecpayAESDecrypt 為 ecpayAESEncrypt 的逆運算：Base64 decode → AES-128-CBC 解密 → UrlDecode → 還原 JSON 字串。
func ecpayAESDecrypt(hashKey, hashIV, b64Cipher string) (string, error) {
	key, iv, err := ecpayAESKeyIV(hashKey, hashIV)
	if err != nil {
		return "", err
	}

	ciphertext, err := base64.StdEncoding.DecodeString(b64Cipher)
	if err != nil {
		return "", fmt.Errorf("ecpay aes decrypt: base64 decode: %w", err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("ecpay aes decrypt: ciphertext length %d is not a positive multiple of block size %d", len(ciphertext), aes.BlockSize)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("ecpay aes decrypt: new cipher: %w", err)
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plaintext, ciphertext)

	unpadded, err := pkcs7Unpad(plaintext, aes.BlockSize)
	if err != nil {
		return "", fmt.Errorf("ecpay aes decrypt: %w", err)
	}

	// url.QueryUnescape 對大小寫混合的 %XX 皆能正確解析（不像加密方向那樣 Go 與 .NET 的大寫/小寫輸出
	// 不同），這個方向相對安全，不需要額外處理大小寫。
	decoded, err := url.QueryUnescape(string(unpadded))
	if err != nil {
		return "", fmt.Errorf("ecpay aes decrypt: url unescape: %w", err)
	}
	return decoded, nil
}

// ecpayAESKeyIV 取 HashKey/HashIV 前 16 bytes 當 AES-128 的 key/iv（兩者皆須至少 16 字元 ASCII）。
func ecpayAESKeyIV(hashKey, hashIV string) (key, iv []byte, err error) {
	if len(hashKey) < 16 {
		return nil, nil, fmt.Errorf("ecpay aes: hashKey must be at least 16 bytes (got %d)", len(hashKey))
	}
	if len(hashIV) < 16 {
		return nil, nil, fmt.Errorf("ecpay aes: hashIV must be at least 16 bytes (got %d)", len(hashIV))
	}
	return []byte(hashKey[:16]), []byte(hashIV[:16]), nil
}

// ecpayBindURLEncode 綠界 AES 加密前的 UrlEncode（官方稱 aesUrlEncode），供站內付2.0 Data 欄位
// 加密前處理使用。
//
// ⚠️ 綠界有「兩種」URL encode，絕對不可混用（混用會 TransCode != 1，是官方列的最常見串接錯誤）：
//
//   - ecpayUrlEncode（CheckMacValue/AIO 用，見 payment.go dotNetURLEncode）：urlencode → 全轉小寫 →
//     .NET 字元還原（-_.!*() 還原為不編碼）。
//   - aesUrlEncode（AES/站內付2.0 用，＝本函式）：「純 urlencode」——!*'() 照樣編碼成 %21 %2A %27
//     %28 %29、十六進位一律大寫、不轉小寫、不做 .NET 還原。
//
// 依綠界官方 ECPay-API-Skill 的 Go 參考實作（test-vectors/verify-go.go aesUrlEncode）與官方測試向量
// （test-vectors/aes-encryption.json、url-encode-comparison.json）：Go 的 url.QueryEscape 與 PHP
// urlencode 只差一個字元——'~'（Go 保留、PHP 編成 %7E），補上替換即可完全一致。
// ※ QueryEscape 的輸出不會自行產生 '~'，因此 ReplaceAll 只會命中原文中的 '~'，安全。
// 本檔測試以官方向量逐 byte 驗證輸出（見 ecpay_aes_test.go 的官方向量測試）。
func ecpayBindURLEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "~", "%7E")
}

// pkcs7Pad 標準 PKCS7 padding（block size 16，AES block size）。
func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	padding := bytes.Repeat([]byte{byte(padLen)}, padLen)
	out := make([]byte, 0, len(data)+padLen)
	out = append(out, data...)
	out = append(out, padding...)
	return out
}

// pkcs7Unpad 驗證並移除標準 PKCS7 padding，padding 不合法（長度為 0、超過 blockSize、或內容不一致）
// 一律回錯，避免 padding oracle 類問題靜默吃掉錯誤資料。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	n := len(data)
	if n == 0 || n%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded data length %d", n)
	}
	padLen := int(data[n-1])
	if padLen == 0 || padLen > blockSize || padLen > n {
		return nil, fmt.Errorf("invalid pkcs7 padding length %d", padLen)
	}
	for _, c := range data[n-padLen:] {
		if int(c) != padLen {
			return nil, fmt.Errorf("invalid pkcs7 padding bytes")
		}
	}
	return data[:n-padLen], nil
}
