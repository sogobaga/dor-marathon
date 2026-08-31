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
//  ① http.MaxBytesReader(25MB+1KB)
//  ② ParseMultipartForm → FormFile("file")（欄位名 file，沿用 api.ts 既有 FormData 慣例）
//  ③ io.LimitReader 讀滿 25MB+1，超過 → 413
//  ④ http.DetectContentType 嗅探，**完全忽略** part header 宣告的 Content-Type
//  ⑤ 白名單只收 image/jpeg、image/png
//  ⑥ image.DecodeConfig：format 必須與嗅探結果一致（防 magic-byte polyglot）；
//     尺寸上限 8000×8000 且總像素 ≤ 40M（防 decompression bomb；不隨檔案大小上限調整，見下）
//  ⑦ 取全域解碼併發信號量（cap=4），防 N 張大圖同時 Decode 撐爆容器
//  ⑧ image.ReencodeStrict：無條件重編碼 → EXIF(含 GPS)、XMP、ICC、檔尾附加 payload 全部消失
//  ⑨ 寫 images（owner_user_id=uid, purpose='runmeet'）
//  ⑩ 回 {id, url}，並設 X-Content-Type-Options: nosniff

const (
	// maxUploadBytes 是**安全網**，不是使用者面對的限制——前端會先把手機隨手拍的照片壓縮過
	// 才送出（見 apps/web 對應改動），使用者正常操作根本碰不到這個數字。25MB 只用來擋
	// 惡意送超大檔或前端壓縮失敗/被繞過的異常請求，不是「請把圖片壓到 25MB 以下」的提示值
	// （錯誤文案因此故意不叫使用者自己壓縮，見 errImageTooLarge）。
	maxUploadBytes = 25 << 20
	// maxImageDim／maxImagePixels 防的是 decompression bomb（幾 KB 的檔案解出上萬像素見方的圖），
	// 與檔案位元組大小無關，**不隨 maxUploadBytes 調整**：25MB 的手機照片解出來通常遠低於
	// 40M 像素，兩者不衝突；真的衝突時以這兩個像素上限為準，回 errImageDims 明確文案。
	maxImageDim    = 8000
	maxImagePixels = 40_000_000
)

// decodeSem 全域解碼併發信號量。
//
// ⚠️ cap=4 評估（放寬 maxUploadBytes 5MB→25MB 時重新確認過，維持不變）：這裡的解碼記憶體
// 上限是由 maxImageDim/maxImagePixels（未變動）決定，不是由上傳位元組數決定——
// image.Decode 對「檔案多小、解出來多大」沒有防禦，一張壓得很扁的 JPEG 一樣能解成滿版
// 40M 像素、約 160MB 的 RGBA/NRGBA 緩衝。也就是說 25MB 上限本身**不會**墊高單次解碼的
// 記憶體天花板，4 個併發頂多同時佔用 ~640MB，與放寬前相同。放寬後唯一多出來的風險是
// ③ io.ReadAll 在進這道信號量之前，會先把最多 25MB（原本 5MB）的原始位元組整包讀進記憶體
// ——但這段本來就不受 decodeSem 保護，真正的邊界是 route 級 20 次/小時/人的限流
// （見 handler.go Router() 的 runmeet_image）與必須登入＋過入口閘門，不是無限併發，
// 因此維持 cap=4、不需要另外調小或新增讀取階段的信號量。
var decodeSem = make(chan struct{}, 4)

var (
	errImageFormat = newErr(http.StatusBadRequest, "只接受 JPG 或 PNG 圖片，請換一張再試。")
	// errImageTooLarge 文案刻意不叫使用者「自己壓縮後再上傳」——手機隨手拍的照片本來就該讓
	// 系統處理，不是把壓縮責任丟回給人（見檔頭「使用者需求」）。25MB 只在真的異常時才會撞到。
	errImageTooLarge = newErr(http.StatusRequestEntityTooLarge, "圖片檔案過大（上限 25MB），請換一張。")
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
