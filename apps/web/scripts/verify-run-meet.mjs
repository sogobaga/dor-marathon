// 驗證 apps/web/src/lib/runMeet.ts（直接 import 實際檔案，非重寫邏輯；Node 24 原生 TS type-stripping）
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-run-meet.mjs`（路徑以本檔為基準，不寫死）
//
// 涵蓋：台北時區換算、距離分級文字、配額文案、人數顯示、CTA 狀態矩陣（規格 5.7 逐格）。
// 這幾組規則錯了不會有畫面報錯，只會默默顯示錯的按鈕/時間，所以一定要有可重跑的驗證。
const modUrl = new URL('../src/lib/runMeet.ts', import.meta.url).href
const {
  taipeiParts, taipeiLocalToISO, isoToTaipeiLocalInput, fmtMeetAt, fmtMeetAtConfirm, meetCountdown,
  distanceBandLabel, quotaBadgeText, resetDayText, remainingText,
  memberCountText, memberPct, confirmSubText, runMeetCta, isDeviceTaipei,
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

eq(fmtMeetAt(MEET_ISO, new Date('2026-08-29T10:00:00+08:00')), '8/31（一）06:00', 'fmtMeetAt 非當日 → 月/日（週）時:分')
eq(fmtMeetAt(MEET_ISO, new Date('2026-08-31T05:00:00+08:00')), '今天 06:00', 'fmtMeetAt 當日（台北）→ 今天 06:00')
eq(fmtMeetAtConfirm(MEET_ISO), '將於 8/31（一）06:00（台北時間）開跑', '表單反算確認行')

// ── 相對時間 ────────────────────────────────────────────────────
eq(meetCountdown(MEET_ISO, new Date('2026-08-29T06:00:00+08:00')), { text: '還有 2 天', urgent: false, ended: false }, '48 小時 → 還有 2 天')
eq(meetCountdown(MEET_ISO, new Date('2026-08-31T03:00:00+08:00')), { text: '還有 3 小時', urgent: true, ended: false }, '3 小時 → urgent（6 小時內橘色）')
eq(meetCountdown(MEET_ISO, new Date('2026-08-30T22:00:00+08:00')), { text: '還有 8 小時', urgent: false, ended: false }, '8 小時 → 不 urgent')
eq(meetCountdown(MEET_ISO, new Date('2026-08-31T05:35:00+08:00')), { text: '還有 25 分鐘', urgent: true, ended: false }, '25 分鐘 → 分鐘級')
eq(meetCountdown(MEET_ISO, new Date('2026-08-31T06:00:01+08:00')), { text: '已結束', urgent: false, ended: true }, '時間已過 → 已結束')

// ── 距離分級（⚠️ 後端刻意不回精確距離，前端只翻譯 band）──────────
eq(distanceBandLabel('lt1'), '1 公里內', 'band lt1')
eq(distanceBandLabel('1to3'), '1–3 公里', 'band 1to3')
eq(distanceBandLabel('3to5'), '3–5 公里', 'band 3to5')
eq(distanceBandLabel('5to10'), '5–10 公里', 'band 5to10')
eq(distanceBandLabel('gt10'), '10 公里以上', 'band gt10')
eq(distanceBandLabel(undefined), '', '未帶 band（非附近搜尋）→ 空字串')
eq(distanceBandLabel('0.23km'), '', '未知值 → 空字串（不外洩精確距離格式）')

// ── 配額文案 ────────────────────────────────────────────────────
eq(quotaBadgeText({ used: 0, cap: 1, is_vip: false }), '本月 0/1', '一般會員配額徽章')
eq(quotaBadgeText({ used: 3, cap: 10, is_vip: true }), '本月 3/10 · VIP', 'VIP 配額徽章')
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

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
