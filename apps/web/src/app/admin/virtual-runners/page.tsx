'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminImagesApi, adminVirtualRunnersApi,
  type VirtualRunner, type VirtualRunnerLevelPreset, type VirtualCity, type VirtualLevel,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

// 虛擬選手後台：virtual_runners 人頭帳號管理（migration 146）。單筆新增／批次產生／編輯／停用／刪除，
// 以及 8 級能力模板（vr_level_presets）調參。個別賽事的「加入/移除」在 admin/races/[id]/virtual-runners。

const CITY_LABEL: Record<VirtualCity, string> = {
  taipei: '台北', new_taipei: '新北', taoyuan: '桃園', hsinchu: '新竹', taichung: '台中', tainan: '台南', kaohsiung: '高雄',
}
const CITIES = Object.keys(CITY_LABEL) as VirtualCity[]

const WINDOW_HOURS = [4, 5, 6, 19, 20, 21, 22] as const
const WINDOW_HOUR_LABEL: Record<number, string> = {
  4: '清晨4點', 5: '清晨5點', 6: '清晨6點', 19: '晚上7點', 20: '晚上8點', 21: '晚上9點', 22: '晚上10點',
}
const GENDER_LABEL: Record<'male' | 'female', string> = { male: '男', female: '女' }

function fmtDateTime(iso: string | null) {
  if (!iso) return '從未'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '從未'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function fmtPace(s: number) {
  const m = Math.floor(s / 60), sec = s % 60
  return `${m}'${String(sec).padStart(2, '0')}"`
}

// --- 編輯表單（同時涵蓋「新增單筆」與「編輯既有選手」；能力值欄位僅編輯時顯示，並用 overrideAbility
//     控制是否連同能力值一起送出——不勾＝交給後端依新 level 重新從 preset 帶入±5%抖動）---
type RForm = {
  user_id: string
  name: string; avatar_url: string
  gender: 'male' | 'female'
  city: VirtualCity
  level: VirtualLevel
  diligence: number
  window_hour: number
  overrideAbility: boolean
  avg_km: number
  monthly_km: number
  pace_fast_s: number
  pace_slow_s: number
  enabled: boolean
}

function emptyRForm(firstLevel: VirtualLevel): RForm {
  return {
    user_id: '', name: '', avatar_url: '', gender: 'male', city: 'taipei', level: firstLevel, diligence: 3, window_hour: 6,
    overrideAbility: false, avg_km: 0, monthly_km: 0, pace_fast_s: 0, pace_slow_s: 0, enabled: true,
  }
}
function toRForm(r: VirtualRunner): RForm {
  return {
    user_id: r.user_id, name: r.name, avatar_url: r.avatar_url, gender: r.gender, city: r.city, level: r.level, diligence: r.diligence,
    window_hour: r.window_hour, overrideAbility: false,
    avg_km: r.avg_km, monthly_km: r.monthly_km, pace_fast_s: r.pace_fast_s, pace_slow_s: r.pace_slow_s, enabled: r.enabled,
  }
}

type BForm = { count: number; level: VirtualLevel | ''; city: VirtualCity | ''; gender: 'male' | 'female' | '' }
function emptyBForm(): BForm { return { count: 10, level: '', city: '', gender: '' } }

export default function AdminVirtualRunnersPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [runners, setRunners] = useState<VirtualRunner[] | null>(null)
  const [presets, setPresets] = useState<VirtualRunnerLevelPreset[] | null>(null)
  const [rForm, setRForm] = useState<RForm | null>(null)
  const [rIsNew, setRIsNew] = useState(false)
  const [bForm, setBForm] = useState<BForm | null>(null)
  const [presetForm, setPresetForm] = useState<{ level: VirtualLevel; avg_km: number; monthly_km: number; pace_fast_s: number; pace_slow_s: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const [avatarBusy, setAvatarBusy] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    adminVirtualRunnersApi.list(t)
      .then((r) => { setRunners(r.runners); setPresets(r.presets) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「虛擬選手」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [router])
  useEffect(() => { load() }, [load])

  const sortedPresets = presets?.slice().sort((a, b) => a.sort_order - b.sort_order) ?? []
  const levelLabel = (lv: VirtualLevel) => sortedPresets.find((p) => p.level === lv)?.label ?? lv

  // --- 選手：新增/編輯 ---
  function rStartNew() {
    setRForm(emptyRForm(sortedPresets[0]?.level ?? 'beginner'))
    setRIsNew(true); setErr(''); setMsg(''); setBForm(null)
  }
  function rStartEdit(r: VirtualRunner) { setRForm(toRForm(r)); setRIsNew(false); setErr(''); setMsg(''); setBForm(null) }
  function rCancel() { setRForm(null); setErr('') }
  function setRF<K extends keyof RForm>(k: K, v: RForm[K]) { setRForm((f) => (f ? { ...f, [k]: v } : f)) }

  // 頭像：走既有 /admin/images 上傳（≤5MB、後端自動壓縮），成功後只先寫進表單，按「儲存」才落庫
  async function onPickAvatar(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = '' // 清掉 value，允許重選同一個檔案
    if (!file || !token) return
    setAvatarBusy(true); setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setRF('avatar_url', url)
    } catch (er: any) { setErr(er?.message || '頭像上傳失敗') } finally { setAvatarBusy(false) }
  }

  async function rSave() {
    if (!token || !rForm) return
    setBusy(true); setErr(''); setMsg('')
    try {
      if (rIsNew) {
        const { runner } = await adminVirtualRunnersApi.create(token, {
          name: rForm.name.trim() || undefined,
          gender: rForm.gender, city: rForm.city, level: rForm.level, diligence: rForm.diligence, window_hour: rForm.window_hour,
        })
        setMsg(`✓ 已新增虛擬選手「${runner.name}」`)
      } else {
        const body: Parameters<typeof adminVirtualRunnersApi.update>[2] = {
          gender: rForm.gender, city: rForm.city, level: rForm.level, diligence: rForm.diligence,
          window_hour: rForm.window_hour, enabled: rForm.enabled,
        }
        const newName = rForm.name.trim()
        if (newName) body.name = newName // 空＝不改名（後端 name 欄位省略即不動）
        body.avatar_url = rForm.avatar_url // 每次帶上：''＝清除、值未變＝冪等
        if (rForm.overrideAbility) {
          body.avg_km = rForm.avg_km; body.monthly_km = rForm.monthly_km
          body.pace_fast_s = rForm.pace_fast_s; body.pace_slow_s = rForm.pace_slow_s
        }
        await adminVirtualRunnersApi.update(token, rForm.user_id, body)
        setMsg(`✓ 已更新「${rForm.name}」`)
      }
      setRForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  async function rToggleEnabled(r: VirtualRunner) {
    if (!token) return
    setErr('')
    try {
      await adminVirtualRunnersApi.update(token, r.user_id, { enabled: !r.enabled })
      load()
    } catch (e: any) { setErr(e?.message || '更新失敗') }
  }

  async function rDelete(r: VirtualRunner) {
    if (!token) return
    if (!confirm(`確定刪除虛擬選手「${r.name}」？此動作無法復原。`)) return
    setBusy(true); setErr(''); setMsg('')
    try {
      await adminVirtualRunnersApi.remove(token, r.user_id)
      setMsg(`✓ 已刪除「${r.name}」`)
      if (rForm?.user_id === r.user_id) setRForm(null)
      load()
    } catch (e: any) {
      if (e?.status === 409) setErr(`「${r.name}」已有報名紀錄，僅能停用（無法刪除）`)
      else setErr(e?.message || '刪除失敗')
    } finally { setBusy(false) }
  }

  // --- 全部重新取名 ---
  async function regenerateAllNames() {
    if (!token) return
    if (!confirm('確定要重新產生「全部」虛擬選手的名字嗎？此動作會覆蓋所有虛擬選手（含已停用）目前的名字，無法復原。')) return
    setBusy(true); setErr(''); setMsg('')
    try {
      const { renamed } = await adminVirtualRunnersApi.regenerateNames(token)
      setMsg(`✓ 已重新命名 ${renamed} 位`)
      load()
    } catch (e: any) { setErr(e?.message || '重新取名失敗') } finally { setBusy(false) }
  }

  // --- 同步稱號：對所有 enabled 選手依既有稱號引擎解鎖稱號 + 視情況更新展示稱號（也是初次回填入口）---
  async function syncTitles() {
    if (!token) return
    setBusy(true); setErr(''); setMsg('')
    try {
      const { synced, changed } = await adminVirtualRunnersApi.syncTitles(token)
      setMsg(`✓ 已同步 ${synced} 位、更新展示稱號 ${changed} 位`)
      load()
    } catch (e: any) { setErr(e?.message || '同步稱號失敗') } finally { setBusy(false) }
  }

  // --- 批次產生 ---
  function bStartNew() { setBForm(emptyBForm()); setErr(''); setMsg(''); setRForm(null) }
  function bCancel() { setBForm(null); setErr('') }
  function setBF<K extends keyof BForm>(k: K, v: BForm[K]) { setBForm((f) => (f ? { ...f, [k]: v } : f)) }

  async function bSave() {
    if (!token || !bForm) return
    if (bForm.count < 1 || bForm.count > 200) { setErr('數量須介於 1-200'); return }
    setBusy(true); setErr(''); setMsg('')
    try {
      const body: Parameters<typeof adminVirtualRunnersApi.batchCreate>[1] = { count: bForm.count }
      if (bForm.level) body.level = bForm.level
      if (bForm.city) body.city = bForm.city
      if (bForm.gender) body.gender = bForm.gender
      const { created } = await adminVirtualRunnersApi.batchCreate(token, body)
      setMsg(`✓ 已批次產生 ${created} 位虛擬選手`)
      setBForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '批次產生失敗') } finally { setBusy(false) }
  }

  // --- 等級參數表 ---
  function pStartEdit(p: VirtualRunnerLevelPreset) {
    setPresetForm({ level: p.level, avg_km: p.avg_km, monthly_km: p.monthly_km, pace_fast_s: p.pace_fast_s, pace_slow_s: p.pace_slow_s })
    setErr(''); setMsg('')
  }
  function pCancel() { setPresetForm(null); setErr('') }
  async function pSave() {
    if (!token || !presetForm) return
    if (presetForm.pace_fast_s >= presetForm.pace_slow_s) { setErr('快配速須小於慢配速'); return }
    setBusy(true); setErr(''); setMsg('')
    try {
      await adminVirtualRunnersApi.updatePreset(token, presetForm.level, {
        avg_km: presetForm.avg_km, monthly_km: presetForm.monthly_km,
        pace_fast_s: presetForm.pace_fast_s, pace_slow_s: presetForm.pace_slow_s,
      })
      setMsg(`✓ 已更新等級參數「${levelLabel(presetForm.level)}」（不回溯已建立的選手）`)
      setPresetForm(null)
      load()
    } catch (e: any) { setErr(e?.message || '儲存失敗') } finally { setBusy(false) }
  }

  return (
    <div style={{ maxWidth: 1040, margin: '0 auto' }}>
      <div>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>虛擬選手</h1>
        <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0, lineHeight: 1.7 }}>
          管理 is_virtual 人頭帳號（無法登入，用於補賽事熱度/陪跑）。能力值（單次/月里程、配速）建立時依等級模板 ±5% 隨機帶入；
          個別賽事的加入/移除請至該賽事「🤖 虛擬選手」頁面。
        </p>
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13, whiteSpace: 'pre-wrap' }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}

      {/* ───────── 選手列表 ───────── */}
      <SectionHeader
        title="選手列表" sub={`（${runners?.length ?? 0}）`}
        action={
          !rForm && !bForm && (
            <div style={{ display: 'flex', gap: 8 }}>
              <button onClick={regenerateAllNames} disabled={busy} style={{ ...ghostBtn, opacity: busy ? 0.5 : 1 }}>🎲 全部重新取名</button>
              <button onClick={syncTitles} disabled={busy} style={{ ...ghostBtn, opacity: busy ? 0.5 : 1 }}>🏅 同步稱號</button>
              <button onClick={bStartNew} style={ghostBtn}>⚡ 批次產生</button>
              <button onClick={rStartNew} style={primaryBtn}>＋ 新增</button>
            </div>
          )
        }
      />

      {bForm && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>⚡ 批次產生虛擬選手</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end' }}>
            <Field label="數量（1-200）">
              <input type="number" min={1} max={200} style={{ ...inp, width: 100 }} value={bForm.count}
                onChange={(e) => setBF('count', Math.max(1, Math.min(200, parseInt(e.target.value, 10) || 1)))} />
            </Field>
            <Field label="等級（空＝逐位隨機）">
              <select style={{ ...inp, width: 160 }} value={bForm.level} onChange={(e) => setBF('level', e.target.value as VirtualLevel | '')}>
                <option value="">隨機</option>
                {sortedPresets.map((p) => <option key={p.level} value={p.level}>{p.label}</option>)}
              </select>
            </Field>
            <Field label="城市（空＝逐位隨機）">
              <select style={{ ...inp, width: 130 }} value={bForm.city} onChange={(e) => setBF('city', e.target.value as VirtualCity | '')}>
                <option value="">隨機</option>
                {CITIES.map((c) => <option key={c} value={c}>{CITY_LABEL[c]}</option>)}
              </select>
            </Field>
            <Field label="性別（空＝逐位隨機）">
              <select style={{ ...inp, width: 120 }} value={bForm.gender} onChange={(e) => setBF('gender', e.target.value as 'male' | 'female' | '')}>
                <option value="">隨機</option>
                <option value="male">男</option>
                <option value="female">女</option>
              </select>
            </Field>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={bSave} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>{busy ? '產生中…' : `產生 ${bForm.count} 位`}</button>
            <button onClick={bCancel} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      {rForm && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>{rIsNew ? '新增虛擬選手' : `編輯選手：${rForm.name}`}</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label={rIsNew ? '姓名（空＝自動取名）' : '姓名（空＝不改名）'}>
              <input style={{ ...inp, width: 140 }} value={rForm.name} onChange={(e) => setRF('name', e.target.value)} placeholder={rIsNew ? '自動' : ''} />
            </Field>
            {!rIsNew && (
              <Field label="頭像">
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  {rForm.avatar_url
                    ? <img src={rForm.avatar_url} alt="" style={{ width: 36, height: 36, borderRadius: 999, objectFit: 'cover', border: '1px solid var(--line-2)' }} />
                    : <div style={{ width: 36, height: 36, borderRadius: 999, background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 14 }}>{rForm.name.trim().charAt(0) || '？'}</div>}
                  <label style={{ ...ghostBtn, cursor: 'pointer', opacity: avatarBusy ? 0.5 : 1 }}>
                    {avatarBusy ? '上傳中…' : '上傳圖片'}
                    <input type="file" accept="image/*" style={{ display: 'none' }} disabled={avatarBusy} onChange={onPickAvatar} />
                  </label>
                  {rForm.avatar_url && <button onClick={() => setRF('avatar_url', '')} style={ghostBtn}>移除</button>}
                </div>
              </Field>
            )}
            <Field label="性別">
              <select style={{ ...inp, width: 100 }} value={rForm.gender} onChange={(e) => setRF('gender', e.target.value as 'male' | 'female')}>
                <option value="male">男</option>
                <option value="female">女</option>
              </select>
            </Field>
            <Field label="城市">
              <select style={{ ...inp, width: 130 }} value={rForm.city} onChange={(e) => setRF('city', e.target.value as VirtualCity)}>
                {CITIES.map((c) => <option key={c} value={c}>{CITY_LABEL[c]}</option>)}
              </select>
            </Field>
            <Field label="等級">
              <select style={{ ...inp, width: 160 }} value={rForm.level} onChange={(e) => setRF('level', e.target.value as VirtualLevel)}>
                {sortedPresets.map((p) => <option key={p.level} value={p.level}>{p.label}</option>)}
              </select>
            </Field>
            <Field label="積極度（1-5）">
              <select style={{ ...inp, width: 90 }} value={rForm.diligence} onChange={(e) => setRF('diligence', +e.target.value)}>
                {[1, 2, 3, 4, 5].map((n) => <option key={n} value={n}>{n}</option>)}
              </select>
            </Field>
            <Field label="慣用時段">
              <select style={{ ...inp, width: 130 }} value={rForm.window_hour} onChange={(e) => setRF('window_hour', +e.target.value)}>
                {WINDOW_HOURS.map((h) => <option key={h} value={h}>{WINDOW_HOUR_LABEL[h]}</option>)}
              </select>
            </Field>
            {!rIsNew && (
              <Field label="啟用">
                <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, height: 36 }}>
                  <input type="checkbox" checked={rForm.enabled} onChange={(e) => setRF('enabled', e.target.checked)} />enabled
                </label>
              </Field>
            )}
          </div>

          {!rIsNew && (
            <div style={{ marginTop: 14, paddingTop: 14, borderTop: '1px solid var(--line-2)' }}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, marginBottom: 10 }}>
                <input type="checkbox" checked={rForm.overrideAbility} onChange={(e) => setRF('overrideAbility', e.target.checked)} />
                手動覆寫能力值（不勾＝若上方等級有變更，儲存時將由後端依新等級模板重新帶入 ±5% 隨機值）
              </label>
              <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', opacity: rForm.overrideAbility ? 1 : 0.45 }}>
                <Field label="單次里程 avg_km">
                  <input type="number" step="0.1" disabled={!rForm.overrideAbility} style={{ ...inp, width: 100 }}
                    value={rForm.avg_km} onChange={(e) => setRF('avg_km', +e.target.value)} />
                </Field>
                <Field label="月里程 monthly_km">
                  <input type="number" step="0.1" disabled={!rForm.overrideAbility} style={{ ...inp, width: 100 }}
                    value={rForm.monthly_km} onChange={(e) => setRF('monthly_km', +e.target.value)} />
                </Field>
                <Field label="快配速（秒/km）">
                  <input type="number" disabled={!rForm.overrideAbility} style={{ ...inp, width: 100 }}
                    value={rForm.pace_fast_s} onChange={(e) => setRF('pace_fast_s', +e.target.value)} />
                </Field>
                <Field label="慢配速（秒/km）">
                  <input type="number" disabled={!rForm.overrideAbility} style={{ ...inp, width: 100 }}
                    value={rForm.pace_slow_s} onChange={(e) => setRF('pace_slow_s', +e.target.value)} />
                </Field>
              </div>
            </div>
          )}

          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={rSave} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={rCancel} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
        {!runners && <div style={{ color: 'var(--tx-dim)' }}>載入中…</div>}
        {runners && runners.length === 0 && <div style={{ color: 'var(--tx-faint)' }}>尚無虛擬選手，點右上「＋ 新增」或「⚡ 批次產生」。</div>}
        {runners?.map((r) => (
          <RunnerRow key={r.user_id} r={r} levelLabel={levelLabel(r.level)}
            onToggle={() => rToggleEnabled(r)} onEdit={() => rStartEdit(r)} onDelete={() => rDelete(r)} />
        ))}
      </div>

      {/* ───────── 等級參數表 ───────── */}
      <SectionHeader title="等級參數表" sub="（vr_level_presets，調整不回溯已建立的選手）" action={null} top={36} />

      {presetForm && (
        <div style={{ ...card, marginTop: 12 }}>
          <div style={{ fontWeight: 800, marginBottom: 12 }}>編輯等級參數：{levelLabel(presetForm.level)}</div>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
            <Field label="單次里程 avg_km">
              <input type="number" step="0.1" style={{ ...inp, width: 110 }} value={presetForm.avg_km}
                onChange={(e) => setPresetForm((f) => (f ? { ...f, avg_km: +e.target.value } : f))} />
            </Field>
            <Field label="月里程 monthly_km">
              <input type="number" step="0.1" style={{ ...inp, width: 110 }} value={presetForm.monthly_km}
                onChange={(e) => setPresetForm((f) => (f ? { ...f, monthly_km: +e.target.value } : f))} />
            </Field>
            <Field label="快配速（秒/km）">
              <input type="number" style={{ ...inp, width: 110 }} value={presetForm.pace_fast_s}
                onChange={(e) => setPresetForm((f) => (f ? { ...f, pace_fast_s: +e.target.value } : f))} />
            </Field>
            <Field label="慢配速（秒/km）">
              <input type="number" style={{ ...inp, width: 110 }} value={presetForm.pace_slow_s}
                onChange={(e) => setPresetForm((f) => (f ? { ...f, pace_slow_s: +e.target.value } : f))} />
            </Field>
          </div>
          <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
            <button onClick={pSave} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.5 : 1 }}>{busy ? '儲存中…' : '儲存'}</button>
            <button onClick={pCancel} style={ghostBtn}>取消</button>
          </div>
        </div>
      )}

      <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflow: 'hidden', marginTop: 12, marginBottom: 24 }}>
        <div style={{ ...presetRow, ...presetHeadRow }}>
          <span style={{ flex: '0 0 110px' }}>等級</span>
          <span style={{ flex: '0 0 90px', textAlign: 'right' }}>單次(km)</span>
          <span style={{ flex: '0 0 90px', textAlign: 'right' }}>月里程(km)</span>
          <span style={{ flex: '0 0 140px', textAlign: 'right' }}>配速區間(/km)</span>
          <span style={{ flex: '0 0 60px' }} />
        </div>
        {!presets && <div style={{ padding: 16, color: 'var(--tx-dim)' }}>載入中…</div>}
        {sortedPresets.map((p) => (
          <div key={p.level} style={presetRow}>
            <span style={{ flex: '0 0 110px', fontWeight: 700 }}>{p.label}</span>
            <span style={{ flex: '0 0 90px', textAlign: 'right', color: 'var(--tx-dim)' }}>{p.avg_km}</span>
            <span style={{ flex: '0 0 90px', textAlign: 'right', color: 'var(--tx-dim)' }}>{p.monthly_km}</span>
            <span style={{ flex: '0 0 140px', textAlign: 'right', color: 'var(--tx-dim)' }}>{fmtPace(p.pace_fast_s)} ~ {fmtPace(p.pace_slow_s)}</span>
            <span style={{ flex: '0 0 60px', textAlign: 'right' }}>
              <button onClick={() => pStartEdit(p)} style={{ ...ghostBtn, padding: '4px 10px', fontSize: 12 }}>編輯</button>
            </span>
          </div>
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

function RunnerRow({
  r, levelLabel, onToggle, onEdit, onDelete,
}: { r: VirtualRunner; levelLabel: string; onToggle: () => void; onEdit: () => void; onDelete: () => void }) {
  return (
    <div style={row}>
      {r.avatar_url
        ? <img src={r.avatar_url} alt="" style={{ width: 34, height: 34, borderRadius: 999, objectFit: 'cover', flexShrink: 0, border: '1px solid var(--line-2)' }} />
        : <div style={{ width: 34, height: 34, borderRadius: 999, background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 14, flexShrink: 0 }}>{r.name.charAt(0)}</div>}
      <div style={{ minWidth: 0, flex: 1 }}>
        <div style={{ fontWeight: 700, fontSize: 14 }}>
          {r.name}
          <span style={{ fontWeight: 400, color: 'var(--tx-dim)', fontSize: 12, marginLeft: 6 }}>{GENDER_LABEL[r.gender]} · {CITY_LABEL[r.city]}</span>
          {!r.enabled && <span style={{ ...badge, color: 'var(--tx-dim)', borderColor: 'var(--line-2)' }}>停用</span>}
        </div>
        <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>
          {levelLabel} · 積極度 {r.diligence} · {WINDOW_HOUR_LABEL[r.window_hour] ?? r.window_hour} ·
          單次 {r.avg_km}km / 月 {r.monthly_km}km · {fmtPace(r.pace_fast_s)}~{fmtPace(r.pace_slow_s)}/km
        </div>
        <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 2 }}>
          最近生成 {fmtDateTime(r.last_generated_at)} · 已參賽 {r.race_count} 場
        </div>
      </div>
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--tx-dim)', flexShrink: 0 }}>
        <input type="checkbox" checked={r.enabled} onChange={onToggle} />啟用
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
const presetRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 10, padding: '10px 14px', borderBottom: '1px solid var(--line)', fontSize: 13 }
const presetHeadRow: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.05em', fontWeight: 700 }
