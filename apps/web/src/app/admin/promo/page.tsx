'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminRacesApi, adminPromoApi, type Race, type PromoCode, type PromoUsage } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
}
function discountText(p: PromoCode) {
  return p.discount_type === 'amount' ? `折 ${ntd(p.discount_value)}` : `折 ${p.discount_value}%`
}

export default function AdminPromoPage() {
  const router = useRouter()
  const [token, setTok] = useState<string | null>(null)
  const [races, setRaces] = useState<Race[]>([])
  const [codes, setCodes] = useState<PromoCode[] | null>(null)
  const [err, setErr] = useState('')
  const [created, setCreated] = useState<string[]>([])
  const [usagesFor, setUsagesFor] = useState<{ code: string; rows: PromoUsage[] } | null>(null)
  const [editing, setEditing] = useState<PromoCode | null>(null)

  // 建立表單
  const [discountType, setDiscountType] = useState<'amount' | 'percent'>('amount')
  const [value, setValue] = useState('100')
  const [quantity, setQuantity] = useState('1')
  const [code, setCode] = useState('')
  const [raceID, setRaceID] = useState('')
  const [maxUses, setMaxUses] = useState('')
  const [perUserOnce, setPerUserOnce] = useState(true)
  const [validUntil, setValidUntil] = useState('')
  const [targetEmail, setTargetEmail] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t)
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  const load = useCallback(() => {
    const t = getToken()
    if (!t) return
    setCodes(null)
    adminPromoApi.list(t).then((r) => setCodes(r.codes)).catch((e) => setErr(e?.message || '載入失敗'))
  }, [])

  useEffect(() => { load() }, [load])

  async function create() {
    if (!token) return
    setErr(''); setCreated([]); setSaving(true)
    try {
      const qty = Math.max(1, parseInt(quantity || '1', 10))
      const dv = discountType === 'amount'
        ? Math.round(parseFloat(value || '0') * 100) // 元 → 分
        : parseInt(value || '0', 10)
      const res = await adminPromoApi.create(token, {
        code: qty === 1 ? code.trim().toUpperCase() || undefined : undefined,
        discount_type: discountType,
        discount_value: dv,
        max_uses: maxUses ? parseInt(maxUses, 10) : null,
        per_user_once: perUserOnce,
        race_id: raceID || null,
        target_email: targetEmail.trim() || undefined,
        valid_until: validUntil ? new Date(validUntil).toISOString() : null,
        quantity: qty,
      })
      setCreated(res.codes.map((c) => c.code))
      setCode('')
      load()
    } catch (e: any) {
      setErr(e?.message || '建立失敗')
    } finally {
      setSaving(false)
    }
  }

  async function toggle(p: PromoCode) {
    if (!token) return
    try {
      await adminPromoApi.setActive(token, p.id, !p.active)
      setCodes((cs) => cs?.map((x) => x.id === p.id ? { ...x, active: !x.active } : x) ?? cs)
    } catch (e: any) { setErr(e?.message || '操作失敗') }
  }

  async function showUsages(p: PromoCode) {
    if (!token) return
    try {
      const { usages } = await adminPromoApi.usages(token, p.id)
      setUsagesFor({ code: p.code, rows: usages })
    } catch (e: any) { setErr(e?.message || '載入失敗') }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 18px', fontSize: 24, fontWeight: 800 }}>序號管理</h1>

      {/* 建立 */}
      <div style={{ border: '1px solid var(--line)', borderRadius: 14, padding: 18, marginBottom: 22 }}>
        <div style={{ fontWeight: 800, marginBottom: 14 }}>建立優惠序號</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
          <F label="折抵型態">
            <select style={inp} value={discountType} onChange={(e) => setDiscountType(e.target.value as any)}>
              <option value="amount">折抵金額 (NT$)</option>
              <option value="percent">折抵 %</option>
            </select>
          </F>
          <F label={discountType === 'amount' ? '折抵金額 (NT$)' : '折抵 %（1–100）'}>
            <input style={inp} type="number" value={value} onChange={(e) => setValue(e.target.value)} />
          </F>
          <F label="生成數量（>1=批次隨機碼）">
            <input style={inp} type="number" min={1} value={quantity} onChange={(e) => setQuantity(e.target.value)} />
          </F>
          {parseInt(quantity || '1', 10) === 1 && (
            <F label="自訂序號（留空自動產生）">
              <input style={inp} value={code} onChange={(e) => setCode(e.target.value.toUpperCase())} placeholder="如 NEWYEAR" />
            </F>
          )}
          <F label="適用賽事">
            <select style={inp} value={raceID} onChange={(e) => setRaceID(e.target.value)}>
              <option value="">全部賽事</option>
              {races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}
            </select>
          </F>
          <F label="總使用次數上限（空=不限）">
            <input style={inp} type="number" value={maxUses} onChange={(e) => setMaxUses(e.target.value)} />
          </F>
          <F label="有效期限（空=不限）">
            <input style={inp} type="datetime-local" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} />
          </F>
          <F label="指定帳號 Email（空=不限）">
            <input style={inp} value={targetEmail} onChange={(e) => setTargetEmail(e.target.value)} />
          </F>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--tx-dim)', alignSelf: 'flex-end', paddingBottom: 8 }}>
            <input type="checkbox" checked={perUserOnce} onChange={(e) => setPerUserOnce(e.target.checked)} />
            每帳號限用一次
          </label>
        </div>
        {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginTop: 10 }}>{err}</div>}
        {created.length > 0 && (
          <div style={{ marginTop: 12, fontSize: 13, color: 'var(--fug)' }}>
            ✓ 已建立 {created.length} 筆：<span style={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>{created.join(', ')}</span>
          </div>
        )}
        <button onClick={create} disabled={saving} style={{ ...primaryBtn, marginTop: 14 }}>{saving ? '建立中…' : '建立'}</button>
      </div>

      {/* 列表 */}
      {!codes && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}
      {codes && codes.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>尚無序號</div>}
      {codes && codes.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
          <Row head><C w={2}>序號</C><C w={1}>折抵</C><C w={2}>範圍/限制</C><C w={1}>使用</C><C w={1}>狀態</C></Row>
          {codes.map((p) => (
            <Row key={p.id}>
              <C w={2}>
                <button onClick={() => showUsages(p)} style={{ ...linkBtn, fontFamily: 'monospace' }}>{p.code}</button>
              </C>
              <C w={1}>{discountText(p)}</C>
              <C w={2} dim>
                {p.race_id ? '指定賽事' : '全賽事'}
                {p.per_user_once ? ' · 每人一次' : ''}
                {p.target_user_id ? ' · 指定帳號' : ''}
                {p.valid_until ? ` · 至 ${new Date(p.valid_until).toLocaleDateString('zh-TW')}` : ''}
              </C>
              <C w={1}>{p.used_count}{p.max_uses != null ? ` / ${p.max_uses}` : ''}</C>
              <C w={1}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <button onClick={() => toggle(p)} style={p.active ? activeBtn : inactiveBtn}>
                    {p.active ? '啟用中' : '已停用'}
                  </button>
                  <button onClick={() => setEditing(p)} style={linkBtn}>編輯</button>
                </div>
              </C>
            </Row>
          ))}
        </div>
      )}

      {/* 使用紀錄 */}
      {usagesFor && (
        <div style={overlay} onClick={() => setUsagesFor(null)}>
          <div style={panel} onClick={(e) => e.stopPropagation()}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 12 }}>
              <strong>使用紀錄 · {usagesFor.code}</strong>
              <button onClick={() => setUsagesFor(null)} style={linkBtn}>✕</button>
            </div>
            {usagesFor.rows.length === 0 ? (
              <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>尚無使用紀錄</div>
            ) : usagesFor.rows.map((u) => (
              <div key={u.id} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, padding: '6px 0', borderBottom: '1px solid var(--line)' }}>
                <span>{u.user_name}（{u.user_email}）· {u.race_title}</span>
                <span style={{ color: 'var(--tx-dim)' }}>折 {ntd(u.discount_cents)} · {new Date(u.used_at).toLocaleString('zh-TW')}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* 編輯序號 */}
      {editing && token && (
        <EditPromoModal
          token={token}
          races={races}
          code={editing}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); load() }}
        />
      )}
    </div>
  )
}

function EditPromoModal({
  token, races, code, onClose, onSaved,
}: {
  token: string
  races: Race[]
  code: PromoCode
  onClose: () => void
  onSaved: () => void
}) {
  const [discountType, setDiscountType] = useState<'amount' | 'percent'>(code.discount_type)
  const [value, setValue] = useState(String(code.discount_type === 'amount' ? code.discount_value / 100 : code.discount_value))
  const [raceID, setRaceID] = useState(code.race_id ?? '')
  const [maxUses, setMaxUses] = useState(code.max_uses != null ? String(code.max_uses) : '')
  const [perUserOnce, setPerUserOnce] = useState(code.per_user_once)
  const [validUntil, setValidUntil] = useState(code.valid_until ? toLocalInput(code.valid_until) : '')
  const [targetEmail, setTargetEmail] = useState(code.target_email ?? '')
  const [active, setActive] = useState(code.active)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  async function save() {
    setErr(''); setSaving(true)
    try {
      const dv = discountType === 'amount' ? Math.round(parseFloat(value || '0') * 100) : parseInt(value || '0', 10)
      await adminPromoApi.update(token, code.id, {
        discount_type: discountType,
        discount_value: dv,
        max_uses: maxUses ? parseInt(maxUses, 10) : null,
        per_user_once: perUserOnce,
        race_id: raceID || null,
        target_email: targetEmail.trim() || undefined,
        valid_until: validUntil ? new Date(validUntil).toISOString() : null,
        quantity: 1,
        active,
      })
      onSaved()
    } catch (e: any) {
      setErr(e?.message || '儲存失敗')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={overlay} onClick={onClose}>
      <div style={{ ...panel, maxWidth: 480 }} onClick={(e) => e.stopPropagation()}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
          <strong style={{ fontSize: 16 }}>編輯序號 · <span style={{ fontFamily: 'monospace' }}>{code.code}</span></strong>
          <button onClick={onClose} style={linkBtn}>✕</button>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
          <F label="折抵型態">
            <select style={inp} value={discountType} onChange={(e) => setDiscountType(e.target.value as any)}>
              <option value="amount">折抵金額 (NT$)</option>
              <option value="percent">折抵 %</option>
            </select>
          </F>
          <F label={discountType === 'amount' ? '折抵金額 (NT$)' : '折抵 %（1–100）'}>
            <input style={inp} type="number" value={value} onChange={(e) => setValue(e.target.value)} />
          </F>
          <F label="適用賽事">
            <select style={inp} value={raceID} onChange={(e) => setRaceID(e.target.value)}>
              <option value="">全部賽事</option>
              {races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}
            </select>
          </F>
          <F label="總使用次數上限（空=不限）">
            <input style={inp} type="number" value={maxUses} onChange={(e) => setMaxUses(e.target.value)} />
          </F>
          <F label="有效期限（空=不限）">
            <input style={inp} type="datetime-local" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} />
          </F>
          <F label="指定帳號 Email（空=不限）">
            <input style={inp} value={targetEmail} onChange={(e) => setTargetEmail(e.target.value)} />
          </F>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--tx-dim)' }}>
            <input type="checkbox" checked={perUserOnce} onChange={(e) => setPerUserOnce(e.target.checked)} /> 每帳號限用一次
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--tx-dim)' }}>
            <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} /> 啟用
          </label>
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 10 }}>
          序號本身與已使用次數（{code.used_count}）不可變更。
        </div>
        {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginTop: 8 }}>{err}</div>}
        <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end', marginTop: 14 }}>
          <button onClick={onClose} style={inactiveBtn}>取消</button>
          <button onClick={save} disabled={saving} style={{ background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 10, padding: '8px 18px', cursor: 'pointer', fontSize: 14 }}>
            {saving ? '儲存中…' : '儲存'}
          </button>
        </div>
      </div>
    </div>
  )
}

// ISO → datetime-local（本地時間）
function toLocalInput(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const off = d.getTimezoneOffset() * 60000
  return new Date(d.getTime() - off).toISOString().slice(0, 16)
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 150, flex: '1 1 150px' }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}
function Row({ children, head }: { children: React.ReactNode; head?: boolean }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderBottom: '1px solid var(--line)',
      background: head ? 'var(--bg-1)' : 'transparent', fontSize: head ? 11 : 14,
      color: head ? 'var(--tx-faint)' : 'var(--tx)', textTransform: head ? 'uppercase' : 'none',
    }}>{children}</div>
  )
}
function C({ children, w, dim }: { children: React.ReactNode; w: number; dim?: boolean }) {
  return <div style={{ flex: w, minWidth: 0, color: dim ? 'var(--tx-dim)' : undefined, fontSize: dim ? 12.5 : undefined }}>{children}</div>
}

const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '9px 11px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14,
}
const linkBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--fug)', cursor: 'pointer', fontSize: 14, padding: 0 }
const activeBtn: React.CSSProperties = { background: 'rgba(45,212,150,.12)', color: 'var(--fug)', border: '1px solid var(--fug)', borderRadius: 8, padding: '5px 10px', cursor: 'pointer', fontSize: 12 }
const inactiveBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 10px', cursor: 'pointer', fontSize: 12 }
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(0,0,0,.55)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 50, padding: 20 }
const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 20, width: '100%', maxWidth: 560, maxHeight: '80vh', overflowY: 'auto' }
