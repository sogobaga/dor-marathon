// 驗證 apps/web/src/lib/runMeet.ts（直接 import 實際檔案，非重寫邏輯；Node 24 原生 TS type-stripping）
// 執行位置：apps/web 下 `node --experimental-strip-types scripts/verify-run-meet.mjs`（路徑以本檔為基準，不寫死）
//
// 涵蓋：台北時區換算、距離分級文字、配額文案、人數顯示、CTA 狀態矩陣（規格 5.7 逐格）。
// 這幾組規則錯了不會有畫面報錯，只會默默顯示錯的按鈕/時間，所以一定要有可重跑的驗證。
const modUrl = new URL('../src/lib/runMeet.ts', import.meta.url).href
const {
  taipeiParts, taipeiLocalToISO, isoToTaipeiLocalInput, fmtMeetAt, fmtMeetAtConfirm, meetCountdown,
  distanceBandLabel, createBtnText, resetDayText, remainingText,
  memberCountText, memberPct, confirmSubText, runMeetCta, isDeviceTaipei,
  reactionPills, sortReactionCounts, optimisticReactionUpdate, mentionPrefix, replyTargetId,
  hasMoreCursor, showViewAllComments, showReplyToggle, replyToggleLabel, viewAllCommentsLabel,
  mergeCommentPages, insertTopComment, insertReply, updateCommentReaction, markCommentDeleted,
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

console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
