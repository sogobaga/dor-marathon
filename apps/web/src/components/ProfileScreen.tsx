'use client'

import { useEffect, useState } from 'react'
import { profileApi, type Profile } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'

const GENDERS = [
  { v: '', t: '未填' },
  { v: 'male', t: '男' },
  { v: 'female', t: '女' },
  { v: 'other', t: '其他' },
]

export default function ProfileScreen({ onBack }: { onBack: () => void }) {
  const [p, setP] = useState<Profile | null>(null)
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    const token = getUserToken()
    if (!token) {
      setErr('請先登入')
      return
    }
    profileApi
      .getMe(token)
      .then((r) => setP(r.profile))
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [])

  function set<K extends keyof Profile>(k: K, v: Profile[K]) {
    setP((prev) => (prev ? { ...prev, [k]: v } : prev))
    setSaved(false)
  }

  async function save() {
    const token = getUserToken()
    if (!token || !p) return
    setErr('')
    setSaving(true)
    try {
      const res = await profileApi.updateMe(token, {
        real_name: p.real_name,
        nickname: p.nickname,
        phone: p.phone,
        address: p.address,
        birthday: p.birthday,
        gender: p.gender,
      })
      setP(res.profile)
      setSaved(true)
    } catch (e: any) {
      setErr(e?.message || '儲存失敗')
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
      </div>
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
