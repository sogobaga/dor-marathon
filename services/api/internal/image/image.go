// Package image 圖片上傳/取用：存 Postgres bytea。
package image

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxUpload = 5 << 20 // 5MB

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
