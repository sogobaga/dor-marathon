'use client'

// 遊戲化角色數值後台（第 21 套，參考 RO 素質系統）：三分頁——參數設定（RpgConfig 逐欄可調＋進階
// JSON 編輯）／入口與 VVIP（rpg_entry_state/whitelist 走通用 app-settings API＋VVIP／角色管理）／
// 預覽計算（GET /admin/rpg/preview，不動玩家資料，純算式試算）。頁面結構比照 admin/monopoly。
import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminRpgApi,
  type RpgConfig, type AdminRpgUser, type RpgDerived, type RpgStats, type RpgStatKey,
  type RpgMonster, type RpgSkill, type RpgItem, type RpgScene, type RpgSceneSlot, type RpgCompanion,
  type RpgEncounter, type RpgEncounterMonster, type RpgBattleLogRow, type RpgBattleLogSummary,
  type RpgWeaponType, type RpgWeapon, type RpgElement, type WeaponRarity,
  type RpgArmorItem, type ArmorItemSlot, type RpgAiStrategy, type RpgJob,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { CONFIG_GROUPS, STAT_META, DERIVED_META, resistLabel, ELEMENT_LABEL, ELEMENT_ORDER, RARITY_LABEL, ARMOR_SLOT_LABEL, SKILL_KIND_LABEL } from '@/lib/rpgMeta'

// DORPG P2（契約 dorpg_p2 §5）：怪物/技能/道具/場景/遭遇/隊友/戰鬥數據七個內容分頁。分頁一多，
// tab 列改橫向捲動（見下方 tab 按鈕列 container 的 overflowX/flexWrap:'nowrap'）。
// DORPG P7（契約 dorpg_p7 CONTRACT.md §2、WIRE §後台）：新增武器類型／武器兩個分頁。
// DORPG P8（契約 dorpg_p8 CONTRACT.md §1、WIRE §後台）：新增防具（含飾品）分頁。
// DORPG P9（契約 dorpg_p9 CONTRACT.md §2、WIRE §後台）：新增「AI 策略」分頁。
type Tab = 'config' | 'entry' | 'preview' | 'monsters' | 'skills' | 'jobs' | 'items' | 'scenes' | 'encounters' | 'companions' | 'battlelogs' | 'weapontypes' | 'weapons' | 'armoritems' | 'aistrategies'
const STAT_KEYS: RpgStatKey[] = ['str', 'agi', 'vit', 'dex', 'int', 'luk']
// rpg_encounter_monsters 槽位固定 5 個（migration 176 DDL），場景/遭遇編輯器都用這個順序渲染。
const ENCOUNTER_SLOTS = ['rear_left', 'rear_right', 'front_left', 'front_center', 'front_right'] as const
const SLOT_LABEL: Record<string, string> = {
  rear_left: '後排・左', rear_right: '後排・右', front_left: '前排・左', front_center: '前排・中', front_right: '前排・右',
}

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

      <div style={{ display: 'flex', gap: 6, marginBottom: 16, borderBottom: '1px solid var(--line)', overflowX: 'auto', flexWrap: 'nowrap' }}>
        {([
          ['config', '參數設定'],
          ['entry', '入口與 VVIP'],
          ['preview', '預覽計算'],
          ['monsters', '怪物'],
          ['skills', '技能'],
          ['jobs', '職業'],
          ['weapontypes', '武器類型'],
          ['weapons', '武器'],
          ['armoritems', '防具'],
          ['aistrategies', 'AI 策略'],
          ['items', '道具'],
          ['scenes', '場景'],
          ['encounters', '遭遇'],
          ['companions', '隊友'],
          ['battlelogs', '戰鬥數據'],
        ] as [Tab, string][]).map(([v, label]) => (
          <button
            key={v}
            onClick={() => setTab(v)}
            style={{
              padding: '8px 14px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14,
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)',
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
              fontWeight: tab === v ? 700 : 400, flexShrink: 0, whiteSpace: 'nowrap',
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
      {tab === 'monsters' && token && <MonstersTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'skills' && token && <SkillsTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'jobs' && token && <JobsTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'weapontypes' && token && <WeaponTypesTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'weapons' && token && <WeaponsTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'armoritems' && token && <ArmorItemsTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'aistrategies' && token && <AiStrategiesTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'items' && token && <ItemsTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'scenes' && token && <ScenesTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'encounters' && token && <EncountersTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'companions' && token && <CompanionsTab token={token} onErr={setErr} onMsg={flash} />}
      {tab === 'battlelogs' && token && <BattleLogsTab token={token} onErr={setErr} />}
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
    // battle_element_chart 是巢狀 map（物件），String(obj) 只會得到沒用的 "[object Object]"——
    // 'json' 型欄位改存成排版過的 JSON 字串，textarea 才看得出內容；其餘欄位維持原本 String() 行為。
    for (const g of CONFIG_GROUPS) for (const f of g.fields) {
      const v = (c as any)[f.key]
      next[f.key] = f.type === 'json' ? JSON.stringify(v ?? {}, null, 2) : String(v)
    }
    return next
  }

  // 拋錯（而非回傳 null）讓呼叫端（save/openJson）沿用既有 try/catch 顯示錯誤訊息，不必各自重寫
  // 一份判斷——'json' 型欄位存的是使用者可編輯的原始文字，存檔/開進階編輯前都要先驗證格式。
  function buildConfig(): RpgConfig {
    const obj: any = {}
    for (const g of CONFIG_GROUPS) for (const f of g.fields) {
      if (f.type === 'json') {
        try {
          obj[f.key] = JSON.parse(edit[f.key] || '{}')
        } catch {
          throw new Error(`「${f.label}」不是合法的 JSON，請修正後再儲存`)
        }
      } else if (f.type === 'checkbox') {
        // 審查#2：checkbox 型欄位存的是 'true'/'false' 字串（見 seedEdit() 的 String(v) 慣例），
        // 存檔前轉回真正的 boolean。
        obj[f.key] = edit[f.key] === 'true'
      } else {
        obj[f.key] = f.type === 'select' ? edit[f.key] : Number(edit[f.key])
      }
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

  function openJson() {
    // buildConfig() 現在可能因為 'json' 型欄位（battle_element_chart）格式不對而拋錯——
    // 開進階編輯前也要驗證，不然會把例外丟到呼叫端沒接住的地方。
    try { setJsonText(JSON.stringify(buildConfig(), null, 2)); setShowJson(true) }
    catch (e: any) { onErr(e?.message || 'JSON 格式錯誤，未開啟') }
  }
  function applyJson() {
    try {
      const obj = JSON.parse(jsonText)
      const next: Record<string, string> = { ...edit }
      for (const g of CONFIG_GROUPS) for (const f of g.fields) {
        if (obj[f.key] !== undefined) {
          // 'json' 型欄位（物件）套回表單也要存成排版過的字串，理由同 seedEdit()。
          next[f.key] = f.type === 'json' ? JSON.stringify(obj[f.key], null, 2) : String(obj[f.key])
        }
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
              <F key={f.key} label={f.label} full={f.type === 'json'}>
                {f.type === 'select' ? (
                  <select style={inp} value={edit[f.key] ?? ''} onChange={(e) => setEdit((s) => ({ ...s, [f.key]: e.target.value }))}>
                    {f.options?.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
                  </select>
                ) : f.type === 'json' ? (
                  <textarea
                    style={{ ...ta, height: 160, fontFamily: 'monospace', fontSize: 12 }}
                    value={edit[f.key] ?? ''}
                    onChange={(e) => setEdit((s) => ({ ...s, [f.key]: e.target.value }))}
                  />
                ) : f.type === 'checkbox' ? (
                  <input
                    type="checkbox"
                    checked={edit[f.key] === 'true'}
                    onChange={(e) => setEdit((s) => ({ ...s, [f.key]: String(e.target.checked) }))}
                    style={{ width: 18, height: 18 }}
                  />
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

// ==================== DORPG P2：內容管理共用型別／小工具（契約 §5，怪物/技能/道具/場景/隊友） ====================
// 五種內容表（monsters/skills/items/scenes/companions）都是「表格列表＋下方編輯表單」同一種形狀，
// 用一個泛型 SimpleContentTab 共用增/刪/改流程，避免五份幾乎一樣的 CRUD 樣板碼；scenes 的 slots
// 與 encounters 的 monsters 編組屬於各表專屬的巢狀結構，另外用 extra 插槽渲染（見下方個別 Tab）。

type FieldType = 'text' | 'number' | 'checkbox' | 'select' | 'tags'
interface FieldSpec<T> {
  key: keyof T & string
  label: string
  type?: FieldType // 省略＝text
  step?: string // number 用，預設 'any'
  options?: { value: string; label: string }[] // select 用
  lockOnEdit?: boolean // 編輯既有列時鎖住（通常是主鍵欄位，改了等同另開新列）
  help?: string
}

function fieldInput<T>(f: FieldSpec<T>, value: any, disabled: boolean, onChange: (v: any) => void) {
  if (f.type === 'checkbox') {
    return <input type="checkbox" checked={!!value} disabled={disabled} onChange={(e) => onChange(e.target.checked)} style={{ width: 18, height: 18 }} />
  }
  if (f.type === 'select') {
    return (
      <select style={inp} value={String(value ?? '')} disabled={disabled} onChange={(e) => onChange(e.target.value)}>
        {f.options?.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
    )
  }
  if (f.type === 'number') {
    return <input style={inp} type="number" step={f.step ?? 'any'} value={value ?? 0} disabled={disabled} onChange={(e) => onChange(Number(e.target.value))} />
  }
  if (f.type === 'tags') {
    const text = Array.isArray(value) ? value.join(', ') : ''
    return <input style={inp} type="text" value={text} disabled={disabled} placeholder="以逗號分隔，如 heal, shield" onChange={(e) => onChange(e.target.value.split(',').map((s) => s.trim()).filter(Boolean))} />
  }
  return <input style={inp} type="text" value={value ?? ''} disabled={disabled} onChange={(e) => onChange(e.target.value)} />
}

interface SimpleContentTabProps<T> {
  token: string
  heading: string
  desc: string
  idKey: keyof T & string
  nameKey: keyof T & string
  fields: FieldSpec<T>[]
  list: (token: string) => Promise<T[]>
  put: (token: string, row: T) => Promise<T>
  remove: (token: string, id: string) => Promise<any>
  empty: () => T
  onErr: (m: string) => void
  onMsg: (m: string) => void
  // scenes/companions 有主表以外的巢狀編輯區（slots／skill_ids 已走 tags，這裡保留擴充點給 scenes 的
  // 五槽位座標編輯）；不需要就不傳。
  renderExtra?: (form: T, setForm: (next: T) => void) => React.ReactNode
}

function SimpleContentTab<T extends { is_active: boolean; sort_order: number }>({
  token, heading, desc, idKey, nameKey, fields, list, put, remove, empty, onErr, onMsg, renderExtra,
}: SimpleContentTabProps<T>) {
  const [rows, setRows] = useState<T[] | null>(null)
  const [form, setForm] = useState<T | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    list(token).then(setRows).catch((e: any) => onErr(e?.message || '載入失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { load() }, [load])

  function startNew() { setForm(empty()); setIsNew(true) }
  function startEdit(r: T) { setForm({ ...r }); setIsNew(false) }
  function setF<K extends keyof T>(k: K, v: T[K]) { setForm((f) => (f ? { ...f, [k]: v } : f)) }

  async function save() {
    if (!form) return
    const id = String((form as any)[idKey] ?? '').trim()
    if (!id) { onErr(`請填 ${String(idKey)}`); return }
    if (!String((form as any)[nameKey] ?? '').trim()) { onErr('請填名稱'); return }
    setBusy(true)
    try {
      const saved = await put(token, form)
      onMsg(`已儲存「${(saved as any)[nameKey] ?? (saved as any)[idKey]}」`)
      setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function del(r: T) {
    const label = (r as any)[nameKey] ?? (r as any)[idKey]
    if (!window.confirm(`確定刪除「${label}」？`)) return
    setBusy(true)
    try {
      await remove(token, String((r as any)[idKey]))
      onMsg(`已刪除「${label}」`)
      if (form && (form as any)[idKey] === (r as any)[idKey]) setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>{heading}</h2>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: 0, maxWidth: 640, lineHeight: 1.7 }}>{desc}</p>
        </div>
        {!form && <button onClick={startNew} style={primaryBtn}>＋ 新增</button>}
      </div>

      {rows === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
      {rows && rows.length === 0 && !form && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚無資料。</div>}
      {rows && rows.length > 0 && (
        <div style={{ overflowX: 'auto' }}>
          <Row head>
            <C w={2}>ID</C>
            <C w={2}>名稱</C>
            <C w={1}>啟用</C>
            <C w={1}>排序</C>
            <C w={2}>操作</C>
          </Row>
          {[...rows].sort((a, b) => a.sort_order - b.sort_order).map((r) => (
            <Row key={String((r as any)[idKey])}>
              <C w={2} dim>{String((r as any)[idKey])}</C>
              <C w={2}>{String((r as any)[nameKey])}</C>
              <C w={1}>{r.is_active ? <span style={{ color: 'var(--fug)' }}>✓</span> : <span style={{ color: 'var(--tx-faint)' }}>—</span>}</C>
              <C w={1} dim>{r.sort_order}</C>
              <C w={2}>
                <div style={{ display: 'flex', gap: 6 }}>
                  <button onClick={() => startEdit(r)} style={linkBtn}>編輯</button>
                  <button onClick={() => del(r)} disabled={busy} style={{ ...linkBtn, color: 'var(--hunt)' }}>刪除</button>
                </div>
              </C>
            </Row>
          ))}
        </div>
      )}

      {form && (
        <div style={panel}>
          <h3 style={{ fontSize: 14, fontWeight: 800, margin: '0 0 10px' }}>{isNew ? '新增' : `編輯：${(form as any)[nameKey] || (form as any)[idKey]}`}</h3>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            {fields.map((f) => (
              <F key={f.key} label={f.label}>
                {fieldInput(f, (form as any)[f.key], !!(f.lockOnEdit && !isNew), (v) => setF(f.key, v))}
              </F>
            ))}
          </div>
          {renderExtra?.(form, setForm)}
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={() => setForm(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

// ============================== 怪物 ==============================

function emptyMonster(): RpgMonster {
  return { id: '', name: '', rank: '', attribute: '', size: 'medium', race: '', sprite_id: '', poster_url: '', hp_mult: 1, atk_mult: 1, def_mult: 1, speed_mult: 1, threat: 0, is_boss: false, is_active: true, sort_order: 0 }
}
const MONSTER_FIELDS: FieldSpec<RpgMonster>[] = [
  { key: 'id', label: 'ID（如 DOR-MON-A-67000200001）', lockOnEdit: true },
  { key: 'name', label: '名稱' },
  { key: 'rank', label: '災害級（顯示文字，如 特S/S/特A/A~F）' },
  { key: 'attribute', label: '屬性（顯示文字，如 金/木/水/火/土/光/闇/無）' },
  { key: 'size', label: '體型（顯示文字，如 大型/中型/小型）' },
  { key: 'race', label: '種族' },
  { key: 'sprite_id', label: '圖集資料夾 ID（空＝用 ID）' },
  { key: 'poster_url', label: '海報圖 URL' },
  { key: 'hp_mult', label: 'HP 倍率（必須 > 0）', type: 'number' },
  { key: 'atk_mult', label: 'ATK 倍率（必須 > 0）', type: 'number' },
  { key: 'def_mult', label: 'DEF 倍率（必須 > 0）', type: 'number' },
  { key: 'speed_mult', label: '行動間隔倍率（<1 更頻繁，必須 > 0）', type: 'number' },
  { key: 'threat', label: '目標權重 threat（越高越優先被鎖定）', type: 'number', step: '1' },
  { key: 'is_boss', label: 'BOSS（顯示等級 +5）', type: 'checkbox' },
  { key: 'is_active', label: '啟用', type: 'checkbox' },
  { key: 'sort_order', label: '排序', type: 'number', step: '1' },
]
function MonstersTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  return (
    <SimpleContentTab<RpgMonster>
      token={token} heading="怪物管理"
      desc="數值一律是「倍率」不是絕對值——實際 HP/ATK/DEF 由後端依玩家戰力即時縮放（D1/D2，見「參數設定→戰鬥」分頁）。改這裡只影響相對強弱與外觀，不用擔心把數字填太大。"
      idKey="id" nameKey="name" fields={MONSTER_FIELDS}
      list={(t) => adminRpgApi.monsters(t).then((r) => r.monsters)}
      put={(t, row) => adminRpgApi.putMonster(t, row)}
      remove={(t, id) => adminRpgApi.deleteMonster(t, id)}
      empty={emptyMonster} onErr={onErr} onMsg={onMsg}
    />
  )
}

// ============================== 技能 ==============================
// DORPG P10（契約 §7 決定更新、CONTRACT.md）：P5 建表時「本輪沒有後台 CRUD」的技術債——job_id／
// path／tier／effect 等職業技能樹欄位過去只能靠 SQL migration 編輯。P10 為了讓後台能設定 taunt
// （kind='taunt'）與重騎士第三路線（path='c'），順便把整個 SkillRow 補齊成後台可編輯（原本
// SimpleContentTab 只擋在 3 個 kind、無 path 的舊表單），改成跟 WeaponsTab／ArmorItemsTab 同款
// 手刻表單（effect 是 JSON，比照 profile 的既有慣例存成排版字串，存檔前 JSON.parse 驗證）。

function emptySkill(): RpgSkill {
  return {
    id: '', name: '', icon_id: '', kind: 'damage', target: 'enemy', weapon: 'sword', element: 'neutral',
    mp_cost: 0, cooldown_ms: 4000, coefficient: 1, flat: 0, cast_ms: 300, is_default: false, is_active: true, sort_order: 0,
    job_id: null, path: '', tier: 0, max_level: 1, effect: {}, dmg_type: 'physical',
    prereq_skill_id: null, prereq_level: 0, mp_cost_per_level: 0, display_text: '', implemented: true,
  }
}

const SKILL_JOB_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: '（通用／既有 5 技能）' },
  { value: 'light_knight', label: '輕騎士' },
  { value: 'archer', label: '弓箭手' },
  { value: 'heavy_knight', label: '重騎士' },
  { value: 'cleric', label: '聖職者' },
  { value: 'merchant', label: '商人' },
  { value: 'mage', label: '魔法師' },
]
const SKILL_PATH_OPTIONS: { value: RpgSkill['path']; label: string }[] = [
  { value: '', label: '（無）' },
  { value: 'a', label: 'A' },
  { value: 'b', label: 'B' },
  { value: 'c', label: 'C（目前僅重騎士「守護」使用）' },
]
const SKILL_TARGET_OPTIONS: { value: RpgSkill['target']; label: string }[] = [
  { value: 'enemy', label: '敵方單體' },
  { value: 'allEnemies', label: '敵方全體' },
  { value: 'ally', label: '我方單體' },
  { value: 'self', label: '自己' },
  { value: 'allAllies', label: '我方全體' },
]
const SKILL_DMG_TYPE_OPTIONS: { value: RpgSkill['dmg_type']; label: string }[] = [
  { value: 'physical', label: '物理' },
  { value: 'magic', label: '魔法' },
]

function SkillsTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [rows, setRows] = useState<RpgSkill[] | null>(null)
  const [form, setForm] = useState<RpgSkill | null>(null)
  const [effectText, setEffectText] = useState('{}')
  const [isNew, setIsNew] = useState(false)
  const [busy, setBusy] = useState(false)
  const [filterJob, setFilterJob] = useState('')

  const load = useCallback(() => {
    adminRpgApi.skills(token).then((r) => setRows(r.skills)).catch((e: any) => onErr(e?.message || '載入失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { load() }, [load])

  const filteredRows = (rows ?? []).filter((s) => {
    if (!filterJob) return true
    if (filterJob === 'null') return !s.job_id
    return s.job_id === filterJob
  })

  function startNew() {
    const f = emptySkill()
    setForm(f); setEffectText(JSON.stringify(f.effect, null, 2)); setIsNew(true)
  }
  function startEdit(r: RpgSkill) { setForm({ ...r }); setEffectText(JSON.stringify(r.effect ?? {}, null, 2)); setIsNew(false) }

  async function save() {
    if (!form) return
    if (!form.id.trim()) { onErr('請填 ID'); return }
    if (!form.name.trim()) { onErr('請填名稱'); return }
    let effect: RpgSkill['effect']
    try { effect = JSON.parse(effectText || '{}') } catch { onErr('「效果 effect」不是合法的 JSON，請修正後再儲存'); return }
    setBusy(true)
    try {
      const saved = await adminRpgApi.putSkill(token, { ...form, job_id: form.job_id || null, prereq_skill_id: form.prereq_skill_id || null, effect })
      onMsg(`已儲存「${saved.name}」`)
      setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function del(r: RpgSkill) {
    if (!window.confirm(`確定刪除「${r.name}」？`)) return
    setBusy(true)
    try {
      await adminRpgApi.deleteSkill(token, r.id)
      onMsg(`已刪除「${r.name}」`)
      if (form?.id === r.id) setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>技能管理</h2>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: 0, maxWidth: 640, lineHeight: 1.7 }}>
            通用技能（job_id 空白）是 8 格技能列固定容量，玩家未自訂 loadout 時依 is_default 與排序自動補入；
            flat 是「參考玩家（HPMax=battle_reference_hp，預設 300）身上」的絕對值，正式對戰依實際玩家 HPMax
            等比例縮放（heal/shield）。指定 job_id 的是職業技能樹（path a/b/c，tier 決定路線內順位），effect 是
            隨等級展開的效果 JSON——kind=taunt 用 duration_base_ms/duration_per_level_ms/damage_taken_pct_base/
            damage_taken_pct_per_level/retarget，kind=passive 想讓「按防禦」也進入守護狀態則加 guard_taunt:true
            （見契約 dorpg_p5/dorpg_p10 CONTRACT §5）。
          </p>
        </div>
        {!form && <button onClick={startNew} style={primaryBtn}>＋ 新增</button>}
      </div>

      {!form && (
        <F label="篩選：職業">
          <select style={inp} value={filterJob} onChange={(e) => setFilterJob(e.target.value)}>
            <option value="">全部</option>
            <option value="null">通用（無職業）</option>
            {SKILL_JOB_OPTIONS.filter((o) => o.value).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
          </select>
        </F>
      )}

      {rows === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
      {rows && filteredRows.length === 0 && !form && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚無符合篩選條件的資料。</div>}
      {rows && filteredRows.length > 0 && !form && (
        <div style={{ overflowX: 'auto' }}>
          <Row head>
            <C w={2}>ID</C>
            <C w={2}>名稱</C>
            <C w={1}>種類</C>
            <C w={2}>職業／路線／Tier</C>
            <C w={1}>啟用</C>
            <C w={2}>操作</C>
          </Row>
          {[...filteredRows]
            .sort((a, b) => (a.job_id ?? '').localeCompare(b.job_id ?? '') || a.path.localeCompare(b.path) || a.tier - b.tier || a.sort_order - b.sort_order)
            .map((s) => (
              <Row key={s.id}>
                <C w={2} dim>{s.id}</C>
                <C w={2}>{s.name}</C>
                <C w={1} dim>{SKILL_KIND_LABEL[s.kind] ?? s.kind}</C>
                <C w={2} dim>{s.job_id ? `${SKILL_JOB_OPTIONS.find((o) => o.value === s.job_id)?.label ?? s.job_id}・${s.path || '—'}・T${s.tier}` : '通用'}</C>
                <C w={1}>{s.is_active ? <span style={{ color: 'var(--fug)' }}>✓</span> : <span style={{ color: 'var(--tx-faint)' }}>—</span>}</C>
                <C w={2}>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <button onClick={() => startEdit(s)} style={linkBtn}>編輯</button>
                    <button onClick={() => del(s)} disabled={busy} style={{ ...linkBtn, color: 'var(--hunt)' }}>刪除</button>
                  </div>
                </C>
              </Row>
            ))}
        </div>
      )}

      {form && (
        <div style={panel}>
          <h3 style={{ fontSize: 14, fontWeight: 800, margin: '0 0 10px' }}>{isNew ? '新增' : `編輯：${form.name || form.id}`}</h3>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            <F label="ID"><input style={inp} type="text" value={form.id} disabled={!isNew} onChange={(e) => setForm({ ...form, id: e.target.value })} /></F>
            <F label="名稱"><input style={inp} type="text" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} /></F>
            <F label="圖示 ID（如 icon_skill_slash）"><input style={inp} type="text" value={form.icon_id} onChange={(e) => setForm({ ...form, icon_id: e.target.value })} /></F>
            <F label="種類">
              <select style={inp} value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value as RpgSkill['kind'] })}>
                {(Object.keys(SKILL_KIND_LABEL) as RpgSkill['kind'][]).map((k) => <option key={k} value={k}>{SKILL_KIND_LABEL[k]}</option>)}
              </select>
            </F>
            <F label="目標">
              <select style={inp} value={form.target} onChange={(e) => setForm({ ...form, target: e.target.value as RpgSkill['target'] })}>
                {SKILL_TARGET_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </F>
            <F label="武器（特效/音效組）">
              <select style={inp} value={form.weapon} onChange={(e) => setForm({ ...form, weapon: e.target.value as RpgSkill['weapon'] })}>
                {[{ v: 'sword', l: '劍' }, { v: 'staff', l: '法杖' }, { v: 'bow', l: '弓' }, { v: 'greatsword', l: '大劍' }].map((o) => <option key={o.v} value={o.v}>{o.l}</option>)}
              </select>
            </F>
            <F label="屬性">
              <select style={inp} value={form.element} onChange={(e) => setForm({ ...form, element: e.target.value })}>
                {['metal', 'wood', 'water', 'fire', 'earth', 'light', 'dark', 'neutral'].map((v) => <option key={v} value={v}>{v}</option>)}
              </select>
            </F>
            <F label="MP 消耗（基礎）"><input style={inp} type="number" step="1" value={form.mp_cost} onChange={(e) => setForm({ ...form, mp_cost: Number(e.target.value) })} /></F>
            <F label="冷卻（毫秒）"><input style={inp} type="number" step="1" value={form.cooldown_ms} onChange={(e) => setForm({ ...form, cooldown_ms: Number(e.target.value) })} /></F>
            <F label="傷害/治療係數"><input style={inp} type="number" value={form.coefficient} onChange={(e) => setForm({ ...form, coefficient: Number(e.target.value) })} /></F>
            <F label="固定加成值 flat"><input style={inp} type="number" step="1" value={form.flat} onChange={(e) => setForm({ ...form, flat: Number(e.target.value) })} /></F>
            <F label="施放時間（毫秒）"><input style={inp} type="number" step="1" value={form.cast_ms} onChange={(e) => setForm({ ...form, cast_ms: Number(e.target.value) })} /></F>
            <F label="預設帶入（未設定 loadout 時，僅通用技能有意義）">
              <input type="checkbox" checked={form.is_default} onChange={(e) => setForm({ ...form, is_default: e.target.checked })} style={{ width: 18, height: 18 }} />
            </F>
            <F label="啟用"><input type="checkbox" checked={form.is_active} onChange={(e) => setForm({ ...form, is_active: e.target.checked })} style={{ width: 18, height: 18 }} /></F>
            <F label="排序"><input style={inp} type="number" step="1" value={form.sort_order} onChange={(e) => setForm({ ...form, sort_order: Number(e.target.value) })} /></F>

            <F label="所屬職業（空＝通用）">
              <select style={inp} value={form.job_id ?? ''} onChange={(e) => setForm({ ...form, job_id: e.target.value || null })}>
                {SKILL_JOB_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </F>
            <F label="路線 path">
              <select style={inp} value={form.path} onChange={(e) => setForm({ ...form, path: e.target.value as RpgSkill['path'] })}>
                {SKILL_PATH_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </F>
            <F label="Tier（路線內順位，1 起算）"><input style={inp} type="number" step="1" value={form.tier} onChange={(e) => setForm({ ...form, tier: Number(e.target.value) })} /></F>
            <F label="技能等級上限 max_level"><input style={inp} type="number" step="1" value={form.max_level} onChange={(e) => setForm({ ...form, max_level: Number(e.target.value) })} /></F>
            <F label="傷害屬性 dmg_type（僅 kind=damage 有意義）">
              <select style={inp} value={form.dmg_type} onChange={(e) => setForm({ ...form, dmg_type: e.target.value as RpgSkill['dmg_type'] })}>
                {SKILL_DMG_TYPE_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </F>
            <F label="前置技能 ID（留空＝無前置）"><input style={inp} type="text" value={form.prereq_skill_id ?? ''} onChange={(e) => setForm({ ...form, prereq_skill_id: e.target.value || null })} /></F>
            <F label="前置需求等級"><input style={inp} type="number" step="1" value={form.prereq_level} onChange={(e) => setForm({ ...form, prereq_level: Number(e.target.value) })} /></F>
            <F label="每級 MP 消耗遞增 mp_cost_per_level"><input style={inp} type="number" value={form.mp_cost_per_level} onChange={(e) => setForm({ ...form, mp_cost_per_level: Number(e.target.value) })} /></F>
            <F label="實裝（關閉＝可配點但戰鬥中不可用）">
              <input type="checkbox" checked={form.implemented} onChange={(e) => setForm({ ...form, implemented: e.target.checked })} style={{ width: 18, height: 18 }} />
            </F>
            <F label="效果說明文字 display_text" full>
              <textarea style={{ ...ta, height: 60 }} value={form.display_text} onChange={(e) => setForm({ ...form, display_text: e.target.value })} />
            </F>
            <F label="效果 effect（JSON，見契約 dorpg_p5/dorpg_p10 CONTRACT §5；通用技能／damage/heal/shield 留 {} 即可）" full>
              <textarea style={{ ...ta, height: 160, fontFamily: 'monospace', fontSize: 12 }} value={effectText} onChange={(e) => setEffectText(e.target.value)} />
            </F>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={() => setForm(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

// ============================== 職業（P10 起後台可編輯 path_c／traits） ==============================
// DORPG P10（CONTRACT §2/§7、WIRE §後台）：六職業固定列，P5 決定「本輪沒有後台 CRUD」；P10 為了
// 讓後台能設定重騎士第三路線與職業特性，追加這個窄用途的編輯能力——只開放編輯 path_c_id/
// path_c_name/path_c_desc（目前只有重騎士「守護」路線在用）與 traits（職業天生特性 JSON，目前
// 只有重騎士 damage_taken_pct:-15）；其餘欄位唯讀顯示，不提供新增／刪除（六職業由 migration 180
// seed，id 生命週期內視為靜態資料，比照 WeaponTypesTab 既有慣例）。

function JobsTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [rows, setRows] = useState<RpgJob[] | null>(null)
  const [form, setForm] = useState<RpgJob | null>(null)
  // DORPG P10：path_c 用獨立的三個字串 state 編輯（而不是直接改 form.path_c），因為 GET 回應的
  // path_c 在多數職業上根本不存在（undefined，見 RpgJob 型別註解），存檔時再組成 PUT body 要求的
  // 完整物件——id 空字串＝清除這條路線（見 adminJobPutRequest 後端註解）。
  const [pathCId, setPathCId] = useState('')
  const [pathCName, setPathCName] = useState('')
  const [pathCDesc, setPathCDesc] = useState('')
  const [traitsText, setTraitsText] = useState('{}')
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    adminRpgApi.jobs(token).then((r) => setRows(r.jobs)).catch((e: any) => onErr(e?.message || '載入失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { load() }, [load])

  function startEdit(r: RpgJob) {
    setForm({ ...r })
    setPathCId(r.path_c?.id ?? '')
    setPathCName(r.path_c?.name ?? '')
    setPathCDesc(r.path_c?.desc ?? '')
    setTraitsText(JSON.stringify(r.traits ?? {}, null, 2))
  }

  async function save() {
    if (!form) return
    let traits: RpgJob['traits']
    try { traits = JSON.parse(traitsText || '{}') } catch { onErr('「職業特性 traits」不是合法的 JSON，請修正後再儲存'); return }
    setBusy(true)
    try {
      const saved = await adminRpgApi.putJob(token, {
        ...form,
        path_c: { id: pathCId.trim(), name: pathCName.trim(), desc: pathCDesc.trim() },
        traits,
      })
      onMsg(`已儲存「${saved.name}」`)
      setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div>
        <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>職業管理</h2>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: 0, maxWidth: 640, lineHeight: 1.7 }}>
          六職業固定資料（無新增／刪除）。這裡只開放編輯第三條路線 path_c（目前僅重騎士「守護」，
          路線 ID 留空＝此職業沒有第三條路線）與職業天生特性 traits（目前僅重騎士
          damage_taken_pct:-15，套用在戰鬥受到傷害減免，角色頁會顯示「職業特性：受到傷害 −15%」
          這行文字）；其餘欄位唯讀顯示，如需調整請走 migration。
        </p>
      </div>

      {rows === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
      {rows && !form && (
        <div style={{ overflowX: 'auto' }}>
          <Row head>
            <C w={2}>ID</C>
            <C w={2}>名稱</C>
            <C w={2}>路線 C</C>
            <C w={2}>traits</C>
            <C w={1}>操作</C>
          </Row>
          {[...rows].sort((a, b) => a.sort_order - b.sort_order).map((j) => (
            <Row key={j.id}>
              <C w={2} dim>{j.id}</C>
              <C w={2}>{j.name}</C>
              <C w={2} dim>{j.path_c?.name || '—'}</C>
              <C w={2} dim>{Object.keys(j.traits ?? {}).length ? JSON.stringify(j.traits) : '—'}</C>
              <C w={1}><button onClick={() => startEdit(j)} style={linkBtn}>編輯</button></C>
            </Row>
          ))}
        </div>
      )}

      {form && (
        <div style={panel}>
          <h3 style={{ fontSize: 14, fontWeight: 800, margin: '0 0 10px' }}>編輯：{form.name}（{form.id}）</h3>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            <F label="路線 A（唯讀）"><input style={inp} type="text" value={`${form.path_a.name}・${form.path_a.desc}`} disabled /></F>
            <F label="路線 B（唯讀）"><input style={inp} type="text" value={`${form.path_b.name}・${form.path_b.desc}`} disabled /></F>
            <F label="路線 C ID（留空＝此職業無第三路線）"><input style={inp} type="text" value={pathCId} onChange={(e) => setPathCId(e.target.value)} /></F>
            <F label="路線 C 名稱"><input style={inp} type="text" value={pathCName} onChange={(e) => setPathCName(e.target.value)} /></F>
            <F label="路線 C 說明" full><textarea style={{ ...ta, height: 60 }} value={pathCDesc} onChange={(e) => setPathCDesc(e.target.value)} /></F>
            <F label={'職業特性 traits（JSON，如 {"damage_taken_pct":-15}）'} full>
              <textarea style={{ ...ta, height: 100, fontFamily: 'monospace', fontSize: 12 }} value={traitsText} onChange={(e) => setTraitsText(e.target.value)} />
            </F>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={() => setForm(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

// ============================== 道具 ==============================

function emptyItem(): RpgItem {
  return { id: '', name: '', icon_id: '', kind: 'hp', amount: 0, default_quantity: 0, is_active: true, sort_order: 0 }
}
const ITEM_FIELDS: FieldSpec<RpgItem>[] = [
  { key: 'id', label: 'ID', lockOnEdit: true },
  { key: 'name', label: '名稱' },
  { key: 'icon_id', label: '圖示 ID' },
  { key: 'kind', label: '種類', type: 'select', options: [{ value: 'hp', label: 'HP 回復' }, { value: 'mp', label: 'MP 回復' }, { value: 'revive', label: '復活' }] },
  { key: 'amount', label: '回復量（hp：依玩家 HPMax÷battle_reference_hp 縮放；mp：依玩家 MPMax÷battle_reference_mp 縮放；revive＝復活後 HP% 不縮放，須 ≤100）', type: 'number', step: '1' },
  { key: 'default_quantity', label: '每場戰鬥預設攜帶數量', type: 'number', step: '1' },
  { key: 'is_active', label: '啟用', type: 'checkbox' },
  { key: 'sort_order', label: '排序', type: 'number', step: '1' },
]
function ItemsTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  return (
    <SimpleContentTab<RpgItem>
      token={token} heading="道具管理" desc="default_quantity > 0 的道具會在戰鬥開始前自動放進玩家道具欄。hp/mp 的 amount 是「參考玩家（HPMax=battle_reference_hp、MPMax=battle_reference_mp，預設 300/100）身上」的絕對回復量——正式對戰會依實際玩家 HPMax/MPMax 等比例縮放，同一瓶藥水對任何等級的角色補的百分比大致一致。"
      idKey="id" nameKey="name" fields={ITEM_FIELDS}
      list={(t) => adminRpgApi.items(t).then((r) => r.items)}
      put={(t, row) => adminRpgApi.putItem(t, row)}
      remove={(t, id) => adminRpgApi.deleteItem(t, id)}
      empty={emptyItem} onErr={onErr} onMsg={onMsg}
    />
  )
}

// ============================== 場景（slots 五槽位座標另外編輯） ==============================

function slotDefault(id: typeof ENCOUNTER_SLOTS[number]): RpgSceneSlot {
  const row: RpgSceneSlot['row'] = id.startsWith('rear') ? 'rear' : 'front'
  return { id, x: 0.5, y: row === 'rear' ? 0.5 : 0.65, scale: 0.25, row }
}
function emptySceneSlots(): RpgSceneSlot[] {
  return ENCOUNTER_SLOTS.map(slotDefault)
}
// normalizeSlots 確保表單一律有固定 5 槽位、順序固定，缺的補預設值——避免舊資料若曾經缺槽位讓
// 編輯器渲染不出對應的輸入列。
function normalizeSlots(slots: RpgSceneSlot[]): RpgSceneSlot[] {
  const byId = new Map(slots.map((s) => [s.id, s] as const))
  return emptySceneSlots().map((d) => byId.get(d.id) ?? d)
}
function emptyScene(): RpgScene {
  return { id: '', name: '', image_url: '', slots: emptySceneSlots(), location_note: '', is_active: true, sort_order: 0 }
}
const SCENE_FIELDS: FieldSpec<RpgScene>[] = [
  { key: 'id', label: 'ID', lockOnEdit: true },
  { key: 'name', label: '名稱' },
  { key: 'image_url', label: '背景圖 URL' },
  { key: 'location_note', label: '地點說明（顯示用）' },
  { key: 'is_active', label: '啟用', type: 'checkbox' },
  { key: 'sort_order', label: '排序', type: 'number', step: '1' },
]
function SceneSlotsEditor({ form, setForm }: { form: RpgScene; setForm: (next: RpgScene) => void }) {
  const slots = normalizeSlots(form.slots)
  function setSlot(i: number, patch: Partial<RpgSceneSlot>) {
    const next = slots.map((s, idx) => (idx === i ? { ...s, ...patch } : s))
    setForm({ ...form, slots: next })
  }
  return (
    <div style={{ marginTop: 14 }}>
      <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>五個怪物槽位座標（比例 0~1，row&lt;0.7 通常算後排）——逐字取自內容包 scene.json 的 monsterSlots，改動請對照美術原稿。</div>
      <div style={{ overflowX: 'auto' }}>
        <Row head><C w={1}>槽位</C><C w={1}>X</C><C w={1}>Y</C><C w={1}>Scale</C><C w={1}>Row</C></Row>
        {slots.map((s, i) => (
          <Row key={s.id}>
            <C w={1} dim>{SLOT_LABEL[s.id] ?? s.id}</C>
            <C w={1}><input style={inp} type="number" step="0.01" value={s.x} onChange={(e) => setSlot(i, { x: Number(e.target.value) })} /></C>
            <C w={1}><input style={inp} type="number" step="0.01" value={s.y} onChange={(e) => setSlot(i, { y: Number(e.target.value) })} /></C>
            <C w={1}><input style={inp} type="number" step="0.01" value={s.scale} onChange={(e) => setSlot(i, { scale: Number(e.target.value) })} /></C>
            <C w={1}>
              <select style={inp} value={s.row} onChange={(e) => setSlot(i, { row: e.target.value as 'rear' | 'front' })}>
                <option value="rear">rear</option>
                <option value="front">front</option>
              </select>
            </C>
          </Row>
        ))}
      </div>
    </div>
  )
}
function ScenesTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  return (
    <SimpleContentTab<RpgScene>
      token={token} heading="場景管理" desc="五個怪物槽位座標決定該場戰鬥怪物站位；場景本身不含難度/編組，那些在「遭遇」分頁設定。"
      idKey="id" nameKey="name" fields={SCENE_FIELDS}
      list={(t) => adminRpgApi.scenes(t).then((r) => r.scenes)}
      put={(t, row) => adminRpgApi.putScene(t, { ...row, slots: normalizeSlots(row.slots) })}
      remove={(t, id) => adminRpgApi.deleteScene(t, id)}
      empty={emptyScene} onErr={onErr} onMsg={onMsg}
      renderExtra={(form, setForm) => <SceneSlotsEditor form={form} setForm={setForm} />}
    />
  )
}

// ============================== 隊友 ==============================

function emptyCompanion(): RpgCompanion {
  return { id: '', name: '', portrait_id: '', role: '', weapon: 'sword', level_offset: 0, hp_mult: 1, mp_mult: 1, atk_mult: 1, matk_mult: 1, def_mult: 1, mdef_mult: 1, act_interval_mult: 1, skill_ids: [], is_player_portrait: false, is_active: true, sort_order: 0 }
}
const COMPANION_FIELDS: FieldSpec<RpgCompanion>[] = [
  { key: 'id', label: 'ID（如 char_xiaomi）', lockOnEdit: true },
  { key: 'name', label: '名稱' },
  { key: 'portrait_id', label: '頭像 ID' },
  { key: 'role', label: '角色定位（顯示用，如「治療」）' },
  { key: 'weapon', label: '武器', type: 'select', options: [{ value: 'sword', label: '劍' }, { value: 'staff', label: '法杖' }, { value: 'bow', label: '弓' }, { value: 'greatsword', label: '大劍' }] },
  { key: 'level_offset', label: '等級偏移（顯示等級＝玩家 Base Lv + 此值）', type: 'number', step: '1' },
  { key: 'hp_mult', label: 'HP 倍率（乘玩家數值，必須 > 0）', type: 'number' },
  { key: 'mp_mult', label: 'MP 倍率（必須 > 0）', type: 'number' },
  { key: 'atk_mult', label: 'ATK 倍率（必須 > 0）', type: 'number' },
  { key: 'matk_mult', label: 'MATK 倍率（必須 > 0）', type: 'number' },
  { key: 'def_mult', label: 'DEF 倍率（必須 > 0）', type: 'number' },
  { key: 'mdef_mult', label: 'MDEF 倍率（必須 > 0）', type: 'number' },
  { key: 'act_interval_mult', label: '行動間隔倍率（必須 > 0）', type: 'number' },
  { key: 'skill_ids', label: 'AI 可用技能 ID（逗號分隔，目前引擎只認 heal）', type: 'tags' },
  { key: 'is_player_portrait', label: '玩家頭像列（全表只能有一列勾選，衝突會被後端擋下）', type: 'checkbox' },
  { key: 'is_active', label: '啟用（此列會出現在隊伍前 4 位候選）', type: 'checkbox' },
  { key: 'sort_order', label: '排序（決定被選入隊伍的優先序）', type: 'number', step: '1' },
]
function CompanionsTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  return (
    <SimpleContentTab<RpgCompanion>
      token={token} heading="隊友管理"
      desc="隊友數值＝玩家數值 × 各項倍率（D3），不另存絕對值。標「玩家頭像列」那一列本身不會出現在隊伍候選裡，只提供 portrait_id 給玩家自己使用。"
      idKey="id" nameKey="name" fields={COMPANION_FIELDS}
      list={(t) => adminRpgApi.companions(t).then((r) => r.companions)}
      put={(t, row) => adminRpgApi.putCompanion(t, row)}
      remove={(t, id) => adminRpgApi.deleteCompanion(t, id)}
      empty={emptyCompanion} onErr={onErr} onMsg={onMsg}
    />
  )
}

// ============================== 遭遇（含五槽位怪物編組） ==============================

function emptyEncounter(): RpgEncounter {
  return { code: '', title: '', subtitle: '', scene_id: '', scene_kind: 'normal', difficulty: 1, power_scale: 1, escape_chance: 0.35, can_escape: true, is_active: true, sort_order: 0, monsters: [], monster_level: 10 }
}
const ENCOUNTER_FIELDS: FieldSpec<RpgEncounter>[] = [
  { key: 'code', label: '代碼 code（對外識別，API 用這個不用內部 UUID）', lockOnEdit: true },
  { key: 'title', label: '標題' },
  { key: 'subtitle', label: '副標' },
  { key: 'scene_kind', label: '場景種類（影響 BGM 選曲）', type: 'select', options: [{ value: 'normal', label: '一般' }, { value: 'boss', label: 'BOSS' }] },
  { key: 'difficulty', label: '難度（1~5，前端畫星）', type: 'number', step: '1' },
  // DORPG P6（契約 §2）：怪物等級制——這一場怪物的等級 N，battle_scale_mode="level" 時驅動
  // RefPlayer(N) 縮放；六場預設 10/20/30/40/50/60，EncounterPicker 卡片會顯示「怪物 Lv.N」。
  { key: 'monster_level', label: '怪物等級 monster_level（1–99）', type: 'number', step: '1' },
  { key: 'power_scale', label: '整場戰力倍率 power_scale（level 模式也會乘進怪物 HP/ATK/DEF/MDEF；migration 185 起六場預設 1.0＝不調，只當單場微調用）', type: 'number' },
  { key: 'escape_chance', label: '逃跑成功率（0~1）', type: 'number', step: '0.01' },
  { key: 'can_escape', label: '可逃跑', type: 'checkbox' },
  { key: 'is_active', label: '啟用（會出現在選單）', type: 'checkbox' },
  { key: 'sort_order', label: '排序', type: 'number', step: '1' },
]
function EncounterMonstersEditor({
  form, setForm, monsters,
}: { form: RpgEncounter; setForm: (next: RpgEncounter) => void; monsters: RpgMonster[] }) {
  const bySlot = new Map(form.monsters.map((m) => [m.slot, m] as const))
  function setSlot(slot: RpgEncounterMonster['slot'], patch: Partial<RpgEncounterMonster>) {
    const current: RpgEncounterMonster = bySlot.get(slot) ?? { slot, monster_id: '', power_scale: 1 }
    const next = { ...current, ...patch }
    const rest = form.monsters.filter((m) => m.slot !== slot)
    // monster_id 清空＝這個槽位不放怪，直接從編組移除（後端 Validate 要求非空 monster_id）。
    setForm({ ...form, monsters: next.monster_id ? [...rest, next] : rest })
  }
  return (
    <div style={{ marginTop: 14 }}>
      <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 6 }}>五個槽位挑選怪物；留空＝該場不出這隻怪（例如訓練場只用 3 槽）。個別 power_scale 只再乘 HP，不影響 DEF/ATK。</div>
      <div style={{ overflowX: 'auto' }}>
        <Row head><C w={1}>槽位</C><C w={2}>怪物</C><C w={1}>個別 power_scale</C></Row>
        {ENCOUNTER_SLOTS.map((slot) => {
          const cur = bySlot.get(slot)
          return (
            <Row key={slot}>
              <C w={1} dim>{SLOT_LABEL[slot]}</C>
              <C w={2}>
                <select style={inp} value={cur?.monster_id ?? ''} onChange={(e) => setSlot(slot, { monster_id: e.target.value })}>
                  <option value="">（不使用此槽位）</option>
                  {monsters.map((m) => <option key={m.id} value={m.id}>{m.name}{m.is_boss ? '（BOSS）' : ''}</option>)}
                </select>
              </C>
              <C w={1}>
                <input style={inp} type="number" step="0.01" value={cur?.power_scale ?? 1} disabled={!cur} onChange={(e) => setSlot(slot, { power_scale: Number(e.target.value) })} />
              </C>
            </Row>
          )
        })}
      </div>
    </div>
  )
}
function EncountersTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [scenes, setScenes] = useState<RpgScene[]>([])
  const [monsters, setMonsters] = useState<RpgMonster[]>([])
  useEffect(() => {
    adminRpgApi.scenes(token).then((r) => setScenes(r.scenes)).catch((e: any) => onErr(e?.message || '載入場景失敗'))
    adminRpgApi.monsters(token).then((r) => setMonsters(r.monsters)).catch((e: any) => onErr(e?.message || '載入怪物失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  const fieldsWithSceneSelect: FieldSpec<RpgEncounter>[] = [
    ENCOUNTER_FIELDS[0], ENCOUNTER_FIELDS[1], ENCOUNTER_FIELDS[2],
    { key: 'scene_id', label: '場景', type: 'select', options: scenes.map((s) => ({ value: s.id, label: s.name })) },
    ...ENCOUNTER_FIELDS.slice(3),
  ]

  return (
    <SimpleContentTab<RpgEncounter>
      token={token} heading="遭遇管理" desc="一個遭遇＝一個場景＋最多 5 隻怪物編組。玩家在選單頁看到的就是這裡的 title/subtitle/怪物頭像列。"
      idKey="code" nameKey="title" fields={fieldsWithSceneSelect}
      list={(t) => adminRpgApi.encounters(t).then((r) => r.encounters)}
      put={(t, row) => adminRpgApi.putEncounter(t, row)}
      remove={(t, code) => adminRpgApi.deleteEncounter(t, code)}
      empty={emptyEncounter} onErr={onErr} onMsg={onMsg}
      renderExtra={(form, setForm) => <EncounterMonstersEditor form={form} setForm={setForm} monsters={monsters} />}
    />
  )
}

// ============================== 戰鬥數據 ==============================

function fmtPct(v: number): string { return `${(v * 100).toFixed(1)}%` }
function fmtMs(ms: number): string {
  if (!ms || ms <= 0) return '—'
  const total = Math.round(ms / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
function outcomeLabel(o: string): string {
  return { victory: '勝利', defeat: '戰敗', draw: '平手', escaped: '逃跑', abandoned: '中離' }[o] ?? o
}
function BattleLogsTab({ token, onErr }: { token: string; onErr: (m: string) => void }) {
  const [days, setDays] = useState<7 | 30>(7)
  const [code, setCode] = useState('')
  const [summary, setSummary] = useState<RpgBattleLogSummary[] | null>(null)
  const [rows, setRows] = useState<RpgBattleLogRow[] | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(() => {
    setLoading(true)
    adminRpgApi.battleLogs(token, { days, code: code || undefined, limit: 200 })
      .then((r) => { setSummary(r.summary); setRows(r.rows) })
      .catch((e: any) => onErr(e?.message || '載入戰鬥數據失敗'))
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token, days, code])
  useEffect(() => { load() }, [load])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={panel}>
        <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: '0 0 10px', lineHeight: 1.7 }}>
          玩很多場之後靠這裡看手感：勝率／時長／承受傷害落點是否符合設計目標（見 BALANCE 報告），不合就回「參數設定→戰鬥」或各內容分頁調整倍率。不顯示 Email 或帳號編碼。
        </p>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', border: '1px solid var(--line-2)', borderRadius: 8, overflow: 'hidden' }}>
            {([7, 30] as const).map((d) => (
              <button key={d} onClick={() => setDays(d)} style={{
                padding: '7px 16px', border: 'none', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
                background: days === d ? 'var(--fug)' : 'transparent', color: days === d ? 'var(--fug-ink)' : 'var(--tx)', fontWeight: days === d ? 700 : 400,
              }}>近 {d} 天</button>
            ))}
          </div>
          <input style={{ ...inp, width: 220 }} placeholder="篩選單一遭遇 code（留空＝全部）" value={code} onChange={(e) => setCode(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') load() }} />
          <button onClick={load} style={ghostBtn} disabled={loading}>{loading ? '載入中…' : '重新整理'}</button>
        </div>
      </div>

      <div style={panel}>
        <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 10px' }}>每遭遇彙總</h2>
        {summary === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
        {summary && summary.length === 0 && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>此期間尚無戰鬥紀錄。</div>}
        {summary && summary.length > 0 && (
          <div style={{ overflowX: 'auto' }}>
            <Row head>
              <C w={2}>遭遇</C><C w={1}>場次</C><C w={1}>勝率</C><C w={1}>敗率</C><C w={1}>平均時長</C><C w={1}>中位時長</C><C w={2}>平均承受傷害</C>
            </Row>
            {summary.map((s) => (
              <Row key={s.encounter_code}>
                <C w={2}>{s.encounter_code}</C>
                <C w={1}>{s.plays}</C>
                <C w={1}><span style={{ color: s.win_rate >= 0.5 ? 'var(--fug)' : 'var(--tx)', fontWeight: 700 }}>{fmtPct(s.win_rate)}</span></C>
                <C w={1} dim>{fmtPct(s.defeat_rate)}</C>
                <C w={1}>{fmtMs(s.avg_duration_ms)}</C>
                <C w={1}>{fmtMs(s.p50_duration_ms)}</C>
                <C w={2} dim>{Math.round(s.avg_damage_taken)}</C>
              </Row>
            ))}
          </div>
        )}
      </div>

      <div style={panel}>
        <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 10px' }}>最近戰鬥明細（最多 200 筆）</h2>
        {rows && rows.length === 0 && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>此期間尚無戰鬥紀錄。</div>}
        {rows && rows.length > 0 && (
          <div style={{ overflowX: 'auto' }}>
            <Row head>
              <C w={2}>時間</C><C w={2}>玩家</C><C w={2}>遭遇</C><C w={1}>結果</C><C w={1}>時長</C><C w={1}>造成傷害</C><C w={1}>承受傷害</C>
            </Row>
            {rows.map((r, i) => (
              <Row key={i}>
                <C w={2} dim>{new Date(r.created_at).toLocaleString('zh-TW', { hour12: false })}</C>
                <C w={2}>{r.display_name}</C>
                <C w={2} dim>{r.encounter_code}</C>
                <C w={1}>{outcomeLabel(r.outcome)}</C>
                <C w={1}>{fmtMs(r.duration_ms)}</C>
                <C w={1} dim>{r.damage_dealt}</C>
                <C w={1} dim>{r.damage_taken}</C>
              </Row>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

// ============================== 武器類型（DORPG P7，契約 §2） ==============================
// 18 筆固定資料（每職業 3 種）。後端只開放 GET/PUT（見 adminRpgApi.weaponTypes 型別註解），沒有
// DELETE 路由——這裡不提供刪除按鈕；PUT 比照其餘內容表的既有慣例視為 upsert，仍保留「新增」以便
// 補建 migration 183 之外臨時想加的一列。沒有 is_active／sort_order 兩欄以外的「啟用」開關可切，
// 故不套用 SimpleContentTab（那顆共用元件的表格固定畫「啟用」欄，這裡的資料形狀對不上）。

const JOB_LABEL: Record<string, string> = {
  light_knight: '輕騎士', archer: '弓箭手', heavy_knight: '重騎士', cleric: '聖職者', merchant: '商人', mage: '魔法師',
}
const WEAPON_VISUAL_OPTIONS = [
  { value: 'sword', label: '劍（sword）' },
  { value: 'bow', label: '弓（bow）' },
  { value: 'staff', label: '法杖（staff）' },
  { value: 'greatsword', label: '大劍（greatsword）' },
]

function emptyWeaponType(): RpgWeaponType {
  return { id: '', job_id: 'light_knight', name: '', visual: 'sword', elemental_capable: false, description: '', traits: {}, sort_order: 0 }
}

function WeaponTypesTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [rows, setRows] = useState<RpgWeaponType[] | null>(null)
  const [form, setForm] = useState<RpgWeaponType | null>(null)
  const [traitsText, setTraitsText] = useState('{}') // traits 是巢狀物件，比照 ConfigTab 的既有慣例存成排版過的字串編輯
  const [isNew, setIsNew] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    adminRpgApi.weaponTypes(token).then((r) => setRows(r.weapon_types)).catch((e: any) => onErr(e?.message || '載入失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { load() }, [load])

  function startNew() { const f = emptyWeaponType(); setForm(f); setTraitsText(JSON.stringify(f.traits, null, 2)); setIsNew(true) }
  function startEdit(r: RpgWeaponType) { setForm({ ...r }); setTraitsText(JSON.stringify(r.traits ?? {}, null, 2)); setIsNew(false) }

  async function save() {
    if (!form) return
    if (!form.id.trim()) { onErr('請填 ID'); return }
    if (!form.name.trim()) { onErr('請填名稱'); return }
    let traits: Record<string, unknown>
    try { traits = JSON.parse(traitsText || '{}') } catch { onErr('「型別特性 traits」不是合法的 JSON，請修正後再儲存'); return }
    setBusy(true)
    try {
      const saved = await adminRpgApi.putWeaponType(token, { ...form, traits })
      onMsg(`已儲存「${saved.name}」`)
      setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>武器類型管理</h2>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: 0, maxWidth: 640, lineHeight: 1.7 }}>
            每職業固定 3 種武器類型（見契約 §2）。traits 大部分鍵純供顯示／設計依據，實際數值全部在「武器」分頁各一級武器的 profile；
            但 DORPG P12 新增的四鍵 row_bonus_front_pct／row_bonus_rear_pct／pierce_chance_pct／pierce_dmg_pct
            例外——後端會把它們合併進武器 profile 真正套用到戰鬥計算（見契約 dorpg_p12 CONTRACT.md §1/§3）。
          </p>
        </div>
        {!form && <button onClick={startNew} style={primaryBtn}>＋ 新增</button>}
      </div>

      {rows === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
      {rows && rows.length === 0 && !form && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚無資料。</div>}
      {rows && rows.length > 0 && (
        <div style={{ overflowX: 'auto' }}>
          <Row head>
            <C w={2}>ID</C>
            <C w={1}>職業</C>
            <C w={1}>名稱</C>
            <C w={1}>視覺</C>
            <C w={1}>屬性版</C>
            <C w={1}>操作</C>
          </Row>
          {[...rows].sort((a, b) => (JOB_LABEL[a.job_id] ?? a.job_id).localeCompare(JOB_LABEL[b.job_id] ?? b.job_id) || a.sort_order - b.sort_order).map((r) => (
            <Row key={r.id}>
              <C w={2} dim>{r.id}</C>
              <C w={1}>{JOB_LABEL[r.job_id] ?? r.job_id}</C>
              <C w={1}>{r.name}</C>
              <C w={1} dim>{r.visual}</C>
              <C w={1}>{r.elemental_capable ? <span style={{ color: 'var(--fug)' }}>✓</span> : <span style={{ color: 'var(--tx-faint)' }}>—</span>}</C>
              <C w={1}><button onClick={() => startEdit(r)} style={linkBtn}>編輯</button></C>
            </Row>
          ))}
        </div>
      )}

      {form && (
        <div style={panel}>
          <h3 style={{ fontSize: 14, fontWeight: 800, margin: '0 0 10px' }}>{isNew ? '新增' : `編輯：${form.name || form.id}`}</h3>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            <F label="ID（如 lk_sword）">
              <input style={inp} type="text" value={form.id} disabled={!isNew} onChange={(e) => setForm({ ...form, id: e.target.value })} />
            </F>
            <F label="職業">
              <select style={inp} value={form.job_id} onChange={(e) => setForm({ ...form, job_id: e.target.value })}>
                {Object.entries(JOB_LABEL).map(([id, label]) => <option key={id} value={id}>{label}</option>)}
              </select>
            </F>
            <F label="名稱">
              <input style={inp} type="text" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </F>
            <F label="視覺（特效/音效組）">
              <select style={inp} value={form.visual} onChange={(e) => setForm({ ...form, visual: e.target.value as RpgWeaponType['visual'] })}>
                {WEAPON_VISUAL_OPTIONS.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </F>
            <F label="可有屬性版（7 元素）">
              <input type="checkbox" checked={form.elemental_capable} onChange={(e) => setForm({ ...form, elemental_capable: e.target.checked })} style={{ width: 18, height: 18 }} />
            </F>
            <F label="排序">
              <input style={inp} type="number" step="1" value={form.sort_order} onChange={(e) => setForm({ ...form, sort_order: Number(e.target.value) })} />
            </F>
            <F label="描述" full>
              <textarea style={{ ...ta, height: 70 }} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </F>
            <F label="型別特性 traits（JSON；多數鍵純顯示，但 P12 的 row_bonus_front_pct／row_bonus_rear_pct／pierce_chance_pct／pierce_dmg_pct 會真的套用到戰鬥計算，見上方說明）" full>
              <textarea style={{ ...ta, height: 120, fontFamily: 'monospace', fontSize: 12 }} value={traitsText} onChange={(e) => setTraitsText(e.target.value)} />
            </F>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={() => setForm(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

// ============================== 武器（DORPG P7，契約 §2/§3） ==============================
// 職業／類型／等級三個篩選器只影響這裡的列表顯示，不影響後端查詢（一次抓全部武器＋全部類型，
// 資料量固定 390 筆內，前端篩選即可，不必為此開新的後端查詢參數）。profile 是契約 §3
// WeaponProfile 的 JSON，比照 ConfigTab／WeaponTypesTab traits 的既有慣例存成排版過的字串編輯。

const WEAPON_RARITY_OPTIONS: WeaponRarity[] = ['common', 'rare', 'epic', 'legendary']
const WEAPON_ELEMENT_OPTIONS: RpgElement[] = ['neutral', ...ELEMENT_ORDER]

function emptyWeapon(defaultTypeId: string): RpgWeapon {
  return {
    id: '', type_id: defaultTypeId, tier: 1, name: '', rarity: 'common', level_req: 1, element: 'neutral',
    profile: {}, description: '', is_active: true, sort_order: 0,
  }
}

function WeaponsTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [types, setTypes] = useState<RpgWeaponType[] | null>(null)
  const [rows, setRows] = useState<RpgWeapon[] | null>(null)
  const [form, setForm] = useState<RpgWeapon | null>(null)
  const [profileText, setProfileText] = useState('{}')
  const [isNew, setIsNew] = useState(false)
  const [busy, setBusy] = useState(false)

  // 篩選器（純前端篩選，見檔頭說明）：'' 代表「全部」。
  const [filterJob, setFilterJob] = useState('')
  const [filterType, setFilterType] = useState('')
  const [filterTier, setFilterTier] = useState('')

  const load = useCallback(() => {
    Promise.all([adminRpgApi.weaponTypes(token), adminRpgApi.weapons(token)])
      .then(([t, w]) => { setTypes(t.weapon_types); setRows(w.weapons) })
      .catch((e: any) => onErr(e?.message || '載入失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { load() }, [load])

  const typeById = new Map((types ?? []).map((t) => [t.id, t]))
  const typesForJobFilter = filterJob ? (types ?? []).filter((t) => t.job_id === filterJob) : (types ?? [])
  const filteredRows = (rows ?? []).filter((w) => {
    const t = typeById.get(w.type_id)
    if (filterJob && t?.job_id !== filterJob) return false
    if (filterType && w.type_id !== filterType) return false
    if (filterTier && w.tier !== Number(filterTier)) return false
    return true
  })

  function startNew() {
    const defaultType = types?.[0]?.id ?? ''
    const f = emptyWeapon(defaultType)
    setForm(f); setProfileText(JSON.stringify(f.profile, null, 2)); setIsNew(true)
  }
  function startEdit(r: RpgWeapon) { setForm({ ...r }); setProfileText(JSON.stringify(r.profile ?? {}, null, 2)); setIsNew(false) }

  async function save() {
    if (!form) return
    if (!form.id.trim()) { onErr('請填 ID'); return }
    if (!form.name.trim()) { onErr('請填名稱'); return }
    let profile: RpgWeapon['profile']
    try { profile = JSON.parse(profileText || '{}') } catch { onErr('「效果 profile」不是合法的 JSON，請修正後再儲存'); return }
    setBusy(true)
    try {
      const saved = await adminRpgApi.putWeapon(token, { ...form, profile })
      onMsg(`已儲存「${saved.name}」`)
      setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function del(r: RpgWeapon) {
    if (!window.confirm(`確定刪除「${r.name}」？`)) return
    setBusy(true)
    try {
      await adminRpgApi.deleteWeapon(token, r.id)
      onMsg(`已刪除「${r.name}」`)
      if (form?.id === r.id) setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>武器管理</h2>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: 0, maxWidth: 640, lineHeight: 1.7 }}>
            每類型 10 級（tier 1–10），可有屬性版的類型另有 7 元素×10 級。profile 是契約 §3 的效果 JSON，缺省欄位＝中性值，戰鬥/角色頁一律照這裡存的值計算。
          </p>
        </div>
        {!form && <button onClick={startNew} disabled={!types || types.length === 0} style={primaryBtn}>＋ 新增</button>}
      </div>

      {!form && (
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
          <F label="篩選：職業">
            <select style={inp} value={filterJob} onChange={(e) => { setFilterJob(e.target.value); setFilterType('') }}>
              <option value="">全部</option>
              {Object.entries(JOB_LABEL).map(([id, label]) => <option key={id} value={id}>{label}</option>)}
            </select>
          </F>
          <F label="篩選：類型">
            <select style={inp} value={filterType} onChange={(e) => setFilterType(e.target.value)}>
              <option value="">全部</option>
              {typesForJobFilter.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
          </F>
          <F label="篩選：等級（tier）">
            <select style={inp} value={filterTier} onChange={(e) => setFilterTier(e.target.value)}>
              <option value="">全部</option>
              {Array.from({ length: 10 }, (_, i) => i + 1).map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </F>
        </div>
      )}

      {rows === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
      {rows && filteredRows.length === 0 && !form && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚無符合篩選條件的資料。</div>}
      {rows && filteredRows.length > 0 && !form && (
        <div style={{ overflowX: 'auto' }}>
          <Row head>
            <C w={2}>ID</C>
            <C w={2}>名稱</C>
            <C w={2}>類型</C>
            <C w={1}>Tier</C>
            <C w={1}>需 Lv</C>
            <C w={1}>屬性</C>
            <C w={1}>啟用</C>
            <C w={2}>操作</C>
          </Row>
          {[...filteredRows].sort((a, b) => a.type_id.localeCompare(b.type_id) || a.tier - b.tier).map((w) => (
            <Row key={w.id}>
              <C w={2} dim>{w.id}</C>
              <C w={2}>{w.name}</C>
              <C w={2} dim>{typeById.get(w.type_id)?.name ?? w.type_id}</C>
              <C w={1}>{w.tier}</C>
              <C w={1} dim>{w.level_req}</C>
              <C w={1}>{ELEMENT_LABEL[w.element] ?? w.element}</C>
              <C w={1}>{w.is_active ? <span style={{ color: 'var(--fug)' }}>✓</span> : <span style={{ color: 'var(--tx-faint)' }}>—</span>}</C>
              <C w={2}>
                <div style={{ display: 'flex', gap: 6 }}>
                  <button onClick={() => startEdit(w)} style={linkBtn}>編輯</button>
                  <button onClick={() => del(w)} disabled={busy} style={{ ...linkBtn, color: 'var(--hunt)' }}>刪除</button>
                </div>
              </C>
            </Row>
          ))}
        </div>
      )}

      {form && (
        <div style={panel}>
          <h3 style={{ fontSize: 14, fontWeight: 800, margin: '0 0 10px' }}>{isNew ? '新增' : `編輯：${form.name || form.id}`}</h3>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            <F label="ID（如 lk_sword_t3 或 lk_sword_t3_fire）">
              <input style={inp} type="text" value={form.id} disabled={!isNew} onChange={(e) => setForm({ ...form, id: e.target.value })} />
            </F>
            <F label="類型">
              <select style={inp} value={form.type_id} onChange={(e) => setForm({ ...form, type_id: e.target.value })}>
                {(types ?? []).map((t) => <option key={t.id} value={t.id}>{JOB_LABEL[t.job_id] ?? t.job_id}・{t.name}</option>)}
              </select>
            </F>
            <F label="Tier（1–10）">
              <input style={inp} type="number" step="1" min={1} max={10} value={form.tier} onChange={(e) => setForm({ ...form, tier: Number(e.target.value) })} />
            </F>
            <F label="名稱">
              <input style={inp} type="text" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </F>
            <F label="稀有度">
              <select style={inp} value={form.rarity} onChange={(e) => setForm({ ...form, rarity: e.target.value as WeaponRarity })}>
                {WEAPON_RARITY_OPTIONS.map((r) => <option key={r} value={r}>{RARITY_LABEL[r]}</option>)}
              </select>
            </F>
            <F label="需求等級 level_req">
              <input style={inp} type="number" step="1" value={form.level_req} onChange={(e) => setForm({ ...form, level_req: Number(e.target.value) })} />
            </F>
            <F label="屬性">
              <select style={inp} value={form.element} onChange={(e) => setForm({ ...form, element: e.target.value as RpgElement })}>
                {WEAPON_ELEMENT_OPTIONS.map((el) => <option key={el} value={el}>{ELEMENT_LABEL[el]}</option>)}
              </select>
            </F>
            <F label="啟用">
              <input type="checkbox" checked={form.is_active} onChange={(e) => setForm({ ...form, is_active: e.target.checked })} style={{ width: 18, height: 18 }} />
            </F>
            <F label="排序">
              <input style={inp} type="number" step="1" value={form.sort_order} onChange={(e) => setForm({ ...form, sort_order: Number(e.target.value) })} />
            </F>
            <F label="描述" full>
              <textarea style={{ ...ta, height: 70 }} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </F>
            <F label="效果 profile（JSON，見契約 §3 WeaponProfile；缺欄位＝中性值）" full>
              <textarea style={{ ...ta, height: 220, fontFamily: 'monospace', fontSize: 12 }} value={profileText} onChange={(e) => setProfileText(e.target.value)} />
            </F>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={() => setForm(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

// --- DORPG P8：防具（含飾品）CRUD（見契約 dorpg_p8 CONTRACT.md §1、WIRE §後台）。job_id 由後端
// 開放 job_id/slot 兩個查詢參數篩選，但比照 WeaponsTab 既有慣例——一次抓全部 390 筆、前端自行
// 篩選。profile 是契約 §2 ArmorProfile 的 JSON，比照 WeaponsTab 存成排版過的字串編輯。

const ARMOR_SLOT_OPTIONS: ArmorItemSlot[] = ['helmet', 'gloves', 'armor', 'legs', 'boots', 'accessory']

function emptyArmorItem(defaultJobId: string): RpgArmorItem {
  return {
    id: '', job_id: defaultJobId, slot: 'helmet', tier: 1, name: '', rarity: 'common', level_req: 1,
    profile: {}, description: '', is_active: true, sort_order: 0,
  }
}

function ArmorItemsTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [rows, setRows] = useState<RpgArmorItem[] | null>(null)
  const [form, setForm] = useState<RpgArmorItem | null>(null)
  const [profileText, setProfileText] = useState('{}')
  const [isNew, setIsNew] = useState(false)
  const [busy, setBusy] = useState(false)

  // 篩選器（純前端篩選，見檔頭說明）：'' 代表「全部」；filterJob 額外支援 'null' 代表「通用（飾品）」。
  const [filterJob, setFilterJob] = useState('')
  const [filterSlot, setFilterSlot] = useState('')

  const load = useCallback(() => {
    adminRpgApi.armorItems(token).then((r) => setRows(r.armor_items)).catch((e: any) => onErr(e?.message || '載入失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { load() }, [load])

  const filteredRows = (rows ?? []).filter((a) => {
    if (filterJob === 'null' && a.job_id !== null) return false
    if (filterJob && filterJob !== 'null' && a.job_id !== filterJob) return false
    if (filterSlot && a.slot !== filterSlot) return false
    return true
  })

  function startNew() {
    const f = emptyArmorItem(Object.keys(JOB_LABEL)[0] ?? 'light_knight')
    setForm(f); setProfileText(JSON.stringify(f.profile, null, 2)); setIsNew(true)
  }
  function startEdit(r: RpgArmorItem) { setForm({ ...r }); setProfileText(JSON.stringify(r.profile ?? {}, null, 2)); setIsNew(false) }

  async function save() {
    if (!form) return
    if (!form.id.trim()) { onErr('請填 ID'); return }
    if (!form.name.trim()) { onErr('請填名稱'); return }
    let profile: RpgArmorItem['profile']
    try { profile = JSON.parse(profileText || '{}') } catch { onErr('「效果 profile」不是合法的 JSON，請修正後再儲存'); return }
    setBusy(true)
    try {
      const saved = await adminRpgApi.putArmorItem(token, { ...form, profile })
      onMsg(`已儲存「${saved.name}」`)
      setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function del(r: RpgArmorItem) {
    if (!window.confirm(`確定刪除「${r.name}」？`)) return
    setBusy(true)
    try {
      await adminRpgApi.deleteArmorItem(token, r.id)
      onMsg(`已刪除「${r.name}」`)
      if (form?.id === r.id) setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>防具管理</h2>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: 0, maxWidth: 640, lineHeight: 1.7 }}>
            五部位（頭盔/手套/衣服/褲裙/鞋子）依職業各 10 級；飾品（accessory）通用（職業留空），18 種效果各 5 級。
            profile 是契約 §2 的效果 JSON，缺省欄位＝中性值，戰鬥/角色頁一律照這裡存的值計算。
          </p>
        </div>
        {!form && <button onClick={startNew} style={primaryBtn}>＋ 新增</button>}
      </div>

      {!form && (
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
          <F label="篩選：職業">
            <select style={inp} value={filterJob} onChange={(e) => setFilterJob(e.target.value)}>
              <option value="">全部</option>
              {Object.entries(JOB_LABEL).map(([id, label]) => <option key={id} value={id}>{label}</option>)}
              <option value="null">通用（飾品）</option>
            </select>
          </F>
          <F label="篩選：部位">
            <select style={inp} value={filterSlot} onChange={(e) => setFilterSlot(e.target.value)}>
              <option value="">全部</option>
              {ARMOR_SLOT_OPTIONS.map((s) => <option key={s} value={s}>{ARMOR_SLOT_LABEL[s]}</option>)}
            </select>
          </F>
        </div>
      )}

      {rows === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
      {rows && filteredRows.length === 0 && !form && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚無符合篩選條件的資料。</div>}
      {rows && filteredRows.length > 0 && !form && (
        <div style={{ overflowX: 'auto' }}>
          <Row head>
            <C w={2}>ID</C>
            <C w={2}>名稱</C>
            <C w={1}>職業</C>
            <C w={1}>部位</C>
            <C w={1}>Tier</C>
            <C w={1}>需 Lv</C>
            <C w={1}>啟用</C>
            <C w={2}>操作</C>
          </Row>
          {[...filteredRows]
            .sort((a, b) => (a.job_id ?? '').localeCompare(b.job_id ?? '') || a.slot.localeCompare(b.slot) || a.tier - b.tier)
            .map((a) => (
              <Row key={a.id}>
                <C w={2} dim>{a.id}</C>
                <C w={2}>{a.name}</C>
                <C w={1} dim>{a.job_id ? (JOB_LABEL[a.job_id] ?? a.job_id) : '通用'}</C>
                <C w={1}>{ARMOR_SLOT_LABEL[a.slot]}</C>
                <C w={1}>{a.tier}</C>
                <C w={1} dim>{a.level_req}</C>
                <C w={1}>{a.is_active ? <span style={{ color: 'var(--fug)' }}>✓</span> : <span style={{ color: 'var(--tx-faint)' }}>—</span>}</C>
                <C w={2}>
                  <div style={{ display: 'flex', gap: 6 }}>
                    <button onClick={() => startEdit(a)} style={linkBtn}>編輯</button>
                    <button onClick={() => del(a)} disabled={busy} style={{ ...linkBtn, color: 'var(--hunt)' }}>刪除</button>
                  </div>
                </C>
              </Row>
            ))}
        </div>
      )}

      {form && (
        <div style={panel}>
          <h3 style={{ fontSize: 14, fontWeight: 800, margin: '0 0 10px' }}>{isNew ? '新增' : `編輯：${form.name || form.id}`}</h3>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            <F label="ID（如 hk_armor_t3 或 acc_hp_pct_t3）">
              <input style={inp} type="text" value={form.id} disabled={!isNew} onChange={(e) => setForm({ ...form, id: e.target.value })} />
            </F>
            <F label="職業（飾品留空＝通用）">
              <select style={inp} value={form.job_id ?? ''} onChange={(e) => setForm({ ...form, job_id: e.target.value || null })}>
                <option value="">（通用，僅飾品）</option>
                {Object.entries(JOB_LABEL).map(([id, label]) => <option key={id} value={id}>{label}</option>)}
              </select>
            </F>
            <F label="部位">
              <select style={inp} value={form.slot} onChange={(e) => setForm({ ...form, slot: e.target.value as RpgArmorItem['slot'] })}>
                {ARMOR_SLOT_OPTIONS.map((s) => <option key={s} value={s}>{ARMOR_SLOT_LABEL[s]}</option>)}
              </select>
            </F>
            <F label="Tier（防具 1–10／飾品 1–5）">
              <input style={inp} type="number" step="1" min={1} max={10} value={form.tier} onChange={(e) => setForm({ ...form, tier: Number(e.target.value) })} />
            </F>
            <F label="名稱">
              <input style={inp} type="text" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </F>
            <F label="稀有度">
              <select style={inp} value={form.rarity} onChange={(e) => setForm({ ...form, rarity: e.target.value as WeaponRarity })}>
                {WEAPON_RARITY_OPTIONS.map((r) => <option key={r} value={r}>{RARITY_LABEL[r]}</option>)}
              </select>
            </F>
            <F label="需求等級 level_req">
              <input style={inp} type="number" step="1" value={form.level_req} onChange={(e) => setForm({ ...form, level_req: Number(e.target.value) })} />
            </F>
            <F label="啟用">
              <input type="checkbox" checked={form.is_active} onChange={(e) => setForm({ ...form, is_active: e.target.checked })} style={{ width: 18, height: 18 }} />
            </F>
            <F label="排序">
              <input style={inp} type="number" step="1" value={form.sort_order} onChange={(e) => setForm({ ...form, sort_order: Number(e.target.value) })} />
            </F>
            <F label="描述" full>
              <textarea style={{ ...ta, height: 70 }} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </F>
            <F label="效果 profile（JSON，見契約 §2 ArmorProfile；缺欄位＝中性值）" full>
              <textarea style={{ ...ta, height: 220, fontFamily: 'monospace', fontSize: 12 }} value={profileText} onChange={(e) => setProfileText(e.target.value)} />
            </F>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={() => setForm(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

// ============================== AI 策略（DORPG P9，契約 §2） ==============================
// id 集合固定六種（引擎 registry 白名單，後端持有同一份常數）——後台只能編輯既有列的顯示/參數/
// 啟用/排序；「新增」理論上只在 ENGINE 未來擴充 registry＋補 seed 之後才有新 id 可用，這裡仍給
// 一顆「＋新增」鈕（比照 WeaponTypesTab／ArmorItemsTab 的既有慣例，id 可編輯輸入），送出後端會用
// 同一份白名單常數擋掉不存在的 id（400 unknown_strategy）。

function emptyAiStrategy(): RpgAiStrategy {
  return { id: '', name: '', description: '', params: {}, is_active: true, sort_order: 0 }
}

function AiStrategiesTab({ token, onErr, onMsg }: { token: string; onErr: (m: string) => void; onMsg: (m: string) => void }) {
  const [rows, setRows] = useState<RpgAiStrategy[] | null>(null)
  const [form, setForm] = useState<RpgAiStrategy | null>(null)
  const [paramsText, setParamsText] = useState('{}') // params 是覆寫引擎預設門檻的 JSON，比照 WeaponTypesTab traits 的既有慣例存成排版過的字串編輯
  const [isNew, setIsNew] = useState(false)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    adminRpgApi.aiStrategies(token).then((r) => setRows(r.ai_strategies)).catch((e: any) => onErr(e?.message || '載入失敗'))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])
  useEffect(() => { load() }, [load])

  function startNew() { const f = emptyAiStrategy(); setForm(f); setParamsText('{}'); setIsNew(true) }
  function startEdit(r: RpgAiStrategy) { setForm({ ...r }); setParamsText(JSON.stringify(r.params ?? {}, null, 2)); setIsNew(false) }

  async function save() {
    if (!form) return
    if (!form.id.trim()) { onErr('請填 ID（須為引擎已知的策略 id）'); return }
    if (!form.name.trim()) { onErr('請填名稱'); return }
    let params: Record<string, unknown>
    try { params = JSON.parse(paramsText || '{}') } catch { onErr('「參數 params」不是合法的 JSON，請修正後再儲存'); return }
    setBusy(true)
    try {
      const saved = await adminRpgApi.putAiStrategy(token, { ...form, params })
      onMsg(`已儲存「${saved.name}」`)
      setForm(null)
      load()
    } catch (e: any) { onErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function del(r: RpgAiStrategy) {
    if (!window.confirm(`確定刪除「${r.name}」？若仍有腳本／玩家引用會被拒絕。`)) return
    setBusy(true)
    try {
      await adminRpgApi.deleteAiStrategy(token, r.id)
      onMsg(`已刪除「${r.name}」`)
      if (form?.id === r.id) setForm(null)
      load()
    } catch (e: any) { onErr(e?.status === 409 ? '仍有腳本或玩家在使用這個策略，無法刪除' : (e?.message || '刪除失敗')) } finally { setBusy(false) }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
        <div>
          <h2 style={{ fontSize: 15, fontWeight: 800, margin: '0 0 4px' }}>AI 戰鬥策略管理</h2>
          <p style={{ fontSize: 12, color: 'var(--tx-dim)', margin: 0, maxWidth: 640, lineHeight: 1.7 }}>
            傭兵腳本與玩家自動戰鬥共用這份策略表（見契約 §4）。這裡只能調整顯示名稱/說明/參數覆寫/
            啟用/排序，不能發明引擎沒有的行為——id 必須是引擎 registry 已知的六種之一，否則儲存時會被拒絕。
          </p>
        </div>
        {!form && <button onClick={startNew} style={primaryBtn}>＋ 新增</button>}
      </div>

      {rows === null && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>載入中…</div>}
      {rows && rows.length === 0 && !form && <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>尚無資料。</div>}
      {rows && rows.length > 0 && !form && (
        <div style={{ overflowX: 'auto' }}>
          <Row head>
            <C w={2}>ID</C>
            <C w={2}>名稱</C>
            <C w={1}>啟用</C>
            <C w={1}>排序</C>
            <C w={2}>操作</C>
          </Row>
          {[...rows].sort((a, b) => a.sort_order - b.sort_order).map((r) => (
            <Row key={r.id}>
              <C w={2} dim>{r.id}</C>
              <C w={2}>{r.name}</C>
              <C w={1}>{r.is_active ? <span style={{ color: 'var(--fug)' }}>✓</span> : <span style={{ color: 'var(--tx-faint)' }}>—</span>}</C>
              <C w={1} dim>{r.sort_order}</C>
              <C w={2}>
                <div style={{ display: 'flex', gap: 6 }}>
                  <button onClick={() => startEdit(r)} style={linkBtn}>編輯</button>
                  <button onClick={() => del(r)} disabled={busy} style={{ ...linkBtn, color: 'var(--hunt)' }}>刪除</button>
                </div>
              </C>
            </Row>
          ))}
        </div>
      )}

      {form && (
        <div style={panel}>
          <h3 style={{ fontSize: 14, fontWeight: 800, margin: '0 0 10px' }}>{isNew ? '新增' : `編輯：${form.name || form.id}`}</h3>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 12 }}>
            <F label="ID（引擎 registry 已知的策略 id，如 mp_conserve）">
              <input style={inp} type="text" value={form.id} disabled={!isNew} onChange={(e) => setForm({ ...form, id: e.target.value })} />
            </F>
            <F label="名稱">
              <input style={inp} type="text" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
            </F>
            <F label="啟用">
              <input type="checkbox" checked={form.is_active} onChange={(e) => setForm({ ...form, is_active: e.target.checked })} style={{ width: 18, height: 18 }} />
            </F>
            <F label="排序">
              <input style={inp} type="number" step="1" value={form.sort_order} onChange={(e) => setForm({ ...form, sort_order: Number(e.target.value) })} />
            </F>
            <F label="說明（玩家在酒館腳本編輯器／戰鬥 HUD 看到的一句話）" full>
              <textarea style={{ ...ta, height: 70 }} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            </F>
            <F label="參數 params（JSON，覆寫引擎預設門檻，見契約 §4 各策略欄位，例如 heal_pct/mp_reserve_pct）" full>
              <textarea style={{ ...ta, height: 160, fontFamily: 'monospace', fontSize: 12 }} value={paramsText} onChange={(e) => setParamsText(e.target.value)} />
            </F>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 14 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={() => setForm(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}

// ============================== 共用小元件／樣式（比照 admin/monopoly） ==============================

// full：讓這個欄位在 CONFIG_GROUPS 的 grid 排版裡橫跨整列（P2 新增，battle_element_chart 的 JSON
// textarea 塞進 minmax(200px,1fr) 的單一格會太窄，其餘呼叫端不傳就維持原本單格寬度）。
function F({ label, children, full }: { label: string; children: React.ReactNode; full?: boolean }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, ...(full ? { gridColumn: '1 / -1' } : {}) }}>
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
