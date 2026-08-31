package runmeet

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg" // DecodeConfig 需要註冊 JPEG/PNG 解碼器
	_ "image/png"
	"io"
	"net/http"

	dorimage "github.com/dor/api/internal/image"
)

// 團練圖片上傳（規格 1.3）。專屬端點，**不共用** /admin/images 那條鏈——
// 那條信任 client 送的 Content-Type 且放行整個 image/*（SVG 直通 → 儲存型 XSS）。
//
// 完整管線：
//  ① http.MaxBytesReader(5MB+1KB)
//  ② ParseMultipartForm → FormFile("file")（欄位名 file，沿用 api.ts 既有 FormData 慣例）
//  ③ io.LimitReader 讀滿 5MB+1，超過 → 413
//  ④ http.DetectContentType 嗅探，**完全忽略** part header 宣告的 Content-Type
//  ⑤ 白名單只收 image/jpeg、image/png
//  ⑥ image.DecodeConfig：format 必須與嗅探結果一致（防 magic-byte polyglot）；
//     尺寸上限 8000×8000 且總像素 ≤ 40M（防 decompression bomb）
//  ⑦ 取全域解碼併發信號量（cap=4），防 N 張大圖同時 Decode 撐爆容器
//  ⑧ image.ReencodeStrict：無條件重編碼 → EXIF(含 GPS)、XMP、ICC、檔尾附加 payload 全部消失
//  ⑨ 寫 images（owner_user_id=uid, purpose='runmeet'）
//  ⑩ 回 {id, url}，並設 X-Content-Type-Options: nosniff

const (
	maxUploadBytes = 5 << 20
	maxImageDim    = 8000
	maxImagePixels = 40_000_000
)

// decodeSem 全域解碼併發信號量。解碼一張 8000×8000 的圖需要約 256MB RGBA，
// 沒有這道閘門的話 10 個並行請求就能把 Railway 容器打到 OOM。
var decodeSem = make(chan struct{}, 4)

var (
	errImageFormat   = newErr(http.StatusBadRequest, "只接受 JPG 或 PNG 圖片，請換一張再試。")
	errImageTooLarge = newErr(http.StatusRequestEntityTooLarge, "圖片超過 5MB，請壓縮後再上傳。")
	errImageDims     = newErr(http.StatusBadRequest, "照片解析度過高，請先縮小後再上傳。")
	errImageBroken   = newErr(http.StatusBadRequest, "這張圖片無法安全處理，請改用相簿的原始 JPG／PNG 重新上傳。")
	errImageMissing  = newErr(http.StatusBadRequest, "缺少檔案欄位 file。")
)

// InsertImage 寫入 images 表（帶 owner/purpose，供授權刪除與孤兒 GC）。
func (r *Repository) InsertImage(ctx context.Context, ownerID, mime string, data []byte) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `
		INSERT INTO images (mime, data, size, owner_user_id, purpose)
		VALUES ($1,$2,$3,$4,'runmeet') RETURNING id`, mime, data, len(data), ownerID).Scan(&id)
	return id, err
}

// processUpload 執行 ③–⑧（純處理，不碰 DB）。抽出來讓管線的每一步都能被單獨閱讀/測試。
func processUpload(file io.Reader) (out []byte, outMime string, err error) {
	data, e := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if e != nil {
		return nil, "", errImageBroken
	}
	if len(data) > maxUploadBytes {
		return nil, "", errImageTooLarge
	}
	if err := validateImageBytes(data); err != nil {
		return nil, "", err
	}

	sniff := sniffMime(data)

	decodeSem <- struct{}{}
	defer func() { <-decodeSem }()

	o, om, e := dorimage.ReencodeStrict(data, sniff)
	if e != nil {
		return nil, "", errImageBroken
	}
	return o, om, nil
}

// sniffMime 只回本套件白名單內的兩種 MIME（其餘回空字串）。
// 一律用 http.DetectContentType 嗅探，**不看** multipart part header 宣告的型別——
// 那是完全由客戶端控制的字串，拿它當安全判定等於沒判。
func sniffMime(data []byte) string {
	switch http.DetectContentType(data) {
	case "image/jpeg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	}
	return ""
}

// validateImageBytes 步驟 ⑤⑥：白名單 + 嗅探/解碼格式交叉比對 + 尺寸上限。純函式，可單元測試。
func validateImageBytes(data []byte) error {
	sniff := sniffMime(data)
	if sniff == "" {
		return errImageFormat
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errImageFormat
	}
	// 防 magic-byte polyglot：嗅探說 PNG、解碼器卻認出 JPEG（或反之）代表這個檔案在兩套
	// 判讀規則下是不同東西，正是 polyglot 攻擊的形狀，直接拒收。
	if (format == "png") != (sniff == "image/png") || (format == "jpeg") != (sniff == "image/jpeg") {
		return errImageFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return errImageFormat
	}
	// 防 decompression bomb：檔案只有幾 KB，解開後卻是 30000×30000（≈3.6GB RGBA）。
	if cfg.Width > maxImageDim || cfg.Height > maxImageDim ||
		int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
		return errImageDims
	}
	return nil
}
