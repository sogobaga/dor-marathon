package image

import "image"

// EXIF Orientation 處理（純標準庫，無外部相依）：
// 手機拍照常在檔案寫入 EXIF Orientation 標籤（值 1..8），像素本身維持相機感光元件方向，靠標籤告訴檢視器該如何轉正。
// 問題：不同檢視器對 EXIF 的遵守度不一致——手機/多數瀏覽器的 <img> 會遵守，但「直接開圖網址」或部分桌機情境不遵守，
// 於是同一張照片在不同裝置看起來方向不同（上下顛倒/翻轉）。Go 標準庫 image.Decode「不」套用 EXIF Orientation。
// 解法：上傳時把方向「烘焙進像素」（實際旋轉/翻轉像素）並在重編碼時剝除 EXIF，讓儲存的圖永遠是正的、與檢視器無關。

// exifOrientation 解析 JPEG 的 EXIF Orientation（tag 0x0112），回傳 1..8；非 JPEG／無 EXIF／解析失敗一律回 1（正常）。
func exifOrientation(data []byte) int {
	// 必須是 JPEG（SOI = 0xFFD8）；PNG 等一律視為正常方向
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+2 <= len(data) {
		if data[i] != 0xFF {
			return 1 // 非標記位元組，保守放棄
		}
		marker := data[i+1]
		// 無長度欄位的獨立標記：SOI/EOI/RSTn/TEM
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if i+4 > len(data) {
			return 1
		}
		segLen := int(data[i+2])<<8 | int(data[i+3]) // 含長度欄位本身的 2 bytes
		if segLen < 2 || i+2+segLen > len(data) {
			return 1
		}
		if marker == 0xE1 { // APP1：可能是 Exif
			if o, ok := parseExifOrientation(data[i+4 : i+2+segLen]); ok {
				return o
			}
			// 是 APP1 但非 Exif（例如 XMP）→ 繼續往後找
		}
		if marker == 0xDA { // SOS：影像資料開始，後面沒有中繼資料了
			return 1
		}
		i += 2 + segLen
	}
	return 1
}

// parseExifOrientation 解析 APP1 payload（應以 "Exif\0\0" 開頭）內 TIFF/IFD0 的 Orientation 值。
func parseExifOrientation(p []byte) (int, bool) {
	if len(p) < 6 || string(p[0:4]) != "Exif" || p[4] != 0 || p[5] != 0 {
		return 0, false
	}
	tiff := p[6:]
	if len(tiff) < 8 {
		return 0, false
	}
	var bigEndian bool
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I': // II = little-endian
		bigEndian = false
	case tiff[0] == 'M' && tiff[1] == 'M': // MM = big-endian
		bigEndian = true
	default:
		return 0, false
	}
	u16 := func(b []byte) uint16 {
		if bigEndian {
			return uint16(b[0])<<8 | uint16(b[1])
		}
		return uint16(b[1])<<8 | uint16(b[0])
	}
	u32 := func(b []byte) uint32 {
		if bigEndian {
			return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		}
		return uint32(b[3])<<24 | uint32(b[2])<<16 | uint32(b[1])<<8 | uint32(b[0])
	}
	if u16(tiff[2:4]) != 42 { // TIFF magic
		return 0, false
	}
	ifdOff := int(u32(tiff[4:8]))
	if ifdOff < 8 || ifdOff+2 > len(tiff) {
		return 0, false
	}
	count := int(u16(tiff[ifdOff : ifdOff+2]))
	entryBase := ifdOff + 2
	for k := 0; k < count; k++ {
		e := entryBase + k*12
		if e+12 > len(tiff) {
			return 0, false
		}
		if u16(tiff[e:e+2]) == 0x0112 { // Orientation
			val := int(u16(tiff[e+8 : e+10])) // SHORT，值靠左放在 value 欄位前 2 bytes
			if val >= 1 && val <= 8 {
				return val, true
			}
			return 0, false
		}
	}
	return 0, false
}

// applyOrientation 依 EXIF Orientation 值把像素轉正，回傳新影像（1 或超出範圍則原樣回傳）。
// 對照 EXIF 規格：2=水平翻轉 3=旋轉180 4=垂直翻轉 5=轉置 6=順時針90 7=反轉置 8=逆時針90（5..8 會交換寬高）。
func applyOrientation(src image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 { // 轉置/旋轉90 類：寬高交換
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, c)
		}
	}
	return dst
}
