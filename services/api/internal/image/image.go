// Package image 圖片上傳/取用：存 Postgres bytea。
package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	stdimage "image"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxUpload = 5 << 20 // 5MB

// MaxImageSide／MaxImagePixels 防 decompression bomb：幾 KB 的檔案宣稱是 JPEG/PNG，實際解出來
// 卻是幾萬像素見方的圖，若直接 image.Decode 會在記憶體中展開成數百 MB 甚至數 GB 的緩衝，5MB 的
// 檔案位元組上限完全擋不住（image.Decode 對「檔案多小、解出來多大」沒有防禦）。
// 匯出給 internal/runmeet 上傳鏈共用同一組數字，避免兩份定義各自漂移。
const (
	MaxImageSide   = 8000
	MaxImagePixels = 40_000_000
)

// DecodeSem 全域圖片解碼併發信號量（cap=4）：一張壓得很扁的圖一樣能解成滿版 40M 像素、約 160MB
// 的 RGBA/NRGBA 緩衝，多個上傳並發 Decode 有把容器記憶體撐爆的風險。/admin/images、
// /profile/avatar（本套件 Upload）與 internal/runmeet 上傳鏈共用同一個信號量，
// 因此「同時最多幾個 Decode 在跑」是全服務層級的上限，不是各自套件分別 cap=4 疊加。
var DecodeSem = make(chan struct{}, 4)

// --- Repository ---

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

func (r *Repository) Insert(ctx context.Context, mime string, data []byte) (string, error) {
	var id string
	err := r.db.QueryRow(ctx,
		`INSERT INTO images (mime, data, size) VALUES ($1,$2,$3) RETURNING id`,
		mime, data, len(data)).Scan(&id)
	return id, err
}

func (r *Repository) Get(ctx context.Context, id string) (string, []byte, error) {
	var mime string
	var data []byte
	err := r.db.QueryRow(ctx, `SELECT mime, data FROM images WHERE id=$1`, id).Scan(&mime, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	return mime, data, err
}

// --- Handler ---

type Handler struct{ repo *Repository }

func NewHandler(repo *Repository) *Handler { return &Handler{repo: repo} }

// AdminRouter 上傳（掛在 /api/v1/admin/images，需 admin）
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Post("/", h.Upload)
	return r
}

// PublicRouter 取圖（掛在 /api/v1/images，公開）
func (h *Handler) PublicRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/{id}", h.Serve)
	return r
}

// POST /api/v1/admin/images — multipart file
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+1024)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		respondErr(w, http.StatusBadRequest, "檔案過大或格式錯誤（上限 5MB）")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respondErr(w, http.StatusBadRequest, "缺少檔案欄位 file")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "讀取檔案失敗")
		return
	}
	if len(data) > maxUpload {
		respondErr(w, http.StatusRequestEntityTooLarge, "圖片超過 5MB")
		return
	}

	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	if !strings.HasPrefix(mime, "image/") && !strings.HasPrefix(mime, "audio/") {
		respondErr(w, http.StatusBadRequest, "僅接受圖片或音檔")
		return
	}

	// 壓縮前先用 DecodeConfig 檢查像素尺寸（只讀 header，不做全量解碼）：擋 decompression bomb
	// ——與下面 CompressImage 判斷「要不要解碼」用同一個 mime 條件，其餘格式（GIF/SVG/ICO/webp/
	// 音檔）本來就不會走到 image.Decode，不需要也不做這個檢查。DecodeConfig 失敗（壞檔/格式
	// 與宣稱不符）時不在此攔，交給下面 CompressImage 的 image.Decode 走原本「解碼失敗→原樣保留」
	// 的路徑即可，不須為同一件事重複判斷一次錯誤訊息。
	if mime == "image/jpeg" || mime == "image/png" {
		if cfg, _, err := stdimage.DecodeConfig(bytes.NewReader(data)); err == nil {
			if cfg.Width > MaxImageSide || cfg.Height > MaxImageSide ||
				int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
				respondErr(w, http.StatusBadRequest, "圖片尺寸過大")
				return
			}
		}
	}

	// 自動壓縮：只對可安全處理的 JPEG/PNG 生效，其餘（含 GIF/SVG/ICO/webp/音檔或解碼失敗）原樣保留。
	// 用 DecodeSem 限制同時解碼數（見該變數註解），避免多個大圖併發 Decode 撐爆記憶體。
	DecodeSem <- struct{}{}
	compressed, compressedMime, changed := CompressImage(data, mime)
	<-DecodeSem
	if changed {
		data, mime = compressed, compressedMime
	}

	id, err := h.repo.Insert(r.Context(), mime, data)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "儲存圖片失敗")
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{
		"id":  id,
		"url": "/api/v1/images/" + id,
	})
}

// GET /api/v1/images/{id} — 公開
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mime, data, err := h.repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	// 安全標頭（全域連帶修正，隨團練邀請同批上線）：本端點是「公開無授權」的（main.go 掛在
	// 未登入群組），而既有上傳鏈（/profile/avatar、/admin/images）放行整個 image/*，任何已登入
	// 會員都能上傳 image/svg+xml 再從**同源**取回執行 → 儲存型 XSS。三個標頭同時關掉利用面：
	//   nosniff        瀏覽器不再依內容猜型別（擋 polyglot/錯 MIME）
	//   CSP sandbox    即使被當 HTML/SVG 解析，也沒有 script/同源能力
	//   X-Frame-Options 不能被嵌進他站做點擊劫持
	// 團練邀請把這條路徑從「1 張頭像」放大成「每人每團 4 張 UGC 圖」，必須同批關掉。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Write(data)
}

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
