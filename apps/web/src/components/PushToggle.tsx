'use client'

// 推播通知開關卡片——掛在 ProfileScreen「運動數據」頁。
// 流程：檢查瀏覽器支援 → 讀 /push/vapid（enabled=false 表示管理員尚未設定）→ 開：要權限→註冊 SW→訂閱→打 /push/subscribe；關：取現有訂閱→退訂+打 /push/unsubscribe。
import { useEffect, useState } from 'react'
import { pushApi, type PushVapid } from '@/lib/api'
import { withUserAuth } from '@/lib/userAuth'

type PushState = 'checking' | 'unsupported' | 'disabled' | 'ready'

// VAPID public key（base64url）→ Uint8Array，供 PushManager.subscribe 的 applicationServerKey 使用（標準寫法）
// 回傳型別明確標為 Uint8Array<ArrayBuffer>：TS 5.7+ TypedArray 泛型化後，預設 Uint8Array（=Uint8Array<ArrayBufferLike>）
// 不相容 DOM BufferSource（要求 ArrayBufferView<ArrayBuffer>），需明確標註才能直接傳給 applicationServerKey。
function urlBase64ToUint8Array(base64Url: string): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64Url.length % 4)) % 4)
  const base64 = (base64Url + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const arr = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) arr[i] = raw.charCodeAt(i)
  return arr
}

// iOS（含偽裝成 Mac 的 iPadOS）：Safari 需先「加入主畫面」才支援 Web Push
function isIOS(): boolean {
  if (typeof navigator === 'undefined') return false
  return /iphone|ipad|ipod/i.test(navigator.userAgent) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)
}

export default function PushToggle() {
  const [state, setState] = useState<PushState>('checking')
  const [vapid, setVapid] = useState<PushVapid | null>(null)
  const [permission, setPermission] = useState<NotificationPermission>('default')
  const [subscribed, setSubscribed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    let cancelled = false
    async function init() {
      const supported = typeof window !== 'undefined' && 'Notification' in window && 'serviceWorker' in navigator && 'PushManager' in window
      if (!supported) { setState('unsupported'); return }
      setPermission(Notification.permission)
      try {
        const v = await withUserAuth((t) => pushApi.vapidKey(t))
        if (cancelled) return
        setVapid(v)
        if (!v.enabled) { setState('disabled'); return }
        setState('ready')
        // 換裝置/重新整理後可能已有訂閱 → 同步顯示狀態
        const reg = await navigator.serviceWorker.getRegistration('/sw.js')
        const sub = await reg?.pushManager.getSubscription()
        if (!cancelled) setSubscribed(!!sub)
      } catch {
        if (!cancelled) setState('unsupported') // 讀取失敗（未登入/網路異常）→ 保守不顯示開關
      }
    }
    init()
    return () => { cancelled = true }
  }, [])

  async function enable() {
    if (!vapid) return
    setBusy(true); setErr('')
    try {
      const perm = await Notification.requestPermission()
      setPermission(perm)
      if (perm !== 'granted') return
      const reg = await navigator.serviceWorker.register('/sw.js')
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(vapid.public_key),
      })
      const json = sub.toJSON()
      if (!json.endpoint || !json.keys?.p256dh || !json.keys?.auth) throw new Error('訂閱資料不完整')
      await withUserAuth((t) => pushApi.subscribe(t, { endpoint: json.endpoint!, keys: { p256dh: json.keys!.p256dh, auth: json.keys!.auth } }))
      setSubscribed(true)
    } catch (e: any) {
      setErr(e?.message || '開啟通知失敗，請稍後再試')
    } finally {
      setBusy(false)
    }
  }

  async function disable() {
    setBusy(true); setErr('')
    try {
      const reg = await navigator.serviceWorker.getRegistration('/sw.js')
      const sub = await reg?.pushManager.getSubscription()
      if (sub) {
        const endpoint = sub.endpoint
        await sub.unsubscribe()
        await withUserAuth((t) => pushApi.unsubscribe(t, endpoint))
      }
      setSubscribed(false)
    } catch (e: any) {
      setErr(e?.message || '關閉通知失敗，請稍後再試')
    } finally {
      setBusy(false)
    }
  }

  const ios = isIOS()

  return (
    <div style={card}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontWeight: 700, color: 'var(--tx)' }}>推播通知</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
            {state === 'checking' && '檢查中…'}
            {state === 'unsupported' && '此瀏覽器不支援推播'}
            {state === 'disabled' && '推播尚未啟用（管理員設定中）'}
            {state === 'ready' && permission === 'denied' && '通知權限已被拒絕，請至瀏覽器設定開啟'}
            {state === 'ready' && permission !== 'denied' && (subscribed ? '已開啟通知' : '開啟後可收到活動與任務提醒')}
          </div>
        </div>
        {state === 'ready' && permission !== 'denied' && (
          <button
            onClick={subscribed ? disable : enable}
            disabled={busy}
            style={subscribed ? { ...toggleBtn, background: 'var(--fug)', color: 'var(--fug-ink)', borderColor: 'var(--fug)' } : toggleBtn}
          >
            {busy ? '處理中…' : subscribed ? '已開啟 ✓' : '開啟通知'}
          </button>
        )}
      </div>
      {err && <div style={{ fontSize: 12, color: 'var(--hunt)', marginTop: 8 }}>{err}</div>}
      {ios && (
        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.6 }}>
          iPhone 需先用 Safari「加入主畫面」後、從主畫面圖示開啟才能開通知
        </div>
      )}
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 14px)', padding: 14 }
const toggleBtn: React.CSSProperties = {
  flexShrink: 0, background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', fontFamily: 'inherit',
}
