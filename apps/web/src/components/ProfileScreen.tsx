'use client'

import { useEffect, useState } from 'react'
import { profileApi, type Profile, type MyRegistration, type MyOrder } from '@/lib/api'
import { getUserToken, withUserAuth, SessionExpiredError } from '@/lib/userAuth'

const GENDERS = [
  { v: '', t: '未填' },
  { v: 'male', t: '男' },
  { v: 'female', t: '女' },
  { v: 'other', t: '其他' },
]
const REG_STATUS: Record<string, { t: string; c: string }> = {
  paid: { t: '報名完成', c: 'var(--fug)' },
  pending: { t: '待繳費', c: 'var(--gold)' },
  cancelled: { t: '已取消', c: 'var(--tx-faint)' },
}
const ITEM_LABEL: Record<string, string> = { entry: '報名費', addon: '加購', discount: '優惠折抵' }

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
}

export default function ProfileScreen({ onBack, focusRaceID }: { onBack: () => void; focusRaceID?: string }) {
  const [p, setP] = useState<Profile | null>(null)
  const [regs, setRegs] = useState<MyRegistration[] | null>(null)
  const [payOrder, setPayOrder] = useState<MyOrder | null>(null)
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (!getUserToken()) {
      setErr('請先登入')
      return
    }
    withUserAuth((t) => profileApi.getMe(t))
      .then((r) => setP(r.profile))
      .catch((e) => setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '載入失敗'))

    withUserAuth((t) => profileApi.registrations(t))
      .then((r) => {
        setRegs(r.registrations)
        // 從「前往繳費」進來：自動開啟該賽事待繳費訂單
        if (focusRaceID) {
          const target = r.registrations.find((x) => x.race_id === focusRaceID && x.status === 'pending' && x.order_id)
          if (target?.order_id) openPay(target.order_id)
        }
      })
      .catch(() => {})
  }, [focusRaceID])

  async function openPay(orderID: string) {
    try {
      const { order } = await withUserAuth((t) => profileApi.order(t, orderID))
      setPayOrder(order)
    } catch (e: any) {
      setErr(e?.message || '載入繳費資訊失敗')
    }
  }

  function set<K extends keyof Profile>(k: K, v: Profile[K]) {
    setP((prev) => (prev ? { ...prev, [k]: v } : prev))
    setSaved(false)
  }

  async function save() {
    if (!p) return
    setErr('')
    setSaving(true)
    try {
      const res = await withUserAuth((t) =>
        profileApi.updateMe(t, {
          real_name: p.real_name, nickname: p.nickname, phone: p.phone,
          address: p.address, birthday: p.birthday, gender: p.gender,
        })
      )
      setP(res.profile)
      setSaved(true)
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '儲存失敗')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: '52px 22px 14px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 2px', fontSize: 23, fontWeight: 800, color: 'var(--tx)' }}>個人資訊</h1>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>報名時會自動帶入這些資料</div>
      </header>

      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 18px 28px' }}>
        {err && <div style={{ color: 'var(--hunt)', padding: 16, fontSize: 13 }}>{err}</div>}
        {!p && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}

        {p && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            <Field label="Email（Google 帳號）"><input style={{ ...inp, opacity: 0.6 }} value={p.email} disabled /></Field>
            <Field label="真實姓名"><input style={inp} value={p.real_name} onChange={(e) => set('real_name', e.target.value)} /></Field>
            <Field label="暱稱"><input style={inp} value={p.nickname} onChange={(e) => set('nickname', e.target.value)} /></Field>
            <Field label="手機"><input style={inp} value={p.phone} onChange={(e) => set('phone', e.target.value)} /></Field>
            <Field label="地址"><input style={inp} value={p.address} onChange={(e) => set('address', e.target.value)} /></Field>
            <div style={{ display: 'flex', gap: 12 }}>
              <Field label="生日"><input style={inp} type="date" value={p.birthday} onChange={(e) => set('birthday', e.target.value)} /></Field>
              <Field label="性別">
                <select style={inp} value={p.gender} onChange={(e) => set('gender', e.target.value as Profile['gender'])}>
                  {GENDERS.map((g) => <option key={g.v} value={g.v}>{g.t}</option>)}
                </select>
              </Field>
            </div>
            {saved && <div style={{ color: 'var(--fug)', fontSize: 13 }}>✓ 已儲存</div>}
            <button onClick={save} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存'}</button>
          </div>
        )}

        {/* 報名紀錄 */}
        <div style={{ marginTop: 28 }}>
          <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', marginBottom: 10 }}>報名紀錄</div>
          {!regs && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>載入中…</div>}
          {regs && regs.length === 0 && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>尚無報名紀錄</div>}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {regs?.map((r) => {
              const st = REG_STATUS[r.status] ?? { t: r.status, c: 'var(--tx-dim)' }
              return (
                <div key={r.registration_id} style={recCard}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8 }}>
                    <div>
                      <div style={{ fontWeight: 700, fontSize: 15 }}>{r.race_title}</div>
                      <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 2 }}>
                        {r.group_revealed ? (r.group_name || '—') : '分組賽事當天公布'}
                      </div>
                    </div>
                    <span style={{ fontSize: 12, color: st.c, fontWeight: 700, flexShrink: 0 }}>{st.t}</span>
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 10 }}>
                    <span style={{ fontSize: 13, color: 'var(--tx-dim)' }}>應繳 {ntd(r.order_total_cents)}</span>
                    {r.status === 'pending' && r.order_id && (
                      <button onClick={() => openPay(r.order_id!)} style={payBtn}>前往繳費</button>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </div>

      {/* 繳費頁面 */}
      {payOrder && (
        <div style={overlay} onClick={() => setPayOrder(null)}>
          <div style={panel} onClick={(e) => e.stopPropagation()}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
              <strong style={{ fontSize: 17 }}>繳費</strong>
              <button onClick={() => setPayOrder(null)} style={backBtn}>✕</button>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, marginBottom: 10 }}>{payOrder.race_title}</div>

            <div style={{ border: '1px solid var(--line-2)', borderRadius: 12, padding: 14, marginBottom: 14 }}>
              {payOrder.items.map((it, i) => (
                <div key={i} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, color: 'var(--tx-dim)', padding: '3px 0' }}>
                  <span>{ITEM_LABEL[it.item_type] ?? it.item_type}{it.addon_name ? `：${it.addon_name}` : ''}{it.qty > 1 ? ` × ${it.qty}` : ''}</span>
                  <span style={{ color: it.subtotal_cents < 0 ? 'var(--fug)' : 'var(--tx)' }}>{ntd(it.subtotal_cents)}</span>
                </div>
              ))}
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 15, fontWeight: 800, marginTop: 8, paddingTop: 8, borderTop: '1px solid var(--line)' }}>
                <span>應繳金額</span><span style={{ color: 'var(--gold)' }}>{ntd(payOrder.total_cents)}</span>
              </div>
            </div>

            {payOrder.status === 'paid' ? (
              <div style={{ color: 'var(--fug)', fontSize: 14, fontWeight: 700 }}>✓ 已完成繳費</div>
            ) : (
              <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
                線上金流串接中。目前可透過<strong style={{ color: 'var(--tx)' }}>優惠序號</strong>於報名時折抵，或聯繫主辦單位完成繳費；
                主辦確認後狀態將更新為「報名完成」。
                <div style={{ marginTop: 8, fontSize: 11, color: 'var(--tx-faint)' }}>訂單編號：{payOrder.id}</div>
              </div>
            )}
            <button onClick={() => setPayOrder(null)} style={{ ...primaryBtn, width: '100%', marginTop: 16 }}>關閉</button>
          </div>
        </div>
      )}
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
      <span style={{ fontSize: 11, letterSpacing: '.08em', color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '11px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 10, padding: '12px 20px', cursor: 'pointer', fontSize: 14, marginTop: 4,
}
const recCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14 }
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#1a1200', fontWeight: 700, border: 'none',
  borderRadius: 9, padding: '7px 14px', cursor: 'pointer', fontSize: 13,
}
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 60, padding: 20 }
const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 20, width: '100%', maxWidth: 420 }
