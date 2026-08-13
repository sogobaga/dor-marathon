'use client'

import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { GoogleLogin } from '@react-oauth/google'
import { authApi } from '@/lib/api'
import { setUserSession, clearUserSession, useUser } from '@/lib/userAuth'
import { googleConfigured } from './GoogleAuthProvider'
import { overlayMount } from '@/lib/overlayMount'

const REF_CODE_KEY = 'dor:ref_code'

export default function UserAuthBar({ onProfile }: { onProfile?: () => void }) {
  const user = useUser()
  const [showLogin, setShowLogin] = useState(false)
  // 受邀歡迎提示：有暫存的推廣碼、且尚未登入 → 顯示一行小提示（掛載後才讀 localStorage，避免 SSR 不一致）
  const [hasRefCode, setHasRefCode] = useState(false)
  useEffect(() => {
    setHasRefCode(!!localStorage.getItem(REF_CODE_KEY))
  }, [])

  function logout() {
    clearUserSession() // 觸發 useUser 更新
  }

  // 已登入：顯示名稱 + 登出
  if (user) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        {user.avatar_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={user.avatar_url} alt="" style={{ width: 26, height: 26, borderRadius: '50%', objectFit: 'cover', display: 'block', flexShrink: 0 }} />
        ) : (
          <div style={avatar}>{(user.name || 'U').slice(0, 1)}</div>
        )}
        <button onClick={onProfile} style={{ ...logoutBtn, color: 'var(--tx)', maxWidth: 110, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title="個人資訊">
          {user.name}
        </button>
        <button onClick={logout} style={logoutBtn}>登出</button>
      </div>
    )
  }

  // 未登入：顯示「登入」按鈕，點擊跳出登入視窗；若帶有推廣連結的推薦碼，順帶顯示歡迎提示
  return (
    <>
      {hasRefCode && (
        <span style={refHint}>🎁 你受邀加入 DOR！註冊並完成累積 10 公里，你和朋友都能獲得 VIP 天數</span>
      )}
      <button onClick={() => setShowLogin(true)} style={loginBtn}>登入</button>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
    </>
  )
}

// 登入彈窗：預設提供 Google 登入，未來可在此擴充其他第三方
export function LoginModal({ onClose }: { onClose: () => void }) {
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  // 受邀歡迎提示：有暫存的推廣碼（尚未登入才會看到這個彈窗）→ 顯示一行小提示
  // 掛載後才讀 localStorage，避免 SSR 不一致
  const [hasRefCode, setHasRefCode] = useState(false)
  useEffect(() => {
    setHasRefCode(!!localStorage.getItem(REF_CODE_KEY))
  }, [])

  // 掛載點：手機模擬框內→portal 進框(桌機不鋪滿視窗)；獨立路由(無手機框)→退回 document.body(視窗)
  const om = overlayMount()
  const content = (
    <div style={{ ...overlay, position: om.position }} onClick={onClose}>
      <div style={panel} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
          <strong style={{ fontSize: 18 }}>登入 DOR</strong>
          <button onClick={onClose} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 18 }}>✕</button>
        </div>
        <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '0 0 18px' }}>選擇登入方式以報名賽事</p>
        {hasRefCode && (
          <p style={refHint}>🎁 你受邀加入 DOR！註冊並完成累積 10 公里，你和朋友都能獲得 VIP 天數</p>
        )}

        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, opacity: busy ? 0.5 : 1, pointerEvents: busy ? 'none' : 'auto' }}>
          {googleConfigured ? (
            <GoogleLogin
              onSuccess={async (cred) => {
                if (!cred.credential) { setErr('未取得 Google 憑證'); return }
                setErr(''); setBusy(true)
                try {
                  // 若之前透過推廣連結進站，帶上暫存的推薦碼（後端只在「新帳號」分支綁定，舊帳號會忽略）
                  const refCode = localStorage.getItem(REF_CODE_KEY) || undefined
                  const res = await authApi.google(cred.credential, refCode)
                  setUserSession(res.tokens.access_token, res.tokens.refresh_token, res.user, res.tokens.session_epoch) // 觸發 useUser 更新；session_epoch 供單一登入判定
                  localStorage.removeItem(REF_CODE_KEY) // 不論新舊帳號都清，避免下次登入又誤帶
                  onClose()
                } catch (e: any) {
                  setErr(e?.message || '登入失敗')
                } finally {
                  setBusy(false)
                }
              }}
              onError={() => setErr('Google 登入失敗')}
              width="280"
            />
          ) : (
            <div style={{ fontSize: 13, color: 'var(--tx-faint)', padding: '10px 0' }}>Google 登入尚未設定</div>
          )}
          {/* 未來其他第三方登入按鈕可加在這裡 */}
        </div>

        {err && <div style={{ fontSize: 12, color: 'var(--hunt)', marginTop: 12 }}>{err}</div>}

        {/* 法律／支援連結：未登入者在登入/註冊前可先看說明。新分頁開啟、不中斷登入流程。 */}
        <div style={{ marginTop: 18, paddingTop: 14, borderTop: '1px solid var(--line-2)' }}>
          <p style={{ fontSize: 11.5, color: 'var(--tx-faint)', margin: '0 0 8px', lineHeight: 1.6, textAlign: 'center' }}>註冊／登入即表示你已閱讀並同意下列條款</p>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px 10px', justifyContent: 'center', alignItems: 'center' }}>
            <a href="/support" target="_blank" rel="noreferrer" style={legalLink}>支援說明</a>
            <span style={{ color: 'var(--tx-faint)', fontSize: 12 }}>·</span>
            <a href="/terms" target="_blank" rel="noreferrer" style={legalLink}>服務條款／退款</a>
            <span style={{ color: 'var(--tx-faint)', fontSize: 12 }}>·</span>
            <a href="/privacy" target="_blank" rel="noreferrer" style={legalLink}>隱私權政策</a>
          </div>
        </div>
      </div>
    </div>
  )
  // 用 portal 掛到手機模擬框(#app-shell)或 document.body：跳出「可拖曳面板背景層」的 -webkit-overflow-scrolling 捲動容器，
  // 否則 iOS Safari 會把 position:fixed 困在該容器內、被面板(z500)蓋住。SSR 時 om.node 為 null → 先不 portal。
  return om.node ? createPortal(content, om.node) : content
}

const avatar: React.CSSProperties = {
  width: 26, height: 26, borderRadius: 999, background: 'var(--bg-2)', border: '1px solid var(--line-2)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, color: 'var(--tx)',
}
const logoutBtn: React.CSSProperties = {
  background: 'rgba(255,255,255,.05)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 8, padding: '4px 10px', cursor: 'pointer', fontSize: 12,
}
const loginBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 9, padding: '7px 16px', cursor: 'pointer', fontSize: 13,
}
const legalLink: React.CSSProperties = { fontSize: 12, color: 'var(--fug)', textDecoration: 'none', fontWeight: 600 }
// 受邀歡迎提示（推廣連結 ?ref= 帶入、尚未登入時顯示）：輕量小字，不搶版面
const refHint: React.CSSProperties = {
  fontSize: 12, color: 'var(--fug)', margin: '0 0 14px', lineHeight: 1.5,
}
const overlay: React.CSSProperties = {
  // 已 portal 到手機模擬框(#app-shell)或 document.body（跳出捲動容器）；position 由 om.position 動態覆蓋
  // （框內用 absolute 被裁切、獨立路由用 fixed 佔滿視窗）；zIndex 拉到系統級提示之上，確保登入視窗永遠在最上層、
  // 不被可拖曳面板(500)、事件演出(2000+)或其他覆蓋層蓋住。
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 3300, padding: 20,
}
const panel: React.CSSProperties = {
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16,
  padding: 22, width: '100%', maxWidth: 340,
}
