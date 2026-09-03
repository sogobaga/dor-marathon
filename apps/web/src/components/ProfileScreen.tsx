'use client'

import { useEffect, useRef, useState } from 'react'
import { profileApi, paymentsApi, integrationsApi, followApi, settingsApi, activitiesApi, referralApi, gpsCalibApi, sourceLabel, type Profile, type MyRegistration, type MyOrder, type StravaStatus, type TerraStatus, type SyncedActivity, type FollowRow, type SiteSettings, type ReferralInfo, type VipCardInfo, type GpsCalibInfo, type DataSource } from '@/lib/api'
import { getUserToken, withUserAuth, SessionExpiredError } from '@/lib/userAuth'
import { readPendingGps, clearPendingGps, type PendingGpsRun } from '@/lib/pendingGps'
import { useDashboard } from '@/lib/useDashboard'
import { APP_VERSION } from '@/lib/version'
import { useVipSubscribeFlow } from '@/lib/useVipSubscribeFlow'
import MemberPanel from './MemberPanel'
import UpgradeVipModal from './UpgradeVipModal'
import BindCardModal from './BindCardModal'
import PushToggle from './PushToggle'
import ScrollArea from './ScrollArea'
import { submitEcpayForm } from '@/lib/ecpay'

const GENDERS = [
  { v: '', t: '未填' },
  { v: 'male', t: '男' },
  { v: 'female', t: '女' },
  { v: 'other', t: '其他' },
]
const REG_STATUS: Record<string, { t: string; c: string }> = {
  paid: { t: '報名完成', c: 'var(--fug)' },
  pending: { t: '待繳費', c: 'var(--gold)' },
  cancelled: { t: '已取消', c: 'var(--tx-faint)' },
}
const ITEM_LABEL: Record<string, string> = {
  entry: '報名費', addon: '加購', discount: '優惠折抵',
  vip_month: 'VIP 月費訂閱', vip_year: 'VIP 年費訂閱', // VIP 訂閱訂單（無賽事，見 orders.race_id 可空）
}
const FLAG_LABEL: Record<string, string> = {
  multi_device_duplicate: '多裝置重複',
  cross_account_duplicate: '跨帳號重複',
  duplicate: '重複資料',
}
// Terra 手錶直連（Phase 1）：已知品牌顯示中文慣用大小寫，未知品牌（後端新增但前端未同步）退回首字大寫。
const TERRA_BRAND_LABEL: Record<string, string> = {
  garmin: 'Garmin', coros: 'COROS', polar: 'Polar', suunto: 'Suunto', wahoo: 'Wahoo',
}
function terraBrandName(provider: string): string {
  const key = provider.toLowerCase()
  return TERRA_BRAND_LABEL[key] ?? (key.charAt(0).toUpperCase() + key.slice(1))
}
// GPS 距離校正（見 internal/gpscalib，2026-08-30）
const GPS_CALIB_STATUS_LABEL: Record<string, string> = {
  warming: '暖機中（配對數不足）',
  active: '校正中',
  unstable: '資料不一致',
  stale: '已過期（逾 120 天無新配對）',
  frozen: '後台已鎖定',
}
const GPS_CALIB_STATUS_COLOR: Record<string, string> = {
  warming: 'var(--tx-dim)',
  active: 'var(--fug)',
  unstable: 'var(--hunt)',
  stale: 'var(--tx-faint)',
  frozen: 'var(--gold)',
}
const GPS_CALIB_SRC_LABEL: Record<string, string> = { strava: 'Strava', garmin: 'Garmin', coros: 'COROS' }
const GPS_CALIB_REJECT_LABEL: Record<string, string> = {
  flagged: '已標記異常',
  ambiguous: '配對不明確',
  partial: '距離差異過大',
  short: '距離過短',
  edge: '起訖時間差過大',
  range: '比值超出合理範圍',
  other_source: '非目前參考來源',
}

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
}
function fmtDate(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function paceStr(sec: number) {
  if (!sec || sec <= 0) return '—'
  return `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`
}

export default function ProfileScreen({ onBack, focusRaceID, initialTab, onOpenPersonalTasks, onOpenExplore, onOpenGallery, onOpenTitle, onOpenAchievement, onOpenTraining, onOpenPerks, onOpenMonopoly, onOpenRewards, onOpenHeroes, onOpenRunMeet }: { onBack: () => void; focusRaceID?: string; initialTab?: 'info' | 'sports' | 'records' | 'follows'; onOpenPersonalTasks?: () => void; onOpenExplore?: () => void; onOpenGallery?: () => void; onOpenTitle?: () => void; onOpenAchievement?: () => void; onOpenTraining?: () => void; onOpenPerks?: () => void; onOpenMonopoly?: () => void; onOpenRewards?: () => void; onOpenHeroes?: () => void; onOpenRunMeet?: () => void }) {
  const [p, setP] = useState<Profile | null>(null)
  const [regs, setRegs] = useState<MyRegistration[] | null>(null)
  const [payOrder, setPayOrder] = useState<MyOrder | null>(null)
  const [paying, setPaying] = useState(false)
  const [err, setErr] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [strava, setStrava] = useState<StravaStatus | null>(null)
  const [stravaBusy, setStravaBusy] = useState(false)
  const [stravaMsg, setStravaMsg] = useState('')
  // Terra 手錶直連（Phase 1）：terra===null 或 !terra.enabled 時卡片維持「即將開放」（production 尚未設定憑證前的常態）
  const [terra, setTerra] = useState<TerraStatus | null>(null)
  const [terraBusy, setTerraBusy] = useState(false)
  const [terraMsg, setTerraMsg] = useState('')
  const [dataSrcMsg, setDataSrcMsg] = useState('') // 里程優先來源設定錯誤訊息（如選到尚未連接的來源）
  const terraPollTimers = useRef<ReturnType<typeof setTimeout>[]>([]) // auth webhook 可能晚到，導回後輪詢用；卸載時清空
  const [activities, setActivities] = useState<SyncedActivity[] | null>(null)
  const [syncing, setSyncing] = useState(false)
  // GPS 距離校正（見 internal/gpscalib，2026-08-30）：入口白名單 shown 才抓；locked 只顯示鎖定卡片、不打 API。
  const [gpsCalib, setGpsCalib] = useState<GpsCalibInfo | null>(null)
  const [gpsCalibBusy, setGpsCalibBusy] = useState(false)
  const [gpsCalibErr, setGpsCalibErr] = useState('')
  const [gpsCalibDetail, setGpsCalibDetail] = useState(false) // 展開最近配對/係數歷程
  const [reminderBusy, setReminderBusy] = useState(false) // 團練開跑前 Email 提醒開關送出中
  const { dash, revalidate: loadDashboard } = useDashboard() // 共用會員儀表板快取（與首頁會員卡同一份）
  const [tab, setTab] = useState<'info' | 'sports' | 'records' | 'follows'>(initialTab ?? 'info')
  // 本機尚未上傳的 GPS（里程優先來源=外部來源時，track 頁結束不自動上傳，留給這裡決定）
  const [pending, setPending] = useState<PendingGpsRun | null>(null)
  const [pendingAsk, setPendingAsk] = useState(false) // 「是否等待外部來源同步」二次確認彈窗
  const [pendingBusy, setPendingBusy] = useState(false)
  const [pendingErr, setPendingErr] = useState('')
  const [uploadingAvatar, setUploadingAvatar] = useState(false)
  const [codeCopied, setCodeCopied] = useState(false)
  const [showUpgrade, setShowUpgrade] = useState(false)
  const vipFlow = useVipSubscribeFlow() // VIP 訂閱 Phase E：Subscribe → BindCardModal（見 lib/useVipSubscribeFlow）
  // 付款卡片（VIP 訂閱 Phase E 卡片管理）
  const [card, setCard] = useState<VipCardInfo | null>(null)
  const [showUnbind, setShowUnbind] = useState(false)
  const [unbindBusy, setUnbindBusy] = useState(false)
  const [unbindErr, setUnbindErr] = useState('')
  const [unbindDone, setUnbindDone] = useState(false) // 解綁成功回饋（三態顯示：未綁定/已綁定/剛解除）
  // 推廣連結：累積里程 ≥10km 才能產生；成功後存推薦碼/統計，供組連結與複製
  const [referral, setReferral] = useState<ReferralInfo | null>(null)
  const [referralBusy, setReferralBusy] = useState(false)
  const [referralErr, setReferralErr] = useState('')
  const [referralCopied, setReferralCopied] = useState(false)
  const [follows, setFollows] = useState<FollowRow[] | null>(null)
  const [site, setSite] = useState<SiteSettings | null>(null) // 全站外觀設定（含 Strava 標章雙版本 URL）
  // 取消報名 / 分級退費
  const [cancelTarget, setCancelTarget] = useState<MyRegistration | null>(null) // 開啟「申請取消報名」對話框的那筆報名
  const [cancelReason, setCancelReason] = useState('')
  const [cancelSubmitting, setCancelSubmitting] = useState(false)
  const [cancelErr, setCancelErr] = useState('')
  const [recMsg, setRecMsg] = useState('') // 報名紀錄頁籤操作成功提示（申請取消／撤回）
  const [expandedRegId, setExpandedRegId] = useState<string | null>(null) // 報名紀錄收合/展開：預設只顯示活動名稱+狀態，點擊展開明細
  const [withdrawBusy, setWithdrawBusy] = useState<string | null>(null) // 撤回中的 registration_id
  function loadFollows() {
    withUserAuth((t) => profileApi.follows(t)).then((r) => setFollows(r.following)).catch(() => {})
  }
  async function unfollow(userId: string) {
    try {
      await withUserAuth((t) => followApi.unfollow(t, userId))
      setFollows((f) => (f ? f.filter((x) => x.user_id !== userId) : f))
      loadDashboard()
    } catch { /* ignore */ }
  }
  // 產生（或取得既有）本人的推廣連結；未達 10km 資格時前端已 gate 不顯示按鈕，這裡仍防禦性接住 403
  async function generateReferral() {
    setReferralBusy(true); setReferralErr('')
    try {
      const res = await withUserAuth((t) => referralApi.generate(t))
      setReferral(res)
    } catch (e: any) {
      setReferralErr(e?.status === 403 ? '需累積完成 10 公里' : (e?.message || '產生失敗，請稍後再試'))
    } finally {
      setReferralBusy(false)
    }
  }
  // 付款卡片：查詢現況（供「個人資料」頁籤顯示）；解除綁卡（有 active 訂閱時後端 409，如實顯示引導先取消訂閱）。
  function loadCard() {
    withUserAuth((t) => profileApi.vipCard(t)).then(setCard).catch(() => {})
  }
  async function unbindCard() {
    setUnbindBusy(true); setUnbindErr('')
    try {
      await withUserAuth((t) => profileApi.vipCardDelete(t))
      setCard({ bound: false })
      setShowUnbind(false)
      setUnbindDone(true) // 明確的成功回饋：行內顯示「已解除綁定 ✓」，避免使用者以為解綁失敗
    } catch (e: any) {
      setUnbindErr(e?.message || '解除綁卡失敗，請稍後再試')
    } finally {
      setUnbindBusy(false)
    }
  }
  function profilePayload(x: Profile): Partial<Profile> {
    return { name: x.name, avatar_url: x.avatar_url, real_name: x.real_name, nickname: x.nickname, phone: x.phone, address: x.address, birthday: x.birthday, gender: x.gender }
  }
  async function onAvatar(file: File) {
    if (!p) return
    setUploadingAvatar(true); setErr('')
    try {
      const { url } = await withUserAuth((t) => profileApi.uploadAvatar(t, file))
      const res = await withUserAuth((t) => profileApi.updateMe(t, { ...profilePayload(p), avatar_url: url }))
      setP(res.profile)
      loadDashboard()
    } catch (e: any) {
      setErr(e?.message || '頭像上傳失敗')
    } finally {
      setUploadingAvatar(false)
    }
  }

  function loadStrava() {
    withUserAuth((t) => integrationsApi.stravaStatus(t))
      .then((s) => { setStrava(s); if (s.connected) loadActivities() })
      .catch(() => {})
  }
  // Terra 手錶直連狀態；載入失敗（含尚未登入的暫態）就不管它，不影響 Strava 卡片。
  function loadTerra() {
    withUserAuth((t) => integrationsApi.terraStatus(t))
      .then(setTerra)
      .catch(() => {})
  }
  // 導回後品牌可能還沒進 connections（Terra auth webhook 晚到），輪詢至多 5 次、每 2 秒一次，
  // 該品牌一出現就停止；計時器記進 ref，卸載時（見下方 useEffect）全部清掉避免記憶體洩漏/setState after unmount。
  function pollTerraForBrand(provider: string, triesLeft: number) {
    if (triesLeft <= 0) return
    const timer = setTimeout(() => {
      withUserAuth((t) => integrationsApi.terraStatus(t))
        .then((s) => {
          setTerra(s)
          if (!s.connections.some((c) => c.provider === provider)) pollTerraForBrand(provider, triesLeft - 1)
        })
        .catch(() => pollTerraForBrand(provider, triesLeft - 1))
    }, 2000)
    terraPollTimers.current.push(timer)
  }
  useEffect(() => {
    return () => { terraPollTimers.current.forEach(clearTimeout); terraPollTimers.current = [] }
  }, [])
  function loadActivities() {
    withUserAuth((t) => integrationsApi.stravaActivities(t)).then((r) => setActivities(r.activities)).catch(() => {})
  }
  async function syncNow() {
    setSyncing(true)
    setStravaMsg('')
    try {
      const r = await withUserAuth((t) => integrationsApi.stravaSync(t))
      setStravaMsg(`同步完成：新增 ${r.imported} 筆${r.duplicates ? `、排除重複 ${r.duplicates} 筆` : ''}`)
      loadActivities()
    } catch (e: any) {
      setStravaMsg(e?.message || '同步失敗')
    } finally {
      setSyncing(false)
    }
  }

  // 本機尚未上傳的 GPS（不管是否連 Strava，都可能有——里程優先來源=外部來源時 track 頁結束會保留在本機）
  useEffect(() => { setPending(readPendingGps()) }, [])

  // GPS 距離校正：僅在入口=shown 才打 API（locked/hidden 打了也是 403，不必浪費請求）
  function loadGpsCalib() {
    withUserAuth((t) => gpsCalibApi.get(t)).then(setGpsCalib).catch(() => {})
  }
  useEffect(() => { if (dash?.gps_calib_entry === 'shown') loadGpsCalib() }, [dash?.gps_calib_entry])
  async function toggleGpsCalib() {
    if (!gpsCalib) return
    setGpsCalibBusy(true); setGpsCalibErr('')
    try {
      const r = await withUserAuth((t) => gpsCalibApi.setEnabled(t, !gpsCalib.enabled))
      setGpsCalib(r)
    } catch (e: any) { setGpsCalibErr(e?.message || '設定失敗') }
    finally { setGpsCalibBusy(false) }
  }
  // 團練開跑前 Email 提醒開關（見 internal/runmeet/reminder.go；migration 163 users.runmeet_reminder_email）。
  // 站內信不受此開關影響，一律照發；這裡只管 Email。
  async function toggleReminderEmail() {
    if (!dash) return
    const next = !dash.runmeet_reminder_email
    setReminderBusy(true)
    try {
      await withUserAuth((t) => profileApi.setNotifyPrefs(t, { runmeet_reminder_email: next }))
      loadDashboard() // 讓共用 dashboard 快取跟著重抓，開關狀態立即反映
    } catch {
      /* ignore，維持原值，使用者可再按一次重試 */
    } finally {
      setReminderBusy(false)
    }
  }
  async function recomputeGpsCalib() {
    setGpsCalibBusy(true); setGpsCalibErr('')
    try {
      const r = await withUserAuth((t) => gpsCalibApi.recompute(t))
      setGpsCalib(r)
    } catch (e: any) { setGpsCalibErr(e?.status === 429 ? '請稍候再試（60 秒限流）' : e?.message || '重新計算失敗') }
    finally { setGpsCalibBusy(false) }
  }

  async function uploadPending() {
    if (!pending) return
    const token = getUserToken(); if (!token) return
    setPendingBusy(true); setPendingErr('')
    try {
      await withUserAuth((t) => activitiesApi.uploadGps(t, {
        started_at: new Date(pending.start).toISOString(),
        ended_at: new Date(pending.endedAt).toISOString(),
        points: pending.points,
        client_version: APP_VERSION,
      }))
      clearPendingGps(); setPending(null); setPendingAsk(false)
      loadActivities() // 刷新已同步活動（該清單含 GPS 來源）
      loadDashboard() // GPS 距離校正（見 internal/gpscalib）：上傳後係數可能被 RecomputeAsync 更新，
      // 讓共用 dashboard 快取（與 /track 頁 calibKRef 開跑快照同一份，見 useDashboard.ts）跟著重抓，
      // 避免下一趟開跑仍拿到舊係數（對抗式審查修正）。
    } catch (e: any) { setPendingErr(e?.message || '上傳失敗，請稍後再試') }
    finally { setPendingBusy(false) }
  }

  // 里程優先來源：樂觀切換，失敗（如選到尚未連接的來源，後端回 400 not_connected）就退回原值＋顯示訊息
  async function selectDataSource(src: DataSource) {
    const prev = p?.preferred_data_source ?? 'gps'
    if (prev === src) return
    setDataSrcMsg('')
    setP((c) => (c ? { ...c, preferred_data_source: src } : c))
    try {
      await withUserAuth((t) => profileApi.setDataSource(t, src))
    } catch (e: any) {
      setP((c) => (c ? { ...c, preferred_data_source: prev } : c))
      setDataSrcMsg(e?.message === 'not_connected' ? '這個來源尚未連接，請先連接後再選擇' : '設定失敗，請再試一次')
    }
  }

  useEffect(() => {
    if (!getUserToken()) {
      setErr('請先登入')
      return
    }
    loadStrava()
    loadTerra()
    settingsApi.get().then((r) => setSite(r.settings)).catch(() => {}) // Strava 標章雙版本 URL
    // 處理 Strava／Terra 導回參數（同一頁面、同一組 query string 邏輯）
    if (typeof window !== 'undefined') {
      const sp = new URLSearchParams(window.location.search)
      let touched = false
      const s = sp.get('strava')
      if (s) {
        setStravaMsg(s === 'connected' ? '✓ 已連接 Strava，正在同步近期活動…'
          : s === 'denied' ? '已取消授權'
          : 'Strava 連接失敗，請再試一次')
        sp.delete('strava')
        touched = true
      }
      const tr = sp.get('terra')
      if (tr) {
        const provider = sp.get('provider') || ''
        const reason = sp.get('reason') || ''
        if (tr === 'connected') {
          setTerraMsg(`✓ 已連接 ${provider ? terraBrandName(provider) : '裝置'}，之後裝置同步的跑步會自動匯入（僅計算連接之後的紀錄）`)
          loadTerra()
          if (provider) pollTerraForBrand(provider, 5) // webhook 可能晚到，補幾次輪詢
        } else {
          setTerraMsg(`連接未完成，請再試一次${reason ? `（${reason}）` : ''}`)
        }
        sp.delete('terra'); sp.delete('provider'); sp.delete('reason')
        touched = true
      }
      if (touched) {
        const qs = sp.toString()
        window.history.replaceState(null, '', window.location.pathname + (qs ? `?${qs}` : ''))
      }
    }
    loadDashboard()
    loadFollows()
    // 回顯既有推廣連結：只查現況、不產生，避免重掛載後（例如切分頁再回來）誤退回「產生」按鈕
    withUserAuth((t) => referralApi.get(t))
      .then((r) => { if (r.referral_code) setReferral(r) })
      .catch(() => {})
    loadCard() // 付款卡片現況（VIP 訂閱 Phase E 卡片管理）
    withUserAuth((t) => profileApi.getMe(t))
      .then((r) => setP(r.profile))
      .catch((e) => setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '載入失敗'))

    loadRegs()
  }, [focusRaceID])

  function loadRegs() {
    withUserAuth((t) => profileApi.registrations(t))
      .then((r) => {
        setRegs(r.registrations)
        // 從「前往繳費」進來：自動開啟該賽事待繳費訂單
        if (focusRaceID) {
          const target = r.registrations.find((x) => x.race_id === focusRaceID && x.status === 'pending' && x.order_id)
          if (target?.order_id) openPay(target.order_id)
        }
      })
      .catch(() => {})
  }

  async function openPay(orderID: string) {
    try {
      const { order } = await withUserAuth((t) => profileApi.order(t, orderID))
      setPayOrder(order)
    } catch (e: any) {
      setErr(e?.message || '載入繳費資訊失敗')
    }
  }

  function openCancelModal(r: MyRegistration) {
    setCancelTarget(r)
    setCancelReason('')
    setCancelErr('')
  }

  async function submitCancelRequest() {
    if (!cancelTarget) return
    const reason = cancelReason.trim()
    if (!reason) { setCancelErr('請填寫取消原因'); return }
    setCancelSubmitting(true)
    setCancelErr('')
    try {
      await withUserAuth((t) => profileApi.cancelRequest(t, cancelTarget.registration_id, reason))
      setCancelTarget(null)
      setCancelReason('')
      setRecMsg('已送出取消申請，我們將盡快審核')
      loadRegs()
    } catch (e: any) {
      setCancelErr(e instanceof SessionExpiredError ? '登入已過期，請重新登入' : e?.message || '送出失敗，請稍後再試')
    } finally {
      setCancelSubmitting(false)
    }
  }

  async function withdrawCancel(r: MyRegistration) {
    if (!window.confirm('確定要撤回這筆取消申請嗎？')) return
    setWithdrawBusy(r.registration_id)
    setRecMsg('')
    try {
      await withUserAuth((t) => profileApi.withdrawCancelRequest(t, r.registration_id))
      setRecMsg('已撤回取消申請')
      loadRegs()
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請重新登入' : e?.message || '撤回失敗，請稍後再試')
    } finally {
      setWithdrawBusy(null)
    }
  }

  async function goEcpay() {
    if (!payOrder) return
    setPaying(true)
    try {
      const { action_url, params } = await withUserAuth((t) => paymentsApi.ecpayCheckout(t, payOrder.id))
      submitEcpayForm(action_url, params) // 導去綠界，不會 return
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請重新登入' : e?.message || '無法前往付款')
      setPaying(false)
    }
  }

  async function connectStrava() {
    setStravaBusy(true)
    try {
      // 帶回程網址＝目前頁面（同源），授權後導回這裡，session 不會掉
      const returnUrl = window.location.origin + window.location.pathname
      const { url } = await withUserAuth((t) => integrationsApi.stravaConnectUrl(t, returnUrl))
      window.location.href = url // 導去 Strava 授權
    } catch (e: any) {
      setStravaMsg(e?.message || '無法連接 Strava')
      setStravaBusy(false)
    }
  }
  async function disconnectStrava() {
    if (!window.confirm('中斷 Strava 連接？已同步的 Strava 活動將一併刪除；你已獲得的 EXP/DP 等獎勵不受影響。')) return
    setStravaBusy(true)
    try {
      await withUserAuth((t) => integrationsApi.stravaDisconnect(t))
      setStrava({ connected: false, enabled: strava?.enabled ?? true })
      setStravaMsg('已中斷 Strava 連接')
    } catch (e: any) {
      setStravaMsg(e?.message || '中斷失敗')
    } finally {
      setStravaBusy(false)
    }
  }
  async function connectTerra() {
    setTerraBusy(true)
    setTerraMsg('')
    try {
      // 帶回程網址＝目前頁面（同源），Terra widget 完成後導回這裡，session 不會掉（同 connectStrava 作法）
      const returnUrl = window.location.origin + window.location.pathname
      const { url } = await withUserAuth((t) => integrationsApi.terraConnectUrl(t, returnUrl))
      window.location.href = url // 導去 Terra 連接 widget
    } catch (e: any) {
      setTerraMsg(e?.status === 503 ? '裝置連接功能尚未開放，請稍後再試' : (e?.message || '無法連接，請再試一次'))
      setTerraBusy(false)
    }
  }
  async function disconnectTerra(provider: string) {
    const brand = terraBrandName(provider)
    if (!window.confirm(`中斷 ${brand} 連接？之後 ${brand} 裝置同步的跑步將不再自動匯入；已獲得的 EXP/DP 等獎勵不受影響。`)) return
    setTerraBusy(true)
    try {
      await withUserAuth((t) => integrationsApi.terraDisconnect(t, provider))
      setTerraMsg(`已中斷 ${brand} 連接`)
      loadTerra()
    } catch (e: any) {
      setTerraMsg(e?.message || '中斷失敗')
    } finally {
      setTerraBusy(false)
    }
  }
  // 手動補匯：Terra webhook 不保證會送 activity 事件（2026-09-03 COROS 真機實測：只收到 daily，沒有 activity），
  // 裝置同步的跑步理論上會自動匯入，但沒進來時給使用者一個主動向 Terra 要近期紀錄的按鈕。
  async function importTerra(provider: string) {
    const brand = terraBrandName(provider)
    setTerraBusy(true)
    setTerraMsg('')
    try {
      const r = await withUserAuth((t) => integrationsApi.terraImport(t, provider))
      if (r.async) {
        setTerraMsg(`已向 Terra 請求重送近 ${r.days} 天的紀錄，資料會在幾分鐘內自動匯入，請稍後再回來看`)
      } else {
        let msg = `${brand} 匯入完成：新增 ${r.imported} 筆`
        if (r.duplicate > 0) msg += `、重複 ${r.duplicate} 筆`
        if (r.skipped_before_connect > 0) msg += `、${r.skipped_before_connect} 筆為連接前的紀錄未計入`
        if (r.skipped_non_running > 0) msg += `、${r.skipped_non_running} 筆非跑步／走路類`
        if (r.errors > 0) msg += `、${r.errors} 筆失敗`
        msg += r.fetched === 0
          ? `（Terra 近 ${r.days} 天沒有回傳任何活動——請確認 ${brand} App 已把活動同步到雲端）`
          : `（Terra 回傳 ${r.fetched} 筆）`
        setTerraMsg(msg)
        if (r.imported > 0) loadActivities()
      }
    } catch (e: any) {
      setTerraMsg(e?.message || '匯入失敗')
    } finally {
      setTerraBusy(false)
    }
  }

  function set<K extends keyof Profile>(k: K, v: Profile[K]) {
    setP((prev) => (prev ? { ...prev, [k]: v } : prev))
    setSaved(false)
  }

  async function save() {
    if (!p) return
    setErr('')
    setSaving(true)
    try {
      const res = await withUserAuth((t) => profileApi.updateMe(t, profilePayload(p)))
      setP(res.profile)
      setSaved(true)
      loadDashboard()
    } catch (e: any) {
      setErr(e instanceof SessionExpiredError ? '登入已過期，請回上一頁重新登入' : e?.message || '儲存失敗')
    } finally {
      setSaving(false)
    }
  }

  // Terra 手錶直連品牌清單文案：enabled 後有 providers 就照後端開放的品牌顯示，否則退回全品牌（卡片仍在「即將開放」，此值不會被用到）
  const terraBrandList = (terra?.providers?.length ? terra.providers.map(terraBrandName) : ['Garmin', 'COROS', 'Polar', 'Suunto', 'Wahoo']).join('／')

  // 里程優先來源：使用者實際已連接的來源，固定順序 gps → 手錶品牌 → strava（App GPS 永遠在，其餘依是否連接過濾）
  const connectedTerraProviders = new Set((terra?.connections ?? []).map((c) => c.provider.toLowerCase()))
  const connectedSources: DataSource[] = [
    'gps',
    ...(['garmin', 'coros', 'polar', 'suunto', 'wahoo'] as const).filter((b) => connectedTerraProviders.has(b)),
    ...(strava?.connected ? (['strava'] as const) : []),
  ]
  // 有效選擇：後端存的偏好若已不在目前已連接清單內（如來源後來被斷開）就退回 gps，避免畫面卡在一個選不到的來源
  const effectiveSource: DataSource = p?.preferred_data_source && connectedSources.includes(p.preferred_data_source)
    ? p.preferred_data_source
    : 'gps'
  const prefLabel = sourceLabel(effectiveSource)

  return (
    <>
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 10 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>會員管理</span>
        {/* 「加入社群」LINE 連結已於 2026-09-03 搬到首頁入口按鈕列（MemberPanel LINE_COMMUNITY_URL），此頁不再重複 */}
      </header>

      {err && <div style={{ color: 'var(--hunt)', padding: '8px 20px 0', fontSize: 13, flexShrink: 0 }}>{err}</div>}

      {/* 會員資訊面板：與首頁共用同一元件、內容一致（此頁頭像可上傳）。2026-09-03 使用者回饋改版：
          入口按鈕列（城市探索/卡片探索/成就探索…）搬去首頁常駐顯示，本頁 showEntries=false 只留會員卡本體，
          原本「COROS 式可上下拖曳面板」也一併拿掉，改回會員卡固定在上＋分頁列固定＋內容捲動的一般全頁版型。 */}
      <div style={{ padding: '4px 18px 0', flexShrink: 0 }}>
        <MemberPanel showEntries={false} onUploadAvatar={onAvatar} uploadingAvatar={uploadingAvatar} onOpenPersonalTasks={onOpenPersonalTasks} onOpenExplore={onOpenExplore} onOpenGallery={onOpenGallery} onOpenTitle={onOpenTitle} onOpenAchievement={onOpenAchievement} onOpenTraining={onOpenTraining} onOpenPerks={onOpenPerks} onOpenMonopoly={onOpenMonopoly} onOpenRewards={onOpenRewards} onOpenHeroes={onOpenHeroes} onOpenRunMeet={onOpenRunMeet} />
      </div>

      {/* 分頁列（個人資料/運動數據/報名紀錄/追蹤列表）：固定在會員卡下方、不隨內容捲動 */}
      <div style={{ display: 'flex', gap: 6, padding: '12px 18px 0', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
        {([['info', '個人資料'], ['sports', '運動數據'], ['records', '報名紀錄'], ['follows', '追蹤列表']] as const).map(([v, label]) => (
          <button key={v} onClick={() => setTab(v)} style={{
            padding: '8px 9px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14, whiteSpace: 'nowrap',
            color: tab === v ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: tab === v ? 700 : 400,
            borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
          }}>{label}</button>
        ))}
      </div>

      {/* 分頁內容（可捲動） */}
      <ScrollArea padding="14px 18px calc(20px + var(--cta-safe, 0px))">
        {!p && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}

        {/* 頁籤①個人資料 */}
        {tab === 'info' && p && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {/* 帳號資訊（唯讀，純文字呈現；非可編輯項目，不用輸入框樣式。帳號編碼保留一鍵複製） */}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 9, fontSize: 14, paddingBottom: 12, borderBottom: '1px solid var(--line)' }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, flexWrap: 'wrap' }}>
                <span style={{ color: 'var(--tx-faint)' }}>帳號：</span>
                <span style={{ fontFamily: 'monospace', fontWeight: 600, color: 'var(--tx)' }}>{dash?.account_code ? `#${dash.account_code}` : '…'}</span>
                <button type="button" onClick={() => { if (dash?.account_code) navigator.clipboard?.writeText(dash.account_code).then(() => { setCodeCopied(true); setTimeout(() => setCodeCopied(false), 1500) }).catch(() => {}) }}
                  style={{ marginLeft: 2, background: 'none', border: 'none', padding: 0, cursor: 'pointer', fontSize: 12, color: codeCopied ? 'var(--fug)' : 'var(--tx-dim)', textDecoration: 'underline' }}>{codeCopied ? '已複製' : '複製'}</button>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <span style={{ color: 'var(--tx-faint)' }}>會員身分：</span>
                <span style={{ fontWeight: dash?.is_vip ? 700 : 500, color: dash?.is_vip ? 'var(--gold)' : 'var(--tx)' }}>
                  {dash?.is_vip ? `VIP${dash.vip_expires_at ? ` (至 ${fmtDate(dash.vip_expires_at).slice(0, 10)})` : ''}` : '一般會員'}
                </span>
                {dash && !dash.is_vip && (
                  <button type="button" onClick={() => setShowUpgrade(true)}
                    style={{ background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 7, padding: '3px 10px', cursor: 'pointer', fontSize: 11.5, fontWeight: 800 }}>✦ 升級VIP</button>
                )}
              </div>
              {dash?.is_vip && (
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
                  <span style={{ color: 'var(--tx-faint)' }}>{Math.round((dash.activity_coupon_value_cents ?? 10000) / 100)}元活動優惠券：</span>
                  <span style={{ fontWeight: 700, color: 'var(--gold)' }}>{dash.activity_coupon_balance ?? 0} 張</span>
                </div>
              )}
              {/* 推廣連結：累積里程 ≥10km 才能產生專屬連結，朋友註冊+跑滿 10km 後雙方各得 VIP 天數 */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 2 }}>
                <span style={{ color: 'var(--tx-faint)' }}>推廣連結：</span>
                {dash && dash.total_km < 10 && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                    <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>
                      累積完成 10 公里即可產生專屬推廣連結（目前 {dash.total_km.toFixed(1)} / 10 km）
                    </span>
                    <button type="button" disabled style={{ ...ghostBtn, opacity: 0.5, cursor: 'default', alignSelf: 'flex-start' }}>產生我的推廣連結</button>
                  </div>
                )}
                {dash && dash.total_km >= 10 && !referral && (
                  <button type="button" onClick={generateReferral} disabled={referralBusy} style={{ ...ghostBtn, alignSelf: 'flex-start', opacity: referralBusy ? 0.6 : 1 }}>
                    {referralBusy ? '產生中…' : '產生我的推廣連結'}
                  </button>
                )}
                {referralErr && <span style={{ fontSize: 12, color: 'var(--hunt)' }}>{referralErr}</span>}
                {referral && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                    <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                      <input readOnly value={`${window.location.origin}/?ref=${referral.referral_code}`}
                        style={{ ...inp, fontSize: 12.5, padding: '8px 10px' }}
                        onFocus={(e) => e.target.select()} />
                      <button type="button" onClick={() => {
                        navigator.clipboard?.writeText(`${window.location.origin}/?ref=${referral.referral_code}`)
                          .then(() => { setReferralCopied(true); setTimeout(() => setReferralCopied(false), 1500) }).catch(() => {})
                      }} style={{ ...ghostBtn, flexShrink: 0, padding: '8px 12px' }}>{referralCopied ? '已複製' : '複製連結'}</button>
                    </div>
                    <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>
                      朋友透過此連結註冊，完成累積 10 公里後，你 +{referral.reward_days_referrer} 天、朋友 +{referral.reward_days_referred} 天 VIP
                    </span>
                    <span style={{ fontSize: 11.5, color: 'var(--tx-dim)' }}>
                      已成功推薦 {referral.rewarded_count} 人（{referral.referred_count} 人已註冊）
                    </span>
                  </div>
                )}
              </div>
              {/* 付款卡片（VIP 訂閱 Phase E 卡片管理）：顯示目前綁定的綠界卡片末四碼/到期年月，可解除綁定 */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginTop: 2 }}>
                <span style={{ color: 'var(--tx-faint)' }}>付款卡片：</span>
                {card?.bound ? (
                  <>
                    <span style={{ fontWeight: 700, color: 'var(--tx)' }}>💳 **** {card.card_last4}{card.card_expiry_mm && card.card_expiry_yy ? `（${card.card_expiry_mm}/${card.card_expiry_yy.slice(-2)}）` : ''}</span>
                    <button type="button" onClick={() => { setUnbindErr(''); setShowUnbind(true) }} style={{ ...ghostBtn, padding: '4px 10px', fontSize: 11.5 }}>解除綁定</button>
                  </>
                ) : (
                  // 三態顯示：剛解除→「未綁定信用卡（已解除綁定 ✓）」明確成功回饋；未綁定→「未綁定信用卡」；載入中
                  <span style={{ color: 'var(--tx-dim)' }}>
                    {card ? '未綁定信用卡' : '載入中…'}
                    {unbindDone && <span style={{ color: 'var(--fug)', fontWeight: 700 }}>（已解除綁定 ✓）</span>}
                  </span>
                )}
              </div>
            </div>
            <Field label="顯示名稱"><input style={inp} value={p.name} onChange={(e) => set('name', e.target.value)} /></Field>
            <Field label="Email（Google 帳號）"><input style={{ ...inp, opacity: 0.6 }} value={p.email} disabled /></Field>
            <Field label="真實姓名"><input style={inp} value={p.real_name} onChange={(e) => set('real_name', e.target.value)} /></Field>
            <Field label="手機"><input style={inp} value={p.phone} onChange={(e) => set('phone', e.target.value)} /></Field>
            <Field label="地址"><input style={inp} value={p.address} onChange={(e) => set('address', e.target.value)} /></Field>
            <Field label="生日"><input style={dateInp} type="date" value={p.birthday} onChange={(e) => set('birthday', e.target.value)} /></Field>
            <Field label="性別">
              <select style={inp} value={p.gender} onChange={(e) => set('gender', e.target.value as Profile['gender'])}>
                {GENDERS.map((g) => <option key={g.v} value={g.v}>{g.t}</option>)}
              </select>
            </Field>
            {saved && <div style={{ color: 'var(--fug)', fontSize: 13 }}>✓ 已儲存</div>}
            <button onClick={save} disabled={saving} style={primaryBtn}>{saving ? '儲存中…' : '儲存'}</button>
          </div>
        )}

        {/* 頁籤④追蹤列表 */}
        {tab === 'follows' && (
          <div>
            {!follows && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>載入中…</div>}
            {follows && follows.length === 0 && <div style={{ fontSize: 13, color: 'var(--tx-dim)', padding: '8px 0' }}>尚未追蹤任何人，可在賽事「排名」頁追蹤跑者。</div>}
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {follows?.map((f) => (
                <div key={f.user_id} style={{ ...recCard, padding: 10, display: 'flex', alignItems: 'center', gap: 10 }}>
                  <div style={{ width: 40, height: 40, borderRadius: '50%', overflow: 'hidden', background: 'var(--bg-2)', border: '1px solid var(--line-2)', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    {f.avatar_url
                      // eslint-disable-next-line @next/next/no-img-element
                      ? <img src={f.avatar_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                      : <span style={{ fontWeight: 800, color: 'var(--tx-dim)' }}>{(f.nickname || '?').slice(0, 1)}</span>}
                  </div>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 14, fontWeight: 600, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.nickname}</div>
                  </div>
                  <button onClick={() => unfollow(f.user_id)} style={{ ...ghostBtn, padding: '6px 12px', fontSize: 12, flexShrink: 0 }}>解除追蹤</button>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* 頁籤②運動數據 */}
        {tab === 'sports' && (
        <div>
          <div style={recCard}>
            {/* Strava 官方連接名額誠實告知（見 memory strava-api-review：上限 10 已滿，重送審核中）；不管 Terra 是否開放都顯示，
                不隱藏連接按鈕（有人中斷會釋出名額）。琥珀色半透明底＋var(--tx) 文字，不用金黃實心底（專案規則：實色金底才強制白字）。 */}
            <div style={{ fontSize: 11.5, color: 'var(--tx)', background: 'rgba(245,158,11,.14)', border: '1px solid rgba(245,158,11,.35)', borderRadius: 8, padding: '8px 10px', marginBottom: 12, lineHeight: 1.6 }}>
              ⚠ Strava 官方限制每個 App 只能連接 10 位跑者，目前名額已滿、升級審核中。使用 Garmin／COROS 等裝置的跑者請改用「連接你常用的跑步裝置」。
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 700, color: '#fc4c02' }}>Strava{!terra?.enabled && <span style={{ fontSize: 10.5, color: 'var(--fug)', fontWeight: 800, marginLeft: 5 }}>· 推薦</span>}</div>
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
                  {strava?.connected
                    ? `已連接${strava.athlete_name ? `：${strava.athlete_name}` : ''} · 活動自動同步`
                    : '連接後自動同步跑步活動，用於個人數據（個人任務、自主訓練、稱號成就、個人里程）；依 Strava 平台規範，Strava 數據不計入活動排名或里程競賽統計——要讓裝置紀錄進賽事，請用下方「連接你常用的跑步裝置」'}
                </div>
              </div>
              {strava?.connected ? (
                <div style={{ display: 'flex', gap: 8, flexShrink: 0, alignSelf: 'flex-start' }}>
                  <button onClick={syncNow} disabled={syncing}
                    style={{ background: '#fc4c02', color: '#fff', border: 'none', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', opacity: syncing ? 0.6 : 1 }}>
                    {syncing ? '同步中…' : '重新同步'}
                  </button>
                  <button onClick={disconnectStrava} disabled={stravaBusy} style={{ ...ghostBtn, whiteSpace: 'nowrap' }}>中斷</button>
                </div>
              ) : (
                <button onClick={connectStrava} disabled={stravaBusy || strava?.enabled === false}
                  aria-label="Connect with Strava"
                  style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', flexShrink: 0, alignSelf: 'flex-start', opacity: stravaBusy || strava?.enabled === false ? 0.5 : 1 }}>
                  {/* Strava 官方「Connect with Strava」按鈕（橙色 @2x；橙色實心底在深/淺 skin 與任何卡片底色皆清晰，白色版僅適純深底故不用） */}
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src="/strava/btn_strava_connect_with_orange_x2.png" alt="Connect with Strava" style={{ height: 48, display: 'block' }} />
                </button>
              )}
            </div>
            {strava?.enabled === false && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 8 }}>（Strava 整合尚未由管理者設定）</div>}
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>依 Strava 平台規範，Strava 數據僅用於你的個人數據，不會用於活動排名／里程競賽統計（此為 Strava 的限制，與賽事設定無關）。</div>
            {stravaMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginTop: 8 }}>{stravaMsg}</div>}
            {strava?.connected && (
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>
                要更換 Strava 帳號？請先{' '}
                <a href="https://www.strava.com/logout" target="_blank" rel="noreferrer" style={{ color: '#fc4c02' }}>登出 Strava</a>
                ，再「中斷」後重新連接（連接的是你瀏覽器當下登入的 Strava 帳號）。
              </div>
            )}
          </div>

          {/* 推播通知開關 */}
          <div style={{ marginTop: 12 }}>
            <PushToggle />
          </div>

          {/* 團練開跑前 Email 提醒開關（見 internal/runmeet/reminder.go；migration 163）。
              站內信一律照發，這裡只管 Email；偏好關閉或在行銷退訂名單都不會收到這封信。 */}
          <div style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 12, padding: '12px 14px' }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)' }}>團練開跑前 Email 提醒</div>
                <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 3, lineHeight: 1.6 }}>
                  開跑前會寄一封提醒信到你的註冊信箱，提醒你別忘了準時出發。只針對你加入的團練，站內信不受此開關影響。
                </div>
              </div>
              <button onClick={toggleReminderEmail} disabled={reminderBusy || !dash}
                style={{
                  flexShrink: 0, padding: '9px 14px', borderRadius: 10, fontSize: 13, fontWeight: 700,
                  whiteSpace: 'nowrap', cursor: 'pointer', fontFamily: 'inherit',
                  background: dash?.runmeet_reminder_email ? 'var(--fug)' : 'transparent',
                  color: dash?.runmeet_reminder_email ? 'var(--fug-ink)' : 'var(--tx-dim)',
                  border: `1px solid ${dash?.runmeet_reminder_email ? 'var(--fug)' : 'var(--line-2)'}`,
                }}>
                {reminderBusy ? '處理中…' : dash?.runmeet_reminder_email ? '已開啟 ✓' : '已關閉'}
              </button>
            </div>
          </div>

          {/* 手錶直連（Garmin/COROS/Polar/Suunto/Wahoo，Terra 聚合器，Phase 1）。terra===null 或 !enabled 時維持
              「即將開放」佔位卡（production 尚未設定 Terra 憑證前的常態，見 memory terra-wearable-integration）；
              enabled 後才是真正的連接流程，且升級為推薦卡（Strava 名額已滿，見上方卡片琥珀提示）。 */}
          <div style={{ ...recCard, marginTop: 12 }}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 700, color: 'var(--tx)' }}>
                  ⌚ 連接你常用的跑步裝置
                  {terra?.enabled && <span style={{ fontSize: 10.5, color: 'var(--fug)', fontWeight: 800, marginLeft: 5 }}>· 推薦</span>}
                </div>
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3, lineHeight: 1.6 }}>
                  {!terra?.enabled
                    ? <>Strava 名額已滿也沒關係——很快就能連接你的 Garmin／COROS／Polar／Suunto／Wahoo 裝置帳號同步跑步，<b>正在開通中</b>。</>
                    : `直接連接 ${terraBrandList} 等裝置帳號同步跑步：計入個人數據（個人任務、自主訓練、稱號成就、個人里程），主辦方開放外部數據的賽事也會計入排名與里程統計。`}
                </div>
              </div>
              {!terra?.enabled ? (
                <span style={{ flexShrink: 0, fontSize: 11.5, fontWeight: 800, color: 'var(--tx-faint)', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '6px 10px' }}>即將開放</span>
              ) : (
                <button onClick={connectTerra} disabled={terraBusy}
                  style={{ background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontWeight: 800, whiteSpace: 'nowrap', flexShrink: 0, alignSelf: 'flex-start', opacity: terraBusy ? 0.6 : 1 }}>
                  {terraBusy ? '連接中…' : '連接裝置'}
                </button>
              )}
            </div>

            {/* 已連接品牌清單：可能同時連好幾支不同品牌的手錶，逐一列出＋各自可斷開；「連接手錶」按鈕仍保留在上方可再加一支 */}
            {terra?.enabled && terra.connections.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 10 }}>
                {terra.connections.map((c) => (
                  <div key={c.provider} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, background: 'var(--bg-2)', borderRadius: 8, padding: '7px 10px' }}>
                    <span style={{ fontSize: 12.5, color: 'var(--tx)', minWidth: 0 }}>✓ {terraBrandName(c.provider)} ・ 已連接 {fmtDate(c.connected_at).split(' ')[0]}</span>
                    <div style={{ display: 'flex', gap: 6, flexShrink: 0 }}>
                      <button onClick={() => importTerra(c.provider)} disabled={terraBusy} style={{ ...ghostBtn, background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', padding: '5px 10px', fontSize: 11.5, whiteSpace: 'nowrap' }}>{terraBusy ? '處理中…' : '匯入數據'}</button>
                      <button onClick={() => disconnectTerra(c.provider)} disabled={terraBusy} style={{ ...ghostBtn, padding: '5px 10px', fontSize: 11.5, whiteSpace: 'nowrap' }}>斷開</button>
                    </div>
                  </div>
                ))}
              </div>
            )}

            {terraMsg && <div style={{ fontSize: 12.5, color: 'var(--fug)', marginTop: 8 }}>{terraMsg}</div>}
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.6 }}>
              連接即表示你同意透過整合商 <b>Terra</b> 取得你的跑步活動資料（跨境處理），並同意本平台 <a href="/privacy" target="_blank" rel="noreferrer" style={{ color: 'var(--fug)' }}>隱私權政策</a>。
            </div>
            {terra?.enabled && terra.connections.length > 0 && (
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4, lineHeight: 1.6 }}>
                裝置同步的跑步／走路（跑步機、越野跑、運動場跑步、徒步都算）通常會自動匯入；若沒進來，按「匯入數據」會向 Terra 抓近 30 天的紀錄（只計入連接之後的活動）。
              </div>
            )}
          </div>

          {/* 里程優先來源（連接 2 個以上來源時可設定；跨來源去重用） */}
          {connectedSources.length >= 2 && (
            <div style={{ marginTop: 12, background: 'var(--bg-2)', borderRadius: 12, padding: '12px 14px' }}>
              <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)' }}>里程優先來源</div>
              <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 3, lineHeight: 1.6 }}>正式紀錄一律以「App GPS 跑步追蹤」為優先。此設定用於：①結束跑步時是否先跳出確認外部數據的提示；②沒有 App GPS 記錄時，多個外部來源（Strava／裝置）之間如何取捨。若外部紀錄里程較長，EXP/DP/總里程會自動補足差額。</div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 10 }}>
                {connectedSources.map((src) => {
                  const on = effectiveSource === src
                  return (
                    <button key={src} disabled={on} onClick={() => selectDataSource(src)}
                      style={{ flex: '1 1 84px', minWidth: 84, padding: '9px 0', borderRadius: 10, fontSize: 13, fontWeight: 800, cursor: on ? 'default' : 'pointer', background: on ? 'var(--fug)' : 'transparent', color: on ? 'var(--fug-ink)' : 'var(--tx-dim)', border: `1px solid ${on ? 'var(--fug)' : 'var(--line-2)'}` }}>
                      {sourceLabel(src)}{on ? ' ✓' : ''}
                    </button>
                  )
                })}
              </div>
              {dataSrcMsg && <div style={{ fontSize: 11.5, color: 'var(--hunt)', marginTop: 8 }}>{dataSrcMsg}</div>}
            </div>
          )}

          {/* 本機尚未上傳的 GPS（里程優先來源=外部來源時，track 頁結束不自動上傳）——不限於已連 Strava，故不包在 strava?.connected 內 */}
          {pending && (
            <div style={{ marginTop: 12, background: 'var(--bg-2)', border: '1px solid var(--fug)', borderRadius: 12, padding: '12px 14px' }}>
              <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)' }}>🏃 本機尚未上傳的跑步</div>
              <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6 }}>
                {effectiveSource === 'gps'
                  ? `約 ${pending.km} km · ${pending.mins} 分。這趟跑步尚未上傳，可直接上傳這趟 GPS 數據。`
                  : `約 ${pending.km} km · ${pending.mins} 分。你的優先來源是 ${prefLabel}，可等 ${prefLabel} 同步後以 ${prefLabel} 為準，或直接上傳這趟 GPS 數據。`}
              </div>
              {pendingErr && <div style={{ fontSize: 11.5, color: 'var(--hunt)', marginTop: 6 }}>{pendingErr}</div>}
              <button onClick={() => (effectiveSource === 'gps' ? uploadPending() : setPendingAsk(true))} disabled={pendingBusy}
                style={{ marginTop: 10, background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 10, padding: '9px 16px', fontSize: 13, fontWeight: 800, cursor: 'pointer', opacity: pendingBusy ? 0.6 : 1 }}>
                {pendingBusy ? '上傳中…' : '上傳數據'}
              </button>
            </div>
          )}

          {/* 已同步活動 */}
          {strava?.connected && (
            <div style={{ marginTop: 12 }}>
              <div style={{ fontSize: 12.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 8 }}>已同步活動</div>
              {!activities && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>載入中…</div>}
              {activities && activities.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚無活動，按「重新同步」匯入近 30 日跑步。</div>}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                {activities?.map((a) => (
                  <div key={a.id} style={{ ...recCard, padding: 12, opacity: a.flagged ? 0.6 : 1 }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
                      <span style={{ fontWeight: 700, fontSize: 14 }}>{a.distance_km.toFixed(2)} K</span>
                      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{fmtDate(a.started_at || a.recorded_at)}</span>
                    </div>
                    {/* GPS 距離校正（見 internal/gpscalib，2026-08-30）：calib_factor!=null 且 <1 才代表這筆
                        App GPS 活動實際套用過校正——只在這種情況下才多顯示一行「校正後/原始」對照。 */}
                    {a.calib_factor != null && a.calib_factor < 1 && (
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 1 }}>
                        校正後 · 原始 {a.raw_distance_km.toFixed(2)} K ×{a.calib_factor.toFixed(4)}
                      </div>
                    )}
                    <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 3 }}>
                      配速 {paceStr(a.avg_pace_s)}/km · {Math.round(a.duration_s / 60)} 分
                      {a.ascent_m != null ? ` · 爬升 ${Math.round(a.ascent_m)}m` : ''}
                      {a.avg_hr != null ? ` · 心率 ${a.avg_hr}` : ''}
                    </div>
                    <div style={{ fontSize: 11, marginTop: 3 }}>
                      {a.flagged
                        ? <span style={{ color: 'var(--hunt)' }}>⚠ {FLAG_LABEL[a.flag_reason ?? ''] ?? '重複'}（不計入賽事）</span>
                        : a.race_title
                          ? <span style={{ color: 'var(--fug)' }}>計入：{a.race_title}</span>
                          : <span style={{ color: 'var(--tx-faint)' }}>未對應賽事</span>}
                    </div>
                    {a.source === 'strava' && a.external_id && (
                      <a href={`https://www.strava.com/activities/${a.external_id}`} target="_blank" rel="noreferrer"
                        style={{ display: 'inline-block', marginTop: 5, fontSize: 11, fontWeight: 700, color: '#fc4c02', textDecoration: 'none' }}>
                        View on Strava ↗
                      </a>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* GPS 距離校正（見 internal/gpscalib，2026-08-30）：以連接的手錶/App(Strava/Garmin/COROS)紀錄
              為參考，估計 App GPS 距離的系統性偏差、只准向下修正、只向前生效。hidden 不渲染；locked 顯示
              鎖定卡片但不打 API（SEC-H5：前端隱藏不等於後端有擋，實際存取仍由 requireEntry 在後端強制複查）。 */}
          {dash && dash.gps_calib_entry !== 'hidden' && (
            <div style={{ ...recCard, marginTop: 12 }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
                <div style={{ fontWeight: 700, color: 'var(--tx)' }}>📡 GPS 距離校正</div>
                {dash.gps_calib_entry === 'locked' && (
                  <span style={{ flexShrink: 0, fontSize: 11.5, fontWeight: 800, color: 'var(--tx-faint)', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '6px 10px' }}>即將開放</span>
                )}
              </div>
              {dash.gps_calib_entry === 'locked' ? (
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.6 }}>
                  以你連接的裝置/App（Strava/Garmin/COROS）紀錄為參考，自動校正 App GPS 跑步的距離系統性偏差。
                </div>
              ) : !gpsCalib ? (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 8 }}>載入中…</div>
              ) : (
                <>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.6 }}>
                    以你的{gpsCalib.ref_source ? `「${GPS_CALIB_SRC_LABEL[gpsCalib.ref_source] ?? gpsCalib.ref_source}」紀錄` : '裝置/App 紀錄'}為參考，自動校正 App GPS 距離的系統性偏差；只會讓距離變短、不會變長，且只影響上線後新跑的紀錄，不會回頭改已有的成績。
                  </div>
                  <div style={{ display: 'flex', gap: 20, marginTop: 10, flexWrap: 'wrap' }}>
                    <div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>目前係數</div>
                      <div style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)' }}>×{gpsCalib.factor.toFixed(4)}</div>
                    </div>
                    <div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>狀態</div>
                      <div style={{ fontSize: 13, fontWeight: 700, color: GPS_CALIB_STATUS_COLOR[gpsCalib.status] ?? 'var(--tx)' }}>{GPS_CALIB_STATUS_LABEL[gpsCalib.status] ?? gpsCalib.status}</div>
                    </div>
                    <div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>視窗內配對數</div>
                      <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx)' }}>{gpsCalib.n_pairs}</div>
                    </div>
                    {gpsCalib.last_pair_at && (
                      <div>
                        <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>最近配對</div>
                        <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx)' }}>{fmtDate(gpsCalib.last_pair_at)}</div>
                      </div>
                    )}
                  </div>
                  {gpsCalibErr && <div style={{ fontSize: 11.5, color: 'var(--hunt)', marginTop: 8 }}>{gpsCalibErr}</div>}
                  <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
                    <button onClick={toggleGpsCalib} disabled={gpsCalibBusy}
                      style={{ flex: 1, padding: '8px 0', borderRadius: 10, fontSize: 12.5, fontWeight: 800, cursor: gpsCalibBusy ? 'default' : 'pointer', background: gpsCalib.enabled ? 'var(--fug)' : 'transparent', color: gpsCalib.enabled ? 'var(--fug-ink)' : 'var(--tx-dim)', border: `1px solid ${gpsCalib.enabled ? 'var(--fug)' : 'var(--line-2)'}`, opacity: gpsCalibBusy ? 0.6 : 1 }}>
                      {gpsCalib.enabled ? '✓ 已開啟校正' : '已關閉（只計算不套用）'}
                    </button>
                    <button onClick={recomputeGpsCalib} disabled={gpsCalibBusy}
                      style={{ padding: '8px 14px', borderRadius: 10, fontSize: 12.5, fontWeight: 800, cursor: gpsCalibBusy ? 'default' : 'pointer', background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', opacity: gpsCalibBusy ? 0.6 : 1, whiteSpace: 'nowrap' }}>
                      重新計算
                    </button>
                  </div>
                  {(gpsCalib.pairs.length > 0 || gpsCalib.log.length > 0) && (
                    <div style={{ marginTop: 10 }}>
                      <button onClick={() => setGpsCalibDetail((v) => !v)} style={{ background: 'transparent', border: 'none', color: 'var(--tx-faint)', fontSize: 11.5, fontWeight: 700, cursor: 'pointer', padding: 0 }}>
                        {gpsCalibDetail ? '收起最近配對 ▲' : `查看最近配對（${gpsCalib.pairs.length}）▼`}
                      </button>
                      {gpsCalibDetail && gpsCalib.pairs.length > 0 && (
                        <div style={{ marginTop: 6, display: 'flex', flexDirection: 'column', gap: 4 }}>
                          {gpsCalib.pairs.map((pr, i) => (
                            <div key={i} style={{ display: 'flex', justifyContent: 'space-between', gap: 8, fontSize: 11, color: pr.accepted ? 'var(--tx-dim)' : 'var(--tx-faint)', background: 'var(--bg-2)', borderRadius: 6, padding: '5px 8px' }}>
                              <span style={{ flexShrink: 0 }}>{fmtDate(pr.activity_at)}</span>
                              <span style={{ flexShrink: 0 }}>{pr.gps_km.toFixed(2)} / {pr.ext_km.toFixed(2)} km</span>
                              <span style={{ textAlign: 'right' }}>{pr.accepted ? `採用（×${pr.ratio.toFixed(3)}）` : (GPS_CALIB_REJECT_LABEL[pr.reject_reason ?? ''] ?? '拒絕')}</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                  {gpsCalibDetail && gpsCalib.log.length > 0 && (
                    <div style={{ marginTop: 10 }}>
                      <div style={{ fontSize: 11.5, fontWeight: 700, color: 'var(--tx-faint)', marginBottom: 4 }}>係數歷程</div>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
                        {gpsCalib.log.map((le, i) => (
                          <div key={i} style={{ display: 'flex', justifyContent: 'space-between', gap: 8, fontSize: 11, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 6, padding: '5px 8px' }}>
                            <span style={{ flexShrink: 0 }}>{fmtDate(le.created_at)}</span>
                            <span>{le.factor_before != null && le.factor_after != null ? `×${le.factor_before.toFixed(4)} → ×${le.factor_after.toFixed(4)}` : (GPS_CALIB_STATUS_LABEL[le.status ?? ''] ?? le.reason)}</span>
                            <span style={{ flexShrink: 0, color: 'var(--tx-faint)' }}>{le.reason}</span>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </>
              )}
            </div>
          )}

          {/* Strava 資料來源歸屬（品牌合規）：依 skin 深淺顯示白/深字版；後台可上傳，未設定則用內建佔位圖。
              兩張都渲染、由 CSS 依 <html data-skin> 決定顯示哪一張（避免 client 判斷造成 SSR 不一致）。 */}
          <div style={{ marginTop: 16, display: 'flex', justifyContent: 'center' }}>
            <a href="https://www.strava.com" target="_blank" rel="noreferrer" aria-label="Powered by Strava">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img className="strava-badge-darkskin" src={site?.strava_powered_dark_url || '/strava/powered_by_strava_white.png'} alt="Powered by Strava" style={{ height: 18 }} />
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img className="strava-badge-lightskin" src={site?.strava_powered_light_url || '/strava/powered_by_strava_orange.png'} alt="Powered by Strava" style={{ height: 18 }} />
            </a>
          </div>
        </div>
        )}

        {/* 頁籤③報名紀錄 */}
        {tab === 'records' && (
        <div>
          {!regs && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>載入中…</div>}
          {regs && regs.length === 0 && <div style={{ color: 'var(--tx-dim)', fontSize: 13 }}>尚無報名紀錄</div>}
          {recMsg && <div style={{ color: 'var(--fug)', fontSize: 13, marginBottom: 10 }}>✓ {recMsg}</div>}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {regs?.map((r) => {
              const st = REG_STATUS[r.status] ?? { t: r.status, c: 'var(--tx-dim)' }
              const cancelling = r.cancel_request_status === 'pending' || r.cancel_request_status === 'processing'
              const expanded = expandedRegId === r.registration_id
              return (
                <div key={r.registration_id} style={recCard}>
                  {/* 第一層（收合）：只顯示活動名稱＋目前狀態，點擊展開/收合明細 */}
                  <button
                    onClick={() => setExpandedRegId(expanded ? null : r.registration_id)}
                    style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 8, width: '100%', background: 'none', border: 'none', padding: 0, margin: 0, cursor: 'pointer', textAlign: 'left', font: 'inherit', color: 'inherit' }}
                  >
                    <div style={{ fontWeight: 700, fontSize: 15, wordBreak: 'keep-all', overflowWrap: 'break-word' }}>{r.race_title}</div>
                    <span style={{ display: 'flex', alignItems: 'center', gap: 6, flexShrink: 0 }}>
                      <span style={{ fontSize: 12, color: st.c, fontWeight: 700 }}>{st.t}</span>
                      <span style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{expanded ? '▾' : '▸'}</span>
                    </span>
                  </button>

                  {/* 第二層（展開）：分組/繳費明細 + 取消報名申請（沿用既有流程） */}
                  {expanded && (
                    <div style={{ marginTop: 10, paddingTop: 10, borderTop: '1px solid var(--line)' }}>
                      <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>
                        {r.group_revealed ? (r.group_name || '—') : '分組賽事當天公布'}
                      </div>
                      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 10 }}>
                        <span style={{ fontSize: 13, color: 'var(--tx-dim)' }}>應繳 {ntd(r.order_total_cents)}</span>
                        {r.status === 'pending' && r.order_id && (
                          <button onClick={() => openPay(r.order_id!)} style={payBtn}>前往繳費</button>
                        )}
                      </div>

                      {/* 取消報名 / 分級退費申請狀態 */}
                      <div style={{ marginTop: 10, paddingTop: 10, borderTop: '1px solid var(--line)' }}>
                        {cancelling ? (
                          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                            <span style={{ fontSize: 12, color: 'var(--gold)', fontWeight: 700 }}>⏳ 取消申請審核中</span>
                            <button
                              onClick={() => withdrawCancel(r)}
                              disabled={withdrawBusy === r.registration_id}
                              style={{ ...ghostBtn, opacity: withdrawBusy === r.registration_id ? 0.6 : 1 }}
                            >
                              {withdrawBusy === r.registration_id ? '撤回中…' : '撤回申請'}
                            </button>
                          </div>
                        ) : r.cancel_request_status === 'approved' ? (
                          <span style={{ fontSize: 12, color: 'var(--tx-dim)', fontWeight: 700 }}>已取消（已核准）</span>
                        ) : r.cancel_request_status === 'rejected' ? (
                          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                            <span style={{ fontSize: 12, color: 'var(--hunt)', fontWeight: 700 }}>取消申請未通過</span>
                            {/* 不提供退費的賽事不再引導重新申請（見下方 refund_disabled 分支同款說明） */}
                            {r.can_cancel && !r.refund_disabled && (
                              <button onClick={() => openCancelModal(r)} style={ghostBtn}>重新申請取消</button>
                            )}
                          </div>
                        ) : r.refund_disabled ? (
                          // config.refund_disabled（此活動不提供退費）：不顯示「申請取消報名」，改灰字說明
                          <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>本活動不適用七天鑑賞期</span>
                        ) : r.can_cancel ? (
                          <button onClick={() => openCancelModal(r)} style={{ ...ghostBtn, color: 'var(--hunt)', borderColor: 'var(--hunt)' }}>
                            申請取消報名
                          </button>
                        ) : r.cancel_blocked_reason ? (
                          <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{r.cancel_blocked_reason}</span>
                        ) : null}
                      </div>
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        </div>
        )}

        {/* 支援與隱私（聯絡 / Strava / 隱私權） */}
        <div style={{ marginTop: 22, paddingTop: 16, borderTop: '1px solid var(--line)', display: 'flex', flexDirection: 'column', gap: 7 }}>
          <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)' }}>支援與隱私</div>
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
            聯絡我們：<a href="mailto:service@dor.tw" style={{ color: 'var(--fug)', textDecoration: 'none', fontWeight: 700 }}>service@dor.tw</a>
          </div>
          {/* 對外聯絡資訊政策（同 support/terms/privacy 頁）：只留 Email+統編，不列地址/電話 */}
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', lineHeight: 1.7 }}>
            統一編號：83005678
          </div>
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', lineHeight: 1.7 }}>
            連接 Strava：到上方「運動數據」分頁點官方「Connect with Strava」即可；要中斷請按「中斷」。我們僅匯入你連接之後的活動，並可隨時中斷。
          </div>
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', lineHeight: 1.7 }}>
            取消與退費：可於賽事開始前，至「報名紀錄」申請取消，退費金額依申請時距賽事天數分級計算，詳見各賽事簡章規定；線上活動不適用七天鑑賞期。
          </div>
          <div style={{ fontSize: 12.5, marginTop: 2, display: 'flex', flexWrap: 'wrap', gap: '4px 8px' }}>
            <a href="/support" style={{ color: 'var(--fug)', textDecoration: 'underline' }}>支援說明</a>
            <span style={{ color: 'var(--tx-faint)' }}>·</span>
            <a href="/terms" style={{ color: 'var(--fug)', textDecoration: 'underline' }}>服務條款／退款</a>
            <span style={{ color: 'var(--tx-faint)' }}>·</span>
            <a href="/privacy" style={{ color: 'var(--fug)', textDecoration: 'underline' }}>隱私權政策</a>
          </div>
        </div>
      </ScrollArea>

      {/* 繳費頁面 */}
      {payOrder && (
        <div style={overlay} onClick={() => setPayOrder(null)}>
          <div style={panel} onClick={(e) => e.stopPropagation()}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
              <strong style={{ fontSize: 17 }}>繳費</strong>
              <button onClick={() => setPayOrder(null)} style={backBtn}>✕</button>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, marginBottom: 10 }}>{payOrder.race_title || 'VIP 訂閱'}</div>

            <div style={{ border: '1px solid var(--line-2)', borderRadius: 12, padding: 14, marginBottom: 14 }}>
              {payOrder.items.map((it, i) => (
                <div key={i} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, color: 'var(--tx-dim)', padding: '3px 0' }}>
                  <span>{ITEM_LABEL[it.item_type] ?? it.item_type}{it.addon_name ? `：${it.addon_name}` : ''}{it.qty > 1 ? ` × ${it.qty}` : ''}</span>
                  <span style={{ color: it.subtotal_cents < 0 ? 'var(--fug)' : 'var(--tx)' }}>{ntd(it.subtotal_cents)}</span>
                </div>
              ))}
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 15, fontWeight: 800, marginTop: 8, paddingTop: 8, borderTop: '1px solid var(--line)' }}>
                <span>應繳金額</span><span style={{ color: 'var(--gold)' }}>{ntd(payOrder.total_cents)}</span>
              </div>
            </div>

            {payOrder.status === 'paid' ? (
              <div style={{ color: 'var(--fug)', fontSize: 14, fontWeight: 700 }}>✓ 已完成繳費</div>
            ) : (
              <>
                <button onClick={goEcpay} disabled={paying} style={{ ...primaryBtn, width: '100%', background: 'var(--gold)', color: '#fff' }}>
                  {paying ? '前往綠界…' : '前往綠界付款'}
                </button>
                <div style={{ marginTop: 10, fontSize: 11, color: 'var(--tx-faint)', lineHeight: 1.6 }}>
                  將導向綠界 ECPay 安全付款頁（信用卡 / ATM / 超商）。付款完成後返回本站，狀態會自動更新為「報名完成」。
                  <div style={{ marginTop: 4 }}>訂單編號：{payOrder.id}</div>
                </div>
              </>
            )}
            <button onClick={() => setPayOrder(null)} style={{ ...primaryBtn, width: '100%', marginTop: 12, background: 'rgba(255,255,255,.06)', color: 'var(--tx)' }}>關閉</button>
          </div>
        </div>
      )}

      {/* 申請取消報名（顯示分級退費比例／預估退款、必填取消原因） */}
      {cancelTarget && (
        <div style={overlay} onClick={() => { if (!cancelSubmitting) setCancelTarget(null) }}>
          <div style={panel} onClick={(e) => e.stopPropagation()}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
              <strong style={{ fontSize: 17 }}>申請取消報名</strong>
              <button onClick={() => setCancelTarget(null)} disabled={cancelSubmitting} style={backBtn}>✕</button>
            </div>
            <div style={{ fontSize: 14, fontWeight: 700, marginBottom: 10 }}>{cancelTarget.race_title}</div>

            <div style={{ border: '1px solid var(--line-2)', borderRadius: 12, padding: 14, marginBottom: 14, fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
              {(cancelTarget.refund_ratio || 0) > 0 ? (
                <>依目前距賽事天數，退費比例 <strong style={{ color: 'var(--tx)' }}>{cancelTarget.refund_ratio}%</strong>，預估可退 <strong style={{ color: 'var(--gold)' }}>{ntd(cancelTarget.estimated_refund_cents || 0)}</strong>。</>
              ) : (
                <span style={{ color: 'var(--hunt)', fontWeight: 700 }}>本次取消不退費，但仍會釋出你的報名名額。</span>
              )}
            </div>

            <label style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={{ fontSize: 11, letterSpacing: '.08em', color: 'var(--tx-faint)' }}>取消原因（必填）</span>
              <textarea
                value={cancelReason}
                onChange={(e) => { const v = e.target.value; if ([...v].length <= 200) setCancelReason(v) }}
                rows={3}
                placeholder="請簡述取消原因"
                style={{ ...inp, resize: 'vertical', minHeight: 72 }}
              />
            </label>
            <div style={{ fontSize: 11, color: 'var(--tx-faint)', textAlign: 'right', marginTop: 4 }}>{[...cancelReason].length}/200</div>

            {cancelErr && <div style={{ color: 'var(--hunt)', fontSize: 13, marginTop: 6 }}>{cancelErr}</div>}

            <button
              onClick={submitCancelRequest}
              disabled={cancelSubmitting || !cancelReason.trim()}
              style={{ ...primaryBtn, width: '100%', marginTop: 14, background: 'var(--hunt)', color: '#fff', opacity: cancelSubmitting || !cancelReason.trim() ? 0.6 : 1 }}
            >
              {cancelSubmitting ? '送出中…' : '確認送出取消申請'}
            </button>
            <button
              onClick={() => setCancelTarget(null)}
              disabled={cancelSubmitting}
              style={{ ...primaryBtn, width: '100%', marginTop: 10, background: 'rgba(255,255,255,.06)', color: 'var(--tx)' }}
            >
              先不取消
            </button>
          </div>
        </div>
      )}

      {showUpgrade && (
        <UpgradeVipModal
          onClose={() => setShowUpgrade(false)}
          onSubscribe={vipFlow.subscribe}
          subscribing={vipFlow.busy}
          subscribeError={vipFlow.error}
        />
      )}
      {vipFlow.bindCard && (
        <BindCardModal
          plan={vipFlow.bindCard.plan}
          amountCents={vipFlow.bindCard.amount_cents}
          token={vipFlow.bindCard.token}
          orderId={vipFlow.bindCard.order_id}
          serverType={vipFlow.bindCard.server_type}
          onClose={vipFlow.closeBindCard}
          onSuccess={() => { vipFlow.handleBindSuccess(); setShowUpgrade(false); loadCard() }}
        />
      )}
      {/* 解除綁卡確認（VIP 訂閱 Phase E）：有 active 訂閱時後端 409，訊息如實顯示（引導先取消訂閱） */}
      {showUnbind && (
        <div style={overlay} onClick={() => { if (!unbindBusy) setShowUnbind(false) }}>
          <div style={panel} onClick={(e) => e.stopPropagation()}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 14 }}>
              <strong style={{ fontSize: 17 }}>解除綁定付款卡片</strong>
              <button onClick={() => setShowUnbind(false)} disabled={unbindBusy} style={backBtn}>✕</button>
            </div>
            <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', lineHeight: 1.75 }}>
              確定要解除目前綁定的付款卡片（💳 **** {card?.card_last4}）嗎？解除後需重新綁卡才能再次訂閱付款。
            </div>
            {unbindErr && <div style={{ color: 'var(--hunt)', fontSize: 13, marginTop: 10 }}>{unbindErr}</div>}
            <button onClick={unbindCard} disabled={unbindBusy} style={{ ...primaryBtn, width: '100%', marginTop: 14, background: 'var(--hunt)', color: '#fff', opacity: unbindBusy ? 0.6 : 1 }}>
              {unbindBusy ? '處理中…' : '確認解除綁定'}
            </button>
            <button onClick={() => setShowUnbind(false)} disabled={unbindBusy} style={{ ...primaryBtn, width: '100%', marginTop: 10, background: 'rgba(255,255,255,.06)', color: 'var(--tx)' }}>
              先不要
            </button>
          </div>
        </div>
      )}
    </div>

    {/* 本機待上傳 GPS：是否等待外部來源同步 二次確認 */}
    {pendingAsk && (
      <div style={{ position: 'fixed', inset: 0, zIndex: 3600, background: 'rgba(0,0,0,.66)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24 }}>
        <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 16, padding: '20px 18px', maxWidth: 340, width: '100%' }}>
          <div style={{ fontSize: 16, fontWeight: 900, color: 'var(--tx)', marginBottom: 8 }}>是否要等待 {prefLabel} 數據同步？</div>
          <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7 }}>
            你的優先來源是 {prefLabel}。若等 {prefLabel} 同步完成，將以 {prefLabel} 數據為準；若直接上傳，這趟會以 GPS 數據計入。
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8, marginTop: 16 }}>
            <button onClick={uploadPending} disabled={pendingBusy}
              style={{ background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 10, padding: '11px', fontSize: 14, fontWeight: 800, cursor: 'pointer', opacity: pendingBusy ? 0.6 : 1 }}>
              {pendingBusy ? '上傳中…' : '否，直接上傳'}
            </button>
            <button onClick={() => setPendingAsk(false)} disabled={pendingBusy}
              style={{ background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }}>
              好，要等待
            </button>
          </div>
        </div>
      </div>
    )}
    </>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 6, flex: 1, minWidth: 0 }}>
      <span style={{ fontSize: 11, letterSpacing: '.08em', color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 'var(--radius-md, 10px)',
  padding: '11px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', boxSizing: 'border-box', fontFamily: 'inherit',
}
// iOS 原生日期框：去除原生外觀以吃滿寬度、文字靠左
const dateInp: React.CSSProperties = {
  ...inp, WebkitAppearance: 'none', appearance: 'none', textAlign: 'left', minWidth: 0, maxWidth: '100%',
}
const primaryBtn: React.CSSProperties = {
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
  borderRadius: 'var(--radius-btn, 10px)', padding: '12px 20px', cursor: 'pointer', fontSize: 14, marginTop: 4,
}
const recCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 14px)', padding: 14 }
const ghostBtn: React.CSSProperties = {
  background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)',
  borderRadius: 10, padding: '9px 14px', cursor: 'pointer', fontSize: 13, fontFamily: 'inherit',
}
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none',
  borderRadius: 'var(--radius-btn, 9px)', padding: '7px 14px', cursor: 'pointer', fontSize: 13,
}
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1200, padding: 20 }
// maxHeight + overflowY：長訂單（多項加購）在小螢幕不會把「前往綠界付款/關閉」擠到畫面外而點不到
const panel: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 20, width: '100%', maxWidth: 420, maxHeight: '90dvh', overflowY: 'auto', overscrollBehavior: 'contain' }
