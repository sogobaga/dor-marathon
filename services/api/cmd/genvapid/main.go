// 產生一組 VAPID 金鑰對，供 Web Push 使用。
// 用法：
//
//	go run ./cmd/genvapid
//
// 印出的 VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY 設進環境變數即可（另需自訂 VAPID_SUBJECT，
// 例如 mailto:admin@dor.tw 或 https://dor.tw）。此指令不進正式部署流程，僅供產生金鑰用。
package main

import (
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to generate VAPID keys:", err)
		os.Exit(1)
	}

	fmt.Printf("VAPID_PUBLIC_KEY=%s\n", publicKey)
	fmt.Printf("VAPID_PRIVATE_KEY=%s\n", privateKey)
}
