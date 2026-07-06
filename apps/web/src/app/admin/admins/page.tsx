'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminAccountsApi, adminMeApi, type AdminAccount, type AdminScope } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

type Draft = { login: string; password: string; name: string; is_super: boolean; permissions: string[] }

export default function AdminAdminsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [me, setMe] = useState<AdminAccount | null>(null)
  const [scopes, setScopes] = useState<AdminScope[]>([])
  const [admins, setAdmins] = useState<AdminAccount[] | null>(null)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  // 新增表單
  const [creating, setCreating] = useState(false)
  const [draft, setDraft] = useState<Draft>({ login: '', password: '', name: '', is_super: false, permissions: [] })
  // 編輯中（id → 編輯草稿）
  const [editId, setEditId] = useState<string | null>(null)
  const [edit, setEdit] = useState<{ name: string; password: string; is_super: boolean; permissions: string[] }>({ name: '', password: '', is_super: false, permissions: [] })

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminMeApi.get(t).then((r) => { setMe(r.admin); setScopes(r.scopes) }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
    })
    adminAccountsApi.list(t).then((r) => setAdmins(r.admins)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('僅超級管理員可管理管理者')
      else setErr(e?.message || '載入失敗')
    })
  }, [router])
  useEffect(() => { load() }, [load])

  const toggle = (list: string[], k: string) => list.includes(k) ? list.filter((x) => x !== k) : [...list, k]

  async function create() {
    if (!token) return
    setErr(''); setMsg(''); setBusy(true)
    try {
      await adminAccountsApi.create(token, draft)
      setMsg('✓ 已新增管理者'); setCreating(false)
      setDraft({ login: '', password: '', name: '', is_super: false, permissions: [] })
      load()
    } catch (e: any) { setErr(e?.message || '新增失敗') } finally { setBusy(false) }
  }
  function startEdit(a: AdminAccount) {
    setEditId(a.id); setErr(''); setMsg('')
    setEdit({ name: a.name, password: '', is_super: a.is_super, permissions: [...a.permissions] })
  }
  async function saveEdit(id: string) {
    if (!token) return
    setErr(''); setMsg(''); setBusy(true)
    try {
      await adminAccountsApi.update(token, id, edit)
      setMsg('✓ 已更新'); setEditId(null); load()
    } catch (e: any) { setErr(e?.message || '更新失敗') } finally { setBusy(false) }
  }
  async function remove(a: AdminAccount) {
    if (!token) return
    if (!confirm(`確定刪除管理者「${a.name}（${a.login}）」？此動作無法復原。`)) return
    setErr(''); setMsg(''); setBusy(true)
    try {
      await adminAccountsApi.remove(token, a.id)
      setMsg('✓ 已刪除'); load()
    } catch (e: any) { setErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  if (me && !me.is_super) {
    return <div style={{ color: 'var(--tx-dim)', padding: 20 }}>此頁僅超級管理員可存取。</div>
  }

  return (
    <div style={{ maxWidth: 860, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 10 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>管理者管理</h1>
          <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0 }}>新增／編輯／刪除後台管理者帳號與各模組權限。超級管理員擁有全部權限，並可管理管理者。</p>
        </div>
        {!creating && <button onClick={() => { setCreating(true); setEditId(null) }} style={primaryBtn}>＋ 新增管理者</button>}
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}

      {/* 新增表單 */}
      {creating && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>新增管理者</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label="登入帳號"><input style={inp} value={draft.login} onChange={(e) => setDraft({ ...draft, login: e.target.value })} placeholder="如 staff01" /></Field>
            <Field label="密碼（至少 4 碼）"><input style={inp} type="text" value={draft.password} onChange={(e) => setDraft({ ...draft, password: e.target.value })} /></Field>
            <Field label="顯示名稱"><input style={inp} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="留空用帳號" /></Field>
          </div>
          <PermPicker scopes={scopes} isSuper={draft.is_super} permissions={draft.permissions}
            onSuper={(v) => setDraft({ ...draft, is_super: v })}
            onToggle={(k) => setDraft({ ...draft, permissions: toggle(draft.permissions, k) })} />
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={create} disabled={busy} style={primaryBtn}>建立</button>
            <button onClick={() => setCreating(false)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      {/* 管理者清單 */}
      <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 10 }}>
        {!admins && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {admins?.map((a) => (
          <div key={a.id} style={card}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
              <div>
                <div style={{ fontWeight: 800, fontSize: 15 }}>
                  {a.name}
                  {a.is_super && <span style={superBadge}>超級管理員</span>}
                  {me?.id === a.id && <span style={{ ...superBadge, background: 'rgba(255,255,255,.08)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)' }}>你</span>}
                </div>
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 2 }}>帳號 {a.login}</div>
                {!a.is_super && (
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6 }}>
                    權限：{a.permissions.length ? a.permissions.map((k) => scopes.find((s) => s.key === k)?.label || k).join('、') : '（無）'}
                  </div>
                )}
              </div>
              {editId !== a.id && (
                <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                  <button onClick={() => startEdit(a)} style={ghostBtn}>編輯</button>
                  {me?.id !== a.id && <button onClick={() => remove(a)} style={{ ...ghostBtn, color: 'var(--hunt)' }}>刪除</button>}
                </div>
              )}
            </div>

            {editId === a.id && (
              <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
                <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
                  <Field label="顯示名稱"><input style={inp} value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} /></Field>
                  <Field label="重設密碼（留空不改）"><input style={inp} type="text" value={edit.password} onChange={(e) => setEdit({ ...edit, password: e.target.value })} placeholder="留空＝不變更" /></Field>
                </div>
                <PermPicker scopes={scopes} isSuper={edit.is_super} permissions={edit.permissions}
                  onSuper={(v) => setEdit({ ...edit, is_super: v })}
                  onToggle={(k) => setEdit({ ...edit, permissions: toggle(edit.permissions, k) })} />
                <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
                  <button onClick={() => saveEdit(a.id)} disabled={busy} style={primaryBtn}>儲存</button>
                  <button onClick={() => setEditId(null)} style={ghostBtn}>取消</button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function PermPicker({ scopes, isSuper, permissions, onSuper, onToggle }: {
  scopes: AdminScope[]; isSuper: boolean; permissions: string[]
  onSuper: (v: boolean) => void; onToggle: (k: string) => void
}) {
  return (
    <div style={{ marginTop: 12 }}>
      <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, cursor: 'pointer' }}>
        <input type="checkbox" checked={isSuper} onChange={(e) => onSuper(e.target.checked)} />
        <b>超級管理員</b><span style={{ color: 'var(--tx-faint)', fontSize: 12 }}>（擁有全部權限，並可管理其他管理者）</span>
      </label>
      {!isSuper && (
        <div style={{ marginTop: 10 }}>
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>可操作的功能模組</div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 8 }}>
            {scopes.map((s) => (
              <label key={s.key} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', cursor: 'pointer' }}>
                <input type="checkbox" checked={permissions.includes(s.key)} onChange={() => onToggle(s.key)} />
                {s.label}
              </label>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 150 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5 }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 14px', cursor: 'pointer', fontSize: 13 }
const superBadge: React.CSSProperties = { marginLeft: 8, fontSize: 10.5, fontWeight: 700, color: 'var(--gold)', background: 'rgba(245,194,66,.12)', border: '1px solid rgba(245,194,66,.4)', borderRadius: 999, padding: '2px 8px', verticalAlign: 'middle' }
