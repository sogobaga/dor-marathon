'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminInvoiceApi, type AdminInvoiceRow } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 電子發票（見 services/api/internal/einvoice，migration 169）狀態措辭；跟 admin/orders 頁的 EINVOICE_STATUS_LABEL
// 是同一份語意但各頁各自維護一份小 label map，沿用本專案既有慣例（不額外抽共用檔）。
const STATUS_LABEL: Record<string, { t: string; c: string }> = {
  pending: { t: '待處理', c: 'var(--tx-faint)' },
  issuing: { t: '開立中', c: 'var(--gold)' },
  issued: { t: '已開立', c: 'var(--fug)' },
  failed: { t: '失敗', c: 'var(--hunt)' },
  skipped: { t: '免開立', c: 'var(--tx-faint)' },
  void: { t: '已作廢', c: 'var(--tx-faint)' },
}
const BUYER_LABEL: Record<string, string> = {
  personal: '個人', company: '公司', donation: '捐贈',
}
const STATUS_TABS: { v: string; t: string }[] = [
  { v: '', t: '全部' },
  { v: 'pending', t: '待處理' },
  { v: 'issuing', t: '開立中' },
  { v: 'issued', t: '已開立' },
  { v: 'failed', t: '失敗' },
  { v: 'skipped', t: '免開立' },
  { v: 'void', t: '已作廢' },
]

function ntd(n: number) {
  return 'NT$ ' + Math.round(n || 0).toLocaleString('zh-TW')
}
function fmtDT(iso?: string | null) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function AdminInvoicesPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [status, setStatus] = useState('')
  const [rows, setRows] = useState<AdminInvoiceRow[] | null>(null)
  const [err, setErr] = useState('')

  const load = useCallback((t: string, st: string) => {
    setErr('')
    setRows(null)
    adminInvoiceApi.list(t, { status: st || undefined, limit: 200 })
      .then((r) => setRows(r.invoices))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
      })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
  }, [router])

  useEffect(() => { if (token) load(token, status) }, [token, status, load])

  return (
    <div>
      <h1 style={{ margin: '0 0 6px', fontSize: 24, fontWeight: 800 }}>電子發票</h1>
      <p style={{ margin: '0 0 18px', color: 'var(--tx-dim)', fontSize: 13.5 }}>
        訂單付款完成後向綠界開立的 B2C 電子發票總覽（見「系統設定 → 電子發票」開關自動開立）。點會員名稱可到訂單管理頁查看該筆訂單並操作開立／作廢／折讓。
      </p>

      <div style={{ display: 'flex', gap: 8, marginBottom: 16, flexWrap: 'wrap' }}>
        {STATUS_TABS.map((s) => (
          <button
            key={s.v}
            onClick={() => setStatus(s.v)}
            style={{
              ...tabBtn,
              background: status === s.v ? 'var(--fug)' : 'var(--bg-2)',
              color: status === s.v ? 'var(--fug-ink)' : 'var(--tx-dim)',
              borderColor: status === s.v ? 'var(--fug)' : 'var(--line-2)',
            }}
          >
            {s.t}
          </button>
        ))}
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0' }}>{err}</div>}
      {!rows && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}
      {rows && rows.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>沒有符合的發票紀錄</div>}

      {rows && rows.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
          <div style={{ overflowX: 'auto' }}>
            <div style={{ minWidth: 760 }}>
              <Row head>
                <C w={1.6}>會員</C><C w={0.9}>買受人</C><C w={1}>金額</C><C w={1}>狀態</C>
                <C w={1.3}>發票號碼</C><C w={1.6}>備註</C><C w={1}>更新時間</C>
              </Row>
              {rows.map((r) => {
                const st = STATUS_LABEL[r.invoice_status] ?? { t: r.invoice_status, c: 'var(--tx-dim)' }
                return (
                  <Row key={r.order_id}>
                    <C w={1.6}>
                      <Link href={`/admin/orders?order_id=${encodeURIComponent(r.order_id)}`} style={linkStyle}>
                        {r.user_name || '（未知會員）'}
                      </Link>
                    </C>
                    <C w={0.9}><span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{BUYER_LABEL[r.buyer_type] ?? r.buyer_type}</span></C>
                    <C w={1}>{ntd(r.amount_ntd)}</C>
                    <C w={1}><span style={{ color: st.c, fontWeight: 700 }}>{st.t}</span></C>
                    <C w={1.3}>
                      <span style={{ fontSize: 13 }}>{r.invoice_number || '—'}</span>
                      {r.invoice_date && <div style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{r.invoice_date}</div>}
                    </C>
                    <C w={1.6}>
                      {r.last_error ? (
                        <span style={{ fontSize: 11, color: 'var(--hunt)' }}>{r.last_error}</span>
                      ) : r.skip_reason ? (
                        <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>略過：{r.skip_reason}</span>
                      ) : (
                        <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>—</span>
                      )}
                    </C>
                    <C w={1}><span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{fmtDT(r.updated_at)}</span></C>
                  </Row>
                )
              })}
            </div>
          </div>
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
const tabBtn: React.CSSProperties = {
  border: '1px solid var(--line-2)', borderRadius: 999, padding: '6px 14px', fontSize: 13, fontWeight: 700,
  cursor: 'pointer', fontFamily: 'inherit',
}
const linkStyle: React.CSSProperties = {
  color: 'var(--tx)', fontWeight: 700, textDecoration: 'none',
}
