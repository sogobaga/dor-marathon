'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminRacesApi, adminOrdersApi, type Race, type OrderRow, type OrderDetail } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const STATUS_LABEL: Record<string, { t: string; c: string }> = {
  paid: { t: '已付款', c: 'var(--fug)' },
  pending: { t: '待付款', c: 'var(--gold)' },
  cancelled: { t: '已取消', c: 'var(--tx-faint)' },
  refunded: { t: '已退款', c: 'var(--hunt)' },
}
const ITEM_LABEL: Record<string, string> = { entry: '報名費', addon: '加購' }

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
}

export default function AdminOrdersPage() {
  const router = useRouter()
  const [races, setRaces] = useState<Race[]>([])
  const [raceID, setRaceID] = useState('')
  const [status, setStatus] = useState('')
  const [rows, setRows] = useState<OrderRow[] | null>(null)
  const [err, setErr] = useState('')
  const [token, setTok] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Record<string, OrderDetail | null>>({})

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t)
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  const load = useCallback((rid: string, st: string) => {
    const t = getToken()
    if (!t) return
    setRows(null)
    adminOrdersApi.list(t, { race_id: rid || undefined, status: st || undefined })
      .then((r) => setRows(r.orders))
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [])

  useEffect(() => { load(raceID, status) }, [raceID, status, load])

  async function toggle(o: OrderRow) {
    if (expanded[o.id] !== undefined) {
      setExpanded((e) => { const n = { ...e }; delete n[o.id]; return n })
      return
    }
    if (!token) return
    try {
      const { order } = await adminOrdersApi.get(token, o.id)
      setExpanded((e) => ({ ...e, [o.id]: order }))
    } catch (e: any) { setErr(e?.message || '載入明細失敗') }
  }

  async function markPaid(o: OrderRow) {
    if (!token) return
    const ref = window.prompt(`標記訂單為已付款。\n可選填金流訂單號（payment_ref），留空亦可：`, '')
    if (ref === null) return
    try {
      await adminOrdersApi.markPaid(token, o.id, ref || undefined)
      setRows((rs) => rs?.map((x) => x.id === o.id ? { ...x, status: 'paid', payment_ref: ref || x.payment_ref } : x) ?? rs)
    } catch (e: any) { setErr(e?.message || '操作失敗') }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 18px', fontSize: 24, fontWeight: 800 }}>訂單管理</h1>

      <div style={{ display: 'flex', gap: 10, marginBottom: 18, flexWrap: 'wrap' }}>
        <select value={raceID} onChange={(e) => setRaceID(e.target.value)} style={{ ...inp, maxWidth: 260 }}>
          <option value="">全部賽事</option>
          {races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}
        </select>
        <select value={status} onChange={(e) => setStatus(e.target.value)} style={{ ...inp, maxWidth: 160 }}>
          <option value="">全部狀態</option>
          <option value="pending">待付款</option>
          <option value="paid">已付款</option>
          <option value="cancelled">已取消</option>
          <option value="refunded">已退款</option>
        </select>
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: 16 }}>{err}</div>}
      {!rows && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}
      {rows && rows.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>沒有符合的訂單</div>}

      {rows && rows.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
          <Row head><C w={2}>會員</C><C w={2}>賽事</C><C w={1}>金額</C><C w={1}>狀態</C><C w={1}>操作</C></Row>
          {rows.map((o) => {
            const st = STATUS_LABEL[o.status] ?? { t: o.status, c: 'var(--tx-dim)' }
            const det = expanded[o.id]
            return (
              <div key={o.id}>
                <Row>
                  <C w={2}>
                    <button onClick={() => toggle(o)} style={linkBtn}>
                      {expanded[o.id] !== undefined ? '▾ ' : '▸ '}{o.user_name}
                    </button>
                    <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{o.user_email}</div>
                  </C>
                  <C w={2}>{o.race_title}</C>
                  <C w={1}>{ntd(o.total_cents)}</C>
                  <C w={1}><span style={{ color: st.c }}>{st.t}</span></C>
                  <C w={1}>
                    {o.status !== 'paid'
                      ? <button onClick={() => markPaid(o)} style={payBtn}>標記已付</button>
                      : <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{o.payment_ref || '—'}</span>}
                  </C>
                </Row>
                {det && (
                  <div style={{ padding: '10px 16px 14px 32px', background: 'var(--bg-1)', borderBottom: '1px solid var(--line)' }}>
                    {det.items.map((it, i) => (
                      <div key={i} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, color: 'var(--tx-dim)', padding: '3px 0' }}>
                        <span>{ITEM_LABEL[it.item_type] ?? it.item_type}{it.addon_name ? `：${it.addon_name}` : ''} × {it.qty}</span>
                        <span>{ntd(it.subtotal_cents)}</span>
                      </div>
                    ))}
                    {det.paid_at && <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6 }}>付款時間：{new Date(det.paid_at).toLocaleString('zh-TW')}{det.payment_ref ? ` · 金流號 ${det.payment_ref}` : ''}</div>}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

function Row({ children, head }: { children: React.ReactNode; head?: boolean }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderBottom: '1px solid var(--line)',
      background: head ? 'var(--bg-1)' : 'transparent',
      fontSize: head ? 11 : 14, letterSpacing: head ? '.08em' : undefined,
      color: head ? 'var(--tx-faint)' : 'var(--tx)', textTransform: head ? 'uppercase' : 'none',
    }}>{children}</div>
  )
}
function C({ children, w }: { children: React.ReactNode; w: number }) {
  return <div style={{ flex: w, minWidth: 0 }}>{children}</div>
}

const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '10px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none',
  borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
}
const linkBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--tx)', cursor: 'pointer', fontSize: 14, fontWeight: 600, padding: 0,
}
