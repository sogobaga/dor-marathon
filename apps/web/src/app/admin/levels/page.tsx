'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminLevelsApi, type LevelConfig, type ExpRules } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

export default function AdminLevelsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [levels, setLevels] = useState<LevelConfig[] | null>(null)
  const [rules, setRules] = useState<ExpRules | null>(null)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminLevelsApi.levelConfig(t).then((r) => setLevels(r.levels)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
    adminLevelsApi.expRules(t).then((r) => setRules(r.exp_rules)).catch(() => {})
  }, [router])
  useEffect(() => { load() }, [load])

  function updLevel(i: number, patch: Partial<LevelConfig>) {
    setLevels((ls) => (ls ? ls.map((x, idx) => (idx === i ? { ...x, ...patch } : x)) : ls))
  }
  function addLevel() {
    setLevels((ls) => {
      const next = ls && ls.length ? Math.max(...ls.map((l) => l.level)) + 1 : 1
      return [...(ls ?? []), { level: next, title: '', exp_required: 0 }]
    })
  }
  async function saveLevels() {
    if (!token || !levels) return
    setSaving(true); setErr(''); setMsg('')
    try {
      const r = await adminLevelsApi.setLevelConfig(token, levels.map((l) => ({ ...l, level: Number(l.level), exp_required: Number(l.exp_required) })))
      setLevels(r.levels); setMsg('✓ 等級門檻已儲存')
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }
  async function saveRules() {
    if (!token || !rules) return
    setSaving(true); setErr(''); setMsg('')
    try {
      const r = await adminLevelsApi.setExpRules(token, { per_race: Number(rules.per_race), per_task: Number(rules.per_task) })
      setRules(r.exp_rules); setMsg('✓ EXP 規則已儲存')
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }

  return (
    <div style={{ maxWidth: 720, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 6px' }}>等級設定</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        會員透過參賽與完成任務獲得 EXP 升等。此處設定各等級門檻與 EXP 取得規則。（EXP 結算將於後續輪接上任務引擎）
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0' }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {/* EXP 規則 */}
      <div style={panel}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 12px' }}>EXP 取得規則</h2>
        {rules ? (
          <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <Field label="每場參賽 EXP">
              <input style={inp} type="number" value={rules.per_race} onChange={(e) => setRules({ ...rules, per_race: parseInt(e.target.value || '0', 10) })} />
            </Field>
            <Field label="每完成一個任務 EXP">
              <input style={inp} type="number" value={rules.per_task} onChange={(e) => setRules({ ...rules, per_task: parseInt(e.target.value || '0', 10) })} />
            </Field>
            <button onClick={saveRules} disabled={saving} style={primaryBtn}>儲存規則</button>
          </div>
        ) : <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
      </div>

      {/* 等級門檻 */}
      <div style={{ ...panel, marginTop: 16 }}>
        <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 12px' }}>等級門檻（累積 EXP）</h2>
        {!levels && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {levels?.map((l, i) => (
            <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'flex-end', flexWrap: 'wrap' }}>
              <Field label="等級"><input style={{ ...inp, width: 70 }} type="number" value={l.level} onChange={(e) => updLevel(i, { level: parseInt(e.target.value || '0', 10) })} /></Field>
              <Field label="名稱"><input style={{ ...inp, width: 130 }} value={l.title} onChange={(e) => updLevel(i, { title: e.target.value })} placeholder="例：菁英" /></Field>
              <Field label="所需累積 EXP"><input style={{ ...inp, width: 130 }} type="number" value={l.exp_required} onChange={(e) => updLevel(i, { exp_required: parseInt(e.target.value || '0', 10) })} /></Field>
              <button onClick={() => setLevels((ls) => (ls ? ls.filter((_, idx) => idx !== i) : ls))} style={{ ...ghostBtn, color: 'var(--hunt)' }}>移除</button>
            </div>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
          <button onClick={addLevel} style={ghostBtn}>＋ 新增等級</button>
          <button onClick={saveLevels} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存等級門檻'}</button>
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>提示：Level 1 的所需 EXP 應為 0；門檻需隨等級遞增。</div>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 18 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 11px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14 }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
