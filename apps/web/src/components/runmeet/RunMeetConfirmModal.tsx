'use client'

import { RunMeetModal, ghostBtn, modalActions, modalSubText, modalText, modalTitle, primaryBtn } from './ui'
import { remainingText } from '@/lib/runMeet'

// 建立前確認彈窗（需求指定）。
// ⚠️ 主文一字不改：「一旦確認建立，將會消耗團練發起次數，請問要繼續建立嗎？」
//    第二段是次級灰字，直接對應產品規則「開啟後關閉，一樣消耗一次」——後端 quota.go 只有
//    consume() 沒有 refund()，close/cancel/delete 都不回補，唯一返還管道是後台人工調整。
// ⚠️ vipCap / vipImages 一律由呼叫端從 GET /run-meets/quota 帶進來（vip_cap / vip_image_limit），
//    刻意不給預設值——寫死 10 / 4 的話，營運改了後台設定（runmeet_quota_vip / runmeet_images_vip）
//    這裡就會對非 VIP 承諾拿不到的權益，是會直接引發客訴的文案錯誤。
export default function RunMeetConfirmModal({
  remaining, resetsAt, isVip, vipCap, vipImages, busy, onCancel, onConfirm, onLearnVip, sourceTitle,
}: {
  remaining: number
  resetsAt: string
  isVip: boolean
  vipCap: number
  vipImages: number
  busy: boolean
  onCancel: () => void
  onConfirm: () => void
  onLearnVip?: () => void
  // 「再辦一次」情境（複製既有團練設定建立新團練）：帶原團名稱時，標題與主文上方多一句情境說明，
  // 主文那句固定文案本身仍一字不改（下面 modalText 內容）。
  sourceTitle?: string
}) {
  return (
    <RunMeetModal onClose={busy ? () => {} : onCancel} dismissOnBackdrop={!busy} maxWidth={360}>
      <div style={modalTitle}>{sourceTitle ? '確認再辦一次' : '確認建立團練'}</div>
      <div style={modalText}>
        {sourceTitle && <>沿用「{sourceTitle}」的設定，建立一個新團練（時間需另外設定）。<br /></>}
        一旦確認建立，將會消耗團練發起次數，請問要繼續建立嗎？
      </div>
      <div style={modalSubText}>
        {remainingText(remaining, resetsAt)}。<br />
        團練建立後即使關閉或刪除，次數也不會返還。
        {!isVip && (
          <>
            <br />
            VIP 會員每月可發起 {vipCap} 次，並可上傳最多 {vipImages} 張圖片。
            {onLearnVip && (
              <button type="button" onClick={onLearnVip} style={{ background: 'none', border: 'none', color: 'var(--gold)', fontWeight: 800, fontSize: 12.5, cursor: 'pointer', padding: '0 0 0 6px', fontFamily: 'inherit' }}>
                了解 VIP ›
              </button>
            )}
          </>
        )}
      </div>
      <div style={modalActions}>
        <button type="button" onClick={onCancel} disabled={busy} style={{ ...ghostBtn, width: '100%', padding: '11px 0', opacity: busy ? 0.6 : 1 }}>取消</button>
        {/* 後端已用 client_token 部分唯一索引擋連點/網路重試，UI 這裡也要擋一次（雙保險） */}
        <button type="button" onClick={onConfirm} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.7 : 1, cursor: busy ? 'default' : 'pointer' }}>
          {busy ? '建立中…' : '確認建立'}
        </button>
      </div>
    </RunMeetModal>
  )
}
