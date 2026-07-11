'use client'

// 站內信（訊息中心）——信封 icon 按鈕 + 未讀紅點徽章 + 訊息列表覆蓋層。
// 自足元件：自行取 user token（withUserAuth）、自行管理開關/清單/已讀狀態，對外無 props。
import { useCallback, useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { mailApi, type MailItem } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { useSiteRealtimeStore } from '@/lib/siteRealtimeStore'

function fmtDate(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

// 信封 icon（按鈕本體，恆定樣式）
function MailIcon({ size = 22 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden>
      <rect x="2.5" y="5" width="19" height="14" rx="2.5" stroke="currentColor" strokeWidth="1.8" />
      <path d="M3 6L12 13L21 6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

// 未讀（閉口／已加封蠟）信封——列表用，較醒目
function SealedEnvelopeIcon({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden>
      <rect x="2.5" y="5" width="19" height="14" rx="2.5" fill="currentColor" fillOpacity="0.14" stroke="currentColor" strokeWidth="1.8" />
      <path d="M3 6L12 12.5L21 6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx="12" cy="12.5" r="1.5" fill="currentColor" />
    </svg>
  )
}

// 已讀（開口）信封——列表用，較淡
function OpenEnvelopeIcon({ size = 18 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden>
      <rect x="2.5" y="8" width="19" height="11" rx="2.5" stroke="currentColor" strokeWidth="1.6" />
      <path d="M3 9L12 3L21 9" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M3 9L10.5 14.5H13.5L21 9" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

export default function MailPanel() {
  const [unread, setUnread] = useState(0)
  const [open, setOpen] = useState(false)
  const [mail, setMail] = useState<MailItem[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const mailTick = useSiteRealtimeStore((s) => s.mailTick) // WS 站內信到達計數：變動→即時重抓未讀數（紅點立即出現）

  const loadUnread = useCallback(() => {
    if (!getUserToken()) return
    withUserAuth((t) => mailApi.unreadCount(t)).then((r) => setUnread(r.unread_count)).catch(() => {})
  }, [])

  // 掛載時抓一次未讀數；切回分頁（focus/visibilitychange）時 revalidate
  useEffect(() => {
    loadUnread()
    function onVis() { if (document.visibilityState === 'visible') loadUnread() }
    window.addEventListener('focus', loadUnread)
    document.addEventListener('visibilitychange', onVis)
    return () => {
      window.removeEventListener('focus', loadUnread)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [loadUnread, mailTick])

  function openPanel() {
    setOpen(true)
    setExpandedId(null)
    setSelected(new Set())
    setLoading(true); setErr('')
    withUserAuth((t) => mailApi.list(t))
      .then((r) => { setMail(r.mail); setUnread(r.unread_count) })
      .catch((e: any) => setErr(e?.message || '載入訊息失敗，請稍後再試'))
      .finally(() => setLoading(false))
  }

  // 點列展開/收合；展開一封未讀信時樂觀標已讀，失敗回滾
  async function toggleExpand(id: string) {
    setExpandedId((cur) => (cur === id ? null : id))
    const m = mail?.find((x) => x.id === id)
    if (!m || m.read) return
    setMail((list) => (list ? list.map((x) => (x.id === id ? { ...x, read: true } : x)) : list))
    setUnread((u) => Math.max(0, u - 1))
    try {
      await withUserAuth((t) => mailApi.markRead(t, { ids: [id] }))
    } catch {
      setMail((list) => (list ? list.map((x) => (x.id === id ? { ...x, read: false } : x)) : list))
      setUnread((u) => u + 1)
    }
  }

  const allChecked = !!mail && mail.length > 0 && selected.size === mail.length
  function toggleAll() {
    if (!mail) return
    setSelected(allChecked ? new Set() : new Set(mail.map((m) => m.id)))
  }
  function toggleOne(id: string) {
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(id)) n.delete(id); else n.add(id)
      return n
    })
  }

  async function markSelectedRead() {
    if (!mail || selected.size === 0) return
    const prevMail = mail
    const prevUnread = unread
    const ids = Array.from(selected)
    const useAll = allChecked
    const newlyRead = ids.filter((id) => prevMail.find((m) => m.id === id && !m.read)).length
    setMail((list) => (list ? list.map((x) => (selected.has(x.id) ? { ...x, read: true } : x)) : list))
    setUnread((u) => Math.max(0, u - newlyRead))
    try {
      if (useAll) await withUserAuth((t) => mailApi.markRead(t, { all: true }))
      else await withUserAuth((t) => mailApi.markRead(t, { ids }))
      setSelected(new Set())
    } catch (e: any) {
      setMail(prevMail)
      setUnread(prevUnread)
      setErr(e?.message || '標記已讀失敗，請稍後再試')
    }
  }

  return (
    <>
      <button onClick={(e) => { e.stopPropagation(); openPanel() }} style={iconBtn} aria-label="訊息中心" title="訊息中心">
        <MailIcon />
        {unread > 0 && <span style={badge}>!</span>}
      </button>

      {open && typeof document !== 'undefined' && createPortal(
        <div onClick={(e) => { e.stopPropagation(); setOpen(false) }} style={overlay}>
          <div onClick={(e) => e.stopPropagation()} style={panel}>
            <div style={header}>
              <span style={{ fontWeight: 800, fontSize: 16, color: 'var(--tx)' }}>訊息中心</span>
              <button onClick={() => setOpen(false)} style={closeBtn} aria-label="關閉">✕</button>
            </div>

            {!!mail?.length && (
              <div style={toolbar}>
                <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12.5, color: 'var(--tx-dim)', cursor: 'pointer' }}>
                  <input type="checkbox" checked={allChecked} onChange={toggleAll} /> 全選
                </label>
                <button onClick={markSelectedRead} disabled={selected.size === 0} style={{ ...markBtn, opacity: selected.size === 0 ? 0.5 : 1, cursor: selected.size === 0 ? 'default' : 'pointer' }}>
                  設為已讀
                </button>
              </div>
            )}

            <div style={{ flex: 1, overflowY: 'auto', WebkitOverflowScrolling: 'touch' }}>
              {loading && <div style={emptyMsg}>載入中…</div>}
              {!loading && err && <div style={{ ...emptyMsg, color: 'var(--hunt)' }}>{err}</div>}
              {!loading && !err && mail && mail.length === 0 && <div style={emptyMsg}>目前沒有訊息</div>}
              {!loading && mail && mail.map((m) => (
                <div key={m.id}>
                  <div style={row} onClick={() => toggleExpand(m.id)}>
                    <input
                      type="checkbox" checked={selected.has(m.id)}
                      onClick={(e) => e.stopPropagation()}
                      onChange={() => toggleOne(m.id)}
                      style={{ marginTop: 3, flexShrink: 0 }}
                    />
                    <span style={{ flexShrink: 0, marginTop: 1, color: m.read ? 'var(--tx-faint)' : 'var(--hunt)' }}>
                      {m.read ? <OpenEnvelopeIcon /> : <SealedEnvelopeIcon />}
                    </span>
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ fontSize: 13.5, fontWeight: m.read ? 500 : 800, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {m.level === 'important' && <span style={{ color: 'var(--fug)' }}>[重要] </span>}
                        {m.level === 'urgent' && <span style={{ color: 'var(--hunt)' }}>[緊急] </span>}
                        <span style={{ color: 'var(--tx)' }}>{m.title}</span>
                      </div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 3 }}>{fmtDate(m.created_at)}</div>
                    </div>
                  </div>
                  {expandedId === m.id && (
                    <div style={detail}>
                      <div style={{ fontSize: 13, color: 'var(--tx)', lineHeight: 1.7, whiteSpace: 'pre-wrap' }}>{m.body}</div>
                      {m.url && <a href={m.url} style={{ fontSize: 12.5, color: 'var(--fug)', marginTop: 8, display: 'inline-block' }}>前往 ›</a>}
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>發送時間 {fmtDate(m.created_at)}</div>
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        </div>,
        document.body,
      )}
    </>
  )
}

const iconBtn: React.CSSProperties = {
  position: 'relative', background: 'transparent', border: 'none', padding: 4, cursor: 'pointer',
  color: 'var(--tx)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', lineHeight: 0,
}
const badge: React.CSSProperties = {
  position: 'absolute', top: -2, right: -2, minWidth: 15, height: 15, borderRadius: '50%',
  background: 'var(--hunt)', color: '#fff', fontSize: 10, fontWeight: 900, display: 'flex',
  alignItems: 'center', justifyContent: 'center', border: '1.5px solid var(--bg-1)', lineHeight: 1,
}
const overlay: React.CSSProperties = {
  position: 'fixed', inset: 0, zIndex: 600, background: 'rgba(4,8,6,.6)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16,
}
const panel: React.CSSProperties = {
  width: '100%', maxWidth: 420, maxHeight: '86dvh', display: 'flex', flexDirection: 'column',
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16,
  boxShadow: '0 16px 50px rgba(0,0,0,.4)', overflow: 'hidden',
}
const header: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '14px 16px',
  borderBottom: '1px solid var(--line)', flexShrink: 0,
}
const closeBtn: React.CSSProperties = { background: 'transparent', border: 'none', color: 'var(--tx-dim)', fontSize: 16, cursor: 'pointer', padding: 4, lineHeight: 1 }
const toolbar: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '8px 16px',
  borderBottom: '1px solid var(--line)', flexShrink: 0,
}
const markBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 8, padding: '6px 12px', fontSize: 12, fontWeight: 800, fontFamily: 'inherit' }
const row: React.CSSProperties = { display: 'flex', alignItems: 'flex-start', gap: 10, padding: '12px 16px', borderBottom: '1px solid var(--line)', cursor: 'pointer' }
const detail: React.CSSProperties = { padding: '2px 16px 16px 46px', background: 'var(--bg-2)' }
const emptyMsg: React.CSSProperties = { padding: '40px 16px', textAlign: 'center', color: 'var(--tx-faint)', fontSize: 13 }
