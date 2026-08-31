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
// ⚠️「不可用」一律回 HTTP 200 + {"available":false}，不分原因、不用 404：不存在／已軟刪／
// 後台已下架／status 非 open 全部收斂進 Repository.GetMeetShare 的同一個 errNotFound，
// 這裡再統一轉成同一句——如果每種原因回不同的狀態碼或錯誤，等於開放外部用這支免登入端點
// 對任意 UUID 做「這個團練存不存在／被下架了嗎」的批次探測。
func (h *Handler) Share(w http.ResponseWriter, r *http.Request) {
	// 爬蟲會反覆重新抓同一張卡（發文編輯、平台快取失效重驗證等），5 分鐘內回應可安全重用。
	w.Header().Set("Cache-Control", "public, max-age=300")
	unavailable := func() { respondJSON(w, http.StatusOK, map[string]bool{"available": false}) }

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
	ImageURLs   []string
	IsPrivate   bool
	MemberCount int
	Capacity    int
}

// GetMeetShare 分享卡專用查詢：不存在／已軟刪／後台下架／status 非 open 一律回 errNotFound
// （呼叫端 Handler.Share 不區分原因，統一轉成 {"available":false}）。
//
// ⚠️ SELECT 清單就是防線本身——不是先撈全欄位再靠程式碼過濾，而是壓根不把 lat/lng/
// meeting_detail/description/join_password_hash/owner_id 等欄位帶出資料庫。
func (r *Repository) GetMeetShare(ctx context.Context, id string) (shareRow, error) {
	var m shareRow
	err := r.db.QueryRow(ctx, `
		SELECT title, meet_at, region, place_label, image_urls,
		       (join_password_hash IS NOT NULL) AS is_private, member_count, capacity
		  FROM run_meets
		 WHERE id = $1 AND deleted_at IS NULL AND hidden_by_admin = FALSE AND status = 'open'`,
		id).Scan(&m.Title, &m.MeetAt, &m.Region, &m.PlaceLabel, &m.ImageURLs,
		&m.IsPrivate, &m.MemberCount, &m.Capacity)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errNotFound
	}
	return m, err
}

// --- 對外 DTO ---

// ShareView GET /run-meets/{id}/share 的成功回應。欄位順序即回應契約的順序。
type ShareView struct {
	Available   bool      `json:"available"`
	Title       string    `json:"title"`
	MeetAt      time.Time `json:"meet_at"`
	Region      string    `json:"region"`
	PlaceLabel  string    `json:"place_label"`
	CoverURL    *string   `json:"cover_url"`
	IsPrivate   bool      `json:"is_private"`
	MemberCount int       `json:"member_count"`
	Capacity    int       `json:"capacity"`
}

// buildShareView 把 shareRow 轉成分享卡（純函式，可單元測試，不碰 DB）。
//
// 私密團（join_password_hash 非 NULL）：region/place_label 回空字串、cover_url 回 null——
// 未解鎖的登入會員本來就看不到這些（見 buildCard 的私密團封面規則），一個連身分都沒有的
// 匿名分享卡沒有理由知道得比未解鎖的會員還多。
func buildShareView(m shareRow) ShareView {
	v := ShareView{
		Available:   true,
		Title:       m.Title,
		MeetAt:      m.MeetAt,
		Region:      m.Region,
		PlaceLabel:  m.PlaceLabel,
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
