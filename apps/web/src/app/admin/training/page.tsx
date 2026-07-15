'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminTrainingApi, type AdminWorkoutTemplate, type AdminPaceLevel } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 自主訓練後台：workout_templates 課表庫 + pace_levels 配速等級表。兩表皆為前台 P1-P4 排課引擎的資料源，
// segments/paces 是任意形狀 jsonb，後台直接用 JSON textarea 編輯（存前 JSON.parse 驗證），不逐欄位拆表單。

const ADJUST_TYPES = ['distance', 'reps', 'pyramid', 'none'] as const
const COMMON_CATEGORIES = ['recovery', 'easy', 'lsd', 'tempo', 'threshold', 'progression', 'interval', 'fartlek', 'pyramid', 'norwegian', 'yasso', 'rep']

const SEGMENTS_EXAMPLE = `[
  { "kind": "warmup", "label": "熱身", "target_type": "distance", "target": 1000 },
  { "kind": "work", "label": "配速跑", "effort": "marathon", "target_type": "time", "target": 1200, "reps": 3, "rest_s": 90 },
  { "kind": "rest", "label": "休息", "target_type": "time", "target": 90 },
  { "kind": "cooldown", "label": "緩和", "target_type": "distance", "target": 800 }
]`
// kind: warmup/work/rest/recovery/cooldown；effort（work 段用）: easy/marathon/threshold/interval/rep；target_type: distance(公尺)/time(秒)

const PACES_EXAMPLE = `{
  "easy": { "fast": 360, "slow": 400 },
  "marathon": { "fast": 300, "slow": 320 },
  "threshold": { "fast": 270, "slow": 285 },
  "interval": { "fast": 240, "slow": 255 },
  "rep": { "fast": 220, "slow": 235 }
}`
// 各配速單位：秒/公里，fast < slow

type TForm = {
  code: string
  name: string
  category: string
  description: string
  segmentsText: string
  sort_order: number
  enabled: boolean
  library_visible: boolean
  adjust_type: AdminWorkoutTemplate['adjust_type']
}
type PForm = { id: string; label: string; pacesText: string; enabled: boolean }

function emptyTForm(): TForm {
  return { code: '', name: '', category: 'easy', description: '', segmentsText: SEGMENTS_EXAMPLE, sort_order: 0, enabled: true, library_visible: true, adjust_type: 'none' }
}
function emptyPForm(): PForm {
  return { id: '', label: '', pacesText: PACES_EXAMPLE, enabled: true }
}
function toTForm(t: AdminWorkoutTemplate): TForm {
  return {
    code: t.code, name: t.name, category: t.category, description: t.description,
    segmentsText: JSON.stringify(t.segments, null, 2),
    sort_order: t.sort_order, enabled: t.enabled, library_visible: t.library_visible, adjust_type: t.adjust_type,
  }
}
function toPForm(p: AdminPaceLevel): PForm {
  return { id: String(p.id), label: p.label, pacesText: JSON.stringify(p.paces, null, 2), enabled: p.enabled }
}

export default function AdminTrainingPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [templates, setTemplates] = useState<AdminWorkoutTemplate[] | null>(null)
  const [paceLevels, setPaceLevels] = useState<AdminPaceLevel[] | null>(null)
  const [tForm, setTForm] = useState<TForm | null>(null)
  const [tIsNew, setTIsNew] = useState(false)
  const [pForm, setPForm] = useState<PForm | null>(null)
  const [pIsNew, setPIsNew] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminTrainingApi.data(t)
      .then((r) => { setTemplates(r.templates); setPaceLevels(r.pace_levels) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「自主訓練課表」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  // --- 課表庫 ---
  function tStartNew() { setTForm(emptyTForm()); setTIsNew(true); setErr(''); setMsg('') }
  function tStartEdit(t: AdminWorkoutTemplate) { setTForm(toTForm(t)); setTIsNew(false); setErr(''); setMsg('') }
  function tCancel() { setTForm(null); setErr('') }
  function setTF<K extends keyof TForm>(k: K, v: TForm[K]) { setTForm((f) => (f ? { ...f, [k]: v } : f)) }

  async function tSave() {
    if (!token || !tForm) return
    if (tIsNew && !tForm.code.trim()) { setErr('請填課表代碼 code'); return }
    if (!tForm.name.trim()) { setErr('請填課表名稱'); return }
    if (!tForm.category.trim()) { setErr('請填分類 category'); return }
    let segments: unknown
    try {
      segments = JSON.parse(tForm.segmentsText)
      if (!Array.isArray(segments)) throw new Error('segments 必須是陣列 [...]')
    } catch (e: any) {
      setErr(`segments 不是合法 JSON：${e?.message || '格式錯誤'}`)
      return
    }
    setBusy(true); setErr(''); setMsg('')
    try {
      const body = {
        name: tForm.name.trim(), category: tForm.category.trim(), description: tForm.description,
        segments, sort_order: Number(tForm.sort_order) || 0, enabled: tForm.enabled,
        library_visible: tForm.library_visible, adjust_type: tForm.adjust_type,
      }
      if (tIsNew) await adminTrainingApi.createTemplate(token, { code: tForm.code.trim(), ...body })
      else await adminTrainingApi.updateTemplate(token, tForm.code, body)
      setMsg(`✓ 已儲存課表「${tForm.name}」`)
      setTForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function tToggle(t: AdminWorkoutTemplate) {
    if (!token) return
    setErr('')
    try {
      const { code, ...rest } = t
      await adminTrainingApi.updateTemplate(token, code, { ...rest, enabled: !t.enabled })
      load()
    } catch (e: any) { setErr(e?.message || '更新失敗') }
  }

  async function tDelete(t: AdminWorkoutTemplate) {
    if (!token) return
    if (!confirm(`確定刪除課表「${t.name}（${t.code}）」？已排入玩家課表的舊排程將無法解析此代碼。`)) return
    setBusy(true); setErr('')
    try {
      await adminTrainingApi.deleteTemplate(token, t.code)
      setMsg(`✓ 已刪除「${t.name}」`)
      if (tForm?.code === t.code) setTForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  // --- 配速等級 ---
  function pStartNew() { setPForm(emptyPForm()); setPIsNew(true); setErr(''); setMsg('') }
  function pStartEdit(p: AdminPaceLevel) { setPForm(toPForm(p)); setPIsNew(false); setErr(''); setMsg('') }
  function pCancel() { setPForm(null); setErr('') }
  function setPF<K extends keyof PForm>(k: K, v: PForm[K]) { setPForm((f) => (f ? { ...f, [k]: v } : f)) }

  async function pSave() {
    if (!token || !pForm) return
    const idNum = parseInt(pForm.id, 10)
    if (pIsNew && (!pForm.id.trim() || !Number.isFinite(idNum) || idNum <= 0)) { setErr('請填正整數的等級 id'); return }
    if (!pForm.label.trim()) { setErr('請填等級名稱 label'); return }
    let paces: unknown
    try {
      paces = JSON.parse(pForm.pacesText)
    } catch (e: any) {
      setErr(`paces 不是合法 JSON：${e?.message || '格式錯誤'}`)
      return
    }
    setBusy(true); setErr(''); setMsg('')
    try {
      const body = { label: pForm.label.trim(), paces, enabled: pForm.enabled }
      if (pIsNew) await adminTrainingApi.createPaceLevel(token, { id: idNum, ...body })
      else await adminTrainingApi.updatePaceLevel(token, idNum, body)
      setMsg(`✓ 已儲存配速等級「${pForm.label}」`)
      setPForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function pToggle(p: AdminPaceLevel) {
    if (!token) return
    setErr('')
    try {
      await adminTrainingApi.updatePaceLevel(token, p.id, { label: p.label, paces: p.paces, enabled: !p.enabled })
      load()
    } catch (e: any) { setErr(e?.message || '更新失敗') }
  }

  async function pDelete(p: AdminPaceLevel) {
    if (!token) return
    if (!confirm(`確定刪除配速等級「${p.label}」（id=${p.id}）？`)) return
    setBusy(true); setErr('')
    try {
      await adminTrainingApi.deletePaceLevel(token, p.id)
      setMsg(`✓ 已刪除「${p.label}」`)
      if (pForm && parseInt(pForm.id, 10) === p.id) setPForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <div>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>自主訓練課表</h1>
        <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0, lineHeight: 1.7 }}>
          管理課表庫（workout_templates）與配速等級表（pace_levels），供前台排課引擎依 code / pace_level 解析與計算。
          segments／paces 為 JSON，儲存前會先驗證格式。
        </p>
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13, whiteSpace: 'pre-wrap' }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}

      {/* ───────── 課表庫 ───────── */}
      <SectionHeader title="課表庫" sub={`（${templates?.length ?? 0}）`} action={!tForm && <button onClick={tStartNew} style={primaryBtn}>＋ 新增課表</button>} />

      {tForm && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>{tIsNew ? '新增課表' : `編輯課表：${tForm.code}`}</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label="代碼 code（不可更改）">
              <input style={{ ...inp, width: 180, opacity: tIsNew ? 1 : 0.6 }} value={tForm.code} disabled={!tIsNew}
                onChange={(e) => setTF('code', e.target.value)} placeholder="如 easy_5k" />
            </Field>
            <Field label="名稱" grow>
              <input style={inp} value={tForm.name} onChange={(e) => setTF('name', e.target.value)} placeholder="如：輕鬆跑 5K" />
            </Field>
            <Field label="分類 category">
              <input style={{ ...inp, width: 160 }} value={tForm.category} list="category-list"
                onChange={(e) => setTF('category', e.target.value)} placeholder="如 easy" />
              <datalist id="category-list">
                {COMMON_CATEGORIES.map((c) => <option key={c} value={c} />)}
              </datalist>
            </Field>
            <Field label="調整方式 adjust_type">
              <select style={{ ...inp, width: 140 }} value={tForm.adjust_type} onChange={(e) => setTF('adjust_type', e.target.value as TForm['adjust_type'])}>
                {ADJUST_TYPES.map((a) => <option key={a} value={a}>{a}</option>)}
              </select>
            </Field>
          </div>
          <div style={{ marginTop: 10 }}>
            <Field label="說明 description">
              <input style={inp} value={tForm.description} onChange={(e) => setTF('description', e.target.value)} placeholder="給玩家看的課表說明" />
            </Field>
          </div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginTop: 10, alignItems: 'flex-end' }}>
            <Field label="排序 sort_order">
              <input style={{ ...inp, width: 100 }} type="number" value={tForm.sort_order} onChange={(e) => setTF('sort_order', +e.target.value)} />
            </Field>
            <Field label="啟用">
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, height: 36 }}>
                <input type="checkbox" checked={tForm.enabled} onChange={(e) => setTF('enabled', e.target.checked)} />enabled
              </label>
            </Field>
            <Field label="課表庫可見">
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, height: 36 }}>
                <input type="checkbox" checked={tForm.library_visible} onChange={(e) => setTF('library_visible', e.target.checked)} />library_visible
              </label>
            </Field>
          </div>
          <div style={{ marginTop: 10 }}>
            <Field label="segments（JSON 陣列）">
              <textarea style={{ ...inp, minHeight: 180, fontFamily: 'var(--font-mono, monospace)', fontSize: 12.5, whiteSpace: 'pre' }}
                value={tForm.segmentsText} onChange={(e) => setTF('segmentsText', e.target.value)} spellCheck={false} />
            </Field>
            <details style={{ marginTop: 6 }}>
              <summary style={{ fontSize: 11.5, color: 'var(--tx-faint)', cursor: 'pointer' }}>segments 格式範例（點開）</summary>
              <pre style={preBox}>{SEGMENTS_EXAMPLE}</pre>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                kind: warmup/work/rest/recovery/cooldown｜effort（work 段用）: easy/marathon/threshold/interval/rep｜target_type: distance(公尺)/time(秒)
              </div>
            </details>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={tSave} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={tCancel} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
        {!templates && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {templates && templates.length === 0 && <div style={{ color: 'var(--tx-faint)' }}>尚無課表，點右上「新增課表」。</div>}
        {templates?.slice().sort((a, b) => a.sort_order - b.sort_order).map((t) => (
          <TemplateRow key={t.code} t={t} onToggle={() => tToggle(t)} onEdit={() => tStartEdit(t)} onDelete={() => tDelete(t)} />
        ))}
      </div>

      {/* ───────── 配速等級 ───────── */}
      <SectionHeader title="配速等級" sub={`（${paceLevels?.length ?? 0}）`} action={!pForm && <button onClick={pStartNew} style={primaryBtn}>＋ 新增等級</button>} top={36} />

      {pForm && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>{pIsNew ? '新增配速等級' : `編輯配速等級：id=${pForm.id}`}</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label="id（不可更改）">
              <input style={{ ...inp, width: 100, opacity: pIsNew ? 1 : 0.6 }} type="number" value={pForm.id} disabled={!pIsNew}
                onChange={(e) => setPF('id', e.target.value)} placeholder="如 3" />
            </Field>
            <Field label="名稱 label" grow>
              <input style={inp} value={pForm.label} onChange={(e) => setPF('label', e.target.value)} placeholder="如：中階跑者" />
            </Field>
            <Field label="啟用">
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, height: 36 }}>
                <input type="checkbox" checked={pForm.enabled} onChange={(e) => setPF('enabled', e.target.checked)} />enabled
              </label>
            </Field>
          </div>
          <div style={{ marginTop: 10 }}>
            <Field label="paces（JSON 物件）">
              <textarea style={{ ...inp, minHeight: 150, fontFamily: 'var(--font-mono, monospace)', fontSize: 12.5, whiteSpace: 'pre' }}
                value={pForm.pacesText} onChange={(e) => setPF('pacesText', e.target.value)} spellCheck={false} />
            </Field>
            <details style={{ marginTop: 6 }}>
              <summary style={{ fontSize: 11.5, color: 'var(--tx-faint)', cursor: 'pointer' }}>paces 格式範例（點開）</summary>
              <pre style={preBox}>{PACES_EXAMPLE}</pre>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>單位：秒/公里，各級距 fast &lt; slow</div>
            </details>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={pSave} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={pCancel} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 8, marginBottom: 24 }}>
        {!paceLevels && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {paceLevels && paceLevels.length === 0 && <div style={{ color: 'var(--tx-faint)' }}>尚無配速等級，點右上「新增等級」。</div>}
        {paceLevels?.slice().sort((a, b) => a.id - b.id).map((p) => (
          <PaceLevelRow key={p.id} p={p} onToggle={() => pToggle(p)} onEdit={() => pStartEdit(p)} onDelete={() => pDelete(p)} />
        ))}
      </div>
    </div>
  )
}

function SectionHeader({ title, sub, action, top }: { title: string; sub: string; action: React.ReactNode; top?: number }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 10, marginTop: top ?? 24 }}>
      <div style={{ fontSize: 16, fontWeight: 800 }}>{title}<span style={{ color: 'var(--tx-dim)', fontWeight: 400, fontSize: 13 }}>{sub}</span></div>
      {action}
    </div>
  )
}

function TemplateRow({ t, onToggle, onEdit, onDelete }: { t: AdminWorkoutTemplate; onToggle: () => void; onEdit: () => void; onDelete: () => void }) {
  return (
    <div style={row}>
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ fontWeight: 700, fontSize: 14 }}>
          {t.name}
          {!t.enabled && <span style={{ ...badge, color: 'var(--tx-dim)', borderColor: 'var(--line-2)' }}>停用</span>}
          {!t.library_visible && <span style={{ ...badge, color: 'var(--tx-dim)', borderColor: 'var(--line-2)' }}>不進課表庫</span>}
        </div>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>
          {t.code} · {t.category} · adjust={t.adjust_type} · 排序 {t.sort_order}
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

function PaceLevelRow({ p, onToggle, onEdit, onDelete }: { p: AdminPaceLevel; onToggle: () => void; onEdit: () => void; onDelete: () => void }) {
  return (
    <div style={row}>
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ fontWeight: 700, fontSize: 14 }}>
          {p.label}
          {!p.enabled && <span style={{ ...badge, color: 'var(--tx-dim)', borderColor: 'var(--line-2)' }}>停用</span>}
        </div>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>id={p.id}</div>
      </div>
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--tx-dim)', flexShrink: 0 }}>
        <input type="checkbox" checked={p.enabled} onChange={onToggle} />啟用
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
const preBox: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: 10, fontSize: 12, lineHeight: 1.5, overflowX: 'auto', marginTop: 4 }
