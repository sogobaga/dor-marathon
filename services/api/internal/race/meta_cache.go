// 賽事 SSR metadata 快取（2026-09-03，Neon 夜間喚醒問題「先做 A」）。
//
// 背景：前台事件廣告落地頁 apps/web/event/[slug]/page.tsx 的 generateMetadata 每個 request 都會
// SSR 呼叫 GET /races/{slug}（GetPublicDetail），但那支端點很重——查分組/加購/物資、算取消退費
// 政策、甚至可能觸發 EXP 背景結算——而 metadata 其實只用得到 title/brochure_title/subtitle/
// hero_image_url 幾個欄位。夜間爬蟲掃很多 slug 會讓這支重查詢一直打，Neon serverless compute
// 因此一直醒著（5 分鐘沒查詢才休眠）。
//
// 解法：GET /races/{slug}/meta 只回傳這幾個精簡欄位，資料來源是「一次查完所有已上線賽事」的
// map（見 Repository.ListPublicRaceMeta），包一層 ttlcache（30 分鐘 TTL，見下方 raceMetaCacheTTL
// 常數註解）。穩態下無論爬蟲掃幾個 slug，每 30 分鐘最多一次 DB 查詢；查無的 slug 也不直接穿透查
// DB（見 GetRaceMeta 下方的 raceMetaMissRefreshGrace 規則）。
package race

import (
	"context"
	"time"

	"github.com/dor/api/internal/ttlcache"
)

// raceMetaCacheTTL 賽事 meta 表快取存活期。
//
// 2026-09-04（Neon 夜間喚醒問題，owner 要求持續查清楚原因）：10 分鐘 → 30 分鐘。TTL 必須遠高於
// Neon 的 5 分鐘休眠門檻，否則快取本身就是一個「每 TTL 週期固定醒一次」的喚醒源——夜間爬蟲掃過的
// 任一 slug 只要落在快取剛好過期後，就會觸發一次背景刷新查詢喚醒 compute。30 分鐘仍遠短於賽事
// 審核/上線的節奏，管理端寫入又會主動 Invalidate，不會有「改了卻等半小時看不到」的體感問題
// （管理後台本身讀的是 AdminGetRace，走未快取的即時查詢，不受影響）——選 30 分鐘而非比照
// appsettings/profile 的 60 分鐘：SSR metadata 對「上線後多快能被爬到」的新鮮度比純外觀設定敏感，
// 折衷取一半。
const raceMetaCacheTTL = 2 * time.Hour // 後台賽事寫入即失效；TTL 只保底（分享卡片的 title/圖），拉長免得爬蟲把 TTL 變成喚醒週期

// raceMetaMissRefreshGrace 查無某 slug 時，只有在快取表「距上次成功載入已超過這個時間」才值得多
// 花一次同步查詢去確認——可能是剛核准上線的新賽事，快取還沒來得及收錄（raceMetaCacheTTL 到期前這
// 段時間背景刷新還沒被觸發）。設 60 秒（遠短於 raceMetaCacheTTL）：新賽事上線後最多 60 秒內對其
// slug 的查詢會各自多付一次查詢成本，超過這個時間才動用「強制刷新」；其餘一律直接 404，不讓爬蟲
// 對從來不存在的 slug 每次都打穿快取查 DB。
const raceMetaMissRefreshGrace = 60 * time.Second

// RaceMeta 是 GET /races/{slug}/meta 的精簡回應欄位——刻意不含分組/加購/物資/報名狀態/取消退費
// 政策等重欄位（那些走既有 GET /races/{slug}），只放 event/[slug]/page.tsx generateMetadata
// 用得到的 title/brochure_title/subtitle/hero_image_url，外加幾個常見會想標示的輔助欄位。
type RaceMeta struct {
	ID            string    `json:"id"`
	Slug          string    `json:"slug"`
	Title         string    `json:"title"`
	BrochureTitle string    `json:"brochure_title"`
	Subtitle      string    `json:"subtitle"`
	HeroImageURL  string    `json:"hero_image_url"`
	StartDate     time.Time `json:"start_date"`
	EndDate       time.Time `json:"end_date"`
	Status        string    `json:"status"`
	EventMode     string    `json:"event_mode"`
}

// newRaceMetaCache 建立賽事 meta 表快取，load 固定查 repo（NewService 呼叫一次）。獨立成建構函式
// 方便單元測試灌自訂 load（不必真的接 DB），見 meta_cache_test.go。
func newRaceMetaCache(load func(ctx context.Context) (map[string]RaceMeta, error)) *ttlcache.Cache[map[string]RaceMeta] {
	return ttlcache.New(raceMetaCacheTTL, load)
}

// needsForceRefreshOnMiss 純函式：查無某 slug 時，是否值得多花一次同步刷新去確認（見上方套件
// 註解）。valid/at 來自 Cache.Peek()；now 由呼叫端傳入方便測試（不用真的等 60 秒）。
func needsForceRefreshOnMiss(valid bool, at, now time.Time, grace time.Duration) bool {
	return !valid || now.Sub(at) >= grace
}

// GetRaceMeta 查單一 slug 的精簡 SSR metadata；ok=false 代表查無此賽事（未上線／未核准／
// closed／testing 皆視為查無，比照 GetPublicDetail 的匿名可見性判斷）。
func (s *Service) GetRaceMeta(ctx context.Context, slug string) (RaceMeta, bool) {
	m := s.raceMetaCache.Get(ctx)
	if meta, ok := m[slug]; ok {
		return meta, true
	}
	_, at, valid := s.raceMetaCache.Peek()
	if needsForceRefreshOnMiss(valid, at, time.Now(), raceMetaMissRefreshGrace) {
		m = s.raceMetaCache.Refresh(ctx)
		if meta, ok := m[slug]; ok {
			return meta, true
		}
	}
	return RaceMeta{}, false
}

// InvalidateRaceMetaCache 供賽事寫入端（admin 新增/編輯/刪除/上下架、合作方審核核准/退回）呼叫，
// 讓下一次讀取重新查出最新的已上線賽事清單。語意同 ttlcache.Cache.Invalidate：不清空舊值，只標記
// 過期，下一次讀取先吃舊值＋背景刷新，DB 查詢失敗也還有稍舊的資料可退。
func (s *Service) InvalidateRaceMetaCache() {
	s.raceMetaCache.Invalidate()
}
