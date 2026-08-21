'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminEventCouponsApi,
  type EventCouponDef, type EventCouponDefWriteBody, type CouponExpiryMode,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 活動優惠券系統（migration 138）：券種管理（名稱/面額/使用期限）。中獎機率設定在各賽事「即時獎勵設定」
// 頁籤（RaceForm.tsx）選用此處建立的券種；玩家中獎後進「活動獎勵」錢包，報名時可折抵報名費。
// 設計見 memory activity-reward-system 活動優惠券擴充。

type DefForm = {
  id?: string
  name: string
  amount: string // 元（顯示用；送出時換算成分）
  expiry_mode: CouponExpiryMode
  expires_at: string // datetime-local；fixed 用
  valid_days: string  // days 用
  enabled: boolean
}
const EMPTY_DEF: DefForm = { name: '', amount: '', expiry_mode: 'days', expires_at: '', valid_days: '', enabled: true }

export default function AdminRewardCouponsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [defs, setDefs] = useState<EventCouponDef[] | null>(null)
  const [form, setForm] = useState<DefForm>(EMPTY_DEF)
  const [busy, setBusy] = useState(false)

  const loadDefs = useCallback(() => {
    const t = getToken()
    if (!t) return
    adminEventCouponsApi.list(t).then((r) => setDefs(r.defs)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入券種失敗')
    })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    loadDefs()
  }, [router, loadDefs])

  function editDef(d: EventCouponDef) {
    setForm({
      id: d.id,
      name: d.name,
      amount: String(Math.round(d.amount_cents / 100)),
      expiry_mode: d.expiry_mode,
      expires_at: d.expires_at ? toLocalInput(d.expires_at) : '',
      valid_days: d.valid_days != null ? String(d.valid_days) : '',
      enabled: d.enabled,
    })
    setErr(''); setMsg('')
  }
  function freshDef() {
    setForm(EMPTY_DEF)
    setErr(''); setMsg('')
  }

  async function saveDef() {
    if (!token) return
    const name = form.name.trim()
    if (!name) { setErr('請填券種名稱'); return }
    const amountCents = Math.round(parseFloat(form.amount || '0') * 100)
    if (!(amountCents > 0)) { setErr('面額需大於 0'); return }
    if (form.expiry_mode === 'fixed' && !form.expires_at) { setErr('指定到期日模式需選日期'); return }
    if (form.expiry_mode === 'days' && !(parseInt(form.valid_days || '0', 10) > 0)) { setErr('獲得後 N 天模式需填正整數天數'); return }

    setBusy(true); setErr(''); setMsg('')
    try {
      const body: EventCouponDefWriteBody = {
        name,
        amount_cents: amountCents,
        expiry_mode: form.expiry_mode,
        expires_at: form.expiry_mode === 'fixed' ? new Date(form.expires_at).toISOString() : null,
        valid_days: form.expiry_mode === 'days' ? parseInt(form.valid_days || '0', 10) : null,
        enabled: form.enabled,
      }
      if (form.id) {
        await adminEventCouponsApi.update(token, form.id, body)
      } else {
        await adminEventCouponsApi.create(token, body)
      }
      setMsg(`✓ 已儲存券種「${name}」`)
      setForm(EMPTY_DEF)
      loadDefs()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function deleteDef(d: EventCouponDef) {
    if (!token || !confirm(`確定刪除券種「${d.name}」？`)) return
    setErr(''); setMsg('')
    try {
      await adminEventCouponsApi.remove(token, d.id)
      setMsg('已刪除券種')
      if (form.id === d.id) freshDef()
      loadDefs()
    } catch (e: any) { setErr(e?.message || '刪除失敗（此券種可能已發放過，請改為停用）') }
  }

  async function toggleEnabled(d: EventCouponDef) {
    if (!token) return
    setErr(''); setMsg('')
    try {
      await adminEventCouponsApi.update(token, d.id, {
        name: d.name, amount_cents: d.amount_cents, expiry_mode: d.expiry_mode,
        expires_at: d.expires_at, valid_days: d.valid_days, enabled: !d.enabled,
      })
      loadDefs()
    } catch (e: any) { setErr(e?.message || '更新失敗') }
  }

  return (
    <div style={{ maxWidth: 900, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>活動優惠券管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        建立券種（名稱／面額／使用期限）；賽事「即時獎勵設定」頁籤可選用此處券種＋設定中獎機率，玩家中獎後進活動獎勵錢包、報名時可折抵報名費。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,320px) minmax(0,1fr)', gap: 16, alignItems: 'start' }}>
        {/* 列表 */}
        <div style={card}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 10 }}>
            <b style={{ fontSize: 13 }}>券種（{defs?.length ?? '—'}）</b>
            <button onClick={freshDef} style={primaryBtn}>＋ 新增</button>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            {defs?.map((d) => (
              <div key={d.id} onClick={() => editDef(d)} style={{ ...rowCard, borderColor: form.id === d.id ? 'var(--fug)' : 'var(--line)', cursor: 'pointer', opacity: d.enabled ? 1 : 0.55 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                  <div style={{ fontSize: 13, fontWeight: 700 }}>{d.name}</div>
                  <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--gold)' }}>NT$ {Math.round(d.amount_cents / 100)}</div>
                </div>
                <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 3 }}>
                  {d.expiry_mode === 'fixed' ? `到期日：${d.expires_at ? new Date(d.expires_at).toLocaleDateString('zh-TW') : '—'}` : `獲得後 ${d.valid_days} 天內可用`}
                </div>
                <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 3 }}>
                  已發放 {d.issued_count} · 已使用 {d.used_count}
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 6 }}>
                  <span style={{ fontSize: 11, fontWeight: 700, color: d.enabled ? 'var(--fug)' : 'var(--tx-faint)' }}>{d.enabled ? '啟用中' : '已停用'}</span>
                  <button onClick={(e) => { e.stopPropagation(); toggleEnabled(d) }} style={tinyBtn}>{d.enabled ? '停用' : '啟用'}</button>
                </div>
              </div>
            ))}
            {defs && defs.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>尚無券種，按「新增」建立。</div>}
          </div>
        </div>

        {/* 表單 */}
        <div style={{ ...card, ...innerCard }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 10 }}>
            <F label="券種名稱（必填）"><input style={inp} value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} placeholder="如：週年慶 100 元優惠券" /></F>
            <F label="面額（元，必填）"><input style={inp} type="number" min={1} step="1" value={form.amount} onChange={(e) => setForm((f) => ({ ...f, amount: e.target.value }))} placeholder="如：100" /></F>
          </div>

          <div style={{ marginTop: 12 }}>
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 6 }}>使用期限（兩種模式擇一）</div>
            <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', marginBottom: 8, fontSize: 13 }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer' }}>
                <input type="radio" name="expiryMode" checked={form.expiry_mode === 'fixed'} onChange={() => setForm((f) => ({ ...f, expiry_mode: 'fixed' }))} />
                指定到期日
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer' }}>
                <input type="radio" name="expiryMode" checked={form.expiry_mode === 'days'} onChange={() => setForm((f) => ({ ...f, expiry_mode: 'days' }))} />
                獲得後 N 天內可用
              </label>
            </div>
            {form.expiry_mode === 'fixed' ? (
              <F label="到期日"><input style={inp} type="datetime-local" value={form.expires_at} onChange={(e) => setForm((f) => ({ ...f, expires_at: e.target.value }))} /></F>
            ) : (
              <F label="天數"><input style={{ ...inp, maxWidth: 160 }} type="number" min={1} value={form.valid_days} onChange={(e) => setForm((f) => ({ ...f, valid_days: e.target.value }))} placeholder="如：30" /></F>
            )}
          </div>

          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, marginTop: 12 }}>
            <input type="checkbox" checked={form.enabled} onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))} />
            啟用（停用後不再出現於賽事即時獎勵設定的可選清單，中獎判定時也一律跳過）
          </label>

          <div style={{ display: 'flex', gap: 10, marginTop: 16 }}>
            <button onClick={saveDef} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.6 : 1 }}>{busy ? '儲存中…' : form.id ? '儲存修改' : '建立券種'}</button>
            {form.id && <button onClick={() => { const d = defs?.find((x) => x.id === form.id); if (d) deleteDef(d) }} style={dangerBtn}>刪除此券種</button>}
            {form.id && <button onClick={freshDef} style={ghostBtn}>取消編輯</button>}
          </div>
        </div>
      </div>
    </div>
  )
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

// ISO → datetime-local（本地時間）
function toLocalInput(iso: string): string {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const off = d.getTimezoneOffset() * 60000
  return new Date(d.getTime() - off).toISOString().slice(0, 16)
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 18 }
const innerCard: React.CSSProperties = { background: 'var(--bg-0, #0d0f14)' }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 11px', color: 'var(--tx)', fontSize: 13.5, width: '100%', boxSizing: 'border-box', fontFamily: 'inherit' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const dangerBtn: React.CSSProperties = { background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--hunt)', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit' }
const tinyBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', border: '1px solid var(--line-2)', borderRadius: 6, color: 'var(--tx)', cursor: 'pointer', fontSize: 11.5, padding: '3px 8px', fontFamily: 'inherit' }
const rowCard: React.CSSProperties = { background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line)', borderRadius: 8, padding: 10 }
