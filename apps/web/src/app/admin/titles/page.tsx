'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminTitlesApi, type AdminTitle, type TitleCategory, type TitleCategoryMeta } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 稱號管理：title_defs CRUD。稱號依 9 個固定 category 分組顯示——
// checkAndAwardTitles 只認得這 9 類（見後端 titleCategoryLabels），故 category 一律用下拉選、不可自由輸入，
// 否則稱號永遠不會被計算、玩家也解不開。

type Form = Partial<AdminTitle>

function emptyForm(categories: TitleCategoryMeta[]): Form {
  return {
    code: '', category: (categories[0]?.key as TitleCategory) ?? 'single_dist',
    threshold: 0, unit: '', name: '', tier: 1, sort_order: 0, enabled: true,
  }
}

export default function AdminTitlesPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [titles, setTitles] = useState<AdminTitle[] | null>(null)
  const [categories, setCategories] = useState<TitleCategoryMeta[]>([])
  const [form, setForm] = useState<Form | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminTitlesApi.list(t)
      .then((r) => { setTitles(r.titles); setCategories(r.categories) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「稱號管理」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  function startNew() { setForm(emptyForm(categories)); setIsNew(true); setErr(''); setMsg('') }
  function startEdit(t: AdminTitle) { setForm({ ...t }); setIsNew(false); setErr(''); setMsg('') }
  function cancel() { setForm(null); setErr('') }
  function setF<K extends keyof Form>(k: K, v: Form[K]) { setForm((f) => (f ? { ...f, [k]: v } : f)) }

  function catLabel(key?: string) { return categories.find((c) => c.key === key)?.label ?? key ?? '' }

  async function save() {
    if (!token || !form) return
    if (isNew && !form.code?.trim()) { setErr('請填稱號代碼 code'); return }
    if (!form.name?.trim()) { setErr('請填稱號名稱'); return }
    if (!form.category) { setErr('請選擇類別'); return }
    const tier = Math.min(6, Math.max(1, Number(form.tier) || 1))
    setBusy(true); setErr(''); setMsg('')
    try {
      if (isNew) {
        await adminTitlesApi.create(token, {
          code: form.code!.trim(),
          category: form.category as TitleCategory,
          threshold: Number(form.threshold) || 0,
          unit: form.unit || '',
          name: form.name!.trim(),
          tier,
          sort_order: Number(form.sort_order) || 0,
          enabled: form.enabled ?? true,
        })
      } else {
        await adminTitlesApi.update(token, form.code!, {
          category: form.category as TitleCategory,
          threshold: Number(form.threshold) || 0,
          unit: form.unit || '',
          name: form.name!.trim(),
          tier,
          sort_order: Number(form.sort_order) || 0,
          enabled: form.enabled ?? true,
        })
      }
      setMsg(`✓ 已儲存「${form.name}」`)
      setForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function toggleEnabled(t: AdminTitle) {
    if (!token) return
    setErr('')
    try {
      // 後端 PUT 是整列覆寫（會驗 category/threshold/name），故送完整欄位而非只送 enabled
      const { code, earned_count, ...rest } = t
      await adminTitlesApi.update(token, code, { ...rest, enabled: !t.enabled })
      load()
    } catch (e: any) { setErr(e?.message || '更新失敗') }
  }

  async function del(t: AdminTitle) {
    if (!token) return
    const ok = confirm(
      `此稱號已被 ${t.earned_count} 位玩家取得，刪除會一併移除他們的此稱號，且展示中者會被清空。\n\n確定刪除「${t.name}（${t.code}）」？`
    )
    if (!ok) return
    setBusy(true); setErr('')
    try {
      const r = await adminTitlesApi.remove(token, t.code)
      setMsg(`✓ 已刪除「${t.name}」，並清除 ${r.revoked_from} 位玩家的此稱號`)
      if (form?.code === t.code) setForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  // 同類別現有門檻（新增/編輯時參考用；排除正在編輯的自己）
  const sameCatThresholds = (titles ?? [])
    .filter((t) => t.category === form?.category && t.code !== form?.code)
    .sort((a, b) => a.threshold - b.threshold)

  const groups = categories
    .map((c) => ({ cat: c, items: (titles ?? []).filter((t) => t.category === c.key).sort((a, b) => a.sort_order - b.sort_order) }))
    .filter((g) => g.items.length > 0)
  const knownKeys = new Set(categories.map((c) => c.key))
  const orphan = (titles ?? []).filter((t) => !knownKeys.has(t.category))

  return (
    <div style={{ maxWidth: 920, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 10 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>稱號管理</h1>
          <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0, lineHeight: 1.7 }}>
            稱號依「單次距離／累積距離／累積時間／打卡地點／關主挑戰／個人任務／玩家等級／卡片收藏／連續步伐」9 類固定門檻自動計算解鎖。
            門檻單位依類別而定（如距離類為公里、打卡類為個、連續跑步為日）——可參考同類別現有稱號的門檻。
          </p>
        </div>
        {!form && <button onClick={startNew} style={primaryBtn}>＋ 新增稱號</button>}
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}

      {form && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>{isNew ? '新增稱號' : `編輯稱號：${form.code}`}</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label="代碼 code（不可更改）">
              <input
                style={{ ...inp, width: 160, opacity: isNew ? 1 : 0.6 }}
                value={form.code || ''}
                disabled={!isNew}
                onChange={(e) => setF('code', e.target.value)}
                placeholder="如 dist_marathon"
              />
            </Field>
            <Field label="名稱" grow>
              <input style={inp} value={form.name || ''} onChange={(e) => setF('name', e.target.value)} placeholder="如：全馬勇者" />
            </Field>
            <Field label="類別">
              <select style={{ ...inp, width: 160 }} value={form.category || ''} onChange={(e) => setF('category', e.target.value as TitleCategory)}>
                {categories.map((c) => <option key={c.key} value={c.key}>{c.label}</option>)}
              </select>
            </Field>
          </div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginTop: 10, alignItems: 'flex-end' }}>
            <Field label="門檻 threshold">
              <input style={{ ...inp, width: 120 }} type="number" step="any" value={form.threshold ?? 0} onChange={(e) => setF('threshold', parseFloat(e.target.value) || 0)} />
            </Field>
            <Field label="單位 unit">
              <input style={{ ...inp, width: 100 }} value={form.unit || ''} onChange={(e) => setF('unit', e.target.value)} placeholder="公里/個/日" />
            </Field>
            <Field label="星級 tier (1-6)">
              <input style={{ ...inp, width: 100 }} type="number" min={1} max={6} value={form.tier ?? 1} onChange={(e) => setF('tier', +e.target.value)} />
            </Field>
            <Field label="排序 sort_order">
              <input style={{ ...inp, width: 100 }} type="number" value={form.sort_order ?? 0} onChange={(e) => setF('sort_order', +e.target.value)} />
            </Field>
            <Field label="啟用">
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, height: 36 }}>
                <input type="checkbox" checked={!!form.enabled} onChange={(e) => setF('enabled', e.target.checked)} />顯示於前台圖鑑
              </label>
            </Field>
          </div>
          {sameCatThresholds.length > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 8 }}>
              同類別「{catLabel(form.category)}」現有門檻參考：{sameCatThresholds.map((t) => `${t.name} ${t.threshold}${t.unit}`).join('、')}
            </div>
          )}
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={save} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={cancel} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 18 }}>
        {!titles && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {titles && titles.length === 0 && <div style={{ color: 'var(--tx-faint)' }}>尚無稱號，點右上「新增稱號」。</div>}

        {groups.map(({ cat, items }) => (
          <div key={cat.key}>
            <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 8 }}>{cat.label}（{items.length}）</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {items.map((t) => (
                <TitleRow key={t.code} t={t} onToggle={() => toggleEnabled(t)} onEdit={() => startEdit(t)} onDelete={() => del(t)} />
              ))}
            </div>
          </div>
        ))}

        {orphan.length > 0 && (
          <div>
            <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--hunt)', marginBottom: 8 }}>未知類別（{orphan.length}）— 不在已知 9 類，不會被自動計算解鎖</div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {orphan.map((t) => (
                <TitleRow key={t.code} t={t} onToggle={() => toggleEnabled(t)} onEdit={() => startEdit(t)} onDelete={() => del(t)} />
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function TitleRow({ t, onToggle, onEdit, onDelete }: { t: AdminTitle; onToggle: () => void; onEdit: () => void; onDelete: () => void }) {
  return (
    <div style={row}>
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ fontWeight: 700, fontSize: 14 }}>
          {t.name} <span style={{ color: 'var(--gold, #FFD24D)' }}>{'★'.repeat(t.tier)}</span>
          {!t.enabled && <span style={{ ...badge, color: 'var(--tx-dim)', borderColor: 'var(--line-2)' }}>停用</span>}
        </div>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>
          {t.code} · 門檻 {t.threshold}{t.unit} · 已 {t.earned_count} 人取得
        </div>
      </div>
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--tx-dim)', flexShrink: 0 }}>
        <input type="checkbox" checked={t.enabled} onChange={onToggle} />啟用
      </label>
      <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
        <button onClick={onEdit} style={ghostBtn}>編輯</button>
        <button onClick={onDelete} style={{ ...ghostBtn, color: 'var(--hunt)' }}>刪除</button>
      </div>
    </div>
  )
}

function Field({ label, children, grow }: { label: string; children: React.ReactNode; grow?: boolean }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: grow ? 1 : undefined, minWidth: grow ? 160 : undefined }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16 }
const row: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 12, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 14px', flexWrap: 'wrap' }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%', boxSizing: 'border-box' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5 }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 14px', cursor: 'pointer', fontSize: 13 }
const badge: React.CSSProperties = { marginLeft: 8, fontSize: 10.5, fontWeight: 700, border: '1px solid', borderRadius: 999, padding: '2px 8px', verticalAlign: 'middle' }
