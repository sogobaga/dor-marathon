'use client'

import { useEffect, useRef, useState } from 'react'
import {
  adminRacesApi,
  adminPresetsApi,
  adminImagesApi,
  adminTaskModulesApi,
  adminAppSettingsApi,
  adminRewardTemplatesApi,
  adminRewardGroupsApi,
  adminEventCouponsApi,
  METRIC_BY_KEY,
  type CreateRacePayload,
  type EventMode,
  type GoalType,
  type RaceDetail,
  type RaceGroup,
  type RaceAddon,
  type GroupPreset,
  type BrochureBlock,
  type BrochureImageItem,
  normalizeBrochureImage,
  type RaceTask,
  type TaskScope,
  type TaskModule,
  type CancellationPolicy,
  type ChallengeRule,
  type CompletionType,
  type RewardItem,
  type RewardItemType,
  type RewardTemplate,
  type RewardSerialGroup,
  type EventCouponDef,
} from '@/lib/api'
import { TaskItemEditor, type TaskFields } from '../TaskItemEditor'
import { CancellationPolicyFields, DEFAULT_CANCELLATION_POLICY, sortTiers, validateCancellationPolicy } from '../CancelPolicyEditor'
import { renderCertificate, CERT_DEFAULT_LAYOUT, resolveCertElementLayout } from '@/lib/certificate'
import type { CertElementLayout } from '@/lib/api'

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
  { v: 'personal', t: '個人挑戰模式', desc: '可重複報名挑戰，依完成次數排名' },
]

const COMPLETION_TYPES: { v: CompletionType; t: string }[] = [
  { v: 'streak_days', t: '連續天數（連續 N 天每天達標）' },
  { v: 'window_cumulative', t: '區間累積（M 天內累積達標）' },
  { v: 'single_distance', t: '單趟距離（單次跑步達標）' },
]

// 完賽證明可視化排版編輯器：8 組元素的顯示名稱＋概略熱區尺寸（canvas px，1240×877 座標系；粗略即可，
// 中心對位）。與 '@/lib/certificate' 的 CERT_DEFAULT_LAYOUT 一一對應，順序即編輯器熱區的疊圖順序。
const CERT_ELEMENT_LABELS: Record<string, string> = {
  cert_title: '完賽證明（小標）',
  name: '姓名',
  race_name: '賽事名稱',
  group: '分組',
  col1: '完成里程',
  col2: '完成時間',
  col3: '完成名次',
  date: '完成日期',
}
const CERT_ELEMENT_KEYS = Object.keys(CERT_ELEMENT_LABELS)
const CERT_HITBOX: Record<string, { w: number; h: number }> = {
  cert_title: { w: 220, h: 44 },
  name: { w: 320, h: 100 },
  race_name: { w: 580, h: 90 },
  group: { w: 320, h: 44 },
  col1: { w: 170, h: 90 },
  col2: { w: 190, h: 90 },
  col3: { w: 170, h: 90 },
  date: { w: 380, h: 44 },
}

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
  // 個人挑戰模式（event_mode=personal）挑戰規則；輸入用字串狀態（空字串＝未填），送出時才轉數字。
  const [completionType, setCompletionType] = useState<CompletionType>(
    initial?.challenge_rule?.completion_type ?? 'streak_days'
  )
  const [chDays, setChDays] = useState(String(initial?.challenge_rule?.days ?? ''))
  const [chMinKmPerDay, setChMinKmPerDay] = useState(String(initial?.challenge_rule?.min_km_per_day ?? ''))
  const [chDailyMode, setChDailyMode] = useState<'cumulative' | 'single'>(initial?.challenge_rule?.daily_mode === 'single' ? 'single' : 'cumulative')
  const [chWindowDays, setChWindowDays] = useState(String(initial?.challenge_rule?.window_days ?? ''))
  const [chCumKm, setChCumKm] = useState(String(initial?.challenge_rule?.cum_km ?? ''))
  const [chSingleKm, setChSingleKm] = useState(String(initial?.challenge_rule?.single_km ?? ''))
  // 即時獎勵設定（完成觸發機率 roll；活動獎勵系統 P2，僅 personal 模式使用，選填）
  const [rewardItems, setRewardItems] = useState<RewardItem[]>(initial?.reward_config?.items ?? [])
  // 參賽虛擬獎勵設定（migration 140；選填）：與上面即時獎勵共用同一 RewardItem 結構/編輯元件
  // （RewardItemRow），但觸發條件完全不同——不看任務，賽事開始後排程自動發給所有已報名(paid)者。
  const [entryRewardItems, setEntryRewardItems] = useState<RewardItem[]>(initial?.entry_reward_config?.items ?? [])
  const [rewardTemplates, setRewardTemplates] = useState<RewardTemplate[]>([])
  const [rewardGroups, setRewardGroups] = useState<RewardSerialGroup[]>([])
  const [couponDefs, setCouponDefs] = useState<EventCouponDef[]>([])
  // 即時獎勵「期望值試算器」的可調假設值（純前端試算顯示用，不寫入賽事資料／不隨表單送出）：
  // LINE POINTS 單點成本、信用卡刷卡手續費率、預估完賽率。三者皆可能隨時間變動（採購價/金流費率/
  // 活動屬性不同），故做成輸入欄而非寫死常數，方便管理者依實際狀況調整。
  const [pointCostNtd, setPointCostNtd] = useState(1.3) // LINE POINTS 單點成本（元）；預設 1.3
  const [cardFeeRatePct, setCardFeeRatePct] = useState(2.35) // 信用卡刷卡手續費率（%）；預設 2.35
  const [expectedFinishRatePct, setExpectedFinishRatePct] = useState(100) // 預估完賽率（%）；預設 100
  // VIP 活動優惠券面額（分）：來自後台系統設定 vip_coupon_value_cents，見下方 useEffect 載入；
  // 10000（$100）為讀取失敗時的 fallback，與後端 appsettings.GetInt 的 fallback 預設一致。
  const [vipCouponValueCents, setVipCouponValueCents] = useState(10000)
  const [controlStatus, setControlStatus] = useState<string>(initial?.control_status ?? 'active')
  const [startingSoonDays, setStartingSoonDays] = useState<string>(String(initial?.starting_soon_days ?? 5))
  const [allowTeamGroups, setAllowTeamGroups] = useState<boolean>(initial?.allow_team_groups ?? false)
  const [testWhitelist, setTestWhitelist] = useState<string[]>(initial?.test_whitelist ?? [])
  const [wlInput, setWlInput] = useState('')
  const [brochureTitle, setBrochureTitle] = useState(initial?.brochure_title ?? '')
  const [brochure, setBrochure] = useState<BrochureBlock[]>(initial?.brochure ?? [])
  const [uploadingKey, setUploadingKey] = useState<string | null>(null)

  // 圖片區塊 content 存「陣列」JSON（元素可為純字串網址，或 {url,caption?,link?} 物件，
  // 每張圖各自可選填說明文字＋點擊連結）；相容舊的單一網址字串。一律正規化成 BrochureImageItem。
  // 編輯時保留空白格（讓使用者剛新增的空格不會被吃掉）；送出/驗證時才濾空（見 finalizeImageItems）。
  function imagesOf(content: string): BrochureImageItem[] {
    const c = (content ?? '').trim()
    if (!c) return []
    if (c.startsWith('[')) {
      try {
        const a = JSON.parse(c)
        return Array.isArray(a) ? a.map(normalizeBrochureImage) : []
      } catch {
        return []
      }
    }
    return [normalizeBrochureImage(c)]
  }
  function blockHasContent(b: { block_type: string; content: string }): boolean {
    return b.block_type === 'image' ? imagesOf(b.content).some((x) => x.url.trim()) : !!b.content.trim()
  }
  function setBlockImages(i: number, imgs: BrochureImageItem[]) {
    setBrochure((bs) => bs.map((x, idx) => (idx === i ? { ...x, content: JSON.stringify(imgs) } : x)))
  }
  // 送出前：trim 各欄位、濾除空白 URL 的項目；沒有 caption/link 的圖片維持純字串格式（與舊資料
  // 格式一致，儲存最精簡，有填才升級成物件）。後端 EncodeBrochureImages 會再做一次正規化/驗證。
  function finalizeImageItems(items: BrochureImageItem[]): (string | BrochureImageItem)[] {
    return items
      .map((it) => ({ url: it.url.trim(), caption: (it.caption ?? '').trim(), link: (it.link ?? '').trim() }))
      .filter((it) => it.url)
      .map((it) => (it.caption || it.link ? it : it.url))
  }
  async function uploadImage(i: number, k: number, file: File) {
    setUploadingKey(`${i}-${k}`)
    setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setBrochure((bs) => bs.map((x, idx) => {
        if (idx !== i) return x
        const imgs = imagesOf(x.content)
        imgs[k] = { ...imgs[k], url }
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
  const [feeMode, setFeeMode] = useState<'uniform' | 'per_group'>(initial?.fee_mode ?? 'uniform')
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
  // 完賽證明模擬預覽：底圖或賽事名稱一變就用示例資料重繪，讓管理者上傳底圖當下就能確認文字配置
  // （見下方 useEffect；certBgUrl 上傳完成後已是真實已存 URL——adminImagesApi.upload 立即上傳到圖床，
  // 不是本地 blob，故可直接沿用 renderCertificate 既有的 loadImage 同源載入，不需另外處理跨域/暫存）。
  const [certPreviewImg, setCertPreviewImg] = useState('')
  const [certPreviewLoading, setCertPreviewLoading] = useState(false)
  // 完賽證明可視化排版編輯器（per-race cert_layout 覆寫）：只存「被改過」的 key，缺項/整個 key 不存
  // 皆 fallback CERT_DEFAULT_LAYOUT（見 '@/lib/certificate'）。拖曳/方向鍵/數字輸入三種互動共用
  // updateCertLayout 這一個寫入點。
  const [certLayout, setCertLayout] = useState<Record<string, Partial<CertElementLayout>>>(
    initial?.config?.cert_layout ?? {}
  )
  const [certSelectedKey, setCertSelectedKey] = useState<string | null>(null)
  const certOverlayRef = useRef<HTMLDivElement>(null)
  // 排版編輯器改「另開全屏 modal」（v0.1.578）：表單內預覽改純顯示防誤觸；互動全部搬進 modal。
  // 開啟時快照 certLayout，「取消」/✕/Esc 一律還原快照丟棄本次調整，「確定」則保留（維持在 certLayout
  // state 上，仍需回表單按「儲存」才真正送出——與 buildCertLayoutPayload 的既有語意完全一致，
  // modal 只是換了互動的容器，不改變資料流）。certLayout 的每次寫入（updateCertLayout/resetCertElement/
  // resetAllCertLayout）皆是回傳新物件、不原地 mutate，故存純參照當快照即可，不需深拷貝。
  const [certEditorOpen, setCertEditorOpen] = useState(false)
  const certLayoutSnapshotRef = useRef<Record<string, Partial<CertElementLayout>>>({})
  // 完賽證明顯示開關：勾選＝前台不顯示完賽證明（一般模式）／完賽歷程（personal 模式）按鈕，
  // 後端 certificate／personal-history 端點同步擋（403，防繞過），語意比照下方 refundDisabled。
  const [certificateDisabled, setCertificateDisabled] = useState<boolean>(initial?.config?.certificate_disabled ?? false)
  const [bannerUrl, setBannerUrl] = useState(initial?.hero_image_url ?? '')
  const [bannerUploading, setBannerUploading] = useState(false)
  const [showDistanceRank, setShowDistanceRank] = useState(initial?.show_distance_rank ?? true)
  const [showTimeRank, setShowTimeRank] = useState(initial?.show_time_rank ?? true)
  const [vipOnly, setVipOnly] = useState<boolean>(initial?.vip_only ?? false)
  const [externalData, setExternalData] = useState<boolean>(initial?.external_data ?? false)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  // 不退費開關：勾選＝此活動不提供退費（玩家仍可申請取消釋出名額，但退費為 0；簡章不顯示退費規則）。
  const [refundDisabled, setRefundDisabled] = useState<boolean>(initial?.config?.refund_disabled ?? false)
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

  // 完賽證明模擬預覽：底圖或賽事名稱一變就重繪。示例資料固定用「王小明」+ 合理的假成績/名次，賽事名稱
  // 用當前表單值即時反映（未填時給個 fallback，避免預覽空白看不出效果）；沿用前台既有 renderCertificate
  // （無自訂底圖時它會自動退回系統預設設計，跟前台實際顯示邏輯完全一致，不用另外重繪一套）。
  useEffect(() => {
    let cancelled = false
    setCertPreviewLoading(true)
    renderCertificate({
      completed: true,
      race_title: title.trim() || '示例賽事',
      name: '王小明',
      group_name: '全程馬拉松組',
      target_km: 42.2,
      completed_km: 42.2,
      completion_at: new Date().toISOString(),
      total_time_s: 4 * 3600 + 32 * 60 + 18, // 4:32:18
      finish_rank: 3,
      finished_count: 128,
      race_ended: true,
      bg_url: certBgUrl || undefined,
    }, certLayout)
      .then((r) => {
        if (cancelled) return
        setCertPreviewImg(r.dataUrl)
      })
      .catch(() => {
        if (cancelled) return
        setCertPreviewImg('')
      })
      .finally(() => {
        if (!cancelled) setCertPreviewLoading(false)
      })
    return () => { cancelled = true }
  }, [certBgUrl, title, certLayout])

  // 排版編輯器 helpers：拖曳/方向鍵/數字輸入三種互動共用同一個寫入點——只寫「解析後的完整值」，
  // 避免局部覆寫堆疊出不一致的 partial 物件。
  function updateCertLayout(key: string, patch: Partial<CertElementLayout>) {
    setCertLayout((prev) => {
      const cur = resolveCertElementLayout(key, prev)
      return { ...prev, [key]: { ...cur, ...patch } }
    })
  }
  function resetCertElement(key: string) {
    setCertLayout((prev) => {
      const next = { ...prev }
      delete next[key]
      return next
    })
  }
  function resetAllCertLayout() {
    setCertLayout({})
  }
  function certPointToFraction(clientX: number, clientY: number): { x: number; y: number } | null {
    const rect = certOverlayRef.current?.getBoundingClientRect()
    if (!rect || rect.width === 0 || rect.height === 0) return null
    return {
      x: Math.min(1, Math.max(0, (clientX - rect.left) / rect.width)),
      y: Math.min(1, Math.max(0, (clientY - rect.top) / rect.height)),
    }
  }
  function handleCertPointerDown(key: string, e: React.PointerEvent) {
    e.preventDefault()
    setCertSelectedKey(key)
    try { (e.currentTarget as Element).setPointerCapture(e.pointerId) } catch { /* 部分環境不支援，仍可用方向鍵/數字輸入調整 */ }
  }
  function handleCertPointerMove(key: string, e: React.PointerEvent) {
    if (!(e.currentTarget as Element).hasPointerCapture?.(e.pointerId)) return
    const pt = certPointToFraction(e.clientX, e.clientY)
    if (!pt) return
    updateCertLayout(key, pt)
  }
  function handleCertPointerUp(e: React.PointerEvent) {
    try { (e.currentTarget as Element).releasePointerCapture(e.pointerId) } catch { /* 忽略 */ }
  }
  function handleCertKeyDown(key: string, e: React.KeyboardEvent) {
    const step = 0.005 // 每按一下微調 0.5%
    let dx = 0, dy = 0
    if (e.key === 'ArrowLeft') dx = -step
    else if (e.key === 'ArrowRight') dx = step
    else if (e.key === 'ArrowUp') dy = -step
    else if (e.key === 'ArrowDown') dy = step
    else return
    e.preventDefault()
    setCertSelectedKey(key)
    const cur = resolveCertElementLayout(key, certLayout)
    updateCertLayout(key, { x: Math.min(1, Math.max(0, cur.x + dx)), y: Math.min(1, Math.max(0, cur.y + dy)) })
  }
  // 送出前把 certLayout 收斂成「只含真正被改過的 key」（與預設值逐欄比對，避免浮點誤差誤判成有改），
  // 全部相同（或未編輯過）則整個欄位省略，讓 config JSON 保持精簡（比照 refund_disabled 的模式）。
  function buildCertLayoutPayload(): Record<string, CertElementLayout> | undefined {
    const out: Record<string, CertElementLayout> = {}
    for (const key of Object.keys(certLayout)) {
      const resolved = resolveCertElementLayout(key, certLayout)
      const def = CERT_DEFAULT_LAYOUT[key]
      const changed = !def
        || Math.abs(resolved.x - def.x) > 1e-6
        || Math.abs(resolved.y - def.y) > 1e-6
        || Math.abs(resolved.size - def.size) > 1e-6
      if (changed) out[key] = resolved
    }
    return Object.keys(out).length > 0 ? out : undefined
  }
  function openCertEditor() {
    if (!certBgUrl) return // 無底圖不開放排版（沿用既有限制：位置不開放自訂，見上方 renderCertificate 註解）
    certLayoutSnapshotRef.current = certLayout
    setCertSelectedKey(null)
    setCertEditorOpen(true)
  }
  // 「確定」：不還原快照，直接關閉——本次調整已經就是 certLayout state 本身，關閉 modal 即完成寫回表單 state。
  function confirmCertEditor() {
    setCertEditorOpen(false)
  }
  // 「取消」/✕/Esc/點背景共用：還原開啟當下的快照，丟棄 modal 內做的所有調整，誤觸/誤拖不再有代價。
  function cancelCertEditor() {
    setCertLayout(certLayoutSnapshotRef.current)
    setCertSelectedKey(null)
    setCertEditorOpen(false)
  }
  useEffect(() => {
    if (!certEditorOpen) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') { e.preventDefault(); cancelCertEditor() }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [certEditorOpen])
  // 清除底圖：確認 dialog 防誤觸（見排查結論——鈕本身無保護、緊鄰高互動排版區，單次誤點就讓縮圖消失，
  // 是使用者回報「底圖消失」最可能的成因）。
  function handleRemoveCertBg() {
    if (!window.confirm('確定要清除底圖？清除後將改用系統預設證明設計（此動作在按下「儲存」前仍可用「取消」離開表單復原）。')) return
    setCertBgUrl('')
  }

  // 完賽證明排版編輯器（全屏 modal）：用「函式呼叫」而非獨立元件，比照上方 taskSection 的既有慣例——
  // 獨立元件在父層重渲染時可能被視為新的元件型別而整棵重新掛載，導致 X/Y/字級輸入框失焦；用函式呼叫
  // 則沿用同一個 render tree，狀態與焦點都不受影響。互動邏輯（拖曳/方向鍵/數字輸入）完全沿用既有的
  // handleCertPointerDown/Move/Up、handleCertKeyDown、updateCertLayout 等既有函式，只是換了外層容器。
  function certEditorModal() {
    return (
      <div
        style={certModalOverlay}
        onClick={cancelCertEditor}
        role="dialog"
        aria-modal="true"
        aria-label="編輯完賽證明排版"
      >
        <div style={certModalPanel} onClick={(e) => e.stopPropagation()}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '14px 18px', borderBottom: '1px solid var(--line-2)' }}>
            <div style={{ fontWeight: 800, fontSize: 16 }}>編輯完賽證明排版</div>
            <button type="button" onClick={cancelCertEditor} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 20, lineHeight: 1, padding: 4 }}>✕</button>
          </div>

          <div style={{ flex: 1, minHeight: 0, display: 'flex' }}>
            {/* 大尺寸預覽：畫面允許下盡量大，maxWidth 900 + aspect-ratio 維持 1240×877 畫布比例 */}
            <div style={{ flex: '1 1 auto', minWidth: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', overflow: 'auto', padding: 20 }}>
              {certPreviewImg ? (
                <div style={{ position: 'relative', width: '100%', maxWidth: 900, aspectRatio: '1240 / 877' }}>
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={certPreviewImg}
                    alt="完賽證明編輯預覽"
                    draggable={false}
                    style={{ width: '100%', height: '100%', display: 'block', borderRadius: 10, border: '1px solid var(--line-2)', objectFit: 'contain', pointerEvents: 'none', userSelect: 'none' }}
                  />
                  {/* 熱區座標直接用 layout 的 x/y 比例(0-1)換算成 %，與 renderCertificate 換算成 canvas px
                      的方式相同，故拖曳結果與實際渲染完全對位。 */}
                  <div ref={certOverlayRef} style={{ position: 'absolute', inset: 0 }}>
                    {CERT_ELEMENT_KEYS.map((key) => {
                      const l = resolveCertElementLayout(key, certLayout)
                      const def = CERT_DEFAULT_LAYOUT[key]
                      const box = CERT_HITBOX[key]
                      const hRatio = def?.size ? l.size / def.size : 1
                      const selected = certSelectedKey === key
                      return (
                        <div
                          key={key}
                          role="button"
                          tabIndex={0}
                          aria-label={`拖曳調整「${CERT_ELEMENT_LABELS[key]}」位置`}
                          onPointerDown={(e) => handleCertPointerDown(key, e)}
                          onPointerMove={(e) => handleCertPointerMove(key, e)}
                          onPointerUp={handleCertPointerUp}
                          onPointerCancel={handleCertPointerUp}
                          onKeyDown={(e) => handleCertKeyDown(key, e)}
                          onClick={() => setCertSelectedKey(key)}
                          style={{
                            position: 'absolute',
                            left: `${l.x * 100}%`,
                            top: `${l.y * 100}%`,
                            width: `${(box.w / 1240) * 100}%`,
                            height: `${(box.h * hRatio / 877) * 100}%`,
                            transform: 'translate(-50%, -50%)',
                            border: selected ? '2px solid #46E3A0' : '1px dashed rgba(255,255,255,.55)',
                            background: selected ? 'rgba(70,227,160,.16)' : 'rgba(0,0,0,.02)',
                            borderRadius: 4,
                            cursor: 'move',
                            touchAction: 'none',
                            outline: 'none',
                          }}
                        >
                          <span
                            style={{
                              position: 'absolute', top: -18, left: 0, fontSize: 11, color: '#fff',
                              background: 'rgba(0,0,0,.6)', padding: '2px 5px', borderRadius: 3, whiteSpace: 'nowrap',
                            }}
                          >
                            {CERT_ELEMENT_LABELS[key]}
                          </span>
                        </div>
                      )
                    })}
                  </div>
                </div>
              ) : (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>{certPreviewLoading ? '產生預覽中…' : '尚無預覽'}</div>
              )}
            </div>

            {/* 側欄：元素清單（點選切換）＋ X/Y/字級輸入 ＋ 單元素還原 ＋ 全部還原 */}
            <div style={{ width: 260, flex: '0 0 260px', borderLeft: '1px solid var(--line-2)', padding: 16, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div>
                <div style={{ fontSize: 11, letterSpacing: '.08em', color: 'var(--tx-faint)', marginBottom: 8, textTransform: 'uppercase' }}>元素清單</div>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                  {CERT_ELEMENT_KEYS.map((key) => (
                    <button
                      key={key}
                      type="button"
                      onClick={() => setCertSelectedKey(key)}
                      style={{
                        textAlign: 'left', padding: '8px 10px', borderRadius: 8, cursor: 'pointer', fontSize: 13,
                        border: certSelectedKey === key ? '1px solid #46E3A0' : '1px solid var(--line-2)',
                        background: certSelectedKey === key ? 'rgba(70,227,160,.12)' : 'var(--bg-2)',
                        color: 'var(--tx)',
                      }}
                    >
                      {CERT_ELEMENT_LABELS[key]}
                    </button>
                  ))}
                </div>
              </div>

              {certSelectedKey ? (
                <div style={{ padding: 10, border: '1px solid var(--line-2)', borderRadius: 8, background: 'var(--bg-2)' }}>
                  <div style={{ fontSize: 12, fontWeight: 700, marginBottom: 8 }}>
                    編輯元素：{CERT_ELEMENT_LABELS[certSelectedKey]}
                  </div>
                  <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'flex-end' }}>
                    <label style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                      X %
                      <input
                        type="number" step={0.1}
                        value={+(resolveCertElementLayout(certSelectedKey, certLayout).x * 100).toFixed(1)}
                        onChange={(e) => updateCertLayout(certSelectedKey, { x: Math.min(1, Math.max(0, (parseFloat(e.target.value) || 0) / 100)) })}
                        style={{ ...inp, width: 76, marginTop: 4 }}
                      />
                    </label>
                    <label style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                      Y %
                      <input
                        type="number" step={0.1}
                        value={+(resolveCertElementLayout(certSelectedKey, certLayout).y * 100).toFixed(1)}
                        onChange={(e) => updateCertLayout(certSelectedKey, { y: Math.min(1, Math.max(0, (parseFloat(e.target.value) || 0) / 100)) })}
                        style={{ ...inp, width: 76, marginTop: 4 }}
                      />
                    </label>
                    <label style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                      字級 px
                      <input
                        type="number" step={1} min={8}
                        value={Math.round(resolveCertElementLayout(certSelectedKey, certLayout).size)}
                        onChange={(e) => updateCertLayout(certSelectedKey, { size: Math.max(8, parseFloat(e.target.value) || 8) })}
                        style={{ ...inp, width: 76, marginTop: 4 }}
                      />
                    </label>
                    <button type="button" style={ghostBtn} onClick={() => resetCertElement(certSelectedKey)}>還原此元素</button>
                  </div>
                </div>
              ) : (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>
                  點選左側清單或預覽圖上的虛線框可拖曳調整位置；選取後可用方向鍵微調（每按 0.5%），或於出現的欄位輸入精確數值。
                </div>
              )}

              <button type="button" style={ghostBtn} onClick={resetAllCertLayout}>全部還原預設排版</button>
            </div>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, padding: '12px 18px', borderTop: '1px solid var(--line-2)', flexWrap: 'wrap' }}>
            <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
              「確定」套用本次調整到表單；仍需回到賽事表單按「儲存」才會真正寫入。「取消」／✕／Esc／點擊背景會捨棄本次在此畫面內的調整。
            </span>
            <div style={{ display: 'flex', gap: 10 }}>
              <button type="button" style={ghostBtn} onClick={cancelCertEditor}>取消</button>
              <button type="button" style={primaryBtn} onClick={confirmCertEditor}>確定</button>
            </div>
          </div>
        </div>
      </div>
    )
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

  async function uploadAddonImage(i: number, file: File) {
    setUploadingKey(`addon-${i}`); setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, image_url: url } : x)))
    } catch (e: any) {
      setErr(e?.message || '圖片上傳失敗')
    } finally {
      setUploadingKey(null)
    }
  }

  async function uploadSupplyImage(i: number, file: File) {
    setUploadingKey(`supply-${i}`); setErr('')
    try {
      const { url } = await adminImagesApi.upload(token, file)
      setSupplies((ss) => ss.map((x, idx) => (idx === i ? { ...x, image_url: url } : x)))
    } catch (e: any) {
      setErr(e?.message || '圖片上傳失敗')
    } finally {
      setUploadingKey(null)
    }
  }

  useEffect(() => {
    adminPresetsApi.list(token).then((r) => setPresets(r.presets)).catch(() => {})
    adminTaskModulesApi.list(token).then((r) => setTaskModules(r.modules)).catch(() => {})
    adminRewardTemplatesApi.list(token).then((r) => setRewardTemplates(r.templates)).catch(() => {})
    adminRewardGroupsApi.list(token).then((r) => setRewardGroups(r.groups)).catch(() => {})
    adminEventCouponsApi.list(token).then((r) => setCouponDefs(r.defs)).catch(() => {})
    adminAppSettingsApi.list(token).then((r) => {
      const raw = r.settings?.['cancellation_policy']
      if (raw) {
        try {
          const parsed = JSON.parse(raw)
          setSystemDefaultPolicy(parsed)
          // 此賽事尚無覆寫時，把編輯器起始值帶成目前系統預設，使用者一旦切到「此賽事自訂」看到的是合理起點而非內建預設。
          if (!initial?.config?.cancellation_policy) setCancelPolicy(parsed)
        } catch {
          /* 壞資料時維持內建預設，不擋表單載入 */
        }
      }
      // 即時獎勵期望值試算器用：VIP 活動優惠券面額（分），供「對比報名費」面板換算「扣VIP券後」金額。
      const couponRaw = r.settings?.['vip_coupon_value_cents']
      if (couponRaw != null && couponRaw !== '') {
        const n = parseInt(couponRaw, 10)
        if (Number.isFinite(n) && n >= 0) setVipCouponValueCents(n)
      }
    }).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  useEffect(() => {
    if (!slugTouched) setSlug(slugify(title))
  }, [title, slugTouched])

  // 個人挑戰模式無分組頁籤（後端自動維護隱藏預設分組）；切到此模式時若還停在分組頁籤，跳回基本頁籤。
  useEffect(() => {
    if (mode === 'personal' && tab === 'groups') setTab('basic')
  }, [mode, tab])

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

  // 挑戰規則是否已完整填妥（依 completion_type 決定必填欄位；與後端 ChallengeRule.Validate 對齊）
  function challengeRuleValid(): boolean {
    const num = (s: string) => parseFloat(s || '0') || 0
    if (completionType === 'streak_days') return num(chDays) > 0 && num(chMinKmPerDay) > 0
    if (completionType === 'window_cumulative') return num(chWindowDays) > 0 && num(chCumKm) > 0
    if (completionType === 'single_distance') return num(chSingleKm) > 0
    return false
  }
  function buildChallengeRule(): ChallengeRule {
    const num = (s: string) => parseFloat(s || '0') || 0
    return {
      completion_type: completionType,
      days: completionType === 'streak_days' ? Math.round(num(chDays)) : undefined,
      min_km_per_day: completionType === 'streak_days' ? num(chMinKmPerDay) : undefined,
      daily_mode: completionType === 'streak_days' ? chDailyMode : undefined,
      window_days: completionType === 'window_cumulative' ? Math.round(num(chWindowDays)) : undefined,
      cum_km: completionType === 'window_cumulative' ? num(chCumKm) : undefined,
      single_km:
        completionType === 'window_cumulative' || completionType === 'single_distance'
          ? num(chSingleKm)
          : undefined,
    }
  }

  // 即時獎勵設定 helpers（活動獎勵系統 P2；比照上面 challenge_rule 的驗證/建構模式）
  function addRewardItem() {
    setRewardItems((its) => [...its, { type: 'exp', min: 0, max: 0, prob_bp: 10000 }])
  }
  function updateRewardItem(i: number, patch: Partial<RewardItem>) {
    setRewardItems((its) => its.map((it, idx) => (idx === i ? { ...it, ...patch } : it)))
  }
  function removeRewardItem(i: number) {
    setRewardItems((its) => its.filter((_, idx) => idx !== i))
  }
  // 參賽虛擬獎勵設定 helpers（migration 140；比照上面即時獎勵的建構/移除模式，獨立一組陣列狀態，不共用
  // 模板套用/存模板功能——那是即時獎勵既有的模板庫，兩者用途不同不混用）。
  function addEntryRewardItem() {
    setEntryRewardItems((its) => [...its, { type: 'exp', min: 0, max: 0, prob_bp: 10000 }])
  }
  function updateEntryRewardItem(i: number, patch: Partial<RewardItem>) {
    setEntryRewardItems((its) => its.map((it, idx) => (idx === i ? { ...it, ...patch } : it)))
  }
  function removeEntryRewardItem(i: number) {
    setEntryRewardItems((its) => its.filter((_, idx) => idx !== i))
  }
  function applyRewardTemplate(templateId: string) {
    const t = rewardTemplates.find((x) => x.id === templateId)
    if (!t) return
    if (rewardItems.length > 0 && !confirm(`套用模板「${t.name}」將覆蓋目前已設定的 ${rewardItems.length} 個獎勵項目，確定套用？`)) return
    const availableGroupIds = new Set(availableRewardGroups.map((g) => g.id))
    setRewardItems(t.items.map((it) => {
      if (it.type === 'coupon') {
        // 模板存的券種可能事後被停用/過期——套用時只保留當下仍可選的，否則清空讓管理者重選
        // （比照下方 serial 的處理方式）。
        const coupon_def_id = it.coupon_def_id && availableCouponDefIds.has(it.coupon_def_id) ? it.coupon_def_id : undefined
        return { ...it, coupon_def_id }
      }
      if (it.type !== 'serial') return { ...it }
      // 模板可能是在別場活動套用/儲存時存的，denominations／serial_group_id 裡的序號組不一定對應本場
      // 活動——剔除本場用不到的組，避免套用後殘留他場專屬序號組仍通過驗證、存檔後真的把他場序號發出去。
      const denominations = (it.denominations ?? []).filter((d) => availableGroupIds.has(d.group_id))
      const serial_group_id = it.serial_group_id && availableGroupIds.has(it.serial_group_id) ? it.serial_group_id : undefined
      return { ...it, denominations, serial_group_id }
    }))
  }
  async function saveCurrentAsRewardTemplate() {
    if (rewardItems.length === 0) return
    const name = prompt('模板名稱：')
    if (!name || !name.trim()) return
    try {
      const { template } = await adminRewardTemplatesApi.create(token, { name: name.trim(), items: rewardItems })
      setRewardTemplates((ts) => [template, ...ts])
    } catch (e: any) {
      setErr(e?.message || '儲存模板失敗')
    }
  }

  // 即時獎勵「期望值試算」彙總面板（區塊下方，摺疊區塊）：加總所有 serial 項目中 LINE POINTS 面額的
  // 期望成本，換算每位完賽者／報名者期望成本，並與報名費（uniform 一行／per_group 逐組）對比顯示淨收與
  // 期望虧損。純顯示、不影響送出資料；計算邏輯全部委由下方純函式（見「小元件」區塊 computeSerialDenomCalcs
  // 等），與 SerialDenomFields/RewardItemRow 內的 inline 即時顯示共用同一套算法，避免兩處分岔。
  function renderRewardExpectedValuePanel() {
    const { rows, nonPointCount } = computeSerialDenomCalcs(rewardItems, availableRewardGroups)
    const perFinisherPoints = rows.reduce((s, r) => s + r.expectedPoints, 0)
    const perFinisherCostNtd = pointsToCost(perFinisherPoints, pointCostNtd)
    const perRegistrantCostNtd = perFinisherCostNtd * (expectedFinishRatePct / 100)

    // 報名費對比行：uniform 顯示一行；per_group 逐組顯示，各組取「有效報名費」——組自訂 entry_fee_cents，
    // 未設定則沿用賽事預設 entryFeeNtd（比照 lib/api.ts effectiveGroupFee 的 fallback 規則，這裡直接用
    // 表單目前的即時輸入值而非已存檔的 initial，讓試算隨表單編輯即時反映）。
    const uniformFeeCents = Math.round((parseFloat(entryFeeNtd || '0') || 0) * 100)
    const feeRows: { label: string; feeCents: number }[] = feeMode === 'per_group'
      ? groups.filter((g) => g.name.trim()).map((g) => ({ label: g.name, feeCents: g.entry_fee_cents ?? uniformFeeCents }))
      : [{ label: '', feeCents: uniformFeeCents }]
    const couponValueNtd = vipCouponValueCents / 100

    return (
      <details style={{ marginTop: 8, border: '1px solid var(--line-2)', borderRadius: 10, padding: '8px 12px', background: 'var(--bg-1, #11131a)' }}>
        <summary style={{ fontSize: 12.5, fontWeight: 700, color: 'var(--tx-dim)', cursor: 'pointer' }}>💰 期望值試算</summary>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 10 }}>
          <Row>
            <Field label="LINE POINTS 單點成本（元，可調）">
              <input
                style={inp} type="number" min={0} step="0.01" value={pointCostNtd}
                onChange={(e) => setPointCostNtd(Math.max(0, parseFloat(e.target.value || '0') || 0))}
              />
            </Field>
            <Field label="信用卡刷卡手續費率（%，可調）">
              <input
                style={inp} type="number" min={0} max={100} step="0.01" value={cardFeeRatePct}
                onChange={(e) => setCardFeeRatePct(Math.max(0, parseFloat(e.target.value || '0') || 0))}
              />
            </Field>
            <Field label="預估完賽率（%）">
              <input
                style={inp} type="number" min={0} max={100} step="1" value={expectedFinishRatePct}
                onChange={(e) => setExpectedFinishRatePct(Math.max(0, Math.min(100, parseFloat(e.target.value || '0') || 0)))}
              />
            </Field>
          </Row>
          <div style={calcHint}>
            單點成本預設 1.3 元（可調）；刷卡手續費預設 2.35%（可調）；VIP 活動優惠券面額讀取系統設定，目前 NT$ {couponValueNtd}。
          </div>

          {rows.length === 0 && nonPointCount === 0 && (
            <div style={calcHint}>目前即時獎勵設定中尚無 LINE POINTS 序號面額。</div>
          )}
          {rows.map((r, idx) => (
            <div key={idx} style={{ display: 'flex', justifyContent: 'space-between', gap: 10, fontSize: 12.5, flexWrap: 'wrap' }}>
              <span>{r.groupName}{r.pointValue != null ? `　面額值 ${r.pointValue}（自名稱解析）` : ''}</span>
              {r.pointValue != null
                ? <span>{r.pointValue} × {r.grantCount} 枚 × {(r.actualProb * 100).toFixed(2)}% ＝ {r.expectedPoints.toFixed(2)} 點</span>
                : <span style={{ color: 'var(--hunt)' }}>⚠ 無法從名稱解析面額，請在序號組名稱中包含點數數字</span>}
            </div>
          ))}
          {nonPointCount > 0 && <div style={calcHint}>未計入：非點數類序號 {nonPointCount} 項</div>}

          <div style={{ borderTop: '1px dashed var(--line-2)', paddingTop: 6, fontSize: 13, fontWeight: 700 }}>
            每位完賽者期望成本 NT$ {perFinisherCostNtd.toFixed(1)}（{perFinisherPoints.toFixed(2)} 點 × {pointCostNtd} 元/點）
          </div>
          <div style={{ fontSize: 12.5 }}>每位報名者期望成本 NT$ {perRegistrantCostNtd.toFixed(1)}（完賽率 {expectedFinishRatePct}%）</div>

          <div style={{ borderTop: '1px dashed var(--line-2)', paddingTop: 6, display: 'flex', flexDirection: 'column', gap: 6, fontSize: 12.5 }}>
            {feeRows.map((fr, idx) => {
              const feeNtd = fr.feeCents / 100
              if (feeNtd <= 0) {
                return (
                  <div key={idx}>
                    {fr.label ? `${fr.label}｜` : ''}免費活動：期望成本 NT$ {perRegistrantCostNtd.toFixed(1)}（純行銷成本）
                  </div>
                )
              }
              const { afterCoupon, net } = netProceeds(feeNtd, couponValueNtd, cardFeeRatePct)
              const ratioPct = net > 0 ? (perRegistrantCostNtd / net) * 100 : null
              const loss = perRegistrantCostNtd > net
              return (
                <div key={idx} style={{ color: loss ? 'var(--hunt)' : 'var(--tx)' }}>
                  {fr.label ? `${fr.label}｜` : ''}
                  報名費 NT$ {feeNtd.toFixed(0)}｜扣VIP券 NT$ {afterCoupon.toFixed(0)}｜手續費後淨收 NT$ {net.toFixed(1)}｜
                  期望成本 NT$ {perRegistrantCostNtd.toFixed(1)}（佔淨收 {ratioPct != null ? ratioPct.toFixed(0) + '%' : '—'}）
                  {loss && <> ⚠ 每單期望虧損 NT$ {(perRegistrantCostNtd - net).toFixed(1)}</>}
                </div>
              )
            })}
          </div>
        </div>
      </details>
    )
  }

  // serial 類「有效商家 id」：優先用新格式 merchant_id；舊資料只有 serial_group_id 時，從序號組反查其
  // 所屬商家（向後相容，讓改版前設定過的賽事在編輯畫面仍能正確判斷/顯示，不必逼使用者重填）。
  function serialEffectiveMerchantId(it: RewardItem): string {
    if (it.merchant_id) return it.merchant_id
    if (it.serial_group_id) {
      const g = rewardGroups.find((x) => x.id === it.serial_group_id)
      if (g?.merchant_id) return g.merchant_id
    }
    return ''
  }
  // 獎勵項目是否皆已填妥（有填才檢查；整組選填，空陣列合法）；serial 類另外要求已選商家、至少一個
  // 面額權重>0（或舊格式 serial_group_id），面額的序號組必須都屬於本場活動可用範圍（見
  // rewardItemsForeignGroupError，這裡是最後一道防線），且同一份設定內不可重複同一商家（後台防呆，見設計文件）。
  // 抽成通用函式（items 參數化）供即時獎勵(rewardItems)與參賽虛擬獎勵(entryRewardItems，migration 140)
  // 兩組獨立狀態共用同一套驗證規則；各自呼叫時各自算 seenMerchants，兩組設定互不影響彼此的重複商家判斷。
  function validateRewardItems(items: RewardItem[]): boolean {
    const availableGroupIds = new Set(availableRewardGroups.map((g) => g.id))
    const seenMerchants = new Set<string>()
    return items.every((it) => {
      if (!(it.prob_bp > 0 && it.prob_bp <= 10000)) return false
      if (it.type === 'exp' || it.type === 'dp' || it.type === 'gp') return (it.min ?? 0) > 0 && (it.max ?? 0) >= (it.min ?? 0)
      if (it.type === 'vip') return (it.days ?? 0) > 0
      if (it.type === 'serial') {
        const denoms = it.denominations ?? []
        const hasDenom = denoms.some((d) => d.group_id && d.weight > 0)
        if (!hasDenom && !it.serial_group_id) return false
        if (denoms.some((d) => d.weight > 0 && !availableGroupIds.has(d.group_id))) return false
        if (!hasDenom && it.serial_group_id && !availableGroupIds.has(it.serial_group_id)) return false
        const mid = serialEffectiveMerchantId(it)
        if (!mid || seenMerchants.has(mid)) return false
        seenMerchants.add(mid)
        return true
      }
      if (it.type === 'coupon') {
        // 下拉只列出「當下可選」的券種（見 availableCouponDefs），故非空即代表選到合法選項；
        // 仍保留這道檢查作為最後防線（例如券種在編輯期間被後台其他分頁停用/改過期）。
        return !!it.coupon_def_id && availableCouponDefIds.has(it.coupon_def_id)
      }
      return false
    })
  }
  function rewardItemsValid(): boolean {
    return validateRewardItems(rewardItems)
  }
  function entryRewardItemsValid(): boolean {
    return validateRewardItems(entryRewardItems)
  }
  // serial 項目是否含有「不屬於本場活動可用序號組」的面額（例如套用了別場專屬的模板但沒被
  // applyRewardTemplate 剔除乾淨、或資料被外部工具直接改過）；回傳說明文字供 submit() 顯示明確錯誤，
  // null 代表沒有這個問題。抽成通用函式（items+label 參數化），供即時獎勵與參賽虛擬獎勵共用。
  function foreignGroupError(items: RewardItem[], label: string): string | null {
    const availableGroupIds = new Set(availableRewardGroups.map((g) => g.id))
    const hasForeign = items.some((it) => {
      if (it.type !== 'serial') return false
      const denoms = it.denominations ?? []
      if (denoms.some((d) => d.weight > 0 && !availableGroupIds.has(d.group_id))) return true
      if (!denoms.length && it.serial_group_id && !availableGroupIds.has(it.serial_group_id)) return true
      return false
    })
    return hasForeign ? `${label}設定中有序號組不屬於本場活動可選範圍（可能是套用了其他活動的模板殘留），請重新選擇面額後再儲存。` : null
  }
  function rewardItemsForeignGroupError(): string | null {
    return foreignGroupError(rewardItems, '即時獎勵')
  }
  function entryRewardItemsForeignGroupError(): string | null {
    return foreignGroupError(entryRewardItems, '參賽虛擬獎勵')
  }
  // 本場活動可用的序號組：對應全部活動、或（編輯中）已明確勾選對應本場活動者
  const availableRewardGroups = rewardGroups.filter((g) => g.applies_all_races || (!!initial?.id && g.race_ids.includes(initial.id)))
  // 「當下可選」的活動優惠券券種：需啟用，且 fixed(指定到期日)模式未過期（days 模式無到期日概念，
  // 一律視為可選）——即「設定時就不給選」已停用/已過期券種的把關點（見 memory activity-reward-system）。
  const availableCouponDefs = couponDefs.filter((d) => d.enabled && (d.expiry_mode !== 'fixed' || !d.expires_at || new Date(d.expires_at).getTime() > Date.now()))
  const availableCouponDefIds = new Set(availableCouponDefs.map((d) => d.id))

  function buildPayload(): CreateRacePayload {
    // uniform 模式下分組費用欄位隱藏、送出一律不帶（強制清成 null，避免切換模式後殘留舊值誤套用）
    const cleanGroups: RaceGroup[] = groups
      .filter((g) => g.name.trim())
      .map((g, idx) => ({
        ...g, display_order: idx,
        entry_fee_cents: feeMode === 'per_group' ? (g.entry_fee_cents ?? null) : null,
      }))

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
      external_data: externalData,
      challenge_rule: mode === 'personal' ? buildChallengeRule() : null,
      // 即時獎勵設定一般化（migration 134）：不再限 personal 模式，其餘模式完成任一「個人額外挑戰」
      // (group_individual scope 任務) 觸發（見後端 progress.go MarkRaceTaskCompletedAndGrant）。
      reward_config: rewardItems.length > 0 ? { items: rewardItems } : null,
      // 參賽虛擬獎勵設定（migration 140）：賽事開始後由後端排程自動發給所有已報名(paid)者，不看任務條件
      // （見後端 entry_reward_schedule.go RunEntryRewardLoop）。
      entry_reward_config: entryRewardItems.length > 0 ? { items: entryRewardItems } : null,
      // config 是整包 JSONB struct marshal（非合併寫入）：務必以既有 config 為底、只覆寫 cancellation_policy，
      // 否則會把 factions/clubs/missions 等既有欄位一併清空（見後端 configToBytes/bytesToConfig 註解）。
      config: {
        ...(initial?.config ?? {}),
        // 不退費開關：false 時送 undefined（JSON 序列化會整個略過該 key），保持 config 乾淨且可清掉舊值。
        refund_disabled: refundDisabled || undefined,
        // 完賽證明顯示開關：同上，false 時送 undefined。
        certificate_disabled: certificateDisabled || undefined,
        // 完賽證明可視化排版覆寫：只送「真的被改過（≠模板預設）」的元素，全部未改則整欄位省略
        // （見 buildCertLayoutPayload；未設定的元素前後台一律 fallback CERT_DEFAULT_LAYOUT）。
        cert_layout: buildCertLayoutPayload(),
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
          content: b.block_type === 'image' ? JSON.stringify(finalizeImageItems(imagesOf(b.content))) : b.content.trim(),
          display_order: idx,
        })),
      entry_fee: Math.round(parseFloat(entryFeeNtd || '0') * 100),
      fee_mode: feeMode,
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
    if (mode === 'personal' && !challengeRuleValid()) {
      setErr('請完整填寫個人挑戰規則參數')
      setTab('basic')
      return
    }
    {
      // 即時獎勵設定驗證一般化（migration 134）：不再限 personal 模式。
      const foreignGroupErr = rewardItemsForeignGroupError()
      if (foreignGroupErr) {
        setErr(foreignGroupErr)
        setTab('basic')
        return
      }
      if (!rewardItemsValid()) {
        setErr('請完整填寫即時獎勵設定的機率／數值／序號組')
        setTab('basic')
        return
      }
    }
    {
      // 參賽虛擬獎勵設定驗證（migration 140）：共用同一套規則，但錯誤時切到「物資」分頁（該設定區塊位置）。
      const entryForeignGroupErr = entryRewardItemsForeignGroupError()
      if (entryForeignGroupErr) {
        setErr(entryForeignGroupErr)
        setTab('supplies')
        return
      }
      if (!entryRewardItemsValid()) {
        setErr('請完整填寫參賽虛擬獎勵設定的機率／數值／序號組')
        setTab('supplies')
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
          ...(mode === 'personal' ? [] : [['groups', `分組 (${groups.filter((g) => g.name.trim()).length})`]]),
          ['addons', `加購 (${addons.filter((a) => a.name.trim()).length})`],
          ['supplies', `物資 (${supplies.filter((s) => s.name.trim()).length})`],
          ['brochure', `簡章 (${brochure.filter(blockHasContent).length})`],
          ['tasks', `任務 (${tasks.filter(taskComplete).length})`],
          ['cancel', `取消退費${refundDisabled ? ' ・不退費' : cancelFollowDefault ? '' : ' ・自訂'}`],
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
            {/* 廣告投放網址：依目前 slug 即時組出，方便直接複製貼給投放渠道（見 /event/{slug} 落地頁） */}
            {slug ? (
              <div style={hint}>廣告投放網址：https://www.dor.tw/event/{slug}</div>
            ) : (
              <div style={hint}>填入 slug 後會產生投放網址</div>
            )}
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
              <Field label="報名費模式">
                <select style={inp} value={feeMode} onChange={(e) => setFeeMode(e.target.value as 'uniform' | 'per_group')}>
                  <option value="uniform">全場統一報名費</option>
                  <option value="per_group">各組獨立報名費</option>
                </select>
              </Field>
              <Field label={feeMode === 'per_group' ? '預設報名費（未獨立設定的組別、前台新增組別適用）' : '報名費 (NT$)'}>
                <input style={inp} type="number" value={entryFeeNtd} onChange={(e) => setEntryFeeNtd(e.target.value)} />
              </Field>
            </Row>
            {feeMode === 'per_group' && (
              <div style={hint}>各組獨立報名費請至「分組」分頁逐組設定；留空的組別（含前台跑團成員新增的組別）自動套用上方預設報名費。</div>
            )}
            <Row>
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

            {mode === 'personal' && (
              <div style={{ ...card, background: 'var(--bg-2)' }}>
                <div style={{ fontWeight: 700, fontSize: 13.5 }}>挑戰規則</div>
                <div style={hint}>
                  個人挑戰模式：玩家可重複報名挑戰（每次各收一次報名費），符合以下規則即完成一次、可再報名再挑戰。
                </div>
                <Field label="完成條件類型">
                  <select style={inp} value={completionType} onChange={(e) => setCompletionType(e.target.value as CompletionType)}>
                    {COMPLETION_TYPES.map((c) => (
                      <option key={c.v} value={c.v}>{c.t}</option>
                    ))}
                  </select>
                </Field>
                {completionType === 'streak_days' && (
                  <>
                    <Row>
                      <Field label="連續天數 (天)">
                        <input style={inp} type="number" min={1} value={chDays} onChange={(e) => setChDays(e.target.value)} />
                      </Field>
                      <Field label="每天最低里程 (km)">
                        <input style={inp} type="number" min={0} step="0.1" value={chMinKmPerDay} onChange={(e) => setChMinKmPerDay(e.target.value)} />
                      </Field>
                    </Row>
                    <Field label="每天達標方式">
                      <select style={inp} value={chDailyMode} onChange={(e) => setChDailyMode(e.target.value as 'cumulative' | 'single')}>
                        <option value="cumulative">累積（當日所有跑步里程加總達標，適合初學者）</option>
                        <option value="single">單次（當日需有一趟跑步達到該里程）</option>
                      </select>
                    </Field>
                  </>
                )}
                {completionType === 'window_cumulative' && (
                  <>
                    <Row>
                      <Field label="視窗天數 (天)">
                        <input style={inp} type="number" min={1} value={chWindowDays} onChange={(e) => setChWindowDays(e.target.value)} />
                      </Field>
                      <Field label="視窗內累積里程 (km)">
                        <input style={inp} type="number" min={0} step="0.1" value={chCumKm} onChange={(e) => setChCumKm(e.target.value)} />
                      </Field>
                    </Row>
                    <Field label="至少一趟里程 (km，選填，留空或 0＝不限)">
                      <input style={inp} type="number" min={0} step="0.1" value={chSingleKm} onChange={(e) => setChSingleKm(e.target.value)} />
                    </Field>
                  </>
                )}
                {completionType === 'single_distance' && (
                  <Field label="單趟里程 (km)">
                    <input style={inp} type="number" min={0} step="0.1" value={chSingleKm} onChange={(e) => setChSingleKm(e.target.value)} />
                  </Field>
                )}
              </div>
            )}

            {(
              <div style={{ ...card, background: 'var(--bg-2)' }}>
                <div style={{ fontWeight: 700, fontSize: 13.5 }}>即時獎勵設定（選填）</div>
                <div style={hint}>
                  {mode === 'personal'
                    ? '完成一次挑戰即觸發抽獎，每個項目獨立判定機率（可同時中多項）。'
                    : '完成任一「個人額外挑戰」（任務頁籤中分組個人額外挑戰任務）即觸發抽獎，每個項目獨立判定機率（可同時中多項）；同一使用者同一任務只會觸發一次。'}
                  經濟類（EXP/DP/GP/VIP）中獎直接入帳；
                  序號類為「以商家為單位的兩層抽獎」：先判定該商家中不中獎，中了才在該商家旗下有庫存的面額中依權重抽一組
                  配發進玩家活動獎勵錢包；商家旗下面額當下全數缺貨則該項跳過、不影響其他項目，同一份設定不可重複選同一商家。
                  活動優惠券類中獎直接發一張進玩家活動獎勵錢包，報名時可折抵報名費（一次最多用 1 張，與其他折抵方式互斥）；
                  下拉只列出目前啟用且未過期的券種，請先到「活動優惠券管理」建立。
                </div>
                <Row>
                  {rewardTemplates.length > 0 && (
                    <Field label="套用模板（載入後可再微調，不會持續同步）">
                      <select
                        style={inp} value=""
                        onChange={(e) => { if (e.target.value) { applyRewardTemplate(e.target.value); e.target.value = '' } }}
                      >
                        <option value="">選擇模板套用…</option>
                        {rewardTemplates.map((t) => <option key={t.id} value={t.id}>{t.name}（{t.items.length} 項）</option>)}
                      </select>
                    </Field>
                  )}
                  <div style={{ display: 'flex', alignItems: 'flex-end' }}>
                    <button type="button" onClick={saveCurrentAsRewardTemplate} disabled={rewardItems.length === 0} style={{ ...ghostBtn, opacity: rewardItems.length === 0 ? 0.5 : 1 }}>
                      另存為模板
                    </button>
                  </div>
                </Row>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 4 }}>
                  {rewardItems.map((it, i) => (
                    <RewardItemRow
                      key={i}
                      item={it}
                      groups={availableRewardGroups}
                      couponDefs={availableCouponDefs}
                      costPerPoint={pointCostNtd}
                      onChange={(patch) => updateRewardItem(i, patch)}
                      onRemove={() => removeRewardItem(i)}
                    />
                  ))}
                  {rewardItems.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚未設定即時獎勵</div>}
                </div>
                <button type="button" onClick={addRewardItem} style={{ ...ghostBtn, alignSelf: 'flex-start' }}>＋ 新增獎勵項目</button>
                {renderRewardExpectedValuePanel()}
              </div>
            )}

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
                VIP 專屬活動（所有人可見；非 VIP 報名時顯示提示、不可報名）
              </label>
              <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                預設關閉。勾選後此賽事所有人皆可見，但非 VIP 帳號點選報名會顯示「VIP專屬活動。」提示、無法完成報名；VIP 期間報名成功者即使日後 VIP 過期也不受影響。
              </span>
            </Field>

            <Field label="活動數據來源">
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, color: 'var(--tx)', paddingTop: 2 }}>
                <input type="checkbox" checked={externalData} onChange={(e) => setExternalData(e.target.checked)} />
                採用 Strava 等外部數據做排名/里程統計
              </label>
              <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                預設關閉＝只採計 App 內 GPS 跑步追蹤（符合 Strava 使用規範）。開啟後將把 Strava/Garmin/COROS 等外部同步數據一併計入本活動排名與里程統計。
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
                <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src={certBgUrl} alt="底圖" style={{ width: 140, height: 99, objectFit: 'cover', borderRadius: 8, border: '1px solid var(--line-2)' }} />
                  {/* 清除鈕：獨立點擊區域（與上方縮圖有明顯間距，且不與任何拖曳互動層相鄰——排版編輯器
                      已改獨立 modal，此處表單原位只剩純顯示，見下方預覽區）＋確認 dialog 防誤觸——
                      使用者曾回報「誤觸後底圖消失要重新上傳」，此鈕清空 certBgUrl 是唯一會讓縮圖立即消失
                      的路徑，加確認 dialog 是最直接的防呆（詳細排查結論見 commit message）。 */}
                  <button type="button" style={{ ...ghostBtn, color: 'var(--hunt)' }} onClick={handleRemoveCertBg}>移除底圖</button>
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
              {(certPreviewImg || certPreviewLoading) && (
                <div style={{ marginTop: 10 }}>
                  <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 6 }}>
                    模擬預覽（示例資料，僅供確認版面配置，不影響實際發放；純顯示，不會誤觸——排版調整請按下方「編輯排版」另開畫面）
                  </div>
                  {certPreviewImg ? (
                    <div style={{ position: 'relative', width: '100%', maxWidth: 420 }}>
                      {/* eslint-disable-next-line @next/next/no-img-element */}
                      <img
                        src={certPreviewImg}
                        alt="完賽證明模擬預覽"
                        draggable={false}
                        style={{ width: '100%', display: 'block', borderRadius: 10, border: '1px solid var(--line-2)', pointerEvents: 'none', userSelect: 'none' }}
                      />
                    </div>
                  ) : (
                    <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>產生預覽中…</div>
                  )}
                  <div style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                    <button
                      type="button"
                      onClick={openCertEditor}
                      disabled={!certBgUrl}
                      style={{ ...ghostBtn, opacity: certBgUrl ? 1 : 0.45, cursor: certBgUrl ? 'pointer' : 'not-allowed' }}
                    >
                      ✎ 編輯排版
                    </button>
                    <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
                      {certBgUrl
                        ? '開啟全屏編輯畫面，拖曳調整各資訊元素的位置與字級。'
                        : '上傳底圖後可編輯各資訊元素的位置與字級（無底圖用系統預設設計，位置不開放自訂）。'}
                    </span>
                  </div>
                </div>
              )}
              <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--tx)', fontWeight: 600, marginTop: 12 }}>
                <input type="checkbox" checked={certificateDisabled} onChange={(e) => setCertificateDisabled(e.target.checked)} />
                不顯示完賽證明
              </label>
              <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>
                勾選後前台不顯示完賽證明按鈕（個人挑戰模式則不顯示完賽歷程按鈕）。
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
                  {feeMode === 'per_group' && (
                    <Field label="報名費 (NT$，留空＝用預設)">
                      <input
                        style={inp} type="number"
                        value={g.entry_fee_cents == null ? '' : String(g.entry_fee_cents / 100)}
                        onChange={(e) => updateGroup(i, { entry_fee_cents: e.target.value === '' ? null : Math.round(parseFloat(e.target.value) * 100) })}
                      />
                    </Field>
                  )}
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
                  <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                    <input style={{ ...inp, flex: 1, minWidth: 160 }} value={a.image_url} onChange={(e) => setAddons((as) => as.map((x, idx) => (idx === i ? { ...x, image_url: e.target.value } : x)))} />
                    <label style={{ ...ghostBtn, display: 'inline-flex', alignItems: 'center', whiteSpace: 'nowrap' }}>
                      {uploadingKey === `addon-${i}` ? '上傳中…' : '⬆ 上傳'}
                      <input type="file" accept="image/*" style={{ display: 'none' }}
                        onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadAddonImage(i, f); e.target.value = '' }} />
                    </label>
                    {a.image_url && (
                      // eslint-disable-next-line @next/next/no-img-element
                      <img src={a.image_url} alt="" style={{ width: 64, height: 48, objectFit: 'cover', borderRadius: 6, border: '1px solid var(--line-2)' }} />
                    )}
                  </div>
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
                    <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                      <input style={{ ...inp, flex: 1, minWidth: 160 }} value={s.image_url} onChange={(e) => setSupplies((ss) => ss.map((x, idx) => (idx === i ? { ...x, image_url: e.target.value } : x)))} />
                      <label style={{ ...ghostBtn, display: 'inline-flex', alignItems: 'center', whiteSpace: 'nowrap' }}>
                        {uploadingKey === `supply-${i}` ? '上傳中…' : '⬆ 上傳'}
                        <input type="file" accept="image/*" style={{ display: 'none' }}
                          onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadSupplyImage(i, f); e.target.value = '' }} />
                      </label>
                      {s.image_url && (
                        // eslint-disable-next-line @next/next/no-img-element
                        <img src={s.image_url} alt="" style={{ width: 64, height: 48, objectFit: 'cover', borderRadius: 6, border: '1px solid var(--line-2)' }} />
                      )}
                    </div>
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

            {/* 參賽虛擬獎勵（migration 140；選填）：重用即時獎勵的 RewardItemRow 編輯元件，但觸發條件
                完全不同——不看任務，賽事開始後由後端排程自動發放給所有已報名者（含開賽後才報名者），
                人人有獎場景請把機率設 100%。 */}
            <div style={{ ...card, background: 'var(--bg-2)' }}>
              <div style={{ fontWeight: 700, fontSize: 13.5 }}>參賽虛擬獎勵（選填）</div>
              <div style={hint}>
                賽事開始後自動發放給所有已報名者（含開賽後才報名者），不需完成任務。人人有獎請將機率設 100%。
                項目類型與抽獎規則與上方「即時獎勵設定」相同：經濟類（EXP/DP/GP/VIP）中獎直接入帳；
                序號類為以商家為單位的兩層抽獎；活動優惠券中獎直接發一張進玩家活動獎勵錢包。
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 4 }}>
                {entryRewardItems.map((it, i) => (
                  <RewardItemRow
                    key={i}
                    item={it}
                    groups={availableRewardGroups}
                    couponDefs={availableCouponDefs}
                    onChange={(patch) => updateEntryRewardItem(i, patch)}
                    onRemove={() => removeEntryRewardItem(i)}
                  />
                ))}
                {entryRewardItems.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚未設定參賽虛擬獎勵</div>}
              </div>
              <button type="button" onClick={addEntryRewardItem} style={{ ...ghostBtn, alignSelf: 'flex-start' }}>＋ 新增獎勵項目</button>
            </div>
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
                      <Field label="圖片（可多張；前台會左右滑動瀏覽；每張圖可各自選填說明文字＋點擊連結）">
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                          {imagesOf(b.content).map((item, k) => (
                            <div key={k} style={{ display: 'flex', flexDirection: 'column', gap: 6, padding: 8, border: '1px solid var(--line-2)', borderRadius: 8 }}>
                              <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                                <span style={{ fontSize: 11, color: 'var(--tx-faint)', width: 16 }}>{k + 1}</span>
                                <input style={{ ...inp, flex: 1, minWidth: 160 }} value={item.url}
                                  onChange={(e) => { const imgs = imagesOf(b.content); imgs[k] = { ...imgs[k], url: e.target.value }; setBlockImages(i, imgs) }}
                                  placeholder="https://… 或上傳" />
                                <label style={{ ...ghostBtn, display: 'inline-flex', alignItems: 'center', whiteSpace: 'nowrap' }}>
                                  {uploadingKey === `${i}-${k}` ? '上傳中…' : '⬆ 上傳'}
                                  <input type="file" accept="image/*" style={{ display: 'none' }}
                                    onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadImage(i, k, f); e.target.value = '' }} />
                                </label>
                                {item.url && (
                                  // eslint-disable-next-line @next/next/no-img-element
                                  <img src={item.url} alt="" style={{ width: 64, height: 48, objectFit: 'cover', borderRadius: 6, border: '1px solid var(--line-2)' }} />
                                )}
                                <button type="button" onClick={() => setBlockImages(i, imagesOf(b.content).filter((_, x) => x !== k))} style={{ ...linkBtn, color: 'var(--hunt)' }}>移除</button>
                              </div>
                              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', paddingLeft: 24 }}>
                                <input style={{ ...inp, flex: 1, minWidth: 160 }} value={item.caption ?? ''}
                                  onChange={(e) => { const imgs = imagesOf(b.content); imgs[k] = { ...imgs[k], caption: e.target.value }; setBlockImages(i, imgs) }}
                                  placeholder="圖片說明（選填，顯示在這張圖片下方）" />
                                <input style={{ ...inp, flex: 1, minWidth: 160 }} value={item.link ?? ''}
                                  onChange={(e) => { const imgs = imagesOf(b.content); imgs[k] = { ...imgs[k], link: e.target.value }; setBlockImages(i, imgs) }}
                                  placeholder="點擊連結（選填，https://… 或站內路徑 /xxx，點這張圖會導向此網址）" />
                              </div>
                            </div>
                          ))}
                          <button type="button" onClick={() => setBlockImages(i, [...imagesOf(b.content), { url: '' }])} style={ghostBtn}>＋ 新增圖片</button>
                        </div>
                      </Field>
                      <Field label="整組圖說（選填；顯示在整組圖片／輪播下方，非逐張說明）"><input style={inp} value={b.caption ?? ''} onChange={(e) => upd({ caption: e.target.value })} /></Field>
                    </>
                  )}
                  {b.block_type === 'video' && (
                    <>
                      <Field label="YouTube / FB Reel 連結"><input style={inp} value={b.content} onChange={(e) => upd({ content: e.target.value })} placeholder="https://www.youtube.com/watch?v=… 或 https://www.facebook.com/reel/…" /></Field>
                      <Field label="影片說明（選填）"><input style={inp} value={b.caption ?? ''} onChange={(e) => upd({ caption: e.target.value })} /></Field>
                    </>
                  )}
                </div>
              )
            })}

            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              <button onClick={() => setBrochure((bs) => [...bs, { block_type: 'text', content: '', display_order: bs.length }])} style={ghostBtn}>＋ 文字區塊</button>
              <button onClick={() => setBrochure((bs) => [...bs, { block_type: 'image', content: '', display_order: bs.length }])} style={ghostBtn}>＋ 圖片</button>
              <button onClick={() => setBrochure((bs) => [...bs, { block_type: 'video', content: '', display_order: bs.length }])} style={ghostBtn}>＋ 影片（YouTube / FB Reel）</button>
            </div>
          </div>
        )}

        {tab === 'tasks' && (
          <div style={col}>
            <div style={hint}>
              本輪為任務設定：可設賽事集體、各分組團體與個人額外挑戰目標。完成判定與前台進度顯示將於後續推出（部分指標需擴充活動上傳資料）。
            </div>

            {taskSection('race_collective', null, '賽事集體任務（全部參賽者）', '全體參賽者數值「加總」達標即完成，例：全員合計爬升 8848m。')}

            <div style={{ fontWeight: 800, fontSize: 14, marginTop: 6 }}>所有分組共同任務</div>
            <div style={hint}>套用到「每一個分組」的統一目標，設一次即可（前台所有分組都會顯示）。例：所有組都需完成總里程 200K。</div>
            {taskSection('group_team', null, '團體任務（每組加總）', '套用到所有分組：各分組成員加總達標。')}
            {taskSection('group_individual', null, '個人額外挑戰（每人各自）', '套用到所有分組：每位成員各自達標。')}

            {groups.filter((g) => g.name.trim()).map((g, gi) => (
              <div key={g.id ?? `g-${gi}`} style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                <div style={{ fontWeight: 800, fontSize: 14, marginTop: 6 }}>分組專屬：{g.name || `分組 ${gi + 1}`}</div>
                {taskSection('group_team', gi, '本組團體任務（全組加總）', '僅此分組：成員加總達標。例：本組需維持團體配速。')}
                {taskSection('group_individual', gi, '本組個人額外挑戰（每人各自）', '僅此分組：每位成員各自達標。例：A 組配速 7:00–8:00、B 組 5:00–6:00。')}
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
                checked={refundDisabled}
                onChange={(e) => setRefundDisabled(e.target.checked)}
              />
              此活動不提供退費
            </label>

            {refundDisabled && (
              <div style={hint}>
                前台簡章將不顯示取消退費規則；玩家仍可申請取消釋出名額，但退費金額為 0。
              </div>
            )}

            {!refundDisabled && (
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13.5, color: 'var(--tx)', fontWeight: 600 }}>
              <input
                type="checkbox"
                checked={!cancelFollowDefault}
                onChange={(e) => setCancelFollowDefault(!e.target.checked)}
              />
              此賽事自訂取消退費規則（不勾選＝跟隨系統預設）
            </label>
            )}

            {refundDisabled ? null : cancelFollowDefault ? (
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
                <CancellationPolicyFields
                  policy={cancelPolicy}
                  onChange={setCancelPolicy}
                  raceStartDate={startDate ? new Date(startDate) : null}
                />
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

      {certEditorOpen && certEditorModal()}
    </div>
  )
}

// --- 小元件 ---

// --- 即時獎勵「期望值試算」純函式（活動獎勵系統，serial 類 LINE POINTS 期望成本）---
// 抽獎語意（見上方即時獎勵設定區塊說明）：serial 類是以商家為單位的兩層抽獎，每個 RewardItem 各自獨立
// 擲骰——先擲該商家中不中獎（prob_bp/10000），中了才依權重比例在商家旗下面額中抽一組。以下函式只做
// 「假設抽很多次後的長期平均」試算，不涉及實際抽獎（那是後端 activityreward.RollAndGrant 的事）。

// 統一「有效面額清單」：優先用新格式 denominations；沒有則退回舊格式 serial_group_id（視為唯一面額、
// 權重視為 1）。與 SerialDenomFields 內 weightOf/setWeight 的判斷邏輯一致，避免兩處實作分岔。
function effectiveDenoms(item: RewardItem): { group_id: string; weight: number }[] {
  if (item.denominations && item.denominations.length > 0) return item.denominations
  if (item.serial_group_id) return [{ group_id: item.serial_group_id, weight: 1 }]
  return []
}

// 從序號組名稱／品項名稱解析面額點數值：取字串中最大的連續數字。
// 驗算例：parsePointValue('LINE POINTS 3000點') === 3000（唯一數字串即最大值）。
// 找不到任何數字時回傳 null，由呼叫端顯示「⚠ 無法從名稱解析面額」。
function parsePointValue(name: string): number | null {
  const matches = name.match(/\d+/g)
  if (!matches || matches.length === 0) return null
  const nums = matches.map((s) => parseInt(s, 10)).filter((n) => Number.isFinite(n))
  return nums.length > 0 ? Math.max(...nums) : null
}

// 面額實際機率 = 該商家中獎機率(prob_bp/10000) × 面額權重佔比(weight/該項目總weight)。
// 驗算例：prob_bp=1000（該商家中獎機率10%）、denominations 總權重100、本面額權重70
// → 實際機率 = 0.10 × (70/100) = 0.07（7%）
function denomActualProb(probBp: number, weight: number, totalWeight: number): number {
  if (totalWeight <= 0 || weight <= 0) return 0
  return (probBp / 10000) * (weight / totalWeight)
}

// 該面額期望點數 = 面額點數值 × 配發枚數(grant_count) × 實際機率。
// 驗算例：面額值3500點、配發枚數1、實際機率1%(0.01) → 期望點數 = 3500 × 1 × 0.01 = 35 點
function denomExpectedPoints(pointValue: number, grantCount: number, actualProb: number): number {
  return pointValue * Math.max(0, grantCount) * actualProb
}

// 期望成本(NT$) = 期望點數 × 單點成本。
// 驗算例：期望點數35、單點成本1.3元 → 期望成本 = 35 × 1.3 = 45.5 元
function pointsToCost(points: number, costPerPoint: number): number {
  return points * costPerPoint
}

// 手續費後淨收 = max(0, 報名費 − VIP券面額) × (1 − 刷卡費率)。
// 驗算例：報名費199元、VIP券面額49元、刷卡費率2.35% → 扣券後 afterCoupon=150 →
// 淨收 net = 150 × (1 − 0.0235) = 150 × 0.9765 = 146.475（約 NT$146.5）
function netProceeds(feeNtd: number, couponValueNtd: number, feeRatePct: number): { afterCoupon: number; net: number } {
  const afterCoupon = Math.max(0, feeNtd - couponValueNtd)
  const net = afterCoupon * (1 - feeRatePct / 100)
  return { afterCoupon, net }
}

interface SerialDenomCalc {
  itemIndex: number
  groupId: string
  groupName: string
  weight: number
  actualProb: number        // 0~1
  pointValue: number | null // null = 無法從名稱解析
  grantCount: number
  expectedPoints: number    // pointValue 為 null 時恆為 0
}
// 彙整一批 RewardItem 中 serial 類、且面額權重>0 的 LINE POINTS 面額期望值明細（不含成本換算——
// 成本＝expectedPoints×單點成本，由呼叫端乘上試算面板當下可調的值，避免每次調整單點成本就要重跑一次
// 完整彙整）。傳入單一項目陣列（如 [item]）即可算該項目自己的期望值，供 RewardItemRow/SerialDenomFields
// 內的 inline 顯示與下方彙總面板（renderRewardExpectedValuePanel）共用同一套算法。
// nonPointCount：面額權重>0 但序號組非 is_line_point 者的列數（這類序號不計入點數期望值，另列一行「未計入」）。
function computeSerialDenomCalcs(items: RewardItem[], groups: RewardSerialGroup[]): { rows: SerialDenomCalc[]; nonPointCount: number } {
  const rows: SerialDenomCalc[] = []
  let nonPointCount = 0
  items.forEach((item, itemIndex) => {
    if (item.type !== 'serial') return
    const denoms = effectiveDenoms(item)
    const totalWeight = denoms.reduce((s, d) => s + Math.max(0, d.weight), 0)
    denoms.forEach((d) => {
      if (d.weight <= 0) return
      const g = groups.find((x) => x.id === d.group_id)
      if (!g) return
      if (!g.is_line_point) { nonPointCount++; return }
      const actualProb = denomActualProb(item.prob_bp ?? 0, d.weight, totalWeight)
      const pointValue = parsePointValue(g.name || g.item_label || '')
      const expectedPoints = pointValue != null ? denomExpectedPoints(pointValue, g.grant_count, actualProb) : 0
      rows.push({ itemIndex, groupId: d.group_id, groupName: g.name, weight: d.weight, actualProb, pointValue, grantCount: g.grant_count, expectedPoints })
    })
  })
  return { rows, nonPointCount }
}

const REWARD_TYPE_LABEL: Record<RewardItemType, string> = {
  exp: 'EXP 經驗值', dp: 'DP', gp: 'GP', vip: 'VIP 天數', serial: '序號（合作商家／LINE POINT）',
  coupon: '活動優惠券',
}

// 即時獎勵設定單一項目列（活動獎勵系統 P2；coupon 為 migration 138 活動優惠券擴充）：type 決定顯示哪些參數欄位。
// costPerPoint：LINE POINTS 期望值試算器的單點成本（元，選填）——只有呼叫端傳入時才顯示 inline 期望成本
// 提示；目前只有「即時獎勵設定」區塊會傳，「參賽虛擬獎勵」沿用同一元件但不傳，維持原本無 inline 提示的樣式。
function RewardItemRow({ item, groups, couponDefs, costPerPoint, onChange, onRemove }: {
  item: RewardItem
  groups: RewardSerialGroup[]
  couponDefs: EventCouponDef[]
  costPerPoint?: number
  onChange: (patch: Partial<RewardItem>) => void
  onRemove: () => void
}) {
  return (
    <div style={{ ...card, background: 'var(--bg-1, #11131a)', padding: 12 }}>
      <Row>
        <Field label="獎勵類型">
          <select style={inp} value={item.type} onChange={(e) => onChange({ type: e.target.value as RewardItemType })}>
            {(Object.keys(REWARD_TYPE_LABEL) as RewardItemType[]).map((k) => (
              <option key={k} value={k}>{REWARD_TYPE_LABEL[k]}</option>
            ))}
          </select>
        </Field>
        <Field label={item.type === 'serial' ? '該商家中獎機率 (%)' : '中獎機率 (%)'}>
          <input
            style={inp} type="number" min={0} max={100} step="0.01"
            value={item.prob_bp / 100}
            onChange={(e) => {
              const pct = parseFloat(e.target.value || '0') || 0
              onChange({ prob_bp: Math.max(0, Math.min(10000, Math.round(pct * 100))) })
            }}
          />
        </Field>
      </Row>
      {/* 前台顯示控制（見後端 activityreward.RewardItem.Hidden）：隱藏＝不出現在前台
          「活動獎勵」預覽頁籤，做成「驚喜獎勵」，但 RollAndGrant 抽獎/發獎完全不受影響、照常進行。
          checked＝顯示狀態（語意與資料欄位 hidden 相反，符合管理員直覺：勾選＝前台看得到）。 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: 'var(--tx)' }}>
          <input type="checkbox" checked={!item.hidden} onChange={(e) => onChange({ hidden: !e.target.checked })} />
          前台顯示
        </label>
        {item.hidden && (
          <span
            style={{
              fontSize: 11, color: 'var(--tx-dim)', background: 'var(--bg-2)',
              border: '1px solid var(--line-2)', borderRadius: 999, padding: '2px 8px',
            }}
          >
            🙈 隱藏（驚喜獎勵）
          </span>
        )}
      </div>
      {(item.type === 'exp' || item.type === 'dp' || item.type === 'gp') && (
        <Row>
          <Field label="數量下限">
            <input style={inp} type="number" min={0} value={item.min ?? 0} onChange={(e) => onChange({ min: parseInt(e.target.value || '0', 10) || 0 })} />
          </Field>
          <Field label="數量上限">
            <input style={inp} type="number" min={0} value={item.max ?? 0} onChange={(e) => onChange({ max: parseInt(e.target.value || '0', 10) || 0 })} />
          </Field>
        </Row>
      )}
      {item.type === 'vip' && (
        <Field label="VIP 天數">
          <input style={inp} type="number" min={1} value={item.days ?? 0} onChange={(e) => onChange({ days: parseInt(e.target.value || '0', 10) || 0 })} />
        </Field>
      )}
      {item.type === 'serial' && <SerialDenomFields item={item} groups={groups} costPerPoint={costPerPoint} onChange={onChange} />}
      {item.type === 'serial' && costPerPoint != null && (() => {
        const { rows, nonPointCount } = computeSerialDenomCalcs([item], groups)
        if (rows.length === 0 && nonPointCount === 0) return null
        const totalPoints = rows.reduce((s, r) => s + r.expectedPoints, 0)
        const totalCost = pointsToCost(totalPoints, costPerPoint)
        const hasUnparsed = rows.some((r) => r.pointValue == null)
        return (
          <div style={calcHint}>
            💰 本項期望成本 NT$ {totalCost.toFixed(1)}（{totalPoints.toFixed(2)} 點 × {costPerPoint} 元/點）
            {hasUnparsed && <span style={{ color: 'var(--hunt)' }}>　⚠ 部分面額無法解析點數，未計入</span>}
            {nonPointCount > 0 && <span>　未計入：非點數類序號 {nonPointCount} 項</span>}
          </div>
        )
      })()}
      {item.type === 'coupon' && (
        <Field label="券種">
          <select style={inp} value={item.coupon_def_id ?? ''} onChange={(e) => onChange({ coupon_def_id: e.target.value })}>
            <option value="">請選擇券種…</option>
            {couponDefs.map((d) => (
              <option key={d.id} value={d.id}>{d.name}（NT$ {Math.round(d.amount_cents / 100)}）</option>
            ))}
          </select>
          {couponDefs.length === 0 && (
            <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
              尚無可用券種（已停用/已過期的不列入），請先到「活動優惠券管理」建立或啟用券種。
            </span>
          )}
        </Field>
      )}
      <button type="button" onClick={onRemove} style={{ ...linkBtn, color: 'var(--hunt)', alignSelf: 'flex-start' }}>移除此項目</button>
    </div>
  )
}

// serial 類「商家 → 面額權重」兩層設定欄位（活動獎勵系統：以商家為單位的兩層抽獎，見後端
// activityreward.RewardItem 設計註解）：先選商家，再對該商家旗下每個序號組（面額）填權重，
// 0＝不列入抽獎池。舊資料相容：item.merchant_id 未設定但有舊格式 serial_group_id 時，用該序號組
// 反查所屬商家當作目前顯示值，一旦使用者調整任何權重就會自動轉存成新格式（denominations 非空後，
// 後端一律優先採用 denominations，不再理會 serial_group_id）。
function SerialDenomFields({ item, groups, costPerPoint, onChange }: {
  item: RewardItem
  groups: RewardSerialGroup[]
  costPerPoint?: number
  onChange: (patch: Partial<RewardItem>) => void
}) {
  // 商家清單：只列出「有指定商家」的序號組，依 merchant_id 去重（未指定商家的序號組不適用兩層抽獎，不列入選項）
  const merchants = Array.from(
    new Map(groups.filter((g) => g.merchant_id).map((g) => [g.merchant_id as string, g.merchant_name || '(未命名商家)'])).entries()
  ).map(([id, name]) => ({ id, name }))

  const legacyGroup = !item.merchant_id && item.serial_group_id ? groups.find((g) => g.id === item.serial_group_id) : undefined
  const selectedMerchantId = item.merchant_id || legacyGroup?.merchant_id || ''
  const merchantGroups = groups.filter((g) => g.merchant_id === selectedMerchantId)

  function weightOf(groupId: string): number {
    const d = item.denominations?.find((x) => x.group_id === groupId)
    if (d) return d.weight
    if (!item.denominations?.length && item.serial_group_id === groupId) return 1 // 舊格式相容：唯一面額，權重視為 1
    return 0
  }
  function setWeight(groupId: string, weight: number) {
    const base = item.denominations ?? (item.serial_group_id ? [{ group_id: item.serial_group_id, weight: 1 }] : [])
    const rest = base.filter((x) => x.group_id !== groupId)
    onChange({ denominations: weight > 0 ? [...rest, { group_id: groupId, weight }] : rest })
  }

  return (
    <>
      <Field label="合作商家">
        <select
          style={inp} value={selectedMerchantId}
          onChange={(e) => onChange({ merchant_id: e.target.value, denominations: [] })}
        >
          <option value="">請選擇商家…</option>
          {merchants.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
        </select>
        {merchants.length === 0 && (
          <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>
            尚無對應本場活動、且已指定商家的序號組，請先到「序號/獎勵管理」建立序號組（需指定商家）並勾選對應全部活動或本場活動。
          </span>
        )}
      </Field>
      {selectedMerchantId && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <span style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>
            面額權重（商家中獎後依權重比例抽一組面額；0＝不列入抽獎，「可用」僅供參考、缺貨面額會自動排除）
          </span>
          {merchantGroups.map((g) => {
            const weight = weightOf(g.id)
            const totalWeight = effectiveDenoms(item).reduce((s, d) => s + Math.max(0, d.weight), 0)
            return (
              <div key={g.id} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <Row>
                  <span style={{ flex: 1, fontSize: 13, alignSelf: 'center' }}>{g.name}（可用 {g.available_count}）</span>
                  <input
                    style={{ ...inp, maxWidth: 100 }} type="number" min={0} step="1"
                    value={weight}
                    onChange={(e) => setWeight(g.id, Math.max(0, parseInt(e.target.value || '0', 10) || 0))}
                  />
                </Row>
                {/* 期望值試算 inline 提示（costPerPoint 由呼叫端傳入才顯示，見 RewardItemRow 註解）：
                    weight=0 代表此面額不在抽獎池中，不顯示提示。 */}
                {costPerPoint != null && weight > 0 && (() => {
                  if (!g.is_line_point) {
                    return <span style={calcHint}>非 LINE POINTS，不計入點數期望值</span>
                  }
                  const actualProb = denomActualProb(item.prob_bp ?? 0, weight, totalWeight)
                  const pointValue = parsePointValue(g.name || g.item_label || '')
                  if (pointValue == null) {
                    return <span style={{ ...calcHint, color: 'var(--hunt)' }}>⚠ 無法從名稱解析面額，請在序號組名稱中包含點數數字</span>
                  }
                  const expectedPoints = denomExpectedPoints(pointValue, g.grant_count, actualProb)
                  return (
                    <span style={calcHint}>
                      面額值 {pointValue}（自名稱解析）｜實際機率 {(actualProb * 100).toFixed(2)}%｜期望 {expectedPoints.toFixed(2)} 點
                    </span>
                  )
                })()}
              </div>
            )
          })}
          {merchantGroups.length === 0 && (
            <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>此商家尚無對應本場活動的序號組。</span>
          )}
        </div>
      )}
    </>
  )
}

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
// 期望值試算器 inline 提示文字樣式（小巧不搶版面，比照 hint 但更淡更小）
const calcHint: React.CSSProperties = { fontSize: 11, color: 'var(--tx-faint)' }
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
// 完賽證明排版編輯器 modal：admin 頁不在前台 phone-shell 內，一般 fixed 全屏即可（比照 NewRaceModal 的
// overlay/panel 慣例，但 zIndex 拉高到 200——RaceForm 本身可能已被 NewRaceModal(zIndex 50) 包一層
// （新增賽事流程），此 modal 需疊在其上）。
const certModalOverlay: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,.65)', zIndex: 200,
  display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 20,
}
const certModalPanel: React.CSSProperties = {
  width: '100%', height: '100%', maxWidth: 1280,
  background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16,
  display: 'flex', flexDirection: 'column', overflow: 'hidden',
}
