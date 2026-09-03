'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { racesApi, raceStatusFlags, RACE_FILTER_CATEGORY, type Race, type MyRegLite } from '@/lib/api'
import { getUserToken, useUser } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import ScrollArea from './ScrollArea'
import UpgradeVipModal from './UpgradeVipModal'
import BindCardModal from './BindCardModal'
import { useVipSubscribeFlow } from '@/lib/useVipSubscribeFlow'

// 活動探索：2026-09-03 首頁改版拆出來的獨立全頁——原本是首頁上的可拖曳活動列表面板，
// 現在收進「活動探索」入口鈕，開成一個獨立、可直向捲動的全頁（不再是拖曳半展面板）。
// RaceCard/PeriodBox/Hint/DISPLAY_STATUS/篩選/格式化等全部從 RacesScreen 搬過來，行為不變
// （含 VIP 專屬賽事報名 gate 流程）；SWR key 沿用 ['races', user?.id, token]，WS 精準失效照舊命中。

const DISPLAY_STATUS: Record<string, { label: string; color: string }> = {
  upcoming_reg: { label: '即將報名', color: 'var(--violet)' },
  registering: { label: '報名中', color: 'var(--gold)' },
  reg_closed: { label: '報名結束', color: 'var(--tx-faint)' },
  starting_soon: { label: '賽事即將開始', color: 'var(--violet)' },
  racing: { label: '進行中', color: 'var(--fug)' },
  ended: { label: '賽事結束', color: 'var(--tx-faint)' },
  paused: { label: '暫停報名', color: 'var(--hunt)' },
  suspended: { label: '賽事中止', color: 'var(--hunt)' },
}

// 搜尋標籤分類：把 display_status 歸到 報名中/進行中/已結束（CATEGORY fallback 定義搬到 lib/api.ts 的
// RACE_FILTER_CATEGORY 共用，與後台數據總覽(admin/overview)同一份判定，避免兩邊各自維護跑掉）
type FilterKey = 'all' | 'reg' | 'racing' | 'ended'
const CATEGORY = RACE_FILTER_CATEGORY
const FILTER_TABS: { key: FilterKey; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'reg', label: '報名中' },
  { key: 'racing', label: '進行中' },
  { key: 'ended', label: '已結束' },
]

function fmtFee(cents: number) {
  return 'NT$ ' + Math.round(cents / 100).toLocaleString('zh-TW')
}

function fmtDateTime(iso: string) {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function ActivityExploreScreen({
  onBack,
  onOpenRanking,
  onRegister,
  onPay,
  onOpenBrochure,
}: {
  onBack: () => void
  onOpenRanking?: (race: Race) => void
  onRegister?: (race: Race) => void
  onPay?: (race: Race) => void
  onOpenBrochure?: (race: Race) => void
}) {
  const user = useUser() // 登入狀態變動時重新渲染 → 用最新 token 重抓報名狀態
  const token = getUserToken() || undefined
  const { data, error, isLoading } = useSWR(['races', user?.id ?? null, token], () => racesApi.list(token))
  const regs = data?.registrations || {}
  const [filter, setFilter] = useState<FilterKey>('all')
  const [showUpgrade, setShowUpgrade] = useState(false)
  const vipFlow = useVipSubscribeFlow() // VIP 訂閱 Phase E：Subscribe → BindCardModal（比照 ProfileScreen 接線）
  const [vipGateReason, setVipGateReason] = useState<string | undefined>()
  const { dash } = useDashboard()
  const races = data?.races ?? []
  // 「報名中」與「進行中」解耦為獨立、可同時成立的條件（見 raceStatusFlags）：
  // 一場賽事可同時符合多個篩選（例：個人挑戰活動中 ongoing && regOpen → 同時落入 報名中/進行中）。
  // 各分類額外保留 CATEGORY 既有的 fallback 判斷（即將報名/暫停/報名結束/賽事中止/報名截止後賽前準備期），
  // 確保一般賽事在每個 display_status 下仍會落入與改版前相同的分類，不因改採日期判斷而漏篩。
  const filtered = races.filter((r) => {
    if (filter === 'all') return true
    const { ended, ongoing, regOpen } = raceStatusFlags(r)
    if (filter === 'reg') return regOpen || CATEGORY[r.display_status] === 'reg'
    if (filter === 'racing') return ongoing || CATEGORY[r.display_status] === 'racing'
    return ended || CATEGORY[r.display_status] === 'ended' // filter === 'ended'
  })

  // VIP 專屬賽事：非 VIP 點「報名」→ 擋下並跳出提示（不進報名表單）；VIP／非 vip_only 賽事照舊
  function handleRegisterClick(race: Race) {
    if (race.vip_only && !dash?.is_vip) {
      setVipGateReason('VIP專屬活動。')
      setShowUpgrade(true)
      return
    }
    onRegister?.(race)
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 10px', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', lineHeight: 1.3 }}>活動探索</div>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 1 }}>賽事與活動</div>
        </div>
      </header>

      {showUpgrade && (
        <UpgradeVipModal
          reason={vipGateReason}
          onClose={() => { setShowUpgrade(false); setVipGateReason(undefined) }}
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
          onSuccess={() => { vipFlow.handleBindSuccess(); setShowUpgrade(false); setVipGateReason(undefined) }}
        />
      )}

      {/* 搜尋標籤（原本在拖曳面板把手區、置右；現在頁面不會拖曳了，直接橫列在標題下方） */}
      <div style={{ display: 'flex', gap: 6, padding: '0 18px 10px', flexShrink: 0, flexWrap: 'wrap' }}>
        {FILTER_TABS.map(({ key, label }) => {
          const on = filter === key
          return (
            <button key={key} onClick={() => setFilter(key)} style={{
              fontSize: 12, padding: '4px 11px', borderRadius: 999, cursor: 'pointer', whiteSpace: 'nowrap',
              background: on ? 'var(--fug)' : 'transparent',
              color: on ? 'var(--fug-ink)' : 'var(--tx-dim)',
              border: `1px solid ${on ? 'var(--fug)' : 'var(--line-2)'}`,
              fontWeight: on ? 700 : 500,
            }}>{label}</button>
          )
        })}
      </div>

      {/* 可捲動活動列表（一般直向捲動，不再是拖曳半展面板） */}
      <ScrollArea padding="4px 18px 28px">
        {isLoading && <Hint>載入中…</Hint>}
        {error && <Hint color="var(--hunt)">無法載入賽事：{String(error.message || error)}</Hint>}
        {data && races.length === 0 && <Hint>目前沒有賽事</Hint>}
        {data && races.length > 0 && filtered.length === 0 && <Hint>此分類目前沒有活動</Hint>}

        {filtered.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
            {filtered.map((r) => (
              <RaceCard key={r.id} race={r} reg={regs[r.id]} onOpenRanking={onOpenRanking} onRegister={handleRegisterClick} onPay={onPay} onOpenBrochure={onOpenBrochure} />
            ))}
          </div>
        )}
        <div className="skin-footer-deco" aria-hidden />
      </ScrollArea>
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }

function RaceCard({
  race,
  reg,
  onRegister,
  onPay,
  onOpenBrochure,
}: {
  race: Race
  reg?: MyRegLite
  onOpenRanking?: (race: Race) => void
  onRegister?: (race: Race) => void
  onPay?: (race: Race) => void
  onOpenBrochure?: (race: Race) => void
}) {
  const s = DISPLAY_STATUS[race.display_status] ?? { label: race.display_status, color: 'var(--tx-faint)' }
  // 徽章：「報名中」與「進行中」解耦為獨立、可同時成立的條件，故可能同時顯示兩顆
  // （例：個人挑戰模式活動期間可報名）；賽事結束一律只顯示「賽事結束」；
  // 三者皆不成立（賽前/暫停等）才 fallback 回原本互斥的 display_status 標籤。
  const { ended: raceEnded, ongoing, regOpen } = raceStatusFlags(race)
  const badges: { label: string; color: string }[] = raceEnded
    ? [DISPLAY_STATUS.ended]
    : [...(ongoing ? [DISPLAY_STATUS.racing] : []), ...(regOpen ? [DISPLAY_STATUS.registering] : [])]
  if (badges.length === 0) badges.push(s)
  const canRegister = race.can_register
  // 只有「進行中」(pending/paid未完成) 的報名才算「已報名」；completed/expired/cancelled 的歷史報名
  // （後端 GetUserRegistrations 為個人挑戰模式回傳最近一筆歷史狀態，見 repository.go 註解）應回到可
  // 再報名的按鈕——與 RaceDetailScreen/RegistrationScreen 的 inProgress 判斷一致（取消報名核准結算後
  // registrations.status→'cancelled'，這裡才會正確切回「立即報名」；取消審核中 status 未變不受影響）。
  const regActive = !!reg && (reg.status === 'pending' || reg.status === 'paid')
  const stop = (e: React.MouseEvent) => e.stopPropagation()
  return (
    <div
      className="skin-frame"
      onClick={() => onOpenBrochure?.(race)}
      style={{
        background: 'var(--bg-1)',
        border: '1px solid var(--line)',
        borderRadius: 'var(--radius-lg, 18px)',
        boxShadow: 'var(--card-shadow, none)',
        overflow: 'hidden',
        cursor: onOpenBrochure ? 'pointer' : 'default',
      }}
    >
      {/* 頂部 Banner：與「活動說明頁」頂部同一張 hero_image_url */}
      {race.hero_image_url && (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={race.hero_image_url} alt="" loading="lazy" style={{ width: '100%', display: 'block' }} />
      )}

      <div style={{ padding: 'var(--card-pad, 18px)', display: 'flex', flexDirection: 'column', gap: 12 }}>
        {/* 標題整寬（不與右側標籤並排，避免擠壓換行） */}
        <div>
          <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--tx)', lineHeight: 1.3, wordBreak: 'keep-all', overflowWrap: 'break-word', display: 'block' }}>{race.title}</span>
          {/* 同一列：參加資格（VIP專屬金底白字／所有會員線框無底，置左）＋ 狀態徽章（進行中／報名中，置右） */}
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, flexWrap: 'wrap', marginTop: 8 }}>
            {race.vip_only ? (
              <span style={{ fontSize: 11.5, fontWeight: 800, padding: '2px 9px', borderRadius: 8, background: 'var(--gold)', color: '#fff', flexShrink: 0 }}>VIP專屬</span>
            ) : (
              <span style={{ fontSize: 11.5, fontWeight: 700, padding: '2px 9px', borderRadius: 8, background: 'transparent', border: '1px solid var(--line-2)', color: 'var(--tx)', flexShrink: 0 }}>所有會員</span>
            )}
            <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', justifyContent: 'flex-end' }}>
              {/* 測試中標籤：只有白名單成員能看到這場賽事本身，能看到就代表本來就有權限看，不是外洩——
                  用虛線框＋紫色系跟正式狀態徽章（實線框）區隔，避免誤以為是已開放的正式狀態 */}
              {race.is_testing && (
                <span style={{ fontSize: 11, fontWeight: 700, padding: '4px 10px', borderRadius: 999, color: 'var(--violet)', border: '1px dashed var(--violet)', background: 'rgba(255,255,255,.03)', whiteSpace: 'nowrap' }}>🧪 測試中</span>
              )}
              {badges.map((b) => (
                <span key={b.label} style={{ fontSize: 11, padding: '4px 10px', borderRadius: 999, color: b.color, border: `1px solid ${b.color}`, background: 'rgba(255,255,255,.03)', whiteSpace: 'nowrap' }}>● {b.label}</span>
              ))}
            </div>
          </div>
          {race.subtitle && <div style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-dim)', marginTop: 6 }}>{race.subtitle}</div>}
        </div>

        {/* 報名期間 / 賽事期間：兩並排資訊卡（各帶 icon） */}
        <div style={{ display: 'flex', gap: 8 }}>
          <PeriodBox icon="/source/ui/01_icons/icon_calendar_orange.png" label="報名期間" start={dt(race.registration_start)} end={dt(race.registration_end)} />
          <PeriodBox icon="/source/ui/01_icons/icon_runner_green.png" label="活動期間" start={dt(race.start_date)} end={dt(race.end_date)} />
        </div>

        {/* 底列：報名費用（左）＋ 立即報名／報名完成／前往繳費（右） */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 13, gap: 10 }}>
          <span style={{ fontSize: 12.5, color: 'var(--tx-dim)', flexShrink: 0 }}>
            報名費用 <b style={{ color: 'var(--tx)', fontSize: 13.5, fontWeight: 800 }}>
              {race.display_fee_cents > 0 ? fmtFee(race.display_fee_cents) : '免費'}{race.fee_mode === 'per_group' ? ' 起' : ''}
            </b>
          </span>
          {regActive
            ? (reg!.status === 'paid'
                ? <span style={{ ...ctaLink, cursor: 'default' }}>報名完成 ›</span>
                : <button onClick={(e) => { stop(e); onPay?.(race) }} style={ctaLink}>前往繳費 ›</button>)
            : (canRegister && onRegister
                ? <button onClick={(e) => { stop(e); onRegister(race) }} style={ctaLink}>立即報名 ›</button>
                : <span style={{ color: 'var(--tx-faint)', fontSize: 12.5 }}>{s.label}</span>)}
        </div>
      </div>
    </div>
  )
}

function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return (
    <div style={{ textAlign: 'center', padding: '60px 20px', fontSize: 13.5, color }}>{children}</div>
  )
}

// 期間 iso → 「M/D HH:MM」；空值顯示「未設定」
function dt(iso?: string | null) { return iso ? fmtDateTime(iso) : '未設定' }

// 活動面板的「報名期間／活動期間」資訊卡（帶 icon；開始/結束分兩行，好讀、不用 – 連接）
function PeriodBox({ icon, label, start, end }: { icon: string; label: string; start: string; end: string }) {
  return (
    <div style={{ flex: 1, minWidth: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 'var(--radius-md, 12px)', padding: '9px 11px', display: 'flex', flexDirection: 'column', gap: 5 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={icon} alt="" width={16} height={16} style={{ display: 'block', flexShrink: 0 }} />
        <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{label}</span>
      </div>
      <div style={{ display: 'flex', gap: 6, fontSize: 12 }}>
        <span style={{ color: 'var(--tx-faint)', flexShrink: 0 }}>開始</span>
        <span style={{ color: 'var(--tx)', fontWeight: 600 }}>{start}</span>
      </div>
      <div style={{ display: 'flex', gap: 6, fontSize: 12 }}>
        <span style={{ color: 'var(--tx-faint)', flexShrink: 0 }}>結束</span>
        <span style={{ color: 'var(--tx)', fontWeight: 600 }}>{end}</span>
      </div>
    </div>
  )
}

const ctaLink: React.CSSProperties = {
  background: 'none', border: 'none', padding: 0, cursor: 'pointer',
  color: 'var(--fug)', fontWeight: 700, fontSize: 13.5,
}
