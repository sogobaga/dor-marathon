'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { rewardsApi, type UserReward } from '@/lib/api'
import { getUserToken, useUser, clearUserSession, withUserAuth } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import MemberPanel, { GuestHero, entryBtn } from './MemberPanel'
import UpgradeVipModal from './UpgradeVipModal'
import BindCardModal from './BindCardModal'
import { useVipSubscribeFlow } from '@/lib/useVipSubscribeFlow'

// 活動獎勵系統 P4：未使用且 30 天內到期（未過期）筆數 — 邏輯比照 RewardsWalletScreen 的 sortRewards「即將到期」判斷
// （無期限 valid_until=null 不算快到期；已過期不算「快到期」）。
const REWARD_SOON_MS = 30 * 24 * 60 * 60 * 1000
function countRewardsSoon(rewards: UserReward[] | undefined): number {
  if (!rewards) return 0
  const now = Date.now()
  let n = 0
  for (const r of rewards) {
    if (r.used || !r.valid_until) continue
    const t = new Date(r.valid_until).getTime()
    if (isNaN(t)) continue
    const diff = t - now
    if (diff >= 0 && diff <= REWARD_SOON_MS) n++
  }
  return n
}

// 首頁（2026-09-03 改版）：COROS 式可拖曳活動列表面板拆出去變成獨立的「活動探索」全頁
// （見 components/ActivityExploreScreen.tsx），首頁本身改回一般直向捲動——會員面板（含函式按鈕列，
// 使用者指示要出現在首頁）在上、置底「開始跑步」CTA 不變。races 的 SWR/篩選/RaceCard 等全部搬過去，
// 首頁不再需要賽事清單本身（無其他地方依賴這份 SWR 資料——見交付說明的決策記錄）。
export default function RacesScreen({
  onOpenProfile,
  onOpenActivityExplore,
  onOpenPersonalTasks,
  onOpenTraining,
  onOpenExplore,
  onOpenGallery,
  onOpenTitle,
  onOpenAchievement,
  onOpenPerks,
  onOpenMonopoly,
  onOpenRewards,
  onOpenHeroes,
  onOpenRunMeet,
}: {
  onOpenProfile?: () => void
  onOpenActivityExplore?: () => void
  onOpenPersonalTasks?: () => void
  onOpenTraining?: () => void
  onOpenExplore?: () => void
  onOpenGallery?: () => void
  onOpenTitle?: () => void
  onOpenAchievement?: () => void
  onOpenPerks?: () => void
  onOpenMonopoly?: () => void
  onOpenRewards?: () => void
  onOpenHeroes?: () => void
  onOpenRunMeet?: () => void
}) {
  const user = useUser() // 登入狀態變動時重新渲染
  const token = getUserToken() || undefined
  // 活動獎勵 P4：共用 RewardsWalletScreen 同一個 SWR key（['profile-rewards']），不多打一次 API；
  // 首屏不阻擋——只在資料就緒且算出 count>0 時才渲染提醒，載入中不顯示任何佔位。
  const { data: rewardsData } = useSWR(token ? ['profile-rewards'] : null, () => withUserAuth((t) => rewardsApi.list(t)))
  const rewardsSoonCount = countRewardsSoon(rewardsData?.rewards)
  const { dash } = useDashboard()
  const [showUpgrade, setShowUpgrade] = useState(false)
  const vipFlow = useVipSubscribeFlow() // VIP 訂閱 Phase E：Subscribe → BindCardModal（比照 ProfileScreen 接線）

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      {/* Header（精簡品牌列；VIP升級／登出置最右上角，一如改版前） */}
      <header style={{ padding: 'var(--app-top) max(22px, env(safe-area-inset-right, 0px)) 0 max(22px, env(safe-area-inset-left, 0px))', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
        <div style={{ fontSize: 13.5, letterSpacing: '.18em', color: 'var(--fug)', fontWeight: 800 }}>
          DOR · 城市探索
        </div>
        {user && (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
            {dash && !dash.is_vip && (
              <button
                onClick={() => setShowUpgrade(true)}
                style={{ background: 'var(--gold)', color: '#fff', border: 'none', borderRadius: 8, padding: '5px 11px', cursor: 'pointer', fontSize: 12, fontWeight: 800 }}
              >✦ 升級VIP</button>
            )}
            <button
              onClick={() => clearUserSession()}
              style={{ background: 'rgba(255,255,255,.05)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '5px 11px', cursor: 'pointer', fontSize: 12 }}
            >登出</button>
          </div>
        )}
      </header>

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
          onSuccess={() => { vipFlow.handleBindSuccess(); setShowUpgrade(false) }}
        />
      )}

      {/* 活動獎勵 P4：未使用且 30 天內到期＞0 才顯示（載入中不佔位）；點擊直接開活動獎勵錢包頁，醒目但不擋操作（僅佔一行、不遮蓋列表/CTA） */}
      {rewardsSoonCount > 0 && (
        <div style={{ padding: '10px 18px 0', flexShrink: 0 }}>
          <button onClick={() => onOpenRewards?.()} style={rewardsReminderBtn}>
            🎁 您有活動獎勵快到期。 ›
          </button>
        </div>
      )}

      {/* 主要內容：一般直向捲動（不再是拖曳面板）。登入＝會員面板＋函式按鈕列；訪客＝品牌 Hero＋單一「活動探索」鈕。 */}
      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: user ? '4px 18px calc(20px + var(--cta-safe, 0px))' : '0 0 calc(20px + var(--cta-safe, 0px))' }}>
        {user ? (
          <MemberPanel
            onOpenProfile={onOpenProfile}
            onOpenActivityExplore={onOpenActivityExplore}
            onOpenPersonalTasks={onOpenPersonalTasks}
            onOpenTraining={onOpenTraining}
            onOpenExplore={onOpenExplore}
            onOpenGallery={onOpenGallery}
            onOpenTitle={onOpenTitle}
            onOpenAchievement={onOpenAchievement}
            onOpenPerks={onOpenPerks}
            onOpenMonopoly={onOpenMonopoly}
            onOpenRewards={onOpenRewards}
            onOpenHeroes={onOpenHeroes}
            onOpenRunMeet={onOpenRunMeet}
            showEntries
          />
        ) : (
          <>
            <GuestHero />
            {/* 訪客沒有會員面板可放函式按鈕列，仍需能瀏覽賽事——單一滿版「活動探索」鈕，沿用 MemberPanel 匯出的 entryBtn 樣式（與登入版視覺一致） */}
            <div style={{ padding: '12px 18px 0' }}>
              {/* 訪客版文案改「現在舉辦中的活動」並置中（2026-09-03 使用者指示）：訪客看的是「有什麼活動可以參加」，
                  不是登入版那顆入口格；entryBtn 預設靠左（配合雙欄格線），這裡覆寫成置中 */}
              <button onClick={() => onOpenActivityExplore?.()} style={{ ...entryBtn, alignItems: 'center', textAlign: 'center' }}>
                <span style={{ fontSize: 14, fontWeight: 900, color: 'var(--tx)' }}>🏁 現在舉辦中的活動</span>
                <span style={{ fontSize: 10.5, fontWeight: 700, color: 'var(--tx-dim)' }}>賽事與活動 ›</span>
              </button>
            </div>
          </>
        )}
      </div>

      {/* 開始跑步（比照 GPS 跑步追蹤頁：置底整排綠色 CTA） */}
      <div style={{ padding: '14px 16px calc(20px + var(--cta-safe, 0px))', flexShrink: 0, borderTop: '1px solid var(--line)', background: 'var(--bg)' }}>
        <a href="/track" className="skin-btn-start" style={startBtn}>▶ 開始跑步</a>
      </div>
    </div>
  )
}

const startBtn: React.CSSProperties = {
  display: 'block', width: '100%', boxSizing: 'border-box', textAlign: 'center', textDecoration: 'none',
  background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none',
  borderRadius: 'var(--radius-btn, 12px)', padding: '15px 20px', fontSize: 16, cursor: 'pointer',
}
// 半透明金色淡底（非實心金底，故文字免強制白色，沿用 var(--tx) 於暗色/warm skin 皆可讀），比照 RewardsWalletScreen「即將到期」卡片的金框強調
const rewardsReminderBtn: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8, width: '100%', boxSizing: 'border-box',
  background: 'rgba(197,139,29,.14)', border: '1.5px solid var(--gold)', borderRadius: 'var(--radius-lg, 14px)',
  padding: '10px 14px', fontSize: 13, fontWeight: 800, color: 'var(--tx)', cursor: 'pointer', fontFamily: 'inherit',
}
