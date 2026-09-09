'use client'

// 遊戲化角色數值後台（第 21 套，參考 RO 素質系統）：三分頁——參數設定（RpgConfig 逐欄可調＋進階
// JSON 編輯）／入口與 VVIP（rpg_entry_state/whitelist 走通用 app-settings API＋VVIP／角色管理）／
// 預覽計算（GET /admin/rpg/preview，不動玩家資料，純算式試算）。頁面結構比照 admin/monopoly。
import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminRpgApi,
  type RpgConfig, type AdminRpgUser, type RpgDerived, type RpgStats, type RpgStatKey,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { CONFIG_GROUPS, STAT_META, DERIVED_META, resistLabel } from '@/lib/rpgMeta'

type Tab = 'config' | 'entry' | 'preview'
const STAT_KEYS: RpgStatKey[] = ['str', 'agi', 'vit', 'dex', 'int', 'luk']

export default function AdminRpgPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [tab, setTab] = useState<Tab>('config')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const [config, setConfig] = useState<RpgConfig | null>(null)
  const [defaults, setDefaults] = useState<RpgConfig | null>(null)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    const onErr = (e: any) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「遊戲化」權限')
      else setErr(e?.message || '載入失敗')
    }
    adminRpgApi.config(t).then((r) => { setConfig(r.config); setDefaults(r.defaults) }).catch(onErr)
  }, [router])
  useEffect(() => { load() }, [load])

  function flash(m: string) { setMsg(m); setErr(''); window.setTimeout(() => setMsg(''), 3500) }

  return (
    <div style={{ maxWidth: 1000, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>遊戲化角色數值 · 後台管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        參考 RO 素質系統的基本素質（STR/AGI/VIT/DEX/INT/LUK）配點與衍生數值。只有 VVIP 與白名單管理者
        在前台看得到「🎮 角色」入口，本頁管理所有係數、入口／VVIP 名單、以及不影響玩家資料的預覽計算。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      <div style={{ display: 'flex', gap: 6, marginBottom: 16, borderBottom: '1px solid var(--line)', flexWrap: 'wrap' }}>
        {([
          ['config', '參數設定'],
          ['entry', '入口與 VVIP'],
          ['preview', '預覽計算'],
        ] as [Tab, string][]).map(([v, label]) => (
          <button
            key={v}
            onClick={() => setTab(v)}
            style={{
              padding: '8px 14px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14,
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)',
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
              fontWeight: tab === v ? 700 : 400,
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'config' && token && config && defaults && (
        <ConfigTab
          token={token}
          config={config}
          defaults={defaults}
          onSaved={(c, d) => { setConfig(c); setDefaults(d); flash('已儲存參數設定') }}
          onErr={setErr}
        />
      )}
      {tab === 'entry' && token && <EntryTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'preview' && token && <PreviewTab token={token} />}
    </div>
  )
}

// ============================== 參數設定 ==============================

function ConfigTab({ token, config, defaults, onSaved, onErr }: {
  token: string
  config: RpgConfig
  defaults: RpgConfig
  onSaved: (config: RpgConfig, defaults: RpgConfig) => void
  onErr: (m: string) => void
}) {
  const [edit, setEdit] = useState<Record<string, string>>(() => seedEdit(config))
  const [saving, setSaving] = useState(false)
  const [showJson, setShowJson] = useState(false)
  const [jsonText, setJsonText] = useState('')

  function seedEdit(c: RpgConfig): Record<string, string> {
    const next: Record<string, string> = {}
    for (const g of CONFIG_GROUPS) for (const f of g.fields) next[f.key] = String((c as any)[f.key])
    return next
  }

  function buildConfig(): RpgConfig {
    const obj: any = {}
    for (const g of CONFIG_GROUPS) for (const f of g.fields) {
      obj[f.key] = f.type === 'select' ? edit[f.key] : Number(edit[f.key])
    }
    return obj as RpgConfig
  }

  async function save() {
    setSaving(true)
    try {
      const r = await adminRpgApi.setConfig(token, buildConfig())
      setEdit(seedEdit(r.config))
      onSaved(r.config, r.defaults)
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSaving(false) }
  }

  function restoreDefaults() {
    setEdit(seedEdit(defaults))
  }

  function openJson() { setJsonText(JSON.stringify(buildConfig(), null, 2)); setShowJson(true) }
  function applyJson() {
    try {
      const obj = JSON.parse(jsonText)
      const next: Record<string, string> = { ...edit }
      for (const g of CONFIG_GROUPS) for (const f of g.fields) {
        if (obj[f.key] !== undefined) next[f.key] = String(obj[f.key])
      }
      setEdit(next)
      setShowJson(false)
    } catch { onErr('JSON 格式錯誤，未套用') }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button onClick={save} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存'}</button>
        <button onClick={restoreDefaults} style={ghostBtn}>還原預設值（尚未儲存）</button>
        <button onClick={showJson ? () => setShowJson(false) : openJson} style={ghostBtn}>{showJson ? '關閉進階 JSON' : '進階 JSON 編輯'}</button>
      </div>

      {showJson && (
        <div style={panel}>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0 }}>直接貼上完整或部分 JSON（缺的欄位維持表單原值），按「套用 JSON」寫回下方表單後仍需按「儲存」才會生效。</p>
          <textarea style={{ ...ta, height: 260, fontFamily: 'monospace', fontSize: 12 }} value={jsonText} onChange={(e) => setJsonText(e.target.value)} />
          <button onClick={applyJson} style={{ ...primaryBtn, marginTop: 8 }}>套用 JSON 到表單</button>
        </div>
      )}

      {CONFIG_GROUPS.map((g) => (
        <div key={g.title} style={panel}>
          <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 10px' }}>{g.title}</h2>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            {g.fields.map((f) => (
              <F key={f.key} label={f.label}>
                {f.type === 'select' ? (
                  <select style={inp} value={edit[f.key] ?? ''} onChange={(e) => setEdit((s) => ({ ...s, [f.key]: e.target.value }))}>
                    {f.options?.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                  </select>
                ) : (
                  <input style={inp} type="number" step="any" value={edit[f.key] ?? ''} onChange={(e) => setEdit((s) => ({ ...s, [f.key]: e.target.value }))} />
                )}
              </F>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

// ============================== 入口與 VVIP ==============================

function EntryTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [entryState, setEntryState] = useState('hidden')
  const [whitelist, setWhitelist] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [savingEntry, setSavingEntry] = useState(false)

  const [q, setQ] = useState('')
  const [users, setUsers] = useState<AdminRpgUser[] | null>(null)
  const [searching, setSearching] = useState(false)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [jobLevelEdit, setJobLevelEdit] = useState<Record<string, string>>({})

  useEffect(() => {
    adminRpgApi.getEntry(token).then((r) => {
      setEntryState(r.state || 'hidden')
      setWhitelist(r.whitelist || '')
      setLoaded(true)
    }).catch((e: any) => onErr(e?.message || '載入入口設定失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  async function saveEntry() {
    setSavingEntry(true)
    try {
      await adminRpgApi.setEntry(token, entryState, whitelist)
      onMsg('已儲存入口設定')
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setSavingEntry(false) }
  }

  const search = useCallback(() => {
    setSearching(true)
    adminRpgApi.users(token, q).then((r) => setUsers(r.users)).catch((e: any) => onErr(e?.message || '搜尋失敗')).finally(() => setSearching(false))
  }, [token, q, onErr])
  useEffect(() => { search() }, []) // eslint-disable-line react-hooks/exhaustive-deps -- 首次進頁列出前 50 筆

  async function toggleVvip(u: AdminRpgUser) {
    setBusyId(u.id)
    try {
      await adminRpgApi.setVvip(token, u.id, !u.is_vvip)
      setUsers((us) => us?.map((x) => (x.id === u.id ? { ...x, is_vvip: !u.is_vvip } : x)) ?? us)
    } catch (e: any) { onErr(e?.message || '設定失敗') } finally { setBusyId(null) }
  }
  async function resetChar(u: AdminRpgUser) {
    if (!window.confirm(`確定要把「${u.name}」的角色素質重置為初始值嗎？（已花費點數會全數退回）`)) return
    setBusyId(u.id)
    try {
      await adminRpgApi.reset(token, u.id)
      onMsg(`已重置「${u.name}」的角色`)
      search()
    } catch (e: any) { onErr(e?.message || '重置失敗') } finally { setBusyId(null) }
  }
  async function saveJobLevel(u: AdminRpgUser) {
    const v = parseInt(jobLevelEdit[u.id] ?? String(u.job_level), 10)
    if (isNaN(v) || v < 1) { onErr('職業等級需為正整數'); return }
    setBusyId(u.id)
    try {
      await adminRpgApi.setJobLevel(token, u.id, v)
      setUsers((us) => us?.map((x) => (x.id === u.id ? { ...x, job_level: v } : x)) ?? us)
      onMsg(`已設定「${u.name}」的 Job Lv`)
    } catch (e: any) { onErr(e?.message || '設定失敗') } finally { setBusyId(null) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={panel}>
        <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>入口顯示狀態</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0, lineHeight: 1.7 }}>
          控制前台會員面板「🎮 角色」按鈕的可見性。走本頁專屬的 /admin/rpg/entry 端點（僅需「遊戲化」
          權限即可完整使用，不必額外申請「系統設定」權限）。
        </p>
        {loaded && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12, maxWidth: 480 }}>
            <F label="入口狀態">
              <select style={inp} value={entryState} onChange={(e) => setEntryState(e.target.value)}>
                <option value="hidden">隱藏（沒有人看得到，含超管——見 internal/rpg.ResolveEntry，刻意不比照其他 *_entry_state 讓超管旁路）</option>
                <option value="whitelist">僅 VVIP／白名單帳號可見（超管仍看得到）</option>
                <option value="shown">全部開放（所有會員都看得到）</option>
              </select>
            </F>
            <F label="指定帳號白名單（Email，一行一個或逗號分隔）">
              <textarea style={ta} rows={4} value={whitelist} onChange={(e) => setWhitelist(e.target.value)} placeholder={'someone@example.com'} />
            </F>
            <button onClick={saveEntry} disabled={savingEntry} style={primaryBtn}>{savingEntry ? '儲存中…' : '儲存入口設定'}</button>
          </div>
        )}
      </div>

      <div style={panel}>
        <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 10px' }}>VVIP／角色管理</h2>
        <div style={{ display: 'flex', gap: 8 }}>
          <input style={{ ...inp, flex: 1 }} placeholder="搜尋 Email／姓名／帳號代碼" value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') search() }} />
          <button onClick={search} style={ghostBtn}>搜尋</button>
        </div>
        <div style={{ marginTop: 12 }}>
          {searching && <div style={{ fontSize: 13, color: 'var(--tx-dim)', padding: '10px 0' }}>搜尋中…</div>}
          {!searching && users && users.length === 0 && <div style={{ fontSize: 13, color: 'var(--tx-dim)', padding: '10px 0' }}>查無帳號</div>}
          {!searching && !!users?.length && (
            <div style={{ overflowX: 'auto' }}>
              <Row head>
                <C w={2}>會員</C>
                <C w={1}>VVIP</C>
                <C w={2}>角色狀態</C>
                <C w={2}>Job Lv</C>
                <C w={2}>操作</C>
              </Row>
              {users.map((u) => (
                <Row key={u.id}>
                  <C w={2}>
                    <div style={{ fontWeight: 700 }}>{u.name}</div>
                    <div style={{ fontSize: 11.5, color: 'var(--tx-dim)' }}>{u.email}</div>
                  </C>
                  <C w={1}>{u.is_vvip ? <span style={{ color: 'var(--gold)', fontWeight: 800 }}>VVIP</span> : '—'}</C>
                  <C w={2} dim>
                    {u.has_character
                      ? `${STAT_KEYS.map((k) => `${k.toUpperCase()} ${u.stats?.[k] ?? 1}`).join(' ／ ')}（自由 ${u.free_points}）`
                      : '尚未建立角色'}
                  </C>
                  <C w={2}>
                    <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                      <input
                        style={{ ...inp, width: 56 }} type="number" min={1}
                        value={jobLevelEdit[u.id] ?? String(u.job_level)}
                        onChange={(e) => setJobLevelEdit((s) => ({ ...s, [u.id]: e.target.value }))}
                      />
                      <button onClick={() => saveJobLevel(u)} disabled={busyId === u.id} style={linkBtn}>設定</button>
                    </div>
                  </C>
                  <C w={2}>
                    <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                      <button onClick={() => toggleVvip(u)} disabled={busyId === u.id} style={ghostBtn}>{u.is_vvip ? '取消 VVIP' : '設為 VVIP'}</button>
                      <button onClick={() => resetChar(u)} disabled={busyId === u.id || !u.has_character} style={ghostBtn}>重置角色</button>
                    </div>
                  </C>
                </Row>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// ============================== 預覽計算 ==============================

function PreviewTab({ token }: { token: string }) {
  const [form, setForm] = useState<Record<string, string>>({ base_level: '1', job_level: '1', str: '1', agi: '1', vit: '1', dex: '1', int: '1', luk: '1' })
  const [result, setResult] = useState<{ derived: RpgDerived; max_hp: number; max_mp: number; next_cost: Partial<RpgStats> } | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const run = useCallback(() => {
    setBusy(true); setErr('')
    const n = (k: string) => parseInt(form[k] || '1', 10) || 1
    adminRpgApi.preview(token, {
      base_level: n('base_level'), job_level: n('job_level'),
      str: n('str'), agi: n('agi'), vit: n('vit'), dex: n('dex'), int: n('int'), luk: n('luk'),
    }).then(setResult).catch((e: any) => setErr(e?.message || '計算失敗')).finally(() => setBusy(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { run() }, []) // eslint-disable-line react-hooks/exhaustive-deps -- 進頁先用預設值跑一次

  return (
    <div style={panel}>
      <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>預覽計算</h2>
      <p style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 0 }}>純算式試算，不會讀寫任何玩家資料——調完「參數設定」想確認結果時用這裡。</p>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(110px, 1fr))', gap: 10 }}>
        <F label="Base Lv"><input style={inp} type="number" min={1} value={form.base_level} onChange={(e) => setForm((s) => ({ ...s, base_level: e.target.value }))} /></F>
        <F label="Job Lv"><input style={inp} type="number" min={1} value={form.job_level} onChange={(e) => setForm((s) => ({ ...s, job_level: e.target.value }))} /></F>
        {STAT_META.map((m) => (
          <F key={m.key} label={`${m.label} ${m.abbr}`}>
            <input style={inp} type="number" min={1} value={form[m.key]} onChange={(e) => setForm((s) => ({ ...s, [m.key]: e.target.value }))} />
          </F>
        ))}
      </div>
      <button onClick={run} disabled={busy} style={{ ...primaryBtn, marginTop: 12 }}>{busy ? '計算中…' : '計算'}</button>
      {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginTop: 8 }}>{err}</div>}

      {result && (
        <div style={{ marginTop: 16 }}>
          <div style={{ display: 'flex', gap: 16, fontSize: 13.5, fontWeight: 700, color: 'var(--fug)' }}>
            <span>Max HP {result.max_hp}</span>
            <span>Max MP {result.max_mp}</span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(150px, 1fr))', gap: 8, marginTop: 10 }}>
            {DERIVED_META.map((d) => (
              <div key={d.key} style={derivedCell}>
                <div style={{ fontSize: 10.5, color: 'var(--tx-dim)' }}>{d.label} {d.abbr}</div>
                <div style={{ fontSize: 15, fontWeight: 800 }}>{fmtNum(result.derived[d.key] as number)}{d.suffix ?? ''}</div>
              </div>
            ))}
          </div>
          {!!result.derived.resists && Object.keys(result.derived.resists).length > 0 && (
            <div style={{ marginTop: 10, fontSize: 12 }}>
              狀態抗性：{Object.entries(result.derived.resists).map(([k, v]) => `${resistLabel(k)} ${fmtNum(v)}%`).join('　')}
            </div>
          )}
          <div style={{ marginTop: 10, fontSize: 12, color: 'var(--tx-dim)' }}>
            下一點花費：{STAT_META.map((m) => `${m.abbr} ${result.next_cost[m.key] ?? '已達上限'}`).join('　')}
          </div>
        </div>
      )}
    </div>
  )
}

function fmtNum(n: number): string {
  if (n == null || isNaN(n)) return '0'
  return Number.isInteger(n) ? String(n) : n.toFixed(1)
}

// ============================== 共用小元件／樣式（比照 admin/monopoly） ==============================

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}
function Row({ children, head }: { children: React.ReactNode; head?: boolean }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '10px 12px', borderBottom: '1px solid var(--line)',
      background: head ? 'var(--bg-1)' : 'transparent', fontSize: head ? 11 : 13.5,
      color: head ? 'var(--tx-faint)' : 'var(--tx)', textTransform: head ? 'uppercase' : 'none', minWidth: 640,
    }}>{children}</div>
  )
}
function C({ children, w, dim }: { children: React.ReactNode; w: number; dim?: boolean }) {
  return <div style={{ flex: w, minWidth: 0, color: dim ? 'var(--tx-dim)' : undefined, fontSize: dim ? 12 : undefined }}>{children}</div>
}

const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 18 }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 11px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%', boxSizing: 'border-box' }
const ta: React.CSSProperties = { ...inp, resize: 'vertical', lineHeight: 1.5 }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const linkBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--fug)', cursor: 'pointer', fontSize: 13, padding: 0, fontFamily: 'inherit' }
const derivedCell: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 10px' }
