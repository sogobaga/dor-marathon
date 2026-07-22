'use client'

import { useEffect, useState } from 'react'
import {
  adminRacesApi,
  adminPresetsApi,
  adminImagesApi,
  adminTaskModulesApi,
  adminAppSettingsApi,
  METRIC_BY_KEY,
  type CreateRacePayload,
  type EventMode,
  type GoalType,
  type RaceDetail,
  type RaceGroup,
  type RaceAddon,
  type GroupPreset,
  type BrochureBlock,
  type RaceTask,
  type TaskScope,
  type TaskModule,
  type CancellationPolicy,
} from '@/lib/api'
import { TaskItemEditor, type TaskFields } from '../TaskItemEditor'
import { CancellationPolicyFields, DEFAULT_CANCELLATION_POLICY, sortTiers, validateCancellationPolicy } from '../CancelPolicyEditor'

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

const CONTROL_STATUSES: { v: string; t: string; d: string }[] = [
  { v: 'active', t: '正常運作中', d: '依報名/賽事時間自動切換狀態' },
  { v: 'paused', t: '暫停報名', d: '強制暫停，報名一律失敗' },
  { v: 'suspended', t: '賽事中止', d: '中止統計，直到恢復正常' },
  { v: 'closed', t: '賽事關閉', d: '中止且前台完全不顯示' },
  { v: 'hidden', t: '賽事隱藏', d: '正常運作但不列在前台（有連結可進）' },
  { v: 'testing', t: '賽事測試中', d: '比照正常，但僅白名單 email 看得到' },
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
    requires_key: false, group_key: '', exp_reward: 0, dp_reward: 0,
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

// Date → datetime-local 字串（本地時間）
function dtLocal(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

// 新增賽事的預設時間：報名=今天00:00~當月最後一日12:00；賽事=下月1日00:00~下月最後一日12:00
function makeDefaults() {
  const n = new Date()
  return {
    regStart: dtLocal(new Date(n.getFullYear(), n.getMonth(), n.getDate(), 0, 0)),
    regEnd: dtLocal(new Date(n.getFullYear(), n.getMonth() + 1, 0, 12, 0)),
    start: dtLocal(new Date(n.getFullYear(), n.getMonth() + 1, 1, 0, 0)),
    end: dtLocal(new Date(n.getFullYear(), n.getMonth() + 2, 0, 12, 0)),
  }
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

  const [tab, setTab] = useState<'basic' | 'groups' | 'addons' | 'supplies' | 'brochure' | 'tasks' | 'cancel'>('basic')
  const [mode, setMode] = useState<EventMode>(initial?.event_mode ?? 'general')
  const [goalType, setGoalType] = useState<GoalType>(initial?.goal_type ?? 'distance')
  const [controlStatus, setControlStatus] = useState<string>(initial?.control_status ?? 'active')
  const [startingSoonDays, setStartingSoonDays] = useState<string>(String(initial?.starting_soon_days ?? 5))
  const [allowTeamGroups, setAllowTeamGroups] = useState<boolean>(initial?.allow_team_groups ?? false)
  const [testWhitelist, setTestWhitelist] = useState<string[]>(initial?.test_whitelist ?? [])
  const [wlInput, setWlInput] = useState('')
  const [brochureTitle, setBrochureTitle] = useState(initial?.brochure_title ?? '')
  const [brochure, setBrochure] = useState<BrochureBlock[]>(initial?.brochure ?? [])
  const [uploadingKey, setUploadingKey] = useState<string | null>(null)

  // 圖片區塊 content 存「圖片網址陣列」JSON；相容舊的單一網址字串。
  // 編輯時保留空白格（讓使用者剛新增的空格不會被吃掉）；送出/驗證時才濾空。
  function imagesOf(content: string): string[] {
    const c = (content ?? '').trim()
    if (!c) return []
    if (c.startsWith('[')) {
      try {
        const a = JSON.parse(c)
        return Array.isArray(a) ? a.map((x) => String(x ?? '')) : []
      } catch {
        return []
      }
    }
    return [c]
  }
  function blockHasContent(b: { block_type: string; content: string }): boolean {
    return b.block_type === 'image' ? imagesOf(b.content).some((x) => x.trim()) : !!b.content.trim()
  }
  function setBlockImages(i: number, imgs: string[]) {
    setBrochure((bs) => bs.map((x, idx) => (idx === i ? { ...x, content: JSON.stringify(imgs) } : x)))
  }
  async function uploadImage(i: number, k: number, file: File) {
    setUploadingKey(`${i}-${k}`)
    setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setBrochure((bs) => bs.map((x, idx) => {
        if (idx !== i) return x
        const imgs = imagesOf(x.content)
        imgs[k] = url
        return { ...x, content: JSON.stringify(imgs) }
      }))
    } catch (e: any) {
      setErr(e?.message || '圖片上傳失敗')
    } finally {
      setUploadingKey(null)
    }
  }

  const [title, setTitle] = useState(initial?.title ?? '')
  const [slug, setSlug] = useState(initial?.slug ?? '')
  const [slugTouched, setSlugTouched] = useState(isEdit)
  const [subtitle, setSubtitle] = useState(initial?.subtitle ?? '')
  const [blurb, setBlurb] = useState(initial?.blurb ?? '')
  const def = makeDefaults() // 新增時的預設時間（編輯則用既有值）
  const [regStart, setRegStart] = useState(initial?.registration_start ? toLocalInput(initial.registration_start) : (isEdit ? '' : def.regStart))
  const [regEnd, setRegEnd] = useState(initial?.registration_end ? toLocalInput(initial.registration_end) : (isEdit ? '' : def.regEnd))
  const [startDate, setStartDate] = useState(initial?.start_date ? toLocalInput(initial.start_date) : (isEdit ? '' : def.start))
  const [endDate, setEndDate] = useState(initial?.end_date ? toLocalInput(initial.end_date) : (isEdit ? '' : def.end))
  const [entryFeeNtd, setEntryFeeNtd] = useState(String((initial?.entry_fee ?? 0) / 100))
  const [requiredFields, setRequiredFields] = useState<string[]>(
    initial?.required_fields ?? ['real_name', 'phone']
  )

  // 後台僅編輯「官方」分組；前台自建的跑團分組(is_user_created)不在此處管理、亦不會被儲存誤刪
  const officialGroups = (initial?.groups ?? []).filter((g) => !g.is_user_created)
  const [groups, setGroups] = useState<RaceGroup[]>(
    officialGroups.length ? officialGroups.map((g) => ({ ...g })) : [emptyGroup(0)]
  )
  const [addons, setAddons] = useState<RaceAddon[]>(initial?.addons?.map((a) => ({ ...a })) ?? [])
  const [supplies, setSupplies] = useState<SupplyDraft[]>(
    (initial?.supplies ?? []).map((s) => ({
      scope: s.group_id ? officialGroups.findIndex((g) => g.id === s.group_id) : -1,
      kind: s.kind,
      name: s.name,
      description: s.description ?? '',
      image_url: s.image_url ?? '',
    }))
  )

  // 賽事任務（scope）：race_collective=全體；group scope 的 group_index 為「目前 groups 索引」，
  // group_index=null 代表「所有分組共同」（後端 group_id NULL）。
  const [tasks, setTasks] = useState<RaceTask[]>(
    (initial?.tasks ?? [])
      .map((t) => ({
        ...t,
        group_index:
          t.scope === 'race_collective' ? null
            : t.group_id ? officialGroups.findIndex((g) => g.id === t.group_id)
            : null, // group scope 且無 group_id → 所有分組共同
      }))
      .filter((t) => t.scope === 'race_collective' || t.group_index === null || (t.group_index ?? -1) >= 0)
  )
  const [taskModules, setTaskModules] = useState<TaskModule[]>([])

  const [presets, setPresets] = useState<GroupPreset[]>([])
  const [certBgUrl, setCertBgUrl] = useState(initial?.certificate_bg_url ?? '')
  const [certBgUploading, setCertBgUploading] = useState(false)
  const [bannerUrl, setBannerUrl] = useState(initial?.hero_image_url ?? '')
  const [bannerUploading, setBannerUploading] = useState(false)
  const [showDistanceRank, setShowDistanceRank] = useState(initial?.show_distance_rank ?? true)
  const [showTimeRank, setShowTimeRank] = useState(initial?.show_time_rank ?? true)
  const [vipOnly, setVipOnly] = useState<boolean>(initial?.vip_only ?? false)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  // 取消退費規則：預設「跟隨系統預設」；此賽事已有覆寫（config.cancellation_policy 非 null）才預設開啟自訂。
  const [cancelFollowDefault, setCancelFollowDefault] = useState<boolean>(!initial?.config?.cancellation_policy)
  const [cancelPolicy, setCancelPolicy] = useState<CancellationPolicy>(
    initial?.config?.cancellation_policy ?? DEFAULT_CANCELLATION_POLICY
  )
  // 目前系統預設值（唯讀參考顯示 + 使用者切換開啟自訂時的起始值）；載入前用內建預設暫代。
  const [systemDefaultPolicy, setSystemDefaultPolicy] = useState<CancellationPolicy>(DEFAULT_CANCELLATION_POLICY)

  async function uploadCertBg(file: File) {
    setCertBgUploading(true); setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setCertBgUrl(url)
    } catch (e: any) {
      setErr(e?.message || '底圖上傳失敗')
    } finally {
      setCertBgUploading(false)
    }
  }

  async function uploadBanner(file: File) {
    setBannerUploading(true); setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setBannerUrl(url)
    } catch (e: any) {
      setErr(e?.message || 'Banner 上傳失敗')
    } finally {
      setBannerUploading(false)
    }
  }

  useEffect(() => {
    adminPresetsApi.list(token).then((r) => setPresets(r.presets)).catch(() => {})
    adminTaskModulesApi.list(token).then((r) => setTaskModules(r.modules)).catch(() => {})
    adminAppSettingsApi.list(token).then((r) => {
      const raw = r.settings?.['cancellation_policy']
      if (!raw) return
      try {
        const parsed = JSON.parse(raw)
        setSystemDefaultPolicy(parsed)
        // 此賽事尚無覆寫時，把編輯器起始值帶成目前系統預設，使用者一旦切到「此賽事自訂」看到的是合理起點而非內建預設。
        if (!initial?.config?.cancellation_policy) setCancelPolicy(parsed)
      } catch {
        /* 壞資料時維持內建預設，不擋表單載入 */
      }
    }).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  // --- 賽事任務 helpers（tasks 為扁平陣列，靠 scope + group_index 分區）---
  // race_collective：忽略 gi。group scope：gi=null→所有分組共同(group_index null)；gi=number→指定分組。
  function sectionTasks(scope: TaskScope, gi: number | null) {
    return tasks
      .map((t, idx) => ({ t, idx }))
      .filter(({ t }) => {
        if (t.scope !== scope) return false
        if (scope === 'race_collective') return true
        return gi === null ? t.group_index == null : t.group_index === gi
      })
  }
  function newTask(scope: TaskScope, gi: number | null): RaceTask {
    return {
      scope, group_index: scope === 'race_collective' ? null : gi,
      metric_type: 'cumulative_distance', target_value: null, range_lo: null, range_hi: null,
      title: '', description: '', display_order: 0,
    }
  }
  function addTask(scope: TaskScope, gi: number | null) {
    setTasks((ts) => [...ts, newTask(scope, gi)])
  }
  function patchTask(idx: number, patch: Partial<RaceTask>) {
    setTasks((ts) => ts.map((t, i) => (i === idx ? { ...t, ...patch } : t)))
  }
  function removeTask(idx: number) {
    setTasks((ts) => ts.filter((_, i) => i !== idx))
  }
  function applyModule(scope: TaskScope, gi: number | null, moduleId: string) {
    const mod = taskModules.find((m) => m.id === moduleId)
    if (!mod) return
    const add: RaceTask[] = mod.items.map((it) => ({
      scope, group_index: scope === 'race_collective' ? null : gi,
      metric_type: it.metric_type, target_value: it.target_value ?? null,
      range_lo: it.range_lo ?? null, range_hi: it.range_hi ?? null,
      title: it.title, description: it.description ?? '', display_order: 0,
    }))
    setTasks((ts) => [...ts, ...add])
  }
  // 任務是否填妥（threshold 需 target；range 需 lo/hi）→ 送出前過濾，避免後端 400
  function taskComplete(t: RaceTask): boolean {
    const m = METRIC_BY_KEY[t.metric_type]
    if (!m) return false
    if (m.kind === 'checkpoint') return (t.checkpoints ?? []).some((c) => c.lat && c.lng)
    return m.kind === 'range' ? t.range_lo != null && t.range_hi != null : t.target_value != null
  }
  // 用「函式呼叫」而非元件，避免每次 render 重新掛載造成輸入失焦
  function taskSection(scope: TaskScope, gi: number | null, label: string, sub: string) {
    const rows = sectionTasks(scope, gi)
    return (
      <div style={{ ...card, background: 'var(--bg-2)' }}>
        <div style={{ fontWeight: 700, fontSize: 13.5 }}>{label}</div>
        <div style={hint}>{sub}</div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 10 }}>
          {rows.map(({ t, idx }) => (
            <TaskItemEditor key={idx} value={t as TaskFields} onChange={(p) => patchTask(idx, p)} onRemove={() => removeTask(idx)} />
          ))}
          {rows.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚未設定任務</div>}
        </div>
        <div style={{ display: 'flex', gap: 8, marginTop: 10, flexWrap: 'wrap', alignItems: 'center' }}>
          <button type="button" onClick={() => addTask(scope, gi)} style={ghostBtn}>＋ 新增任務</button>
          {taskModules.length > 0 && (
            <select
              style={{ ...inp, width: 'auto' }} value=""
              onChange={(e) => { if (e.target.value) { applyModule(scope, gi, e.target.value); e.target.value = '' } }}
            >
              <option value="">套用任務模組…</option>
              {taskModules.map((m) => <option key={m.id} value={m.id}>{m.name}（{m.items.length}）</option>)}
            </select>
          )}
        </div>
      </div>
    )
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
      hero_image_url: bannerUrl,
      event_mode: mode,
      goal_type: mode === 'competition' ? goalType : 'distance',
      group_mode: isRandom ? 'random' : 'self',
      control_status: controlStatus as CreateRacePayload['control_status'],
      starting_soon_days: parseInt(startingSoonDays || '5', 10) || 5,
      allow_team_groups: mode === 'competition' ? allowTeamGroups : false,
      vip_only: vipOnly,
      // config 是整包 JSONB struct marshal（非合併寫入）：務必以既有 config 為底、只覆寫 cancellation_policy，
      // 否則會把 factions/clubs/missions 等既有欄位一併清空（見後端 configToBytes/bytesToConfig 註解）。
      config: {
        ...(initial?.config ?? {}),
        cancellation_policy: cancelFollowDefault
          ? null
          : { deadline_days: cancelPolicy.deadline_days, tiers: sortTiers(cancelPolicy.tiers ?? []) },
      },
      test_whitelist: testWhitelist,
      brochure_title: brochureTitle.trim(),
      brochure: brochure
        .filter(blockHasContent)
        .map((b, idx) => ({
          ...b,
          content: b.block_type === 'image' ? JSON.stringify(imagesOf(b.content).map((x) => x.trim()).filter(Boolean)) : b.content.trim(),
          display_order: idx,
        })),
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
      tasks: tasks
        .filter(taskComplete)
        .map((t, idx) => ({
          scope: t.scope,
          group_index: t.scope === 'race_collective' ? null : t.group_index,
          metric_type: t.metric_type,
          target_value: t.target_value ?? null,
          range_lo: t.range_lo ?? null,
          range_hi: t.range_hi ?? null,
          title: t.title.trim(),
          description: (t.description ?? '').trim(),
          display_order: idx,
          checkpoints: t.metric_type === 'checkpoint'
            ? (t.checkpoints ?? []).filter((c) => c.lat && c.lng).map((c, ci) => ({
                lat: c.lat, lng: c.lng, radius_m: c.radius_m || 20, title: (c.title ?? '').trim(), display_order: ci,
              }))
            : undefined,
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
    // 打卡任務：座標未填會被靜默丟棄 → 明確擋下並提醒（避免誤把提示座標當成已填）
    const badCheckpoint = tasks.find((t) => t.metric_type === 'checkpoint' && (t.checkpoints ?? []).some((c) => !c.lat || !c.lng))
    if (badCheckpoint) {
      setErr(`打卡任務「${badCheckpoint.title || '未命名'}」有打卡點的座標未填（緯度/經度）。灰字只是提示，不是實際值，請確實填入後再儲存。`)
      setTab('tasks')
      return
    }
    if (!cancelFollowDefault) {
      const cancelErr = validateCancellationPolicy(cancelPolicy)
      if (cancelErr) {
        setErr(cancelErr)
        setTab('cancel')
        return
      }
    }
    setSaving(true)
    try {
      const payload = buildPayload()
      const res = isEdit
        ? await adminRacesApi.updateFull(token, initial!.id, payload)
        : await adminRacesApi.create(token, payload)
      // 完賽證明底圖（獨立端點；新賽事建立後才有 id）
      if ((certBgUrl || '') !== (initial?.certificate_bg_url || '')) {
        await adminRacesApi.setCertificateBg(token, res.race.id, certBgUrl)
      }
      // 排行榜顯示設定（獨立端點）
      if (showDistanceRank !== (initial?.show_distance_rank ?? true) || showTimeRank !== (initial?.show_time_rank ?? true) || !isEdit) {
        await adminRacesApi.setRankDisplay(token, res.race.id, { show_distance_rank: showDistanceRank, show_time_rank: showTimeRank })
      }
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
          ['brochure', `簡章 (${brochure.filter(blockHasContent).length})`],
          ['tasks', `任務 (${tasks.filter(taskComplete).length})`],
          ['cancel', `取消退費${cancelFollowDefault ? '' : ' ・自訂'}`],
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
                <input style={inp} type="datetime-local" value={startDate} onChange={(e) => setStartDate(e.target.value)} />
              </Field>
              <Field label="競賽結束">
                <input style={inp} type="datetime-local" value={endDate} onChange={(e) => setEndDate(e.target.value)} />
              </Field>
            </Row>
            <Row>
              <Field label="報名費 (NT$)">
                <input style={inp} type="number" value={entryFeeNtd} onChange={(e) => setEntryFeeNtd(e.target.value)} />
              </Field>
              <Field label="賽事即將開始 倒數天數">
                <input style={inp} type="number" min={0} value={startingSoonDays} onChange={(e) => setStartingSoonDays(e.target.value)} />
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

            <Field label="賽事控制狀態">
              <select style={inp} value={controlStatus} onChange={(e) => setControlStatus(e.target.value)}>
                {CONTROL_STATUSES.map((s) => (
                  <option key={s.v} value={s.v}>{s.t}</option>
                ))}
              </select>
              <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                {CONTROL_STATUSES.find((s) => s.v === controlStatus)?.d}
                　顯示狀態（報名中/賽事進行中…）由系統依時間自動推導。
              </span>
            </Field>

            {(
              <Field label="此賽事測試白名單（email）">
                <div style={{ display: 'flex', gap: 8 }}>
                  <input
                    style={inp} value={wlInput} placeholder="someone@example.com"
                    onChange={(e) => setWlInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        const v = wlInput.trim().toLowerCase()
                        if (v && !testWhitelist.includes(v)) setTestWhitelist((w) => [...w, v])
                        setWlInput('')
                      }
                    }}
                  />
                  <button
                    type="button" style={ghostBtn}
                    onClick={() => {
                      const v = wlInput.trim().toLowerCase()
                      if (v && !testWhitelist.includes(v)) setTestWhitelist((w) => [...w, v])
                      setWlInput('')
                    }}
                  >＋ 加入</button>
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
                  {testWhitelist.map((e) => (
                    <span key={e} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '4px 10px', fontSize: 12 }}>
                      {e}
                      <button type="button" onClick={() => setTestWhitelist((w) => w.filter((x) => x !== e))} style={{ ...linkBtn, color: 'var(--hunt)' }}>✕</button>
                    </span>
                  ))}
                  {testWhitelist.length === 0 && <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚未加入任何 email</span>}
                </div>
                <span style={{ fontSize: 11, color: controlStatus === 'testing' ? 'var(--fug)' : 'var(--tx-faint)', marginTop: 4 }}>
                  {controlStatus === 'testing'
                    ? '此賽事為「測試中」：只有此名單 + 全域預設白名單的帳號看得到。'
                    : '僅在「賽事控制狀態 = 賽事測試中」時生效；可先在此預設好名單。'}
                  　另也吃「後台 → 測試白名單」的全域預設名單。
                </span>
              </Field>
            )}

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

            <Field label="排行榜顯示（預設兩種都顯示，可關閉其一）">
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8, paddingTop: 2 }}>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, color: 'var(--tx)' }}>
                  <input type="checkbox" checked={showDistanceRank} onChange={(e) => setShowDistanceRank(e.target.checked)} />
                  顯示「累積里程榜」
                </label>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, color: 'var(--tx)' }}>
                  <input type="checkbox" checked={showTimeRank} onChange={(e) => setShowTimeRank(e.target.checked)} />
                  顯示「完成時間榜」（時間／配速；非配速賽可關閉）
                </label>
              </div>
            </Field>

            <Field label="VIP 限定">
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, color: 'var(--tx)', paddingTop: 2 }}>
                <input type="checkbox" checked={vipOnly} onChange={(e) => setVipOnly(e.target.checked)} />
                只提供給 VIP 會員（非 VIP 看不到、也不能報名）
              </label>
              <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                預設關閉。勾選後此賽事僅 VIP 帳號可見與報名。
              </span>
            </Field>

            <Field label="賽事 Banner（選填，顯示於賽事資訊頁頂部）">
              {bannerUrl ? (
                <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src={bannerUrl} alt="banner" style={{ width: 200, height: 75, objectFit: 'cover', borderRadius: 8, border: '1px solid var(--line-2)' }} />
                  <button type="button" style={{ ...ghostBtn, color: 'var(--hunt)' }} onClick={() => setBannerUrl('')}>移除</button>
                </div>
              ) : (
                <label style={{ ...ghostBtn, display: 'inline-block', cursor: 'pointer' }}>
                  {bannerUploading ? '上傳中…' : '＋ 上傳 Banner'}
                  <input type="file" accept="image/*" style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadBanner(f); e.target.value = '' }} />
                </label>
              )}
              <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                建議寬幅橫式（約 1200×400）；顯示於賽事資訊頁最上方。
              </span>
            </Field>

            <Field label="完賽證明底圖（選填，留空用系統預設設計）">
              {certBgUrl ? (
                <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src={certBgUrl} alt="底圖" style={{ width: 140, height: 99, objectFit: 'cover', borderRadius: 8, border: '1px solid var(--line-2)' }} />
                  <button type="button" style={{ ...ghostBtn, color: 'var(--hunt)' }} onClick={() => setCertBgUrl('')}>移除</button>
                </div>
              ) : (
                <label style={{ ...ghostBtn, display: 'inline-block', cursor: 'pointer' }}>
                  {certBgUploading ? '上傳中…' : '＋ 上傳底圖'}
                  <input type="file" accept="image/*" style={{ display: 'none' }} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadCertBg(f); e.target.value = '' }} />
                </label>
              )}
              <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                建議橫式、比例約 1240×877；姓名與成績會自動疊加在中下方。
              </span>
            </Field>
          </div>
        )}

        {tab === 'groups' && (
          <div style={col}>
            {mode === 'competition' && (
              <div style={{ ...card, background: 'var(--bg-2)' }}>
                <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, color: 'var(--tx)', fontWeight: 600 }}>
                  <input type="checkbox" checked={allowTeamGroups} onChange={(e) => setAllowTeamGroups(e.target.checked)} />
                  開放「跑團分組申請」
                </label>
                <div style={{ ...hint, marginTop: 6 }}>
                  開啟後，前台跑團成員可自行建立跑團分組；建立者可自選是否需要「跑團鑰匙」。此處設定的為官方分組。
                </div>
              </div>
            )}
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
                  <Field label="完成此分組獎勵 EXP">
                    <input style={inp} type="number" value={g.exp_reward ?? 0} onChange={(e) => updateGroup(i, { exp_reward: parseInt(e.target.value || '0', 10) })} />
                  </Field>
                  <Field label="完成此分組獎勵 DP">
                    <input style={inp} type="number" value={g.dp_reward ?? 0} onChange={(e) => updateGroup(i, { dp_reward: parseInt(e.target.value || '0', 10) })} />
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
                <Row>
                  <Field label="跑團鑰匙">
                    <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--tx)', padding: '6px 0' }}>
                      <input type="checkbox" checked={!!g.requires_key} onChange={(e) => updateGroup(i, { requires_key: e.target.checked })} />
                      需要鑰匙才能加入此分組
                    </label>
                  </Field>
                  {g.requires_key && (
                    <Field label="鑰匙密碼（報名時需輸入）">
                      <input style={inp} value={g.group_key ?? ''} onChange={(e) => updateGroup(i, { group_key: e.target.value })} placeholder="例：DOR2026" />
                    </Field>
                  )}
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

        {tab === 'brochure' && (
          <div style={col}>
            <Field label="簡章大主標">
              <input style={inp} value={brochureTitle} onChange={(e) => setBrochureTitle(e.target.value)} placeholder="例：2026 英雄馬拉松 賽事簡章" />
            </Field>

            {brochure.map((b, i) => {
              const upd = (patch: Partial<BrochureBlock>) =>
                setBrochure((bs) => bs.map((x, idx) => (idx === i ? { ...x, ...patch } : x)))
              const move = (d: number) =>
                setBrochure((bs) => {
                  const j = i + d
                  if (j < 0 || j >= bs.length) return bs
                  const n = [...bs]; [n[i], n[j]] = [n[j], n[i]]; return n
                })
              const TYPE_LABEL = { text: '文字', image: '圖片', video: '影片' } as const
              return (
                <div key={b.id ?? `new-${i}`} style={card}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                    <strong style={{ fontSize: 13 }}>區塊 {i + 1}・{TYPE_LABEL[b.block_type]}</strong>
                    <div style={{ display: 'flex', gap: 10 }}>
                      <button onClick={() => move(-1)} style={linkBtn} title="上移">↑</button>
                      <button onClick={() => move(1)} style={linkBtn} title="下移">↓</button>
                      <button onClick={() => setBrochure((bs) => bs.filter((_, idx) => idx !== i))} style={{ ...linkBtn, color: 'var(--hunt)' }}>移除</button>
                    </div>
                  </div>
                  {b.block_type === 'text' && (
                    <Field label="文字內容（可用 HTML，如 <h2> <p> <b> <ul> <a>）">
                      <textarea style={{ ...inp, minHeight: 120, resize: 'vertical', fontFamily: 'monospace' }} value={b.content} onChange={(e) => upd({ content: e.target.value })} />
                    </Field>
                  )}
                  {b.block_type === 'image' && (
                    <>
                      <Field label="圖片（可多張；前台會左右滑動瀏覽）">
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                          {imagesOf(b.content).map((src, k) => (
                            <div key={k} style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                              <span style={{ fontSize: 11, color: 'var(--tx-faint)', width: 16 }}>{k + 1}</span>
                              <input style={{ ...inp, flex: 1, minWidth: 160 }} value={src}
                                onChange={(e) => { const imgs = imagesOf(b.content); imgs[k] = e.target.value; setBlockImages(i, imgs) }}
                                placeholder="https://… 或上傳" />
                              <label style={{ ...ghostBtn, display: 'inline-flex', alignItems: 'center', whiteSpace: 'nowrap' }}>
                                {uploadingKey === `${i}-${k}` ? '上傳中…' : '⬆ 上傳'}
                                <input type="file" accept="image/*" style={{ display: 'none' }}
                                  onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadImage(i, k, f); e.target.value = '' }} />
                              </label>
                              {src && (
                                // eslint-disable-next-line @next/next/no-img-element
                                <img src={src} alt="" style={{ width: 64, height: 48, objectFit: 'cover', borderRadius: 6, border: '1px solid var(--line-2)' }} />
                              )}
                              <button type="button" onClick={() => setBlockImages(i, imagesOf(b.content).filter((_, x) => x !== k))} style={{ ...linkBtn, color: 'var(--hunt)' }}>移除</button>
                            </div>
                          ))}
                          <button type="button" onClick={() => setBlockImages(i, [...imagesOf(b.content), ''])} style={ghostBtn}>＋ 新增圖片</button>
                        </div>
                      </Field>
                      <Field label="圖說（選填）"><input style={inp} value={b.caption ?? ''} onChange={(e) => upd({ caption: e.target.value })} /></Field>
                    </>
                  )}
                  {b.block_type === 'video' && (
                    <>
                      <Field label="YouTube 連結"><input style={inp} value={b.content} onChange={(e) => upd({ content: e.target.value })} placeholder="https://www.youtube.com/watch?v=…" /></Field>
                      <Field label="影片說明（選填）"><input style={inp} value={b.caption ?? ''} onChange={(e) => upd({ caption: e.target.value })} /></Field>
                    </>
                  )}
                </div>
              )
            })}

            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <button onClick={() => setBrochure((bs) => [...bs, { block_type: 'text', content: '', display_order: bs.length }])} style={ghostBtn}>＋ 文字區塊</button>
              <button onClick={() => setBrochure((bs) => [...bs, { block_type: 'image', content: '', display_order: bs.length }])} style={ghostBtn}>＋ 圖片</button>
              <button onClick={() => setBrochure((bs) => [...bs, { block_type: 'video', content: '', display_order: bs.length }])} style={ghostBtn}>＋ YouTube 影片</button>
            </div>
          </div>
        )}

        {tab === 'tasks' && (
          <div style={col}>
            <div style={hint}>
              本輪為任務設定：可設賽事集體、各分組團體與個人的任務目標。完成判定與前台進度顯示將於後續推出（部分指標需擴充活動上傳資料）。
            </div>

            {taskSection('race_collective', null, '賽事集體任務（全部參賽者）', '全體參賽者數值「加總」達標即完成，例：全員合計爬升 8848m。')}

            <div style={{ fontWeight: 800, fontSize: 14, marginTop: 6 }}>所有分組共同任務</div>
            <div style={hint}>套用到「每一個分組」的統一目標，設一次即可（前台所有分組都會顯示）。例：所有組都需完成總里程 200K。</div>
            {taskSection('group_team', null, '團體任務（每組加總）', '套用到所有分組：各分組成員加總達標。')}
            {taskSection('group_individual', null, '個人任務（每人各自）', '套用到所有分組：每位成員各自達標。')}

            {groups.filter((g) => g.name.trim()).map((g, gi) => (
              <div key={g.id ?? `g-${gi}`} style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                <div style={{ fontWeight: 800, fontSize: 14, marginTop: 6 }}>分組專屬：{g.name || `分組 ${gi + 1}`}</div>
                {taskSection('group_team', gi, '本組團體任務（全組加總）', '僅此分組：成員加總達標。例：本組需維持團體配速。')}
                {taskSection('group_individual', gi, '本組個人任務（每人各自）', '僅此分組：每位成員各自達標。例：A 組配速 7:00–8:00、B 組 5:00–6:00。')}
              </div>
            ))}
            {groups.filter((g) => g.name.trim()).length === 0 && (
              <div style={{ fontSize: 12.5, color: 'var(--tx-faint)' }}>請先到「分組」分頁建立分組，才能設定分組專屬任務。</div>
            )}
          </div>
        )}

        {tab === 'cancel' && (
          <div style={col}>
            <div style={hint}>
              使用者申請取消報名時，依此政策計算可退費比例（詳見「系統設定」頁的說明）。預設跟隨系統預設值；
              如此賽事需要不同的退費規則（例如報名費不可退、或截止天數不同），可在此開啟自訂。
            </div>

            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, color: 'var(--tx)', fontWeight: 600 }}>
              <input
                type="checkbox"
                checked={!cancelFollowDefault}
                onChange={(e) => setCancelFollowDefault(!e.target.checked)}
              />
              此賽事自訂取消退費規則（不勾選＝跟隨系統預設）
            </label>

            {cancelFollowDefault ? (
              <div style={{ ...card, background: 'var(--bg-2)' }}>
                <div style={{ fontWeight: 700, fontSize: 13.5 }}>目前系統預設值（唯讀）</div>
                <div style={{ ...hint, marginTop: 6 }}>
                  {systemDefaultPolicy.tiers.length
                    ? systemDefaultPolicy.tiers
                        .slice()
                        .sort((a, b) => b.days_before - a.days_before)
                        .map((t) => `距賽事 ≥${t.days_before} 天退 ${t.ratio}%`)
                        .join('、')
                    : '未設定任何退費級距（一律不退費）'}
                  ；賽事開始前 {systemDefaultPolicy.deadline_days} 天內不可申請取消。
                </div>
                <div style={{ ...hint, marginTop: 6 }}>如需調整系統預設值，請到「系統設定」頁的「退費政策預設值」修改。</div>
              </div>
            ) : (
              <div style={card}>
                <CancellationPolicyFields policy={cancelPolicy} onChange={setCancelPolicy} />
              </div>
            )}
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
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 10, padding: '11px 20px', cursor: 'pointer', fontSize: 14,
}
const ghostBtn: React.CSSProperties = {
  background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)',
  borderRadius: 10, padding: '11px 16px', cursor: 'pointer', fontSize: 14,
}
const linkBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--fug)', cursor: 'pointer', fontSize: 12, padding: 0,
}
