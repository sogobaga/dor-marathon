'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminTaskModulesApi, type TaskModule } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { TaskItemEditor, taskSummary, emptyTask, type TaskFields } from '../TaskItemEditor'

type Draft = { id?: string; name: string; description: string; is_system: boolean; items: TaskFields[] }

function toDraft(m: TaskModule): Draft {
  return {
    id: m.id, name: m.name, description: m.description ?? '', is_system: m.is_system,
    items: m.items.map((it) => ({
      metric_type: it.metric_type, target_value: it.target_value ?? null,
      range_lo: it.range_lo ?? null, range_hi: it.range_hi ?? null,
      title: it.title, description: it.description ?? '',
    })),
  }
}

export default function AdminTaskModulesPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [modules, setModules] = useState<TaskModule[] | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminTaskModulesApi.list(t)
      .then((r) => setModules(r.modules))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else setErr(e?.message || '載入失敗')
      })
  }, [router])

  useEffect(() => { load() }, [load])

  function startNew() {
    setErr('')
    setDraft({ name: '', description: '', is_system: false, items: [emptyTask()] })
  }
  function updateItem(i: number, patch: Partial<TaskFields>) {
    setDraft((d) => (d ? { ...d, items: d.items.map((x, idx) => (idx === i ? { ...x, ...patch } : x)) } : d))
  }
  async function save() {
    if (!token || !draft) return
    if (!draft.name.trim()) { setErr('請輸入任務模組名稱'); return }
    setSaving(true); setErr('')
    try {
      const body = {
        name: draft.name.trim(),
        description: draft.description.trim() || undefined,
        items: draft.items
          .filter((it) => it.title.trim() || true)
          .map((it, idx) => ({ ...it, title: it.title.trim(), display_order: idx })),
      }
      if (draft.id) await adminTaskModulesApi.update(token, draft.id, body)
      else await adminTaskModulesApi.create(token, body)
      setDraft(null)
      load()
    } catch (e: any) {
      setErr(e?.message || '儲存失敗')
    } finally {
      setSaving(false)
    }
  }
  async function remove(id: string) {
    if (!token) return
    if (!window.confirm('確定刪除此任務模組？')) return
    try {
      await adminTaskModulesApi.remove(token, id)
      if (draft?.id === id) setDraft(null)
      load()
    } catch (e: any) {
      setErr(e?.message || '刪除失敗')
    }
  }

  return (
    <div style={{ maxWidth: 760, margin: '0 auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', margin: '0 0 6px' }}>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: 0 }}>賽事任務</h1>
        {!draft && <button onClick={startNew} style={primaryBtn}>＋ 新增任務模組</button>}
      </div>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0 }}>
        任務模組是可重複取用的任務組合，建立後可在各賽事的「任務」分頁快速套用到賽事集體 / 分組團體 / 分組個人任務。
      </p>

      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0' }}>{err}</div>}

      {draft ? (
        <div style={panel}>
          <h2 style={{ fontSize: 17, fontWeight: 800, margin: '0 0 12px' }}>{draft.id ? '編輯任務模組' : '新增任務模組'}</h2>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <Field label="模組名稱">
              <input style={inp} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="例：入門挑戰" />
            </Field>
            <Field label="說明（選填）">
              <input style={inp} value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />
            </Field>
          </div>

          <h3 style={{ fontSize: 14, fontWeight: 700, margin: '16px 0 8px' }}>任務項目（{draft.items.length}）</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {draft.items.map((it, i) => (
              <TaskItemEditor
                key={i} value={it}
                onChange={(patch) => updateItem(i, patch)}
                onRemove={() => setDraft({ ...draft, items: draft.items.filter((_, idx) => idx !== i) })}
              />
            ))}
          </div>
          <button onClick={() => setDraft({ ...draft, items: [...draft.items, emptyTask()] })} style={ghostBtn}>＋ 新增任務項目</button>

          <div style={{ display: 'flex', gap: 10, marginTop: 18 }}>
            <button onClick={save} disabled={saving} style={{ ...primaryBtn, opacity: saving ? 0.6 : 1 }}>{saving ? '儲存中…' : '儲存模組'}</button>
            <button onClick={() => { setDraft(null); setErr('') }} style={ghostBtn}>取消</button>
            {draft.id && !draft.is_system && (
              <button onClick={() => remove(draft.id!)} style={{ ...ghostBtn, color: 'var(--hunt)', marginLeft: 'auto' }}>刪除模組</button>
            )}
          </div>
        </div>
      ) : !modules ? (
        <div style={{ color: 'var(--tx-dim)', padding: 20 }}>載入中…</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          {modules.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 20 }}>尚無任務模組，點右上「新增任務模組」建立。</div>}
          {modules.map((m) => (
            <div key={m.id} style={panel}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 15, fontWeight: 700 }}>
                    {m.name}
                    {m.is_system && <span style={badge}>系統</span>}
                  </div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>{m.description || `${m.items.length} 項任務`}</div>
                </div>
                <div style={{ display: 'flex', gap: 10, flexShrink: 0 }}>
                  <button onClick={() => { setErr(''); setDraft(toDraft(m)) }} style={{ color: 'var(--fug)', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13 }}>編輯</button>
                  {!m.is_system && <button onClick={() => remove(m.id)} style={{ color: 'var(--hunt)', background: 'none', border: 'none', cursor: 'pointer', fontSize: 13 }}>刪除</button>}
                </div>
              </div>
              {m.items.length > 0 && (
                <ul style={{ margin: '10px 0 0', paddingLeft: 18, fontSize: 12.5, color: 'var(--tx-dim)' }}>
                  {m.items.map((it) => <li key={it.id ?? it.title}>{taskSummary(it as TaskFields)}</li>)}
                </ul>
              )}
            </div>
          ))}
        </div>
      )}
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
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8,
  padding: '9px 11px', color: 'var(--tx)', fontSize: 14, fontFamily: 'inherit', width: '100%',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14,
}
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx)', border: '1px dashed var(--line-2)',
  borderRadius: 10, padding: '10px 16px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit',
}
const badge: React.CSSProperties = {
  marginLeft: 8, fontSize: 10, fontWeight: 600, color: 'var(--tx-faint)',
  border: '1px solid var(--line-2)', borderRadius: 999, padding: '1px 7px',
}
