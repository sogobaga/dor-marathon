'use client'

import useSWR from 'swr'
import { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import { racesApi, followApi, raceStatusFlags, METRIC_BY_KEY, formatChallengeRule, formatChallengeProgress, type Race, type TaskProgress, type TaskContributors, type TaskRangeDetail, type GrantedReward, type RewardPreviewItem, type RaceSupply, type PersonalHistory } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import { useScrollLock } from '@/lib/useScrollLock'
import { useVipSubscribeFlow } from '@/lib/useVipSubscribeFlow'
import BindCardModal from './BindCardModal'
import { useSheetDismiss } from '@/lib/useSheetDismiss'
import { overlayMount } from '@/lib/overlayMount'
import { renderCertificate, downloadCertificate, type CertificateRender } from '@/lib/certificate'
import ExpSettlementModal from './ExpSettlementModal'
import RewardGrantedModal from './RewardGrantedModal'
import UpgradeVipModal from './UpgradeVipModal'
import FollowHeartButton from './shared/FollowHeartButton'
import { BrochureBody } from './BrochureScreen'
import { RankingBody } from './RaceRankingScreen'
import { ExploreBody } from './ExploreBody'
import ScrollArea from './ScrollArea'
import ImageLightbox from './ImageLightbox'

const STATUS_LABEL: Record<string, string> = {
  registering: '報名中', upcoming_reg: '即將報名', reg_closed: '報名結束',
  starting_soon: '即將開始', racing: '進行中', ended: '已結束',
  paused: '暫停報名', suspended: '賽事中止',
}

function fmt(iso?: string | null) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function paceFmt(sec: number) {
  return `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`
}

// formatProbLabel 消保法機率揭露：把 prob_bp（萬分位）換算成前台「(中獎機率 xx%)」小字標示。
// - undefined／>=10000（100% 必得）：回傳空字串——不特別標示，維持版面乾淨（玩家保證拿得到，沒有
//   「機率」需要揭露；後端 race.RewardPreviewItem 註解也是這個口徑）。
// - 其餘：四捨五入到最多 1 位小數（35% / 12.5%），整數時不帶小數點；換算後仍四捨五入成 0 但原始機率
//   實際 >0 時，改顯示 <0.1%，避免讓玩家誤以為完全不會中獎。
function formatProbLabel(probBP?: number): string {
  if (!probBP || probBP >= 10000) return ''
  const pct = probBP / 100
  const rounded = Math.round(pct * 10) / 10
  const text = rounded > 0 ? (Number.isInteger(rounded) ? `${rounded}` : `${rounded.toFixed(1)}`) : '<0.1'
  return `(中獎機率 ${text}%)`
}

// fmtDurationShort 個人挑戰「完成用時」通常橫跨數天，改用 天/時/分 呈現（不同於單場跑步的 時:分:秒）
function fmtDurationShort(totalSec: number): string {
  const d = Math.floor(totalSec / 86400)
  const h = Math.floor((totalSec % 86400) / 3600)
  const m = Math.floor((totalSec % 3600) / 60)
  if (d > 0) return `${d} 天 ${h} 時`
  if (h > 0) return `${h} 時 ${m} 分`
  return `${m} 分`
}

// formatPersonalBest 完賽歷程「最佳成績」一句話呈現（見後端 race.PersonalHistory.BestMetric 註解）
function formatPersonalBest(h: PersonalHistory): string {
  if (h.best_metric === 'duration' && h.best_duration_s) return `最短用時 ${fmtDurationShort(h.best_duration_s)}`
  if (h.best_metric === 'distance' && h.best_distance_km) return `最長距離 ${h.best_distance_km.toFixed(1)} km`
  return ''
}

type Tab = 'brochure' | 'progress' | 'explore' | 'rank' | 'reward'

export default function RaceDetailScreen({
  race, onBack, onRegister, initialTab,
}: {
  race: Race
  onBack: () => void
  onRegister?: (race: Race) => void
  initialTab?: Tab
}) {
  const token = getUserToken() || undefined
  const { dash } = useDashboard()
  const [showUpgrade, setShowUpgrade] = useState(false)
  const vipFlow = useVipSubscribeFlow() // VIP 訂閱 Phase E：Subscribe → BindCardModal（比照 ProfileScreen 接線）
  const { data: detailData } = useSWR(['detail', race.id], () => racesApi.detail(race.id, token))
  const { data: standings } = useSWR(
    race.event_mode === 'competition' ? ['standings', race.id] : null,
    () => racesApi.standings(race.id, token),
  )
  const detail = detailData?.race
  const registration = detailData?.registration
  const isPersonal = race.event_mode === 'personal'
  // 完賽證明顯示開關（config.certificate_disabled）：關閉時一般模式的完賽證明、personal 模式取代它的
  // 完賽歷程一併隱藏（見下方兩處區塊）。讀 detail（GetPublicDetail 回傳，含完整 config）而非 race prop
  // ——race prop 來自列表頁可能是較舊的快取資料。
  const certificateDisabled = !!detail?.config?.certificate_disabled

  // 活動獎勵頁籤：完成活動有機會獲得的獎勵預覽（公開、輕量，不含機率/數量）。空陣列時不顯示頁籤。
  const { data: rewardPreviewData } = useSWR(['reward-preview', race.id], () => racesApi.rewardPreview(race.id))
  const rewardPreview = rewardPreviewData?.rewards ?? []

  // 參賽虛擬獎勵預覽（migration 140）：賽事開始後自動發放給所有已報名者的項目，展示在「報名禮」面板尾部
  // （見 SuppliesBody）。公開、輕量，不含機率/數量；空陣列時不額外渲染。
  const { data: entryRewardPreviewData } = useSWR(['entry-reward-preview', race.id], () => racesApi.entryRewardPreview(race.id))
  const entryRewardPreview = entryRewardPreviewData?.rewards ?? []

  // 完賽證明：賽事結束後、已報名、已登入才查；personal 模式改走完賽歷程（見下方 historyData），
  // 一般完賽證明對 personal 不適用（後端 GetMyCertificate 也已擋下 personal，見 certificate.go）
  const ended = race.display_status === 'ended'
  const { data: certData } = useSWR(
    !isPersonal && !certificateDisabled && ended && registration && token ? ['cert', race.id] : null,
    () => racesApi.certificate(race.id, token!),
  )
  const cert = certData?.certificate
  const [certImg, setCertImg] = useState('')
  const [certRender, setCertRender] = useState<CertificateRender | null>(null)
  const [certErr, setCertErr] = useState(false)
  const [certRetry, setCertRetry] = useState(0) // 手動觸發重試
  const [certZoom, setCertZoom] = useState(false)
  useEffect(() => {
    if (!cert?.completed) {
      setCertImg('')
      setCertRender(null)
      setCertErr(false)
      return
    }
    let cancelled = false
    setCertErr(false)
    renderCertificate(cert, cert.layout)
      .then((r) => {
        if (cancelled) return
        setCertImg(r.dataUrl)
        setCertRender(r)
      })
      .catch((e) => {
        if (cancelled) return
        console.error('[certificate] renderCertificate failed', e)
        setCertErr(true)
      })
    return () => { cancelled = true }
  }, [cert, certRetry])

  // 本場 EXP 結算明細（賽事結束 + 已報名）
  const { data: bdData } = useSWR(
    ended && registration && token ? ['exp-bd', race.id] : null,
    () => racesApi.expBreakdown(race.id, token!),
  )
  const breakdown = bdData?.breakdown
  const [showExp, setShowExp] = useState(false)
  useEffect(() => {
    if (!breakdown || breakdown.gained <= 0) return
    const key = `dor_exp_seen_${race.id}`
    if (typeof window !== 'undefined' && !localStorage.getItem(key)) {
      localStorage.setItem(key, '1')
      setShowExp(true) // 完賽後首次自動演出
    }
  }, [breakdown, race.id])

  // 個人挑戰模式「隨報隨進行」：活動中的 display_status 是 'registering'(可報名)而非 'racing'，
  // 故 started 對 personal 改用「活動開始日已過(now >= start_date)」判斷，否則排名/進度分頁會誤顯示「尚未開始」。
  const started = race.display_status === 'racing' || race.display_status === 'ended'
    || (race.event_mode === 'personal' && !!race.start_date && Date.now() >= new Date(race.start_date).getTime())
  // 競賽/分組對抗才有「當天揭曉分組＋分組戰報」；一般模式分組直接顯示
  const battleMode = race.event_mode === 'competition' || race.event_mode === 'faction_battle'
  // 個人挑戰模式完成判定引擎觸發點：開頁即打，即時評估規則＋CAS 標記完成/逾期（見後端 GetPersonalProgress）。
  // revalidateOnFocus 全域預設 true（AppProviders.tsx）：跑步結束回前景時會自動重打，不用額外接 hook。
  const { data: pp } = useSWR(
    isPersonal && token ? ['personal-progress', race.id] : null,
    () => racesApi.personalProgress(race.id, token!),
  )
  // 完賽歷程（取代一般模式完賽證明，見上方 certificateDisabled 註解）：等 detail 載入才判斷開關，
  // 避免開關關閉時先打一次 API 才收回（比照 cert 的 !certificateDisabled 閘門寫法）。
  const { data: historyData } = useSWR(
    isPersonal && token && detail && !certificateDisabled ? ['personal-history', race.id] : null,
    () => racesApi.personalHistory(race.id, token!),
  )
  const history = historyData?.history
  // 活動獎勵系統 P3：完成挑戰即得獎勵彈窗。後端只在「這次呼叫剛好把 attempt 判定為完成」才會回非空
  // newly_granted（見 race.GetPersonalProgress／MarkAttemptCompletedAndGrant 的 CAS 保證），之後
  // revalidate（如切背景回前景的 revalidateOnFocus）一律拿到 undefined/空陣列 → 依賴陣列參照變動的
  // effect 天然只會觸發一次，不需要額外用 localStorage 記錄「看過了」。
  const [rewardGranted, setRewardGranted] = useState<GrantedReward[] | undefined>(undefined)
  useEffect(() => {
    if (pp?.newly_granted && pp.newly_granted.length > 0) setRewardGranted(pp.newly_granted)
  }, [pp?.newly_granted])
  // 即時獎勵一般化（migration 134）：非 personal 賽事完成「個人額外挑戰」(group_individual scope 任務)
  // 觸發點在一般進度輪詢（見後端 progress.go GetRaceProgress／MarkRaceTaskCompletedAndGrant）。這裡與
  // ProgressBody 內建的 SWR 共用同一個 key(['progress', race.id])，SWR 自動去重不會重複打兩次 API；
  // 拉到父層是為了不論使用者目前停在哪個頁籤，只要輪詢在跑就能接住 newly_granted 彈窗（比照上面 pp 的寫法）。
  const { data: progData } = useSWR(
    !isPersonal && token ? ['progress', race.id] : null,
    () => racesApi.progress(race.id, token),
    { refreshInterval: 30000 },
  )
  useEffect(() => {
    if (progData?.progress?.newly_granted && progData.progress.newly_granted.length > 0) {
      setRewardGranted(progData.progress.newly_granted)
    }
  }, [progData?.progress?.newly_granted])
  // 個人挑戰模式可重複報名再挑戰：只有「進行中」(pending/paid未完成) 的 attempt 才算擋下再報名；
  // completed/expired/cancelled 的歷史報名應可再次顯示「報名挑戰」按鈕（與 RegistrationScreen 對稱）。
  // personal 用 pp（即時 CAS 判定結果）為準，而非 registration 快照——開頁評估可能剛把 attempt
  // 標記完成/逾期，registration 是 racesApi.detail 當下的舊快照，不會反映這次評估的結果。
  const inProgress = isPersonal
    ? !!pp?.has_attempt && (pp.status === 'pending' || pp.status === 'paid')
    : !!registration && (registration.status === 'pending' || registration.status === 'paid')
  const defaultTab: Tab = race.display_status === 'racing' ? 'progress' : race.display_status === 'ended' ? 'rank' : 'brochure'
  const [tab, setTab] = useState<Tab>(initialTab ?? defaultTab)
  // 是否有打卡點任務 → 決定是否顯示「探索」頁籤。改由已載入的 detail.tasks 算，不再額外打一支只為此用途的 progress 查詢
  // （後端 progress 會掃全體報名者活動、未聚合，很重；進度分頁本身的資料由下方 ProgressBody 內建的 SWR 負責，未受影響）。
  const hasCheckpoints = (detail?.tasks ?? []).some((t) => t.metric_type === 'checkpoint')

  // 狀態徽章：「進行中」與「報名中」解耦為獨立可並存條件，與 RacesScreen 的 RaceCard 徽章邏輯一致
  // （見 raceStatusFlags）；賽事結束一律只顯示「已結束」；三者皆不成立（賽前/暫停等）才 fallback 回
  // 原本互斥的 STATUS_LABEL[display_status]。用 periodEnded 命名避免和上面既有的 ended（cert/exp 用途）撞名。
  const { ended: periodEnded, ongoing: periodOngoing, regOpen: periodRegOpen } = raceStatusFlags(race)
  const statusLabels: string[] = periodEnded
    ? [STATUS_LABEL.ended]
    : [...(periodOngoing ? [STATUS_LABEL.racing] : []), ...(periodRegOpen ? [STATUS_LABEL.registering] : [])]
  if (statusLabels.length === 0) statusLabels.push(STATUS_LABEL[race.display_status] ?? race.display_status)

  // VIP 專屬賽事：非 VIP 點「立即報名」→ 擋下並跳出提示（不進報名表單）；VIP／非 vip_only 賽事照舊
  function handleRegisterClick() {
    if (race.vip_only && !dash?.is_vip) {
      setShowUpgrade(true)
      return
    }
    onRegister?.(race)
  }

  // 「前往挑戰」：已報名（含個人挑戰進行中）→ 導向 GPS 跑步追蹤頁；帶 from=race 供該頁顯示一次性新手提醒
  // 「點擊下方開始跑步按鈕，立即進行挑戰」（見 track/page.tsx 的 showStartTip，只在此路徑進入時顯示）。
  function goToTrack() {
    window.location.href = '/track?from=race'
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 10px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 0', fontSize: 22, fontWeight: 800, color: 'var(--tx)', wordBreak: 'keep-all', overflowWrap: 'break-word' }}>{race.title}</h1>
      </header>

      <ScrollArea padding="4px 18px 30px">
        {/* 賽事 Banner */}
        {(detail?.hero_image_url || race.hero_image_url) && (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={detail?.hero_image_url || race.hero_image_url}
            alt=""
            style={{ width: '100%', display: 'block', margin: '0 auto 14px', borderRadius: 12, maxHeight: 220, objectFit: 'contain', objectPosition: 'center' }}
          />
        )}
        {/* 賽事資訊 Dashboard */}
        <div className="skin-frame" style={dashCard}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            {statusLabels.map((label) => (
              <span key={label} style={statusBadge}>{label}</span>
            ))}
            {/* 測試中標籤：只有白名單成員能看到這場賽事本身才會收到 is_testing=true，不是外洩——
                虛線框＋紫色系跟正式狀態徽章（實線框／綠色）區隔 */}
            {race.is_testing && <span style={testingBadge}>🧪 測試中</span>}
            {/* 已報名標籤：與下方「前往挑戰」按鈕同一套判定（inProgress，見上方定義），使用者有效報名才顯示 */}
            {inProgress && <span style={registeredBadge}>已報名</span>}
            <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
              {race.event_mode === 'competition' ? '競賽' : race.event_mode === 'faction_battle' ? '分組對抗' : isPersonal ? '個人挑戰' : '一般'}
            </span>
            {race.vip_only && <span style={vipBadge}>✦ VIP專屬</span>}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr', gap: 4, marginTop: 10, fontSize: 12 }}>
            <Row k="報名期間" v={`${fmt(race.registration_start)} – ${fmt(race.registration_end)}`} />
            <Row k="賽事期間" v={`${fmt(race.start_date)} – ${fmt(race.end_date)}`} />
          </div>

          {/* 個人挑戰模式：挑戰內容（組人話規則說明）＋當前進度（來自完成判定引擎 /personal-progress） */}
          {isPersonal && (detail?.challenge_rule || race.challenge_rule) && (
            <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>挑戰內容</div>
              <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--tx)', marginTop: 4, lineHeight: 1.5 }}>
                {formatChallengeRule(detail?.challenge_rule ?? race.challenge_rule)}
              </div>
              {pp?.has_attempt && pp.status === 'completed' && (
                <div style={{ marginTop: 8, fontSize: 15, fontWeight: 800, color: 'var(--fug)' }}>🎉 挑戰完成！</div>
              )}
              {pp?.has_attempt && pp.status === 'expired' && (
                <div style={{ marginTop: 8, fontSize: 13, color: 'var(--tx-faint)' }}>已逾期，可重新報名再挑戰</div>
              )}
              {pp?.has_attempt && pp.status === 'pending' && (
                <div style={{ marginTop: 8, fontSize: 13, color: 'var(--tx-faint)' }}>完成付款後開始計算挑戰進度</div>
              )}
              {pp?.has_attempt && pp.status === 'paid' && pp.progress && (
                <div style={{ marginTop: 8, fontSize: 13, color: 'var(--tx-dim)' }}>{formatChallengeProgress(pp.progress)}</div>
              )}
              {pp && (
                <div style={{ marginTop: 8, fontSize: 12, color: 'var(--tx-faint)' }}>已完成 {pp.completed_count} 次</div>
              )}
            </div>
          )}

          {/* 我的分組（競賽/分組對抗：當天揭曉＋戰報；一般：直接顯示分組；個人挑戰無分組概念，不顯示）。
              取消報名核准後 registration 會回退成該賽事「最近一筆」（含 cancelled，見 GetRegistration 註解，
              服務 ProfileScreen 等歷史顯示需求），因此這裡必須額外用 inProgress（僅認 pending|paid）把
              cancelled 排除，否則會顯示已取消報名時的舊分組（同族：RacesScreen regActive 判斷）。 */}
          {!isPersonal && registration && inProgress && (battleMode || registration.group_name) && (
            <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>我的分組</div>
              {battleMode ? (
                <>
                  <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>
                    {standings?.my_group?.group_name || (registration.group_revealed ? '已加入分組' : '分組賽事當天公布')}
                  </div>
                  {started ? (
                    standings?.my_group && (
                      <div style={{ display: 'flex', gap: 16, marginTop: 6, fontSize: 12, color: 'var(--tx-dim)' }}>
                        <span>累積榜 第 <b style={{ color: 'var(--fug)' }}>{standings.my_group.cumulative_rank}</b> 名</span>
                        <span>{standings.my_group.total_km.toFixed(1)} K</span>
                      </div>
                    )
                  ) : (
                    <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 6 }}>賽事開始後顯示分組戰報</div>
                  )}
                </>
              ) : (
                <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>{registration.group_name}</div>
              )}
            </div>
          )}

          {/* 報名按鈕 / 前往挑戰（修正：不再多一層） */}
          {/* 個人挑戰模式：只有「進行中」的 attempt 才顯示「前往挑戰」，completed/expired/cancelled 的
              舊 attempt 都應回到可再報名的按鈕（見 inProgress 計算；personal 以 pp 為準，見上方註解）。
              已報名（含待繳費）一律可按「前往挑戰」導去 GPS 追蹤頁；上方「已報名」徽章同用 inProgress 判定。 */}
          <div style={{ marginTop: 14 }}>
            {inProgress ? (
              <button onClick={goToTrack} style={registerBtn}>▶ 前往挑戰</button>
            ) : detail?.can_register && onRegister ? (
              <button onClick={handleRegisterClick} style={registerBtn}>{isPersonal ? '報名挑戰' : '立即報名'}</button>
            ) : null}
          </div>

          {/* 完賽證明（賽事結束後，完賽者：預覽縮圖→點擊放大→下載）；personal 模式不顯示，改用下方
              「完賽歷程」；certificate_disabled 開關兩者共用（見上方 certificateDisabled 註解） */}
          {!isPersonal && !certificateDisabled && ended && cert?.completed && (
            <div style={{ marginTop: 14 }}>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 8 }}>完賽證明</div>
              {certImg ? (
                <>
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={certImg}
                    alt="完賽證明"
                    onClick={() => setCertZoom(true)}
                    style={{ width: '100%', borderRadius: 12, border: '1px solid var(--line-2)', cursor: 'zoom-in', display: 'block' }}
                  />
                  <button
                    onClick={() => certRender && downloadCertificate(certRender, `完賽證明_${cert.race_title}.png`)}
                    style={certBtn}
                  ><span className="skin-ico" data-ico="star" aria-hidden>🏅</span> 下載完賽證明</button>
                </>
              ) : certErr ? (
                <button onClick={() => setCertRetry((n) => n + 1)} style={certRetryBtn}>證明產生失敗，請重試</button>
              ) : (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '8px 0' }}>產生證明中…</div>
              )}
            </div>
          )}
          {!isPersonal && !certificateDisabled && ended && cert && !cert.completed && registration && (
            <div style={{ marginTop: 10, fontSize: 12, color: 'var(--tx-faint)', textAlign: 'center' }}>本場未達完賽標準，無完賽證明</div>
          )}

          {/* 完賽歷程（personal 模式取代完賽證明：挑戰次數/完成次數/最佳成績/最近完成時間）。
              可重複挑戰，不比照一般模式綁 ended——只要登入且開關未關即顯示，尚無 attempt 時顯示空狀態。 */}
          {isPersonal && !certificateDisabled && token && (
            <div style={{ marginTop: 14 }}>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 8 }}>完賽歷程</div>
              {!history ? (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '8px 0' }}>載入中…</div>
              ) : history.total_attempts === 0 ? (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '8px 0', textAlign: 'center' }}>
                  尚無挑戰紀錄，完成一次挑戰後這裡會顯示你的歷程
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
                  <Row k="挑戰次數" v={`${history.total_attempts} 次`} />
                  <Row k="完成次數" v={`${history.completed_count} 次`} />
                  {formatPersonalBest(history) && <Row k="最佳成績" v={formatPersonalBest(history)} />}
                  {history.last_completed_at && <Row k="最近完成" v={fmt(history.last_completed_at)} />}
                </div>
              )}
            </div>
          )}

          {/* 本場 EXP 結算（重看） */}
          {ended && breakdown && breakdown.gained > 0 && (
            <button onClick={() => setShowExp(true)} style={expBtn}>🎮 查看本場結算（+{breakdown.gained} EXP）</button>
          )}
        </div>

        {/* 物資（報名禮）面板：緊接在「立即報名」面板下方，讓使用者一進頁面就看到報名禮／完賽物資
            （原本掛在簡章頁籤尾部，v0.1.537 搬到此處；資料仍吃同一份 detail.supplies，不多打 API）；
            尾部追加參賽虛擬獎勵展示（migration 140，見 entryRewardPreview） */}
        {detail && <SuppliesBody supplies={detail.supplies} entryRewards={entryRewardPreview} />}

        {/* 頁籤 */}
        <div style={{ display: 'flex', gap: 6, margin: '16px 0 14px', borderBottom: '1px solid var(--line)' }}>
          {(([['brochure', '簡章'], ['progress', '進度'], ...(hasCheckpoints ? [['explore', '探索']] : []), ['rank', '排名'], ...(rewardPreview.length > 0 ? [['reward', '活動獎勵']] : [])]) as [Tab, string][]).map(([v, label]) => (
            <button key={v} onClick={() => setTab(v)} style={{
              padding: '8px 14px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14,
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: tab === v ? 700 : 400,
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
            }}>{label}</button>
          ))}
        </div>

        {!started && (tab === 'progress' || tab === 'rank') && (
          <div style={notStartedHint}>賽事尚未開始，敬請期待。</div>
        )}

        {tab === 'brochure' && (detail ? <BrochureBody detail={detail} /> : <Hint>載入中…</Hint>)}
        {/* 修 bug：!started 時只顯示上面的「尚未開始」提示，不再額外掛載 ProgressBody——原本兩者同時渲染，
            未開始的賽事本就無活動可算（後端 GetRaceProgress 統計只認 recorded_at>=start_date），掛載了也只是
            白白多打一次 API 且和上面的提示重複；已開始才需要真的顯示進度內容。registered 用父層已算好的
            inProgress（不必等這支 API 才知道，避免「未報名」的引導閃爍）。 */}
        {tab === 'progress' && (started ? (
          <ProgressBody
            race={race}
            registered={inProgress}
            onRegister={detail?.can_register && onRegister ? handleRegisterClick : undefined}
          />
        ) : null)}
        {tab === 'explore' && <ExploreBody race={race} />}
        {tab === 'rank' && <RankingBody race={race} />}
        {tab === 'reward' && <RewardPreviewBody rewards={rewardPreview} />}
      </ScrollArea>

      {/* 完賽證明全屏檢視 */}
      {certZoom && certImg && cert && (
        <div onClick={() => setCertZoom(false)} style={lightbox}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={certImg} alt="完賽證明" style={{ maxWidth: '96%', maxHeight: '82%', borderRadius: 8, boxShadow: '0 8px 40px rgba(0,0,0,.6)' }} />
          <button onClick={(e) => { e.stopPropagation(); certRender && downloadCertificate(certRender, `完賽證明_${cert.race_title}.png`) }} style={lightboxDl}><span className="skin-ico" data-ico="star" aria-hidden>🏅</span> 下載完賽證明</button>
          <div onClick={() => setCertZoom(false)} style={{ position: 'absolute', top: 14, right: 20, color: '#fff', fontSize: 30, cursor: 'pointer', lineHeight: 1 }}>✕</div>
        </div>
      )}

      {/* 本場 EXP 結算演出。與下方「完成獲得獎勵」彈窗互斥排隊：兩者同 z-index，若同時觸發（個人挑戰於
          結束當下完成）先只顯示獎勵彈窗，待其關閉後 showExp 仍為 true 才補跳 EXP 結算，避免互相完全遮蓋。 */}
      {showExp && breakdown && breakdown.gained > 0 && !(rewardGranted && rewardGranted.length > 0) && (
        <ExpSettlementModal breakdown={breakdown} subtitle={race.title} onClose={() => setShowExp(false)} />
      )}

      {/* 非 VIP 點「立即報名」VIP 專屬賽事 → 提示 + 升級 CTA */}
      {showUpgrade && (
        <UpgradeVipModal
          reason="VIP專屬活動。"
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
          onSuccess={() => { vipFlow.handleBindSuccess(); setShowUpgrade(false) }}
        />
      )}

      {/* 完成挑戰即得獎勵彈窗（活動獎勵系統 P3；只跳一次，見上方 rewardGranted 的 effect 註解） */}
      {rewardGranted && rewardGranted.length > 0 && (
        <RewardGrantedModal rewards={rewardGranted} onClose={() => setRewardGranted(undefined)} />
      )}
    </div>
  )
}

// 修 bug：舊版 `isLoading || !prog` 把「載入中」「fetch 失敗（error）」「回應但資料為空」三種狀態通通顯示成
// 同一句「載入中…」——一旦 API 失敗（逾時／短暫 5xx／網路抖動），isLoading 會安定為 false 但 prog 永遠是
// undefined，畫面就卡死在「載入中」、使用者以為壞掉且沒有任何重試手段。這裡拆出 error 分支＋重試鈕
// （比照同檔 GroupMembers 彈窗、RaceRankingScreen 的 RankingBody 既有寫法）。
// registered／onRegister 由父層傳入（見呼叫點）：未報名（含未登入）者仍照打這支 API，讓「賽事集體」等
// 公開任務進度可見（後端 GetRaceProgress 對未報名者 myGroup 為空，team/individual 任務天然被略過，只留
// 公開的集體任務），但「我的統計」／「歷程記錄」對未報名者沒有意義（永遠是 0，容易誤會成「還沒開始跑」），
// 改顯示報名引導；用父層已算好的狀態而非等這支 API 的 registered 欄位回來，避免先閃一下錯誤內容。
function ProgressBody({ race, registered, onRegister }: { race: Race; registered: boolean; onRegister?: () => void }) {
  const token = getUserToken() || undefined
  const { data, error, isLoading, mutate } = useSWR(['progress', race.id], () => racesApi.progress(race.id, token), { refreshInterval: 30000 })
  const [detailTask, setDetailTask] = useState<TaskProgress | null>(null)
  const [rangeTask, setRangeTask] = useState<TaskProgress | null>(null)
  const prog = data?.progress

  if (error) {
    return (
      <Hint color="var(--hunt)">
        載入失敗，下拉重試
        <div style={{ marginTop: 10 }}>
          <button onClick={() => mutate()} style={certRetryBtn}>重新載入</button>
        </div>
      </Hint>
    )
  }
  if (isLoading || !prog) return <Hint>載入中…</Hint>

  const tasks = prog.tasks ?? []
  const my = prog.my ?? { total_km: 0, activities: 0, ascent_m: 0 }
  const groupsBy: { label: string; tasks: TaskProgress[] }[] = []
  for (const label of ['賽事集體', '所有分組共同（團體）', '本組團體', '所有分組共同（個人）', '本組個人']) {
    const ts = tasks.filter((t) => t.scope_label === label)
    if (ts.length) groupsBy.push({ label, tasks: ts })
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* 我的統計：未報名者顯示報名引導取代（見上方元件註解） */}
      {registered ? (
        <div style={{ display: 'flex', gap: 10 }}>
          <Stat label="我的里程" value={`${my.total_km.toFixed(1)} K`} />
          <Stat label="活動" value={`${my.activities}`} />
          <Stat label="爬升" value={`${Math.round(my.ascent_m)} m`} />
        </div>
      ) : (
        <div style={progressRegHint}>
          <div>報名後即可查看你的個人進度</div>
          {onRegister && <button onClick={onRegister} style={progressRegHintBtn}>前往報名</button>}
        </div>
      )}

      {/* 每日歷程記錄：第一層看每天統計，點「詳細」展開當天各筆活動（里程窗與上方「我的里程」完全一致，故每日加總對得起總里程）；未報名者沒有個人歷程可看 */}
      {registered && <DailyHistory race={race} />}

      {tasks.length === 0 && <Hint>此賽事尚未設定任務目標</Hint>}

      {groupsBy.map((g) => (
        <div key={g.label}>
          <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)', marginBottom: 8 }}>{g.label}任務</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {g.tasks.map((t, i) => <TaskRow key={t.id ?? i} t={t} onClick={t.id ? () => (METRIC_BY_KEY[t.metric_type]?.kind === 'range' ? setRangeTask(t) : setDetailTask(t)) : undefined} />)}
          </div>
        </div>
      ))}

      {detailTask && <TaskContributorsModal race={race} task={detailTask} onClose={() => setDetailTask(null)} />}
      {rangeTask && <RangeDetailModal race={race} task={rangeTask} onClose={() => setRangeTask(null)} />}
    </div>
  )
}

// 台北時區日期（YYYY-MM-DD）：offsetDays 為相對天數（0=今天、-1=昨天）。en-CA 產出正好是 YYYY-MM-DD 格式。
function twDate(offsetDays = 0): string {
  return new Date(Date.now() + offsetDays * 86400000).toLocaleDateString('en-CA', { timeZone: 'Asia/Taipei' })
}
// 每日標題：今天/昨天，其餘顯示 M/D（週X）
function fmtDayLabel(date: string): string {
  if (date === twDate(0)) return '今天'
  if (date === twDate(-1)) return '昨天'
  const [y, m, dd] = date.split('-').map(Number)
  const wk = ['日', '一', '二', '三', '四', '五', '六'][new Date(Date.UTC(y, m - 1, dd)).getUTCDay()]
  return `${m}/${dd}（週${wk}）`
}
// 活動時間 → 台北 HH:MM
function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString('zh-TW', { hour: '2-digit', minute: '2-digit', hour12: false, timeZone: 'Asia/Taipei' })
}
// 時長秒數 → m:ss（超過 1 小時顯示 h:mm:ss）
function fmtDur(sec: number): string {
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
  return `${m}:${String(s).padStart(2, '0')}`
}
// 資料來源標籤：App GPS(空字串)不標，其餘顯示品牌
function sourceLabel(src: string): string {
  switch (src) {
    case '': return ''
    case 'strava': return 'Strava'
    case 'garmin': return 'Garmin'
    case 'coros': return 'COROS'
    default: return src
  }
}
const sourceChip: React.CSSProperties = { fontSize: 10, fontWeight: 700, color: 'var(--tx-dim)', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '1px 7px', whiteSpace: 'nowrap' }

// 進度頁「每日歷程記錄」：第一層每天一列（日期＋當天總里程＋筆數），點「詳細」展開當天各筆活動
//（時間/距離/時長/配速/來源）。里程窗與 GetRaceProgress 的「我的里程」一致，每日加總對得起總里程。
function DailyHistory({ race }: { race: Race }) {
  const token = getUserToken() || undefined
  // 未登入無個人歷程可查（後端未登入回 404）→ 傳 null key 直接停用抓取，避免無謂請求
  const { data, isLoading } = useSWR(token ? ['daily', race.id] : null, () => racesApi.myDailyActivities(race.id, token), { refreshInterval: 30000 })
  const [open, setOpen] = useState<Record<string, boolean>>({})
  const days = data?.days ?? []
  if (isLoading || days.length === 0) return null // 靜默：載入中或尚無活動就不佔位（避免空白區塊）
  return (
    <div>
      <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)', marginBottom: 8 }}>歷程記錄</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {days.map((d) => {
          const isOpen = !!open[d.date]
          return (
            <div key={d.date} style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 12px)', overflow: 'hidden' }}>
              <div onClick={() => setOpen((o) => ({ ...o, [d.date]: !o[d.date] }))}
                   style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, padding: '11px 13px', cursor: 'pointer' }}>
                <div style={{ display: 'flex', flexDirection: 'column' }}>
                  <span style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)' }}>{fmtDayLabel(d.date)}</span>
                  <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 1 }}>{d.count} 筆</span>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--fug)' }}>{d.total_km.toFixed(2)} K</span>
                  <span style={{ fontSize: 11, color: 'var(--gold)', whiteSpace: 'nowrap' }}>{isOpen ? '收合 ▾' : '詳細 ›'}</span>
                </div>
              </div>
              {isOpen && (
                <div style={{ borderTop: '1px solid var(--line)', padding: '4px 13px 6px' }}>
                  {d.activities.map((a, i) => (
                    <div key={i} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, padding: '7px 0', borderBottom: i < d.activities.length - 1 ? '1px solid var(--line-2)' : 'none' }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                        <span style={{ fontSize: 12, color: 'var(--tx-faint)', width: 42, flexShrink: 0 }}>{fmtTime(a.recorded_at)}</span>
                        <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--tx)' }}>{a.distance_km.toFixed(2)} km</span>
                        {sourceLabel(a.source) && <span style={sourceChip}>{sourceLabel(a.source)}</span>}
                      </div>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexShrink: 0 }}>
                        <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{fmtDur(a.duration_s)}</span>
                        <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{a.avg_pace_s > 0 ? `${paceFmt(a.avg_pace_s)}/km` : '—'}</span>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

// 活動獎勵頁籤：完成活動有機會獲得的獎勵預覽卡片列表。名稱後方視情況加中獎機率小字（消保法機率揭露，
// 見 formatProbLabel；100% 必得的項目不標示）。仍只顯示 icon/名稱/說明/機率，不露權重與庫存等抽獎引擎
// 內部設定（後端 race.GetRewardPreview 白名單欄位，見 memory activity-reward-system）。
function RewardPreviewBody({ rewards }: { rewards: RewardPreviewItem[] }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div>
        <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>有機會獲得以下獎勵</div>
        <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 3 }}>實際獲得依完成當次結果為準</div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {rewards.map((rw, i) => {
          const probLabel = formatProbLabel(rw.prob_bp)
          return (
            <div key={i} style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 12px)', overflow: 'hidden' }}>
              {rw.icon_url && (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={rw.icon_url} alt="" style={{ width: '100%', aspectRatio: '2 / 1', objectFit: 'cover', display: 'block' }} />
              )}
              <div style={{ padding: '11px 13px' }}>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, flexWrap: 'wrap' }}>
                  <span style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)' }}>{rw.name}</span>
                  {probLabel && <span style={{ fontSize: 11, fontWeight: 400, color: 'var(--tx-faint)' }}>{probLabel}</span>}
                  {rw.amount && <span style={{ fontSize: 14, fontWeight: 800, color: 'var(--gold)' }}>{rw.amount}</span>}
                </div>
                {rw.description && (
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.5 }}>{rw.description}</div>
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function TaskRow({ t, onClick }: { t: TaskProgress; onClick?: () => void }) {
  const clickable = !!onClick
  const clickStyle = clickable ? { cursor: 'pointer' as const } : {}
  const hint = clickable ? <div style={{ fontSize: 11, color: 'var(--gold)', marginTop: 8, textAlign: 'right' }}>{METRIC_BY_KEY[t.metric_type]?.kind === 'range' ? '查看哪幾公里達標' : '查看里程貢獻榜'} ›</div> : null
  const m = METRIC_BY_KEY[t.metric_type]
  if (m?.kind === 'checkpoint') {
    const cps = t.checkpoints ?? []
    const collected = cps.filter((c) => c.collected).length
    return (
      <div onClick={onClick} style={{ background: 'var(--bg-1)', border: `1px solid ${t.done ? 'var(--fug)' : 'var(--line)'}`, borderRadius: 'var(--radius-md, 12px)', padding: '11px 13px', ...clickStyle }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, alignItems: 'baseline' }}>
          <span style={{ fontSize: 13.5, fontWeight: 700 }}>{t.done ? '✓ ' : ''}{t.title || m?.label}</span>
          <span style={{ fontSize: 12, color: t.done ? 'var(--fug)' : 'var(--tx-dim)', whiteSpace: 'nowrap' }}>集章 {collected}/{cps.length}</span>
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>指定地點打卡</div>
        {cps.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
            {cps.map((c, i) => (
              <span key={c.id ?? i} style={{
                fontSize: 11.5, padding: '3px 9px', borderRadius: 999,
                background: c.collected ? 'rgba(70,227,160,.15)' : c.pending ? 'rgba(245,194,66,.15)' : 'var(--bg-2)',
                border: `1px solid ${c.collected ? 'var(--fug)' : c.pending ? 'var(--gold)' : 'var(--line-2)'}`,
                color: c.collected ? 'var(--fug)' : c.pending ? 'var(--gold)' : 'var(--tx-dim)',
              }}>{c.collected ? '✓ ' : c.pending ? '⏳ ' : '○ '}{c.title || `點 ${i + 1}`}</span>
            ))}
          </div>
        )}
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>到各點半徑內以「開始跑步」打卡，集滿即完成</div>
        {hint}
      </div>
    )
  }
  const isRange = m?.kind === 'range'
  let targetText = ''
  let pct = 0
  if (isRange) {
    if (t.metric_type === 'avg_pace_range') targetText = `${paceFmt(t.range_lo ?? 0)}–${paceFmt(t.range_hi ?? 0)} /km`
    else targetText = `${t.range_lo ?? '—'}–${t.range_hi ?? '—'} ${m?.unit ?? ''}`
  } else {
    targetText = `≥ ${t.target_value ?? '—'} ${m?.unit ?? ''}`
    const target = t.target_value ?? 0
    pct = target > 0 ? Math.min(100, (t.current / target) * 100) : 0
  }
  return (
    <div onClick={onClick} style={{ background: 'var(--bg-1)', border: `1px solid ${t.done ? 'var(--fug)' : 'var(--line)'}`, borderRadius: 12, padding: '11px 13px', ...clickStyle }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, alignItems: 'baseline' }}>
        <span style={{ fontSize: 13.5, fontWeight: 700 }}>{t.done ? '✓ ' : ''}{t.title || m?.label}</span>
        <span style={{ fontSize: 12, color: t.done ? 'var(--fug)' : 'var(--tx-dim)', whiteSpace: 'nowrap' }}>{targetText}</span>
      </div>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>{m?.label}</div>
      {isRange ? (
        <div style={{ fontSize: 12, marginTop: 6, color: t.done ? 'var(--fug)' : 'var(--tx-dim)' }}>
          {t.done ? `已達標 · 符合 ${t.qualify_count} 筆` : '尚未有符合區間的活動'}
        </div>
      ) : (
        <>
          <div style={{ height: 6, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 7 }}>
            <div style={{ height: '100%', width: `${pct}%`, background: t.done ? 'var(--fug)' : 'var(--gold)', borderRadius: 999 }} />
          </div>
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4, textAlign: 'right' }}>
            {t.current} / {t.target_value ?? '—'} {m?.unit ?? ''}
            {/* 每日／每週類指標顯示的是「最佳單日／單週」桶值（後端 metricValue bestBucket），不是累計——
                貢獻榜是累計總和，兩者本來就不同口徑；標出來避免被當成數據落差（2026-09-03 使用者回報） */}
            {t.metric_type === 'daily_distance' ? '（最佳單日）' : t.metric_type === 'weekly_distance' ? '（最佳單週）' : ''}
          </div>
        </>
      )}
      {hint}
    </div>
  )
}

// 任務貢獻明細彈窗：前 20 名里程貢獻 + 自己（即使在 20 名外）

function TaskContributorsModal({ race, task, onClose }: { race: Race; task: TaskProgress; onClose: () => void }) {
  useScrollLock() // 開啟時鎖背景捲動 → 只滑得動本彈窗清單
  const { panelRef, dy } = useSheetDismiss(onClose) // 下滑關閉 → 短清單也有反應
  const token = getUserToken() || undefined
  const { data, isLoading, error } = useSWR(['contrib', race.id, task.id], () => racesApi.taskContributors(race.id, task.id!, token))
  const c: TaskContributors | undefined = data?.contributors
  const meInTop = c?.top.some((x) => x.is_me)
  // 追蹤（樂觀更新）：先切換本地狀態、再打 API，失敗回滾
  const [followOverride, setFollowOverride] = useState<Record<string, boolean>>({})
  const isFollowing = (x: TaskContributors['top'][number]) => followOverride[x.user_id] ?? x.is_following
  async function toggleFollow(x: TaskContributors['top'][number]) {
    const t = getUserToken()
    if (!t) return
    const cur = isFollowing(x)
    setFollowOverride((o) => ({ ...o, [x.user_id]: !cur }))
    try {
      if (cur) await followApi.unfollow(t, x.user_id)
      else await followApi.follow(t, x.user_id)
    } catch {
      setFollowOverride((o) => ({ ...o, [x.user_id]: cur }))
    }
  }
  const row = (x: TaskContributors['top'][number], showRank = true) => (
    <div key={x.user_id} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px', borderRadius: 'var(--radius-md, 9px)', background: x.is_me ? 'rgba(255,194,75,.14)' : 'var(--bg-2)', border: x.is_me ? '1px solid var(--gold)' : '1px solid transparent' }}>
      <span style={{ width: 30, textAlign: 'center', fontWeight: 900, fontSize: 13, color: x.rank <= 3 ? 'var(--gold)' : 'var(--tx-dim)' }}>{showRank ? x.rank : '·'}</span>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
          {x.title && <span style={{ color: 'var(--gold)', fontWeight: 800, marginRight: 5 }}>{x.title}</span>}{x.name}{x.is_me ? '（我）' : ''}
        </div>
        {x.group_name && <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{x.group_name}</div>}
      </div>
      <div style={{ textAlign: 'right' }}>
        <div style={{ fontSize: 14, fontWeight: 900, color: 'var(--fug)', fontVariantNumeric: 'tabular-nums' }}>{x.distance_km.toFixed(1)} <span style={{ fontSize: 11, color: 'var(--tx-dim)' }}>km</span></div>
        <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>{x.activities} 筆</div>
      </div>
      {token && !x.is_me && (
        <FollowHeartButton following={isFollowing(x)} onClick={() => toggleFollow(x)} size={16} />
      )}
    </div>
  )
  // 掛載點：手機模擬框內→portal 進框(桌機不鋪滿視窗)；獨立路由(無手機框)→退回 document.body(視窗)
  const om = overlayMount()
  const content = (
    <div onClick={onClose} style={{ position: om.position, inset: 0, zIndex: 3000, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'flex-end', justifyContent: 'center' }}>
      <div ref={panelRef} onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 460, maxHeight: '82dvh', overflow: 'hidden', display: 'flex', flexDirection: 'column', background: 'var(--bg-1)', borderRadius: '18px 18px 0 0', border: '1px solid var(--line-2)', borderBottom: 'none', transform: dy ? `translateY(${dy}px)` : undefined, transition: dy ? 'none' : 'transform .22s ease', willChange: 'transform' }}>
        <div style={{ display: 'flex', justifyContent: 'center', padding: '8px 0 2px', flexShrink: 0 }}><div style={{ width: 38, height: 4, borderRadius: 999, background: 'var(--line-2)' }} /></div>
        <div style={{ padding: '4px 18px 10px', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10 }}>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)' }}>{task.title || '任務'} · 里程貢獻榜</div>
              <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 3 }}>{c ? `${c.pool_label} · ${c.contributed}/${c.total} 人已貢獻` : '　'}</div>
            </div>
            <button onClick={onClose} style={{ background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 12px', color: 'var(--tx)', fontSize: 12.5, cursor: 'pointer', flexShrink: 0 }}>關閉</button>
          </div>
        </div>
        <ScrollArea padding="14" lockPass>
          {error ? <Hint>載入失敗，請稍後再試</Hint> : isLoading || !c ? <Hint>載入中…</Hint> : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {c.top.length === 0 && <Hint>目前還沒有人貢獻里程</Hint>}
              {c.top.map((x) => row(x))}
              {c.me && !meInTop && (
                <>
                  <div style={{ textAlign: 'center', color: 'var(--tx-faint)', fontSize: 14, padding: '2px 0' }}>⋯</div>
                  {row(c.me)}
                </>
              )}
            </div>
          )}
        </ScrollArea>
      </div>
    </div>
  )
  return om.node ? createPortal(content, om.node) : content
}

// 區間任務達標明細：點進去看自己哪幾公里/哪幾筆落在配速（或心率）區間
function RangeDetailModal({ race, task, onClose }: { race: Race; task: TaskProgress; onClose: () => void }) {
  useScrollLock() // 開啟時鎖背景捲動 → 只滑得動本彈窗清單
  const { panelRef, dy } = useSheetDismiss(onClose) // 下滑關閉 → 短清單也有反應
  const token = getUserToken() || undefined
  const { data, isLoading, error } = useSWR(['rangedetail', race.id, task.id], () => racesApi.taskRangeDetail(race.id, task.id!, token))
  const d: TaskRangeDetail | undefined = data?.detail
  const isPace = task.metric_type === 'avg_pace_range'
  const rangeText = d ? (isPace ? `${paceFmt(d.range_lo)}–${paceFmt(d.range_hi)} /km` : `${d.range_lo}–${d.range_hi}`) : ''
  // 掛載點：手機模擬框內→portal 進框(桌機不鋪滿視窗)；獨立路由(無手機框)→退回 document.body(視窗)
  const om = overlayMount()
  const content = (
    <div onClick={onClose} style={{ position: om.position, inset: 0, zIndex: 3000, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'flex-end', justifyContent: 'center' }}>
      <div ref={panelRef} onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 460, maxHeight: '82dvh', overflow: 'hidden', display: 'flex', flexDirection: 'column', background: 'var(--bg-1)', borderRadius: '18px 18px 0 0', border: '1px solid var(--line-2)', borderBottom: 'none', transform: dy ? `translateY(${dy}px)` : undefined, transition: dy ? 'none' : 'transform .22s ease', willChange: 'transform' }}>
        <div style={{ display: 'flex', justifyContent: 'center', padding: '8px 0 2px', flexShrink: 0 }}><div style={{ width: 38, height: 4, borderRadius: 999, background: 'var(--line-2)' }} /></div>
        <div style={{ padding: '4px 18px 10px', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10 }}>
            <div style={{ minWidth: 0 }}>
              <div style={{ fontSize: 15, fontWeight: 900, color: 'var(--tx)' }}>{task.title || '任務'} · 達標明細</div>
              <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 3 }}>{isPace ? `配速落在 ${rangeText} 的公里就算達標` : `平均心率落在 ${rangeText} 就算達標`}</div>
            </div>
            <button onClick={onClose} style={{ background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 12px', color: 'var(--tx)', fontSize: 12.5, cursor: 'pointer', flexShrink: 0 }}>關閉</button>
          </div>
        </div>
        <ScrollArea padding="14" lockPass>
          {error ? <Hint>載入失敗，請稍後再試</Hint> : isLoading || !d ? <Hint>載入中…</Hint> : d.activities.length === 0 ? <Hint>此賽事期間還沒有你的跑步紀錄</Hint> : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {d.activities.map((a, i) => (
                <div key={i} style={{ background: 'var(--bg-2)', borderRadius: 10, padding: '10px 12px', border: a.qualified ? '1px solid var(--fug)' : '1px solid transparent' }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 8 }}>
                    <span style={{ fontSize: 13, fontWeight: 700, color: 'var(--tx)' }}>{fmt(a.recorded_at)}</span>
                    <span style={{ fontSize: 12, fontWeight: 800, color: a.qualified ? 'var(--fug)' : 'var(--tx-faint)' }}>{a.qualified ? '✓ 有達標' : '未達標'}</span>
                  </div>
                  <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', marginTop: 2 }}>{a.distance_km.toFixed(2)} km · 均速 {paceFmt(a.avg_pace_s)}/km{isPace ? '' : ` · 均心率 ${a.avg_hr || '—'}`}</div>
                  {isPace && a.km_paces.length > 0 && (
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5, marginTop: 8 }}>
                      {a.km_paces.map((p, k) => {
                        const on = a.qualify_kms.includes(k + 1)
                        return <span key={k} style={{ fontSize: 11, padding: '2px 7px', borderRadius: 999, background: on ? 'rgba(70,227,160,.15)' : 'var(--bg-1)', border: `1px solid ${on ? 'var(--fug)' : 'var(--line-2)'}`, color: on ? 'var(--fug)' : 'var(--tx-faint)', fontWeight: on ? 700 : 400 }}>{k + 1}k {paceFmt(p)}{on ? ' ✓' : ''}</span>
                      })}
                    </div>
                  )}
                  {isPace && a.km_paces.length === 0 && <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6 }}>（此筆無每公里分段，以整段均速判定）</div>}
                </div>
              ))}
            </div>
          )}
        </ScrollArea>
      </div>
    </div>
  )
  return om.node ? createPortal(content, om.node) : content
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ flex: 1, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 12px)', padding: '10px 12px', textAlign: 'center' }}>
      <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)' }}>{value}</div>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 2 }}>{label}</div>
    </div>
  )
}
function Row({ k, v }: { k: string; v: string }) {
  return (
    <div style={{ display: 'flex', gap: 8 }}>
      {/* minWidth+nowrap：標籤一律單行——原寫死 width:56 在完賽歷程字級下四字標籤被折行（2026-08-29 使用者回報） */}
      <span style={{ color: 'var(--tx-faint)', minWidth: 56, whiteSpace: 'nowrap', flexShrink: 0 }}>{k}</span>
      <span style={{ color: 'var(--tx-dim)' }}>{v}</span>
    </div>
  )
}
function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '40px 20px', fontSize: 13.5, color }}>{children}</div>
}

// 「立即報名」面板正下方的獨立面板：報名禮／完賽物資（依 kind 分節，group_id 非空標注分組專屬）。
// 卡片樣式比照上方 dashCard，讓它讀起來是同一系列的面板；supplies 與 entryRewards 皆為空時整塊不渲染。
// entryRewards：參賽虛擬獎勵預覽（migration 140，見後端 race.GetEntryRewardPreview）——賽事開始後自動
// 發放給所有已報名者，附在「參賽物資」小節尾部展示，不含機率/數量（比照 RewardPreviewBody 白名單欄位）。
function SuppliesBody({ supplies, entryRewards }: { supplies: RaceSupply[]; entryRewards?: RewardPreviewItem[] }) {
  const hasEntryRewards = !!entryRewards && entryRewards.length > 0
  if ((!supplies || supplies.length === 0) && !hasEntryRewards) return null
  const raceItems = supplies.filter((s) => s.kind === 'race_pack')
  const finisherItems = supplies.filter((s) => s.kind === 'finisher')
  return (
    <div className="skin-frame" style={{ ...dashCard, marginTop: 14 }}>
      {/* 面板標題「報名禮」只出現一次（無 icon）；報名禮小節不再重複小標，完賽物資保留小標區隔 */}
      <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', marginBottom: 10 }}>報名禮</div>
      {raceItems.length > 0 && <SupplySection label="" items={raceItems} />}
      {hasEntryRewards && <EntryRewardSection items={entryRewards!} />}
      {finisherItems.length > 0 && <SupplySection label="完賽物資" items={finisherItems} />}
    </div>
  )
}

// 參賽虛擬獎勵展示小節（migration 140）：緊接在「參賽物資」小節尾部，卡片樣式比照 SupplySection，
// 但資料來源是 RewardPreviewItem（icon_url/name/amount/description/prob_bp），不是 RaceSupply。名稱後
// 方視情況加中獎機率小字（消保法機率揭露，見 formatProbLabel；100% 必得不標示）。
function EntryRewardSection({ items }: { items: RewardPreviewItem[] }) {
  return (
    <div style={{ marginBottom: 14 }}>
      <div style={{ fontSize: 12.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 6 }}>參賽虛擬獎勵（開賽後自動發放）</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {items.map((rw, i) => {
          const probLabel = formatProbLabel(rw.prob_bp)
          return (
            <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }}>
              {rw.icon_url && (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={rw.icon_url} alt="" style={{ width: 64, height: 48, objectFit: 'cover', borderRadius: 6, border: '1px solid var(--line-2)', flexShrink: 0 }} />
              )}
              <div>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, flexWrap: 'wrap' }}>
                  <span style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)' }}>{rw.name}</span>
                  {probLabel && <span style={{ fontSize: 11, fontWeight: 400, color: 'var(--tx-faint)' }}>{probLabel}</span>}
                  {rw.amount && <span style={{ fontSize: 13, fontWeight: 800, color: 'var(--gold)' }}>{rw.amount}</span>}
                </div>
                {rw.description && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 2 }}>{rw.description}</div>}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
function SupplySection({ label, items }: { label: string; items: RaceSupply[] }) {
  const [zoomImg, setZoomImg] = useState<string | null>(null) // 物資縮圖放大檢視
  return (
    <div style={{ marginBottom: 14 }}>
      {label && <div style={{ fontSize: 12.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 6 }}>{label}</div>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {items.map((s, i) => (
          <div key={s.id ?? i} style={{ display: 'flex', alignItems: 'center', gap: 10, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px' }}>
            {s.image_url && (
              // eslint-disable-next-line @next/next/no-img-element
              <img
                src={s.image_url} alt="" onClick={() => setZoomImg(s.image_url!)}
                style={{ width: 64, height: 48, objectFit: 'cover', borderRadius: 6, border: '1px solid var(--line-2)', flexShrink: 0, cursor: 'zoom-in' }}
              />
            )}
            <div>
              <div style={{ fontSize: 13.5, fontWeight: 700, color: 'var(--tx)' }}>
                {s.name}{s.group_id ? <span style={{ fontSize: 11, fontWeight: 400, color: 'var(--tx-faint)' }}>（分組專屬）</span> : ''}
              </div>
              {s.description && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 2 }}>{s.description}</div>}
            </div>
          </div>
        ))}
      </div>
      {zoomImg && <ImageLightbox src={zoomImg} onClose={() => setZoomImg(null)} />}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const dashCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-lg, 16px)', padding: 16, boxShadow: 'var(--card-shadow, none)' }
const statusBadge: React.CSSProperties = { fontSize: 12, fontWeight: 700, color: 'var(--fug)', background: 'rgba(45,212,150,.1)', border: '1px solid var(--fug)', borderRadius: 999, padding: '2px 10px' }
// 測試中徽章：虛線框（非實線）＋紫色系，跟正式狀態徽章（statusBadge 綠色實線）明顯區隔，不會被誤認成已開放的正式狀態
const testingBadge: React.CSSProperties = { fontSize: 12, fontWeight: 700, color: 'var(--violet)', background: 'rgba(157,140,255,.12)', border: '1px dashed var(--violet)', borderRadius: 999, padding: '2px 10px' }
// 已報名徽章：實心底，比照 registerBtn 的 fug/fug-ink 配色組合（已隨皮膚正確配對前景/背景，非寫死顏色）
const registeredBadge: React.CSSProperties = { fontSize: 12, fontWeight: 700, color: 'var(--fug-ink)', background: 'var(--fug)', borderRadius: 999, padding: '2px 10px' }
// VIP 專屬徽章：金底白字（金黃色實心底框上的文字一律用白色）
const vipBadge: React.CSSProperties = { fontSize: 11, fontWeight: 800, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '2px 10px', whiteSpace: 'nowrap' }
const registerBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 'var(--radius-btn, 12px)', padding: '12px 20px', cursor: 'pointer', fontSize: 15, width: '100%' }
const certBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'linear-gradient(135deg,#E5C46B,#caa64e)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 'var(--radius-btn, 12px)', padding: '12px 20px', cursor: 'pointer', fontSize: 15 }
const certRetryBtn: React.CSSProperties = { width: '100%', background: 'var(--bg-2)', color: 'var(--tx-dim)', fontWeight: 700, border: '1px solid var(--line-2)', borderRadius: 'var(--radius-btn, 12px)', padding: '10px 20px', cursor: 'pointer', fontSize: 13 }
const expBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'rgba(70,227,160,.1)', color: 'var(--fug)', fontWeight: 800, border: '1px solid rgba(70,227,160,.35)', borderRadius: 'var(--radius-btn, 12px)', padding: '11px 20px', cursor: 'pointer', fontSize: 14 }
const lightbox: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 90, background: 'rgba(0,0,0,.88)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 16, padding: 16 }
const lightboxDl: React.CSSProperties = { background: 'linear-gradient(135deg,#E5C46B,#caa64e)', color: '#fff', fontWeight: 800, border: 'none', borderRadius: 10, padding: '11px 22px', cursor: 'pointer', fontSize: 15 }
const notStartedHint: React.CSSProperties = { background: 'rgba(255,210,90,.08)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 12px)', padding: '12px 14px', fontSize: 13, color: 'var(--gold)', marginBottom: 14, textAlign: 'center' }
// 進度頁「未報名」引導區塊（取代「我的統計」；見 ProgressBody）
const progressRegHint: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 'var(--radius-md, 12px)', padding: '16px 14px', textAlign: 'center', fontSize: 13, color: 'var(--tx-dim)' }
const progressRegHintBtn: React.CSSProperties = { marginTop: 10, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none', borderRadius: 'var(--radius-btn, 12px)', padding: '9px 20px', cursor: 'pointer', fontSize: 13 }
