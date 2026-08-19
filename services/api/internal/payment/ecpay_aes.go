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

// 綠界站內付2.0 端點基底（供之後 Phase B 組請求 URL 用；本階段尚未串接任何 HTTP 呼叫）。
const (
	ecpayBindStageURL = "https://ecpg-stage.ecpay.com.tw"
	ecpayBindProdURL  = "https://ecpg.ecpay.com.tw"
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

// ecpayBindURLEncode 貼近 .NET HttpUtility.UrlEncode 的輸出，供站內付2.0 Data 欄位加密前的
// UrlEncode 前處理步驟使用。
//
// ⚠️ 這是綠界串接最常見的雷，務必看懂再動這段：
//
//   - .NET HttpUtility.UrlEncode：空白 → '+'；-_.!*() 這幾個字元「不」編碼；其餘保留字元一律編碼成
//     小寫的 %xx（例如 '/' → "%2f"，不是 "%2F"）。
//   - Go 的 url.QueryEscape：空白 → '+'（一致）；但 -_.!*() 會被編碼成 %2D %5F %2E %21 %2A %28 %29
//     （不一致）；且一律輸出大寫 %XX（不一致）。
//
// 因此若原封不動拿 url.QueryEscape 的結果去做 AES 加密，UrlEncode 完的 bytes 就和綠界（.NET）端
// 不一致，AES 加密出來的密文自然也會不同——但要注意：這不影響「解密方向」的正確性，因為加密與
// 解密用的是「同一段 Go 產生的 UrlEncode 結果」自我一致，roundtrip 測試會過；真正會出問題的是
// 「Go 加密 → 送給綠界解密」或「綠界加密 → Go 解密」這種跨語言的場合——若日後實際串接時綠界解密
// 失敗、或 Go 解密不出綠界回傳的 Data，這裡（-_.!*() 是否編碼、大小寫）就是第一個該檢查的地方。
// 這裡選擇貼近 .NET 原生輸出（-_.!*() 不編碼、%xx 小寫），把跨語言不一致的風險降到最低。
//
// -_.!*() 不編碼的邏輯與 payment.go 的 dotNetURLEncode 相同（兩者都是在還原 .NET HttpUtility.UrlEncode
// 的行為），但目的不同、不可共用同一份實作：dotNetURLEncode 之後會把「整段字串」轉小寫（AIO
// CheckMacValue 演算法本身就是大小寫不敏感比對，連帶把英數字內容也轉小寫沒關係）；這裡加密的是
// 真實資料內容（JSON 值本身可能含大小寫敏感的英數字，如卡號末四碼、Base64 片段），只能把「%XX escape
// 序列」轉小寫，不能動到其餘未編碼字元的大小寫。
func ecpayBindURLEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.NewReplacer(
		"%21", "!",
		"%2A", "*",
		"%28", "(",
		"%29", ")",
	).Replace(e)
	return lowerPercentEscapes(e)
}

// lowerPercentEscapes 只把 %XX escape 序列裡的十六進位字母轉小寫，其餘字元原樣保留。
func lowerPercentEscapes(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] == '%' && i+2 < len(b) {
			b[i+1] = toLowerHexDigit(b[i+1])
			b[i+2] = toLowerHexDigit(b[i+2])
			i += 2
		}
	}
	return string(b)
}

func toLowerHexDigit(c byte) byte {
	if c >= 'A' && c <= 'F' {
		return c - 'A' + 'a'
	}
	return c
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
