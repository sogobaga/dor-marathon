// 驗證 Terra／Strava 串接結束公告（announce）前台（apps/web/src/lib/wearableSunset.ts 的純函式，以及
// ProfileScreen／PhoneShell／SunsetBanner／lib/api.ts／lib/appSettings.ts 的接線守門）。
// 直接 import 實際檔案、非重寫邏輯（Node 24 原生 TS type-stripping，所以 lib 檔不能有 import／enum／參數屬性）。
// 執行位置：apps/web 下 `node scripts/verify-wearable-sunset.mjs`（路徑以本檔為基準，不寫死）。
//
// 涵蓋：
//   ① sunset 欄位正規化：缺席／null／非物件／未知 state／壞日期一律安全退成 off 或省略日期（舊後端、壞資料畫面不變）；
//   ② 日期標示（月日不補零、閏年、不存在的曆日）；
//   ③ 卡片視圖矩陣（狀態 × 有無連線）：off 恆 normal；
//   ④ 公告橫幅／短提示／確認視窗追加句／?strava=sunset 固定訊息：事實正確、COROS 只在入口開放時才被指名、沒有日期時句子仍通順；
//      「同步說不準」的連線列（保守品牌或最後資料逾 48 小時）不承諾照常同步、不指名其他品牌；沒有任何未經核准的承諾；
//      Terra 斷開確認在公告期間明講「活動將一併刪除（賽事成績會重新計算）」；
//   ⑤ API 409 → 固定訊息：只認 409＋code、絕不回顯伺服器字串（內容注入）、日期只取通過驗證的值；
//   ⑥ 文案守則：禁用詞（第三方裝置品牌、跑團、背書字樣）掃本檔與所有文案函式的輸出；沒有 HTML 注入路徑；
//   ⑦ 接線守門（讀原始碼）：兩張卡與四個 handler 都引用公告、連接鈕只在 normal 視圖且各只有一個呼叫點、每張橫幅直接掛在
//      自己的 === 'connected' 條件下、?strava=sunset／?terra=sunset 有分支、PhoneShell 的 ?strava= 導回會切到 sports 分頁
//      （訊息在該分頁內）、off 狀態的既有字串一個都沒被改、api.ts 型別、後台設定型錄；
//   ⑦b Terra 導回網址參數的顯示淨化（?terra=…&provider=…&reason=… 任何人都能偽造）：reason 只在長得像代碼（/^[\w:.-]{1,60}$/）時顯示，
//      provider 只經已知品牌對照表轉名稱、未知值（含 constructor／__proto__ 這類原型鏈名稱）一律用通用字樣；
//      因為 ProfileScreen 是 TSX 無法直接載入，這幾個純函式是從該檔原始碼擷取、去掉型別後實際執行的；
//   ⑦c 後台寫入設定（PUT /admin/app-settings/{key}）：所有呼叫點都送 {value: <字串>}、公告狀態下拉絕不送空字串，並與後端寫入驗證器對拍；
//   ⑧ 與後端 Go 原始碼對拍（找不到檔案就略過、不算失敗）：錯誤碼、狀態碼、導回值、JSON 欄位、預設結束日、設定鍵、錯誤句子。
import fs from 'node:fs'

const here = (rel) => new URL(rel, import.meta.url)
const read = (rel) => fs.readFileSync(here(rel), 'utf8').replace(/\r\n/g, '\n')
const tryRead = (rel) => { try { return read(rel) } catch { return null } }

const lib = await import(here('../src/lib/wearableSunset.ts').href)
const {
  SUNSET_ERROR_CODE, SUNSET_ERROR_STATUS, SUNSET_RESULT, SUNSET_RESULT_TEXT,
  normalizeSunset, sunsetDateLabel, sunsetCardView, sunsetBannerTitle, sunsetBannerLines,
  sunsetPausedNotice, sunsetErrorText, sunsetDisconnectNote, sunsetSupportLine, sunsetRowSyncUncertain, sunsetCanNameCoros,
} = lib

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

// 對外文案禁用詞（擁有者規定）：第三方裝置品牌 Garmin／佳明、「跑團」二字、不得暗示品牌背書。
const BANNED = /garmin|佳明|跑團|官方合作|認證|贊助|背書/i

// ════════ ① 正規化 ════════
const OFF = { state: 'off' }
for (const [v, lab] of [
  [undefined, 'undefined（舊後端沒有 sunset 欄位）'], [null, 'null'], ['announce', '字串（不是物件）'], [42, '數字'], [true, '布林'],
  [[], '陣列'], [['announce'], '含 announce 的陣列'], [{}, '空物件'], [{ state: null }, 'state=null'], [{ state: 1 }, 'state=數字'],
  [{ state: '' }, 'state=空字串'], [{ state: 'off' }, 'state=off'], [{ state: 'OFF', date: '2026-10-31' }, 'state=OFF＋日期'],
  [{ state: 'closed', date: '2026-10-31' }, '未知 state=closed（本版沒有 closed）'], [{ state: 'vip' }, '未知 state=vip'],
  [{ state: 'announced' }, 'state=announced（不是 announce）'], [{ state: ['announce'] }, 'state=陣列'], [{ state: { toString() { return 'announce' } } }, 'state=物件'],
]) {
  eq(normalizeSunset(v), OFF, `正規化：${lab} → off`)
}
eq(normalizeSunset({ state: 'announce', date: '2026-10-31' }), { state: 'announce', date: '2026-10-31' }, '正規化：announce＋日期')
eq(normalizeSunset({ state: ' Announce ', date: '2026-10-31' }), { state: 'announce', date: '2026-10-31' }, '正規化：前後空白與大小寫')
eq(normalizeSunset({ state: 'announce' }), { state: 'announce' }, '正規化：announce 沒有日期')
for (const bad of ['2026-10-32', '2026-02-29', '2026-13-01', '2026-00-10', '2026-10-00', '2026/10/31', '20261031', '2026-1-5', 'tomorrow', '', 20261031, null, {}, ['2026-10-31'], '2026-10-31T00:00:00Z', ' 2026-10-31']) {
  eq(normalizeSunset({ state: 'announce', date: bad }), { state: 'announce' }, `正規化：壞日期 ${JSON.stringify(bad)} → 省略日期（仍是 announce）`)
}
eq(normalizeSunset({ state: 'announce', date: '2028-02-29' }), { state: 'announce', date: '2028-02-29' }, '正規化：閏日 2028-02-29 合法')
eq(Object.keys(normalizeSunset({ state: 'announce', date: '2026-10-31', extra: 'x', __proto__: { evil: 1 } })).sort(), ['date', 'state'], '正規化：只留 state／date，多餘欄位丟掉')

// ════════ ② 日期標示 ════════
eq(sunsetDateLabel('2026-10-31'), '10 月 31 日', '日期標示 10/31')
eq(sunsetDateLabel('2026-01-05'), '1 月 5 日', '日期標示月日不補零')
eq(sunsetDateLabel('2026-12-01'), '12 月 1 日', '日期標示 12/1')
eq(sunsetDateLabel('2028-02-29'), '2 月 29 日', '日期標示閏日')
for (const bad of [undefined, '', 'nope', '2026-02-29', '2026-13-01', '2026-10-31T00:00', null]) {
  eq(sunsetDateLabel(bad), '', `日期標示：${JSON.stringify(bad)} → 空字串`)
}

// ════════ ③ 卡片視圖矩陣 ════════
eq(sunsetCardView('off', false), 'normal', '視圖：off＋沒連線 → normal')
eq(sunsetCardView('off', true), 'normal', '視圖：off＋已連線 → normal（與改版前完全相同）')
eq(sunsetCardView('announce', true), 'connected', '視圖：announce＋已連線 → connected（橫幅＋保留同步／中斷）')
eq(sunsetCardView('announce', false), 'paused', '視圖：announce＋沒連線 → paused（沒有連接鈕）')
eq(sunsetCardView('weird', true), 'normal', '視圖：未知狀態一律 normal（只有明確的 announce 才改畫面）')

// ════════ ④ 文案內容 ════════
eq(sunsetBannerTitle('Strava', '2026-10-31'), 'Strava 串接將於 10 月 31 日結束', '橫幅標題（Strava）')
eq(sunsetBannerTitle('Terra', '2026-11-15'), 'Terra 串接將於 11 月 15 日結束', '橫幅標題（Terra，不同日期）')
eq(sunsetBannerTitle('Terra', undefined), 'Terra 串接即將結束', '橫幅標題：沒有日期仍通順')
{
  // 四種情境：正常／同步說不準（保守品牌或最後資料逾 48 小時）× COROS 入口開放與否
  const ctxs = {
    '正常＋有 COROS 入口': { corosShown: true },
    '正常＋沒有 COROS 入口': { corosShown: false },
    '同步說不準＋有 COROS 入口': { corosShown: true, syncUncertain: true },
    '同步說不準＋沒有 COROS 入口': { corosShown: false, syncUncertain: true },
  }
  for (const [lab, ctx] of Object.entries(ctxs)) {
    const lines = sunsetBannerLines('2026-10-31', ctx)
    const t = lines.join('\n')
    ok(lines.length === 2, `橫幅內文兩段（${lab}）`)
    ok(t.includes('目前不再開放新的連接'), `橫幅（${lab}）：說明不再開放新連接`)
    ok(t.includes('中斷連線') && t.includes('不再同步新的紀錄'), `橫幅（${lab}）：之後中斷連線、不再同步新紀錄`)
    ok(t.includes('DOR 的 GPS 跑步追蹤'), `橫幅（${lab}）：引導用 DOR 的 GPS 跑步追蹤記錄成績`)
    // 不承諾也不否認已匯入資料的處置（另案決定）
    ok(!/刪除|保留|清除/.test(t), `橫幅（${lab}）：不對已匯入資料的處置做任何承諾`)
    // 未經擁有者核准的承諾一律不寫（例如「其他品牌的直連開放時，我們會再通知你」）
    ok(!/通知|我們會|之後會再|開放時/.test(t), `橫幅（${lab}）：沒有任何未經核准的承諾（不寫「之後會再通知你」之類）`)
  }
  const normalCoros = sunsetBannerLines('2026-10-31', { corosShown: true }).join('\n')
  const normalPlain = sunsetBannerLines('2026-10-31', { corosShown: false }).join('\n')
  const uncertainCoros = sunsetBannerLines('2026-10-31', { corosShown: true, syncUncertain: true }).join('\n')
  const uncertainPlain = sunsetBannerLines('2026-10-31', { corosShown: false, syncUncertain: true }).join('\n')
  ok(normalCoros.includes('照常同步到 10 月 31 日（當天仍可使用）') && normalPlain.includes('照常同步到 10 月 31 日（當天仍可使用）'), '橫幅（正常）：既有連線照常同步到結束日、當天仍可使用')
  ok(normalCoros.includes('「COROS 直連」') && normalCoros.includes('直連'), '橫幅（正常）：COROS 入口開放時才指名「COROS 直連」')
  ok(!normalPlain.includes('COROS') && !normalPlain.includes('直連'), '橫幅（正常）：COROS 入口沒開給這位會員時完全不提 COROS 與直連')
  // 同步說不準：不承諾照常同步（否則會與列內「同步中斷」警示自相矛盾）、不引導改用其他品牌——即使 corosShown 為真
  for (const [lab, t] of [['有 COROS 入口', uncertainCoros], ['沒有 COROS 入口', uncertainPlain]]) {
    ok(!t.includes('照常同步') && !t.includes('當天仍可使用'), `橫幅（同步說不準＋${lab}）：不承諾「照常同步到結束日」`)
    ok(!t.includes('COROS') && !t.includes('直連'), `橫幅（同步說不準＋${lab}）：不指名任何品牌的直連（corosShown 為真也不指名）`)
  }
  eq(uncertainCoros, uncertainPlain, '橫幅（同步說不準）：corosShown 不影響內容')
  eq(sunsetBannerLines('2026-10-31', { corosShown: true }), sunsetBannerLines('2026-10-31', { corosShown: true, syncUncertain: false }), '橫幅：syncUncertain 省略＝false')
  ok(sunsetBannerLines(undefined, { corosShown: false }).join('\n').includes('照常同步到 結束日（當天仍可使用）'), '橫幅：沒有日期時用「結束日」，句子仍通順')
  eq(sunsetBannerLines(undefined, { corosShown: true, syncUncertain: true }), sunsetBannerLines('2026-10-31', { corosShown: true, syncUncertain: true }), '橫幅（同步說不準）：內容不含日期（日期只在標題），有沒有日期都一樣')
}
// 「同步說不準」與「可指名 COROS」的判斷（純函式，保守品牌清單與逾期判斷都由呼叫端提供；這裡用一個假品牌代碼代替真的清單）
{
  const cautious = ['xbrand']
  const stale = (iso) => iso === 'stale'
  const row = (provider, last) => ({ provider, ...(last === undefined ? {} : { last_data_at: last }) })
  eq(sunsetRowSyncUncertain(row('polar', 'fresh'), cautious, stale), false, '同步說不準：一般品牌、資料新鮮 → 不說不準')
  eq(sunsetRowSyncUncertain(row('polar', undefined), cautious, stale), false, '同步說不準：一般品牌、last_data_at 缺席（後端查詢失敗）→ 不說不準（不顯示警示的列，橫幅也不改口）')
  eq(sunsetRowSyncUncertain(row('polar', 'stale'), cautious, stale), true, '同步說不準：一般品牌但最後資料逾期 → 說不準（與列內「同步中斷」警示一致）')
  eq(sunsetRowSyncUncertain(row('xbrand', 'fresh'), cautious, stale), true, '同步說不準：保守品牌即使資料新鮮 → 說不準')
  eq(sunsetRowSyncUncertain(row('xbrand', undefined), cautious, stale), true, '同步說不準：保守品牌且 last_data_at 缺席 → 說不準（不能因為欄位缺席就承諾照常同步）')
  eq(sunsetRowSyncUncertain(row('XBrand', 'fresh'), cautious, stale), true, '同步說不準：品牌比對不分大小寫')
  eq(sunsetRowSyncUncertain(row('polar', 'stale'), [], stale), true, '同步說不準：空的保守清單時仍看逾期')
  eq(sunsetRowSyncUncertain(row('xbrand', 'fresh'), [], stale), false, '同步說不準：空的保守清單、資料新鮮 → 不說不準')
  eq(sunsetCanNameCoros(true, ['gps', 'coros', 'polar'], cautious), true, '可指名 COROS：入口開放、沒有保守品牌的連線 → 可以')
  eq(sunsetCanNameCoros(true, ['gps', 'xbrand'], cautious), false, '可指名 COROS：入口開放但有保守品牌的連線 → 不指名')
  eq(sunsetCanNameCoros(false, ['gps'], cautious), false, '可指名 COROS：入口沒開給這位會員 → 不指名')
  eq(sunsetCanNameCoros(true, [], cautious), true, '可指名 COROS：沒有任何已連接來源 → 可以')
  eq(sunsetCanNameCoros(true, ['gps', 'xbrand'], []), true, '可指名 COROS：保守清單為空 → 不限制')
}
eq(sunsetPausedNotice('Strava', '2026-10-31', false), '新的 Strava 連接目前暫停開放（Strava 串接將於 10 月 31 日結束）。', '短提示（Strava，沒有 COROS 入口）')
eq(sunsetPausedNotice('Terra', '2026-10-31', false), '新的裝置連接目前暫停開放（Terra 串接將於 10 月 31 日結束）。', '短提示（Terra，沒有 COROS 入口）')
eq(sunsetPausedNotice('Terra', '2026-10-31', true), '新的裝置連接目前暫停開放（Terra 串接將於 10 月 31 日結束）。想同步手錶紀錄，請改用下方的「COROS 直連」。', '短提示（Terra，有 COROS 入口）')
eq(sunsetPausedNotice('Strava', undefined, false), '新的 Strava 連接目前暫停開放（Strava 串接即將結束）。', '短提示：沒有日期仍通順')
eq(sunsetDisconnectNote('Strava'), 'Strava 串接即將結束，中斷後無法再重新連接。', '中斷確認追加句（Strava）')
eq(sunsetDisconnectNote('Terra'), '這個品牌經 Terra 匯入的活動將一併刪除（賽事成績會重新計算）。Terra 串接即將結束，斷開後無法再重新連接。', '斷開確認追加句（Terra）：明講活動會一併刪除、賽事成績重算、無法再連接')
{
  const t = sunsetDisconnectNote('Terra')
  ok(t.includes('活動將一併刪除') && t.includes('賽事成績會重新計算') && t.includes('無法再重新連接'), '斷開確認追加句（Terra）：三件事都有（刪除活動、賽事重算、無法再連接）')
  // Strava 的確認文字（兩個狀態）原本就講了活動會刪除，追加句只補「無法再連接」，不重複
  ok(!sunsetDisconnectNote('Strava').includes('刪除'), '中斷確認追加句（Strava）：不重複已在原文案裡的「活動刪除」')
}
eq(sunsetSupportLine('2026-10-31'), 'Strava 串接將於 10 月 31 日結束，目前不再開放新的連接；已連接者可隨時到上方「運動數據」分頁按「中斷」。', '支援區說明（announce 版，取代「點 Connect with Strava」）')
eq(sunsetSupportLine(undefined), 'Strava 串接即將結束，目前不再開放新的連接；已連接者可隨時到上方「運動數據」分頁按「中斷」。', '支援區說明：沒有日期仍通順')
eq(SUNSET_RESULT_TEXT, 'Terra 與 Strava 串接即將結束，目前不再開放新的連接。', '?strava=sunset／?terra=sunset 固定訊息')
eq(SUNSET_RESULT, 'sunset', '導回結果值')
eq(SUNSET_ERROR_CODE, 'wearable_sunset', 'API 錯誤碼')

// ════════ ⑤ API 409 → 固定訊息 ════════
eq(sunsetErrorText(409, { code: 'wearable_sunset', date: '2026-10-31', error: 'x' }), 'Terra 與 Strava 串接將於 10 月 31 日結束，目前不再開放新的連接。', '409＋code → 固定訊息（含日期）')
eq(sunsetErrorText(409, { code: 'wearable_sunset' }), SUNSET_RESULT_TEXT, '409＋code 沒有日期 → 通用固定訊息')
eq(sunsetErrorText(409, { code: 'wearable_sunset', date: 'garbage' }), SUNSET_RESULT_TEXT, '409＋code 日期壞掉 → 通用固定訊息')
eq(SUNSET_ERROR_STATUS, 409, 'API 錯誤狀態碼')
// 狀態碼不能被每日資安報告算成登入失敗（401／403）或不存在（404），也不能是 5xx（會進「API 5xx 激增」告警）
ok(![401, 403, 404].includes(SUNSET_ERROR_STATUS) && SUNSET_ERROR_STATUS >= 400 && SUNSET_ERROR_STATUS < 500, 'API 錯誤狀態碼是 4xx 但不是 401／403／404（不會被算成登入失敗）')
for (const [st, body, lab] of [
  [409, { code: 'other' }, '409 但不是公告錯誤碼'], [409, { error: 'conflict' }, '409 沒有 code'], [409, null, '409 沒有 body'], [409, 'wearable_sunset', '409 body 是字串'],
  [403, { code: 'wearable_sunset' }, '403＋公告 code（後端已不用 403，不當成公告）'], [401, { code: 'wearable_sunset' }, '401＋公告 code'], [404, { code: 'wearable_sunset' }, '404＋公告 code'],
  [410, { code: 'wearable_sunset' }, '410＋公告 code（本版沒有 closed）'],
  [500, { code: 'wearable_sunset' }, '500＋公告 code（絕不當成公告）'], [503, { code: 'wearable_sunset' }, '503＋公告 code'], [200, { code: 'wearable_sunset' }, '200'],
  ['409', { code: 'wearable_sunset' }, 'status 是字串'], [undefined, { code: 'wearable_sunset' }, 'status 缺席'], [409, undefined, '409 body 缺席'],
]) {
  eq(sunsetErrorText(st, body), null, `API 錯誤：${lab} → null（走原本的錯誤處理）`)
}
{
  const evil = '<img src=x onerror=alert(1)>垃圾訊息'
  const t = sunsetErrorText(409, { code: 'wearable_sunset', date: '2026-10-31', error: evil, state: evil })
  ok(typeof t === 'string' && !t.includes('<') && !t.includes('垃圾訊息'), 'API 錯誤：絕不回顯伺服器的 error／state 字串')
  const t2 = sunsetErrorText(409, { code: 'wearable_sunset', date: evil })
  ok(typeof t2 === 'string' && !t2.includes('<'), 'API 錯誤：日期欄位是惡意字串也不回顯')
}

// ════════ ⑥ 文案守則 ════════
{
  const outputs = [
    SUNSET_RESULT_TEXT, sunsetDisconnectNote('Strava'), sunsetDisconnectNote('Terra'),
    sunsetSupportLine('2026-10-31'), sunsetSupportLine(undefined), sunsetSupportLine('bad'),
    sunsetErrorText(409, { code: 'wearable_sunset', date: '2026-10-31' }), sunsetErrorText(409, { code: 'wearable_sunset' }),
  ]
  for (const brand of ['Strava', 'Terra']) {
    for (const date of ['2026-10-31', '2026-01-05', undefined, 'bad']) {
      outputs.push(sunsetBannerTitle(brand, date))
      for (const ctx of [{ corosShown: true }, { corosShown: false }, { corosShown: true, syncUncertain: true }, { corosShown: false, syncUncertain: true }]) {
        outputs.push(...sunsetBannerLines(date, ctx))
      }
      outputs.push(sunsetPausedNotice(brand, date, true), sunsetPausedNotice(brand, date, false))
    }
  }
  const bad = outputs.filter((s) => BANNED.test(s))
  ok(bad.length === 0, `所有文案函式輸出（${outputs.length} 句）不含禁用詞${bad.length ? '：' + bad[0] : ''}`)
  ok(outputs.every((s) => typeof s === 'string' && s.length > 0 && !/undefined|null|NaN|\[object/.test(s)), '所有文案輸出都是通順字串（沒有 undefined／NaN／[object）')
  ok(outputs.every((s) => !s.includes('<') && !s.includes('&lt;')), '所有文案輸出都是純文字（沒有 HTML）')
  ok(outputs.every((s) => !/通知你|我們會|之後會再/.test(s)), '所有文案輸出都沒有未經擁有者核准的承諾（例如「之後會再通知你」）')
  const libSrc = read('../src/lib/wearableSunset.ts')
  const bannerSrc = read('../src/components/profile/SunsetBanner.tsx')
  // 去掉註解後（註解裡會用文字描述守則本身，例如「不暗示品牌背書」）整份原始碼的字串都不含禁用詞；
  // 第三方裝置品牌名與「跑團」連註解都不准出現（刻意不在這兩個新檔提到）。
  const stripComments = (s) => s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/\s\/\/.*$/gm, '')
  ok(!BANNED.test(stripComments(libSrc)), 'lib/wearableSunset.ts 的字串不含禁用詞')
  ok(!BANNED.test(stripComments(bannerSrc)), 'SunsetBanner.tsx 的字串不含禁用詞')
  ok(!/garmin|佳明|跑團/i.test(libSrc) && !/garmin|佳明|跑團/i.test(bannerSrc), '兩個新檔連註解都不含第三方裝置品牌名與「跑團」')
  ok(!/dangerouslySetInnerHTML|innerHTML/.test(libSrc + bannerSrc), '公告相關檔案沒有任何 HTML 注入路徑')
  ok(!/^\s*import\s/m.test(libSrc) && !/\benum\b/.test(libSrc.replace(/\/\/.*$/gm, '')), 'lib/wearableSunset.ts 沒有 import／enum（Node 原生 type-stripping 才載得進來）')
  ok(!/\bGarmin\b|佳明/i.test(read('../src/components/profile/SunsetBanner.tsx')), '橫幅元件不含第三方裝置品牌字樣')
}

// ════════ ⑦ 接線守門 ════════
{
  const ps = read('../src/components/ProfileScreen.tsx')
  const shell = read('../src/components/PhoneShell.tsx')
  const api = read('../src/lib/api.ts')
  const cat = read('../src/lib/appSettings.ts')

  ok(ps.includes("from '@/lib/wearableSunset'") && ps.includes("from './profile/SunsetBanner'"), 'ProfileScreen 載入公告 lib 與橫幅元件')
  ok(/const sunset = normalizeSunset\(strava\?\.sunset \?\? terra\?\.sunset\)/.test(ps), 'ProfileScreen：sunset 取自兩支 /status 回應（先到的），經 normalizeSunset')
  ok(ps.includes("const stravaView = sunsetCardView(sunset.state, strava?.connected === true)"), 'Strava 卡視圖由「公告狀態＋有無連線」決定')
  ok(ps.includes("const terraView = sunsetCardView(sunset.state, (terra?.connections?.length ?? 0) > 0)"), 'Terra 卡視圖由「公告狀態＋有無連線」決定')
  ok(/showTerraList = .*terraView === 'connected' \|\| terra\?\.enabled === true/.test(ps), 'Terra 已連接清單：announce 不看 terra.enabled，normal 維持需 enabled')

  // 四個 handler
  ok((ps.match(/sunsetErrorText\(e\?\.status, e\?\.body\)/g) || []).length === 2, '連接 Strava／連接 Terra 兩個 handler 都把 409＋code 轉成固定訊息')
  ok(ps.includes("sunsetDisconnectNote('Strava')") && ps.includes("sunsetDisconnectNote('Terra')"), '中斷 Strava／斷開 Terra 的確認視窗在公告期間追加一句')
  ok(/sunset\.state === 'announce' \? sunsetDisconnectNote\('Strava'\) : ''/.test(ps) && /sunset\.state === 'announce' \? sunsetDisconnectNote\('Terra'\) : ''/.test(ps), '追加句只在 announce 才出現（off 時確認文字不變）')
  ok(/if \(sunsetMsg\) loadStrava\(\)/.test(ps) && /if \(sunsetMsg\) loadTerra\(\)/.test(ps), '被擋下後重抓狀態，讓卡片切到公告畫面')
  ok(ps.includes('setStrava({ connected: false, enabled: strava?.enabled ?? true, sunset: strava?.sunset })'),
    '中斷 Strava 後本地 state 保留 sunset（否則 Terra 狀態沒載入時卡片會退回有連接鈕的畫面）')

  // 導回參數
  ok(/s === SUNSET_RESULT \? SUNSET_RESULT_TEXT/.test(ps), '?strava=sunset 導回有固定訊息分支')
  ok(/else if \(tr === SUNSET_RESULT\) \{\s*\n\s*setTerraMsg\(SUNSET_RESULT_TEXT\)/.test(ps), '?terra=sunset 導回有固定訊息分支（不顯示任何參數）')

  // 連接鈕只在 normal 視圖
  ok(/stravaView === 'paused' \? null[^\n]*: \(\s*\n\s*<button onClick=\{connectStrava\}/.test(ps), 'Strava「Connect with Strava」只在非 paused 視圖渲染')
  ok(/terraView !== 'normal' \? null : !terra\?\.enabled \? \(/.test(ps), 'Terra「連接裝置」鈕與「即將開放」只在 normal 視圖渲染（announce 時任何人都沒有）')
  ok(/\{terraView === 'normal' && \(\s*\n\s*<div[^>]*>\s*\n\s*連接即表示你同意透過整合商/.test(ps), 'Terra 跨境同意文案只在 normal 視圖')
  ok(/\{strava\?\.connected && stravaView === 'normal' && \(\s*\n\s*<div[^>]*>\s*\n\s*要更換 Strava 帳號/.test(ps), '「要更換 Strava 帳號」提示只在 normal 視圖（announce 後無法重新連接）')
  ok(ps.includes("<SunsetBanner title={sunsetBannerTitle('Strava', sunset.date)} lines={sunsetBannerLines(sunset.date, { corosShown: sunsetCorosHint })} />"), 'Strava 橫幅：用 sunsetCorosHint 決定要不要指名「COROS 直連」（Strava 連線沒有同步說不準的問題）')
  ok(ps.includes("<SunsetBanner title={sunsetBannerTitle('Terra', sunset.date)} lines={sunsetBannerLines(sunset.date, { corosShown: sunsetCorosHint, syncUncertain: terraSyncUncertain })} />"), 'Terra 橫幅：同時帶 sunsetCorosHint 與 terraSyncUncertain（不承諾照常同步、不引導改用其他品牌）')
  ok(ps.includes("sunsetPausedNotice('Strava', sunset.date, sunsetCorosHint)") && ps.includes("sunsetPausedNotice('Terra', sunset.date, sunsetCorosHint)"), '兩張卡沒有連線時顯示短提示（同一個 sunsetCorosHint）')
  // 橫幅一定直接掛在自己的 === 'connected' 條件下（不能被改成 {false && …} 或換成別的條件而靜默消失）；整份檔案只有這兩處
  ok(/\{stravaView === 'connected' && \(\s*\n\s*<SunsetBanner title=\{sunsetBannerTitle\('Strava', sunset\.date\)\}/.test(ps), "Strava 橫幅直接掛在 stravaView === 'connected' 之下")
  ok(/\{terraView === 'connected' && \(\s*\n\s*<SunsetBanner title=\{sunsetBannerTitle\('Terra', sunset\.date\)\}/.test(ps), "Terra 橫幅直接掛在 terraView === 'connected' 之下")
  eq((ps.match(/<SunsetBanner\b/g) || []).length, 2, 'ProfileScreen 只有兩處 <SunsetBanner（Strava 卡、Terra 卡各一）')
  // 「同步說不準」與「可指名 COROS」的判斷接線（判斷本身是 lib 的純函式，上面 ④ 已用真的函式驗過行為）：保守品牌清單
  // 只有一處定義，兩個判斷都用它；列內「同步中斷」警示用的 terraDataStale 同時是「逾 48 小時」的判斷來源。
  ok(/const SUNSET_CAUTIOUS_BRANDS: readonly DataSource\[\] = \['garmin'\]/.test(ps), "保守品牌清單只有一處定義（['garmin']）")
  ok(ps.includes('const terraSyncUncertain = (terra?.connections ?? []).some((c) => sunsetRowSyncUncertain(c, SUNSET_CAUTIOUS_BRANDS, terraDataStale))'), 'terraSyncUncertain：任何一條 Terra 連線列說不準就保守（保守品牌清單＋與列內警示同一個 terraDataStale）')
  ok(ps.includes('const sunsetCorosHint = sunsetCanNameCoros(corosEntryShown, connectedSources, SUNSET_CAUTIOUS_BRANDS)'), 'sunsetCorosHint：入口開放且（Dashboard 彙整的）已連接來源沒有保守品牌才指名「COROS 直連」')
  ok(/const terraDataStaleAfterMs = 48 \* 60 \* 60 \* 1000/.test(ps) && /function terraDataStale\(iso\?: string\): boolean \{/.test(ps), '逾期判斷 terraDataStale 仍是 48 小時（與後端 wearableStaleAfter 同門檻）')
  ok(!/dangerouslySetInnerHTML/.test(ps), 'ProfileScreen 沒有任何 dangerouslySetInnerHTML')
  // 連接鈕的呼叫點數量（只算程式碼、不算註解）：函式宣告一次＋唯一的按鈕一次。再多一個 onClick={connectStrava}／connectTerra()
  // 呼叫點，就可能在 announce 期間繞過「只在 normal 視圖渲染」的條件，所以多了要失敗、逼人檢視。
  {
    const code = ps.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/\s\/\/.*$/gm, '')
    eq((ps.match(/onClick=\{connectStrava\}/g) || []).length, 1, 'connectStrava 只有一個按鈕（onClick）呼叫點')
    eq((ps.match(/onClick=\{connectTerra\}/g) || []).length, 1, 'connectTerra 只有一個按鈕（onClick）呼叫點')
    eq((code.match(/\bconnectStrava\b/g) || []).length, 2, 'connectStrava 在程式碼中只出現兩次（函式宣告＋那一個按鈕）：沒有任何程式化呼叫')
    eq((code.match(/\bconnectTerra\b/g) || []).length, 2, 'connectTerra 在程式碼中只出現兩次（函式宣告＋那一個按鈕）：沒有任何程式化呼叫')
    eq((code.match(/stravaConnectUrl|terraConnectUrl/g) || []).length, 2, '發起連線的 API 呼叫（stravaConnectUrl／terraConnectUrl）在 ProfileScreen 各只有一處')
  }
  ok(/\{sunset\.state === 'announce'\s*\n\s*\? sunsetSupportLine\(sunset\.date\)[^\n]*\n\s*: '連接 Strava：到上方「運動數據」分頁點官方「Connect with Strava」即可；要中斷請按「中斷」。我們僅匯入你連接之後的活動，並可隨時中斷。'\}/.test(ps),
    '支援區的「點 Connect with Strava」說明：announce 時換成 sunsetSupportLine，off 時逐字維持原句')

  // off 狀態既有字串一個都沒被改（逐字）
  for (const s of [
    '⚠ Strava 官方限制每個 App 只能連接 10 位跑者，目前名額已滿、升級審核中。',
    '使用 COROS 的跑者請改用下方的「COROS 直連」；',
    '請改用「連接你常用的跑步裝置」。',
    'aria-label="Connect with Strava"', '/strava/btn_strava_connect_with_orange_x2.png',
    '連接後自動同步跑步活動，用於個人數據（個人任務、自主訓練、稱號成就、個人里程）；依 Strava 平台規範，Strava 數據不計入活動排名或里程競賽統計——要讓裝置紀錄進賽事，請用下方',
    '（Strava 整合尚未由管理者設定）',
    '依 Strava 平台規範，Strava 數據僅用於你的個人數據，不會用於活動排名／里程競賽統計（此為 Strava 的限制，與賽事設定無關）。',
    '要更換 Strava 帳號？請先', '，再「中斷」後重新連接（連接的是你瀏覽器當下登入的 Strava 帳號）。',
    '⌚ 連接你常用的跑步裝置', 'Strava 名額已滿也沒關係——很快就能連接你的 ', '正在開通中', '即將開放', "{terraBusy ? '連接中…' : '連接裝置'}",
    '連接即表示你同意透過整合商 <b>Terra</b> 取得你的跑步活動資料（跨境處理）',
    '裝置同步的跑步／走路（跑步機、越野跑、運動場跑步、徒步都算）通常會自動匯入；若沒進來，按「匯入數據」會向 Terra 抓近 30 天的紀錄（只計入連接之後的活動）。',
    '中斷 Strava 連接？已同步的 Strava 活動將一併刪除；你已獲得的 EXP/DP 等獎勵不受影響。',
    '之後 ${brand} 裝置同步的跑步將不再自動匯入；已獲得的 EXP/DP 等獎勵不受影響。',
    "setStravaMsg(sunsetMsg ?? (e?.message || '無法連接 Strava'))",
    "'裝置連接功能尚未開放，請稍後再試'", '連接未完成，請再試一次',
    "'Strava 連接失敗，請再試一次'",
  ]) {
    ok(ps.includes(s), `off 既有字串仍在：${s.slice(0, 48)}${s.length > 48 ? '…' : ''}`)
  }

  // PhoneShell：?strava= 導回要切到運動數據分頁（Strava 卡與結果訊息都在該分頁內，停在預設的「個人資料」分頁就看不到訊息）
  ok(/if \(params\.has\('strava'\)\) \{\s*\n\s*setShowProfile\(true\)\s*\n\s*setProfileInitialTab\('sports'\)/.test(shell), 'PhoneShell：?strava= 導回會開個人頁並切到「運動數據」分頁')
  for (const p of ['terra', 'coros_mcp', 'garmin']) {
    ok(new RegExp(`if \\(params\\.has\\('${p}'\\)\\) \\{\\s*\\n\\s*setShowProfile\\(true\\)\\s*\\n\\s*setProfileInitialTab\\('sports'\\)`).test(shell), `PhoneShell：?${p}= 仍切到運動數據分頁（沒被動到）`)
  }

  // api.ts 型別
  ok(/export interface StravaStatus \{[^}]*sunset\?: SunsetInfo/s.test(api), 'api.ts：StravaStatus 有 sunset?')
  ok(/export interface TerraStatus \{[^}]*sunset\?: SunsetInfo/s.test(api), 'api.ts：TerraStatus 有 sunset?')
  ok(api.includes("import type { SunsetInfo } from './wearableSunset'"), 'api.ts：以 type-only import 引用 SunsetInfo')

  // 後台設定型錄
  const specState = cat.match(/key: 'wearable_sunset_state'[^\n]*\n/)?.[0] ?? ''
  const specDate = cat.match(/key: 'wearable_sunset_date'[^\n]*\n/)?.[0] ?? ''
  ok(/type: 'select'/.test(specState) && /def: 'off'/.test(specState), '設定型錄：wearable_sunset_state 是 select、預設 off')
  ok(/type: 'text'/.test(specDate) && !/type: 'number'/.test(specDate) && /def: '2026-10-31'/.test(specDate), "設定型錄：wearable_sunset_date 一定是 type:'text'（number 會被存成 NaN）、預設 2026-10-31")
  ok(/group: 'Terra／Strava 關閉'/.test(specState) && /group: 'Terra／Strava 關閉'/.test(specDate), '設定型錄：兩個鍵同一個群組')
  ok(/\{ value: 'off', label: [^}]+\},\s*\n\s*\{ value: 'announce', label: [^}]+\},/.test(cat.slice(cat.indexOf("key: 'wearable_sunset_state'"))), '設定型錄：選項只有 off／announce（本版沒有 closed）')
}

// ════════ ⑦b Terra 導回參數的顯示淨化（?terra=…&provider=…&reason=… 任何人都能偽造，畫面不得原樣顯示）════════
{
  const ps = read('../src/components/ProfileScreen.tsx')

  // ProfileScreen 是 TSX（有 import、JSX），Node 無法直接載入：把那幾個純函式／常數的原始碼從檔案裡擷取出來、去掉型別後實際執行。
  const pick = (re, label) => {
    const m = ps.match(re)
    ok(!!m, `ProfileScreen：找得到 ${label}`)
    return m ? m[0] : ''
  }
  const src = [
    pick(/const TERRA_BRAND_LABEL: Record<string, string> = \{[\s\S]*?\n\}\n/, 'TERRA_BRAND_LABEL（已知品牌對照表）'),
    pick(/function terraBrandName\(provider: string\): string \{[\s\S]*?\n\}\n/, 'terraBrandName'),
    pick(/const TERRA_REDIRECT_REASON_RE = [^\n]+\n/, 'TERRA_REDIRECT_REASON_RE'),
    pick(/function terraRedirectReason\(raw: string\): string \{[\s\S]*?\n\}\n/, 'terraRedirectReason'),
    pick(/function terraRedirectBrand\(raw: string\): string \{[\s\S]*?\n\}\n/, 'terraRedirectBrand'),
  ].join('\n')
  const { stripTypeScriptTypes } = await import('node:module')
  const origEmitWarning = process.emitWarning
  process.emitWarning = (w, ...rest) => (String(w).includes('stripTypeScriptTypes') ? undefined : origEmitWarning.call(process, w, ...rest)) // 擷取用的實驗性 API 會印警告，這裡不需要
  let fns
  try {
    fns = new Function(`${stripTypeScriptTypes(src)}\nreturn { terraBrandName, terraRedirectReason, terraRedirectBrand }`)()
  } catch (e) {
    fail++; console.log(`FAIL 無法執行從 ProfileScreen 擷取的函式：${e}`)
    fns = { terraBrandName: () => '', terraRedirectReason: () => '\0', terraRedirectBrand: () => '\0' }
  } finally {
    process.emitWarning = origEmitWarning
  }
  const { terraBrandName, terraRedirectReason, terraRedirectBrand } = fns

  // reason：只有長得像代碼（/^[\w:.-]{1,60}$/，與 COROS 導回同一個樣式）才顯示，否則省略
  for (const r of ['provider_unavailable', 'already_direct', 'user_cancelled', 'access.denied', 'a:b-c.d', 'x', 'A'.repeat(60)]) {
    eq(terraRedirectReason(r), r, `reason 長得像代碼 → 原樣顯示：${r.slice(0, 24)}`)
  }
  for (const r of [
    '', ' ', 'a b', 'x'.repeat(61), '<img src=x onerror=alert(1)>', '<script>alert(1)</script>', 'https://evil.example/login',
    '請改到 evil.example 重新登入', '（偽造）', 'a\nb', 'abc\n', '\nabc', 'a/b', 'a%20b', 'a=1&b=2', "x'; drop", '中文', 'ａｂｃ',
  ]) {
    eq(terraRedirectReason(r), '', `reason 不像代碼 → 省略：${JSON.stringify(r).slice(0, 40)}`)
  }
  // 與 COROS 導回使用同一個樣式（兩處不得各改各的）
  eq(ps.match(/const TERRA_REDIRECT_REASON_RE = (\/.*\/)\n/)?.[1], '/^[\\w:.-]{1,60}$/', 'Terra 導回 reason 的樣式 /^[\\w:.-]{1,60}$/')
  ok(ps.includes('/^[\\w:.-]{1,60}$/.test(cmReason)'), 'COROS 導回仍用同一個樣式（兩處一致）')

  // provider：只透過已知品牌對照表轉成名稱；未知值（含原型鏈上的名稱）一律用通用字樣「裝置」，絕不顯示原始參數
  for (const [raw, label] of Object.entries({ polar: 'Polar', POLAR: 'Polar', Garmin: 'Garmin', suunto: 'Suunto', wahoo: 'Wahoo', coros: 'COROS', Coros: 'COROS' })) {
    eq(terraRedirectBrand(raw), label, `品牌 ${raw} → ${label}`)
    eq(terraRedirectBrand(raw), terraBrandName(raw), `品牌 ${raw}：與既有 terraBrandName 同名（合法導回的訊息不變）`)
  }
  for (const raw of [
    '', 'fitbit', 'zzz', '<b>x</b>', '<img src=x onerror=alert(1)>', 'https://evil.example', 'polar ', ' polar', 'po lar', '請改到 evil.example 重新登入',
    'constructor', '__proto__', 'toString', 'hasOwnProperty', 'valueOf', 'isPrototypeOf', 'x'.repeat(500),
  ]) {
    const label = terraRedirectBrand(raw)
    eq(label, '裝置', `品牌 ${JSON.stringify(raw).slice(0, 36)}：未知值 → 通用字樣「裝置」（不顯示原始參數）`)
    ok(typeof label === 'string' && (raw === '' || !label.includes(raw)), `品牌 ${JSON.stringify(raw).slice(0, 36)}：回傳值不含原始參數`)
  }

  // 接線：導回區塊裡 reason／provider 一律經上面兩個函式，沒有任何地方原樣插值；sunset 分支維持原樣（⑦ 已逐字檢查）
  const trStart = ps.indexOf("const tr = sp.get('terra')")
  const trEnd = ps.indexOf("sp.delete('terra'); sp.delete('provider'); sp.delete('reason')", trStart)
  ok(trStart > 0 && trEnd > trStart, 'ProfileScreen：找得到 ?terra= 導回處理區塊')
  const trBlock = trStart > 0 && trEnd > trStart ? ps.slice(trStart, trEnd) : ''
  ok(trBlock.includes("const reason = terraRedirectReason(sp.get('reason') || '')"), '?terra= 導回：reason 先經 terraRedirectReason（不像代碼就省略）')
  ok(!/sp\.get\('reason'\)/.test(trBlock.replace("terraRedirectReason(sp.get('reason') || '')", '')), '?terra= 導回：區塊內沒有其他地方直接取用 reason 參數')
  ok(trBlock.includes('已連接 ${terraRedirectBrand(provider)}，之後裝置同步的跑步會自動匯入'), '?terra=connected：品牌只經 terraRedirectBrand（已知對照表＋通用字樣）')
  ok(!trBlock.includes('terraBrandName('), '?terra= 導回區塊不使用 terraBrandName（它對未知值會首字大寫後原樣顯示參數）')
  ok(!/\$\{provider\}/.test(trBlock), '?terra= 導回區塊沒有 ${provider} 原樣插值')
  ok(trBlock.includes('連接未完成，請再試一次${reason ? `（${reason}）` : \'\'}'), '?terra=failed|error：訊息只帶經過淨化的 reason（沒有就不帶）')
  ok(/else if \(tr === SUNSET_RESULT\) \{\s*\n\s*setTerraMsg\(SUNSET_RESULT_TEXT\)/.test(trBlock), '?terra=sunset 分支維持原樣（固定訊息，不顯示任何參數）')
  ok(/s === SUNSET_RESULT \? SUNSET_RESULT_TEXT/.test(ps), '?strava=sunset 分支維持原樣')
}

// ════════ ⑦c 後台寫入設定（PUT /admin/app-settings/{key}）：所有呼叫點都送 {value: <字串>}，公告狀態下拉絕不送空字串 ════════
{
  const api = read('../src/lib/api.ts')
  const sys = read('../src/app/admin/system/page.tsx')
  const cat = read('../src/lib/appSettings.ts')
  const stripComments = (s) => s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '').replace(/\s\/\/.*$/gm, '')

  // 寫入入口只有 lib/api.ts 的 adminAppSettingsApi.set：src 底下沒有任何其他檔案（程式碼，不含註解）組出 /admin/app-settings/{key} 的網址
  const walk = (dir) => fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(new URL(`${e.name}/`, dir)) : [new URL(e.name, dir)]))
  const hits = walk(here('../src/'))
    .filter((u) => /\.(ts|tsx)$/.test(u.pathname))
    .filter((u) => /`\/admin\/app-settings\//.test(stripComments(fs.readFileSync(u, 'utf8'))))
    .map((u) => u.pathname.split('/src/')[1])
  eq(hits, ['lib/api.ts'], '寫入 /admin/app-settings/{key} 的程式碼只在 lib/api.ts（沒有繞過它的呼叫點）')
  ok(/set: \(token: string, key: string, value: string\) => request<[^\n]*`\/admin\/app-settings\/\$\{key\}`, \{ method: 'PUT', headers: withAuth\(token\), body: JSON\.stringify\(\{ value \}\) \}\)/.test(api),
    "adminAppSettingsApi.set：value 型別是 string，body 恆為 JSON.stringify({ value })（所有呼叫點因此都送 {value: <字串>}）")
  // 呼叫點（程式碼）：system 頁 4 處、interstitial 頁 1 處；其餘頁面只 list
  const callers = walk(here('../src/'))
    .filter((u) => /\.(ts|tsx)$/.test(u.pathname))
    .map((u) => [u.pathname.split('/src/')[1], (stripComments(fs.readFileSync(u, 'utf8')).match(/adminAppSettingsApi\.set\(/g) || []).length])
    .filter(([, n]) => n > 0)
    .sort()
  eq(callers, [['app/admin/interstitial/page.tsx', 1], ['app/admin/system/page.tsx', 4]], 'adminAppSettingsApi.set 的呼叫點：system 頁 4 處（退費政策、通用儲存、favicon 上傳／清除）、蓋板廣告頁 1 處')
  // 通用儲存：select 型一律 raw || spec.def（空值會換成預設，所以公告狀態下拉絕不會送出空字串）；text 型才允許送空字串（清除）
  ok(/\} else \{\s*\n\s*val = raw \|\| spec\.def\s*\n\s*editVal = val/.test(sys), '後台通用儲存：select 型送出的值是 raw || spec.def（空值換成預設）')
  const specState = cat.match(/key: 'wearable_sunset_state'[^\n]*\n/)?.[0] ?? ''
  ok(/type: 'select'/.test(specState) && /def: 'off'/.test(specState), '公告狀態是 select 型、預設值 off（非空）→ 儲存時值恆為 off 或 announce')
  const stateOptions = cat.slice(cat.indexOf("key: 'wearable_sunset_state'")).match(/options: \[([\s\S]*?)\],\s*\n\s*\}/)?.[1] ?? ''
  eq([...stateOptions.matchAll(/value: '([^']*)'/g)].map((m) => m[1]), ['off', 'announce'], '公告狀態下拉的選項值只有 off／announce（沒有空字串）')
  // 與後端寫入驗證器對拍：後端只收 off／announce（空字串會被擋）
  const goSettings2 = tryRead('../../../services/api/internal/appsettings/appsettings.go')
  if (!goSettings2) skip('後端 appsettings 原始碼不在——略過狀態驗證器對拍')
  else ok(/func isSunsetState\(v string\) bool \{ return v == "off" \|\| v == sunsetAnnounce \}/.test(goSettings2), '對拍：後端 wearable_sunset_state 的寫入驗證器只收 off／announce（不收空字串）')
}

// ════════ ⑧ 與後端 Go 原始碼對拍 ════════
{
  const goPkg = tryRead('../../../services/api/internal/integration/wearablesunset/wearablesunset.go')
  const goSettings = tryRead('../../../services/api/internal/appsettings/appsettings.go')
  const goGate = tryRead('../../../services/api/internal/integration/wearable_sunset.go')
  const goTerra = tryRead('../../../services/api/internal/integration/terra.go')
  const goStrava = tryRead('../../../services/api/internal/integration/strava.go')
  if (!goPkg) {
    skip('後端 wearablesunset 原始碼不在（單獨部署前端？）——略過對拍')
  } else {
    eq(goPkg.match(/ErrorCode\s*=\s*"([^"]+)"/)?.[1], SUNSET_ERROR_CODE, '對拍：後端 ErrorCode＝前端 SUNSET_ERROR_CODE')
    eq(goPkg.match(/ResultValue\s*=\s*"([^"]+)"/)?.[1], SUNSET_RESULT, '對拍：後端 ResultValue＝前端 SUNSET_RESULT')
    // 被擋下的 API 狀態碼：後端 RefusalStatus（http.StatusXxx 名稱）換算成數字，必須等於前端 SUNSET_ERROR_STATUS
    {
      const name = goPkg.match(/RefusalStatus\s*=\s*http\.Status(\w+)/)?.[1]
      eq(({ Conflict: 409 })[name], SUNSET_ERROR_STATUS, `對拍：後端 RefusalStatus（http.Status${name}）＝前端 SUNSET_ERROR_STATUS（${SUNSET_ERROR_STATUS}）`)
    }
    ok(/json:"state"/.test(goPkg) && /json:"date,omitempty"/.test(goPkg), '對拍：後端 sunset 物件欄位是 state／date（date 只在 announce 帶出）')
    ok(/StateOff\s*=\s*"off"/.test(goPkg) && /StateAnnounce\s*=\s*"announce"/.test(goPkg), '對拍：後端狀態值 off／announce')
    // 錯誤句子：後端 Message() 與前端 sunsetErrorText 由同一組片段組成
    ok(goPkg.includes('"Terra 與 Strava 串接將於 " + l + "結束，目前不再開放新的連接。"'), '對拍：後端錯誤句子（含日期）與前端一致')
    ok(goPkg.includes('"Terra 與 Strava 串接即將結束，目前不再開放新的連接。"') && SUNSET_RESULT_TEXT === 'Terra 與 Strava 串接即將結束，目前不再開放新的連接。', '對拍：後端錯誤句子（無日期）＝前端固定訊息')
    ok(!BANNED.test(goPkg.replace(/^\s*\/\/.*$/gm, '')), '對拍：後端公告套件的字串不含禁用詞')
  }
  if (!goSettings) {
    skip('後端 appsettings 原始碼不在——略過設定鍵對拍')
  } else {
    ok(/WearableSunsetStateKey\s*=\s*"wearable_sunset_state"/.test(goSettings) && /WearableSunsetDateKey\s*=\s*"wearable_sunset_date"/.test(goSettings), '對拍：後端設定鍵名稱與後台型錄相同')
    eq(goSettings.match(/WearableSunsetDefaultDate\s*=\s*"([^"]+)"/)?.[1], '2026-10-31', '對拍：後端預設結束日＝型錄 def')
    ok(/WearableSunsetStateKey:\s*isSunsetState/.test(goSettings) && /WearableSunsetDateKey:\s*isDateYYYYMMDD/.test(goSettings), '對拍：兩個鍵都登記在後端 specs（含驗證器）')
    ok(!/publicKeys = map\[string\]bool\{[^}]*wearable_sunset/.test(goSettings), '對拍：兩個鍵不在 publicKeys')
  }
  if (!goGate || !goTerra || !goStrava) {
    skip('後端 integration 原始碼不在——略過守門對拍')
  } else {
    ok(/respondJSON\(w, wearablesunset\.RefusalStatus, info\.ErrorBody\(\)\)/.test(goGate), '對拍：被擋下的 API 一律回 RefusalStatus（409）＋ ErrorBody（error/code/state/date）')
    ok(/redirectFront\(wearablesunset\.ResultValue, ""\)/.test(goTerra), '對拍：Terra callback 導回 ?terra=sunset')
    ok(/redirectFront\(wearablesunset\.ResultValue\)/.test(goStrava), '對拍：Strava callback 導回 ?strava=sunset')
    ok(/"sunset": h\.sunsetInfo\(r\.Context\(\)\)/.test(goTerra) && (goStrava.match(/"sunset": sunset/g) || []).length === 2, '對拍：Terra／Strava 的 /status 回應都帶 sunset 欄位')
  }
}

console.log(`\n${pass} passed, ${fail} failed, ${skipped} skipped`)
process.exit(fail ? 1 : 0)
