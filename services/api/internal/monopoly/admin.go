package monopoly

// admin.go 環台大富翁後台 API（C1）：獎勵池編輯／兌換碼批次／知識卡管理／抽卡設定。
// 掛載於 main.go 的 r.With(perm("monopoly")).Mount("/admin/monopoly", monopolyHandler.AdminRouter())，
// 因此本檔所有 handler 皆已由外層中介層保證 RequireAuth + RequireAdmin + RequirePerm("monopoly")。

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

var adminUUIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool {
	return adminUUIDRE.MatchString(s)
}

// AdminRouter 掛 /admin/monopoly（權限見本檔開頭註解）。
func (h *Handler) AdminRouter() chi.Router {
	r := chi.NewRouter()

	r.Get("/pool", h.AdminListPool)
	r.Post("/pool", h.AdminCreatePoolEntry)
	r.Patch("/pool/{id}", h.AdminUpdatePoolEntry)
	r.Delete("/pool/{id}", h.AdminDeletePoolEntry)

	r.Get("/redeem/batches", h.AdminListRedemptionBatches)
	r.Post("/redeem/batch", h.AdminCreateRedemptionBatch)
	r.Get("/redeem/batch/{key}", h.AdminGetRedemptionBatch)

	r.Get("/cards", h.AdminListCards)
	r.Patch("/cards/{id}", h.AdminUpdateCard)

	r.Get("/stickers", h.AdminListStickers)
	r.Patch("/stickers/{id}", h.AdminUpdateSticker)
	r.Put("/figure-settings", h.AdminPutFigureSettings)

	r.Get("/redemptions", h.AdminListFigureRedemptions)
	r.Patch("/redemptions/{id}", h.AdminUpdateFigureRedemption)

	r.Get("/settings", h.AdminGetSettings)
	r.Put("/settings", h.AdminPutSettings)

	return r
}

// --- 獎勵池 pool editor ---

var validMonopolyPools = map[string]bool{"chance": true, "destiny": true}
var validMonopolyRewardTypes = map[string]bool{
	"gp": true, "dp": true, "vip_days": true,
	"knowledge_card": true, "sticker": true, "redemption_code": true,
}

// validatePoolEntryInput 驗證 + 正規化（見任務規格）：
//   - pool 限 chance/destiny；reward_type 限六種；weight 不可為負。
//   - gp/dp/vip_days 的 amount 必須 >0。
//   - redemption_code 的 redemption_batch_key 不可空。
//   - knowledge_card/sticker 不使用 amount/batch_key，直接強制清空（即使呼叫端誤帶值也不留底）。
func validatePoolEntryInput(in *PoolEntryInput) error {
	if !validMonopolyPools[in.Pool] {
		return errors.New("pool must be chance or destiny")
	}
	if !validMonopolyRewardTypes[in.RewardType] {
		return errors.New("invalid reward_type")
	}
	if in.Weight < 0 {
		return errors.New("weight must be >= 0")
	}
	switch in.RewardType {
	case "gp", "dp", "vip_days":
		if in.Amount <= 0 {
			return errors.New("amount must be > 0 for gp/dp/vip_days")
		}
	case "redemption_code":
		in.RedemptionBatchKey = strings.TrimSpace(in.RedemptionBatchKey)
		if in.RedemptionBatchKey == "" {
			return errors.New("redemption_batch_key is required for redemption_code")
		}
	case "knowledge_card", "sticker":
		in.Amount = 0
		in.RedemptionBatchKey = ""
	}
	return nil
}

func (h *Handler) AdminListPool(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.repo.AdminListPoolEntries(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list pool entries")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handler) AdminCreatePoolEntry(w http.ResponseWriter, r *http.Request) {
	var in PoolEntryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validatePoolEntryInput(&in); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := h.svc.repo.AdminCreatePoolEntry(r.Context(), in)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create pool entry")
		return
	}
	respondJSON(w, http.StatusOK, entry)
}

func (h *Handler) AdminUpdatePoolEntry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusNotFound, "pool entry not found")
		return
	}
	var in PoolEntryInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validatePoolEntryInput(&in); err != nil {
		respondErr(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := h.svc.repo.AdminUpdatePoolEntry(r.Context(), id, in)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "pool entry not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update pool entry")
		return
	}
	respondJSON(w, http.StatusOK, entry)
}

func (h *Handler) AdminDeletePoolEntry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusNotFound, "pool entry not found")
		return
	}
	if err := h.svc.repo.AdminDeletePoolEntry(r.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			respondErr(w, http.StatusNotFound, "pool entry not found")
			return
		}
		respondErr(w, http.StatusInternalServerError, "failed to delete pool entry")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- 兌換碼批次 ---

// parseCodesText 多行文字 → 去空白/去空行/去重後的碼清單（保留原順序，供 codes_text 貼上批次匯入）。
func parseCodesText(text string) []string {
	lines := strings.Split(text, "\n")
	seen := map[string]bool{}
	out := []string{}
	for _, ln := range lines {
		code := strings.TrimSpace(ln)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out
}

func (h *Handler) AdminListRedemptionBatches(w http.ResponseWriter, r *http.Request) {
	batches, err := h.svc.repo.AdminListRedemptionBatches(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list redemption batches")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"batches": batches})
}

func (h *Handler) AdminCreateRedemptionBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BatchKey  string `json:"batch_key"`
		Kind      string `json:"kind"`
		Label     string `json:"label"`
		CodesText string `json:"codes_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	batchKey := strings.TrimSpace(req.BatchKey)
	if batchKey == "" {
		respondErr(w, http.StatusBadRequest, "batch_key is required")
		return
	}
	if req.Kind != "line_point" && req.Kind != "coupon" {
		respondErr(w, http.StatusBadRequest, "kind must be line_point or coupon")
		return
	}
	codes := parseCodesText(req.CodesText)
	if len(codes) == 0 {
		respondErr(w, http.StatusBadRequest, "codes_text must contain at least one code")
		return
	}
	inserted, skipped, err := h.svc.repo.AdminCreateRedemptionBatch(r.Context(), batchKey, req.Kind, req.Label, codes)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create redemption batch")
		return
	}
	respondJSON(w, http.StatusOK, map[string]int{"inserted": inserted, "skipped": skipped})
}

func (h *Handler) AdminGetRedemptionBatch(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	if strings.TrimSpace(key) == "" {
		respondErr(w, http.StatusBadRequest, "batch key is required")
		return
	}
	codes, err := h.svc.repo.AdminListRedemptionCodes(r.Context(), key)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list redemption codes")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

// --- 知識卡管理（主要＝補圖） ---

func (h *Handler) AdminListCards(w http.ResponseWriter, r *http.Request) {
	theme := r.URL.Query().Get("theme")
	if theme != "" && theme != "training" && theme != "care" {
		respondErr(w, http.StatusBadRequest, "theme must be training or care")
		return
	}
	cards, err := h.svc.repo.AdminListKnowledgeCards(r.Context(), theme)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list knowledge cards")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"cards": cards})
}

// AdminUpdateCard PATCH 部分更新：image_url/rarity/is_active 為主要用途（補圖），title/body 允許修正、
// 皆選填。未帶的欄位（nil）在 repo 層用 COALESCE 保留原值。
func (h *Handler) AdminUpdateCard(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusNotFound, "knowledge card not found")
		return
	}
	var req struct {
		ImageURL *string `json:"image_url"`
		Rarity   *string `json:"rarity"`
		IsActive *bool   `json:"is_active"`
		Title    *string `json:"title"`
		Body     *string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Rarity != nil && *req.Rarity != "common" && *req.Rarity != "rare" {
		respondErr(w, http.StatusBadRequest, "rarity must be common or rare")
		return
	}
	card, err := h.svc.repo.AdminUpdateKnowledgeCard(r.Context(), id, req.ImageURL, req.Rarity, req.Title, req.Body, req.IsActive)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "knowledge card not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update knowledge card")
		return
	}
	respondJSON(w, http.StatusOK, card)
}

// --- 完賽公仔貼紙管理（主要＝補圖／換彩圖，Phase 2b C） ---

// AdminListStickers GET /admin/monopoly/stickers：9 片灰階拼圖（set_key='finisher'，依 position 排序）
// ＋彩圖 URL／標題（來自 app_settings，見 AdminGetFigureSettings）。
func (h *Handler) AdminListStickers(w http.ResponseWriter, r *http.Request) {
	pieces, err := h.svc.repo.AdminListStickerPieces(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list sticker pieces")
		return
	}
	figureColorURL, figureTitle, lineOA, landingURL := h.svc.repo.AdminGetFigureSettings(r.Context())
	respondJSON(w, http.StatusOK, map[string]any{
		"figure_color_url": figureColorURL,
		"figure_title":     figureTitle,
		"line_oa":          lineOA,
		"landing_url":      landingURL,
		"pieces":           pieces,
	})
}

// AdminUpdateSticker PATCH 部分更新：image_url/title/rarity/is_active 皆選填，未帶的欄位（nil）在
// repo 層用 COALESCE 保留原值（比照 AdminUpdateCard）。
func (h *Handler) AdminUpdateSticker(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusNotFound, "sticker piece not found")
		return
	}
	var req struct {
		ImageURL *string `json:"image_url"`
		Title    *string `json:"title"`
		Rarity   *string `json:"rarity"`
		IsActive *bool   `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Rarity != nil && *req.Rarity != "common" && *req.Rarity != "rare" {
		respondErr(w, http.StatusBadRequest, "rarity must be common or rare")
		return
	}
	piece, err := h.svc.repo.AdminUpdateStickerPiece(r.Context(), id, req.ImageURL, req.Title, req.Rarity, req.IsActive)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "sticker piece not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update sticker piece")
		return
	}
	respondJSON(w, http.StatusOK, piece)
}

// AdminPutFigureSettings PUT /admin/monopoly/figure-settings：完整覆寫彩圖 URL＋標題（兩者皆必填，
// 比照 AdminPutSettings 的全量寫入風格，不是欄位級部分更新）。line_oa/landing_url 為選填指標欄位
// （比照 AdminUpdateSticker 的 COALESCE 部分更新風格）：未帶（nil）＝維持原設定不動，方便舊前端呼叫
// 不受影響；帶了（含空字串）＝直接覆寫，允許管理者刻意清空。
func (h *Handler) AdminPutFigureSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FigureColorURL string  `json:"figure_color_url"`
		FigureTitle    string  `json:"figure_title"`
		LineOA         *string `json:"line_oa"`
		LandingURL     *string `json:"landing_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.FigureColorURL = strings.TrimSpace(req.FigureColorURL)
	req.FigureTitle = strings.TrimSpace(req.FigureTitle)
	if req.FigureColorURL == "" {
		respondErr(w, http.StatusBadRequest, "figure_color_url is required")
		return
	}
	if req.FigureTitle == "" {
		respondErr(w, http.StatusBadRequest, "figure_title is required")
		return
	}
	if err := h.svc.repo.AdminSetSetting(r.Context(), "monopoly_figure_color_url", req.FigureColorURL); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	if err := h.svc.repo.AdminSetSetting(r.Context(), "monopoly_figure_title", req.FigureTitle); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	if req.LineOA != nil {
		if err := h.svc.repo.AdminSetSetting(r.Context(), "monopoly_figure_line_oa", strings.TrimSpace(*req.LineOA)); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
	}
	if req.LandingURL != nil {
		if err := h.svc.repo.AdminSetSetting(r.Context(), "monopoly_figure_landing_url", strings.TrimSpace(*req.LandingURL)); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to save settings")
			return
		}
	}
	figureColorURL, figureTitle, lineOA, landingURL := h.svc.repo.AdminGetFigureSettings(r.Context())
	respondJSON(w, http.StatusOK, map[string]any{
		"figure_color_url": figureColorURL,
		"figure_title":     figureTitle,
		"line_oa":          lineOA,
		"landing_url":      landingURL,
	})
}

// --- 完賽公仔兌換管理（A 後端） ---

var validFigureRedemptionStatuses = map[string]bool{"pending": true, "fulfilled": true, "rejected": true}

// AdminListFigureRedemptions GET /admin/monopoly/redemptions：全部兌換申請，依 created_at DESC。
func (h *Handler) AdminListFigureRedemptions(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.repo.AdminListFigureRedemptions(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list figure redemptions")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"redemptions": list})
}

// AdminUpdateFigureRedemption PATCH /admin/monopoly/redemptions/{id}：更新狀態＋備註，status 限
// pending/fulfilled/rejected；找不到該筆回 404。
func (h *Handler) AdminUpdateFigureRedemption(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !isValidUUID(id) {
		respondErr(w, http.StatusNotFound, "redemption not found")
		return
	}
	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !validFigureRedemptionStatuses[req.Status] {
		respondErr(w, http.StatusBadRequest, "status must be pending, fulfilled, or rejected")
		return
	}
	item, err := h.svc.repo.AdminUpdateFigureRedemption(r.Context(), id, req.Status, req.Note)
	if errors.Is(err, ErrNotFound) {
		respondErr(w, http.StatusNotFound, "redemption not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update figure redemption")
		return
	}
	respondJSON(w, http.StatusOK, item)
}

// --- 抽卡設定 ---

func (h *Handler) AdminGetSettings(w http.ResponseWriter, r *http.Request) {
	dupGP, fallbackGP := h.svc.repo.AdminGetSettings(r.Context())
	respondJSON(w, http.StatusOK, map[string]any{"dup_gp": dupGP, "redeem_fallback_gp": fallbackGP})
}

func (h *Handler) AdminPutSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DupGP            map[string]int `json:"dup_gp"`
		RedeemFallbackGP int            `json:"redeem_fallback_gp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.DupGP) == 0 {
		respondErr(w, http.StatusBadRequest, "dup_gp is required")
		return
	}
	for _, gp := range req.DupGP {
		if gp < 0 {
			respondErr(w, http.StatusBadRequest, "dup_gp values must be >= 0")
			return
		}
	}
	if req.RedeemFallbackGP < 0 {
		respondErr(w, http.StatusBadRequest, "redeem_fallback_gp must be >= 0")
		return
	}
	dupGPBytes, err := json.Marshal(req.DupGP)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to encode dup_gp")
		return
	}
	if err := h.svc.repo.AdminSetSetting(r.Context(), "monopoly_dup_gp", string(dupGPBytes)); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	if err := h.svc.repo.AdminSetSetting(r.Context(), "monopoly_redeem_fallback_gp", strconv.Itoa(req.RedeemFallbackGP)); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	dupGP, fallbackGP := h.svc.repo.AdminGetSettings(r.Context())
	respondJSON(w, http.StatusOK, map[string]any{"dup_gp": dupGP, "redeem_fallback_gp": fallbackGP})
}
