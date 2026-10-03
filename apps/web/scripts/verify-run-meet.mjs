// 驗證 apps/web/src/lib/runMeet.ts（直接 import 實際檔案，非重寫邏輯；Node 24 原生 TS type-stripping）
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-run-meet.mjs`（路徑以本檔為基準，不寫死）
//
// 涵蓋：台北時區換算、距離分級文字、配額文案、人數顯示、CTA 狀態矩陣（規格 5.7 逐格）。
// 這幾組規則錯了不會有畫面報錯，只會默默顯示錯的按鈕/時間，所以一定要有可重跑的驗證。
const modUrl = new URL('../src/lib/runMeet.ts', import.meta.url).href
const {
  taipeiParts, taipeiLocalToISO, isoToTaipeiLocalInput, fmtMeetAt, fmtMeetAtConfirm, fmtMeetRange,
  meetCountdown, meetPhase, phaseCountdown,
  isFetchPending, isAbsorbing, shouldShowError,
  distanceBandLabel, runMeetLocationIcon, runMeetLocationText, coverFallbackGlyph, createBtnText, resetDayText, remainingText,
  memberCountText, memberPct, confirmSubText, runMeetCta, isDeviceTaipei, createGate,
  reactionPills, sortReactionCounts, optimisticReactionUpdate, mentionPrefix, replyTargetId,
  hasMoreCursor, showViewAllComments, showReplyToggle, replyToggleLabel, viewAllCommentsLabel,
  mergeCommentPages, insertTopComment, insertReply, updateCommentReaction, markCommentDeleted,
  runMeetLiveCta, activeHrefMeetId, liveClockOffsetMs, runMeetLiveSmallPrint, LIVE_SMALLPRINT, LIVE_SMALLPRINT_PRESENCE,
  LIVE_WINDOW_BADGE, LIVE_BTN_READY_LABEL, LIVE_BTN_RESUME_LABEL, runMeetLiveConsentKey, readLiveConsent, writeLiveConsent,
  LIVE_CONSENT_SESSION_MAX_MS, runMeetLiveConsentSessionKey, markLiveConsentSession, readLiveConsentSession,
} = await import(modUrl)

let pass = 0, fail = 0
function eq(actual, expected, label) {
  const a = JSON.stringify(actual), e = JSON.stringify(expected)
  if (a === e) { pass++; console.log(`PASS ${label}`) }
  else { fail++; console.log(`FAIL ${label}\n  actual:   ${a}\n  expected: ${e}`) }
}

const MEET_ISO = '2026-08-30T22:00:00.000Z' // = 2026-08-31（一）06:00 台北
const RESETS = '2026-09-01T00:00:00+08:00'

// ── 台北時區 ────────────────────────────────────────────────────
eq(taipeiParts(MEET_ISO), { y: 2026, m: 8, d: 31, hh: 6, mm: 0, wd: '一' }, 'taipeiParts 以台北時區拆解')
eq(taipeiLocalToISO('2026-08-31T06:00'), MEET_ISO, 'datetime-local 牆上時間當台北時間 → ISO')
eq(isoToTaipeiLocalInput(MEET_ISO), '2026-08-31T06:00', 'ISO → datetime-local 值（台北牆上時間）')
eq(taipeiLocalToISO(''), '', '空字串 → 空字串（不丟例外）')
eq(isoToTaipeiLocalInput(null), '', 'null → 空字串')
// 跨日邊界：台北 00:30 的前一天 UTC 16:30，日期不可以退回 8/30
eq(taipeiParts('2026-08-30T16:30:00.000Z'), { y: 2026, m: 8, d: 31, hh: 0, mm: 30, wd: '一' }, '台北午夜後仍屬同一天（UTC 前一日 16:30）')

// fmtMeetAt（2026-08-31 使用者回報「分不清楚是上午 4:30 還是下午 4:30」→ 12 小時制＋六個時段用語）
eq(fmtMeetAt(MEET_ISO, new Date('2026-08-29T10:00:00+08:00')), '8/31（一）上午 6:00', 'fmtMeetAt 非當日 → 月/日（週）上午/下午 h:mm')
eq(fmtMeetAt(MEET_ISO, new Date('2026-08-31T05:00:00+08:00')), '今天 上午 6:00', 'fmtMeetAt 當日（台北）→ 今天 上午 6:00')
// 六個時段（使用者定案，左閉右開）：[0,4)凌晨 [4,6)清晨 [6,12)上午 [12,17)下午 [17,19)傍晚 [19,24)晚上
// 每個分界的「起點」與「前一分鐘」都測，日後若整段位移會立刻被抓到。
// ⚠️ 小時是「>12 才減 12」：0 點保留 0（凌晨 0:30，不是上午 12:30）、中午保留 12（下午 12:00）。
const AT = (utc) => fmtMeetAt(utc, new Date('2026-08-29T10:00:00+08:00'))
eq(AT('2026-08-29T16:00:00.000Z'), '8/30（日）凌晨 0:00', '00:00 → 凌晨 0:00（不是「上午 12:00」）')
eq(AT('2026-08-29T16:30:00.000Z'), '8/30（日）凌晨 0:30', '00:30 → 凌晨 0:30')
eq(AT('2026-08-29T19:59:00.000Z'), '8/30（日）凌晨 3:59', '03:59 → 仍是凌晨')
eq(AT('2026-08-29T20:00:00.000Z'), '8/30（日）清晨 4:00', '04:00 → 清晨（凌晨/清晨分界）')
eq(AT('2026-08-29T21:30:00.000Z'), '8/30（日）清晨 5:30', '05:30 → 清晨')
eq(AT('2026-08-29T22:00:00.000Z'), '8/30（日）上午 6:00', '06:00 → 上午（清晨/上午分界）')
eq(AT('2026-08-30T03:59:00.000Z'), '8/30（日）上午 11:59', '11:59 → 仍是上午')
eq(AT('2026-08-30T04:00:00.000Z'), '8/30（日）下午 12:00', '12:00 → 下午 12:00（中午不寫成上午）')
eq(AT('2026-08-30T08:30:00.000Z'), '8/30（日）下午 4:30', '16:30 → 下午 4:30')
eq(AT('2026-08-30T09:00:00.000Z'), '8/30（日）傍晚 5:00', '17:00 → 傍晚（下午/傍晚分界）')
eq(AT('2026-08-30T10:30:00.000Z'), '8/30（日）傍晚 6:30', '18:30 → 傍晚')
eq(AT('2026-08-30T11:00:00.000Z'), '8/30（日）晚上 7:00', '19:00 → 晚上（傍晚/晚上分界）')
eq(AT('2026-08-30T15:00:00.000Z'), '8/30（日）晚上 11:00', '23:00 → 晚上 11:00')
eq(fmtMeetAtConfirm(MEET_ISO), '將於 8/31（一）06:00（台北時間）開跑', '表單反算確認行（維持 24 小時制，這行不受本次變更影響）')

// ── 團練時間區間顯示（fmtMeetRange，migration 168 ends_at）───────────────
// MEET_ISO = 2026-08-31（一）06:00 台北；ENDS_ISO 是同一天 07:30 台北，全站顯示「時間」的唯一入口。
const ENDS_ISO = '2026-08-30T23:30:00.000Z' // = 2026-08-31（一）07:30 台北，跟 MEET_ISO 同一天
// 同日區間：今天／非今天兩種日期前綴都要蓋到（前綴判斷沿用 fmtMeetAt 的邏輯，時間部分改 24 小時制區間）。
eq(fmtMeetRange(MEET_ISO, ENDS_ISO, new Date('2026-08-31T05:00:00+08:00')), '今天 06:00–07:30', 'fmtMeetRange｜同日區間・今天')
eq(fmtMeetRange(MEET_ISO, ENDS_ISO, new Date('2026-08-29T10:00:00+08:00')), '8/31（一）06:00–07:30', 'fmtMeetRange｜同日區間・非今天（月/日（週））')
// 跨日（防禦性：後端已強制 ends_at 與 meet_at 同一台北日曆日，理論上不會發生，但驗證這支不會
// 因此顯示出第二個日期——日期前綴恆取自 meet_at，結束時間一律只印 HH:mm）。
eq(
  fmtMeetRange(MEET_ISO, '2026-08-31T17:00:00.000Z', new Date('2026-08-29T10:00:00+08:00')),
  '8/31（一）06:00–01:00',
  'fmtMeetRange｜ends_at 落在隔天（防禦性）：仍只用 meet_at 的日期前綴，不冒出第二個日期',
)
// null／undefined 退回 fmtMeetAt（migration 168 上線前的舊資料，只顯示開始時間，維持既有樣式）。
eq(fmtMeetRange(MEET_ISO, null, new Date('2026-08-31T05:00:00+08:00')), fmtMeetAt(MEET_ISO, new Date('2026-08-31T05:00:00+08:00')), 'fmtMeetRange｜ends_at=null → 退回 fmtMeetAt（今天）')
eq(fmtMeetRange(MEET_ISO, undefined, new Date('2026-08-29T10:00:00+08:00')), fmtMeetAt(MEET_ISO, new Date('2026-08-29T10:00:00+08:00')), 'fmtMeetRange｜ends_at=undefined → 退回 fmtMeetAt（非今天）')
// 23:00–23:59 邊界：兩個時間都在同一天最後一小時，不該冒出任何額外的日期標籤（例如誤植成隔天）。
eq(
  fmtMeetRange('2026-08-31T15:00:00.000Z', '2026-08-31T15:59:00.000Z', new Date('2026-08-29T10:00:00+08:00')),
  '8/31（一）23:00–23:59',
  'fmtMeetRange｜23:00–23:59 同日邊界，不冒出多餘日期標籤',
)

// ── 相對時間 ────────────────────────────────────────────────────
eq(meetCountdown(MEET_ISO, new Date('2026-08-29T06:00:00+08:00')), { text: '還有 2 天', urgent: false, ended: false }, '48 小時 → 還有 2 天')
eq(meetCountdown(MEET_ISO, new Date('2026-08-31T03:00:00+08:00')), { text: '還有 3 小時', urgent: true, ended: false }, '3 小時 → urgent（6 小時內橘色）')
eq(meetCountdown(MEET_ISO, new Date('2026-08-30T22:00:00+08:00')), { text: '還有 8 小時', urgent: false, ended: false }, '8 小時 → 不 urgent')
eq(meetCountdown(MEET_ISO, new Date('2026-08-31T05:35:00+08:00')), { text: '還有 25 分鐘', urgent: true, ended: false }, '25 分鐘 → 分鐘級')
eq(meetCountdown(MEET_ISO, new Date('2026-08-31T06:00:01+08:00')), { text: '已結束', urgent: false, ended: true }, '時間已過 → 已結束')

// ── 生命週期三態（phase-2，2026-09-04 使用者定案）────────────────────────
// MEET_ISO = 06:00 台北開始，ENDS_ISO = 07:30 台北結束（同一天，見上面 fmtMeetRange 那節）。
// anchor＝effectiveEnd = COALESCE(ends_at, meet_at)；both 邊界都是「>= 才算」（左閉右開），
// 跟後端 is_ended／phase 的判斷式必須算出同一個答案。
eq(meetPhase(MEET_ISO, ENDS_ISO, new Date('2026-08-31T05:59:00+08:00')), 'upcoming', 'meetPhase｜開始前一分鐘 → upcoming')
eq(meetPhase(MEET_ISO, ENDS_ISO, new Date('2026-08-31T06:00:00+08:00')), 'ongoing', 'meetPhase｜恰好 meet_at → ongoing（左閉）')
eq(meetPhase(MEET_ISO, ENDS_ISO, new Date('2026-08-31T07:00:00+08:00')), 'ongoing', 'meetPhase｜meet_at 與 ends_at 之間 → ongoing')
eq(meetPhase(MEET_ISO, ENDS_ISO, new Date('2026-08-31T07:30:00+08:00')), 'ended', 'meetPhase｜恰好 ends_at → ended（右閉，不是開區間）')
eq(meetPhase(MEET_ISO, ENDS_ISO, new Date('2026-08-31T08:00:00+08:00')), 'ended', 'meetPhase｜ends_at 之後 → ended')
// ends_at 為 null／undefined（migration 168 上線前的舊資料）：effectiveEnd 退回 meet_at 本身，
// 沒有「進行中」這個窗口——一到 meet_at 就直接是 ended，跟改動前的舊行為一致。
eq(meetPhase(MEET_ISO, null, new Date('2026-08-31T05:59:00+08:00')), 'upcoming', 'meetPhase｜ends_at=null・開始前 → upcoming')
eq(meetPhase(MEET_ISO, null, new Date('2026-08-31T06:00:00+08:00')), 'ended', 'meetPhase｜ends_at=null・恰好開始 → 直接 ended，沒有 ongoing 窗口')
eq(meetPhase(MEET_ISO, undefined, new Date('2026-08-31T06:00:00+08:00')), 'ended', 'meetPhase｜ends_at=undefined 與 null 同一種退回行為')

// phaseCountdown：ongoing/ended 不再各自對 meet_at 重算一次（避免跟旁邊的三態徽章互相矛盾），
// upcoming 原樣委派給 meetCountdown（含 urgent 6 小時內強調）。
eq(phaseCountdown('ongoing', MEET_ISO), { text: '進行中', urgent: false, ended: false }, 'phaseCountdown｜ongoing → 固定「進行中」，不管 meet_at 早就已經「過了」')
eq(phaseCountdown('ended', MEET_ISO), { text: '已結束', urgent: false, ended: true }, 'phaseCountdown｜ended → 固定「已結束」')
eq(phaseCountdown('upcoming', MEET_ISO, new Date('2026-08-29T06:00:00+08:00')), { text: '還有 2 天', urgent: false, ended: false }, 'phaseCountdown｜upcoming → 委派給 meetCountdown')

// ── 距離分級（⚠️ 後端刻意不回精確距離，前端只翻譯 band）──────────
eq(distanceBandLabel('lt1'), '1 公里內', 'band lt1')
eq(distanceBandLabel('1to3'), '1–3 公里', 'band 1to3')
eq(distanceBandLabel('3to5'), '3–5 公里', 'band 3to5')
eq(distanceBandLabel('5to10'), '5–10 公里', 'band 5to10')
eq(distanceBandLabel('gt10'), '10 公里以上', 'band gt10')
eq(distanceBandLabel(undefined), '', '未帶 band（非附近搜尋）→ 空字串')
eq(distanceBandLabel('0.23km'), '', '未知值 → 空字串（不外洩精確距離格式）')

// ── 集合地點顯示（migration 161「不限地點」）─────────────────────
// ⚠️ 後端 no_location=true 的團，region/place_label 固定是「不限」佔位字串——顯示邏輯必須先判斷
// no_location 旗標本身，不能直接拼接兩欄，否則會顯示成「不限・不限」這種沒有意義的字面組合。
eq(
  runMeetLocationText({ no_location: false, region: '臺北市・大安區', place_label: '大安森林公園' }),
  '臺北市・大安區 · 大安森林公園',
  'runMeetLocationText｜一般定點：region · place_label',
)
eq(
  runMeetLocationText({ no_location: false, region: '臺北市・大安區', place_label: '' }),
  '臺北市・大安區',
  'runMeetLocationText｜無 place_label 時只顯示 region（防禦性；正常情境兩欄皆必填）',
)
eq(
  runMeetLocationText({ no_location: true, region: '不限', place_label: '不限' }),
  '不限地點',
  'runMeetLocationText｜no_location 一律回固定文字，不拼接成「不限・不限」',
)
eq(runMeetLocationIcon(false), '📍', 'runMeetLocationIcon｜一般定點用 📍')
eq(runMeetLocationIcon(true), '🌏', 'runMeetLocationIcon｜不限地點用 🌏')

// ── 卡片封面佔位（migration 162：私密團沒有封面時改顯示鎖頭，不再顯示名稱首字）───
eq(coverFallbackGlyph(true, '晨間夜跑團'), '🔒', '私密團無封面 → 鎖頭，不透露名稱首字')
eq(coverFallbackGlyph(true, ''), '🔒', '私密團無封面（無標題）→ 仍是鎖頭')
eq(coverFallbackGlyph(false, '晨間夜跑團'), '晨', '非私密團無封面 → 維持既有的標題首字佔位')
eq(coverFallbackGlyph(false, ''), '團', '非私密團無封面且無標題 → 預設「團」字（既有防禦性行為）')

// ── 配額文案 ────────────────────────────────────────────────────
// 配額只出現在「發起」按鈕上（v1.1.683 使用者定案：獨立徽章與入口旁的「本月還有 N 次」
// 都會被誤讀成「還能加入 N 個團練」，一律移除）。
eq(createBtnText(3), '＋ 發起團練（尚可發起 3 次）', '有次數時按鈕帶剩餘數')
eq(createBtnText(1), '＋ 發起團練（尚可發起 1 次）', '剩 1 次')
eq(createBtnText(0), '＋ 發起團練', '0 次時不顯示括號（點下去才給對應出口）')
eq(createBtnText(3, true), '＋ 發起（尚可發起 3 次）', '短版（頁首）')
eq(createBtnText(0, true), '＋ 發起', '短版 0 次')
eq(resetDayText(RESETS), '9 月 1 日', '重置日文字')
eq(remainingText(1, RESETS), '本月剩餘 1 次（9 月 1 日重置）', '剩餘次數文案')
eq(remainingText(0, RESETS), '本月發起次數已用完（9 月 1 日重置）', '用完的文案')
eq(remainingText(2, ''), '本月剩餘 2 次', '無 resets_at 時省略括號')
eq(
  confirmSubText(1, RESETS, false, 10, 4),
  ['本月剩餘 1 次（9 月 1 日重置）。', '團練建立後即使關閉或刪除，次數也不會返還。', 'VIP 會員每月可發起 10 次，並可上傳最多 4 張圖片。'],
  '確認彈窗次級說明（非 VIP 多一行 VIP 權益）',
)
eq(
  confirmSubText(7, RESETS, true, 10, 4),
  ['本月剩餘 7 次（9 月 1 日重置）。', '團練建立後即使關閉或刪除，次數也不會返還。'],
  '確認彈窗次級說明（VIP 不重複推銷）',
)
// VIP 權益數字一律吃 quota 的 vip_cap / vip_image_limit（後台可調），不得寫死 10 / 4
eq(
  confirmSubText(1, RESETS, false, 5, 2),
  ['本月剩餘 1 次（9 月 1 日重置）。', '團練建立後即使關閉或刪除，次數也不會返還。', 'VIP 會員每月可發起 5 次，並可上傳最多 2 張圖片。'],
  '確認彈窗次級說明跟著後台設定走',
)

// ── 人數 ────────────────────────────────────────────────────────
eq(memberCountText(6, 12), '6 / 12 人', '人數顯示（含發起人）')
eq(memberPct(6, 12), 50, '人數進度 50%')
eq(memberPct(13, 12), 100, '超額夾在 100%')
eq(memberPct(1, 0), 0, 'capacity=0 → 0%（不除以零）')

// ── CTA 狀態矩陣（規格 5.7 逐格）──────────────────────────────────
const base = {
  my_state: 'none', status: 'open', is_ended: false, is_private: false,
  approval_required: false, has_access: true, member_count: 3, capacity: 10,
}
const cta = (patch) => runMeetCta({ ...base, ...patch })
const SHARE = { label: '📤 分享', action: 'share' }

eq(cta({}), { label: '加入團練', action: 'join', disabled: false, variant: 'primary', secondary: SHARE }, '非成員｜open+自由+公開 → 加入團練')
eq(cta({ approval_required: true }), { label: '申請加入', action: 'apply', disabled: false, variant: 'primary', secondary: SHARE }, '非成員｜open+審核+公開 → 申請加入')
eq(cta({ is_private: true, has_access: false }), { label: '🔒 輸入密碼加入', action: 'unlock', disabled: false, variant: 'primary', secondary: SHARE }, '非成員｜open+自由+私密未解鎖 → 輸入密碼加入')
eq(cta({ is_private: true, has_access: false, approval_required: true }), { label: '🔒 輸入密碼並申請', action: 'unlock', disabled: false, variant: 'primary', secondary: SHARE }, '非成員｜open+審核+私密未解鎖 → 輸入密碼並申請')
eq(cta({ is_private: true, has_access: true, approval_required: true }), { label: '申請加入', action: 'apply', disabled: false, variant: 'primary', secondary: SHARE }, '已解鎖仍需正式申請（解鎖 ≠ 成員）')
eq(cta({ member_count: 10 }), { label: '已額滿', action: 'none', disabled: true, variant: 'muted', secondary: SHARE }, '非成員｜額滿 → 已額滿(disabled)')
eq(cta({ is_ended: true }), { label: '已結束', action: 'none', disabled: true, variant: 'muted' }, '非成員｜已結束')
eq(cta({ status: 'closed' }), { label: '已關閉，不再收新成員', action: 'none', disabled: true, variant: 'muted' }, '非成員｜已關閉')
eq(cta({ status: 'cancelled' }), { label: '已中止', action: 'none', disabled: true, variant: 'muted' }, '非成員｜已中止（原「已取消」改名，closed/cancelled 都可重新開啟，非終局狀態）')
eq(cta({ my_state: 'pending' }), { label: '⏳ 審核中…', action: 'none', disabled: true, variant: 'muted', secondary: { label: '撤回申請', action: 'withdraw' } }, 'pending｜open → 審核中 + 撤回申請')
eq(cta({ my_state: 'pending', member_count: 10 }), { label: '⏳ 審核中…', action: 'none', disabled: true, variant: 'muted', secondary: { label: '撤回申請', action: 'withdraw' } }, 'pending｜額滿仍是審核中（pending 不占名額）')
eq(cta({ my_state: 'pending', is_ended: true }), { label: '申請已失效', action: 'none', disabled: true, variant: 'muted' }, 'pending｜已結束 → 申請已失效')
eq(cta({ my_state: 'rejected' }), { label: '申請未通過', action: 'none', disabled: true, variant: 'muted' }, 'rejected｜open → 申請未通過（24h 冷卻）')
eq(cta({ my_state: 'kicked' }), { label: '你已被移出這個團練', action: 'none', disabled: true, variant: 'muted' }, 'kicked｜任何狀態')
eq(cta({ my_state: 'kicked', is_ended: true, status: 'cancelled' }), { label: '你已被移出這個團練', action: 'none', disabled: true, variant: 'muted' }, 'kicked 優先於生命週期狀態')
eq(cta({ my_state: 'left' }), { label: '重新加入', action: 'join', disabled: false, variant: 'primary', secondary: SHARE }, 'left｜open+自由 → 重新加入')
eq(cta({ my_state: 'left', approval_required: true }), { label: '重新申請加入', action: 'apply', disabled: false, variant: 'primary', secondary: SHARE }, 'left｜open+審核 → 重新申請加入')
eq(cta({ my_state: 'joined' }), { label: '已加入 ✓', action: 'none', disabled: true, variant: 'outline', secondary: { label: '退出團練', action: 'leave' } }, 'joined｜open → 已加入 + 退出團練')
eq(cta({ my_state: 'joined', member_count: 10 }), { label: '已加入 ✓', action: 'none', disabled: true, variant: 'outline', secondary: { label: '退出團練', action: 'leave' } }, 'joined｜額滿 → 仍是已加入')
eq(cta({ my_state: 'joined', is_ended: true }), { label: '已加入 ✓', action: 'none', disabled: true, variant: 'outline' }, 'joined｜已結束 → 不再提供退出')
eq(cta({ my_state: 'owner' }), { label: '管理團練', action: 'manage', disabled: false, variant: 'primary', secondary: SHARE }, 'owner｜open → 管理團練')
eq(cta({ my_state: 'owner', status: 'cancelled', is_ended: true }), { label: '管理團練', action: 'manage', disabled: false, variant: 'primary', secondary: SHARE }, 'owner｜任何狀態都能管理')

// isDeviceTaipei 只驗「純函式、不看實際執行環境時區」：傳入固定 Date 由該 Date 的 offset 決定
eq(typeof isDeviceTaipei(new Date()), 'boolean', 'isDeviceTaipei 回傳布林')

// ── 「發起團練」開表單前的配額/VIP 閘門（「＋ 發起團練」與「再辦一次」共用同一判定）─────
eq(createGate({ requires_vip: false, is_vip: false, remaining: 3 }), 'ok', 'createGate｜非 VIP 尚有次數 → ok')
eq(createGate({ requires_vip: false, is_vip: true, remaining: 5 }), 'ok', 'createGate｜VIP 尚有次數 → ok')
eq(createGate({ requires_vip: true, is_vip: false, remaining: 3 }), 'vip', 'createGate｜政策要求 VIP 且本人非 VIP → vip（即使還有次數也不能開）')
eq(createGate({ requires_vip: true, is_vip: true, remaining: 3 }), 'ok', 'createGate｜政策要求 VIP 但本人已是 VIP → ok')
eq(createGate({ requires_vip: false, is_vip: false, remaining: 0 }), 'vip', 'createGate｜非 VIP 次數用完 → vip（有解法：升級）')
eq(createGate({ requires_vip: false, is_vip: true, remaining: 0 }), 'wait', 'createGate｜VIP 次數也用完 → wait（無解法，只能等下個月，不可再跳升級引導）')

// ── 留言討論串（migration 159）：表情彙總／樂觀更新、@提及前綴、cursor 分頁、頂層插入／回覆插入、
// 軟刪遮蔽——這幾條錯了不會有畫面報錯，只會默默少一則留言或表情數字兜不起來。────────────────

eq(
  reactionPills([{ kind: 'fire', count: 3 }, { kind: 'like', count: 1 }], 'fire'),
  [{ kind: 'fire', count: 3, emoji: '🔥', label: '熱血', mine: true }, { kind: 'like', count: 1, emoji: '👍', label: '讚', mine: false }],
  'reactionPills 套上 emoji/label，標出我按過的那顆',
)
eq(reactionPills([], null), [], 'reactionPills 無反應→空陣列')

eq(
  sortReactionCounts([{ kind: 'like', count: 1 }, { kind: 'fire', count: 3 }, { kind: 'heart', count: 3 }]),
  [{ kind: 'fire', count: 3 }, { kind: 'heart', count: 3 }, { kind: 'like', count: 1 }],
  'sortReactionCounts count desc、同票 kind asc（比照後端 thread.go sortReactions）',
)

eq(optimisticReactionUpdate([], null, 'fire'), { reactions: [{ kind: 'fire', count: 1 }], myReaction: 'fire' }, '樂觀更新｜原本沒反應→按下去 +1')
eq(optimisticReactionUpdate([{ kind: 'fire', count: 1 }], 'fire', null), { reactions: [], myReaction: null }, '樂觀更新｜取消自己唯一的反應→歸零移除')
eq(
  optimisticReactionUpdate([{ kind: 'fire', count: 2 }, { kind: 'like', count: 1 }], 'fire', 'like'),
  { reactions: [{ kind: 'like', count: 2 }, { kind: 'fire', count: 1 }], myReaction: 'like' },
  '樂觀更新｜換成另一種表情：舊的 -1、新的 +1，並依新計數重新排序',
)
eq(
  optimisticReactionUpdate([{ kind: 'fire', count: 1 }, { kind: 'like', count: 5 }], null, 'fire'),
  { reactions: [{ kind: 'like', count: 5 }, { kind: 'fire', count: 2 }], myReaction: 'fire' },
  '樂觀更新｜原本沒按過，直接對既有計數 +1（不影響其他人已有的反應）',
)

eq(mentionPrefix('小明'), '@小明 ', 'mentionPrefix 組出「@name 」前綴')
eq(mentionPrefix('  '), '', 'mentionPrefix 空白名稱不強加 @')
eq(mentionPrefix(''), '', 'mentionPrefix 空字串')

eq(replyTargetId({ id: 'c2', parent_id: null }), 'c2', 'replyTargetId｜對頂層留言回覆＝它自己的 id')
eq(replyTargetId({ id: 'c3', parent_id: 'c1' }), 'c1', 'replyTargetId｜對回覆再按回覆＝所屬頂層留言 id（只允許一層）')

eq(hasMoreCursor(null), false, 'hasMoreCursor｜null→false')
eq(hasMoreCursor(undefined), false, 'hasMoreCursor｜undefined→false')
eq(hasMoreCursor(''), false, 'hasMoreCursor｜空字串→false')
eq(hasMoreCursor('abc123'), true, 'hasMoreCursor｜有值→true（還有下一頁）')

eq(showViewAllComments(10), false, 'showViewAllComments｜total=10 不顯示（<=10 不必開完整討論區）')
eq(showViewAllComments(11), true, 'showViewAllComments｜total=11 顯示')
eq(showViewAllComments(0), false, 'showViewAllComments｜total=0 不顯示')

eq(showReplyToggle(2), false, 'showReplyToggle｜reply_count=2 不顯示（等於預覽則數，兩則都已顯示）')
eq(showReplyToggle(3), true, 'showReplyToggle｜reply_count=3 顯示「查看全部回覆」')

eq(replyToggleLabel(5, false), '查看全部 5 則回覆', 'replyToggleLabel｜未展開')
eq(replyToggleLabel(5, true), '收起回覆', 'replyToggleLabel｜已展開')

eq(viewAllCommentsLabel(23), '查看全部留言（23）', 'viewAllCommentsLabel 文案')

eq(mergeCommentPages([{ id: 'a' }, { id: 'b' }], [{ id: 'b' }, { id: 'c' }]), [{ id: 'a' }, { id: 'b' }, { id: 'c' }], 'mergeCommentPages｜依 id 去重（邊界重疊那筆不重複）')
eq(mergeCommentPages([], [{ id: 'x' }]), [{ id: 'x' }], 'mergeCommentPages｜空陣列起頭')

eq(insertTopComment([{ id: 'old' }], { id: 'new' }), [{ id: 'new' }, { id: 'old' }], 'insertTopComment｜插入陣列最前面（新的在前）')

const baseThread = [
  { id: 't1', parent_id: null, reply_count: 1, replies: [{ id: 'r1', parent_id: 't1' }] },
  { id: 't2', parent_id: null, reply_count: 0, replies: [] },
]
eq(
  insertReply(baseThread, 't1', { id: 'r2', parent_id: 't1' }),
  [
    { id: 't1', parent_id: null, reply_count: 2, replies: [{ id: 'r1', parent_id: 't1' }, { id: 'r2', parent_id: 't1' }] },
    { id: 't2', parent_id: null, reply_count: 0, replies: [] },
  ],
  'insertReply｜接到所屬頂層留言的 replies 尾端，reply_count +1',
)
eq(insertReply(baseThread, 'nope', { id: 'r3' }), baseThread, 'insertReply｜parentId 對不到任何頂層留言時原樣返回')

const rxItems = [{ id: 't1', reactions: [], my_reaction: null, replies: [{ id: 'r1', reactions: [], my_reaction: null }] }]
eq(
  updateCommentReaction(rxItems, 't1', [{ kind: 'fire', count: 1 }], 'fire'),
  [{ id: 't1', reactions: [{ kind: 'fire', count: 1 }], my_reaction: 'fire', replies: [{ id: 'r1', reactions: [], my_reaction: null }] }],
  'updateCommentReaction｜命中頂層留言本身',
)
eq(
  updateCommentReaction(rxItems, 'r1', [{ kind: 'heart', count: 1 }], 'heart'),
  [{ id: 't1', reactions: [], my_reaction: null, replies: [{ id: 'r1', reactions: [{ kind: 'heart', count: 1 }], my_reaction: 'heart' }] }],
  'updateCommentReaction｜命中某則回覆（不動同一則頂層留言自己的欄位）',
)

const delItems = [{
  id: 't1', body: 'hi', can_delete: true, reactions: [{ kind: 'like', count: 1 }], my_reaction: 'like', deleted: false,
  replies: [{ id: 'r1', body: 'yo', can_delete: true, reactions: [], my_reaction: null, deleted: false }],
}]
eq(
  markCommentDeleted(delItems, 't1'),
  [{
    id: 't1', body: '', can_delete: false, reactions: [], my_reaction: null, deleted: true,
    replies: [{ id: 'r1', body: 'yo', can_delete: true, reactions: [], my_reaction: null, deleted: false }],
  }],
  'markCommentDeleted｜遮蔽頂層留言本身，其回覆照常顯示',
)
eq(
  markCommentDeleted(delItems, 'r1'),
  [{
    id: 't1', body: 'hi', can_delete: true, reactions: [{ kind: 'like', count: 1 }], my_reaction: 'like', deleted: false,
    replies: [{ id: 'r1', body: '', can_delete: false, reactions: [], my_reaction: null, deleted: true }],
  }],
  'markCommentDeleted｜遮蔽某則回覆，所屬頂層留言不受影響',
)


// ── 團練同步跑入口：runMeetLiveCta（契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §2／§8／§9 P4）──────────
// 固定時窗（台北）：meet_at 06:00 − 30 分 ＝ 05:30 開放；ends_at 07:30 ＋ 30 分 ＝ 08:00 關閉（左閉右開）。
// 這支錯了不會有畫面報錯——只會讓人在時窗外按到、或該出現時看不到按鈕——所以矩陣逐格釘住。
const LIVE_MEET_ID = '5b0e7c9e-1c1f-4c58-9d0a-2f6d3a8b7e41'
const LIVE_BLOCK = {
  enabled: true,
  opens_at: '2026-08-30T21:30:00.000Z',   // 05:30 台北
  closes_at: '2026-08-31T00:00:00.000Z',  // 08:00 台北
  server_now: '2026-08-31T05:00:00+08:00',
  presence_only: false,
}
const T = (hms) => Date.parse(`2026-08-31T${hms}+08:00`) // 2026-08-31（台北）某時刻
const lm = (patch = {}, livePatch = {}) => ({ id: LIVE_MEET_ID, my_state: 'joined', status: 'open', live: { ...LIVE_BLOCK, ...livePatch }, ...patch })
const HIDDEN = { kind: 'hidden' }
const WAIT_0530 = { kind: 'waiting', label: '🏃 開始跑步（05:30 開放）', opensAt: LIVE_BLOCK.opens_at, presenceOnly: false, inWindow: false }
const READY = { kind: 'ready', label: '🏃 開始跑步', presenceOnly: false, inWindow: true }
const RESUME_IN = { kind: 'resume', label: '▶ 回到團練跑', presenceOnly: false, inWindow: true }
const ACTIVE = `/track?meet=${LIVE_MEET_ID}`

eq(LIVE_BTN_READY_LABEL, '🏃 開始跑步', '團練同步｜ready 文案常數')
eq(LIVE_BTN_RESUME_LABEL, '▶ 回到團練跑', '團練同步｜resume 文案常數')
eq(LIVE_WINDOW_BADGE, '團練進行中', '團練同步｜時窗內徽章文字')

// 時窗前／中／後 × owner／joined
eq(runMeetLiveCta(lm(), T('05:00:00')), WAIT_0530, '團練同步｜joined・時窗前 → waiting（灰、「HH:mm 開放」，opensAt 帶原字串）')
eq(runMeetLiveCta(lm({ my_state: 'owner' }), T('05:00:00')), WAIT_0530, '團練同步｜owner・時窗前 → waiting')
eq(runMeetLiveCta(lm(), T('05:29:59.999')), WAIT_0530, '團練同步｜開放前 1ms → 仍是 waiting')
eq(runMeetLiveCta(lm(), T('05:30:00')), READY, '團練同步｜恰好 opens_at → ready（左閉）')
eq(runMeetLiveCta(lm(), T('06:30:00')), READY, '團練同步｜joined・時窗內 → ready（金底白字「🏃 開始跑步」）')
eq(runMeetLiveCta(lm({ my_state: 'owner' }), T('06:30:00')), READY, '團練同步｜owner・時窗內 → ready')
eq(runMeetLiveCta(lm(), T('07:59:59.999')), READY, '團練同步｜關閉前 1ms → 仍是 ready')
eq(runMeetLiveCta(lm(), T('08:00:00')), HIDDEN, '團練同步｜恰好 closes_at → 不顯示（右開，now ≥ closes_at）')
eq(runMeetLiveCta(lm(), T('09:00:00')), HIDDEN, '團練同步｜joined・時窗後 → 不顯示')
eq(runMeetLiveCta(lm({ my_state: 'owner' }), T('09:00:00')), HIDDEN, '團練同步｜owner・時窗後 → 不顯示')

// cancelled（中止）一律不顯示；closed（暫停收人）仍可跑（契約 §2：status IN open,closed）
eq(runMeetLiveCta(lm({ status: 'cancelled' }), T('06:30:00')), HIDDEN, '團練同步｜cancelled・時窗內 → 不顯示')
eq(runMeetLiveCta(lm({ status: 'cancelled', my_state: 'owner' }), T('06:30:00')), HIDDEN, '團練同步｜cancelled・owner・時窗內 → 不顯示')
eq(runMeetLiveCta(lm({ status: 'cancelled' }), T('05:00:00')), HIDDEN, '團練同步｜cancelled・時窗前 → 不顯示（不顯示灰按鈕）')
eq(runMeetLiveCta(lm({ status: 'closed' }), T('06:30:00')), READY, '團練同步｜closed（暫停收人）・時窗內 → 仍 ready')
eq(runMeetLiveCta(lm({ status: 'closed' }), T('05:00:00')), WAIT_0530, '團練同步｜closed・時窗前 → 仍 waiting')

// enabled=false（該使用者的入口非 shown）／live 缺（公開層 DTO 沒有這個欄位）
eq(runMeetLiveCta(lm({}, { enabled: false }), T('06:30:00')), HIDDEN, '團練同步｜enabled=false・時窗內 → 不顯示')
eq(runMeetLiveCta(lm({ my_state: 'owner' }, { enabled: false }), T('05:00:00')), HIDDEN, '團練同步｜enabled=false・owner・時窗前 → 不顯示（連灰按鈕都沒有）')
eq(runMeetLiveCta(lm({ live: undefined }), T('06:30:00')), HIDDEN, '團練同步｜live 缺（公開層 DTO）→ 不顯示')
eq(runMeetLiveCta(lm({ live: null }), T('06:30:00')), HIDDEN, '團練同步｜live=null → 不顯示')
{
  const pub = { id: LIVE_MEET_ID, my_state: 'none', status: 'open', location_locked: true } // 結構上就沒有 live
  eq(runMeetLiveCta(pub, T('06:30:00')), HIDDEN, '團練同步｜非成員的公開層 DTO（無 live）→ 不顯示')
}

// 非成員（即使後端不小心給了 live.enabled=true，例如後台視角 my_state=none 但看得到成員層 DTO）
for (const st of ['none', 'pending', 'rejected', 'kicked', 'left']) {
  eq(runMeetLiveCta(lm({ my_state: st }), T('06:30:00')), HIDDEN, `團練同步｜my_state=${st}・時窗內 → 不顯示（只有 owner／joined 可開跑）`)
  eq(runMeetLiveCta(lm({ my_state: st }), T('05:00:00'), ACTIVE), HIDDEN, `團練同步｜my_state=${st}・時窗前＋殘留 active href → 仍不顯示`)
}

// presence_only（不限地點團）：按鈕規則不變，只是帶 presenceOnly 讓小字／同意視窗換文案
eq(runMeetLiveCta(lm({}, { presence_only: true }), T('06:30:00')), { ...READY, presenceOnly: true }, '團練同步｜presence_only・時窗內 → ready（presenceOnly=true）')
eq(runMeetLiveCta(lm({}, { presence_only: true }), T('05:00:00')), { ...WAIT_0530, presenceOnly: true }, '團練同步｜presence_only・時窗前 → waiting（presenceOnly=true）')
eq(runMeetLiveCta(lm({}, { presence_only: true }), T('09:00:00')), HIDDEN, '團練同步｜presence_only・時窗後 → 不顯示')
eq(runMeetLiveCta(lm({}, { presence_only: true }), T('06:30:00'), ACTIVE), { ...RESUME_IN, presenceOnly: true }, '團練同步｜presence_only・resume → presenceOnly=true')
eq(runMeetLiveSmallPrint(false), '團練同步需用手機 GPS 全程開啟；只用手錶記錄，夥伴看不到你的位置。', '團練同步｜小字（一般團）')
eq(runMeetLiveSmallPrint(true), '不限地點團練：只顯示在跑人數，不分享位置。', '團練同步｜小字（presence_only 團）')
eq([LIVE_SMALLPRINT, LIVE_SMALLPRINT_PRESENCE].some((s) => s.includes('跑團')), false, '團練同步｜文案不得出現「跑團」二字')

// resume：本機 dor_gps_active.href 含 meet=<這個團練的 id>
eq(runMeetLiveCta(lm(), T('06:30:00'), ACTIVE), RESUME_IN, '團練同步｜時窗內＋active href 含 meet=<id> → resume「▶ 回到團練跑」')
eq(runMeetLiveCta(lm({ my_state: 'owner' }), T('06:30:00'), ACTIVE), RESUME_IN, '團練同步｜owner・resume')
eq(runMeetLiveCta(lm(), T('06:30:00'), `/track?strategy=abc&meet=${LIVE_MEET_ID}&from=race`), RESUME_IN, '團練同步｜resume：meet 參數不在第一個、旁邊還有別的 query')
eq(runMeetLiveCta(lm(), T('06:30:00'), `/track?meet=${LIVE_MEET_ID.toUpperCase()}`), RESUME_IN, '團練同步｜resume：href 內 id 大小寫不拘')
eq(runMeetLiveCta(lm({ id: LIVE_MEET_ID.toUpperCase() }), T('06:30:00'), ACTIVE), RESUME_IN, '團練同步｜resume：detail.id 大小寫不拘')
eq(runMeetLiveCta(lm(), T('06:30:00'), `/track?meet=${LIVE_MEET_ID}#x`), RESUME_IN, '團練同步｜resume：href 帶 #hash 仍可辨識')
eq(runMeetLiveCta(lm(), T('06:30:00'), '/track?meet=11111111-2222-4333-8444-555555555555'), READY, '團練同步｜active href 是「另一個」團練 → 不是 resume（維持 ready）')
eq(runMeetLiveCta(lm(), T('06:30:00'), '/track'), READY, '團練同步｜active href 沒有 meet（一般自由跑）→ 不是 resume')
eq(runMeetLiveCta(lm(), T('06:30:00'), '/track?strategy=abc'), READY, '團練同步｜active href 是策略跑 → 不是 resume')
eq(runMeetLiveCta(lm(), T('06:30:00'), `/other?meet=${LIVE_MEET_ID}`), READY, '團練同步｜meet 參數出現在非 /track 路徑 → 不算')
eq(runMeetLiveCta(lm(), T('06:30:00'), ''), READY, '團練同步｜active href 空字串 → ready')
eq(runMeetLiveCta(lm(), T('06:30:00'), null), READY, '團練同步｜active href=null → ready')
eq(runMeetLiveCta(lm(), T('06:30:00'), undefined), READY, '團練同步｜未帶 active href → ready')
eq(runMeetLiveCta(lm(), T('09:00:00'), ACTIVE), HIDDEN, '團練同步｜時窗後即使有 active href → 不顯示（now ≥ closes_at 優先）')
eq(runMeetLiveCta(lm({ status: 'cancelled' }), T('06:30:00'), ACTIVE), HIDDEN, '團練同步｜cancelled＋active href → 不顯示')
eq(runMeetLiveCta(lm({}, { enabled: false }), T('06:30:00'), ACTIVE), HIDDEN, '團練同步｜enabled=false＋active href → 不顯示')
eq(runMeetLiveCta(lm(), T('05:00:00'), ACTIVE), { ...RESUME_IN, inWindow: false }, '團練同步｜（異常）時窗前卻有這團的 active href → resume，inWindow=false（已在跑的人要能回去）')

// ⚠️ 不吃 is_ended／phase：ends_at 為 NULL 的團練，meetPhase 在 meet_at 就是 ended，但同步時窗（meet_at＋3 小時＋30 分）仍開著。
{
  const nullEndsLive = { enabled: true, opens_at: '2026-08-30T21:30:00.000Z', closes_at: '2026-08-31T01:30:00.000Z', server_now: '2026-08-31T06:30:00+08:00', presence_only: false } // 關閉 09:30 台北
  const m = { id: LIVE_MEET_ID, my_state: 'joined', status: 'open', is_ended: true, phase: 'ended', live: nullEndsLive }
  eq(meetPhase(MEET_ISO, null, new Date(T('06:30:00'))), 'ended', 'ends_at=NULL・06:30：meetPhase 已是 ended（前提）')
  eq(runMeetLiveCta(m, T('06:30:00')), READY, '團練同步｜ends_at=NULL・phase=ended・時窗內 → 仍 ready（不因 is_ended／phase 藏掉）')
  eq(runMeetLiveCta(m, T('09:29:59.999')), READY, '團練同步｜ends_at=NULL・meet_at＋3h＋30m 前 1ms → 仍 ready')
  eq(runMeetLiveCta(m, T('09:30:00')), HIDDEN, '團練同步｜ends_at=NULL・恰好 meet_at＋3h＋30m → 不顯示')
  eq(runMeetLiveCta({ ...m, phase: 'upcoming', is_ended: false }, T('06:30:00')), READY, '團練同步｜結果不隨 is_ended／phase 改變')
}

// 「HH:mm 開放」一律台北時間；不同一個台北日曆日則補「M/D 」（避免三天後才開放寫成「05:30 開放」）
eq(runMeetLiveCta(lm(), Date.parse('2026-08-31T00:10:00+08:00')).label, '🏃 開始跑步（05:30 開放）', '團練同步｜同一個台北日（00:10）→ 只寫 HH:mm')
eq(runMeetLiveCta(lm(), Date.parse('2026-08-30T23:50:00+08:00')).label, '🏃 開始跑步（8/31 05:30 開放）', '團練同步｜前一天 23:50 → 補「8/31 」')
eq(runMeetLiveCta(lm(), Date.parse('2026-08-28T10:00:00+08:00')).label, '🏃 開始跑步（8/31 05:30 開放）', '團練同步｜三天前 → 補「8/31 」')
eq(runMeetLiveCta(lm({}, { opens_at: '2026-08-31T00:05:00+08:00', closes_at: '2026-08-31T03:00:00+08:00' }), Date.parse('2026-08-30T22:00:00+08:00')).label, '🏃 開始跑步（8/31 00:05 開放）', '團練同步｜午夜過後才開放：台北 00:05（不是 24:05）')

// 手機時鐘偏差：以 server_now 校正（呼叫端收到詳情當下量一次 offset，之後傳 Date.now()+offset）
// 四個邊界各抓一格「不校正會判錯、校正後才對」：手機慢→誤判未開／誤判已開；手機快→誤判已開／誤判已關。
{
  const skew = (serverHms, phoneHms) => {
    const phone = T(phoneHms)
    const offset = liveClockOffsetMs(`2026-08-31T${serverHms}+08:00`, phone) // 收到詳情當下量一次
    return { offset, raw: runMeetLiveCta(lm(), phone).kind, fixed: runMeetLiveCta(lm(), phone + offset).kind }
  }
  eq(skew('05:31:00', '05:21:00'), { offset: 600000, raw: 'waiting', fixed: 'ready' }, '團練同步｜手機慢 10 分、伺服器 05:31（已開）：未校正誤判 waiting、校正後 ready')
  eq(skew('05:25:00', '05:35:00'), { offset: -600000, raw: 'ready', fixed: 'waiting' }, '團練同步｜手機快 10 分、伺服器 05:25（未開）：未校正誤判 ready、校正後 waiting（不會提早開放）')
  eq(skew('07:55:00', '08:05:00'), { offset: -600000, raw: 'hidden', fixed: 'ready' }, '團練同步｜手機快 10 分、伺服器 07:55（未關）：未校正誤判已關、校正後 ready')
  eq(skew('08:05:00', '07:55:00'), { offset: 600000, raw: 'ready', fixed: 'hidden' }, '團練同步｜手機慢 10 分、伺服器 08:05（已關）：未校正誤判 ready、校正後 hidden')
  eq(skew('06:30:00', '06:30:00'), { offset: 0, raw: 'ready', fixed: 'ready' }, '團練同步｜時鐘準確 → offset=0、結果不變')
}
eq(liveClockOffsetMs(null, 123), 0, '團練同步｜server_now 缺 → offset 0（信任手機時鐘）')
eq(liveClockOffsetMs(undefined, 123), 0, '團練同步｜server_now=undefined → 0')
eq(liveClockOffsetMs('', 123), 0, '團練同步｜server_now 空字串 → 0')
eq(liveClockOffsetMs('not-a-date', 123), 0, '團練同步｜server_now 解析失敗 → 0')
eq(liveClockOffsetMs('2026-08-31T05:31:00+08:00', Number.NaN), 0, '團練同步｜手機時間非有限數 → 0')

// fail-closed：時間欄位壞掉一律不顯示（寧可少一顆按鈕，也不要讓人在不明時窗按到）
eq(runMeetLiveCta(lm({}, { opens_at: 'garbage' }), T('06:30:00')), HIDDEN, '團練同步｜opens_at 解析失敗 → 不顯示')
eq(runMeetLiveCta(lm({}, { closes_at: '' }), T('06:30:00')), HIDDEN, '團練同步｜closes_at 空字串 → 不顯示')
eq(runMeetLiveCta(lm(), Number.NaN), HIDDEN, '團練同步｜nowMs=NaN → 不顯示')

// 純函式不改動輸入
{
  const m = lm(); const before = JSON.stringify(m)
  runMeetLiveCta(m, T('06:30:00'), ACTIVE)
  eq(JSON.stringify(m), before, '團練同步｜runMeetLiveCta 不改動傳入的物件')
}

// 完整矩陣：my_state × status × 時窗（前／內／後）× presence_only × enabled，共 252 格，與獨立寫的判定表對帳
{
  const states = ['owner', 'joined', 'pending', 'rejected', 'kicked', 'left', 'none']
  const statuses = ['open', 'closed', 'cancelled']
  const wins = { before: '05:00:00', inside: '06:30:00', after: '08:30:00' }
  const bad = []
  let cells = 0
  for (const my_state of states) for (const status of statuses) for (const [win, hms] of Object.entries(wins))
    for (const presence_only of [false, true]) for (const enabled of [true, false]) {
      cells++
      const got = runMeetLiveCta(lm({ my_state, status }, { presence_only, enabled }), T(hms))
      const want = !enabled || !['owner', 'joined'].includes(my_state) || status === 'cancelled' ? 'hidden'
        : win === 'before' ? 'waiting' : win === 'inside' ? 'ready' : 'hidden'
      const po = got.kind === 'hidden' ? undefined : got.presenceOnly
      const inW = got.kind === 'hidden' ? undefined : got.inWindow
      if (got.kind !== want || (want !== 'hidden' && (po !== presence_only || inW !== (win === 'inside')))) {
        bad.push(`${my_state}/${status}/${win}/po=${presence_only}/en=${enabled} → ${got.kind}（應為 ${want}）`)
      }
    }
  eq({ cells, bad }, { cells: 252, bad: [] }, '團練同步｜完整矩陣 252 格全部符合判定表')
}

// dor_gps_active.href → 團練 id
eq(activeHrefMeetId(`/track?meet=${LIVE_MEET_ID}`), LIVE_MEET_ID, 'activeHrefMeetId｜/track?meet=<id>')
eq(activeHrefMeetId('/track?meet=ABCDEF12-0000-4000-8000-000000000001'), 'abcdef12-0000-4000-8000-000000000001', 'activeHrefMeetId｜轉小寫（與 /track 解析 ?meet= 的規範字串一致）')
eq(activeHrefMeetId('/track?strategy=s1&meet=m1&from=race'), 'm1', 'activeHrefMeetId｜meet 不在第一個參數')
eq(activeHrefMeetId('/track/?meet=m2'), 'm2', 'activeHrefMeetId｜/track/ 也算')
eq(activeHrefMeetId('/track?meet=m3#frag'), 'm3', 'activeHrefMeetId｜忽略 #hash')
eq(activeHrefMeetId('/track'), '', 'activeHrefMeetId｜沒有 query → 空字串')
eq(activeHrefMeetId('/track?strategy=s1'), '', 'activeHrefMeetId｜沒有 meet 參數 → 空字串')
eq(activeHrefMeetId('/track?meet='), '', 'activeHrefMeetId｜meet 為空 → 空字串')
eq(activeHrefMeetId('/trackfoo?meet=x'), '', 'activeHrefMeetId｜/trackfoo 不是 /track')
eq(activeHrefMeetId('/?meet=x'), '', 'activeHrefMeetId｜非 /track 路徑 → 空字串')
eq(activeHrefMeetId('https://evil.example/track?meet=x'), '', 'activeHrefMeetId｜絕對網址（非本站 pathname+search 格式）→ 空字串')
eq(activeHrefMeetId(''), '', 'activeHrefMeetId｜空字串')
eq(activeHrefMeetId(null), '', 'activeHrefMeetId｜null')
eq(activeHrefMeetId(undefined), '', 'activeHrefMeetId｜undefined')
eq(activeHrefMeetId(123), '', 'activeHrefMeetId｜非字串 → 空字串（不丟例外）')

// 同意視窗「這個團練不再提醒」（localStorage dor_meet_consent_v1:<meetId>；storage 由呼叫端傳入，全部 try/catch）
{
  eq(runMeetLiveConsentKey('ABC-123'), 'dor_meet_consent_v1:abc-123', '同意旗標｜key＝dor_meet_consent_v1:<小寫 meetId>')
  const mem = new Map()
  const okStore = { getItem: (k) => (mem.has(k) ? mem.get(k) : null), setItem: (k, v) => { mem.set(k, String(v)) } }
  eq(readLiveConsent(okStore, LIVE_MEET_ID), false, '同意旗標｜沒存過 → false（要彈窗）')
  writeLiveConsent(okStore, LIVE_MEET_ID)
  eq(mem.get(`dor_meet_consent_v1:${LIVE_MEET_ID}`), '1', '同意旗標｜寫入值＝"1"')
  eq(readLiveConsent(okStore, LIVE_MEET_ID), true, '同意旗標｜存過 → true（不彈窗）')
  eq(readLiveConsent(okStore, LIVE_MEET_ID.toUpperCase()), true, '同意旗標｜meetId 大小寫不拘')
  eq(readLiveConsent(okStore, '11111111-2222-4333-8444-555555555555'), false, '同意旗標｜只對「這個團練」有效，不外溢到別的團練')
  mem.set(`dor_meet_consent_v1:${LIVE_MEET_ID}`, 'true')
  eq(readLiveConsent(okStore, LIVE_MEET_ID), false, '同意旗標｜值不是 "1" → 視為沒勾')
  const boom = { getItem: () => { throw new Error('SecurityError') }, setItem: () => { throw new Error('QuotaExceededError') } }
  eq(readLiveConsent(boom, LIVE_MEET_ID), false, '同意旗標｜storage 讀取丟例外 → false（每次彈窗），不擋開跑')
  let threw = false
  try { writeLiveConsent(boom, LIVE_MEET_ID) } catch { threw = true }
  eq(threw, false, '同意旗標｜storage 寫入丟例外 → 吞掉，不擋開跑')
  eq(readLiveConsent(null, LIVE_MEET_ID), false, '同意旗標｜storage=null（取不到 localStorage）→ false')
  let threwNull = false
  try { writeLiveConsent(undefined, LIVE_MEET_ID) } catch { threwNull = true }
  eq(threwNull, false, '同意旗標｜storage=undefined 寫入 → 不丟例外')
}

// 同意證據（本分頁 sessionStorage dor_meet_consent_ok:<meetId>；/track?meet= 深連結不得在「沒在團練頁確認過同意視窗」時啟用同步／送 consent_v）
// 團練頁在使用者確認視窗（或因「不再提醒」略過視窗）的當下寫入毫秒時間戳；/track 同步引擎讀它，≤12 小時才算數。storage 由呼叫端傳入，全部 try/catch。
{
  const NOW = 1_790_000_000_000 // 固定「現在」，不依賴實際時鐘
  const H = 3600 * 1000
  const mem = new Map()
  const okStore = { getItem: (k) => (mem.has(k) ? mem.get(k) : null), setItem: (k, v) => { mem.set(k, String(v)) } }
  const KEY = `dor_meet_consent_ok:${LIVE_MEET_ID}`
  eq(LIVE_CONSENT_SESSION_MAX_MS, 12 * H, '同意證據｜有效期上限＝12 小時')
  eq(runMeetLiveConsentSessionKey('ABC-123'), 'dor_meet_consent_ok:abc-123', '同意證據｜key＝dor_meet_consent_ok:<小寫 meetId>')
  eq(runMeetLiveConsentSessionKey('ABC-123') === runMeetLiveConsentKey('ABC-123'), false, '同意證據｜與「不再提醒」旗標是不同的 key（不互相覆蓋）')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW), false, '同意證據｜沒寫過 → false（深連結不同步）')
  markLiveConsentSession(okStore, LIVE_MEET_ID, NOW)
  eq(mem.get(KEY), String(NOW), '同意證據｜寫入值＝毫秒時間戳字串')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW), true, '同意證據｜剛寫入 → true')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW + 5 * 60 * 1000), true, '同意證據｜5 分鐘後 → true')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW + 12 * H), true, '同意證據｜剛好 12 小時 → true（邊界含）')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW + 12 * H + 1), false, '同意證據｜超過 12 小時 1 毫秒 → false')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW + 30 * H), false, '同意證據｜30 小時後 → false（過期不能拿舊同意開新的同步）')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID.toUpperCase(), NOW), true, '同意證據｜meetId 大小寫不拘（讀寫皆小寫化）')
  eq(readLiveConsentSession(okStore, '11111111-2222-4333-8444-555555555555', NOW), false, '同意證據｜只對「這個團練」有效，不外溢到別的團練')
  // 裝置時鐘被往回校正：標記時間略晚於現在（≤2 分鐘）容許；晚很多＝不可信
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW - 60 * 1000), true, '同意證據｜標記比現在晚 1 分鐘（時鐘微調）→ true')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW - 2 * 60 * 1000), true, '同意證據｜標記比現在晚剛好 2 分鐘 → true（邊界含）')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW - 2 * 60 * 1000 - 1), false, '同意證據｜標記比現在晚超過 2 分鐘 → false（未來時間戳不可信）')
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW - 5 * H), false, '同意證據｜標記是 5 小時後的「未來」→ false')
  // 髒資料一律當沒有證據（fail-closed）
  for (const [raw, why] of [['', '空字串'], ['abc', '非數字'], ['NaN', 'NaN'], ['0', '0'], ['-5', '負數'], ['1', '字串 1（旗標用的值，不是時間戳）'], ['true', 'true'], ['Infinity', 'Infinity']]) {
    mem.set(KEY, raw)
    eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW), false, `同意證據｜值是${why} → false`)
  }
  // 重新寫入會覆蓋舊標記（使用者再次從團練頁進入 → 期限重算）
  mem.set(KEY, String(NOW - 20 * H))
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW), false, '同意證據｜20 小時前的舊標記 → false')
  markLiveConsentSession(okStore, LIVE_MEET_ID, NOW)
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW), true, '同意證據｜再次從團練頁進入 → 重寫標記、期限重算 → true')
  markLiveConsentSession(okStore, LIVE_MEET_ID, NOW + 0.4)
  eq(mem.get(KEY), String(NOW), '同意證據｜時間戳四捨五入成整數毫秒')
  // 隔離：本機持久旗標與本分頁標記互不影響
  mem.clear()
  writeLiveConsent(okStore, LIVE_MEET_ID)
  eq(readLiveConsentSession(okStore, LIVE_MEET_ID, NOW), false, '同意證據｜只有「不再提醒」旗標、沒有分頁標記 → 分頁標記仍是 false（兩者各自獨立判定）')
  // storage 取不到／會丟例外 → 不丟、fail-closed
  const boom = { getItem: () => { throw new Error('SecurityError') }, setItem: () => { throw new Error('QuotaExceededError') } }
  eq(readLiveConsentSession(boom, LIVE_MEET_ID, NOW), false, '同意證據｜storage 讀取丟例外 → false（不同步），不丟例外')
  let threw = false
  try { markLiveConsentSession(boom, LIVE_MEET_ID, NOW) } catch { threw = true }
  eq(threw, false, '同意證據｜storage 寫入丟例外 → 吞掉，不擋開跑')
  eq(readLiveConsentSession(null, LIVE_MEET_ID, NOW), false, '同意證據｜storage=null → false')
  eq(readLiveConsentSession(undefined, LIVE_MEET_ID, NOW), false, '同意證據｜storage=undefined → false')
  let threwNull = false
  try { markLiveConsentSession(null, LIVE_MEET_ID, NOW); markLiveConsentSession(undefined, LIVE_MEET_ID, NOW) } catch { threwNull = true }
  eq(threwNull, false, '同意證據｜storage=null/undefined 寫入 → 不丟例外')
  eq(readLiveConsentSession(okStore, '', NOW), false, '同意證據｜meetId 為空 → false（不丟例外）')
}

// ── 載入態判定（2026-08-31 使用者回報：一進頁面先看到「載入失敗」）──────────────
// 空窗期＝isLoading 已 false、但 data 與 error 都還是 undefined（SWR 切換查詢金鑰的那一幀）。
eq(isFetchPending(true, undefined, undefined), true, '正在載入 → pending')
eq(isFetchPending(false, undefined, undefined), true, '金鑰切換空窗期（三者皆空）→ 仍算 pending，不可顯示失敗')
eq(isFetchPending(false, { items: [] }, undefined), false, '已拿到資料 → 不是 pending')
eq(isFetchPending(false, undefined, new Error('x')), false, '確定失敗 → 不是 pending')
eq(shouldShowError(false, undefined, new Error('x'), false), true, '有錯誤且無資料 → 顯示失敗')
eq(shouldShowError(false, undefined, new Error('x'), true), false, '重新驗證失敗但有舊資料 → 不清空畫面')
eq(shouldShowError(false, undefined, undefined, false), false, '空窗期 → 絕不顯示失敗')
eq(shouldShowError(true, undefined, new Error('x'), false), false, '仍在載入 → 先不報錯')


// ── 「資料到手但清單尚未同步」的那一幀（使用者回報：一進頁面先閃「找不到團練」）──
eq(isAbsorbing(0, 10), true, 'data 有 10 筆、items 還是 0 → 仍算載入中，不可顯示空狀態')
eq(isAbsorbing(0, 0), false, '後端就是回空陣列 → 正常顯示空狀態')
eq(isAbsorbing(0, undefined), false, 'data 還沒到（undefined）→ 交給 isFetchPending 判定')
eq(isAbsorbing(5, 10), false, '已有內容 → 不是同步中')

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)