package image

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// ErrUnsupportedMime ReencodeStrict 只收 image/jpeg 與 image/png。
var ErrUnsupportedMime = errors.New("unsupported mime")

// ReencodeStrict 無條件重新編碼（不看是否變小）。成功即保證：輸出是純像素重新編碼的結果，
// EXIF（含 GPS 座標）、XMP、ICC、檔尾附加的 HTML/JS/ZIP payload、polyglot 前後綴全部消失。
//
// ⚠️ 為什麼不能沿用 CompressImage：compress.go 有「orient==1 且沒變小就放棄」的安全閥
// （見該檔「安全閥」註解），會對「已高度壓縮的正常小圖」回 changed=false 並吐回原始 data。
// 若把 changed 當安全判定（＝把「沒變小」誤讀成「無法安全處理」），會誤拒真實使用者的正常照片
// ——這是會打到使用者的可用性 bug，不是理論風險。本函式移除該安全閥（安全 > 體積），
// 其餘流程（EXIF 方向烘焙、長邊 1600 等比縮圖、有 alpha 走 PNG、無 alpha 走 JPEG q82、
// 重編碼後二次 Decode 驗證）與 CompressImage 完全相同。
//
// 呼叫端必須自己先做 MIME 嗅探與 DecodeConfig 交叉比對（見 internal/runmeet/imageupload.go）；
// 本函式只負責「重新編碼」這一步，不做格式白名單以外的安全判斷。
func ReencodeStrict(data []byte, mime string) (out []byte, outMime string, err error) {
	if mime != "image/jpeg" && mime != "image/png" {
		return nil, "", ErrUnsupportedMime
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}

	// 依 EXIF Orientation 把方向「烘焙進像素」（image.Decode 不會套用 EXIF）；重編碼本就會剝除
	// EXIF，轉正後即與檢視器無關。與 CompressImage 相同，刻意「先縮圖、後轉正」（見該檔註解：
	// 均勻縮放與軸對齊旋轉可交換，先縮可省下全解析度暫存記憶體）。
	orient := exifOrientation(data)

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, "", errors.New("empty image bounds")
	}
	if w > maxDimension || h > maxDimension {
		var nw, nh int
		if w >= h {
			nw = maxDimension
			nh = int(float64(h) * float64(maxDimension) / float64(w))
		} else {
			nh = maxDimension
			nw = int(float64(w) * float64(maxDimension) / float64(h))
		}
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		img = dst
	}

	if orient != 1 {
		img = applyOrientation(img, orient)
	}

	var buf bytes.Buffer
	var newMime string
	if hasAlpha(img) {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		newMime = "image/png"
	} else {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, "", err
		}
		newMime = "image/jpeg"
	}

	encoded := buf.Bytes()

	// ⚠️ 這裡刻意「沒有」CompressImage 的 len(encoded) >= len(data) 安全閥——見函式註解。

	// 再 decode 一次確認輸出不是壞圖（與 CompressImage 同一道把關）
	if _, _, err := image.Decode(bytes.NewReader(encoded)); err != nil {
		return nil, "", err
	}

	return encoded, newMime, nil
}
