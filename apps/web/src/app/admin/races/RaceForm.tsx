'use client'

import { useEffect, useState } from 'react'
import {
  adminRacesApi,
  adminPresetsApi,
  type CreateRacePayload,
  type EventMode,
  type GoalType,
  type RaceDetail,
  type RaceGroup,
  type RaceAddon,
  type GroupPreset,
} from '@/lib/api'

// 物資編輯用的中介型別：scope 用「-1=共用」或分組索引表示
interface SupplyDraft {
  scope: number // -1 = 共用；>=0 = groups 陣列索引
  kind: 'race_pack' | 'finisher'
  name: string
  description: string
  image_url: string
}

const MODES: { v: EventMode; t: string; desc: string }[] = [
  { v: 'general', t: '一般模式', desc: '個人參賽，報名時自選分組' },
  { v: 'competition', t: '競賽模式', desc: '一般模式 + 分組成績統計與排名' },
  { v: 'faction_battle', t: '分組對抗模式', desc: '隨機分組，賽前不公開所屬分組' },
]

const STATUSES: { v: string; t: string }[] = [
  { v: 'soon', t: '即將開始' },
  { v: 'open', t: '報名中' },
  { v: 'live', t: '進行中' },
  { v: 'done', t: '已結束' },
]

const REQUIRED_FIELD_OPTS: { v: string; t: string }[] = [
  { v: 'real_name', t: '真實姓名' },
  { v: 'nickname', t: '暱稱' },
  { v: 'phone', t: '手機' },
  { v: 'address', t: '地址' },
  { v: 'birthday', t: '生日' },
  { v: 'gender', t: '性別' },
]

function slugify(s: string): string {
  const base = s
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9一-龥]+/g, '-')
    .replace(/(^-|-$)/g, '')
  if (!base || /[一-龥]/.test(base)) {
    return 'race-' + Math.random().toString(36).slice(2, 8)
  }
  return base
}

function emptyGroup(order: number): RaceGroup {
  return {
    name: '', description: '', display_order: order, slot_limit: null,
    gender_limit: 'any', age_min: null, age_max: null, target_distance_km: null,
  }
}
function emptyAddon(order: number): RaceAddon {
  return {
    name: '', description: '', image_url: '', price_cents: 0,
    per_user_limit: null, total_stock: null, display_order: order, active: true,
  }
}

// ISO → datetime-local 值（本地時間，去秒）
function toLocalInput(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const off = d.getTimezoneOffset() * 60000
  return new Date(d.getTime() - off).toISOString().slice(0, 16)
}
function toDateInput(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const off = d.getTimezoneOffset() * 60000
  return new Date(d.getTime() - off).toISOString().slice(0, 10)
}

/**
 * 賽事表單（新增與編輯共用）。
 * 傳入 initial（RaceDetail）= 編輯模式；不傳 = 新增模式。
 */
export default function RaceForm({
  token,
  initial,
  onDone,
  onCancel,
  submitLabel,
}: {
  token: string
  initial?: RaceDetail
  onDone: (detail: RaceDetail) => void
  onCancel: () => void
  submitLabel?: string
}) {
  const isEdit = !!initial?.id

  const [tab, setTab] = useState<'basic' | 'groups' | 'addons' | 'supplies'>('basic')
  const [mode, setMode] = useState<EventMode>(initial?.event_mode ?? 'general')
  const [goalType, setGoalType] = useState<GoalType>(initial?.goal_type ?? 'distance')
  const [status, setStatus] = useState<string>(initial?.status ?? 'soon')

  const [title, setTitle] = useState(initial?.title ?? '')
  const [slug, setSlug] = useState(initial?.slug ?? '')
  const [slugTouched, setSlugTouched] = useState(isEdit)
  const [subtitle, setSubtitle] = useState(initial?.subtitle ?? '')
  const [blurb, setBlurb] = useState(initial?.blurb ?? '')
  const [regStart, setRegStart] = useState(toLocalInput(initial?.registration_start))
  const [regEnd, setRegEnd] = useState(toLocalInput(initial?.registration_end))
  const [startDate, setStartDate] = useState(toDateInput(initial?.start_date))
  const [endDate, setEndDate] = useState(toDateInput(initial?.end_date))
  const [entryFeeNtd, setEntryFeeNtd] = useState(String((initial?.entry_fee ?? 0) / 100))
  const [requiredFields, setRequiredFields] = useState<string[]>(
    initial?.required_fields ?? ['real_name', 'phone']
  )

  const [groups, setGroups] = useState<RaceGroup[]>(
    initial?.groups?.length ? initial.groups.map((g) => ({ ...g })) : [emptyGroup(0)]
  )
  const [addons, setAddons] = useState<RaceAddon[]>(initial?.addons?.map((a) => ({ ...a })) ?? [])
  const [supplies, setSupplies] = useState<SupplyDraft[]>(
    (initial?.supplies ?? []).map((s) => ({
      scope: s.group_id ? (initial!.groups.findIndex((g) => g.id === s.group_id)) : -1,
      kind: s.kind,
      name: s.name,
      description: s.description ?? '',
      image_url: s.image_url ?? '',
    }))
  )

  const [presets, setPresets] = useState<GroupPreset[]>([])
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    adminPresetsApi.list(token).then((r) => setPresets(r.presets)).catch(() => {})
  }, [token])

  useEffect(() => {
    if (!slugTouched) setSlug(slugify(title))
  }, [title, slugTouched])

  const isRandom = mode === 'faction_battle'

  function updateGroup(i: number, patch: Partial<RaceGroup>) {
    setGroups((gs) => gs.map((g, idx) => (idx === i ? { ...g, ...patch } : g)))
  }
  function removeGroup(i: number) {
    setGroups((gs) => gs.filter((_, idx) => idx !== i))
    setSupplies((ss) =>
      ss.map((s) => {
        if (s.scope === i) return { ...s, scope: -1 }
        if (s.scope > i) return { ...s, scope: s.scope - 1 }
        return s
      })
    )
  }
  function applyPreset(i: number, presetId: string) {
    const p = presets.find((x) => x.id === presetId)
    if (!p) return
    updateGroup(i, { name: p.name, target_distance_km: p.default_distance_km ?? null })
  }
  async function saveCurrentAsPreset(i: number) {
    const g = groups[i]
    if (!g.name) return
    try {
      const { preset } = await adminPresetsApi.create(token, {
        name: g.name,
        default_distance_km: g.target_distance_km ?? null,
      })
      setPresets((ps) => (ps.some((x) => x.id === preset.id) ? ps : [...ps, preset]))
    } catch {
      /* 忽略重複 */
    }
  }

  function buildPayload(): CreateRacePayload {
    const cleanGroups: RaceGroup[] = groups
      .filter((g) => g.name.trim())
      .map((g, idx) => ({ ...g, display_order: idx }))

    return {
      title: title.trim(),
      slug: slug.trim(),
      subtitle: subtitle.trim(),
      blurb: blurb.trim(),
      event_mode: mode,
      goal_type: mode === 'competition' ? goalType : 'distance',
      group_mode: isRandom ? 'random' : 'self',
      status: status as CreateRacePayload['status'],
      entry_fee: Math.round(parseFloat(entryFeeNtd || '0') * 100),
      required_fields: requiredFields,
      registration_start: regStart ? new Date(regStart).toISOString() : null,
      registration_end: regEnd ? new Date(regEnd).toISOString() : null,
      start_date: startDate ? new Date(startDate).toISOString() : new Date().toISOString(),
      end_date: endDate ? new Date(endDate).toISOString() : new Date().toISOString(),
      groups: cleanGroups,
      addons: addons.filter((a) => a.name.trim()).map((a, idx) => ({ ...a, display_order: idx })),
      supplies: supplies
        .filter((s) => s.name.trim())
        .map((s, idx) => ({
          kind: s.kind,
          name: s.name.trim(),
          description: s.description.trim(),
          image_url: s.image_url.trim(),
          display_order: idx,
          group_index: s.scope < 0 ? null : s.scope,
        })),
    }
  }

  async function submit() {
    setErr('')
    if (!title.trim()) {
      setErr('請填寫賽事名稱')
      setTab('basic')
      return
    }
    setSaving(true)
    try {
      const payload = buildPayload()
      const res = isEdit
        ? await adminRacesApi.updateFull(token, initial!.id, payload)
        : await adminRacesApi.create(token, payload)
      onDone(res.race)
    } catch (e: any) {
      setErr(e?.message || (isEdit ? '儲存失敗' : '建立失敗'))
    } finally {
      setSaving(false)
    }
  }

  const groupOptions = groups.map((g, i) => ({ i, label: g.name || `分組 ${i + 1}` }))

  return (
    <div>
      {/* 模式選擇 */}
      <div style={{ display: 'flex', gap: 10, marginBottom: 16, flexWrap: 'wrap' }}>
        {MODES.map((m) => (
          <button
            key={m.v}
            onClick={() => setMode(m.v)}
            style={{
              flex: '1 1 180px', textAlign: 'left', padding: '10px 12px', borderRadius: 10, cursor: 'pointer',
              border: mode === m.v ? '1px solid var(--fug)' : '1px solid var(--line-2)',
              background: mode === m.v ? 'rgba(45,212,150,.08)' : 'var(--bg-2)', color: 'var(--tx)',
            }}
          >
            <div style={{ fontWeight: 700, fontSize: 14 }}>{m.t}</div>
            <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 3 }}>{m.desc}</div>
          </button>
        ))}
      </div>

      {/* Tabs */}
      <div style={{ display: 'flex', gap: 6, marginBottom: 14, borderBottom: '1px solid var(--line)' }}>
        {[
          ['basic', '基本'],
          ['groups', `分組 (${groups.filter((g) => g.name.trim()).length})`],
          ['addons', `加購 (${addons.filter((a) => a.name.trim()).length})`],
          ['supplies', `物資 (${supplies.filter((s) => s.name.trim()).length})`],
        ].map(([v, label]) => (
          <button
            key={v}
            onClick={() => setTab(v as any)}
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

      <div style={{ minHeight: 280 }}>
        {tab === 'basic' && (
          <div style={col}>
            <Field label="賽事名稱 *">
              <input style={inp} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="例：2026 英雄馬拉松" />
            </Field>
            <Row>
              <Field label="Slug（網址代稱，需唯一）">
                <input style={inp} value={slug} onChange={(e) => { setSlug(e.target.value); setSlugTouched(true) }} />
              </Field>
              <Field label="副標題">
                <input style={inp} value={subtitle} onChange={(e) => setSubtitle(e.target.value)} />
              </Field>
            </Row>
            <Field label="賽事說明">
              <textarea style={{ ...inp, minHeight: 70, resize: 'vertical' }} value={blurb} onChange={(e) => setBlurb(e.target.value)} />
            </Field>
            <Row>
              <Field label="報名開始">
                <input style={inp} type="datetime-local" value={regStart} onChange={(e) => setRegStart(e.target.value)} />
              </Field>
              <Field label="報名截止">
                <input style={inp} type="datetime-local" value={regEnd} onChange={(e) => setRegEnd(e.target.value)} />
              </Field>
            </Row>
            <Row>
              <Field label="競賽開始">
                <input style={inp} type="date" value={startDate} onChange={(e) => setStartDate(e.target.value)} />
              </Field>
              <Field label="競賽結束">
                <input style={inp} type="date" value={endDate} onChange={(e) => setEndDate(e.target.value)} />
              </Field>
            </Row>
            <Row>
              <Field label="報名費 (NT$)">
                <input style={inp} type="number" value={entryFeeNtd} onChange={(e) => setEntryFeeNtd(e.target.value)} />
              </Field>
              <Field label="狀態">
                <select style={inp} value={status} onChange={(e) => setStatus(e.target.value)}>
                  {STATUSES.map((s) => (
                    <option key={s.v} value={s.v}>{s.t}</option>
                  ))}
                </select>
              </Field>
              {mode === 'competition' ? (
                <Field label="完賽目標型態">
                  <select style={inp} value={goalType} onChange={(e) => setGoalType(e.target.value as GoalType)}>
                    <option value="distance">指定完成里程</option>
                    <option value="cumulative">各分組總累積里程</option>
                  </select>
                </Field>
              ) : (
                <div style={{ flex: 1 }} />
              )}
            </Row>

            <div>
              <span style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
                報名必填欄位
              </span>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 8 }}>
                {REQUIRED_FIELD_OPTS.map((f) => {
                  const on = requiredFields.includes(f.v)
                  return (
                    <button
                      key={f.v}
                      type="button"
                      onClick={() =>
                        setRequiredFields((rf) => (on ? rf.filter((x) => x !== f.v) : [...rf, f.v]))
                      }
                      style={{
                        padding: '7px 13px', borderRadius: 999, cursor: 'pointer', fontSize: 13,
                        border: on ? '1px solid var(--fug)' : '1px solid var(--line-2)',
                        background: on ? 'rgba(45,212,150,.1)' : 'var(--bg-2)',
                        color: on ? 'var(--fug)' : 'var(--tx-dim)',
                      }}
                    >
                      {on ? '✓ ' : ''}{f.t}
                    </button>
                  )
                })}
              </div>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6 }}>
                未勾選者為選填。報名時若分組有性別/年齡限制，會自動要求對應欄位。
              </div>
            </div>
          </div>
        )}

        {tab === 'groups' && (
          <div style={col}>
            {isRandom && <div style={hint}>分組對抗模式：報名時隨機分配、賽前不公開。以下分組即為對抗陣營。</div>}
            {groups.map((g, i) => (
              <div key={g.id ?? `new-${i}`} style={card}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                  <strong style={{ fontSize: 13 }}>
                    分組 {i + 1}
                    {g.id && typeof g.slots_taken === 'number' ? (
                      <span style={{ marginLeft: 8, fontWeight: 400, color: 'var(--tx-faint)' }}>已報名 {g.slots_taken}</span>
                    ) : null}
                  </strong>
                  {groups.length > 1 && <button onClick={() => removeGroup(i)} style={linkBtn}>移除</button>}
                </div>
                <Row>
                  <Field label="分組名稱">
                    <input style={inp} value={g.name} onChange={(e) => updateGroup(i, { name: e.target.value })} placeholder="例：全馬組" />
                  </Field>
                  <Field label="套用預設選單">
                    <select style={inp} value="" onChange={(e) => applyPreset(i, e.target.value)}>
                      <option value="">— 選擇 —</option>
                      {presets.map((p) => (
                        <option key={p.id} value={p.id}>{p.name}</option>
                      ))}
                    </select>
                  </Field>
                </Row>
                <Row>
                  <Field label="完賽目標里程 (km)">
                    <input style={inp} type="number" value={g.target_distance_km ?? ''} onChange={(e) => updateGroup(i, { target_distance_km: e.target.value === '' ? null : parseFloat(e.target.value) })} />
                  </Field>
                  <Field label="人數限制 (空=不限)">
                    <input style={inp} type="number" value={g.slot_limit ?? ''} onChange={(e) => updateGroup(i, { slot_limit: e.target.value === '' ? null : parseInt(e.target.value, 10) })} />
                  </Field>
                </Row>
                <Row>
                  <Field label="性別限制">
                    <select style={inp} value={g.gender_limit} onChange={(e) => updateGroup(i, { gender_limit: e.target.value as RaceGroup['gender_limit'] })}>
                      <option value="any">不限</option>
                      <option value="male">限男性</option>
                      <option value="female">限女性</option>
                    </select>
                  </Field>
                  <Field label="年齡下限 (空=不限)">
                    <input style={inp} type="number" value={g.age_min ?? ''} onChange={(e) => updateGroup(i, { age_min: e.target.value === '' ? null : parseInt(e.target.value, 10) })} />
                  </Field>
                  <Field label="年齡上限 (空=不限)">
                    <input style={inp} type="number" value={g.age_max ?? ''} onChange={(e) => updateGroup(i, { age_max: e.target.value === '' ? null : parseInt(e.target.value, 10) })} />
                  </Field>
                </Row>
                <button onClick={() => saveCurrentAsPreset(i)} style={linkBtn}>＋ 加入預設選單</button>
              </div>
            ))}
            <button onClick={() => setGroups((gs) => [...gs, emptyGroup(gs.length)])} style={ghostBtn}>＋ 新增分組</button>
          </div>
        )}

        {tab === 'addons' && (
          <div style={col}>
            {addons.map((a, i) => (
              <div key={a.id ?? `new-${i}`} style={card}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                  <strong style={{ fontSize: 13 }}>加購 {i + 1}</strong>
                  <button onClick={() => setAddons((as) => as.filter((_, idx) => idx !== i))} style={linkBtn}>移除</button>
                </div>
                <Row>
                  <Field label="名稱">
                    <input style={inp} value={a.name} onChange={(e) => setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, name: e.target.value } : x)))} />
                  </Field>
                  <Field label="價格 (NT$)">
                    <input style={inp} type="number" value={a.price_cents / 100} onChange={(e) => setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, price_cents: Math.round(parseFloat(e.target.value || '0') * 100) } : x)))} />
                  </Field>
                </Row>
                <Field label="照片網址">
                  <input style={inp} value={a.image_url} onChange={(e) => setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, image_url: e.target.value } : x)))} />
                </Field>
                <Field label="說明">
                  <input style={inp} value={a.description} onChange={(e) => setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, description: e.target.value } : x)))} />
                </Field>
                <Row>
                  <Field label="個人限購 (空=不限)">
                    <input style={inp} type="number" value={a.per_user_limit ?? ''} onChange={(e) => setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, per_user_limit: e.target.value === '' ? null : parseInt(e.target.value, 10) } : x)))} />
                  </Field>
                  <Field label="總銷售量 (空=不限)">
                    <input style={inp} type="number" value={a.total_stock ?? ''} onChange={(e) => setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, total_stock: e.target.value === '' ? null : parseInt(e.target.value, 10) } : x)))} />
                  </Field>
                </Row>
              </div>
            ))}
            <button onClick={() => setAddons((as) => [...as, emptyAddon(as.length)])} style={ghostBtn}>＋ 新增加購項目</button>
          </div>
        )}

        {tab === 'supplies' && (
          <div style={col}>
            <div style={hint}>物資可設為「共用」（全賽事）或指定某分組；類型分參賽物資與完賽物資。</div>
            {supplies.map((s, i) => (
              <div key={i} style={card}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                  <strong style={{ fontSize: 13 }}>物資 {i + 1}</strong>
                  <button onClick={() => setSupplies((ss) => ss.filter((_, idx) => idx !== i))} style={linkBtn}>移除</button>
                </div>
                <Row>
                  <Field label="範圍">
                    <select style={inp} value={s.scope} onChange={(e) => setSupplies((ss) => ss.map((x, idx) => (idx === i ? { ...x, scope: parseInt(e.target.value, 10) } : x)))}>
                      <option value={-1}>共用（全賽事）</option>
                      {groupOptions.map((o) => (
                        <option key={o.i} value={o.i}>{o.label}</option>
                      ))}
                    </select>
                  </Field>
                  <Field label="類型">
                    <select style={inp} value={s.kind} onChange={(e) => setSupplies((ss) => ss.map((x, idx) => (idx === i ? { ...x, kind: e.target.value as SupplyDraft['kind'] } : x)))}>
                      <option value="race_pack">參賽物資</option>
                      <option value="finisher">完賽物資</option>
                    </select>
                  </Field>
                </Row>
                <Row>
                  <Field label="名稱">
                    <input style={inp} value={s.name} onChange={(e) => setSupplies((ss) => ss.map((x, idx) => (idx === i ? { ...x, name: e.target.value } : x)))} />
                  </Field>
                  <Field label="照片網址">
                    <input style={inp} value={s.image_url} onChange={(e) => setSupplies((ss) => ss.map((x, idx) => (idx === i ? { ...x, image_url: e.target.value } : x)))} />
                  </Field>
                </Row>
                <Field label="說明">
                  <input style={inp} value={s.description} onChange={(e) => setSupplies((ss) => ss.map((x, idx) => (idx === i ? { ...x, description: e.target.value } : x)))} />
                </Field>
              </div>
            ))}
            <button
              onClick={() => setSupplies((ss) => [...ss, { scope: -1, kind: 'race_pack', name: '', description: '', image_url: '' }])}
              style={ghostBtn}
            >＋ 新增物資</button>
          </div>
        )}
      </div>

      {err && <div style={{ color: 'var(--hunt)', fontSize: 13, marginTop: 10 }}>{err}</div>}

      <div style={{ display: 'flex', gap: 10, marginTop: 16, justifyContent: 'flex-end' }}>
        <button onClick={onCancel} style={ghostBtn}>取消</button>
        <button onClick={submit} disabled={saving} style={primaryBtn}>
          {saving ? '儲存中…' : submitLabel ?? (isEdit ? '儲存變更' : '建立賽事')}
        </button>
      </div>
    </div>
  )
}

// --- 小元件 ---

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, flex: 1 }}>
      <span style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>{label}</span>
      {children}
    </label>
  )
}
function Row({ children }: { children: React.ReactNode }) {
  return <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap' }}>{children}</div>
}

// --- 樣式 ---

const col: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 14 }
const card: React.CSSProperties = {
  border: '1px solid var(--line-2)', borderRadius: 12, padding: 14,
  display: 'flex', flexDirection: 'column', gap: 12, background: 'var(--bg-2)',
}
const hint: React.CSSProperties = { fontSize: 12, color: 'var(--tx-dim)' }
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '10px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none',
  borderRadius: 10, padding: '11px 20px', cursor: 'pointer', fontSize: 14,
}
const ghostBtn: React.CSSProperties = {
  background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)',
  borderRadius: 10, padding: '11px 16px', cursor: 'pointer', fontSize: 14,
}
const linkBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--fug)', cursor: 'pointer', fontSize: 12, padding: 0,
}
