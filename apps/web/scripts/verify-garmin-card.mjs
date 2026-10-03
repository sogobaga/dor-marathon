// 驗證 Garmin 官方直連前台（apps/web/src/lib/attribution.ts、lib/garminApi.ts 的純函式與 API client，
// 以及 GarminCard／ProfileScreen／RaceDetailScreen／PhoneShell 的接線守門）。
// 直接 import 實際檔案、非重寫邏輯（Node 24 原生 TS type-stripping，所以兩支 lib 都不能有 import／enum／參數屬性）。
// 執行位置：apps/web 下 `node scripts/verify-garmin-card.mjs`（路徑以本檔為基準，不寫死）。
//
// 涵蓋：
//   ① 歸屬字串「Garmin ＋ 型號」：型號不明→只寫 Garmin、型號已含 Garmin 不重複、非 Garmin 來源絕不標成 Garmin、表頭集中標示；
//   ② ?garmin= 導回結果文案：後端 13 個固定詞彙都有訊息（與 Go 原始碼對拍）、未知 reason 絕不原樣顯示（內容注入）、原型鏈鍵安全；
//   ③ 授權網址防呆、API 錯誤→使用者文案（不回顯伺服器字串）、中斷結果文案（筆數、Garmin 端撤銷失敗要請使用者自行移除）；
//   ④ 卡片狀態矩陣（入口 hidden＋未連線零請求、已連線但入口關閉只剩中斷、舊 Terra 連線→重新授權…）；
//   ⑤ 時間顯示（台北時區、跨日、h23 午夜、相對時間邊界、48 小時門檻）；
//   ⑥ API client：以假 fetch 驗證 URL／方法／Authorization／連接 body 帶 consent_v、錯誤形狀與 withUserAuth 相容；
//   ⑦ 合規與接線守門：沒有「匯入數據」API、官方 tile 只留預留位（沒有任何仿製圖形）、同意＋免責＋AI 聲明文案存在、
//      活動列／賽事歷程的 Garmin 歸屬接線在、COROS 既有那一行沒被動到、PhoneShell 認得 ?garmin=；
//   ⑧ 與後端 Go 原始碼對拍（找不到檔案就略過、不算失敗）：導回 reason 詞彙、/status 與 /disconnect 欄位、同意版本 v1、
//      後端實際落地的活動欄位都要在同意文案裡列出（新增欄位或平均心率開關改變會讓這裡失敗，逼人重審文案並升 consent_v）。
import fs from 'node:fs'

const here = (rel) => new URL(rel, import.meta.url)
const read = (rel) => fs.readFileSync(here(rel), 'utf8').replace(/\r\n/g, '\n')

const attr = await import(here('../src/lib/attribution.ts').href)
const api = await import(here('../src/lib/garminApi.ts').href)
const {
  GARMIN_BRAND, cleanDeviceModel, garminAttribution, isGarminSource, sourceAttribution, garminListAttribution,
} = attr
const {
  GARMIN_CONSENT_VERSION, GARMIN_RESULT_REASONS, GARMIN_STALE_AFTER_MS, GarminApiError, garminApi,
  isGarminAuthUrl, garminResultNotice, garminErrorText, garminDisconnectNotice, garminShouldFetch, garminView,
  garminDay, garminShortTime, garminRelativeAgo, garminDataStale,
} = api

let pass = 0, fail = 0, skipped = 0
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}
function ok(cond, label) {
  if (cond) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}`) }
}
function skip(label) { skipped++; console.log(`SKIP ${label}`) }

// ════════ ① 歸屬字串 ════════
eq(GARMIN_BRAND, 'Garmin', '品牌字樣常數')
// 不可見字元用碼位組出來，原始碼裡不放任何隱藏字元
const RLO = String.fromCharCode(0x202e), ZWSP = String.fromCharCode(0x200b), BOM = String.fromCharCode(0xfeff)
eq(garminAttribution('Forerunner 265'), 'Garmin Forerunner 265', '有型號 → Garmin ＋ 型號')
eq(garminAttribution('Garmin Forerunner 265'), 'Garmin Forerunner 265', '型號已以 Garmin 開頭 → 不重複')
eq(garminAttribution('GARMIN FENIX 7'), 'Garmin FENIX 7', '開頭 GARMIN 全大寫 → 品牌字樣統一成 Garmin，型號維持原樣')
eq(garminAttribution('garmin-Forerunner 955'), 'Garmin Forerunner 955', '開頭 garmin- 連同分隔符一併去掉')
eq(garminAttribution('Garmin: Venu 3S'), 'Garmin Venu 3S', '開頭 Garmin: 連同冒號去掉')
eq(garminAttribution('Garminfoo X'), 'Garmin Garminfoo X', '開頭是 Garminfoo（不是獨立的 Garmin 字）→ 不誤砍')
for (const [v, lab] of [
  [null, 'null'], [undefined, 'undefined'], ['', '空字串'], ['   ', '純空白'], ['unknown', 'unknown'], ['Unknown', 'Unknown'],
  ['UNKNOWN', 'UNKNOWN'], ['n/a', 'n/a'], ['null', '"null" 字串'], ['undefined', '"undefined" 字串'], ['-', '"-"'],
  ['Garmin', '只有 Garmin'], ['garmin', '只有 garmin'], ['Garmin unknown', 'Garmin unknown'], [123, '數字'], [{}, '物件'], [['x'], '陣列'],
]) {
  eq(garminAttribution(v), 'Garmin', `型號不明（${lab}）→ 只寫 Garmin`)
}
eq(garminAttribution('Forerunner\n\t  265'), 'Garmin Forerunner 265', '換行／tab／連續空白壓成單一空白')
eq(garminAttribution('Fore\u0000runner\u0007 265'), 'Garmin Fore runner 265', '控制字元當空白處理')
{
  const long = 'X'.repeat(300)
  const lab = garminAttribution(long)
  ok(lab === 'Garmin ' + 'X'.repeat(64), '超長型號截到 64 個字元（防版面被撐爆）')
}
eq(garminAttribution('Fore' + RLO + 'runner 265'), 'Garmin Forerunner 265', '雙向控制字元（RLO）直接移除')
eq(garminAttribution('' + ZWSP + 'Forerunner' + ZWSP + ' 265' + BOM + ''), 'Garmin Forerunner 265', '零寬字元／BOM 直接移除')
eq(garminAttribution('' + RLO + '' + ZWSP + ''), 'Garmin', '只有不可見字元 → 視為型號不明')
eq(garminAttribution('Garmin' + ZWSP + ' Forerunner 265'), 'Garmin Forerunner 265', '品牌字樣中夾零寬字元也不會重複')
eq(cleanDeviceModel('Garmin Forerunner 265'), 'Forerunner 265', 'cleanDeviceModel 只回純型號（不含品牌）')
eq(cleanDeviceModel('unknown'), '', 'cleanDeviceModel 不明回空字串')
eq(cleanDeviceModel('🏃 Forerunner 🏔'), '🏃 Forerunner 🏔', '表情符號型號不被破壞')

// isGarminSource／sourceAttribution：只有 source==garmin 才標 Garmin
eq(isGarminSource('garmin'), true, "isGarminSource('garmin')")
eq(isGarminSource('GARMIN'), true, "isGarminSource('GARMIN')（大小寫不拘）")
eq(isGarminSource(' garmin '), true, 'isGarminSource 容忍前後空白')
for (const s of ['coros', 'strava', 'polar', '', 'gps', 'manual', 'garmin2', 'garmin-terra', null, undefined]) {
  eq(isGarminSource(s), false, `isGarminSource(${JSON.stringify(s)}) = false`)
}
eq(sourceAttribution('garmin', 'Forerunner 265'), 'Garmin Forerunner 265', 'Garmin 列有型號 → Garmin Forerunner 265')
eq(sourceAttribution('garmin', null), 'Garmin', 'Garmin 列型號不明 → Garmin')
eq(sourceAttribution('garmin', 'Garmin Forerunner 265'), 'Garmin Forerunner 265', 'Garmin 列型號已含 Garmin → 不重複')
eq(sourceAttribution('coros', 'Garmin Forerunner 265'), null, 'COROS 列即使 device_name 長得像 Garmin 也不標 Garmin')
eq(sourceAttribution('strava', 'Forerunner 265'), null, 'Strava 列不標 Garmin')
eq(sourceAttribution('', 'Forerunner 265'), null, 'App GPS 列（空來源）不標 Garmin')
eq(sourceAttribution(null, null), null, 'null 來源 → null')
eq(sourceAttribution(undefined, undefined), null, 'undefined 來源 → null')

// garminListAttribution（表頭集中標示）
eq(garminListAttribution([]), null, '空清單 → null')
eq(garminListAttribution([{ source: 'coros', device_name: 'X' }, { source: '', device_name: 'Y' }]), null, '沒有 Garmin 列 → null（不顯示歸屬）')
eq(garminListAttribution([{ source: 'garmin' }]), 'Garmin', '只有 Garmin 列、型號不明 → Garmin')
eq(garminListAttribution([{ source: 'garmin', device_name: 'Forerunner 265' }]), 'Garmin Forerunner 265', '單一型號')
eq(
  garminListAttribution([{ source: 'garmin', device_name: 'Forerunner 265' }, { source: 'garmin', device_name: 'fenix 7' }]),
  'Garmin Forerunner 265、Garmin fenix 7', '兩種型號 → 各自完整歸屬，以「、」連接，依出現順序',
)
eq(
  garminListAttribution([{ source: 'garmin', device_name: 'Forerunner 265' }, { source: 'garmin', device_name: 'FORERUNNER 265' }]),
  'Garmin Forerunner 265', '型號大小寫不同視為同一個，不重複列',
)
eq(
  garminListAttribution([{ source: 'garmin' }, { source: 'garmin', device_name: 'Forerunner 265' }]),
  'Garmin Forerunner 265', '部分列型號不明、部分已知 → 只列已知型號（Garmin 已是列出的來源）',
)
eq(
  garminListAttribution([{ source: 'garmin', device_name: 'Forerunner 265' }, { source: 'coros', device_name: 'Pace 3' }, { source: 'strava', device_name: 'Edge' }]),
  'Garmin Forerunner 265', '混合來源：只列 Garmin 的型號，不把 COROS／Strava 的型號算進來',
)
eq(garminListAttribution([null, undefined, { source: 'garmin' }]), 'Garmin', '清單裡有 null／undefined 不崩')

// ════════ ② ?garmin= 導回結果文案 ════════
{
  const n = garminResultNotice('connected', null)
  ok(n && n.kind === 'success' && n.text.includes('授權完成') && n.text.includes('Garmin Connect'), 'connected → 成功訊息（授權完成＋說明資料由手錶同步後送達）')
  ok(n.text.includes('連接之後開始'), 'connected 文案說明只匯入連接之後的活動')
}
eq(garminResultNotice('connected', 'whatever')?.kind, 'success', 'connected 帶多餘 reason 也照成功處理')
eq(garminResultNotice('weird', null), null, '非 connected／error 的值 → 不處理')
eq(garminResultNotice('', null), null, '空字串 → 不處理')
eq(garminResultNotice(null, null), null, 'null → 不處理')
eq(garminResultNotice(undefined, undefined), null, 'undefined → 不處理')
eq(garminResultNotice('CONNECTED', null), null, '大小寫不符 → 不處理（後端固定小寫）')

const BACKEND_REASONS = ['invalid_state', 'state_mismatch', 'denied', 'missing_code', 'token_exchange_failed', 'api_failed', 'no_activity_permission', 'already_linked', 'account_changed', 'entry_closed', 'server_config', 'save_failed', 'disabled']
eq([...GARMIN_RESULT_REASONS].sort(), [...BACKEND_REASONS].sort(), '前台詞彙表＝契約的 13 個固定 reason')
const GENERIC = garminResultNotice('error', 'nonsense_reason').text
for (const r of BACKEND_REASONS) {
  const n = garminResultNotice('error', r)
  ok(n && n.text.length > 10 && n.text !== GENERIC, `reason=${r} 有專屬說明（不是通用句）`)
  ok(n && n.kind === (r === 'denied' ? 'info' : 'error'), `reason=${r} 的訊息類型（denied＝使用者自己取消，用中性色；其餘＝錯誤）`)
}
ok(garminResultNotice('error', 'already_linked').text.includes('此 Garmin 帳號已連結到另一個 DOR 帳號'), 'already_linked 文案')
ok(garminResultNotice('error', 'state_mismatch').text.includes('Safari／Chrome'), 'state_mismatch 引導改用 Safari／Chrome')
ok(garminResultNotice('error', 'no_activity_permission').text.includes('活動'), 'no_activity_permission 說明要允許「活動」資料')
ok(garminResultNotice('error', 'account_changed').text.includes('中斷連線'), 'account_changed 引導先中斷連線再連新帳號')
ok(garminResultNotice('error', 'denied').text.includes('沒有取得任何資料'), 'denied 說明 DOR 沒取得資料')
ok(garminResultNotice('error', 'invalid_state').text.includes('重新按'), 'invalid_state 引導重新連接')
ok(garminResultNotice('error', 'entry_closed').text === 'Garmin 直連目前未對你的帳號開放。如有疑問請聯絡我們。', 'entry_closed 文案（同 403，不承諾稍後再試）')

// 內容注入：未知 reason 一律通用句，且輸出不含輸入
for (const evil of [
  '<img src=x onerror=alert(1)>', '"><script>alert(1)</script>', 'javascript:alert(1)', 'reason with spaces and 中文', '../../etc/passwd',
  'a'.repeat(5000), 'INVALID_STATE', ' invalid_state\u0000', '__proto__', 'constructor', 'toString', 'hasOwnProperty', 'valueOf', '__defineGetter__',
]) {
  const n = garminResultNotice('error', evil)
  const label = evil.length > 40 ? evil.slice(0, 20) + '…(' + evil.length + ' 字)' : evil
  ok(n && n.text === GENERIC && n.kind === 'error', `未知 reason「${label}」→ 通用句、不處理成已知詞彙`)
  ok(n && (evil.trim() === '' || !n.text.includes(evil.trim())), `未知 reason「${label}」不會被原樣顯示`)
}
eq(garminResultNotice('error', null).text, GENERIC, 'error 沒帶 reason → 通用句')
eq(garminResultNotice('error', undefined).text, GENERIC, 'error reason=undefined → 通用句')
eq(garminResultNotice('error', '').text, GENERIC, 'error reason 空字串 → 通用句')
eq(garminResultNotice('error', '  denied  ').kind, 'info', 'reason 前後空白容忍（trim）')

// ════════ ③ 授權網址防呆、錯誤文案、中斷結果 ════════
for (const u of [
  'https://connect.garmin.com/oauth2Confirm?response_type=code&client_id=abc&code_challenge=xyz&code_challenge_method=S256&redirect_uri=https%3A%2F%2Fexample.test%2Fcb&state=s',
  'https://garmin.com/x', 'https://sub.connect.garmin.com/y', 'HTTPS://CONNECT.GARMIN.COM/oauth2Confirm',
]) {
  eq(isGarminAuthUrl(u), true, `isGarminAuthUrl 放行 ${u.slice(0, 48)}`)
}
for (const u of [
  'http://connect.garmin.com/oauth2Confirm', 'https://evil.example/oauth2Confirm', 'https://connect.garmin.com.evil.example/', 'https://evilgarmin.com/',
  'https://garmin.com.evil.example', 'https://notgarmin.com', 'javascript:alert(1)', 'data:text/html,<script>alert(1)</script>',
  'https://user:pw@connect.garmin.com/', 'https://connect.garmin.com@evil.example/', '//connect.garmin.com/x', '/api/v1/x', 'connect.garmin.com',
  '', null, undefined, 123, {}, 'https://' + 'a'.repeat(5000) + '.garmin.com/',
]) {
  eq(isGarminAuthUrl(u), false, `isGarminAuthUrl 擋下 ${String(typeof u === 'string' ? u.slice(0, 48) : JSON.stringify(u))}`)
}

const E = (status, message, name) => ({ status, message, name })
eq(garminErrorText('connect', E(0, '', 'SessionExpiredError')), '登入已過期，請重新登入。', '登入過期 → 請重新登入')
eq(garminErrorText('connect', E(400, 'consent_required')), '請先勾選同意後再連接。', 'connect 400 consent_required')
eq(garminErrorText('connect', E(400, 'invalid_consent_version')), '說明內容已更新，請重新整理頁面後再試。', 'connect 400 invalid_consent_version → 請重新整理')
eq(garminErrorText('connect', E(403, 'forbidden')), 'Garmin 直連目前未對你的帳號開放。如有疑問請聯絡我們。', 'connect 403（入口未開放／被後台限制同一句，不承諾「稍後再試」）')
eq(garminErrorText('connect', E(503, 'garmin_disabled')), 'Garmin 直連目前尚未開通，請稍後再試。', 'connect 503 garmin_disabled')
eq(garminErrorText('connect', E(503, 'try_again')), '系統忙碌中，請稍後再試。', 'connect 503 try_again')
eq(garminErrorText('connect', E(429, 'rate limited')), '操作太頻繁，請稍候一分鐘再試。', 'connect 429')
eq(garminErrorText('connect', E(500, 'failed')), '無法連接，請再試一次。', 'connect 500 → 通用')
eq(garminErrorText('connect', null), '無法連接，請再試一次。', 'connect null 錯誤 → 通用')
eq(garminErrorText('connect', 'oops'), '無法連接，請再試一次。', 'connect 字串錯誤 → 通用')
eq(garminErrorText('disconnect', E(500, 'failed')), '中斷失敗，請稍後再試。', 'disconnect 失敗')
eq(garminErrorText('disconnect', E(429, 'x')), '操作太頻繁，請稍候一分鐘再試。', 'disconnect 429')
eq(garminErrorText('status', E(500, 'x')), '無法取得 Garmin 連線狀態，請稍後再試。', 'status 失敗')
ok(!garminErrorText('connect', E(500, '<script>alert(1)</script>')).includes('<script>'), '不回顯伺服器錯誤字串（連接）')
ok(!garminErrorText('disconnect', E(500, 'SELECT * FROM users')).includes('SELECT'), '不回顯伺服器錯誤字串（中斷）')

{
  const a = garminDisconnectNotice({ ok: true, deleted_activities: 12, garmin_notified: true, was_connected: true })
  ok(a.kind === 'success' && a.text.includes('已刪除 12 筆') && a.text.includes('已通知 Garmin 撤銷授權') && a.text.includes('EXP／DP'), '中斷成功：刪除筆數＋已通知 Garmin＋獎勵不受影響')
  const b = garminDisconnectNotice({ ok: true, deleted_activities: 0, garmin_notified: true, was_connected: true })
  ok(b.text.includes('已刪除 0 筆'), '刪除 0 筆也要明說')
  const c = garminDisconnectNotice({ ok: true, deleted_activities: 3, garmin_notified: false, was_connected: true })
  ok(c.kind === 'warn' && c.text.includes('已刪除 3 筆') && c.text.includes('沒能通知 Garmin') && c.text.includes('Connected Apps') && c.text.includes('移除 DOR'), 'Garmin 端撤銷沒成功：警示＋請使用者到 Garmin Connect 移除 DOR')
  const d = garminDisconnectNotice({ ok: true, was_connected: false })
  ok(d.kind === 'info' && d.text.includes('沒有連接中的 Garmin'), 'was_connected=false（冪等）→ 說明原本就沒連線')
  const e = garminDisconnectNotice({ ok: true })
  ok(e.kind === 'success' && !e.text.includes('筆已匯入') && e.text.includes('已匯入的 Garmin 紀錄已刪除'), '舊後端沒回筆數 → 不編造數字')
  eq(garminDisconnectNotice({ ok: true, deleted_activities: -1, garmin_notified: true }).text.includes('-1'), false, '負數筆數不顯示')
  ok(garminDisconnectNotice({ ok: true, deleted_activities: 3.7, garmin_notified: true }).text.includes('已刪除 3 筆'), '小數筆數取整')
  ok(!garminDisconnectNotice({ ok: true, deleted_activities: 'x', garmin_notified: true }).text.includes('x 筆'), '非數字筆數不顯示')
  eq(garminDisconnectNotice(null).kind, 'success', 'null 結果不崩')
  eq(garminDisconnectNotice(undefined).kind, 'success', 'undefined 結果不崩')
}

// ════════ ④ 卡片狀態矩陣 ════════
eq(garminShouldFetch('shown', false), true, 'shouldFetch：入口 shown')
eq(garminShouldFetch('hidden', false), false, 'shouldFetch：入口 hidden 且沒連線 → 零請求')
eq(garminShouldFetch('hidden', true), true, 'shouldFetch：入口 hidden 但已有連線 → 要打（才能顯示狀態與中斷）')
eq(garminShouldFetch(undefined, false), false, 'shouldFetch：入口缺席且沒連線 → 零請求')
eq(garminShouldFetch(null, false), false, 'shouldFetch：入口 null → 零請求')
eq(garminShouldFetch('locked', false), false, 'shouldFetch：locked 視同未開放')
eq(garminShouldFetch(undefined, true), true, 'shouldFetch：入口缺席但有連線 → 要打')
const V = (o) => garminView({ entryHint: 'shown', hasConnectionHint: false, status: null, loaded: false, failed: false, hasNotice: false, ...o })
eq(V({ entryHint: 'hidden' }), { kind: 'none' }, '入口 hidden、沒連線、沒訊息 → 不顯示')
eq(V({ entryHint: undefined }), { kind: 'none' }, '入口缺席、沒連線、沒訊息 → 不顯示')
eq(V({ entryHint: 'hidden', hasNotice: true }), { kind: 'closed' }, '入口 hidden、沒連線、有訊息 → 只顯示訊息的卡（不 loading：沒有請求）')
eq(V({}), { kind: 'loading' }, '入口 shown、status 未回 → loading（不先閃連接表單）')
eq(V({ entryHint: 'hidden', hasConnectionHint: true }), { kind: 'loading' }, '入口 hidden 但 Dashboard 說有連線、status 未回 → loading')
eq(V({ loaded: true, status: { connected: false, entry: 'shown' } }), { kind: 'connect', legacy: false }, '未連線＋入口 shown → 連接表單')
eq(V({ loaded: true, status: { connected: false, legacy_terra: true, entry: 'shown' } }), { kind: 'connect', legacy: true }, '舊 Terra 連線＋入口 shown → 連接表單（legacy 橫幅：請重新授權一次）')
eq(V({ loaded: true, status: { connected: true, entry: 'shown' } }), { kind: 'connected', entryShown: true, needsReauth: false, paused: false }, '已連接（一般）')
eq(V({ loaded: true, status: { connected: true, needs_reauth: true, entry: 'shown' } }), { kind: 'connected', entryShown: true, needsReauth: true, paused: false }, '已連接＋needs_reauth')
eq(V({ loaded: true, status: { connected: true, paused: true, entry: 'shown' } }), { kind: 'connected', entryShown: true, needsReauth: false, paused: true }, '已連接＋paused')
eq(V({ loaded: true, status: { connected: true, paused: true, needs_reauth: true, entry: 'shown' } }), { kind: 'connected', entryShown: true, needsReauth: true, paused: true }, '兩個旗標同時成立')
eq(V({ entryHint: 'hidden', hasConnectionHint: true, loaded: true, status: { connected: true, entry: 'hidden' } }), { kind: 'connected', entryShown: false, needsReauth: false, paused: false }, '入口關閉但已連線 → 只剩狀態與中斷（entryShown=false）')
eq(V({ entryHint: 'hidden', loaded: true, status: { connected: true, entry: 'shown' } }), { kind: 'connected', entryShown: true, needsReauth: false, paused: false }, 'status.entry 比 Dashboard 的 entry 新，以 status 為準（shown）')
eq(V({ entryHint: 'shown', hasConnectionHint: true, loaded: true, status: { connected: true, entry: 'hidden' } }), { kind: 'connected', entryShown: false, needsReauth: false, paused: false }, 'status.entry 比 Dashboard 的 entry 新，以 status 為準（hidden）')
eq(V({ entryHint: 'hidden', hasConnectionHint: true, loaded: true, status: { connected: false, legacy_terra: true, entry: 'hidden' } }), { kind: 'none' }, '只有舊 Terra 連線＋入口關閉 → 卡片不顯示（Terra 卡自己列出）')
eq(V({ entryHint: 'hidden', hasConnectionHint: true, loaded: true, hasNotice: true, status: { connected: false, entry: 'hidden' } }), { kind: 'closed' }, '剛中斷、入口關閉 → 只留訊息的卡（讓使用者看到刪除筆數）')
eq(V({ loaded: true, failed: true }), { kind: 'error' }, 'status 失敗且沒有舊資料 → error（可重試）')
eq(V({ loaded: true, failed: true, status: { connected: true, entry: 'shown' } }), { kind: 'connected', entryShown: true, needsReauth: false, paused: false }, 'status 後續失敗但有舊資料 → 保留舊畫面')
eq(V({ entryHint: 'hidden', loaded: true, failed: true }), { kind: 'none' }, '不該打 status 的人，即使 failed 旗標殘留也不顯示 error')
eq(V({ loaded: true, status: { connected: false } }), { kind: 'connect', legacy: false }, 'status 沒回 entry → 退回 Dashboard 的 entry（shown）')
eq(V({ entryHint: 'hidden', hasConnectionHint: true, loaded: true, status: { connected: false } }), { kind: 'none' }, 'status 沒回 entry、Dashboard 也 hidden → 不顯示')

// ════════ ⑤ 時間顯示 ════════
eq(garminDay('2026-10-03T16:30:00Z'), '2026/10/04', '台北日期：UTC 16:30 已是台北隔天 00:30')
eq(garminDay('2026-10-03T15:59:59Z'), '2026/10/03', '台北日期：UTC 15:59:59 仍是台北當天 23:59')
eq(garminDay('2026-01-01T00:00:00+08:00'), '2026/01/01', '台北日期：+08:00 輸入')
eq(garminDay('2026-12-31T16:00:00Z'), '2027/01/01', '台北日期：跨年')
eq(garminDay('not-a-date'), '', '無效日期 → 空字串')
eq(garminDay(null), '', 'null → 空字串')
eq(garminDay(''), '', '空字串 → 空字串')
eq(garminShortTime('2026-10-03T16:30:00Z'), '10/04 00:30', '短時間：午夜顯示 00 而不是 24（h23）')
eq(garminShortTime('2026-10-03T15:59:00Z'), '10/03 23:59', '短時間：23:59')
eq(garminShortTime('2026-10-03T01:05:00Z'), '10/03 09:05', '短時間：補零')
eq(garminShortTime(undefined), '', '短時間 undefined → 空字串')
const NOW = Date.parse('2026-10-03T12:00:00Z')
const AGO = (ms) => new Date(NOW - ms).toISOString()
eq(garminRelativeAgo(AGO(0), NOW), '剛剛', '相對時間：0 秒')
eq(garminRelativeAgo(AGO(59_000), NOW), '剛剛', '相對時間：59 秒')
eq(garminRelativeAgo(AGO(60_000), NOW), '1 分鐘前', '相對時間：1 分鐘')
eq(garminRelativeAgo(AGO(59 * 60_000), NOW), '59 分鐘前', '相對時間：59 分鐘')
eq(garminRelativeAgo(AGO(60 * 60_000), NOW), '1 小時前', '相對時間：60 分鐘＝1 小時')
eq(garminRelativeAgo(AGO(119 * 60_000), NOW), '1 小時前', '相對時間：119 分鐘仍是 1 小時')
eq(garminRelativeAgo(AGO(23 * 3600_000 + 59 * 60_000), NOW), '23 小時前', '相對時間：23 小時 59 分')
eq(garminRelativeAgo(AGO(24 * 3600_000), NOW), '1 天前', '相對時間：24 小時＝1 天')
eq(garminRelativeAgo(AGO(49 * 3600_000), NOW), '2 天前', '相對時間：49 小時＝2 天')
eq(garminRelativeAgo(new Date(NOW + 5 * 60_000).toISOString(), NOW), '剛剛', '相對時間：未來時間（時鐘偏差）視為剛剛')
eq(garminRelativeAgo('garbage', NOW), '', '相對時間：無效 → 空字串')
eq(garminRelativeAgo(null, NOW), '', '相對時間：null → 空字串')
eq(GARMIN_STALE_AFTER_MS, 48 * 3600 * 1000, '資料延遲門檻 48 小時')
eq(garminDataStale(AGO(48 * 3600_000), null, NOW), false, '剛好 48 小時 → 不算過期（要超過）')
eq(garminDataStale(AGO(48 * 3600_000 + 1000), null, NOW), true, '超過 48 小時 1 秒 → 過期')
eq(garminDataStale(AGO(1 * 3600_000), AGO(100 * 3600_000), NOW), false, '有最近資料（1 小時前）→ 不看連接時間')
eq(garminDataStale(null, AGO(72 * 3600_000), NOW), true, '從沒收到資料、連接已 72 小時 → 過期（以連接時間起算）')
eq(garminDataStale(null, AGO(2 * 3600_000), NOW), false, '從沒收到資料、剛連接 2 小時 → 不提示')
eq(garminDataStale(null, null, NOW), false, '兩個時間都沒有 → 不亂提示')
eq(garminDataStale('garbage', null, NOW), false, '時間無效 → 不亂提示')

// ════════ ⑥ API client（假 fetch）════════
{
  const calls = []
  const realFetch = globalThis.fetch
  const respond = (status, body, headers = {}) => new Response(body == null ? null : (typeof body === 'string' ? body : JSON.stringify(body)), { status, headers })
  let next = () => respond(200, {})
  globalThis.fetch = async (url, init) => { calls.push({ url: String(url), init }); return next() }
  try {
    eq(Object.keys(garminApi).sort(), ['connect', 'disconnect', 'status'], 'garminApi 只有 status／connect／disconnect（Garmin 只有推送：沒有 import／probe／sync）')
    eq(GARMIN_CONSENT_VERSION, 'v1', '同意版本常數 v1')
    ok(/^[A-Za-z0-9._-]{1,24}$/.test(GARMIN_CONSENT_VERSION), '同意版本符合後端 consentVersionRe')

    next = () => respond(200, { url: 'https://connect.garmin.com/oauth2Confirm?x=1' })
    const r = await garminApi.connect('tok-1')
    eq(r.url, 'https://connect.garmin.com/oauth2Confirm?x=1', 'connect 回傳 url')
    const c = calls.at(-1)
    eq(c.url, '/api/v1/integrations/garmin/connect', 'connect URL')
    eq(c.init.method, 'POST', 'connect 方法 POST（後端 /connect 只收 POST）')
    eq(c.init.headers.Authorization, 'Bearer tok-1', 'connect 帶 Authorization Bearer')
    eq(JSON.parse(c.init.body), { consent: true, consent_v: 'v1' }, 'connect body 帶 consent:true＋consent_v')
    eq(Object.keys(JSON.parse(c.init.body)).sort(), ['consent', 'consent_v'], 'connect body 只有 consent／consent_v（沒有多餘欄位）')

    next = () => respond(200, { connected: true, entry: 'shown', device_name: 'Forerunner 265' })
    const s = await garminApi.status('tok-2')
    eq(s.connected, true, 'status 回傳解析')
    const sc = calls.at(-1)
    eq(sc.url, '/api/v1/integrations/garmin/status', 'status URL')
    ok(!sc.init.method || sc.init.method === 'GET', 'status 方法 GET')
    eq(sc.init.cache, 'no-store', 'status 不快取')
    eq(sc.init.headers.Authorization, 'Bearer tok-2', 'status 帶 Authorization')

    next = () => respond(200, { ok: true, deleted_activities: 4, garmin_notified: true, was_connected: true })
    const d = await garminApi.disconnect('tok-3')
    eq(d.deleted_activities, 4, 'disconnect 回傳解析（deleted_activities）')
    const dc = calls.at(-1)
    eq(dc.url, '/api/v1/integrations/garmin/disconnect', 'disconnect URL')
    eq(dc.init.method, 'POST', 'disconnect 方法 POST')
    ok(dc.init.body === undefined, 'disconnect 沒有 body')

    // 錯誤形狀：與 lib/api.ts ApiError／userAuth.withUserAuth（e.status===401）相容
    next = () => respond(400, { error: 'consent_required' })
    try { await garminApi.connect('t'); ok(false, '400 應丟錯') } catch (e) {
      ok(e instanceof GarminApiError && e instanceof Error, '錯誤是 GarminApiError（也是 Error）')
      eq([e.status, e.message, e.name], [400, 'consent_required', 'GarminApiError'], '錯誤帶 status／message(error 代碼)／name')
      eq(e.body, { error: 'consent_required' }, '錯誤帶解析後的 body')
    }
    next = () => respond(429, { error: 'rate limited' }, { 'Retry-After': '30' })
    try { await garminApi.status('t'); ok(false, '429 應丟錯') } catch (e) { eq([e.status, e.retryAfterS], [429, 30], '429 帶 retryAfterS') }
    next = () => respond(401, { error: 'invalid token' })
    try { await garminApi.status('t'); ok(false, '401 應丟錯') } catch (e) { eq(e.status, 401, '401 → e.status===401（withUserAuth 據此換發 token 後重試）') }
    next = () => respond(502, '<html>bad gateway</html>')
    try { await garminApi.status('t'); ok(false, '502 應丟錯') } catch (e) { eq([e.status, e.message, e.body], [502, 'request failed', null], '非 JSON 錯誤 body → message=request failed、body=null') }
    next = () => respond(503, { error: 'garmin_disabled' })
    try { await garminApi.connect('t'); ok(false, '503 應丟錯') } catch (e) { eq([e.status, e.message], [503, 'garmin_disabled'], '503 garmin_disabled 代碼') }
    next = () => respond(204, null)
    eq(await garminApi.disconnect('t'), null, '204／空 body → null（不因解析 JSON 崩潰）')
    next = () => respond(200, 'not json')
    eq(await garminApi.status('t'), null, '成功但 body 不是 JSON → null（不崩）')
  } finally {
    globalThis.fetch = realFetch
  }
}

// ════════ ⑦ 合規與接線守門 ════════
const libAttr = read('../src/lib/attribution.ts')
const libApi = read('../src/lib/garminApi.ts')
const card = read('../src/components/profile/GarminCard.tsx')
const profile = read('../src/components/ProfileScreen.tsx')
const race = read('../src/components/RaceDetailScreen.tsx')
const shell = read('../src/components/PhoneShell.tsx')
const apiTs = read('../src/lib/api.ts')

ok(!/^\s*import\s/m.test(libAttr.replace(/\/\/.*$/gm, '')), 'attribution.ts 沒有 import（可被 type-stripping 直接載入）')
ok(!/^\s*import\s/m.test(libApi.replace(/\/\/.*$/gm, '')), 'garminApi.ts 沒有 import（可被 type-stripping 直接載入）')

// 官方 tile：只留預留位，不仿製任何 Garmin 標誌
ok(card.includes('data-garmin-tile-slot'), '卡片有 data-garmin-tile-slot（官方 tile 預留位）')
ok(/const GARMIN_TILE_SRC: string \| null = null/.test(card), '官方 tile 路徑預設 null（擁有者提供檔案後才填）')
ok(!/<svg|<path|<polygon|<canvas|data:image|base64,/i.test(card), '卡片沒有 svg／path／canvas／內嵌圖片（沒有任何自畫的標誌）')
ok(!/<svg|<path|data:image|base64,/i.test(libAttr + libApi), 'lib 沒有任何圖形資料')
ok(!/garmin[-_ ]?(logo|tag)|garmin-tag/i.test(card + libAttr + libApi), '沒有 Garmin logo／tag 相關的圖形引用')
ok(/完整名稱|Garmin Connect 直連/.test(card) && card.includes('Garmin Connect 直連'), '卡片標題使用完整名稱 Garmin Connect（不縮寫）')
// 沒有「匯入數據」按鈕／API（Garmin 只有推送）
ok(!/garminApi\.(import|probe|sync)/.test(card + profile), '卡片／個人頁沒有呼叫 Garmin 的 import／probe／sync')
ok((card.match(/匯入數據/g) || []).length > 0 && (card.match(/匯入數據/g) || []).length === (card.match(/沒有「匯入數據」按鈕/g) || []).length,
  '卡片裡每一處「匯入數據」都是「沒有『匯入數據』按鈕」的說明句（沒有這種按鈕）')
ok(!/onClick=\{[^}]*import/i.test(card), '卡片沒有任何 import 的 onClick')
// 同意／免責／AI／隱私
for (const [needle, label] of [
  ['type="checkbox"', '同意勾選框'],
  ['我已了解上述說明，同意透過 Garmin Connect 將我的活動資料分享給 DOR', '同意文案'],
  ['活動類型、開始時間、時間長度、距離、爬升、配速、平均心率與手錶型號', '說明分享哪些資料'],
  ['可能需要幾分鐘到數小時', '說明資料延遲'],
  ['只匯入你連接之後開始的活動', '說明只匯入連接之後'],
  ['不會用於訓練 AI', '不用於 AI 聲明'],
  ['看不到你的單筆活動或心率', '其他使用者只看到衍生結果'],
  ['已獲得的 EXP／DP 等獎勵不會收回', '中斷後獎勵保留'],
  ['DOR 與 Garmin 沒有隸屬關係，也未獲得 Garmin 背書', '免責聲明（無隸屬、無背書）'],
  ['/privacy#garmin', '隱私政策連結'],
  ['確定要中斷 Garmin 連線嗎？', '中斷確認'],
  ['連結的應用程式（Connected Apps）', '引導到 Garmin Connect 移除 DOR／重新開啟分享'],
]) ok(card.includes(needle), `卡片文案：${label}`)
ok(/disabled=\{!consent \|\| busy !== ''\}/.test(card), '未勾同意時「連接」鈕 disabled')
ok(/listStyleType: 'disc'/.test(card) && (card.match(/<ul style=\{\{ \.\.\.bulletList, \.\.\.small \}\}>/g) || []).length === 2, '說明清單與確認面板清單都補回項目符號（全站樣式重置掉了 ul 圓點）')
ok(card.includes('按下按鈕後會前往 Garmin 的授權頁面；在那裡登入並同意後，會自動回到 DOR。'), '連接鈕下方說明會前往 Garmin 授權頁面再回到 DOR')
ok(!card.includes('官方授權'), '使用者可見文字沒有「官方授權」（避免暗示 Garmin 背書）')
ok(/role="group" aria-label="確認中斷 Garmin 連線"/.test(card), '中斷確認面板是行內群組（不是假裝成會搶焦點的對話框）')
ok(/connect\(false\)/.test(card) && /connect\(true\)/.test(card), '首次連接（需同意）與重新授權（不重複要求）兩條路徑')
ok(/if \(!reauth && !consent\) return/.test(card), '首次連接沒勾同意 → 函式內也擋（不只靠 disabled）')
ok(/isGarminAuthUrl\(url\)/.test(card), '導去 Garmin 前檢查授權網址')
ok(/pageshow/.test(card), 'bfcache 還原時解除「連接中」忙碌狀態')
ok(card.includes('data-garmin-attribution'), '已連接卡片有 Garmin 歸屬標記')
ok(/garminAttribution\(status\?\.device_name\)/.test(card), '卡片歸屬走 garminAttribution（Garmin ＋ 型號）')
// 接線
ok(profile.includes("import GarminCard from './profile/GarminCard'"), 'ProfileScreen 匯入 GarminCard')
ok(/showGarminCard = dash\?\.garmin_entry === 'shown' \|\| dashHasGarmin \|\| !!garminNotice/.test(profile), 'ProfileScreen：入口 shown／已有連線／有訊息 才掛載卡片')
ok(/\{showGarminCard && \(/.test(profile), 'ProfileScreen：卡片只在 showGarminCard 時渲染')
ok(/garminResultNotice\(gm, sp\.get\('reason'\)\)/.test(profile) && /sp\.delete\('garmin'\); sp\.delete\('reason'\)/.test(profile), 'ProfileScreen：?garmin= 走固定詞彙函式並清掉參數')
ok(/sourceAttribution\(a\.source, a\.device_name\)/.test(profile), 'ProfileScreen：活動列 Garmin 歸屬（Garmin ＋ 型號）')
ok(profile.includes("{a.source === 'coros' && a.device_name && ("), 'ProfileScreen：COROS 那一行維持原條件（只給 coros 列）')
ok(profile.includes('Data provided by COROS · {a.device_name}'), 'ProfileScreen：COROS 那一行文案沒被動到')
ok(/garminListAttribution\(days\.flatMap/.test(race) && /sourceAttribution\(a\.source, a\.device_name\)/.test(race), 'RaceDetailScreen：歷程標題下方集中歸屬＋每列 Garmin 標籤')
ok(/含 \{garminAttr\} 資料/.test(race), 'RaceDetailScreen：表頭歸屬文案「含 … 資料」')
ok(shell.includes("params.has('garmin')") && /params\.has\('garmin'\)\) \{\s*setShowProfile\(true\)\s*setProfileInitialTab\('sports'\)/.test(shell), 'PhoneShell：?garmin= 開個人頁「運動數據」分頁')
ok(/export interface DailyActivity \{[\s\S]*?device_name\?: string \| null/.test(apiTs), 'api.ts：DailyActivity 有 device_name（型別）')
ok(/garmin_entry\?: 'hidden' \| 'locked' \| 'shown'/.test(apiTs) && /connected_sources\?: string\[\]/.test(apiTs), 'api.ts：Dashboard 已有 garmin_entry／connected_sources 型別')

// 與後端 Go 原始碼對拍（找不到檔案就略過，不算失敗）
const goConnect = (() => { try { return read('../../../services/api/internal/integration/garmin_connect.go') } catch { return null } })()
const goMain = (() => { try { return read('../../../services/api/internal/integration/garmin.go') } catch { return null } })()
if (goConnect) {
  const used = [...goConnect.matchAll(/redirectFront\(w, r, "error", "([a-z_]+)"\)/g)].map((m) => m[1])
  const usedSet = [...new Set(used)].sort()
  ok(usedSet.length >= 13, `後端實際會導回的 reason 數量（${usedSet.length}）≥ 13`)
  eq(usedSet.filter((r) => !GARMIN_RESULT_REASONS.includes(r)), [], '後端會導回的每個 reason 前台都有專屬文案')
  eq(GARMIN_RESULT_REASONS.filter((r) => !usedSet.includes(r)), [], '前台詞彙沒有後端不會送的多餘 reason')
  ok(/redirectFront\(w, r, "connected", ""\)/.test(goConnect), '後端成功導回 ?garmin=connected')
  ok(/appendQuery\(base, "garmin", status\)/.test(goConnect) && /appendQuery\(target, "reason", reason\)/.test(goConnect), '後端導回參數名稱 garmin／reason')
  ok(/json:"consent"/.test(goConnect) && /json:"consent_v"/.test(goConnect), '後端 /connect 讀 consent／consent_v')
  ok(/respondErr\(w, http\.StatusBadRequest, "consent_required"\)/.test(goConnect) && /"invalid_consent_version"/.test(goConnect), '後端 400 代碼 consent_required／invalid_consent_version')
  ok(/"garmin_disabled"/.test(goConnect) && /"try_again"/.test(goConnect), '後端 503 代碼 garmin_disabled／try_again')
  for (const k of ['connected', 'connected_at', 'import_from', 'last_data_at', 'device_name', 'needs_reauth', 'paused', 'legacy_terra', 'entry']) {
    ok(new RegExp(`"${k}"`).test(goConnect), `後端 /status 有欄位 ${k}`)
  }
  for (const k of ['ok', 'deleted_activities', 'garmin_notified', 'was_connected']) {
    ok(new RegExp(`"${k}"`).test(goConnect), `後端 /disconnect 有欄位 ${k}`)
  }
} else skip('找不到 garmin_connect.go，略過與後端的對拍')
{
  // 同意文案要與「後端實際落地的欄位」一致：後端新增儲存欄位、或平均心率開關改了，這裡會失敗，逼人重新審視文案並升 consent_v
  const goTypes = (() => { try { return read('../../../services/api/internal/integration/garmin_types.go') } catch { return null } })()
  if (goTypes) {
    const body = (goTypes.match(/type garminStoredActivity struct \{([\s\S]*?)\n\}/) || [])[1] || ''
    const keys = [...body.matchAll(/json:"([A-Za-z]+)/g)].map((m) => m[1])
    const TERMS = {
      activityType: '活動類型', startTimeInSeconds: '開始時間', durationInSeconds: '時間長度', distanceInMeters: '距離',
      deviceName: '手錶型號', totalElevationGainInMeters: '爬升', averageHeartRateInBeatsPerMinute: '平均心率',
    }
    const IDENT = new Set(['summaryId', 'activityId', 'manual', 'isWebUpload', 'isParent', 'parentSummaryId']) // 識別碼與判斷旗標，不是使用者資料項目
    ok(keys.length >= 10, `後端落地欄位解析成功（${keys.length} 個）`)
    eq(keys.filter((k) => !IDENT.has(k) && !(k in TERMS)), [], '後端沒有新增「同意文案沒列到」的落地欄位（有新欄位要補文案並升 consent_v）')
    for (const k of keys) if (k in TERMS) ok(card.includes(TERMS[k]), `同意文案有列出後端落地欄位 ${k}（${TERMS[k]}）`)
    ok(/garminStoreAvgHR\s*=\s*true/.test(goTypes) === card.includes('平均心率'), '文案提到「平均心率」⇔ 後端 garminStoreAvgHR 開著（兩者同步）')
    ok(!/atitude|ongitude|activityName|alories/.test(body), '後端落地欄位不含座標／活動名稱／熱量（文案「不保存位置座標與路線」成立）')
  } else skip('找不到 garmin_types.go，略過落地欄位對拍')
}
if (goMain) {
  ok(/c\.ConsentVersions = \[\]string\{"v1"\}/.test(goMain), `後端預設同意版本含 ${GARMIN_CONSENT_VERSION}（前台送的版本會被接受）`)
  ok(/r\.With\(h\.limit\("garmin_connect", 10, time\.Minute\)\)\.Post\("\/connect"/.test(goMain), '後端 /connect 是 POST')
  ok(/Get\("\/status"/.test(goMain) && /Post\("\/disconnect"/.test(goMain), '後端 /status GET、/disconnect POST')
} else skip('找不到 garmin.go，略過與後端的對拍')

console.log(`\n${pass} passed, ${fail} failed${skipped ? `, ${skipped} skipped` : ''}`)
if (fail > 0) process.exit(1)
