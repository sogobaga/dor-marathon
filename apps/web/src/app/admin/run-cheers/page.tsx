'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminRunCheersApi, type RunCheerMessage, type RunCheerInput, type RunCheerPhase } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 跑步鼓勵語管理：GPS 跑步頁每跨一整公里彈出一句鼓勵語，分兩池——
// before（累積式，完成目標 50% 前使用，文案含 {done} 佔位符，前台代入已完成距離如「3 km」）
// after （剩餘式，超過 50% 後使用，文案含 {remain} 佔位符，前台代入剩餘距離／時間如「7 km」「38 分鐘」）
// 後端種了 100 組（各池 50）；此頁只做 CRUD，不動 track 頁前台呈現邏輯。

type Draft = { text: string; sort_order: number; enabled: boolean }

function draftOf(m: RunCheerMessage): Draft {
  return { text: m.text, sort_order: m.sort_order, enabled: m.enabled }
}

// 即時預覽：把佔位符換成示意值，貼近前台實際呈現
function preview(text: string): string {
  return text.replaceAll('{done}', '3 km').replaceAll('{remain}', '7 km')
}

export default function AdminRunCheersPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [items, setItems] = useState<RunCheerMessage[] | null>(null)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminRunCheersApi.list(t)
      .then((r) => setItems(r.items))
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「跑步鼓勵語」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  const before = (items ?? []).filter((i) => i.phase === 'before').sort((a, b) => a.sort_order - b.sort_order)
  const after = (items ?? []).filter((i) => i.phase === 'after').sort((a, b) => a.sort_order - b.sort_order)

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <div>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>跑步鼓勵語</h1>
        <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0, lineHeight: 1.8 }}>
          GPS 跑步頁每跨一整公里會彈出一句鼓勵語，分兩個文案池：<br />
          <strong style={{ color: 'var(--tx)' }}>前半段（累積式）</strong>——完成目標 <strong>50% 前</strong>使用，文案建議含佔位符{' '}
          <code style={codeStyle}>{'{done}'}</code>，前台會代入已完成距離（如「3 km」）。<br />
          <strong style={{ color: 'var(--tx)' }}>後半段（剩餘式）</strong>——超過 <strong>50%</strong> 後使用，文案建議含佔位符{' '}
          <code style={codeStyle}>{'{remain}'}</code>，前台會代入剩餘距離或時間（如「7 km」「38 分鐘」）。<br />
          未含對應佔位符不會擋儲存，但該句只會顯示固定文字，不會隨進度變化。
        </p>
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}

      {!items && <div style={{ color: 'var(--tx-dim)', marginTop: 16 }}>載入中…</div>}

      {items && token && (
        <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 32 }}>
          <Section
            title="前半段（累積式）"
            phase="before"
            placeholder="{done}"
            items={before}
            token={token}
            onMsg={setMsg}
            onErr={setErr}
            reload={load}
          />
          <Section
            title="後半段（剩餘式）"
            phase="after"
            placeholder="{remain}"
            items={after}
            token={token}
            onMsg={setMsg}
            onErr={setErr}
            reload={load}
          />
        </div>
      )}
    </div>
  )
}

function Section({
  title, phase, placeholder, items, token, onMsg, onErr, reload,
}: {
  title: string
  phase: RunCheerPhase
  placeholder: string
  items: RunCheerMessage[]
  token: string
  onMsg: (s: string) => void
  onErr: (s: string) => void
  reload: () => void
}) {
  const enabledCount = items.filter((i) => i.enabled).length
  const nextSort = items.length ? Math.max(...items.map((i) => i.sort_order)) + 1 : 0
  const [sortOverride, setSortOverride] = useState<number | null>(null)
  const sortValue = sortOverride ?? nextSort
  const [newText, setNewText] = useState('')
  const [busy, setBusy] = useState(false)

  async function addNew() {
    if (!newText.trim()) { onErr('請輸入文案'); return }
    setBusy(true); onErr(''); onMsg('')
    try {
      await adminRunCheersApi.create(token, { phase, text: newText.trim(), enabled: true, sort_order: sortValue })
      onMsg('✓ 已新增')
      setNewText('')
      setSortOverride(null)
      reload()
    } catch (e: any) { onErr(e?.message || '新增失敗') } finally { setBusy(false) }
  }

  return (
    <div>
      <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 8 }}>
        {title}（啟用 {enabledCount} / 共 {items.length}）
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {items.length === 0 && <div style={{ color: 'var(--tx-faint)', fontSize: 13 }}>尚無文案，於下方新增。</div>}
        {items.map((it) => (
          <Row key={it.id} item={it} phase={phase} placeholder={placeholder} token={token} onMsg={onMsg} onErr={onErr} reload={reload} />
        ))}
      </div>

      <div style={{ ...card, marginTop: 10 }}>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 8 }}>新增一句</div>
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <Field label="文案" grow>
            <input
              style={inp}
              value={newText}
              onChange={(e) => setNewText(e.target.value)}
              placeholder={`如：太棒了！已經完成 ${placeholder} 了`}
            />
          </Field>
          <Field label="排序 sort_order">
            <input
              style={{ ...inp, width: 90 }}
              type="number"
              value={sortValue}
              onChange={(e) => setSortOverride(parseInt(e.target.value, 10) || 0)}
            />
          </Field>
          <button onClick={addNew} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>
            {busy ? '新增中…' : '＋ 新增一句'}
          </button>
        </div>
        {newText && !newText.includes(placeholder) && (
          <div style={hintStyle}>未含 {placeholder}，將只顯示固定文字</div>
        )}
        {newText && (
          <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--fug)', marginTop: 8 }}>預覽：{preview(newText)}</div>
        )}
      </div>
    </div>
  )
}

function Row({
  item, phase, placeholder, token, onMsg, onErr, reload,
}: {
  item: RunCheerMessage
  phase: RunCheerPhase
  placeholder: string
  token: string
  onMsg: (s: string) => void
  onErr: (s: string) => void
  reload: () => void
}) {
  const [draft, setDraft] = useState<Draft>(() => draftOf(item))
  const [busy, setBusy] = useState(false)

  // 外部（reload 後）拿到最新資料時，同步覆蓋本地草稿
  useEffect(() => { setDraft(draftOf(item)) }, [item.id, item.text, item.sort_order, item.enabled])

  const dirty = draft.text !== item.text || draft.sort_order !== item.sort_order || draft.enabled !== item.enabled

  async function save(override?: Partial<Draft>) {
    const next: Draft = { ...draft, ...override }
    if (!next.text.trim()) { onErr('文案不可為空'); return }
    const body: RunCheerInput = { phase, text: next.text.trim(), sort_order: next.sort_order, enabled: next.enabled }
    setBusy(true); onErr(''); onMsg('')
    try {
      await adminRunCheersApi.update(token, item.id, body)
      onMsg('✓ 已儲存')
      reload()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  function toggleEnabled() {
    const next = !draft.enabled
    setDraft((d) => ({ ...d, enabled: next }))
    save({ enabled: next })
  }

  async function del() {
    const ok = window.confirm(`確定刪除這句鼓勵語？\n\n「${item.text}」`)
    if (!ok) return
    setBusy(true); onErr('')
    try {
      await adminRunCheersApi.remove(token, item.id)
      onMsg('✓ 已刪除')
      reload()
    } catch (e: any) { onErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  const missing = !draft.text.includes(placeholder)

  return (
    <div style={row}>
      <input
        style={{ ...inp, width: 60, flexShrink: 0 }}
        type="number"
        value={draft.sort_order}
        onChange={(e) => setDraft((d) => ({ ...d, sort_order: parseInt(e.target.value, 10) || 0 }))}
        onBlur={() => dirty && save()}
        title="排序號"
      />
      <div style={{ flex: 2, minWidth: 200 }}>
        <input
          style={inp}
          value={draft.text}
          onChange={(e) => setDraft((d) => ({ ...d, text: e.target.value }))}
          onBlur={() => dirty && save()}
        />
        {missing && <div style={hintStyle}>未含 {placeholder}，將只顯示固定文字</div>}
      </div>
      <div style={{ flex: 1.3, minWidth: 150, fontSize: 13, fontWeight: 700, color: 'var(--fug)' }}>
        {preview(draft.text)}
      </div>
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--tx-dim)', flexShrink: 0 }}>
        <input type="checkbox" checked={draft.enabled} onChange={toggleEnabled} />啟用
      </label>
      {dirty && (
        <button onClick={() => save()} disabled={busy} style={{ ...primaryBtn, padding: '6px 12px', fontSize: 12, flexShrink: 0 }}>
          {busy ? '儲存中…' : '儲存'}
        </button>
      )}
      <button onClick={del} style={{ ...ghostBtn, color: 'var(--hunt)', flexShrink: 0 }}>刪除</button>
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
const hintStyle: React.CSSProperties = { fontSize: 11, color: 'var(--gold)', marginTop: 4 }
const codeStyle: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 4, padding: '1px 6px', fontSize: 12, fontFamily: 'monospace' }
