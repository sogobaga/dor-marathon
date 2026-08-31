// 團練「開跑前提醒」：站內信 + Email，排程骨架比照 internal/ops/dailyreport.go
// （RunDailyReportLoop + maybeRunDailyReport）——每小時 tick、獨立連線 pg_try_advisory_lock。
//
// ⚠️ 與每日報告的關鍵差異：那邊冪等靠「今天跑過沒」這個全域 app_settings 標記；這裡是
// per 團練、各自 meet_at 不同，所以冪等改用 per-row 標記（run_meets.reminder_sent_at）＋
// CAS UPDATE（RowsAffected=0 代表已發過或被其他實例搶走，直接跳過——寧可漏發也不要重複打擾，
// 見 migration 163 檔頭②）。
//
// ⚠️ 合併降噪：同一使用者若同時有多場團練即將開始，站內信與 Email 各只發一封、信中列出多場。
// 這是全站第一個通知合併機制，把「使用者 × 團練」平坦清單分組成「每人一封」的邏輯抽成純函式
// （buildReminderContents），連同時間格式化、CAS 決策、Email 收件資格判定，都刻意脫離 DB 依賴，
// 方便 reminder_test.go 直接單元測試（本檔的 DB 呼叫全部是薄薄一層，複雜邏輯都在純函式裡）。
//
// ⚠️ 地點分層：Email 會離開這個系統本身（留在收件匣、可能被轉寄/被郵件服務掃描），絕對不可放
// lat/lng/meeting_detail 成員層座標——見 migration 163 檔頭與 model.go 檔頭地點三層揭露規則。
// Email 只帶 region/place_label（或「不限地點」）這組公開層資訊，並註明「詳細集合地點請點連結
// 在 App 查看」。
package runmeet

import (
	"context"
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/appsettings"
	"github.com/dor/api/internal/notify"
)

// --- 設定 key／預設值 ---

const (
	keyReminderEnabled = "runmeet_reminder_enabled" // app_settings，'1' 才跑（見 migration 163）
	keyReminderHours   = "runmeet_reminder_hours"   // 開跑前幾小時發送
	defReminderHours   = 3

	reminderTickInterval     = time.Hour
	reminderAdvisoryLockName = "runmeet:reminder" // 獨立鎖名，避免與 ops 排程互搶

	reminderMailTitle = "團練即將開始" // 站內信標題（單場/多場皆同一標題，差異在內文與 url）

	reminderEmailBatchSize   = 100 // Resend /emails/batch 單次上限
	reminderEmailBatchSleep  = 600 * time.Millisecond
	reminderEmailSendTimeout = 30 * time.Second
)

// reminderFrontendURL 組 Email 內「前往查看」與「個人資料頁」連結用的網站根網址；main.go 啟動時
// 用 SetFrontendURL 注入（比照 SetMailInserter 的 1-method/晚繫結慣例，見 admin.go 檔頭）。
// 未注入時退回正式站網址，避免忘了 wiring 時寄出的信裡連結是空字串+相對路徑組出的爛連結。
var reminderFrontendURL = "https://www.dor.tw"

// SetFrontendURL main.go 啟動時呼叫一次即可（比照 cfg.FrontendURL 既有慣例，見 emailbroadcast.NewHandler）。
func SetFrontendURL(url string) {
	if url = strings.TrimRight(strings.TrimSpace(url), "/"); url != "" {
		reminderFrontendURL = url
	}
}

// --- 排程骨架（比照 ops/dailyreport.go）---

// RunReminderLoop 背景每小時排程。
func (h *Handler) RunReminderLoop(ctx context.Context) {
	h.maybeSendReminders(ctx)
	t := time.NewTicker(reminderTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.maybeSendReminders(ctx)
		}
	}
}

// maybeSendReminders 總開關 → 獨立連線 advisory lock → 撈候選 → 逐筆 CAS 標記＋展開成員 →
// 合併分組 → 先發站內信、再發 Email（Email 失敗不得影響已發出的站內信，見檔尾 sendReminderEmails）。
func (h *Handler) maybeSendReminders(ctx context.Context) {
	if appsettings.GetInt(ctx, h.db, keyReminderEnabled, 1) != 1 {
		return
	}
	hours := appsettings.GetInt(ctx, h.db, keyReminderHours, defReminderHours)
	if hours <= 0 {
		hours = defReminderHours
	}

	conn, err := h.db.Acquire(ctx)
	if err != nil {
		log.Error().Err(err).Msg("runmeet reminder: acquire dedicated connection for advisory lock failed")
		return
	}
	defer conn.Release()

	var gotLock bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, reminderAdvisoryLockName).Scan(&gotLock); err != nil {
		log.Error().Err(err).Msg("runmeet reminder: try advisory lock failed")
		return
	}
	if !gotLock {
		log.Debug().Msg("runmeet reminder: another instance is already running this tick, skip")
		return
	}
	defer func() {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, reminderAdvisoryLockName).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Msg("runmeet reminder: advisory unlock failed (will auto-release once this connection closes)")
		}
	}()

	now := time.Now()
	candidates, err := h.fetchReminderCandidates(ctx, hours)
	if err != nil {
		log.Error().Err(err).Msg("runmeet reminder: fetch candidates failed")
		return
	}
	pairs := processReminderCandidates(ctx, candidates, now, hours, h.markReminderSent, h.joinedMemberIDs)
	if len(pairs) == 0 {
		return
	}

	contents := buildReminderContents(pairs, now)
	h.sendReminderMail(ctx, contents)   // 一律先發站內信
	h.sendReminderEmails(ctx, contents) // Email 失敗只記 log，不影響已發出的站內信
}

// --- DB 讀寫（薄薄一層，複雜邏輯都在下面的純函式）---

// reminderCandidate fetchReminderCandidates 撈出的一列（吃 migration 163 的 idx_run_meets_reminder）。
type reminderCandidate struct {
	ID          string
	Title       string
	MeetAt      time.Time
	Region      string
	PlaceLabel  string
	NoLocation  bool
	MemberCount int
	Capacity    int
}

// fetchReminderCandidates 撈「即將開跑且還沒提醒過」的團練。
//
// ⚠️ meet_at > NOW() 是必要條件（不是 >=）：重啟後補跑最容易踩到「這場其實已經開跑，只是
// reminder_sent_at 剛好還是 NULL」的情況——見檔頭與 migration 163。上界用 make_interval(hours=>$2)
// 而不是字串拼 interval（比照 repository.go 既有慣例，避免參數型別推導問題）。
func (h *Handler) fetchReminderCandidates(ctx context.Context, hours int) ([]reminderCandidate, error) {
	rows, err := h.db.Query(ctx, `
		SELECT id, title, meet_at, region, place_label, no_location, member_count, capacity
		  FROM run_meets
		 WHERE reminder_sent_at IS NULL
		   AND deleted_at IS NULL
		   AND hidden_by_admin = FALSE
		   AND status = $1
		   AND meet_at > NOW()
		   AND meet_at <= NOW() + make_interval(hours => $2)
		 ORDER BY meet_at ASC`, StatusOpen, hours)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reminderCandidate
	for rows.Next() {
		var c reminderCandidate
		if err := rows.Scan(&c.ID, &c.Title, &c.MeetAt, &c.Region, &c.PlaceLabel, &c.NoLocation, &c.MemberCount, &c.Capacity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// markReminderSent CAS 標記：RowsAffected=0 代表已發過或被其他實例搶走。
func (h *Handler) markReminderSent(ctx context.Context, meetID string) (bool, error) {
	tag, err := h.db.Exec(ctx,
		`UPDATE run_meets SET reminder_sent_at = NOW() WHERE id=$1 AND reminder_sent_at IS NULL`, meetID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// joinedMemberIDs 該團練目前 status='joined' 的成員（發起人建團時已以 role='owner', status='joined'
// 寫入，天然含發起人，見 migration 156）。
func (h *Handler) joinedMemberIDs(ctx context.Context, meetID string) ([]string, error) {
	rows, err := h.db.Query(ctx,
		`SELECT user_id FROM run_meet_members WHERE meet_id=$1 AND status=$2`, meetID, MemberJoined)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- 純邏輯（DB 操作經函式參數／回傳值注入，方便單元測試脫離真實連線）---

// reminderMeet 一場即將開始的團練的提醒用資訊。⚠️ 只含公開層欄位（region/place_label/no_location/
// member_count/capacity）——這個型別會被拿去組 Email 內文，絕不可加 lat/lng/meeting_detail。
type reminderMeet struct {
	ID          string
	Title       string
	MeetAt      time.Time
	Region      string
	PlaceLabel  string
	NoLocation  bool
	MemberCount int
	Capacity    int
}

// userMeetPair 「這位使用者、這場即將開始的團練」——processReminderCandidates 的輸出、
// buildReminderContents 的輸入單位。
type userMeetPair struct {
	UserID string
	Meet   reminderMeet
}

type reminderMarkFn func(ctx context.Context, meetID string) (marked bool, err error)
type reminderMembersFn func(ctx context.Context, meetID string) ([]string, error)

// isReminderCandidate 純函式：是否落在「還沒開跑、且在提前 hours 小時視窗內」。
//
// ⚠️ meetAt.After(now) 是硬性條件，不是 >=（等於 now 的瞬間也算「正在開跑」，不發）——
// 這是本函式存在的主要理由：SQL 端已經有同樣的 meet_at > NOW() 條件，這裡是重啟補跑時的第二道
// 防線（fetch 到候選之後、CAS 標記之前再確認一次），也讓這條規則能脫離 DB 被單元測試釘住邊界。
func isReminderCandidate(meetAt, now time.Time, hours int) bool {
	if !meetAt.After(now) {
		return false
	}
	return !meetAt.After(now.Add(time.Duration(hours) * time.Hour))
}

// processReminderCandidates 排程主邏輯（不含連線細節）：逐一處理候選團練——
//  1. isReminderCandidate 二次確認未開跑／仍在視窗內
//  2. CAS 標記（mark；標記失敗或 RowsAffected=0 就跳過，不查成員也不發送）
//  3. 標記成功才查 joined 成員，展開成 userMeetPair
//
// DB 操作經 mark/members 兩個函式參數注入（不是介面——只有兩個依賴，func value 已經夠用），
// 讓 CAS 冪等、時間邊界等行為可以完全脫離真實 DB 連線做單元測試（見 reminder_test.go）。
func processReminderCandidates(ctx context.Context, candidates []reminderCandidate, now time.Time, hours int,
	mark reminderMarkFn, members reminderMembersFn) []userMeetPair {
	var pairs []userMeetPair
	for _, c := range candidates {
		if !isReminderCandidate(c.MeetAt, now, hours) {
			continue
		}
		marked, err := mark(ctx, c.ID)
		if err != nil {
			log.Error().Err(err).Str("meet_id", c.ID).Msg("runmeet reminder: mark reminder_sent_at failed")
			continue
		}
		if !marked {
			continue // 已發過或被其他實例搶走
		}
		ids, err := members(ctx, c.ID)
		if err != nil {
			log.Error().Err(err).Str("meet_id", c.ID).Msg("runmeet reminder: fetch joined members failed")
			continue
		}
		rm := reminderMeet{
			ID: c.ID, Title: c.Title, MeetAt: c.MeetAt, Region: c.Region, PlaceLabel: c.PlaceLabel,
			NoLocation: c.NoLocation, MemberCount: c.MemberCount, Capacity: c.Capacity,
		}
		for _, uid := range ids {
			pairs = append(pairs, userMeetPair{UserID: uid, Meet: rm})
		}
	}
	return pairs
}

// reminderContent 合併後、每個使用者一份的通知內容（站內信文案在這裡先組好；Email 內容另外在
// sendReminderEmails 組，因為 Email 還要套用「只放公開層資訊」的邊界與收件資格判定）。
type reminderContent struct {
	UserID    string
	Meets     []reminderMeet // 依 meet_at 升冪排序
	MailTitle string
	MailBody  string
	MailURL   string // 單場 /?runmeet={id}；多場 /?runmeet=list
}

// buildReminderContents 純函式：把「使用者 × 團練」平坦清單依 user 分組（保留輸入中第一次出現的
// 順序，讓測試結果可預期），組出每人一份的站內信內容。這是全站第一個通知合併機制：同一人若同時
// 有多場團練即將開始，只產生一份 reminderContent（一封站內信、一封 Email），信中列出全部場次。
func buildReminderContents(pairs []userMeetPair, now time.Time) []reminderContent {
	order := make([]string, 0, len(pairs))
	byUser := map[string][]reminderMeet{}
	for _, p := range pairs {
		if _, seen := byUser[p.UserID]; !seen {
			order = append(order, p.UserID)
		}
		byUser[p.UserID] = append(byUser[p.UserID], p.Meet)
	}
	contents := make([]reminderContent, 0, len(order))
	for _, uid := range order {
		meets := byUser[uid]
		sort.Slice(meets, func(i, j int) bool { return meets[i].MeetAt.Before(meets[j].MeetAt) })
		contents = append(contents, reminderContent{
			UserID:    uid,
			Meets:     meets,
			MailTitle: reminderMailTitle,
			MailBody:  buildReminderMailBody(meets, now),
			MailURL:   buildReminderMailURL(meets),
		})
	}
	return contents
}

func buildReminderMailURL(meets []reminderMeet) string {
	if len(meets) == 1 {
		return "/?runmeet=" + meets[0].ID
	}
	return "/?runmeet=list"
}

// buildReminderMailBody 站內信內文：單場只列名稱與時間；多場先講「你有 N 場」，逐場列名稱與時間。
func buildReminderMailBody(meets []reminderMeet, now time.Time) string {
	if len(meets) == 1 {
		m := meets[0]
		return fmt.Sprintf("《%s》%s 開始，別忘了準時出發！", m.Title, fmtMeetAt(m.MeetAt, now))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "你有 %d 場團練即將開始：\n", len(meets))
	for _, m := range meets {
		fmt.Fprintf(&b, "《%s》%s\n", m.Title, fmtMeetAt(m.MeetAt, now))
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- 台北時間格式化（Go 版 fmtMeetAt，與 apps/web/src/lib/runMeet.ts 的六時段規則保持一致）---

// taipeiWeekdayChars time.Weekday（Sunday=0）→ 中文星期單字元。
var taipeiWeekdayChars = [7]string{"日", "一", "二", "三", "四", "五", "六"}

// dayPeriods 時段用語（左閉右開，與前端 DAY_PERIODS 逐項對應，見 lib/runMeet.ts 檔頭長註解）：
//
//	[0,4) 凌晨　[4,6) 清晨　[6,12) 上午　[12,17) 下午　[17,19) 傍晚　[19,24) 晚上
var dayPeriods = []struct {
	from  int
	label string
}{
	{19, "晚上"}, {17, "傍晚"}, {12, "下午"}, {6, "上午"}, {4, "清晨"}, {0, "凌晨"},
}

// taipeiTime 全站慣例：distroless 無 tzdata，一律手算 UTC+8（不用 time.LoadLocation），
// 見 internal/ops/selfcheck.go taiwanNow。
func taipeiTime(t time.Time) time.Time { return t.UTC().Add(8 * time.Hour) }

// fmtHour12 24 小時制 hh/mm → 「時段 h:mm」。
//
// ⚠️ 兩個最容易寫錯的邊界（與前端 fmtHour12 逐字對應，見 lib/runMeet.ts 檔頭）：
// 0 點是「凌晨 0:xx」不是「上午 12:xx」（中文口語會把後者讀成中午）；12 點是「下午 12:xx」
// 不是「下午 0:xx」。換算是「>12 才減 12」，不是 hh%12。
func fmtHour12(hh, mm int) string {
	label := "凌晨"
	for _, p := range dayPeriods {
		if hh >= p.from {
			label = p.label
			break
		}
	}
	h12 := hh
	if hh > 12 {
		h12 = hh - 12
	}
	return fmt.Sprintf("%s %d:%02d", label, h12, mm)
}

// fmtMeetAt 「8/31（日）晚上 9:00」；與 now 同一台北日則為「今天 晚上 9:00」。
func fmtMeetAt(meetAt, now time.Time) string {
	t, n := taipeiTime(meetAt), taipeiTime(now)
	timeStr := fmtHour12(t.Hour(), t.Minute())
	if t.Year() == n.Year() && t.Month() == n.Month() && t.Day() == n.Day() {
		return "今天 " + timeStr
	}
	return fmt.Sprintf("%d/%d（%s）%s", int(t.Month()), t.Day(), taipeiWeekdayChars[int(t.Weekday())], timeStr)
}

// reminderLocationText Email／站內信共用的地點文字（僅公開層）：不限地點時固定文案，
// 否則 region・place_label（與前台列表卡片一致，見 RunMeetScreen 的 NoLocation 顯示規則）。
func reminderLocationText(m reminderMeet) string {
	if m.NoLocation {
		return "🌏 不限地點"
	}
	return m.Region + "・" + m.PlaceLabel
}

// --- 站內信寄送 ---

// sendReminderMail 逐人寫一封站內信（不是逐團練——這正是合併降噪的效果：一人多場只有一列
// user_mail）。直接用套件層 mailer 變數（admin.go 宣告、main.go 於 NewHandler 後 SetMailInserter
// 注入），不經 admin.go 的 sendMail() helper：那支 helper 把 url 寫死成空字串，這裡需要依單場/
// 多場帶不同 url，見 buildReminderMailURL。
func (h *Handler) sendReminderMail(ctx context.Context, contents []reminderContent) {
	if mailer == nil {
		return
	}
	for _, c := range contents {
		if _, err := mailer.InsertForUsers(ctx, []string{c.UserID}, "normal", c.MailTitle, c.MailBody, c.MailURL); err != nil {
			log.Warn().Err(err).Str("user_id", c.UserID).Msg("runmeet reminder: site mail insert failed")
		}
	}
}

// --- Email 寄送 ---

// emailRecipient 一位收件人的 Email 資格判定所需欄位（DB 查詢結果的最小切片，供 eligibleForReminderEmail
// 脫離 DB 單元測試）。
type emailRecipient struct {
	UserID string
	Email  string
	OptIn  bool // users.runmeet_reminder_email（migration 163；預設 TRUE）
	Unsub  bool // 是否在 email_unsubscribes（migration 141；行銷退訂，但使用者明確表達過不想收信仍要尊重）
}

// eligibleForReminderEmail 純函式：兩個條件都要通過（使用者需求「兩個都要」）——
// 偏好開啟 且 不在退訂表；email 為空也跳過（沒地址寄不出）。
func eligibleForReminderEmail(r emailRecipient) bool {
	return r.OptIn && !r.Unsub && strings.TrimSpace(r.Email) != ""
}

// fetchEmailRecipients 查詢一批使用者的 Email 資格欄位。
func (h *Handler) fetchEmailRecipients(ctx context.Context, userIDs []string) ([]emailRecipient, error) {
	rows, err := h.db.Query(ctx, `
		SELECT id::text, COALESCE(email,''), runmeet_reminder_email,
		       (id IN (SELECT user_id FROM email_unsubscribes))
		  FROM users WHERE id = ANY($1::uuid[])`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []emailRecipient
	for rows.Next() {
		var r emailRecipient
		if err := rows.Scan(&r.UserID, &r.Email, &r.OptIn, &r.Unsub); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// buildReminderEmailSubject 單場帶團練名稱；多場總數。
func buildReminderEmailSubject(meets []reminderMeet) string {
	if len(meets) == 1 {
		return "「" + meets[0].Title + "」即將開始"
	}
	return fmt.Sprintf("你有 %d 場團練即將開始", len(meets))
}

// buildReminderEmailBodyHTML 組 Email 內文（僅公開層資訊：名稱／時間／region・place_label 或
// 「不限地點」／人數）。⚠️ 絕不可放 lat/lng/meeting_detail——見檔頭與 model.go 地點三層揭露規則；
// 明確寫一句「詳細集合地點請點連結在 App 查看」，不讓收件人誤以為信裡就是完整地點。
func buildReminderEmailBodyHTML(meets []reminderMeet, now time.Time) string {
	var b strings.Builder
	if len(meets) == 1 {
		b.WriteString("你加入的團練即將開始：<br/><br/>")
	} else {
		fmt.Fprintf(&b, "你有 %d 場團練即將開始：<br/><br/>", len(meets))
	}
	for _, m := range meets {
		url := reminderFrontendURL + "/?runmeet=" + m.ID
		fmt.Fprintf(&b, `<div style="margin-bottom:14px;padding:12px 14px;background:#f7f7f7;border-radius:8px;">`+
			`<div style="font-weight:700;">%s</div>`+
			`<div style="margin-top:4px;">🕒 %s</div>`+
			`<div style="margin-top:2px;">📍 %s</div>`+
			`<div style="margin-top:2px;color:#666;">👥 %d/%d 人</div>`+
			`<div style="margin-top:8px;"><a href="%s" style="color:#fc4c02;">前往查看</a></div>`+
			`</div>`,
			html.EscapeString(m.Title), html.EscapeString(fmtMeetAt(m.MeetAt, now)),
			html.EscapeString(reminderLocationText(m)), m.MemberCount, m.Capacity, html.EscapeString(url))
	}
	b.WriteString(`<div style="color:#888;">詳細集合地點請點連結在 App 查看。</div>`)
	return b.String()
}

// buildReminderEmailHTML 品牌樣式比照 emailbroadcast/template.go buildEmailHTML，但這裡不能直接
// import 那個未匯出函式（且 footer 文案刻意不同——見下）。
//
// ⚠️ footer 不沿用行銷退訂連結（email_unsubscribes 的 unsubscribeURL）：那是「電子報退訂」，
// 使用者若點了會以為自己退訂了電子報，但其實他要關的是「這場團練的提醒」，兩者是刻意分開的
// 設定（migration 163 檔頭①）。這裡改連到個人資料頁的開關（?profile=sports，PushToggle 同一頁）。
func buildReminderEmailHTML(subject, bodyHTML, prefsURL string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><body style="font-family:sans-serif;color:#222;max-width:600px;margin:0 auto;padding:24px 20px;">
<div style="color:#888;font-size:12px;letter-spacing:1px;">DOR｜城市探索</div>
<h2 style="margin:10px 0 20px;">%s</h2>
<div style="line-height:1.75;font-size:15px;">%s</div>
<hr style="margin:36px 0 16px;border:none;border-top:1px solid #eee;" />
<div style="font-size:12px;color:#999;line-height:1.9;">
DOR｜城市探索　·　service@dor.tw　·　統一編號：83005678<br />
你會收到這封信，是因為你加入了這場團練。可在 <a href="%s" style="color:#999;">DOR 個人資料頁</a> 關閉此提醒。
</div>
</body></html>`, html.EscapeString(subject), bodyHTML, html.EscapeString(prefsURL))
}

// sendReminderEmails 查資格 → 組信 → 分批（<=100）寄送，批間 sleep 節流；遇服務未設定／配額用盡
// 立即中止本輪（後續批次必然同樣結果）。⚠️ 呼叫端已先發完站內信才叫這支——這裡任何錯誤都只記
// log，不 return 錯誤給呼叫端、不影響已經發出的站內信（見 maybeSendReminders 呼叫順序註解）。
func (h *Handler) sendReminderEmails(ctx context.Context, contents []reminderContent) {
	userIDs := make([]string, len(contents))
	for i, c := range contents {
		userIDs[i] = c.UserID
	}
	recipients, err := h.fetchEmailRecipients(ctx, userIDs)
	if err != nil {
		log.Error().Err(err).Msg("runmeet reminder: fetch email recipients failed")
		return
	}
	byID := make(map[string]emailRecipient, len(recipients))
	for _, r := range recipients {
		byID[r.UserID] = r
	}

	now := time.Now()
	prefsURL := reminderFrontendURL + "/?profile=sports"
	var msgs []notify.EmailMsg
	for _, c := range contents {
		rec, ok := byID[c.UserID]
		if !ok || !eligibleForReminderEmail(rec) {
			continue
		}
		subject := buildReminderEmailSubject(c.Meets)
		bodyHTML := buildReminderEmailBodyHTML(c.Meets, now)
		msgs = append(msgs, notify.EmailMsg{
			To: []string{rec.Email}, Subject: subject,
			HTML: buildReminderEmailHTML(subject, bodyHTML, prefsURL),
		})
	}
	if len(msgs) == 0 {
		return
	}

	from := notify.DefaultFrom()
	sent, failed := 0, 0
batchLoop:
	for i := 0; i < len(msgs); i += reminderEmailBatchSize {
		end := min(i+reminderEmailBatchSize, len(msgs))
		batch := msgs[i:end]

		sendCtx, cancel := context.WithTimeout(context.Background(), reminderEmailSendTimeout)
		err := notify.SendEmailBatch(sendCtx, from, batch)
		cancel()

		switch {
		case err == nil:
			sent += len(batch)
		case errors.Is(err, notify.ErrEmailNotConfigured):
			failed += len(msgs) - i
			log.Error().Msg("runmeet reminder: RESEND_API_KEY unset, aborting email batch")
			break batchLoop
		case notify.IsQuotaExceeded(err):
			failed += len(msgs) - i
			log.Warn().Err(err).Int("sent", sent).Msg("runmeet reminder: email quota exceeded, stopping")
			break batchLoop
		default:
			failed += len(batch)
			log.Error().Err(err).Int("batch_start", i).Msg("runmeet reminder: email batch send failed, continuing")
		}
		if end < len(msgs) {
			time.Sleep(reminderEmailBatchSleep)
		}
	}
	log.Info().Int("sent", sent).Int("failed", failed).Msg("runmeet reminder: email batch done")
}
