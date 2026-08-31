package runmeet

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dor/api/internal/appsettings"
)

// GET /run-meets/{id}/share — 社群分享卡最小資訊端點（供前端 /m/[id] SSR OG 卡用）。
//
// ⚠️ 這支端點連身分都沒有：社群爬蟲（Facebook/LINE/Twitter 等）不會帶登入態，因此**不能**
// 掛在 Router()（該路由第一行是 h.requireEntry，非登入者會直接被擋）。掛法見 cmd/api/main.go
// 的「公開端點」區塊（比照 /app-settings/public、/run-cheers 那類免登入 GET），路徑字面上
// 仍是 /run-meets/{id}/share——與 Router() 掛的 /run-meets/* 是 chi 兩個不同的路由註冊，
// 靜態／具體路徑優先於 Mount 的萬用比對，不會衝突。
//
// ⚠️ 隱私是這支端點唯一的重點，欄位表到 ShareView 為止，絕不可加 lat/lng/meeting_detail/
// description/成員名單/發起人 email 等任何成員層或個資欄位——這裡沒有任何後續閘門能補救
// 多回的欄位（其他端點還能靠登入/入口/成員身分擋一層，這支端點前面什麼都沒有）。
// 私密團之下 region/place_label/cover_url 一律遮蔽，見 buildShareView。
//
// ⚠️「不可用」原則上一律回 HTTP 200 + {"available":false}，不分原因、不用 404：不存在／
// 後台已下架／發起人已隱藏／status 非 open 全部收斂進同一句——如果每種原因回不同的狀態碼或
// 錯誤，等於開放外部用這支免登入端點對任意 UUID 做「這個團練存不存在／被下架了嗎」的批次探測。
//
// ⚠️ 唯一的例外是「已軟刪」：回 {"available":false,"deleted":true}。理由：連結是發起人主動
// 分享出去的，收到連結的人本來就知道它存在過，多回一個「已刪除」不構成新洩漏（前端可以用它
// 顯示導頁提示）；但「查無此團」不得回 deleted，一律走前面那句籠統的 unavailable，避免被拿來
// 探測任意 UUID 是否曾經存在過（見 Repository.GetMeetShare 的 errNotFound / errDeleted 分流）。
func (h *Handler) Share(w http.ResponseWriter, r *http.Request) {
	// 爬蟲會反覆重新抓同一張卡（發文編輯、平台快取失效重驗證等），5 分鐘內回應可安全重用。
	w.Header().Set("Cache-Control", "public, max-age=300")
	unavailable := func() { respondJSON(w, http.StatusOK, map[string]bool{"available": false}) }
	deleted := func() { respondJSON(w, http.StatusOK, map[string]bool{"available": false, "deleted": true}) }

	id, ok := meetIDParam(r)
	if !ok {
		unavailable()
		return
	}

	// 全站入口總開關：功能預設 hidden（尚未對外開放）時，分享卡也一併不可用——沒開放的功能
	// 不該先被分享連結看到。「locked」（可見但按鈕鎖住的軟上線）與「whitelist」刻意不擋：
	// 這兩態代表功能已經在對外揭露自己的存在，分享預覽卡不應該比一般訪客能看到的還少。
	if appsettings.GetString(r.Context(), h.db, EntryStateKey, "hidden") == "hidden" {
		unavailable()
		return
	}

	m, err := h.repo.GetMeetShare(r.Context(), id)
	if errors.Is(err, errDeleted) {
		deleted()
		return
	}
	if err != nil {
		unavailable()
		return
	}
	respondJSON(w, http.StatusOK, buildShareView(m))
}

// --- 資料層 ---

// shareRow GetMeetShare 的最小查詢結果。
//
// ⚠️ 刻意不重用 meetRow：meetRow 帶 lat/lng/meeting_detail/description/owner 等成員層與
// 詳情欄位，若共用一個型別，日後有人幫 meetRow 加欄位、忘了檢查這條路徑，就會直接經
// buildShareView 外洩到匿名爬蟲手上。這裡的欄位表就是分享卡允許離開資料庫的全部。
type shareRow struct {
	Title       string
	MeetAt      time.Time
	Region      string
	PlaceLabel  string
	NoLocation  bool
	ImageURLs   []string
	IsPrivate   bool
	MemberCount int
	Capacity    int
}

// errDeleted 分享卡／詳情端點共用的哨兵錯誤：這個團練「曾經存在、現在已被發起人軟刪」。
// 是唯一允許從「查無/不可用」的統一回應裡多揭露一點資訊的原因（見本檔案首與 GetMeetShare
// 的長註解）。刻意獨立於 errNotFound（*apiErr，會被 respondAPIErr 轉成 404）之外：
// Handler.Share／Handler.Detail 都要在轉成通用「不可用」之前，先攔截這個特殊分支。
var errDeleted = errors.New("meet deleted")

// GetMeetShare 分享卡專用查詢。
//
// ⚠️ 三種結果：(a) 查無此列 → errNotFound；(b) 已軟刪 → errDeleted（呼叫端據此多回一個
// deleted:true，見本檔案首的理由）；(c) 其餘不可用（後台下架／發起人隱藏／status 非 open）
// → errNotFound，與「查無」共用同一個錯誤、統一轉成籠統的 {"available":false}，不外洩差異。
//
// ⚠️ SELECT 清單就是防線本身——不是先撈全欄位再靠程式碼過濾，而是壓根不把 lat/lng/
// meeting_detail/description/join_password_hash/owner_id 等欄位帶出資料庫。
func (r *Repository) GetMeetShare(ctx context.Context, id string) (shareRow, error) {
	var m shareRow
	var deletedAt *time.Time
	var hiddenAdmin, hiddenOwner bool
	var status string
	err := r.db.QueryRow(ctx, `
		SELECT title, meet_at, region, place_label, no_location, image_urls,
		       (join_password_hash IS NOT NULL) AS is_private, member_count, capacity,
		       deleted_at, hidden_by_admin, hidden_by_owner, status
		  FROM run_meets WHERE id = $1`,
		id).Scan(&m.Title, &m.MeetAt, &m.Region, &m.PlaceLabel, &m.NoLocation, &m.ImageURLs,
		&m.IsPrivate, &m.MemberCount, &m.Capacity,
		&deletedAt, &hiddenAdmin, &hiddenOwner, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errNotFound
	}
	if err != nil {
		return m, err
	}
	return m, shareAvailability(deletedAt != nil, hiddenAdmin, hiddenOwner, status)
}

// shareAvailability 純函式版的「這個團練分享卡能不能用」判定（GetMeetShare 是它與 DB 之間
// 唯一的轉譯層，方便脫離 DB 單元測試）。
//
//	nil        → 可用
//	errDeleted → 已軟刪（唯一可以額外揭露成 deleted:true 的原因，見本檔案首）
//	errNotFound → 其餘原因（後台下架／發起人隱藏／status 非 open），統一收斂、不外洩差異
func shareAvailability(deleted, hiddenAdmin, hiddenOwner bool, status string) error {
	if deleted {
		return errDeleted
	}
	if hiddenAdmin || hiddenOwner || status != StatusOpen {
		return errNotFound
	}
	return nil
}

// --- 對外 DTO ---

// ShareView GET /run-meets/{id}/share 的成功回應。欄位順序即回應契約的順序。
type ShareView struct {
	Available   bool      `json:"available"`
	Title       string    `json:"title"`
	MeetAt      time.Time `json:"meet_at"`
	Region      string    `json:"region"`
	PlaceLabel  string    `json:"place_label"`
	// NoLocation 「不限地點」：即使私密團把 Region/PlaceLabel 遮蔽成空字串，這個旗標本身仍照實回傳
	// （見 buildShareView）——它不揭露任何座標或行政區資訊，只表示「這團沒有指定集合地點」，
	// 前端據此把地點欄改顯示「🌏 不限地點」，避免對已遮蔽的空字串誤判成「沒填地點」。
	NoLocation  bool      `json:"no_location"`
	CoverURL    *string   `json:"cover_url"`
	IsPrivate   bool      `json:"is_private"`
	MemberCount int       `json:"member_count"`
	Capacity    int       `json:"capacity"`
}

// buildShareView 把 shareRow 轉成分享卡（純函式，可單元測試，不碰 DB）。
//
// 私密團（join_password_hash 非 NULL）：region/place_label 回空字串、cover_url 回 null——
// 未解鎖的登入會員本來就看不到這些（見 buildCard 的私密團封面規則），一個連身分都沒有的
// 匿名分享卡沒有理由知道得比未解鎖的會員還多。no_location 例外：不論公開/私密都照實回傳，
// 它不揭露座標或行政區，只是「這團沒有指定地點」這個事實本身，且前端私密團分支根本不畫
// 地點欄（見 app/m/[id]/page.tsx），這裡多回一個布林不構成新的資訊揭露。
func buildShareView(m shareRow) ShareView {
	v := ShareView{
		Available:   true,
		Title:       m.Title,
		MeetAt:      m.MeetAt,
		Region:      m.Region,
		PlaceLabel:  m.PlaceLabel,
		NoLocation:  m.NoLocation,
		IsPrivate:   m.IsPrivate,
		MemberCount: m.MemberCount,
		Capacity:    m.Capacity,
	}
	if m.IsPrivate {
		v.Region = ""
		v.PlaceLabel = ""
		return v // CoverURL 維持 nil（私密團未解鎖不給封面圖）
	}
	if len(m.ImageURLs) > 0 {
		u := m.ImageURLs[0]
		v.CoverURL = &u
	}
	return v
}
