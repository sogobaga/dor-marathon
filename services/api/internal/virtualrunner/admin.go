package virtualrunner

import (
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// uuidRE 驗證路徑帶入的 id 是否為合法 UUID 格式，比照 internal/partner 慣例——不合法直接擋掉，
// 避免把非 UUID 字串丟給 Postgres 的 uuid 欄位比較（型別錯誤 → 500 + log 噪音）。
var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isValidUUID(s string) bool { return uuidRE.MatchString(s) }

type Handler struct {
	repo *Repository
}

func NewHandler(repo *Repository) *Handler {
	return &Handler{repo: repo}
}

// AdminRouter 掛載在 /admin/virtual-runners（需 virtual 權限，見 internal/adminacct.Scopes）。
func (h *Handler) AdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.List)
	r.Post("/", h.Create)
	r.Post("/batch", h.BatchCreate)
	r.Put("/{userID}", h.Update)
	r.Delete("/{userID}", h.Delete)
	// 靜態路徑 "presets"/"race" 在 chi/{userID} 之上，radix tree 依字面優先比對，不會被
	// {userID} 攔截（比照 internal/partner AdminRouter 對 "vip-featured-min-km" 的註解）。
	r.Put("/presets/{level}", h.UpdatePreset)
	r.Get("/race/{raceID}", h.RaceStatus)
	r.Post("/race/{raceID}/assign", h.Assign)
	r.Delete("/race/{raceID}/{userID}", h.RemoveFromRace)
	return r
}

func newRNG() *rand.Rand { return rand.New(rand.NewSource(time.Now().UnixNano())) }

// --- GET / ---

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	runners, err := h.repo.ListRunners(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list virtual runners")
		return
	}
	presets, err := h.repo.ListPresets(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list level presets")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"runners": runners, "presets": presets})
}

// --- POST / ---

type createReq struct {
	Name       string `json:"name"`
	Gender     string `json:"gender"`
	City       string `json:"city"`
	Level      string `json:"level"`
	Diligence  int    `json:"diligence"`
	WindowHour int    `json:"window_hour"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !ValidGender(req.Gender) {
		respondErr(w, http.StatusBadRequest, "gender 不合法")
		return
	}
	if !ValidCity(req.City) {
		respondErr(w, http.StatusBadRequest, "city 不合法")
		return
	}
	if !ValidLevel(req.Level) {
		respondErr(w, http.StatusBadRequest, "level 不合法")
		return
	}
	if !ValidDiligence(req.Diligence) {
		respondErr(w, http.StatusBadRequest, "diligence 需為 1-5")
		return
	}
	if !ValidWindowHour(req.WindowHour) {
		respondErr(w, http.StatusBadRequest, "window_hour 不合法")
		return
	}

	preset, err := h.repo.GetPreset(r.Context(), req.Level)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level preset")
		return
	}
	rng := newRNG()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = RandomNickname(rng)
	}
	runner, err := h.repo.CreateRunner(r.Context(), CreateRunnerInput{
		Name: name, Gender: req.Gender, City: req.City, Level: req.Level,
		Diligence: req.Diligence, WindowHour: req.WindowHour, Ability: jitterAbility(*preset, rng),
	})
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create virtual runner")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"runner": runner})
}

// --- POST /batch ---

type batchReq struct {
	Count  int    `json:"count"`
	Level  string `json:"level"`
	City   string `json:"city"`
	Gender string `json:"gender"`
}

func (h *Handler) BatchCreate(w http.ResponseWriter, r *http.Request) {
	var req batchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Count < 1 || req.Count > 200 {
		respondErr(w, http.StatusBadRequest, "count 需為 1-200")
		return
	}
	if req.Level != "" && !ValidLevel(req.Level) {
		respondErr(w, http.StatusBadRequest, "level 不合法")
		return
	}
	if req.City != "" && !ValidCity(req.City) {
		respondErr(w, http.StatusBadRequest, "city 不合法")
		return
	}
	if req.Gender != "" && !ValidGender(req.Gender) {
		respondErr(w, http.StatusBadRequest, "gender 不合法")
		return
	}

	presets, err := h.repo.ListPresets(r.Context())
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load level presets")
		return
	}
	if len(presets) == 0 {
		respondErr(w, http.StatusInternalServerError, "no level presets configured")
		return
	}
	presetByLevel := make(map[string]*LevelPreset, len(presets))
	for _, p := range presets {
		presetByLevel[p.Level] = p
	}

	rng := newRNG()
	created := 0
	// usedNicknames 同批內盡量避免綽號撞名：抽到已用過的就重抽，最多重抽 5 次；重抽 5 次仍撞
	// 也照樣接受（現實中真人跑者暱稱本來就會撞，不必為此擋住整批建立）。
	usedNicknames := make(map[string]bool, req.Count)
	for i := 0; i < req.Count; i++ {
		level := req.Level
		if level == "" {
			level = presets[rng.Intn(len(presets))].Level
		}
		city := req.City
		if city == "" {
			city = cityList[rng.Intn(len(cityList))]
		}
		gender := req.Gender
		if gender == "" {
			gender = genderList[rng.Intn(len(genderList))]
		}
		windowHour := windowHourList[rng.Intn(len(windowHourList))]
		diligence := 1 + rng.Intn(5)

		nickname := RandomNickname(rng)
		for attempt := 0; attempt < 5 && usedNicknames[nickname]; attempt++ {
			nickname = RandomNickname(rng)
		}
		usedNicknames[nickname] = true

		if _, err := h.repo.CreateRunner(r.Context(), CreateRunnerInput{
			Name: nickname, Gender: gender, City: city, Level: level,
			Diligence: diligence, WindowHour: windowHour, Ability: jitterAbility(*presetByLevel[level], rng),
		}); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to batch create virtual runners")
			return
		}
		created++
	}
	respondJSON(w, http.StatusOK, map[string]any{"created": created})
}

// --- PUT /{userID} ---

type updateReq struct {
	Gender     *string  `json:"gender"`
	City       *string  `json:"city"`
	Level      *string  `json:"level"`
	Diligence  *int     `json:"diligence"`
	WindowHour *int     `json:"window_hour"`
	AvgKm      *float64 `json:"avg_km"`
	MonthlyKm  *float64 `json:"monthly_km"`
	PaceFastS  *int     `json:"pace_fast_s"`
	PaceSlowS  *int     `json:"pace_slow_s"`
	Enabled    *bool    `json:"enabled"`
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if !isValidUUID(userID) {
		respondErr(w, http.StatusBadRequest, "userID is invalid")
		return
	}
	var req updateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Gender != nil && !ValidGender(*req.Gender) {
		respondErr(w, http.StatusBadRequest, "gender 不合法")
		return
	}
	if req.City != nil && !ValidCity(*req.City) {
		respondErr(w, http.StatusBadRequest, "city 不合法")
		return
	}
	if req.Level != nil && !ValidLevel(*req.Level) {
		respondErr(w, http.StatusBadRequest, "level 不合法")
		return
	}
	if req.Diligence != nil && !ValidDiligence(*req.Diligence) {
		respondErr(w, http.StatusBadRequest, "diligence 需為 1-5")
		return
	}
	if req.WindowHour != nil && !ValidWindowHour(*req.WindowHour) {
		respondErr(w, http.StatusBadRequest, "window_hour 不合法")
		return
	}
	// pace 若這次請求兩者都有給，須驗證 fast<slow；只給其中一個時無從驗證完整區間，交由
	// repository COALESCE 局部套用（維運端一次補齊兩個值是合理期待，不在此擋單邊更新）。
	if req.PaceFastS != nil && req.PaceSlowS != nil && !ValidPaceRange(*req.PaceFastS, *req.PaceSlowS) {
		respondErr(w, http.StatusBadRequest, "pace_fast_s 需小於 pace_slow_s")
		return
	}

	in := UpdateRunnerInput{
		Gender: req.Gender, City: req.City, Level: req.Level, Diligence: req.Diligence,
		WindowHour: req.WindowHour, AvgKm: req.AvgKm, MonthlyKm: req.MonthlyKm,
		PaceFastS: req.PaceFastS, PaceSlowS: req.PaceSlowS, Enabled: req.Enabled,
	}
	// 「level 變更時能力值重從新 preset 帶入抖動，除非同請求有明給能力值」：level 有變更
	// 且本次完全沒明給任何能力值欄位時，才用新等級的 preset + 抖動覆寫這四個欄位。
	if req.Level != nil && !in.AbilityGiven() {
		preset, err := h.repo.GetPreset(r.Context(), *req.Level)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to load level preset")
			return
		}
		ability := jitterAbility(*preset, newRNG())
		in.AvgKm, in.MonthlyKm, in.PaceFastS, in.PaceSlowS = &ability.AvgKm, &ability.MonthlyKm, &ability.PaceFastS, &ability.PaceSlowS
	}

	runner, err := h.repo.UpdateRunner(r.Context(), userID, in)
	if errors.Is(err, ErrRunnerNotFound) {
		respondErr(w, http.StatusNotFound, "virtual runner not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update virtual runner")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"runner": runner})
}

// --- DELETE /{userID} ---

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if !isValidUUID(userID) {
		respondErr(w, http.StatusBadRequest, "userID is invalid")
		return
	}
	err := h.repo.DeleteRunner(r.Context(), userID)
	if errors.Is(err, ErrHasRegistrations) {
		respondErr(w, http.StatusConflict, "has_registrations")
		return
	}
	if errors.Is(err, ErrRunnerNotFound) {
		respondErr(w, http.StatusNotFound, "virtual runner not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete virtual runner")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- PUT /presets/{level} ---

type presetReq struct {
	AvgKm     float64 `json:"avg_km"`
	MonthlyKm float64 `json:"monthly_km"`
	PaceFastS int     `json:"pace_fast_s"`
	PaceSlowS int     `json:"pace_slow_s"`
}

func (h *Handler) UpdatePreset(w http.ResponseWriter, r *http.Request) {
	level := chi.URLParam(r, "level")
	if !ValidLevel(level) {
		respondErr(w, http.StatusBadRequest, "level 不合法")
		return
	}
	var req presetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AvgKm <= 0 || req.MonthlyKm <= 0 {
		respondErr(w, http.StatusBadRequest, "avg_km / monthly_km 需大於 0")
		return
	}
	if !ValidPaceRange(req.PaceFastS, req.PaceSlowS) {
		respondErr(w, http.StatusBadRequest, "pace_fast_s 需小於 pace_slow_s")
		return
	}
	preset, err := h.repo.UpdatePreset(r.Context(), level, req.AvgKm, req.MonthlyKm, req.PaceFastS, req.PaceSlowS)
	if errors.Is(err, ErrPresetNotFound) {
		respondErr(w, http.StatusNotFound, "level preset not found")
		return
	}
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to update level preset")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"preset": preset})
}

// --- GET /race/{raceID} ---

func (h *Handler) RaceStatus(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	if !isValidUUID(raceID) {
		respondErr(w, http.StatusBadRequest, "raceID is invalid")
		return
	}
	exists, err := h.repo.RaceExists(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to check race")
		return
	}
	if !exists {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}

	assigned, err := h.repo.AssignedRunners(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list assigned virtual runners")
		return
	}
	groups, err := h.repo.RaceGroups(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to list race groups")
		return
	}
	count, err := h.repo.CandidatesCount(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to count candidates")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"assigned": assigned, "groups": groups, "candidates_count": count})
}

// --- POST /race/{raceID}/assign ---

// maxRandomAssign random_count 上限；契约未明訂此端點的上限，比照 POST /batch 的 count(1-200)
// 沿用同一個防呆邊界（避免單次請求鎖住過多 race_groups 列/長時間佔用交易）。
const maxRandomAssign = 200

type assignReq struct {
	UserIDs     []string `json:"user_ids"`
	RandomCount int      `json:"random_count"`
	GroupID     string   `json:"group_id"`
}

func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	if !isValidUUID(raceID) {
		respondErr(w, http.StatusBadRequest, "raceID is invalid")
		return
	}
	var req assignReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	hasUserIDs := len(req.UserIDs) > 0
	hasRandomCount := req.RandomCount > 0
	if hasUserIDs == hasRandomCount { // 同時給了兩者，或兩者都沒給
		respondErr(w, http.StatusBadRequest, "user_ids 與 random_count 需擇一給值")
		return
	}
	if hasRandomCount && (req.RandomCount < 1 || req.RandomCount > maxRandomAssign) {
		respondErr(w, http.StatusBadRequest, "random_count 需為 1-200")
		return
	}
	for _, uid := range req.UserIDs {
		if !isValidUUID(uid) {
			respondErr(w, http.StatusBadRequest, "user_ids 內含不合法的 UUID")
			return
		}
	}

	exists, err := h.repo.RaceExists(r.Context(), raceID)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to check race")
		return
	}
	if !exists {
		respondErr(w, http.StatusNotFound, "race not found")
		return
	}
	if req.GroupID != "" {
		if !isValidUUID(req.GroupID) {
			respondErr(w, http.StatusBadRequest, "group_id is invalid")
			return
		}
		ok, err := h.repo.GroupExists(r.Context(), raceID, req.GroupID)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to check group")
			return
		}
		if !ok {
			respondErr(w, http.StatusBadRequest, "group not found")
			return
		}
	}

	userIDs := req.UserIDs
	if hasRandomCount {
		userIDs, err = h.repo.RandomCandidateIDs(r.Context(), raceID, req.RandomCount)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to pick random candidates")
			return
		}
	}

	rng := newRNG()
	added := 0
	skipped := []AssignSkip{}
	for _, uid := range userIDs {
		reason, err := h.repo.AssignUser(r.Context(), raceID, uid, req.GroupID, rng)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to assign virtual runner")
			return
		}
		if reason == "" {
			added++
		} else {
			skipped = append(skipped, AssignSkip{UserID: uid, Reason: reason})
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
}

// --- DELETE /race/{raceID}/{userID} ---

func (h *Handler) RemoveFromRace(w http.ResponseWriter, r *http.Request) {
	raceID := chi.URLParam(r, "raceID")
	userID := chi.URLParam(r, "userID")
	if !isValidUUID(raceID) || !isValidUUID(userID) {
		respondErr(w, http.StatusBadRequest, "raceID/userID is invalid")
		return
	}
	if err := h.repo.RemoveFromRace(r.Context(), raceID, userID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to remove virtual runner from race")
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondErr(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}
