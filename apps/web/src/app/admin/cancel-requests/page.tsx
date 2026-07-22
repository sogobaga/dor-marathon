'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminCancelRequestsApi, type AdminCancelRequest } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const fmtDt = (iso: string) => { const d = new Date(iso); return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }
const ntd = (cents: number) => 'NT$ ' + Math.round((cents || 0) / 100).toLocaleString('zh-TW')

export default function AdminCancelRequestsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [rows, setRows] = useState<AdminCancelRequest[] | null>(null)
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback((t: string) => {
    setErr('')
    adminCancelRequestsApi.list(t, 'pending')
      .then((r) => setRows(r.cancel_requests))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
      })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    load(t)
  }, [router, load])

  async function approve(c: AdminCancelRequest) {
    if (!token || busy) return
    const warn = c.refund_ratio > 0
      ? `將執行退款 ${ntd(c.refund_amount_cents)}（比例 ${c.refund_ratio}%）並釋出名額，此動作無法復原。確定核准？`
      : `不退費，僅釋出名額，此動作無法復原。確定核准？`
    if (!window.confirm(warn)) return
    setBusy(c.id); setErr(''); setMsg('')
    try {
      const res = await adminCancelRequestsApi.approve(token, c.id)
      setRows((rs) => (rs ?? []).filter((x) => x.id !== c.id))
      setMsg(`已核准「${c.user_name || c.user_email}」的取消申請${res.refund_note ? '（' + res.refund_note + '）' : ''}`)
    } catch (e: any) {
      setErr(e?.message || '核准失敗，請稍後再試')
    } finally {
      setBusy('')
    }
  }

  async function reject(c: AdminCancelRequest) {
    if (!token || busy) return
    const note = window.prompt('駁回原因（將記錄於申請單，可留空）：', '')
    if (note === null) return
    setBusy(c.id); setErr(''); setMsg('')
    try {
      await adminCancelRequestsApi.reject(token, c.id, note)
      setRows((rs) => (rs ?? []).filter((x) => x.id !== c.id))
      setMsg(`已駁回「${c.user_name || c.user_email}」的取消申請`)
    } catch (e: any) {
      setErr(e?.message || '駁回失敗，請稍後再試')
    } finally {
      setBusy('')
    }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 6px', fontSize: 24, fontWeight: 800 }}>取消申請審核</h1>
      <p style={{ margin: '0 0 18px', color: 'var(--tx-dim)', fontSize: 13.5 }}>
        會員線上申請取消報名後會列在這裡等待審核。核准後系統會自動依申請當下鎖定的比例退款並釋出名額；駁回不會有任何金流或名額異動。
      </p>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0' }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13.5 }}>{msg}</div>}
      {!rows && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}
      {rows && rows.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>目前沒有待審核的取消申請 ✅</div>}

      {rows && rows.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
          {rows.map((c) => (
            <div key={c.id} style={{ padding: '14px 16px', borderBottom: '1px solid var(--line)' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap' }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontWeight: 700 }}>{c.user_name || '（未提供姓名）'}</div>
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{c.user_email}</div>
                </div>
                <div style={{ fontSize: 11, color: 'var(--tx-faint)', whiteSpace: 'nowrap' }}>申請時間 {fmtDt(c.created_at)}</div>
              </div>

              <div style={{ fontSize: 13, marginTop: 8 }}>🏁 {c.race_title || '（賽事資料缺失）'}</div>

              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0,1fr))', gap: 8, marginTop: 10 }}>
                <div style={statBox}>
                  <div style={statLabel}>訂單金額</div>
                  <div style={statValue}>{ntd(c.order_total_cents)}</div>
                </div>
                <div style={statBox}>
                  <div style={statLabel}>退費比例</div>
                  <div style={statValue}>{c.refund_ratio}%</div>
                </div>
                <div style={statBox}>
                  <div style={statLabel}>退費金額</div>
                  <div style={{ ...statValue, color: c.refund_amount_cents > 0 ? 'var(--fug)' : 'var(--tx-dim)' }}>{ntd(c.refund_amount_cents)}</div>
                </div>
              </div>

              <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 8 }}>距賽事開跑 {c.days_before_race} 天申請</div>
              <div style={{ fontSize: 13, marginTop: 6, color: 'var(--tx)', wordBreak: 'break-word' }}>
                申請原因：{c.reason?.trim() ? c.reason : '（未填寫）'}
              </div>

              <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
                <button onClick={() => approve(c)} disabled={busy === c.id} style={approveBtn}>
                  {busy === c.id ? '處理中…' : '✓ 核准'}
                </button>
                <button onClick={() => reject(c)} disabled={busy === c.id} style={rejectBtn}>✕ 駁回</button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

const statBox: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 10px', minWidth: 0,
}
const statLabel: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)' }
const statValue: React.CSSProperties = { fontSize: 14, fontWeight: 800, marginTop: 2, overflowWrap: 'break-word' }
const approveBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none',
  borderRadius: 9, padding: '7px 16px', cursor: 'pointer', fontSize: 13,
}
const rejectBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--hunt)', fontWeight: 700, border: '1px solid rgba(255,75,92,.5)',
  borderRadius: 9, padding: '7px 16px', cursor: 'pointer', fontSize: 13,
}
