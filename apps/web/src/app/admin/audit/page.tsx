'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { auditApi, type AuditLog } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const PAGE = 50
const RESOURCES: { k: string; t: string }[] = [
  { k: '', t: '全部' },
  { k: 'races', t: '賽事' },
  { k: 'admins', t: '管理者' },
  { k: 'members', t: '會員' },
  { k: 'orders', t: '訂單' },
  { k: 'signups', t: '報名' },
  { k: 'promo-codes', t: '序號' },
  { k: 'task-modules', t: '賽事任務' },
  { k: 'membership', t: '等級設定' },
  { k: 'settings', t: '系統設定' },
  { k: 'gps-runs', t: 'GPS' },
]

function fmt(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export default function AdminAuditPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [logs, setLogs] = useState<AuditLog[] | null>(null)
  const [count, setCount] = useState(0)
  const [offset, setOffset] = useState(0)
  const [resource, setResource] = useState('')
  const [err, setErr] = useState('')

  const load = useCallback((t: string, off: number, res: string) => {
    auditApi.list(t, { limit: PAGE, offset: off, resource: res || undefined })
      .then((r) => { setLogs(r.logs); setCount(r.count) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('此頁僅超級管理員可存取')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    load(t, 0, '')
  }, [router, load])

  function changeResource(res: string) {
    setResource(res); setOffset(0)
    if (token) load(token, 0, res)
  }
  function page(off: number) {
    setOffset(off)
    if (token) load(token, off, resource)
  }

  return (
    <div style={{ maxWidth: 980, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>操作紀錄</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>自動記錄後台所有異動操作（新增／更新／刪除）。僅超級管理員可檢視。</p>
      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}

      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', margin: '10px 0 14px' }}>
        {RESOURCES.map((r) => (
          <button key={r.k} onClick={() => changeResource(r.k)}
            style={{ ...chip, ...(resource === r.k ? chipOn : {}) }}>{r.t}</button>
        ))}
      </div>

      <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflow: 'hidden' }}>
        <div style={{ ...row, ...headRow }}>
          <span style={{ flex: '0 0 150px' }}>時間</span>
          <span style={{ flex: '0 0 120px' }}>操作者</span>
          <span style={{ flex: '0 0 110px' }}>動作</span>
          <span style={{ flex: 1, minWidth: 0 }}>路徑</span>
          <span style={{ flex: '0 0 50px', textAlign: 'right' }}>狀態</span>
        </div>
        {!logs && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>載入中…</div>}
        {logs && logs.length === 0 && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>目前沒有紀錄</div>}
        {logs?.map((l) => {
          const ok = l.status < 400
          return (
            <div key={l.id} style={row}>
              <span style={{ flex: '0 0 150px', color: 'var(--tx-dim)', fontSize: 12 }}>{fmt(l.created_at)}</span>
              <span style={{ flex: '0 0 120px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={`${l.actor_name}（${l.actor_login}）`}>
                {l.actor_name || l.actor_login || '—'}
              </span>
              <span style={{ flex: '0 0 110px', fontWeight: 700 }}>
                <span style={{ fontSize: 10, color: 'var(--tx-faint)', marginRight: 5 }}>{l.method}</span>{l.action}
              </span>
              <span style={{ flex: 1, minWidth: 0, color: 'var(--tx-faint)', fontSize: 12, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={l.path}>{l.path}</span>
              <span style={{ flex: '0 0 50px', textAlign: 'right', fontWeight: 700, color: ok ? 'var(--fug)' : 'var(--hunt)' }}>{l.status}</span>
            </div>
          )
        })}
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
const chip: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '5px 12px', fontSize: 12.5, cursor: 'pointer', color: 'var(--tx-dim)' }
const chipOn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: '1px solid var(--fug)' }
const pgBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 14px', fontSize: 13, cursor: 'pointer', color: 'var(--tx)' }
