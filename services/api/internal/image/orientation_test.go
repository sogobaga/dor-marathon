package image

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// tiffIFD0Orientation 造一段 little-endian TIFF（IFD0 只放一個 Orientation 條目）。
func tiffIFD0Orientation(o byte) []byte {
	return []byte{
		'I', 'I', // little-endian
		0x2A, 0x00, // TIFF magic 42
		0x08, 0x00, 0x00, 0x00, // IFD0 offset = 8
		0x01, 0x00, // 條目數 = 1
		0x12, 0x01, // tag 0x0112 (Orientation)
		0x03, 0x00, // type = SHORT
		0x01, 0x00, 0x00, 0x00, // count = 1
		o, 0x00, 0x00, 0x00, // value (SHORT，靠左)
		0x00, 0x00, 0x00, 0x00, // 下一個 IFD = 0
	}
}

// jpegWithOrientation 把一張圖編成 JPEG，並在 SOI 後插入帶指定 Orientation 的 Exif APP1 段。
func jpegWithOrientation(t *testing.T, img image.Image, o byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jb := buf.Bytes()
	payload := append([]byte("Exif\x00\x00"), tiffIFD0Orientation(o)...)
	segLen := len(payload) + 2
	app1 := []byte{0xFF, 0xE1, byte(segLen >> 8), byte(segLen & 0xFF)}
	app1 = append(app1, payload...)
	out := make([]byte, 0, len(jb)+len(app1))
	out = append(out, jb[0], jb[1]) // FFD8 (SOI)
	out = append(out, app1...)
	out = append(out, jb[2:]...)
	return out
}

func TestExifOrientationReadsAllValues(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for o := byte(1); o <= 8; o++ {
		data := jpegWithOrientation(t, img, o)
		if got := exifOrientation(data); got != int(o) {
			t.Errorf("orientation %d: got %d", o, got)
		}
	}
	// 無 EXIF 的乾淨 JPEG → 1
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	if got := exifOrientation(buf.Bytes()); got != 1 {
		t.Errorf("plain jpeg: got %d, want 1", got)
	}
	// 非 JPEG → 1
	if got := exifOrientation([]byte{0x89, 'P', 'N', 'G'}); got != 1 {
		t.Errorf("png-ish: got %d, want 1", got)
	}
	// 越界值 → 1
	if got := exifOrientation(jpegWithOrientation(t, img, 9)); got != 1 {
		t.Errorf("orientation 9 (invalid): got %d, want 1", got)
	}
}

func TestApplyOrientationRotate180(t *testing.T) {
	A := color.RGBA{10, 0, 0, 255}
	B := color.RGBA{0, 20, 0, 255}
	C := color.RGBA{0, 0, 30, 255}
	D := color.RGBA{40, 40, 0, 255}
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, A)
	src.Set(1, 0, B)
	src.Set(0, 1, C)
	src.Set(1, 1, D)
	dst := applyOrientation(src, 3) // 旋轉 180 → 對角互換
	want := map[[2]int]color.RGBA{{0, 0}: D, {1, 0}: C, {0, 1}: B, {1, 1}: A}
	for p, wc := range want {
		if got := color.RGBAModel.Convert(dst.At(p[0], p[1])).(color.RGBA); got != wc {
			t.Errorf("rot180 at %v: got %v want %v", p, got, wc)
		}
	}
}

func TestApplyOrientationRotate90CW(t *testing.T) {
	// 水平 A|B（2×1），orientation 6 = 順時針 90 → A 在上、B 在下（1×2）
	A := color.RGBA{10, 0, 0, 255}
	B := color.RGBA{0, 20, 0, 255}
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, A)
	src.Set(1, 0, B)
	dst := applyOrientation(src, 6)
	if dst.Bounds().Dx() != 1 || dst.Bounds().Dy() != 2 {
		t.Fatalf("dims: got %v, want 1x2", dst.Bounds())
	}
	if got := color.RGBAModel.Convert(dst.At(0, 0)).(color.RGBA); got != A {
		t.Errorf("rot90CW top: got %v want A", got)
	}
	if got := color.RGBAModel.Convert(dst.At(0, 1)).(color.RGBA); got != B {
		t.Errorf("rot90CW bottom: got %v want B", got)
	}
}

// 端到端：orientation=3 的 JPEG 經 CompressImage 後，像素應已轉正(左右對調)，且 changed=true。
func TestCompressImageBakesOrientation(t *testing.T) {
	// 左半紅、右半藍；旋轉 180 後應變成 左半藍、右半紅
	w, hgt := 64, 32
	src := image.NewRGBA(image.Rect(0, 0, w, hgt))
	red := color.RGBA{220, 20, 20, 255}
	blue := color.RGBA{20, 20, 220, 255}
	for y := 0; y < hgt; y++ {
		for x := 0; x < w; x++ {
			if x < w/2 {
				src.Set(x, y, red)
			} else {
				src.Set(x, y, blue)
			}
		}
	}
	data := jpegWithOrientation(t, src, 3)
	out, outMime, changed := CompressImage(data, "image/jpeg")
	if !changed {
		t.Fatal("changed = false, want true (orientation!=1 必須採用轉正版)")
	}
	if outMime != "image/jpeg" {
		t.Fatalf("mime = %s", outMime)
	}
	dec, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	// 轉正後：左邊界應偏藍、右邊界應偏紅（JPEG 有損 → 用 R vs B 相對大小判定，容忍壓縮誤差）
	left := color.RGBAModel.Convert(dec.At(3, hgt/2)).(color.RGBA)
	right := color.RGBAModel.Convert(dec.At(w-4, hgt/2)).(color.RGBA)
	if !(left.B > left.R) {
		t.Errorf("left pixel should be blue-ish after 180 rotation, got %v", left)
	}
	if !(right.R > right.B) {
		t.Errorf("right pixel should be red-ish after 180 rotation, got %v", right)
	}
}
