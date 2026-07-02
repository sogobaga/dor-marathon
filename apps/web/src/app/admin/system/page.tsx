'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminAppSettingsApi } from '@/lib/api'
import { SETTINGS_SPECS, type SettingSpec } from '@/lib/appSettings'
import { getToken, clearToken } from '@/lib/adminAuth'

export default function AdminSystemPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [values, setValues] = useState<Record<string, string>>({})
  const [edit, setEdit] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const seeded = useRef(false)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminAppSettingsApi.list(t).then((r) => {
      const s = r.settings || {}
      setValues(s)
      // 只在「首次載入」用伺服器值填入輸入框（之後各列自儲存後單獨同步；save 的 setValues 不會再觸發填入，
      // 避免儲存某列時洗掉其他列正在輸入但未儲存的值）。
      if (!seeded.current) {
        seeded.current = true
        const next: Record<string, string> = {}
        for (const spec of SETTINGS_SPECS) next[spec.key] = s[spec.key] ?? ''
        setEdit(next)
      }
    }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「系統設定」權限')
      else setErr(e?.message || '載入失敗')
    })
  }, [router])
  useEffect(() => { load() }, [load])

  async function save(spec: SettingSpec) {
    if (!token) return
    const key = spec.key
    const raw = (edit[key] ?? '').trim()
    let val: string
    if (spec.type === 'number') {
      const min = spec.min ?? 0, max = spec.max ?? 999999, def = parseFloat(spec.def) || 0
      val = raw === '' ? String(def) : String(Math.min(max, Math.max(min, Math.round(parseFloat(raw) || def))))
    } else {
      val = raw || spec.def
    }
    setBusy(key); setErr(''); setMsg('')
    try {
      const r = await adminAppSettingsApi.set(token, key, val)
      setValues(r.settings || {}); setEdit((s) => ({ ...s, [key]: val })); setMsg(`✓ 已儲存「${spec.label}」`)
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy('') }
  }

  const groups = Array.from(new Set(SETTINGS_SPECS.map((s) => s.group)))

  return (
    <div style={{ maxWidth: 760, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>系統設定</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px' }}>
        可調整的系統參數。修改後前台下次載入即生效（跑步中的使用者會在「下次開始跑步」或重整後套用）。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      {groups.map((g) => (
        <div key={g} style={{ marginTop: 18 }}>
          <div style={{ fontSize: 13, fontWeight: 800, letterSpacing: '.05em', marginBottom: 8 }}>{g}</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {SETTINGS_SPECS.filter((s) => s.group === g).map((s) => {
              const cur = values[s.key]
              const hasCur = cur != null && cur !== ''
              const curLabel = s.type === 'select'
                ? (s.options?.find((o) => o.value === (hasCur ? cur : s.def))?.label ?? (hasCur ? cur : s.def))
                : `${hasCur ? cur : s.def} ${s.unit ?? ''}`
              return (
                <div key={s.key} style={card}>
                  <div style={{ fontWeight: 800, fontSize: 15 }}>{s.label}</div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.7 }}>{s.help}</div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 8, flexWrap: 'wrap' }}>
                    {s.type === 'select' ? (
                      <select value={edit[s.key] || s.def} onChange={(e) => setEdit((st) => ({ ...st, [s.key]: e.target.value }))} style={{ ...inp, width: 260 }}>
                        {s.options?.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                      </select>
                    ) : (
                      <>
                        <input type="number" min={s.min} max={s.max} value={edit[s.key] ?? ''} placeholder={`預設 ${s.def}`}
                          onChange={(e) => setEdit((st) => ({ ...st, [s.key]: e.target.value }))} style={inp} />
                        <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{s.unit}</span>
                      </>
                    )}
                    <button onClick={() => save(s)} disabled={busy === s.key} style={{ ...primaryBtn, opacity: busy === s.key ? 0.5 : 1 }}>儲存</button>
                    <span style={{ fontSize: 11.5, color: hasCur ? 'var(--gold)' : 'var(--tx-faint)' }}>
                      {hasCur ? `目前：${curLabel}` : `使用預設：${curLabel}`}
                    </span>
                    {s.type === 'number' && <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>範圍 {s.min}–{s.max}</span>}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 14 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 9, padding: '7px 14px', cursor: 'pointer', fontSize: 12.5 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: 120 }
