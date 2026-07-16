'use client'

import { useEffect, useMemo, useRef, useState } from 'react'
import useSWR from 'swr'
import { trainingApi, type WorkoutTemplate, type PaceLevel, type TrainingCalendar, type TrainingDay, type TrainingPlan, type AutoPlanRequest } from '@/lib/api'
import { resolveTemplate, saveFreetrainWorkout, totalKm, estMinutes, fmtDuration, segSummary, targetPaceBand, adjustMeta, adjustedValue, currentValue, pyramidPeak } from '@/lib/workout'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import UpgradeVipModal from './UpgradeVipModal'

// 自主訓練（VIP 專屬）：
// P1「📚 課表庫」——依分類列出，選配速等級後即時解析出總距離/預估時間；「開始訓練」把解析後的分段
// 橋接給 /track（sessionStorage，見 lib/workout.ts saveFreetrainWorkout）→ 帶到 GPS 追蹤跑。
// P2「🗓️ 訓練月曆」——比照成就月曆的月曆殼（換月/滑動/格子），每日顯示已排課表 + 是否已有實跑。
// P3：每日改「可多份」（user_training_schedule 加 id 主鍵，一天多筆）；新增「⚡ 一鍵安排課表」依跑齡/
// 最佳配速/目標賽事(或週數)自動產生一組訓練計畫（training_plans，每帳號 ≤3），計畫的課表用
// library_visible=false 的距離變體（lsd_6..32/easy_4/8/10，不進課表庫清單，但仍可解析開跑）。
// 都不是挑戰制：跑步照常走 GPS 上傳自動發里程 EXP，排程只是對照顯示。
const CATEGORY_LABELS: Record<string, string> = {
  recovery: '恢復', easy: '輕鬆', lsd: '長距離 LSD', tempo: '節奏', threshold: '閾值',
  progression: '漸速', interval: '間歇', fartlek: '法特雷克', pyramid: '金字塔',
  norwegian: '挪威 4×4', yasso: '亞索 800', rep: '重複跑',
}
// 月曆日格徽章的短標籤（空間小，全名裝不下)
const CATEGORY_SHORT: Record<string, string> = {
  recovery: '恢復', easy: '輕鬆', lsd: 'LSD', tempo: '節奏', threshold: '閾值',
  progression: '漸速', interval: '間歇', fartlek: '法特', pyramid: '金字',
  norwegian: '北歐', yasso: '亞索', rep: '重複',
}
// 月曆日格徽章顏色（依課表強度分桶：恢復/輕鬆/長距離＝綠、節奏/閾值/漸速＝金、間歇系＝紅、亞索/挪威＝紫）
const CATEGORY_COLOR: Record<string, { bg: string; fg: string }> = {
  recovery: { bg: 'rgba(45,229,154,.28)', fg: '#0b3324' },
  easy: { bg: 'rgba(45,229,154,.30)', fg: '#0b3324' },
  lsd: { bg: 'rgba(45,229,154,.22)', fg: '#0b3324' },
  tempo: { bg: 'rgba(255,194,75,.32)', fg: '#3a2705' },
  threshold: { bg: 'rgba(255,159,67,.32)', fg: '#3a1f05' },
  progression: { bg: 'rgba(255,194,75,.32)', fg: '#3a2705' },
  interval: { bg: 'rgba(255,107,107,.32)', fg: '#3a0a0a' },
  fartlek: { bg: 'rgba(255,107,107,.28)', fg: '#3a0a0a' },
  pyramid: { bg: 'rgba(255,107,107,.28)', fg: '#3a0a0a' },
  norwegian: { bg: 'rgba(199,88,255,.30)', fg: '#2a0a3a' },
  yasso: { bg: 'rgba(199,88,255,.30)', fg: '#2a0a3a' },
  rep: { bg: 'rgba(199,88,255,.26)', fg: '#2a0a3a' },
}
function catColor(cat: string) { return CATEGORY_COLOR[cat] || { bg: 'var(--bg-2)', fg: 'var(--tx-dim)' } }

// 一鍵安排課表：跑齡分級（決定預勾的休息日）與賽事距離選項
// defaultRestDays 索引比照 checkbox 一..日＝0(週一)..6(週日)：new 休 4 天(一三五六)、novice 休 3 天(一三五)、
// experienced/veteran 休 2 天(三五)——可自行改，但送出前至少要留 1 天非休息。
const RUNNING_AGE_OPTIONS: { id: AutoPlanRequest['running_age']; label: string; defaultRestDays: number[] }[] = [
  { id: 'new', label: '不到 1 年', defaultRestDays: [0, 2, 4, 5] },
  { id: 'novice', label: '1–3 年', defaultRestDays: [0, 2, 4] },
  { id: 'experienced', label: '3–5 年', defaultRestDays: [2, 4] },
  { id: 'veteran', label: '5 年以上', defaultRestDays: [2, 4] },
]
const RACE_DISTANCE_OPTIONS: { id: NonNullable<AutoPlanRequest['race_distance']>; label: string }[] = [
  { id: '5k', label: '5K' }, { id: '10k', label: '10K' }, { id: 'half', label: '半程馬拉松' }, { id: 'full', label: '全程馬拉松' },
]
const RACE_DISTANCE_LABEL: Record<string, string> = { '5k': '5K', '10k': '10K', half: '半程馬拉松', full: '全程馬拉松' }
const WEEKS_OPTIONS = [1, 4, 8, 12, 16]
// 休息日 checkbox 星期標籤，索引 0(週一)..6(週日)——與月曆格用的 WK（0=週日起算）刻意不同慣例，各自對應用途。
const WEEKDAY_MON_FIRST = ['一', '二', '三', '四', '五', '六', '日']

// 1km 最佳成績輸入：接受「mm:ss」或純秒數
function parseBest1km(raw: string): number | null {
  const s = raw.trim()
  if (!s) return null
  if (s.includes(':')) {
    const [m, sec] = s.split(':').map(Number)
    if (!isFinite(m) || !isFinite(sec)) return null
    return m * 60 + sec
  }
  const n = Number(s)
  return isFinite(n) && n > 0 ? n : null
}

const WK = ['日', '一', '二', '三', '四', '五', '六']
function ym(d: Date) { return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}` }
function shiftMonth(key: string, delta: number) { const [y, m] = key.split('-').map(Number); return ym(new Date(y, m - 1 + delta, 1)) }
function pad2(n: number) { return String(n).padStart(2, '0') }
function ymd(d: Date) { return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}` }

export default function TrainingScreen({ onBack }: { onBack: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data, error } = useSWR(
    uid && getUserToken() ? ['training-templates', uid] : null,
    () => withUserAuth((t) => trainingApi.templates(t)),
  )
  const vipLocked = !!error && error?.status === 403 && error?.message === 'vip_only'
  const loadFailed = !!error && !vipLocked
  const unlocked = !!user && !vipLocked && !loadFailed && !!data

  const [tab, setTab] = useState<'library' | 'calendar'>('library')
  const [levelId, setLevelId] = useState<number | null>(null)
  // 記住上次選的配速等級（切頁/重整後維持不變，不再每次回到預設）
  useEffect(() => { const v = window.localStorage.getItem('dor_training_pace_level'); if (v) setLevelId(Number(v)) }, [])
  const [navigating, setNavigating] = useState(false)
  const [showUpgrade, setShowUpgrade] = useState(false)

  const levels = useMemo(() => data?.pace_levels ?? [], [data])
  const level: PaceLevel | null = useMemo(() => {
    if (!levels.length) return null
    const byId = levelId != null ? levels.find((l) => l.id === levelId) : undefined
    return byId ?? levels.find((l) => l.id === 5) ?? levels[Math.floor(levels.length / 2)]
  }, [levels, levelId])

  // 依 category 分組，保留課表庫原本的 sort_order（課表庫分頁 + 月曆選課表 modal 共用）。
  // 只列 library_visible!==false 者——產生器用的距離變體（lsd_6..32/easy_4/8/10）不進清單，
  // 但仍在 data.templates 完整清單中，供 startWorkout()/月曆已排課表用 template_code 解析分段。
  const groups = useMemo(() => {
    const map = new Map<string, WorkoutTemplate[]>()
    for (const t of data?.templates ?? []) {
      if (t.library_visible === false) continue
      const arr = map.get(t.category)
      if (arr) arr.push(t)
      else map.set(t.category, [t])
    }
    return Array.from(map.entries())
  }, [data])

  // 依 template_code + 指定配速等級解析並橋接給 /track（課表庫「開始訓練」與月曆「開始此課表」共用）。
  // adjust（migration 085）：微調量，課表庫卡片帶目前微調、月曆已排課表帶該筆 scheduled.adjust；
  // adjust_type 直接從查到的 t 取得（不需呼叫端另傳）。
  function startWorkout(code: string, useLevel: PaceLevel | null, adjust = 0) {
    const t = (data?.templates ?? []).find((x) => x.code === code)
    if (!t || !useLevel) return
    const segments = resolveTemplate(t.segments, useLevel, t.adjust_type, adjust)
    saveFreetrainWorkout(t.code, t.name, segments)
    setNavigating(true)
    setTimeout(() => { window.location.href = '/track' }, 380)
  }
  function startTemplate(t: WorkoutTemplate) { startWorkout(t.code, level, libAdjust[t.code] ?? 0) }

  // ── 課表微調（migration 085）：課表庫卡片各自記一份 delta（Record<code,number>，預設 0）──
  const [libAdjust, setLibAdjust] = useState<Record<string, number>>({})
  function bumpLib(code: string, delta: number) { setLibAdjust((prev) => ({ ...prev, [code]: (prev[code] ?? 0) + delta })) }

  // ── 訓練月曆（P2）+ 每日多份/一鍵訓練計畫（P3）──
  const [month, setMonth] = useState(() => ym(new Date()))
  const [cal, setCal] = useState<TrainingCalendar | null>(null)
  const [calErr, setCalErr] = useState(false)
  function loadCalendar(m: string) {
    if (!getUserToken()) return
    withUserAuth((t) => trainingApi.calendar(t, m)).then((c) => { setCal(c); setCalErr(false) }).catch(() => setCalErr(true))
  }
  useEffect(() => {
    if (tab !== 'calendar' || !unlocked) return
    setCal(null)
    loadCalendar(month)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [month, tab, unlocked])

  const touchPt = useRef<{ x: number; y: number } | null>(null)
  function go(delta: number) { setMonth((m) => shiftMonth(m, delta)) }
  function onTouchStart(e: React.TouchEvent) { touchPt.current = { x: e.touches[0].clientX, y: e.touches[0].clientY } }
  function onTouchEnd(e: React.TouchEvent) {
    const st = touchPt.current
    if (!st) return
    touchPt.current = null
    const dx = e.changedTouches[0].clientX - st.x
    const dy = e.changedTouches[0].clientY - st.y
    // 垂直捲動優先：水平位移要夠大、且明顯大於垂直位移，才算「換月滑動」——否則上下捲頁時
    // 只要帶一點水平漂移就會誤切月份（只看 dx 會誤判）。
    if (Math.abs(dx) < 45 || Math.abs(dx) < Math.abs(dy) * 1.5) return
    if (dx > 0) go(-1)   // 右滑 → 上個月
    else go(1)           // 左滑 → 下個月（未來月不鎖，可排課）
  }

  // 我的訓練計畫（P3，≤3 個；一鍵安排課表產生，計畫刪除連帶刪其排程）
  const [plans, setPlans] = useState<TrainingPlan[] | null>(null)
  const [plansErr, setPlansErr] = useState(false)
  const [deletingPlanId, setDeletingPlanId] = useState<string | null>(null)
  const plansRef = useRef<HTMLDivElement | null>(null)
  const [planLimitMsg, setPlanLimitMsg] = useState('')
  function loadPlans() {
    if (!getUserToken()) return
    withUserAuth((t) => trainingApi.plans(t)).then((r) => { setPlans(r.plans); setPlansErr(false) }).catch(() => setPlansErr(true))
  }
  useEffect(() => {
    if (tab !== 'calendar' || !unlocked) return
    loadPlans()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, unlocked])

  // 月曆「顯示來源」：避免同日多份課表塞爆格子，改成一次只看一個來源——'manual'＝手動排定，
  // 其餘為某 training_plans.id（依 scheduled[].plan_id 過濾）。預設選最新的計畫，無計畫則手動排；
  // 使用者選定某計畫後只要該計畫還在就不覆蓋，計畫被刪除才回退預設。
  const [calSource, setCalSource] = useState<string>('')
  useEffect(() => {
    if (!plans) return
    if (calSource === 'manual') return
    if (calSource && plans.some((p) => p.id === calSource)) return
    setCalSource(plans.length > 0 ? plans[plans.length - 1].id : 'manual')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plans])

  async function removePlan(id: string) {
    if (!window.confirm('清除此訓練計畫？將一併移除已排定但尚未完成的課表。')) return
    const token = getUserToken()
    if (!token) return
    setDeletingPlanId(id)
    try {
      await withUserAuth((t) => trainingApi.deletePlan(t, id))
      loadPlans()
      loadCalendar(month) // 計畫的排程已 CASCADE 刪除，月曆需一併刷新
    } catch {
      /* 靜默失敗，計畫清單維持原狀，使用者可重試 */
    } finally {
      setDeletingPlanId(null)
    }
  }

  // 日課表 modal（點某日開啟；null=關閉）——列出當日所有已排課表(可多份)，可個別開始/移除，下方可加新課表
  // 點日期只「選中」（下方列出當天已排課表）；要變更/加入才由「編輯／加入」鈕開 pickerDate modal
  const [selectedDate, setSelectedDate] = useState<string | null>(null)
  const [pickerDate, setPickerDate] = useState<string | null>(null)
  const [pickerLevelId, setPickerLevelId] = useState<number | null>(null)
  const [pickerBusy, setPickerBusy] = useState(false)
  const [pickerErr, setPickerErr] = useState('')
  const [removingId, setRemovingId] = useState<string | null>(null)
  const pickerDay: TrainingDay | undefined = useMemo(() => cal?.days.find((d) => d.date === pickerDate), [cal, pickerDate])
  const pickerLevel: PaceLevel | null = useMemo(() => levels.find((l) => l.id === pickerLevelId) ?? level, [levels, pickerLevelId, level])
  // 選課表 modal 內各課表的微調 delta（開啟新的一天時重置，避免上一天的微調殘留）
  const [pickerAdjust, setPickerAdjust] = useState<Record<string, number>>({})
  function bumpPicker(code: string, delta: number) { setPickerAdjust((prev) => ({ ...prev, [code]: (prev[code] ?? 0) + delta })) }

  function openPicker(date: string) {
    setPickerDate(date)
    setPickerLevelId(levelId ?? level?.id ?? null) // 一天可能有多份、配速各異，新增一律沿用全域上次選的等級
    setPickerAdjust({})
    setPickerErr('')
  }
  function closePicker() { setPickerDate(null); setPickerErr('') }

  // 新增一筆課表到當日（手動排定，plan_id 固定 NULL）；加入後保留 modal 開啟，方便一次排多份
  async function saveSchedule(t: WorkoutTemplate) {
    if (!pickerDate || !pickerLevel) return
    const adj = pickerAdjust[t.code] ?? 0
    const resolved = resolveTemplate(t.segments, pickerLevel, t.adjust_type, adj)
    const token = getUserToken()
    if (!token) return
    setPickerBusy(true); setPickerErr('')
    try {
      await withUserAuth((tok) => trainingApi.schedule(tok, {
        date: pickerDate, template_code: t.code, pace_level: pickerLevel.id,
        planned_km: totalKm(resolved), planned_min: estMinutes(resolved), adjust: adj,
      }))
      loadCalendar(month)
    } catch {
      setPickerErr('排定失敗，請稍後再試')
    } finally {
      setPickerBusy(false)
    }
  }

  // 移除當日某一筆已排課表（依 id；來自計畫的課表也只刪這一筆，不影響同計畫其餘課表）
  async function removeSchedule(id: string) {
    const token = getUserToken()
    if (!token) return
    setRemovingId(id); setPickerErr('')
    try {
      await withUserAuth((tok) => trainingApi.unschedule(tok, id))
      loadCalendar(month)
    } catch {
      setPickerErr('刪除失敗，請稍後再試')
    } finally {
      setRemovingId(null)
    }
  }

  // ── 一鍵安排課表（P3）：跑齡/最佳配速/最長跑量 + 目標賽事(或週數) → 自動產生一個訓練計畫 ──
  const [showAutoPlan, setShowAutoPlan] = useState(false)
  const [apRunningAge, setApRunningAge] = useState<AutoPlanRequest['running_age']>('novice')
  const [apBest1km, setApBest1km] = useState('')
  const [apLongestKm, setApLongestKm] = useState('')
  const [apLongestMin, setApLongestMin] = useState('')
  const [apHasRace, setApHasRace] = useState(true)
  const [apRaceDate, setApRaceDate] = useState('')
  const [apRaceDistance, setApRaceDistance] = useState<NonNullable<AutoPlanRequest['race_distance']>>('10k')
  const [apWeeks, setApWeeks] = useState(8)
  const [apRestDays, setApRestDays] = useState<number[]>(() => RUNNING_AGE_OPTIONS.find((o) => o.id === 'novice')!.defaultRestDays)
  const [apBusy, setApBusy] = useState(false)
  const [apErr, setApErr] = useState('')

  function openAutoPlan() {
    setApErr('')
    setShowAutoPlan(true)
  }
  function onRunningAgeChange(id: AutoPlanRequest['running_age']) {
    setApRunningAge(id)
    const opt = RUNNING_AGE_OPTIONS.find((o) => o.id === id)
    if (opt) setApRestDays(opt.defaultRestDays) // 依跑齡帶預設休息日（送出前仍可自行改）
  }
  function toggleRestDay(idx: number) {
    setApRestDays((prev) => (prev.includes(idx) ? prev.filter((d) => d !== idx) : [...prev, idx].sort((a, b) => a - b)))
  }

  async function submitAutoPlan() {
    const token = getUserToken()
    if (!token) return
    const best1kmS = parseBest1km(apBest1km)
    const longestKm = Number(apLongestKm)
    const longestMin = Number(apLongestMin)
    if (!best1kmS || !(longestKm > 0) || !(longestMin > 0)) { setApErr('請完整填寫跑力資料'); return }
    if (apHasRace && !apRaceDate) { setApErr('請選擇賽事日期'); return }
    if (apRestDays.length >= 7) { setApErr('至少需保留 1 天非休息日才能排課'); return }
    setApBusy(true); setApErr('')
    try {
      const body: AutoPlanRequest = {
        running_age: apRunningAge, best_1km_s: best1kmS, longest_km: longestKm, longest_min: longestMin,
        has_race: apHasRace, rest_days: apRestDays,
        ...(apHasRace ? { race_date: apRaceDate, race_distance: apRaceDistance } : { weeks: apWeeks }),
      }
      const res = await withUserAuth((tok) => trainingApi.autoPlan(tok, body))
      setShowAutoPlan(false)
      setCalSource(res.plan.id) // 剛產生的計畫直接切為月曆顯示來源，馬上看得到排好的課表
      loadPlans()
      loadCalendar(month)
    } catch (e: any) {
      if (e?.status === 409 && e?.message === 'plan_limit') {
        setShowAutoPlan(false)
        setPlanLimitMsg('已達 3 個訓練計畫上限，請先清除一個')
        setTimeout(() => plansRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 50)
      } else if (e?.status === 400 && e?.message === 'need_training_day') {
        setApErr('至少需保留 1 天非休息日才能排課')
      } else {
        setApErr('產生失敗，請稍後再試')
      }
    } finally {
      setApBusy(false)
    }
  }

  // 月曆格
  const [yy, mm] = month.split('-').map(Number)
  const first = new Date(yy, mm - 1, 1).getDay()
  const daysIn = new Date(yy, mm, 0).getDate()
  const cells: (number | null)[] = [...Array(first).fill(null), ...Array.from({ length: daysIn }, (_, i) => i + 1)]
  const dayMap: Record<string, TrainingDay> = {}
  ;(cal?.days ?? []).forEach((d) => { dayMap[d.date] = d })
  const todayStr = ymd(new Date())

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🏃 自主訓練</span>
      </header>

      {unlocked && (
        <div style={{ display: 'flex', gap: 4, padding: '0 18px', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
          {([['library', '📚 課表庫'], ['calendar', '🗓️ 訓練月曆']] as const).map(([v, label]) => (
            <button key={v} onClick={() => setTab(v)} style={{
              padding: '10px 10px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 13.5, whiteSpace: 'nowrap',
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: tab === v ? 800 : 500,
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent', fontFamily: 'inherit',
            }}>{label}</button>
          ))}
        </div>
      )}

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '6px 18px 28px' }}>
        {!user ? (
          <div style={emptyBox}>請先登入以使用自主訓練</div>
        ) : vipLocked ? (
          <div style={emptyBox}>
            <div style={{ fontSize: 32 }}>🔒</div>
            <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', marginTop: 8 }}>VIP 專屬功能</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.7 }}>
              自主訓練提供完整課表庫（恢復／輕鬆／節奏／閾值／間歇…）與訓練月曆排程，<br />升級 VIP 即可解鎖，依你的能力自訂訓練。
            </div>
            <button onClick={() => setShowUpgrade(true)} style={{ marginTop: 14, background: 'var(--gold)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 10, padding: '10px 22px', fontSize: 13.5, cursor: 'pointer' }}>✦ 升級 VIP</button>
          </div>
        ) : loadFailed ? (
          <div style={emptyBox}>課表庫載入失敗，請稍後再試</div>
        ) : !data ? (
          <div style={emptyBox}>載入中…</div>
        ) : tab === 'library' ? (
          <>
            <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '4px 2px 12px', lineHeight: 1.7 }}>
              選擇配速等級，課表庫即自動換算成你的實際配速。挑一份「開始訓練」帶到 GPS 追蹤跑——完成即照常記錄跑步、累計里程 EXP。
            </p>

            {/* 配速等級選擇 */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 14px', marginBottom: 16 }}>
              <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', flexShrink: 0 }}>配速等級</span>
              <select
                value={level?.id ?? ''}
                onChange={(e) => { const id = Number(e.target.value); setLevelId(id); window.localStorage.setItem('dor_training_pace_level', String(id)) }}
                style={levelSelect}
              >
                {levels.map((l) => <option key={l.id} value={l.id}>Lv.{l.id} · {l.label}</option>)}
              </select>
            </div>

            {groups.length === 0 && <div style={emptyBox}>目前尚無課表庫內容</div>}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
              {groups.map(([cat, templates]) => (
                <div key={cat}>
                  <div style={{ fontSize: 12, fontWeight: 800, color: 'var(--fug)', marginBottom: 8, letterSpacing: '.05em' }}>{CATEGORY_LABELS[cat] || cat}</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                    {templates.map((t) => {
                      const adj = libAdjust[t.code] ?? 0
                      const meta = adjustMeta(t)
                      const resolved = level ? resolveTemplate(t.segments, level, t.adjust_type, adj) : []
                      // 金字塔段數多，segSummary 會很長；改精簡呈現「金字塔 400→800→…→{peak}→…→400」
                      const summaryText = t.adjust_type === 'pyramid' ? `金字塔 400→800→…→${pyramidPeak(t.segments, adj)}m→…→400` : segSummary(resolved)
                      return (
                        <div key={t.code} style={tplCard}>
                          <div style={{ fontSize: 14.5, fontWeight: 800, color: 'var(--tx)' }}>{t.name}</div>
                          {t.description && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6 }}>{t.description}</div>}
                          {summaryText && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.6, padding: '7px 10px', background: 'var(--bg-2)', borderRadius: 8 }}>📋 {summaryText}</div>}
                          {targetPaceBand(resolved) && <div style={{ fontSize: 12, color: 'var(--fug)', fontWeight: 700, marginTop: 6, fontVariantNumeric: 'tabular-nums' }}>🎯 目標配速 {targetPaceBand(resolved)}</div>}
                          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 5, fontVariantNumeric: 'tabular-nums' }}>總距離 {totalKm(resolved)} K · 預估 {fmtDuration(estMinutes(resolved))}</div>
                          {meta.type !== 'none' && (() => {
                            const val = adjustedValue(t, adj)
                            const atMin = val <= meta.min
                            const atMax = val >= meta.max
                            return (
                              <div style={adjustRow}>
                                <button type="button" onClick={() => bumpLib(t.code, -1)} disabled={atMin} style={{ ...adjustBtn, opacity: atMin ? 0.4 : 1 }}>−</button>
                                <span style={adjustVal}>{currentValue(t, adj)}</span>
                                <button type="button" onClick={() => bumpLib(t.code, 1)} disabled={atMax} style={{ ...adjustBtn, opacity: atMax ? 0.4 : 1 }}>＋</button>
                              </div>
                            )
                          })()}
                          <button onClick={() => startTemplate(t)} disabled={!level} style={{ ...startBtn, opacity: level ? 1 : 0.5 }}>▶ 開始訓練</button>
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          </>
        ) : (
          <>
            {/* 一鍵安排課表 */}
            <div style={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 10, marginBottom: 10 }}>
              <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>訓練計畫 {plans ? plans.length : '…'}/3</span>
              <button onClick={openAutoPlan} style={autoPlanBtn}>⚡ 一鍵安排課表</button>
            </div>

            {/* 顯示來源：切換月曆日格要看哪個來源的課表（手動排 or 某訓練計畫），避免多份塞爆格子 */}
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 800, margin: '2px 2px 6px' }}>顯示來源</div>
            <div style={{ display: 'flex', gap: 6, overflowX: 'auto', WebkitOverflowScrolling: 'touch', paddingBottom: 2, marginBottom: 12 }}>
              <button type="button" onClick={() => setCalSource('manual')} style={{ ...sourceChip, ...(calSource === 'manual' ? sourceChipActive : {}) }}>手動排</button>
              {(plans ?? []).map((p) => (
                <button key={p.id} type="button" onClick={() => setCalSource(p.id)} style={{ ...sourceChip, ...(calSource === p.id ? sourceChipActive : {}) }}>{p.name}</button>
              ))}
            </div>

            {/* 本月總覽（加總全部來源；下方月曆僅顯示目前選中來源） */}
            <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px', marginBottom: 14 }}>
              {calErr ? <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>本月資料載入失敗，請稍後再試</div> : !cal ? <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>載入中…</div> : (
                <>
                  {/* 同一項目（天數/里程/時間）預計 vs 實際並列同一列 + 完成% */}
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr 1fr', gap: 4, fontSize: 10.5, color: 'var(--tx-faint)', fontWeight: 800, paddingBottom: 6, borderBottom: '1px solid var(--line)' }}>
                    <span>本月</span><span style={{ textAlign: 'right' }}>預計</span><span style={{ textAlign: 'right' }}>實際</span><span style={{ textAlign: 'right' }}>完成</span>
                  </div>
                  {([
                    { k: '天數', p: cal.planned.days, a: cal.actual.days, u: '天' },
                    { k: '里程', p: cal.planned.km, a: cal.actual.km, u: 'K' },
                    { k: '時間', p: cal.planned.min, a: cal.actual.min, u: '分' },
                  ] as const).map((r) => {
                    const pct = r.p > 0 ? Math.round((r.a / r.p) * 100) : null
                    const fmt = (n: number) => (r.u === 'K' ? n.toFixed(1) : String(n))
                    return (
                      <div key={r.k} style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr 1fr', gap: 4, fontSize: 12.5, padding: '6px 0', fontVariantNumeric: 'tabular-nums', alignItems: 'baseline' }}>
                        <span style={{ color: 'var(--tx-dim)', fontWeight: 700 }}>{r.k}</span>
                        <span style={{ textAlign: 'right', color: 'var(--tx)', fontWeight: 800 }}>{fmt(r.p)}<span style={{ fontSize: 10, color: 'var(--tx-faint)', fontWeight: 600 }}> {r.u}</span></span>
                        <span style={{ textAlign: 'right', color: 'var(--fug)', fontWeight: 800 }}>{fmt(r.a)}<span style={{ fontSize: 10, color: 'var(--tx-faint)', fontWeight: 600 }}> {r.u}</span></span>
                        <span style={{ textAlign: 'right', fontWeight: 800, color: pct == null ? 'var(--tx-faint)' : pct >= 100 ? 'var(--fug)' : 'var(--gold)' }}>{pct == null ? '—' : `${pct}%`}</span>
                      </div>
                    )
                  })}
                  <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginTop: 4 }}>（加總全部來源，不受下方顯示來源篩選影響）</div>
                </>
              )}
            </div>

            {/* 月曆殼（比照成就月曆：換月/滑動/格子；未來月不鎖，可預先排課） */}
            <div
              onTouchStart={onTouchStart} onTouchEnd={onTouchEnd}
              style={{ background: 'linear-gradient(160deg, var(--bg-1), var(--bg-2))', border: '1px solid var(--line)', borderRadius: 18, padding: '16px 16px 14px' }}
            >
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 12 }}>
                <button onClick={() => go(-1)} style={navBtn}>‹</button>
                <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>{yy} 年 {mm} 月</div>
                <button onClick={() => go(1)} style={navBtn}>›</button>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7,1fr)', gap: 4, marginBottom: 4 }}>
                {WK.map((w) => <div key={w} style={{ textAlign: 'center', fontSize: 10, color: 'var(--tx-faint)' }}>{w}</div>)}
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7,1fr)', gap: 4 }}>
                {cells.map((day, i) => {
                  if (day == null) return <div key={`b${i}`} />
                  const dateStr = `${month}-${pad2(day)}`
                  const info = dayMap[dateStr]
                  // 只顯示目前選中來源的課表（'manual'＝plan_id null，否則比對 plan_id）——每日最多顯示 1 份，避免跑版
                  const sched = (info?.scheduled ?? []).filter((s) => (calSource === 'manual' ? s.plan_id === null : s.plan_id === calSource))
                  const isToday = dateStr === todayStr
                  // ≤2 份逐一顯示各自的分類色徽章；>2 份收成一顆「+N」徽章（避免格子塞爆；同來源理論上最多 1 份，此為防禦）
                  const badges = sched.length <= 2 ? sched : []
                  return (
                    <button key={day} onClick={() => setSelectedDate(dateStr)} style={{
                      aspectRatio: '1', borderRadius: 8, background: dateStr === selectedDate ? 'rgba(70,227,160,.14)' : 'var(--bg-2)',
                      border: dateStr === selectedDate ? '2px solid var(--fug)' : isToday ? '1.5px solid var(--fug)' : '1px solid var(--line)',
                      display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 1,
                      padding: 2, cursor: 'pointer', position: 'relative', fontFamily: 'inherit',
                    }}>
                      <span style={{ fontSize: 10, color: 'var(--tx-faint)', fontWeight: 700 }}>{day}</span>
                      {badges.map((s) => {
                        const col = catColor(s.category)
                        return (
                          <span key={s.id} style={{ fontSize: 7.5, fontWeight: 800, padding: '1px 4px', borderRadius: 5, background: col.bg, color: col.fg, maxWidth: '94%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {CATEGORY_SHORT[s.category] || s.category}
                          </span>
                        )
                      })}
                      {sched.length > 2 && (
                        <span style={{ fontSize: 7.5, fontWeight: 800, padding: '1px 4px', borderRadius: 5, background: 'var(--bg-3, var(--line))', color: 'var(--tx-dim)' }}>+{sched.length}</span>
                      )}
                      {info?.has_activity && <span style={{ position: 'absolute', top: 2, right: 3, fontSize: 9, color: 'var(--fug)', fontWeight: 900 }}>✓</span>}
                    </button>
                  )
                })}
              </div>
            </div>
            <div style={{ textAlign: 'center', fontSize: 10.5, color: 'var(--tx-faint)', margin: '8px 0 4px' }}>左右滑動或按 ‹ › 切換月份 · 點日期查看當天課表</div>

            {/* 當天課表：點日期後在此列出（不直接進編輯介面）；要變更/加入按「編輯／加入」開 modal */}
            <div style={{ marginTop: 12, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px' }}>
              {!selectedDate ? (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', textAlign: 'center', padding: '6px 0' }}>👆 點上方日期，查看當天已排的課表</div>
              ) : (() => {
                const day = dayMap[selectedDate]
                const list = day?.scheduled ?? []
                return (
                  <>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginBottom: 10 }}>
                      <div style={{ fontSize: 13, fontWeight: 900, color: 'var(--tx)', minWidth: 0 }}>
                        {selectedDate}
                        <span style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 700 }}> · 已排 {list.length} 份{day?.has_activity ? ' · ✓ 當天有跑' : ''}</span>
                      </div>
                      <button onClick={() => openPicker(selectedDate)} style={{ flexShrink: 0, background: 'var(--bg-2)', border: '1px solid var(--fug)', color: 'var(--fug)', fontWeight: 800, borderRadius: 9, padding: '7px 12px', cursor: 'pointer', fontSize: 12, fontFamily: 'inherit' }}>✏️ 編輯／加入</button>
                    </div>
                    {list.length === 0 ? (
                      <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>這天還沒排課表——按「編輯／加入」挑一份。</div>
                    ) : (
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                        {list.map((s) => {
                          const sTpl = (data?.templates ?? []).find((x) => x.code === s.template_code)
                          const sMeta = sTpl ? adjustMeta(sTpl) : null
                          const sAdjLabel = sTpl && sMeta && sMeta.type !== 'none'
                            ? (sMeta.type === 'distance' ? `${adjustedValue(sTpl, s.adjust)}K` : sMeta.type === 'reps' ? `×${adjustedValue(sTpl, s.adjust)}` : `峰${adjustedValue(sTpl, s.adjust)}m`)
                            : ''
                          return (
                            <div key={s.id} style={{ background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 12px' }}>
                              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                                <div style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                  {s.name}{sAdjLabel && <span style={{ color: 'var(--fug)', fontWeight: 700 }}> · {sAdjLabel}</span>}
                                </div>
                                <span style={{ fontSize: 9.5, fontWeight: 800, padding: '2px 6px', borderRadius: 6, background: s.plan_id ? 'rgba(199,88,255,.18)' : 'var(--line)', color: s.plan_id ? '#a05ad0' : 'var(--tx-dim)', flexShrink: 0, whiteSpace: 'nowrap' }}>
                                  {s.plan_id ? `📋 ${s.plan_name || '計畫'}` : '手動'}
                                </span>
                              </div>
                              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, marginTop: 6 }}>
                                <span style={{ fontSize: 11.5, color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums' }}>
                                  {CATEGORY_LABELS[s.category] || s.category} · {s.planned_km.toFixed(1)} K · {fmtDuration(s.planned_min)}
                                </span>
                                <button onClick={() => { const lvl = levels.find((l) => l.id === s.pace_level) ?? null; startWorkout(s.template_code, lvl, s.adjust) }} style={{ flexShrink: 0, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 11.5, fontFamily: 'inherit' }}>▶ 開始</button>
                              </div>
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </>
                )
              })()}
            </div>

            {/* 我的訓練計畫（P3，≤3 個） */}
            <div ref={plansRef} style={{ marginTop: 20 }}>
              <div style={{ fontSize: 12, fontWeight: 800, color: 'var(--fug)', marginBottom: 8, letterSpacing: '.05em' }}>我的訓練計畫</div>
              {planLimitMsg && (
                <div style={{ fontSize: 12, color: '#ff9f43', fontWeight: 700, background: 'rgba(255,159,67,.12)', border: '1px solid rgba(255,159,67,.3)', borderRadius: 10, padding: '9px 12px', marginBottom: 10 }}>
                  ⚠ {planLimitMsg}
                </div>
              )}
              {plansErr ? (
                <div style={{ ...emptyBox, padding: '16px 4px' }}>訓練計畫載入失敗</div>
              ) : !plans ? (
                <div style={{ ...emptyBox, padding: '16px 4px' }}>載入中…</div>
              ) : plans.length === 0 ? (
                <div style={{ ...emptyBox, padding: '16px 4px' }}>尚無訓練計畫，點上方「⚡ 一鍵安排課表」建立一個</div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {plans.map((p) => (
                    <div key={p.id} style={tplCard}>
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10 }}>
                        <div style={{ minWidth: 0 }}>
                          <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>{p.name}</div>
                          <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 5, lineHeight: 1.8 }}>
                            {p.race_distance ? `🏁 ${RACE_DISTANCE_LABEL[p.race_distance] || p.race_distance}${p.race_date ? ` · ${p.race_date}` : ''}` : `${p.weeks} 週計畫`} · 每週 {p.days_per_week} 天
                            <br />{p.start_date} ~ {p.end_date} · 共 {p.workout_count} 份課表
                          </div>
                        </div>
                        <button disabled={deletingPlanId === p.id} onClick={() => removePlan(p.id)} style={{ flexShrink: 0, background: 'none', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 8, padding: '6px 10px', cursor: 'pointer', fontSize: 11.5, fontFamily: 'inherit' }}>🗑 清除計畫</button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </>
        )}
      </div>

      {navigating && (
        <div style={{ position: 'absolute', inset: 0, zIndex: 3400, background: 'var(--bg)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, animation: 'fadeIn .3s ease' }}>
          <div style={{ fontSize: 30 }}>🏃‍♂️</div>
          <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)' }}>前往 GPS 跑步追蹤…</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>準備開始自主訓練</div>
        </div>
      )}

      {showUpgrade && <UpgradeVipModal onClose={() => setShowUpgrade(false)} />}

      {/* 選課表 modal：排定/更換某日課表，或開始已排定的課表 */}
      {pickerDate && (
        <div data-skin="default" onClick={closePicker} style={{ position: 'fixed', inset: 0, zIndex: 3600, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'flex-end', justifyContent: 'center' }}>
          <div onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 440, maxHeight: '86dvh', overflowY: 'auto', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--line-2)', borderTopLeftRadius: 18, borderTopRightRadius: 18, boxShadow: '0 -12px 40px rgba(0,0,0,.6)', padding: '16px 18px 22px' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ fontSize: 15, fontWeight: 900, color: '#fff' }}>{pickerDate}</div>
              <button onClick={closePicker} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 20, cursor: 'pointer', lineHeight: 1, padding: 4 }}>×</button>
            </div>

            {!!pickerDay?.scheduled.length && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8, margin: '10px 0' }}>
                <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 800 }}>當日已排（{pickerDay.scheduled.length}）</div>
                {pickerDay.scheduled.map((s) => {
                  // 顯示該筆已排課表目前的微調值（如「6K」/「×8」/「峰1600m」）；type='none' 或找不到課表原型不顯示
                  const sTpl = (data?.templates ?? []).find((x) => x.code === s.template_code)
                  const sMeta = sTpl ? adjustMeta(sTpl) : null
                  const sAdjLabel = sTpl && sMeta && sMeta.type !== 'none'
                    ? (sMeta.type === 'distance' ? `${adjustedValue(sTpl, s.adjust)}K` : sMeta.type === 'reps' ? `×${adjustedValue(sTpl, s.adjust)}` : `峰${adjustedValue(sTpl, s.adjust)}m`)
                    : ''
                  return (
                    <div key={s.id} style={{ background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '11px 13px' }}>
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                        <div style={{ fontSize: 14, fontWeight: 800, color: '#fff' }}>{s.name}{sAdjLabel && <span style={{ color: 'var(--fug)', fontWeight: 700 }}> · {sAdjLabel}</span>}</div>
                        <span style={{ fontSize: 10, fontWeight: 800, padding: '2px 7px', borderRadius: 6, background: s.plan_id ? 'rgba(199,88,255,.20)' : 'rgba(255,255,255,.08)', color: s.plan_id ? '#d9a8ff' : 'var(--tx-dim)', flexShrink: 0, whiteSpace: 'nowrap' }}>
                          {s.plan_id ? `📋 ${s.plan_name || '訓練計畫'}` : '手動'}
                        </span>
                      </div>
                      <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 3, fontVariantNumeric: 'tabular-nums' }}>
                        {CATEGORY_LABELS[s.category] || s.category} · {s.planned_km.toFixed(1)} K · {fmtDuration(s.planned_min)}
                      </div>
                      <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                        <button
                          disabled={pickerBusy || removingId === s.id}
                          onClick={() => { const code = s.template_code; const lvl = levels.find((l) => l.id === s.pace_level) ?? null; const adj = s.adjust; closePicker(); startWorkout(code, lvl, adj) }}
                          style={{ flex: 1, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }}
                        >▶ 開始此課表</button>
                        <button disabled={pickerBusy || removingId === s.id} onClick={() => removeSchedule(s.id)} style={{ background: 'none', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 9, padding: '9px 14px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }}>🗑 移除</button>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}

            <div style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 800, margin: '12px 2px 8px' }}>+ 加課表</div>

            {/* 配速等級（預設沿用上次選的等級） */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '9px 13px', marginBottom: 12 }}>
              <span style={{ fontSize: 12, fontWeight: 800, color: '#fff', flexShrink: 0 }}>配速等級</span>
              <select value={pickerLevelId ?? ''} onChange={(e) => setPickerLevelId(Number(e.target.value))} style={levelSelect}>
                {levels.map((l) => <option key={l.id} value={l.id}>Lv.{l.id} · {l.label}</option>)}
              </select>
            </div>

            {pickerErr && <div style={{ fontSize: 12, color: '#ff6b6b', textAlign: 'center', margin: '4px 0 10px' }}>{pickerErr}</div>}

            <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
              {groups.length === 0 && <div style={{ ...emptyBox, padding: '16px 4px' }}>目前尚無課表庫內容</div>}
              {groups.map(([cat, templates]) => (
                <div key={cat}>
                  <div style={{ fontSize: 11, fontWeight: 800, color: 'var(--tx-faint)', marginBottom: 6, letterSpacing: '.05em' }}>{CATEGORY_LABELS[cat] || cat}</div>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    {templates.map((t) => {
                      const adj = pickerAdjust[t.code] ?? 0
                      const meta = adjustMeta(t)
                      const resolved = pickerLevel ? resolveTemplate(t.segments, pickerLevel, t.adjust_type, adj) : []
                      const alreadyIn = !!pickerDay?.scheduled.some((s) => s.template_code === t.code)
                      return (
                        <div key={t.code} style={{ background: 'rgba(255,255,255,.03)', border: '1px solid var(--line-2)', borderRadius: 11, padding: '9px 12px' }}>
                          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                            <div style={{ fontSize: 13, fontWeight: 800, color: '#fff' }}>{t.name}{alreadyIn && <span style={{ color: 'var(--fug)', fontWeight: 700 }}> ✓</span>}</div>
                            <button disabled={pickerBusy || !pickerLevel} onClick={() => saveSchedule(t)} style={{ flexShrink: 0, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 11.5, fontFamily: 'inherit' }}>+ 加入</button>
                          </div>
                          <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 4, fontVariantNumeric: 'tabular-nums' }}>總距離 {totalKm(resolved)} K · 預估 {fmtDuration(estMinutes(resolved))}</div>
                          {meta.type !== 'none' && (() => {
                            const val = adjustedValue(t, adj)
                            const atMin = val <= meta.min
                            const atMax = val >= meta.max
                            return (
                              <div style={adjustRowSmall}>
                                <button type="button" onClick={() => bumpPicker(t.code, -1)} disabled={atMin} style={{ ...adjustBtnSmall, opacity: atMin ? 0.4 : 1 }}>−</button>
                                <span style={adjustValSmall}>{currentValue(t, adj)}</span>
                                <button type="button" onClick={() => bumpPicker(t.code, 1)} disabled={atMax} style={{ ...adjustBtnSmall, opacity: atMax ? 0.4 : 1 }}>＋</button>
                              </div>
                            )
                          })()}
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* 一鍵安排課表 modal（P3）：填跑力資料 + 目標賽事(或週數) → 自動產生一組訓練計畫 */}
      {showAutoPlan && (
        <div data-skin="default" onClick={() => !apBusy && setShowAutoPlan(false)} style={{ position: 'fixed', inset: 0, zIndex: 3600, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'flex-end', justifyContent: 'center' }}>
          <div onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 440, maxHeight: '86dvh', overflowY: 'auto', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--line-2)', borderTopLeftRadius: 18, borderTopRightRadius: 18, boxShadow: '0 -12px 40px rgba(0,0,0,.6)', padding: '16px 18px 22px' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ fontSize: 15, fontWeight: 900, color: '#fff' }}>⚡ 一鍵安排課表</div>
              <button disabled={apBusy} onClick={() => setShowAutoPlan(false)} style={{ background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 20, cursor: 'pointer', lineHeight: 1, padding: 4 }}>×</button>
            </div>
            <p style={{ fontSize: 11.5, color: 'var(--tx-dim)', margin: '8px 2px 14px', lineHeight: 1.7 }}>填一下你的跑力資料，系統會依目標賽事（或指定週數）自動排一組課表到訓練月曆。</p>

            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <label style={apField}>
                <span style={apLabel}>跑齡</span>
                <select value={apRunningAge} onChange={(e) => onRunningAgeChange(e.target.value as AutoPlanRequest['running_age'])} style={levelSelect}>
                  {RUNNING_AGE_OPTIONS.map((o) => <option key={o.id} value={o.id}>{o.label}</option>)}
                </select>
              </label>

              <label style={apField}>
                <span style={apLabel}>1km 最快</span>
                <input value={apBest1km} onChange={(e) => setApBest1km(e.target.value)} placeholder="例如 4:30 或 270（秒）" style={apInput} />
              </label>

              <div style={{ display: 'flex', gap: 10 }}>
                <label style={{ ...apField, flex: 1 }}>
                  <span style={apLabel}>最長距離（km）</span>
                  <input type="number" min={0} step={0.1} value={apLongestKm} onChange={(e) => setApLongestKm(e.target.value)} style={apInput} />
                </label>
                <label style={{ ...apField, flex: 1 }}>
                  <span style={apLabel}>最長時間（分）</span>
                  <input type="number" min={0} step={1} value={apLongestMin} onChange={(e) => setApLongestMin(e.target.value)} style={apInput} />
                </label>
              </div>

              <div style={apField}>
                <span style={apLabel}>目標賽事</span>
                <div style={{ display: 'flex', gap: 8 }}>
                  <button type="button" onClick={() => setApHasRace(true)} style={{ ...toggleBtn, ...(apHasRace ? toggleBtnActive : {}) }}>有目標賽事</button>
                  <button type="button" onClick={() => setApHasRace(false)} style={{ ...toggleBtn, ...(!apHasRace ? toggleBtnActive : {}) }}>先練體能</button>
                </div>
              </div>

              {apHasRace ? (
                // 手機直向堆疊，避免 date input 與 select 在窄螢幕同列擠壓重疊
                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <label style={apField}>
                    <span style={apLabel}>賽事日期</span>
                    <input type="date" value={apRaceDate} onChange={(e) => setApRaceDate(e.target.value)} style={apInput} />
                  </label>
                  <label style={apField}>
                    <span style={apLabel}>賽事距離</span>
                    <select value={apRaceDistance} onChange={(e) => setApRaceDistance(e.target.value as NonNullable<AutoPlanRequest['race_distance']>)} style={levelSelect}>
                      {RACE_DISTANCE_OPTIONS.map((o) => <option key={o.id} value={o.id}>{o.label}</option>)}
                    </select>
                  </label>
                </div>
              ) : (
                <label style={apField}>
                  <span style={apLabel}>計畫週數</span>
                  <select value={apWeeks} onChange={(e) => setApWeeks(Number(e.target.value))} style={levelSelect}>
                    {WEEKS_OPTIONS.map((w) => <option key={w} value={w}>{w} 週</option>)}
                  </select>
                </label>
              )}

              <div style={apField}>
                <span style={apLabel}>預定休息日（其餘為訓練日，至少留 1 天）</span>
                <div style={{ display: 'flex', gap: 5 }}>
                  {WEEKDAY_MON_FIRST.map((w, idx) => {
                    const active = apRestDays.includes(idx)
                    return (
                      <button key={idx} type="button" onClick={() => toggleRestDay(idx)} style={{ ...toggleBtn, flex: 1, padding: '8px 0', fontSize: 12, ...(active ? toggleBtnActive : {}) }}>{w}</button>
                    )
                  })}
                </div>
                <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>已選休息 {apRestDays.length} 天・訓練 {7 - apRestDays.length} 天</div>
              </div>
            </div>

            {apErr && <div style={{ fontSize: 12, color: '#ff6b6b', textAlign: 'center', margin: '12px 0 0' }}>{apErr}</div>}

            <button disabled={apBusy} onClick={submitAutoPlan} style={{ ...startBtn, marginTop: 16, opacity: apBusy ? 0.6 : 1 }}>{apBusy ? '產生中…' : '產生訓練計畫'}</button>
          </div>
        </div>
      )}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const emptyBox: React.CSSProperties = { color: 'var(--tx-dim)', fontSize: 13.5, lineHeight: 1.9, padding: '32px 10px', textAlign: 'center' }
const levelSelect: React.CSSProperties = { flex: 1, minWidth: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, fontFamily: 'inherit' }
const tplCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '11px 13px' }
const startBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 0', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
// 課表微調（migration 085）「− 值 ＋」列：課表庫卡片版與選課表 modal 精簡版
const adjustRow: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 12, marginTop: 10, background: 'var(--bg-2)', borderRadius: 9, padding: '6px 10px' }
const adjustBtn: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line-2)', color: 'var(--tx)', borderRadius: 7, width: 28, height: 28, fontSize: 15, fontWeight: 800, cursor: 'pointer', lineHeight: 1, fontFamily: 'inherit' }
const adjustVal: React.CSSProperties = { fontSize: 12, fontWeight: 800, color: 'var(--tx)', minWidth: 76, textAlign: 'center', fontVariantNumeric: 'tabular-nums' }
const adjustRowSmall: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 10, marginTop: 8, background: 'rgba(255,255,255,.04)', borderRadius: 8, padding: '5px 8px' }
const adjustBtnSmall: React.CSSProperties = { background: 'rgba(255,255,255,.06)', border: '1px solid var(--line-2)', color: '#fff', borderRadius: 6, width: 24, height: 24, fontSize: 13, fontWeight: 800, cursor: 'pointer', lineHeight: 1, fontFamily: 'inherit' }
const adjustValSmall: React.CSSProperties = { fontSize: 11, fontWeight: 800, color: '#fff', minWidth: 70, textAlign: 'center', fontVariantNumeric: 'tabular-nums' }
const navBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx)', borderRadius: 10, width: 34, height: 34, fontSize: 20, cursor: 'pointer', flexShrink: 0, lineHeight: 1 }
const autoPlanBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '8px 14px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit', whiteSpace: 'nowrap' }
const apField: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 5 }
const apLabel: React.CSSProperties = { fontSize: 11.5, fontWeight: 800, color: 'var(--tx-dim)' }
const apInput: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, fontFamily: 'inherit', width: '100%', boxSizing: 'border-box' }
const toggleBtn: React.CSSProperties = { flex: 1, background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 9, padding: '8px 0', cursor: 'pointer', fontSize: 12.5, fontWeight: 700, fontFamily: 'inherit' }
const toggleBtnActive: React.CSSProperties = { background: 'var(--fug)', borderColor: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800 }
const sourceChip: React.CSSProperties = { flexShrink: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', color: 'var(--tx-dim)', borderRadius: 20, padding: '6px 14px', fontSize: 12, fontWeight: 700, cursor: 'pointer', whiteSpace: 'nowrap', fontFamily: 'inherit' }
const sourceChipActive: React.CSSProperties = { background: 'var(--fug)', borderColor: 'var(--fug)', color: 'var(--fug-ink)' }
