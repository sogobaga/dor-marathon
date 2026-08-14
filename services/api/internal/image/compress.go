package image

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

const (
	maxDimension = 1600
	jpegQuality  = 82
)

// CompressImage 嘗試壓縮圖片：只處理可解碼的 JPEG/PNG；
// 最長邊 >1600px 等比縮小；依是否含透明重編碼成 PNG 或 JPEG(q82)。
// 任何無法安全處理的情況（非 JPEG/PNG、解碼失敗、重編碼後沒有變小、重編碼結果無法再解碼）
// 一律回傳 changed=false，呼叫端應改用原始 data/mime。
func CompressImage(data []byte, mime string) (out []byte, outMime string, changed bool) {
	if mime != "image/jpeg" && mime != "image/png" {
		return data, mime, false
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mime, false
	}

	// 依 EXIF Orientation 把方向「烘焙進像素」（image.Decode 不會套用 EXIF）：避免不同檢視器(手機遵守 EXIF、
	// 直接開圖網址/部分桌機不遵守)對同一張照片顯示方向不一致(上下顛倒/翻轉)。重編碼本就會剝除 EXIF，轉正後即與檢視器無關。
	// orient!=1 時「一律」採用重編碼版(即使沒變小)，否則會留著帶錯方向 EXIF 的原檔，bug 依舊。見 orientation.go。
	orient := exifOrientation(data)

	// 最長邊 >1600 才等比縮小。刻意「先縮圖、後轉正」：轉正對 90 度類(5..8)會交換寬高，但 max(w,h) 不變→縮放
	// 決策與比例相同；且均勻縮放與軸對齊旋轉/翻轉可交換 → 先縮後轉的輸出與先轉後縮「像素完全相同」。先縮可避免在
	// 原始全解析度先配置一份 RGBA 再逐點搬移(24MP 照片可省下約 90MB 暫用記憶體與 2400 萬次像素運算)。
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return data, mime, false
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

	// 轉正（在縮圖之後，見上方註解）
	if orient != 1 {
		img = applyOrientation(img, orient)
	}

	// 重編碼：含 alpha 透明 → PNG；否則 → JPEG q82
	var buf bytes.Buffer
	var newMime string
	if hasAlpha(img) {
		if err := png.Encode(&buf, img); err != nil {
			return data, mime, false
		}
		newMime = "image/png"
	} else {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return data, mime, false
		}
		newMime = "image/jpeg"
	}

	encoded := buf.Bytes()

	// 安全閥：方向正常(orient==1)時，重編碼沒比原始小就放棄（維持原本不無謂膨脹的行為）；
	// 但方向非正常時，一律採用已轉正的重編碼版——方向正確性優先於體積（否則會退回帶錯方向 EXIF 的原檔）。
	if orient == 1 && len(encoded) >= len(data) {
		return data, mime, false
	}

	// 再 decode 一次確認不是壞圖
	if _, _, err := image.Decode(bytes.NewReader(encoded)); err != nil {
		return data, mime, false
	}

	return encoded, newMime, true
}

// hasAlpha 檢查影像是否含有非全不透明的 alpha 通道畫素。
// JPEG 解碼結果（image.YCbCr/image.Gray）恆無透明度，直接走快速路徑判 false；
// PNG 解碼結果常見 *image.RGBA/*image.NRGBA，各自有快速路徑；其餘型別退回逐點檢查。
func hasAlpha(img image.Image) bool {
	switch m := img.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		_ = m
		return false
	case *image.RGBA:
		return rgbaHasAlpha(m)
	case *image.NRGBA:
		return nrgbaHasAlpha(m)
	}

	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a != 0xffff {
				return true
			}
		}
	}
	return false
}

func rgbaHasAlpha(m *image.RGBA) bool {
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if m.RGBAAt(x, y).A != 0xff {
				return true
			}
		}
	}
	return false
}

func nrgbaHasAlpha(m *image.NRGBA) bool {
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if m.NRGBAAt(x, y).A != 0xff {
				return true
			}
		}
	}
	return false
}
