'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminLoginLogsApi, type LoginLog } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const PAGE = 50
const METHOD_LABEL: Record<string, string> = { password: '密碼', google: 'Google', register: '註冊' }

function fmt(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export default function AdminLoginLogsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [logs, setLogs] = useState<LoginLog[] | null>(null)
  const [count, setCount] = useState(0)
  const [offset, setOffset] = useState(0)
  const [q, setQ] = useState('')
  const [qApplied, setQApplied] = useState('')
  const [err, setErr] = useState('')

  const load = useCallback((t: string, off: number, query: string) => {
    adminLoginLogsApi.list(t, { limit: PAGE, offset: off, q: query || undefined })
      .then((r) => { setLogs(r.logs); setCount(r.count) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('權限不足，無法檢視此頁')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    load(t, 0, '')
  }, [router, load])

  function submitSearch(e: React.FormEvent) {
    e.preventDefault()
    setOffset(0)
    setQApplied(q)
    if (token) load(token, 0, q)
  }
  function page(off: number) {
    setOffset(off)
    if (token) load(token, off, qApplied)
  }

  return (
    <div style={{ maxWidth: 980, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>登入紀錄</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        記錄玩家每次成功登入（密碼／Google／註冊），可檢查是否天天登入。不含自動續期（refresh）。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}

      <form onSubmit={submitSearch} style={{ display: 'flex', gap: 10, margin: '10px 0 14px' }}>
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="搜尋 Email"
          style={{
            flex: 1, maxWidth: 320, background: 'var(--bg-2)', border: '1px solid var(--line-2)',
            borderRadius: 10, padding: '9px 12px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit',
          }}
        />
        <button type="submit" style={{
          background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
          borderRadius: 10, padding: '9px 18px', cursor: 'pointer', fontSize: 14,
        }}>搜尋</button>
      </form>

      <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflow: 'hidden' }}>
        <div style={{ ...row, ...headRow }}>
          <span style={{ flex: '0 0 150px' }}>時間</span>
          <span style={{ flex: 1, minWidth: 0 }}>會員 Email</span>
          <span style={{ flex: '0 0 90px' }}>方式</span>
          <span style={{ flex: '0 0 140px', textAlign: 'right' }}>IP</span>
        </div>
        {!logs && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>載入中…</div>}
        {logs && logs.length === 0 && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>目前沒有紀錄</div>}
        {logs?.map((l, i) => (
          <div key={`${l.user_id}-${l.created_at}-${i}`} style={row}>
            <span style={{ flex: '0 0 150px', color: 'var(--tx-dim)', fontSize: 12 }}>{fmt(l.created_at)}</span>
            <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={l.email}>
              {l.email || '—'}
            </span>
            <span style={{ flex: '0 0 90px', fontWeight: 700 }}>{METHOD_LABEL[l.method] || l.method}</span>
            <span style={{ flex: '0 0 140px', textAlign: 'right', color: 'var(--tx-faint)', fontSize: 12, fontFamily: 'monospace' }}>{l.ip || '—'}</span>
          </div>
        ))}
      </div>

      {/* 分頁 */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 12, fontSize: 13, color: 'var(--tx-dim)' }}>
        <span>共 {count} 筆 · 第 {Math.floor(offset / PAGE) + 1} / {Math.max(1, Math.ceil(count / PAGE))} 頁</span>
        <div style={{ display: 'flex', gap: 8 }}>
          <button disabled={offset <= 0} onClick={() => page(Math.max(0, offset - PAGE))} style={{ ...pgBtn, opacity: offset <= 0 ? 0.4 : 1 }}>上一頁</button>
          <button disabled={offset + PAGE >= count} onClick={() => page(offset + PAGE)} style={{ ...pgBtn, opacity: offset + PAGE >= count ? 0.4 : 1 }}>下一頁</button>
        </div>
      </div>
    </div>
  )
}

const row: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 12, padding: '10px 14px', borderBottom: '1px solid var(--line)', fontSize: 13 }
const headRow: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.05em', fontWeight: 700 }
const pgBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 14px', fontSize: 13, cursor: 'pointer', color: 'var(--tx)' }
