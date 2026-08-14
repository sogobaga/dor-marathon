package image

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// 2×2 具四個相異角色的測圖
func quad() (*image.RGBA, [4]color.RGBA) {
	A := color.RGBA{10, 0, 0, 255}
	B := color.RGBA{0, 20, 0, 255}
	C := color.RGBA{0, 0, 30, 255}
	D := color.RGBA{40, 40, 0, 255}
	m := image.NewRGBA(image.Rect(0, 0, 2, 2))
	m.Set(0, 0, A)
	m.Set(1, 0, B)
	m.Set(0, 1, C)
	m.Set(1, 1, D)
	return m, [4]color.RGBA{A, B, C, D}
}

func at(img image.Image, x, y int) color.RGBA {
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

// 逐一驗證 orientation 2,4,5,7,8 的精確像素落點（3、6 已在 orientation_test.go 覆蓋）。
func TestApplyOrientationRemainingCases(t *testing.T) {
	type cell struct {
		x, y int
		want color.RGBA
	}
	src, c := quad()
	A, B, C, D := c[0], c[1], c[2], c[3]
	cases := []struct {
		o     int
		dw, dh int
		cells []cell
	}{
		// 2 水平翻轉：左右鏡射
		{2, 2, 2, []cell{{0, 0, B}, {1, 0, A}, {0, 1, D}, {1, 1, C}}},
		// 4 垂直翻轉：上下鏡射
		{4, 2, 2, []cell{{0, 0, C}, {1, 0, D}, {0, 1, A}, {1, 1, B}}},
		// 5 轉置（主對角線鏡射）(x,y)->(y,x)
		{5, 2, 2, []cell{{0, 0, A}, {1, 0, C}, {0, 1, B}, {1, 1, D}}},
		// 7 反轉置（反對角線鏡射）
		{7, 2, 2, []cell{{0, 0, D}, {1, 0, B}, {0, 1, C}, {1, 1, A}}},
		// 8 逆時針 90
		{8, 2, 2, []cell{{0, 0, B}, {1, 0, D}, {0, 1, A}, {1, 1, C}}},
	}
	for _, tc := range cases {
		dst := applyOrientation(src, tc.o)
		if dst.Bounds().Dx() != tc.dw || dst.Bounds().Dy() != tc.dh {
			t.Errorf("o=%d dims: got %v want %dx%d", tc.o, dst.Bounds(), tc.dw, tc.dh)
		}
		for _, cl := range tc.cells {
			if got := at(dst, cl.x, cl.y); got != cl.want {
				t.Errorf("o=%d at (%d,%d): got %v want %v", tc.o, cl.x, cl.y, got, cl.want)
			}
		}
	}
}

// 大端序（MM）TIFF：驗證 parser 的 byte-order 分支（LE 已由既有測試覆蓋）
func tiffIFD0OrientationBE(o byte) []byte {
	return []byte{
		'M', 'M', // big-endian
		0x00, 0x2A, // magic 42
		0x00, 0x00, 0x00, 0x08, // IFD0 offset = 8
		0x00, 0x01, // 條目數 = 1
		0x01, 0x12, // tag 0x0112
		0x00, 0x03, // type SHORT
		0x00, 0x00, 0x00, 0x01, // count = 1
		0x00, o, 0x00, 0x00, // value SHORT（大端序，高位在前）
		0x00, 0x00, 0x00, 0x00, // 下一個 IFD = 0
	}
}

func jpegWithTIFF(t *testing.T, img image.Image, tiff []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jb := buf.Bytes()
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segLen := len(payload) + 2
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen & 0xFF)}
	app1 = append(app1, payload...)
	out := make([]byte, 0, len(jb)+len(app1))
	out = append(out, jb[0], jb[1])
	out = append(out, app1...)
	out = append(out, jb[2:]...)
	return out
}

func TestExifOrientationBigEndian(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for o := byte(1); o <= 8; o++ {
		data := jpegWithTIFF(t, img, tiffIFD0OrientationBE(o))
		if got := exifOrientation(data); got != int(o) {
			t.Errorf("BE orientation %d: got %d", o, got)
		}
	}
}

// 損壞/截斷輸入不得 panic，且回傳 1
func splitImage(w, h int, left, right color.RGBA) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				m.Set(x, y, left)
			} else {
				m.Set(x, y, right)
			}
		}
	}
	return m
}

// 縮圖(>1600)＋旋轉180 一起走：驗證輸出被壓到 <=1600 且方向已轉正（先縮後轉的組合路徑，先前測試未覆蓋）
func TestCompressImageResizeThenOrient180(t *testing.T) {
	red := color.RGBA{220, 20, 20, 255}
	blue := color.RGBA{20, 20, 220, 255}
	data := jpegWithOrientation(t, splitImage(1700, 200, red, blue), 3)
	out, _, changed := CompressImage(data, "image/jpeg")
	if !changed {
		t.Fatal("changed=false, want true")
	}
	dec, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	bd := dec.Bounds()
	if bd.Dx() > 1600 || bd.Dy() > 1600 {
		t.Fatalf("未壓到 <=1600: %v", bd)
	}
	l := at(dec, 3, bd.Dy()/2)
	r := at(dec, bd.Dx()-4, bd.Dy()/2)
	if !(l.B > l.R) {
		t.Errorf("旋轉180 後左側應偏藍, got %v", l)
	}
	if !(r.R > r.B) {
		t.Errorf("旋轉180 後右側應偏紅, got %v", r)
	}
}

// 縮圖(>1600)＋順時針90 一起走：驗證寬高交換後仍 <=1600 且左(紅)轉到上、右(藍)轉到下
func TestCompressImageResizeThenOrient90(t *testing.T) {
	red := color.RGBA{220, 20, 20, 255}
	blue := color.RGBA{20, 20, 220, 255}
	data := jpegWithOrientation(t, splitImage(1700, 100, red, blue), 6)
	out, _, changed := CompressImage(data, "image/jpeg")
	if !changed {
		t.Fatal("changed=false, want true")
	}
	dec, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	bd := dec.Bounds()
	if bd.Dx() > 1600 || bd.Dy() > 1600 {
		t.Fatalf("未壓到 <=1600: %v", bd)
	}
	if bd.Dy() <= bd.Dx() {
		t.Errorf("順時針90 後應為直式(高>寬), got %v", bd)
	}
	top := at(dec, bd.Dx()/2, 3)
	bot := at(dec, bd.Dx()/2, bd.Dy()-4)
	if !(top.R > top.B) {
		t.Errorf("順時針90 後上緣應偏紅, got %v", top)
	}
	if !(bot.B > bot.R) {
		t.Errorf("順時針90 後下緣應偏藍, got %v", bot)
	}
}

func TestExifOrientationMalformedNoPanic(t *testing.T) {
	inputs := [][]byte{
		{},
		{0xFF},
		{0xFF, 0xD8},
		{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, // APP1 宣稱超長
		{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x08, 'E', 'x', 'i', 'f', 0x00, 0x00}, // Exif 但 TIFF 太短
		append([]byte{0xFF, 0xD8}, bytes.Repeat([]byte{0xFF, 0xE0, 0x00, 0x03, 0x00}, 3)...),
	}
	for i, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("input %d panicked: %v", i, r)
				}
			}()
			if got := exifOrientation(in); got != 1 {
				t.Errorf("input %d: got %d want 1", i, got)
			}
		}()
	}
}
