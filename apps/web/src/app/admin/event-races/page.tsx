'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminEventRacesApi, adminRacesApi, type RaceEventDef, type EventTypeSpec, type RelOption, type Race } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import DpCoin from '@/components/DpCoin'
import EventImageSlots from '@/components/EventImageSlots'
import ShapeCompletionEditor from '@/components/ShapeCompletionEditor'

function emptyDef(cCat: EventTypeSpec[]): RaceEventDef {
  return {
    name: '', description: '', enabled: true, race_id: '', weight: 100,
    trigger_min_m: 1000, initiator_cooldown_sec: 900, target_count: 0,
    group_rel: 'any', follow_rel: 'any', gender_rel: 'any', join_window_s: 60,
    completion_type: cCat[0]?.key ?? '', completion_params: {},
    message: '', reward_exp: 0, reward_dp: 0, per_user_daily_cap: 0,
  }
}

export default function AdminEventRacesPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [defs, setDefs] = useState<RaceEventDef[] | null>(null)
  const [cCat, setCCat] = useState<EventTypeSpec[]>([])
  const [groupRel, setGroupRel] = useState<RelOption[]>([])
  const [followRel, setFollowRel] = useState<RelOption[]>([])
  const [genderRel, setGenderRel] = useState<RelOption[]>([])
  const [races, setRaces] = useState<Race[]>([])
  const [edit, setEdit] = useState<RaceEventDef | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminEventRacesApi.list(t).then((r) => {
      setDefs(r.defs); setCCat(r.completion_catalog)
      setGroupRel(r.group_rel_options); setFollowRel(r.follow_rel_options); setGenderRel(r.gender_rel_options)
    }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「事件任務」權限')
      else setErr(e?.message || '載入失敗')
    })
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch(() => {})
  }, [router])
  useEffect(() => { load() }, [load])

  function startNew() { setEdit(emptyDef(cCat)); setIsNew(true); setErr(''); setMsg('') }
  function startEdit(d: RaceEventDef) { setEdit(JSON.parse(JSON.stringify(d))); setIsNew(false); setErr(''); setMsg('') }
  function duplicate(d: RaceEventDef) { const c = JSON.parse(JSON.stringify(d)); delete c.id; c.name = d.name + '（複製）'; setEdit(c); setIsNew(true); setErr(''); setMsg('') }

  async function save() {
    if (!token || !edit) return
    if (!edit.name.trim()) { setErr('請填名稱'); return }
    setBusy(true); setErr(''); setMsg('')
    try {
      if (isNew) await adminEventRacesApi.create(token, edit)
      else await adminEventRacesApi.update(token, edit.id!, edit)
      setMsg('✓ 已儲存'); setEdit(null); load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }
  async function remove(d: RaceEventDef) {
    if (!token || !d.id) return
    if (!confirm(`刪除多人事件「${d.name}」？`)) return
    setBusy(true); setErr('')
    try { await adminEventRacesApi.remove(token, d.id); setMsg('✓ 已刪除'); load() }
    catch (e: any) { setErr(e?.message || '刪除失敗') } finally { setBusy(false) }
  }

  const cSpec = cCat.find((c) => c.key === edit?.completion_type)
  const relLabel = (opts: RelOption[], k: string) => opts.find((o) => o.key === k)?.label ?? k
  const raceLabel = (id?: string) => (!id ? '所有賽事' : races.find((r) => r.id === id)?.title ?? '（指定賽事）')

  return (
    <div style={{ maxWidth: 880, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 10 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>賽事多人連動事件</h1>
          <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0 }}>
            觸發者跑步累積移動達門檻 → 依對象規則挑「同賽事報名者」→ 即時邀請、限時加入、各自達標領獎。
            對象規則同類互斥、跨類交集（AND）。
          </p>
        </div>
        {!edit && <button onClick={startNew} style={primaryBtn}>＋ 新增多人事件</button>}
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}

      {edit && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>{isNew ? '新增多人事件' : '編輯多人事件'}</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label="名稱" grow><input style={inp} value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} placeholder="如：全員衝刺" /></Field>
            <Field label="套用賽事"><select style={{ ...inp, width: 200 }} value={edit.race_id ?? ''} onChange={(e) => setEdit({ ...edit, race_id: e.target.value })}><option value="">所有賽事</option>{races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}</select></Field>
            <Field label="啟用"><select style={{ ...inp, width: 90 }} value={edit.enabled ? '1' : '0'} onChange={(e) => setEdit({ ...edit, enabled: e.target.value === '1' })}><option value="1">啟用</option><option value="0">停用</option></select></Field>
            <Field label="隨機權重"><input style={{ ...inp, width: 90 }} type="number" value={edit.weight} onChange={(e) => setEdit({ ...edit, weight: parseInt(e.target.value || '0', 10) })} /></Field>
          </div>

          <div style={sect}>
            <div style={sectTitle}>觸發（由觸發者跑步里程驅動）</div>
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <Field label="累積移動達（公尺）"><input style={{ ...inp, width: 140 }} type="number" value={edit.trigger_min_m} onChange={(e) => setEdit({ ...edit, trigger_min_m: parseInt(e.target.value || '0', 10) })} /></Field>
              <Field label="觸發者冷卻（秒）"><input style={{ ...inp, width: 140 }} type="number" value={edit.initiator_cooldown_sec} onChange={(e) => setEdit({ ...edit, initiator_cooldown_sec: parseInt(e.target.value || '0', 10) })} /></Field>
              <Field label="隨機推撥人數（0=全部）"><input style={{ ...inp, width: 150 }} type="number" value={edit.target_count} onChange={(e) => setEdit({ ...edit, target_count: parseInt(e.target.value || '0', 10) })} /></Field>
              <Field label="可加入時窗（秒）"><input style={{ ...inp, width: 120 }} type="number" value={edit.join_window_s} onChange={(e) => setEdit({ ...edit, join_window_s: parseInt(e.target.value || '0', 10) })} /></Field>
            </div>
          </div>

          <div style={sect}>
            <div style={sectTitle}>對象規則（相對觸發者；同類互斥、跨類交集）</div>
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
              <Field label="分組關係"><select style={{ ...inp, width: 160 }} value={edit.group_rel} onChange={(e) => setEdit({ ...edit, group_rel: e.target.value })}>{groupRel.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}</select></Field>
              <Field label="追蹤關係"><select style={{ ...inp, width: 180 }} value={edit.follow_rel} onChange={(e) => setEdit({ ...edit, follow_rel: e.target.value })}>{followRel.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}</select></Field>
              <Field label="性別關係"><select style={{ ...inp, width: 140 }} value={edit.gender_rel} onChange={(e) => setEdit({ ...edit, gender_rel: e.target.value })}>{genderRel.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}</select></Field>
            </div>
          </div>

          <div style={sect}>
            <div style={sectTitle}>完成條件</div>
            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end', marginBottom: edit.completion_type === 'draw_shape' ? 12 : 0 }}>
              <Field label="類型"><select style={{ ...inp, width: 200 }} value={edit.completion_type} onChange={(e) => setEdit({ ...edit, completion_type: e.target.value, completion_params: {} })}>{cCat.map((c) => <option key={c.key} value={c.key}>{c.label}</option>)}</select></Field>
              {edit.completion_type !== 'draw_shape' && (cSpec?.params ?? []).map((p) => (
                <Field key={p.key} label={`${p.label}（${p.unit}）`}>
                  <input style={{ ...inp, width: 120 }} type="number" value={edit.completion_params[p.key] ?? ''} onChange={(e) => setEdit({ ...edit, completion_params: { ...edit.completion_params, [p.key]: parseFloat(e.target.value) || 0 } })} />
                </Field>
              ))}
            </div>
            {edit.completion_type === 'draw_shape' && (
              <ShapeCompletionEditor value={edit.completion_params} onChange={(patch) => setEdit({ ...edit, completion_params: { ...edit.completion_params, ...patch } })} />
            )}
          </div>

          <div style={{ marginTop: 12 }}>
            <Field label="邀請文案（收邀者看到的劇情）" grow>
              <textarea style={{ ...inp, minHeight: 56, resize: 'vertical' }} value={edit.message} onChange={(e) => setEdit({ ...edit, message: e.target.value })} placeholder="有人發起衝刺！限時一起跑，達標領獎！" />
            </Field>
          </div>

          <div style={{ marginTop: 12 }}>
            <EventImageSlots value={edit} token={token} onChange={(patch) => setEdit({ ...edit, ...patch })} />
          </div>
          <div style={{ display: 'flex', gap: 12, marginTop: 12, flexWrap: 'wrap' }}>
            <Field label="完成獎勵 EXP"><input style={{ ...inp, width: 110 }} type="number" value={edit.reward_exp} onChange={(e) => setEdit({ ...edit, reward_exp: parseInt(e.target.value || '0', 10) })} /></Field>
            <Field label="完成獎勵 DP"><input style={{ ...inp, width: 110 }} type="number" value={edit.reward_dp} onChange={(e) => setEdit({ ...edit, reward_dp: parseInt(e.target.value || '0', 10) })} /></Field>
            <Field label="每人每日發獎上限（0=不限）"><input style={{ ...inp, width: 160 }} type="number" value={edit.per_user_daily_cap} onChange={(e) => setEdit({ ...edit, per_user_daily_cap: parseInt(e.target.value || '0', 10) })} /></Field>
          </div>

          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={save} disabled={busy} style={primaryBtn}>儲存</button>
            <button onClick={() => setEdit(null)} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 10 }}>
        {!defs && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {defs && defs.length === 0 && <div style={{ color: 'var(--tx-faint)' }}>尚無多人事件，點右上「新增多人事件」。</div>}
        {defs?.map((d) => {
          const c = cCat.find((x) => x.key === d.completion_type)
          const goal = (c?.params ?? []).map((p) => `${p.label} ${d.completion_params[p.key] ?? '—'}${p.unit}`).join('、')
          return (
            <div key={d.id} style={card}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10, flexWrap: 'wrap' }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontWeight: 800, fontSize: 15 }}>
                    {d.name}
                    {!d.enabled && <span style={{ ...badge, color: 'var(--tx-dim)', borderColor: 'var(--line-2)' }}>停用</span>}
                    <span style={{ ...badge, color: 'var(--fug)', borderColor: 'rgba(70,227,160,.4)' }}>{raceLabel(d.race_id)}</span>
                  </div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4 }}>
                    觸發：移動達 {d.trigger_min_m}m → 對象：{relLabel(groupRel, d.group_rel)}／{relLabel(followRel, d.follow_rel)}／{relLabel(genderRel, d.gender_rel)}
                    {d.target_count > 0 ? `（隨機 ${d.target_count} 人）` : ''} → 完成：{c?.label ?? d.completion_type}（{goal}）
                  </div>
                  {d.message && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 4, fontStyle: 'italic' }}>「{d.message}」</div>}
                  <div style={{ display: 'inline-flex', alignItems: 'center', gap: 10, marginTop: 6, fontSize: 12.5, flexWrap: 'wrap' }}>
                    <span style={{ color: 'var(--gold)', fontWeight: 700 }}>+{d.reward_exp} EXP</span>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3, color: '#FFD24D', fontWeight: 700 }}><DpCoin size={13} />+{d.reward_dp}</span>
                    <span style={{ color: 'var(--tx-faint)' }}>加入 {d.join_window_s}s・觸發冷卻 {d.initiator_cooldown_sec}s{d.per_user_daily_cap > 0 ? `・每日上限 ${d.per_user_daily_cap}` : ''}</span>
                  </div>
                </div>
                <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                  <button onClick={() => startEdit(d)} style={ghostBtn}>編輯</button>
                  <button onClick={() => duplicate(d)} style={ghostBtn}>複製</button>
                  <button onClick={() => remove(d)} style={{ ...ghostBtn, color: 'var(--hunt)' }}>刪除</button>
                </div>
              </div>
            </div>
          )
        })}
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
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 800, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5 }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 14px', cursor: 'pointer', fontSize: 13 }
const sect: React.CSSProperties = { marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }
const sectTitle: React.CSSProperties = { fontSize: 13, fontWeight: 700, marginBottom: 8, color: 'var(--tx)' }
const badge: React.CSSProperties = { marginLeft: 8, fontSize: 10.5, fontWeight: 700, border: '1px solid', borderRadius: 999, padding: '2px 8px', verticalAlign: 'middle' }
