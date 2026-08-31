'use client'

import { useState } from 'react'
import { runMeetApi } from '@/lib/api'
import { withUserAuth } from '@/lib/userAuth'
import { RunMeetModal, errText, ghostBtn, inputStyle, modalActions, modalSubText, modalText, modalTitle, primaryBtn } from './ui'

// 私密團練密碼輸入。
// ⚠️ 通過密碼 ≠ 成為成員：解鎖只是「進入詳情頁的入場券」（後端 run_meet_access 票證表，
//    一次解鎖跨裝置有效），精確地點與集合細節仍必須正式加入才看得到。
// 後端對「團不存在／已下架／密碼錯誤」一律回同一句 403 且時序一致（防列舉），
// 所以前端直接顯示後端訊息即可，不要自作聰明分類。
export default function RunMeetUnlockModal({ meetId, title, onClose, onUnlocked }: {
  meetId: string
  title: string
  onClose: () => void
  onUnlocked: () => void
}) {
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  async function submit() {
    if (busy || pw.length < 1) return
    setBusy(true); setErr('')
    try {
      await withUserAuth((t) => runMeetApi.unlock(t, meetId, pw))
      onUnlocked()
    } catch (e: any) {
      setErr(e?.message || '團練密碼錯誤，請再確認一次。')
    } finally {
      setBusy(false)
    }
  }

  return (
    <RunMeetModal onClose={busy ? () => {} : onClose} dismissOnBackdrop={!busy} maxWidth={340}>
      <div style={modalTitle}>🔒 這是私密團練</div>
      <div style={modalText}>請輸入發起人提供的密碼，才能看到「{title}」的完整內容。</div>
      <input
        value={pw}
        onChange={(e) => setPw(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') submit() }}
        type="password"
        autoComplete="off"
        maxLength={32}
        placeholder="團練密碼"
        style={{ ...inputStyle, marginTop: 14 }}
      />
      <div style={modalSubText}>通過密碼後即可看到完整說明與圖片；精確地點與集合細節仍需成功加入才會顯示。</div>
      {err && <div style={errText}>{err}</div>}
      <div style={modalActions}>
        <button type="button" onClick={onClose} disabled={busy} style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}>取消</button>
        <button type="button" onClick={submit} disabled={busy || !pw} style={{ ...primaryBtn, opacity: busy || !pw ? 0.7 : 1 }}>
          {busy ? '確認中…' : '確認'}
        </button>
      </div>
    </RunMeetModal>
  )
}
