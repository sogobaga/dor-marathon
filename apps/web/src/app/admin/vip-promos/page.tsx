'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminVipPromosApi, type VipPromo } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const PLAN_LABEL: Record<string, string> = { monthly: '月繳', annual: '年繳', both: '月繳＋年繳' }

export default function AdminVipPromosPage() {
  const router = useRouter()
  const [token, setTok] = useState<string | null>(null)
  const [promos, setPromos] = useState<VipPromo[] | null>(null)
  const [err, setErr] = useState('')
  const [editing, setEditing] = useState<Partial<VipPromo> | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback((t: string) => {
    adminVipPromosApi.list(t).then((r) => setPromos(r.promos)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t); load(t)
  }, [router, load])

  const blank = (): Partial<VipPromo> => ({ name: '', plan: 'both', pay_pct: 70, starts_at: '', ends_at: '', active: true })
  const toLocal = (iso?: string | null) => (iso ? iso.slice(0, 16) : '')
  const fmtPeriod = (p: VipPromo) => `${p.starts_at ? p.starts_at.slice(0, 10) : '即刻'} ～ ${p.ends_at ? p.ends_at.slice(0, 10) : '不限'}`

  async function save() {
    if (!token || !editing) return
    if (!(editing.name || '').trim()) { setErr('請輸入優惠名稱'); return }
    setSaving(true); setErr('')
    try {
      await adminVipPromosApi.save(token, {
        id: editing.id,
        name: (editing.name || '').trim(),
        plan: (editing.plan || 'both') as VipPromo['plan'],
        pay_pct: Number(editing.pay_pct) || 0,
        starts_at: editing.starts_at || '',
        ends_at: editing.ends_at || '',
        active: !!editing.active,
      })
      setEditing(null); load(token)
    } catch (e: any) { setErr(e?.message || '儲存失敗') }
    finally { setSaving(false) }
  }
  async function del(id: string) {
    if (!token || !window.confirm('確定刪除此優惠檔期？')) return
    try { await adminVipPromosApi.del(token, id); load(token) } catch (e: any) { setErr(e?.message || '刪除失敗') }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 8px', fontSize: 24, fontWeight: 800 }}>訂閱優惠管理</h1>
      <p style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7, margin: '0 0 16px', maxWidth: 640 }}>
        設定 VIP 訂閱的促銷檔期與折扣。<b>實付%</b> 越低越優惠（70＝打七折、55＝5.5 折）。
        同一時段有多檔生效時，系統自動取「<b>最優惠</b>」的那一檔；也會與新註冊 14 天試用到期的<b>首購優惠</b>比較，取較優者。
      </p>

      {!editing && <button onClick={() => setEditing(blank())} style={primaryBtn}>＋ 新增優惠檔期</button>}
      {err && <div style={{ color: 'var(--hunt)', marginTop: 10, fontSize: 13 }}>{err}</div>}

      {editing && (
        <div style={card}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>{editing.id ? '編輯' : '新增'}優惠檔期</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12 }}>
            <F label="優惠名稱"><input style={inp} value={editing.name || ''} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder="例：週年慶 5 折" /></F>
            <F label="適用方案">
              <select style={inp} value={editing.plan} onChange={(e) => setEditing({ ...editing, plan: e.target.value as VipPromo['plan'] })}>
                <option value="both">月繳＋年繳</option>
                <option value="monthly">月繳</option>
                <option value="annual">年繳</option>
              </select>
            </F>
            <F label="實付%（1–100，越低越優惠）"><input style={inp} type="number" min={1} max={100} value={editing.pay_pct ?? 70} onChange={(e) => setEditing({ ...editing, pay_pct: Number(e.target.value) })} /></F>
            <F label="開始時間（留空＝即刻）"><input style={inp} type="datetime-local" value={toLocal(editing.starts_at)} onChange={(e) => setEditing({ ...editing, starts_at: e.target.value })} /></F>
            <F label="結束時間（留空＝不限）"><input style={inp} type="datetime-local" value={toLocal(editing.ends_at)} onChange={(e) => setEditing({ ...editing, ends_at: e.target.value })} /></F>
            <F label="狀態"><label style={{ display: 'flex', alignItems: 'center', gap: 8, paddingTop: 9, fontSize: 14 }}><input type="checkbox" checked={!!editing.active} onChange={(e) => setEditing({ ...editing, active: e.target.checked })} />生效中</label></F>
          </div>
          <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
            <button onClick={save} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存'}</button>
            <button onClick={() => { setEditing(null); setErr('') }} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      {promos === null ? (
        <div style={{ color: 'var(--tx-faint)', marginTop: 18 }}>載入中…</div>
      ) : promos.length === 0 ? (
        <div style={{ color: 'var(--tx-faint)', marginTop: 18 }}>尚無優惠檔期。</div>
      ) : (
        <div style={{ marginTop: 18, display: 'flex', flexDirection: 'column', gap: 10 }}>
          {promos.map((p) => (
            <div key={p.id} style={row}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 800 }}>{p.name}<span style={{ fontSize: 12, color: p.active ? 'var(--fug)' : 'var(--tx-faint)', marginLeft: 8, fontWeight: 700 }}>{p.active ? '● 生效' : '○ 停用'}</span></div>
                <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 3 }}>{PLAN_LABEL[p.plan] || p.plan} · 實付 {p.pay_pct}%（省 {100 - p.pay_pct}%） · {fmtPeriod(p)}</div>
              </div>
              <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                <button onClick={() => setEditing(p)} style={ghostBtn}>編輯</button>
                <button onClick={() => del(p.id)} style={{ ...ghostBtn, color: 'var(--hunt)' }}>刪除</button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 5, minWidth: 180, flex: '1 1 180px' }}>
      <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit', boxSizing: 'border-box' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 18px', cursor: 'pointer', fontSize: 14, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
const card: React.CSSProperties = { border: '1px solid var(--line)', borderRadius: 14, padding: 18, marginTop: 14, marginBottom: 4 }
const row: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12, border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px' }
