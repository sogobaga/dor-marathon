'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminRacesApi, adminOrdersApi, adminPaymentsApi, type Race, type OrderRow, type OrderDetail, type RefundRow, type EcpayEnvCheck } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const REFUND_STATUS_LABEL: Record<string, { t: string; c: string }> = {
  pending: { t: '處理中', c: 'var(--gold)' },
  success: { t: '已退款（API）', c: 'var(--fug)' },
  failed: { t: '失敗', c: 'var(--hunt)' },
  manual_required: { t: '待人工處理', c: 'var(--gold)' },
  manual_done: { t: '已人工退款', c: 'var(--fug)' },
}

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
  const [refunds, setRefunds] = useState<Record<string, RefundRow[]>>({})
  const [busy, setBusy] = useState<string>('') // 進行中的 orderID/refundID，避免重複點擊
  const [envCheck, setEnvCheck] = useState<EcpayEnvCheck | null>(null)
  const [envCheckErr, setEnvCheckErr] = useState('')

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t)
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
    adminPaymentsApi.envCheck(t).then(setEnvCheck).catch((e) => setEnvCheckErr(e?.message || '金流環境診斷載入失敗'))
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
      loadRefunds(o.id)
    } catch (e: any) { setErr(e?.message || '載入明細失敗') }
  }

  const loadRefunds = useCallback((orderID: string) => {
    const t = getToken()
    if (!t) return
    adminPaymentsApi.listRefunds(t, orderID)
      .then((r) => setRefunds((rs) => ({ ...rs, [orderID]: r.refunds })))
      .catch(() => {})
  }, [])

  async function markPaid(o: OrderRow) {
    if (!token) return
    const ref = window.prompt(`標記訂單為已付款。\n可選填金流訂單號（payment_ref），留空亦可：`, '')
    if (ref === null) return
    try {
      await adminOrdersApi.markPaid(token, o.id, ref || undefined)
      setRows((rs) => rs?.map((x) => x.id === o.id ? { ...x, status: 'paid', payment_ref: ref || x.payment_ref } : x) ?? rs)
    } catch (e: any) { setErr(e?.message || '操作失敗') }
  }

  async function refundOrder(o: OrderRow) {
    if (!token || busy) return
    const amountStr = window.prompt(
      `退款金額（新台幣整數，NT$ ${Math.round(o.total_cents / 100).toLocaleString('zh-TW')} 為訂單總額）。\n留空＝退還剩餘可退全額：`,
      ''
    )
    if (amountStr === null) return
    const reason = window.prompt('退款原因（必填）：', '')
    if (reason === null) return
    if (!reason.trim()) { setErr('退款原因為必填'); return }
    let amountCents: number | undefined
    if (amountStr.trim()) {
      const n = Number(amountStr.trim())
      if (!Number.isFinite(n) || n <= 0) { setErr('退款金額格式錯誤'); return }
      amountCents = Math.round(n * 100)
    }
    if (!window.confirm(`確定要對此訂單退款嗎？\n金額：${amountCents ? 'NT$ ' + Math.round(amountCents / 100).toLocaleString('zh-TW') : '剩餘可退全額'}\n原因：${reason}`)) return

    setBusy(o.id)
    try {
      const res = await adminPaymentsApi.createRefund(token, { order_id: o.id, amount_cents: amountCents, reason: reason.trim() })
      if (res.status === 'manual_required') {
        window.alert(`已建立人工退款紀錄，請完成銀行匯款後回來標記「已完成」。\n${res.note || ''}`)
      }
      const { order } = await adminOrdersApi.get(token, o.id)
      setExpanded((e) => ({ ...e, [o.id]: order }))
      setRows((rs) => rs?.map((x) => x.id === o.id ? { ...x, status: order.status } : x) ?? rs)
      loadRefunds(o.id)
    } catch (e: any) {
      setErr(e?.message || '退款操作失敗')
    } finally {
      setBusy('')
    }
  }

  async function manualDone(orderID: string, refundID: string) {
    if (!token || busy) return
    if (!window.confirm('確認已完成人工匯款，標記此筆退款為「已完成」？')) return
    setBusy(refundID)
    try {
      await adminPaymentsApi.markRefundManualDone(token, refundID)
      const { order } = await adminOrdersApi.get(token, orderID)
      setExpanded((e) => ({ ...e, [orderID]: order }))
      setRows((rs) => rs?.map((x) => x.id === orderID ? { ...x, status: order.status } : x) ?? rs)
      loadRefunds(orderID)
    } catch (e: any) {
      setErr(e?.message || '操作失敗')
    } finally {
      setBusy('')
    }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 18px', fontSize: 24, fontWeight: 800 }}>訂單管理</h1>

      {envCheckErr && <div style={{ color: 'var(--hunt)', padding: 12, marginBottom: 14 }}>金流環境診斷載入失敗：{envCheckErr}</div>}
      {envCheck && (
        <div style={{
          border: `1px solid ${envCheck.would_charge_real_money ? 'var(--hunt)' : 'var(--line)'}`,
          borderRadius: 14, padding: 16, marginBottom: 18, background: 'var(--bg-1)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 11, fontWeight: 700, letterSpacing: '.08em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>金流環境診斷</span>
            <span style={{
              background: envCheck.would_charge_real_money ? 'var(--hunt)' : 'var(--fug)',
              color: '#fff', fontWeight: 800, fontSize: 12, padding: '3px 10px', borderRadius: 999,
            }}>
              {envCheck.would_charge_real_money ? '⚠ 正式特店（會收真錢）' : '測試特店（不會收真錢）'}
            </span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: '4px 20px', fontSize: 13, color: 'var(--tx-dim)' }}>
            <div>結帳網址：<span style={{ color: 'var(--tx)' }}>{envCheck.resolved_action_url}</span></div>
            <div>特店編號：<span style={{ color: 'var(--tx)' }}>{envCheck.resolved_merchant_id || '（空）'}</span></div>
            <div>全域 ECPAY_ENV：<span style={{ color: 'var(--tx)' }}>{envCheck.global_ecpay_env}</span></div>
            <div>正式網域白名單：<span style={{ color: 'var(--tx)' }}>{envCheck.prod_hosts.join('、') || '（空）'}</span></div>
            <div>Host：<span style={{ color: 'var(--tx)' }}>{envCheck.seen.host || '（空）'}</span></div>
            <div>X-Forwarded-Host：<span style={{ color: 'var(--tx)' }}>{envCheck.seen.x_forwarded_host || '（空）'}</span></div>
            <div>解析採用 Host：<span style={{ color: 'var(--tx)' }}>{envCheck.seen.resolved_host || '（空）'}</span></div>
            <div>
              正式三寶已設定：{' '}
              <span style={{ color: envCheck.prod_credentials_configured.merchant_id ? 'var(--fug)' : 'var(--hunt)' }}>MerchantID {envCheck.prod_credentials_configured.merchant_id ? '✓' : '✗'}</span>{' '}
              <span style={{ color: envCheck.prod_credentials_configured.hash_key ? 'var(--fug)' : 'var(--hunt)' }}>HashKey {envCheck.prod_credentials_configured.hash_key ? '✓' : '✗'}</span>{' '}
              <span style={{ color: envCheck.prod_credentials_configured.hash_iv ? 'var(--fug)' : 'var(--hunt)' }}>HashIV {envCheck.prod_credentials_configured.hash_iv ? '✓' : '✗'}</span>
            </div>
          </div>
        </div>
      )}

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
                    {o.status === 'pending'
                      ? <button onClick={() => markPaid(o)} style={payBtn}>標記已付</button>
                      : o.status === 'paid'
                        ? <button onClick={() => refundOrder(o)} disabled={busy === o.id} style={refundBtn}>{busy === o.id ? '處理中…' : '退款'}</button>
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
                    {(refunds[o.id]?.length ?? 0) > 0 && (
                      <div style={{ marginTop: 10, borderTop: '1px solid var(--line)', paddingTop: 8 }}>
                        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 4, letterSpacing: '.05em', textTransform: 'uppercase' }}>退款紀錄</div>
                        {refunds[o.id]!.map((rf) => {
                          const rst = REFUND_STATUS_LABEL[rf.status] ?? { t: rf.status, c: 'var(--tx-dim)' }
                          return (
                            <div key={rf.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, fontSize: 12, color: 'var(--tx-dim)', padding: '4px 0' }}>
                              <span>
                                {ntd(rf.amount_cents)} · {rf.method === 'api' ? '信用卡 API' : '人工'} ·{' '}
                                <span style={{ color: rst.c }}>{rst.t}</span>
                                {rf.reason ? ` · ${rf.reason}` : ''}
                                {rf.ecpay_rtn_msg ? ` · ${rf.ecpay_rtn_msg}` : ''}
                              </span>
                              {rf.status === 'manual_required' && (
                                <button onClick={() => manualDone(o.id, rf.id)} disabled={busy === rf.id} style={smallBtn}>
                                  {busy === rf.id ? '處理中…' : '標記已完成'}
                                </button>
                              )}
                            </div>
                          )
                        })}
                      </div>
                    )}
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
const refundBtn: React.CSSProperties = {
  background: 'var(--hunt)', color: '#fff', fontWeight: 700, border: 'none',
  borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
}
const smallBtn: React.CSSProperties = {
  background: 'var(--bg-2)', color: 'var(--tx)', fontWeight: 600, border: '1px solid var(--line-2)',
  borderRadius: 6, padding: '3px 8px', cursor: 'pointer', fontSize: 11, whiteSpace: 'nowrap',
}
const linkBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--tx)', cursor: 'pointer', fontSize: 14, fontWeight: 600, padding: 0,
}
