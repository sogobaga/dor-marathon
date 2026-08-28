'use client'

import { useEffect, useRef, useState } from 'react'
import dynamic from 'next/dynamic'
import { useIsMobile } from '@/lib/useIsMobile'
import RacesScreen from './RacesScreen'
import ProfileScreen from './ProfileScreen'
import TitleUnlockModal from './TitleUnlockModal'
import GoogleAuthProvider from './GoogleAuthProvider'
import VersionBadge from './VersionBadge'
import MileageExpGate from './MileageExpGate'
import DedupNoticeGate from './DedupNoticeGate'
import { validateSession, getUserToken } from '@/lib/userAuth'
import { captureAcquisition } from '@/lib/acquisition'
import { useDashboard, refreshDashboard } from '@/lib/useDashboard'
import { useVipSubscribeFlow } from '@/lib/useVipSubscribeFlow'
import { pageview } from '@/lib/analytics'
import { profileApi, titleApi, racesApi, type Race } from '@/lib/api'
import UpgradeVipModal from './UpgradeVipModal'
import BindCardModal from './BindCardModal'

// 非首屏必用的畫面 → code-split，減少首開 bundle（點了才載入對應 chunk）。
// RacesScreen（首屏必用）與 ProfileScreen（登入後高機率立即用）保留靜態 import。
const RegistrationScreen = dynamic(() => import('./RegistrationScreen'), { ssr: false })
const PersonalTasksScreen = dynamic(() => import('./PersonalTasksScreen'), { ssr: false })
const ExploreScreen = dynamic(() => import('./ExploreScreen'), { ssr: false })
const CardGalleryScreen = dynamic(() => import('./CardGalleryScreen'), { ssr: false })
const TitleScreen = dynamic(() => import('./TitleScreen'), { ssr: false })
const AchievementScreen = dynamic(() => import('./AchievementScreen'), { ssr: false })
const TrainingScreen = dynamic(() => import('./TrainingScreen'), { ssr: false })
const PartnerPerksScreen = dynamic(() => import('./PartnerPerksScreen'), { ssr: false })
const RewardsWalletScreen = dynamic(() => import('./RewardsWalletScreen'), { ssr: false })
const HundredHeroesScreen = dynamic(() => import('./HundredHeroesScreen'), { ssr: false })
const MonopolyScreen = dynamic(() => import('./MonopolyScreen'), {
  ssr: false,
  // 環台大富翁 chunk 較大（盤面等圖已轉 WebP，但仍需下載時間）→ 加極簡置中骨架，避免下載期間全空白。
  loading: () => (
    <div style={{ height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--bg)', color: 'var(--tx-dim)', fontSize: 13, fontWeight: 700 }}>
      載入中…
    </div>
  ),
})
const RaceDetailScreen = dynamic(() => import('./RaceDetailScreen'), { ssr: false })

// openEventSlug：廣告落地頁 /event/{slug} 傳入，開頁即直接顯示該活動簡章（見 app/event/[slug]/EventLanding.tsx）。
// openShopId：合作商家專屬連結 /shop/{id} 傳入，開頁即直接顯示該商家詳細頁（見 app/shop/[id]/ShopLanding.tsx）。
export default function PhoneShell({ openEventSlug, openShopId }: { openEventSlug?: string; openShopId?: string } = {}) {
  const isMobile = useIsMobile()
  const [detailRace, setDetailRace] = useState<Race | null>(null)
  const [detailTab, setDetailTab] = useState<'brochure' | 'progress' | 'rank' | undefined>(undefined)
  const [registerRace, setRegisterRace] = useState<Race | null>(null)
  const [showProfile, setShowProfile] = useState(false)
  const [profileInitialTab, setProfileInitialTab] = useState<'info' | 'sports' | 'records' | 'follows' | undefined>(undefined)
  const [showPersonalTasks, setShowPersonalTasks] = useState(false)
  const [showExplore, setShowExplore] = useState(false)
  const [showGallery, setShowGallery] = useState(false)
  const [showTitle, setShowTitle] = useState(false)
  const [showAchievement, setShowAchievement] = useState(false)
  const [showTraining, setShowTraining] = useState(false)
  const [showPerks, setShowPerks] = useState(false)
  const [perksInitialShop, setPerksInitialShop] = useState<string | undefined>(undefined)
  const [showMonopoly, setShowMonopoly] = useState(false)
  const [showRewards, setShowRewards] = useState(false)
  const [showHeroes, setShowHeroes] = useState(false)
  const [titlesModal, setTitlesModal] = useState<{ code: string; name: string; tier: number; category: string }[]>([])
  const titlesHandled = useRef(false)
  const [unlockCardId, setUnlockCardId] = useState<string | undefined>(undefined)
  const [payRace, setPayRace] = useState<Race | null>(null)
  const [trialModal, setTrialModal] = useState(false)
  const trialHandled = useRef(false)
  const vipFlow = useVipSubscribeFlow() // VIP 訂閱 Phase E：試用到期自動彈出的 UpgradeVipModal 也可直接訂閱（見 lib/useVipSubscribeFlow）
  // 綠界 3D 驗證完成後的導回結果（?vip_bind=success|fail，見 BindHandler.redirectBindResult）
  const [vipBindResult, setVipBindResult] = useState<'success' | 'fail' | null>(null)

  // 試用到期且尚未提示過 → 自動跳一次升級彈窗（標記已顯示，之後不再跳，改由「升級VIP」鈕）。
  // ⚠️ 必須「已登入(有 token)」才自動跳——未登入/未註冊者一進頁面就被強制跳付費彈窗很傷體驗，一律不跳。
  //（且未登入無法 markTrialNoticeShown → 否則每次進頁面都會重跳。）
  const { dash } = useDashboard()
  useEffect(() => {
    const t = getUserToken()
    if (t && dash?.show_trial_expiry_notice && !trialHandled.current) {
      trialHandled.current = true
      setTrialModal(true)
      profileApi.markTrialNoticeShown(t).catch(() => {})
    }
  }, [dash?.show_trial_expiry_notice])

  // 新解鎖稱號 → 華麗彈窗（dashboard 驅動、只跳一次；關閉時標記 seen）
  useEffect(() => {
    const nt = dash?.new_titles
    if (nt && nt.length && !titlesHandled.current) {
      titlesHandled.current = true
      setTitlesModal(nt)
    }
  }, [dash?.new_titles])

  // 開啟指定活動簡章（廣告落地頁 /event/{slug} 或 ?event= 深連結共用）。
  // 失敗（查無此活動/連線異常）靜默不動即可——不存在的 slug 由伺服器端落地頁擋掉，這裡只是保險。
  const openEventBrochure = (slug: string) => {
    // 帶 token：測試(testing)控制狀態的活動需靠使用者 email 驗白名單，不帶 token 後端會回 404（見後端 GetPublicDetail）。
    racesApi.detail(slug, getUserToken() || undefined).then((res) => {
      setDetailRace(res.race)
      setDetailTab('brochure')
    }).catch(() => {})
  }

  useEffect(() => {
    // 開啟 app 即驗證/換發 token：避免「顯示已登入但實際過期」的不一致
    validateSession()
    // 會員註冊來源歸因（first-touch，見 lib/acquisition.ts）：與下面 ?ref= 擷取並存，互不覆蓋。
    captureAcquisition()
    const params = new URLSearchParams(window.location.search)
    // 推廣連結（?ref=<code>）→ 記到 localStorage，留到登入/註冊成功時才帶給後端綁定。
    // 刻意不清 query、不清 localStorage：使用者可能先逛一逛才登入，要撐到那時候還在。
    const refCode = params.get('ref')
    if (refCode) {
      localStorage.setItem('dor:ref_code', refCode)
    }
    // Strava 授權導回（?strava=...）→ 直接開個人資訊頁顯示結果
    if (params.has('strava')) {
      setShowProfile(true)
    }
    // 綠界站內付 2.0 綁卡 3D 驗證完成導回（?vip_bind=success|fail，見 BindHandler.redirectBindResult）
    // → 顯示結果彈窗；成功時順便讓全站會員儀表板重抓一次（VIP 徽章/到期日即時更新）。清參數避免重整重播。
    const vipBind = params.get('vip_bind')
    if (vipBind === 'success' || vipBind === 'fail') {
      setVipBindResult(vipBind)
      if (vipBind === 'success') refreshDashboard()
      window.history.replaceState({}, '', '/')
    }
    // 深連結指定個人資訊頁分頁（?profile=sports，如：track 頁「有待上傳的 GPS，前往確認數據」）→ 開個人資訊頁並跳到指定分頁
    const profileParam = params.get('profile')
    if (profileParam) {
      setShowProfile(true)
      if (profileParam === 'sports') setProfileInitialTab('sports')
      window.history.replaceState({}, '', '/') // 清掉參數，避免重整重播
    }
    // 關主挑戰取卡導回（?unlock=<bossId>）→ 開卡片圖鑑並跳到該卡、播翻轉解鎖特效
    const unlock = params.get('unlock')
    if (unlock) {
      setShowGallery(true)
      setUnlockCardId(unlock)
      window.history.replaceState({}, '', '/') // 清掉參數，避免重整重播
    }
    // 活動簡章深連結：優先吃 openEventSlug prop（來自 /event/{slug} 路由，網址已經是漂亮的、不清參數）；
    // 否則吃 ?event= query（例如外部連結手動帶參數），清掉參數避免重整重播。
    if (openEventSlug) {
      openEventBrochure(openEventSlug)
    } else {
      const eventParam = params.get('event')
      if (eventParam) {
        openEventBrochure(eventParam)
        window.history.replaceState({}, '', '/') // 清掉參數，避免重整重播
      }
    }
    // 合作商家詳細頁深連結：優先吃 openShopId prop（來自 /shop/{id} 路由，網址已經是漂亮的、不清參數）；
    // 否則吃 ?shop= query（例如外部連結手動帶參數），清掉參數避免重整重播。做法比照上面的活動簡章深連結。
    if (openShopId) {
      setShowPerks(true)
      setPerksInitialShop(openShopId)
    } else {
      const shopParam = params.get('shop')
      if (shopParam) {
        setShowPerks(true)
        setPerksInitialShop(shopParam)
        window.history.replaceState({}, '', '/') // 清掉參數，避免重整重播
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // GA4：SPA 換畫面（狀態切換、非 URL 變動）也送一次 page_view。初始首頁由 initGA 的 config 送出，故略過首次。
  const firstView = useRef(true)
  useEffect(() => {
    if (firstView.current) { firstView.current = false; return }
    let path = '/', title = '首頁'
    if (showGallery) { path = '/gallery'; title = '卡片探索' }
    else if (showTitle) { path = '/titles'; title = '成就探索' }
    else if (showAchievement) { path = '/achievements'; title = '數據探索' }
    else if (showTraining) { path = '/training'; title = '自主訓練' }
    else if (showPerks) { path = '/perks'; title = '跑者充電站' }
    else if (showRewards) { path = '/rewards'; title = '活動獎勵' }
    else if (showMonopoly) { path = '/monopoly'; title = '環台大富翁' }
    else if (showHeroes) { path = '/heroes'; title = '百里英雄榜' }
    else if (showExplore) { path = '/explore'; title = '城市探索' }
    else if (showPersonalTasks) { path = '/personal-tasks'; title = '個人任務' }
    else if (showProfile || payRace) { path = '/profile'; title = '會員資訊' }
    else if (registerRace) { path = `/register/${registerRace.slug}`; title = `報名 - ${registerRace.title}` }
    else if (detailRace) { path = `/race/${detailRace.slug}`; title = detailRace.title }
    pageview(path, title)
  }, [showGallery, showTitle, showAchievement, showTraining, showPerks, showRewards, showMonopoly, showHeroes, showExplore, showPersonalTasks, showProfile, payRace, registerRace, detailRace])

  return (
    <GoogleAuthProvider>
    <div id="app-shell" className={isMobile ? 'w-full h-dvh' : 'phone-shell'} style={{ position: 'relative', overflow: 'hidden' }}>
      {/* 假動態島（僅桌面模擬框顯示；真手機由 CSS .fake-notch media query 隱藏） */}
      <div className="fake-notch" />

      {/* App content 區域 */}
      <div style={{
        position: 'absolute',
        inset: 0,
        paddingTop: isMobile ? 'env(safe-area-inset-top)' : 0,
        paddingBottom: isMobile ? 'env(safe-area-inset-bottom)' : 0,
        overflow: 'hidden',
      }}>
        {/* 賽事列表 / 賽事資訊(簡章·進度·排名) / 報名 / 個人資訊 / 個人任務 — 串接 Go API 真實資料 */}
        {showGallery ? (
          <CardGalleryScreen onBack={() => setShowGallery(false)} focusCardId={unlockCardId} />
        ) : showTitle ? (
          <TitleScreen onBack={() => setShowTitle(false)} />
        ) : showAchievement ? (
          <AchievementScreen onBack={() => setShowAchievement(false)} />
        ) : showTraining ? (
          <TrainingScreen onBack={() => setShowTraining(false)} />
        ) : showPerks ? (
          <PartnerPerksScreen onBack={() => setShowPerks(false)} initialShopId={perksInitialShop} />
        ) : showRewards ? (
          <RewardsWalletScreen onBack={() => setShowRewards(false)} />
        ) : showMonopoly ? (
          <MonopolyScreen onBack={() => setShowMonopoly(false)} />
        ) : showHeroes ? (
          <HundredHeroesScreen onBack={() => setShowHeroes(false)} />
        ) : showExplore ? (
          <ExploreScreen onBack={() => setShowExplore(false)} onOpenTrack={(bossId) => { window.location.href = bossId ? '/track?focus=' + encodeURIComponent(bossId) : '/track' }} />
        ) : showPersonalTasks ? (
          <PersonalTasksScreen onBack={() => setShowPersonalTasks(false)} />
        ) : showProfile || payRace ? (
          <ProfileScreen
            focusRaceID={payRace?.id}
            initialTab={profileInitialTab}
            onBack={() => { setShowProfile(false); setPayRace(null) }}
            onOpenPersonalTasks={() => setShowPersonalTasks(true)}
            onOpenExplore={() => setShowExplore(true)}
            onOpenGallery={() => setShowGallery(true)}
            onOpenTitle={() => setShowTitle(true)}
            onOpenAchievement={() => setShowAchievement(true)}
            onOpenTraining={() => setShowTraining(true)}
            onOpenPerks={() => setShowPerks(true)}
            onOpenMonopoly={() => setShowMonopoly(true)}
            onOpenRewards={() => setShowRewards(true)}
            onOpenHeroes={() => setShowHeroes(true)}
          />
        ) : registerRace ? (
          <RegistrationScreen race={registerRace} onBack={() => setRegisterRace(null)} />
        ) : detailRace ? (
          <RaceDetailScreen
            race={detailRace}
            initialTab={detailTab}
            onBack={() => setDetailRace(null)}
            onRegister={(r) => { setDetailRace(null); setRegisterRace(r) }}
          />
        ) : (
          <RacesScreen
            onOpenRanking={(r) => { setDetailTab('rank'); setDetailRace(r) }}
            onRegister={setRegisterRace}
            onPay={setPayRace}
            onOpenProfile={() => setShowProfile(true)}
            onOpenPersonalTasks={() => setShowPersonalTasks(true)}
            onOpenTraining={() => setShowTraining(true)}
            onOpenExplore={() => setShowExplore(true)}
            onOpenGallery={() => setShowGallery(true)}
            onOpenTitle={() => setShowTitle(true)}
            onOpenAchievement={() => setShowAchievement(true)}
            onOpenBrochure={(r) => { setDetailTab(undefined); setDetailRace(r) }}
            onOpenRewards={() => setShowRewards(true)}
          />
        )}
      </div>

      {/* 版號（置底置中） */}
      <VersionBadge absolute />

      {/* 日常里程 EXP 結算彈窗（全域） */}
      <MileageExpGate />
      {/* 跨來源（GPS/Strava）重複數據首次提示彈窗（全域） */}
      <DedupNoticeGate />
      {/* VIP 試用到期升級彈窗（只跳一次） */}
      {trialModal && (
        <UpgradeVipModal
          expired
          onClose={() => setTrialModal(false)}
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
          onSuccess={() => { vipFlow.handleBindSuccess(); setTrialModal(false) }}
        />
      )}
      {/* 綠界 3D 驗證完成導回結果彈窗（?vip_bind=success|fail） */}
      {vipBindResult && (
        <div data-skin="default" onClick={() => setVipBindResult(null)} style={{ position: 'fixed', inset: 0, zIndex: 3500, background: 'rgba(4,8,6,.88)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16 }}>
          <div onClick={(e) => e.stopPropagation()} style={{ width: '100%', maxWidth: 360, background: '#0b0e13', border: '1px solid var(--line-2)', borderRadius: 16, padding: '20px 18px', boxShadow: '0 16px 50px rgba(0,0,0,.7)', textAlign: 'center' }}>
            {vipBindResult === 'success' ? (
              <>
                <div style={{ fontSize: 36 }}>✓</div>
                <div style={{ fontSize: 17, fontWeight: 900, color: '#fff', marginTop: 8 }}>VIP 已生效</div>
                <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.7 }}>綁卡付款完成，VIP 權益已開通。</div>
              </>
            ) : (
              <>
                <div style={{ fontSize: 36 }}>✕</div>
                <div style={{ fontSize: 17, fontWeight: 900, color: '#fff', marginTop: 8 }}>綁卡付款未完成</div>
                <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.7 }}>3D 驗證未成功或已取消，可重新嘗試訂閱。</div>
              </>
            )}
            <button onClick={() => setVipBindResult(null)} style={{ marginTop: 16, width: '100%', background: 'var(--gold)', color: '#fff', fontWeight: 900, border: 'none', borderRadius: 12, padding: '11px', fontSize: 14, cursor: 'pointer', fontFamily: 'inherit' }}>我知道了</button>
          </div>
        </div>
      )}
      {/* 稱號解鎖彈窗（dashboard 驅動、只跳一次；關閉標記 seen） */}
      {titlesModal.length > 0 && (
        <TitleUnlockModal titles={titlesModal} onClose={() => {
          const codes = titlesModal.map((t) => t.code)
          setTitlesModal([])
          const tk = getUserToken()
          if (tk) titleApi.seen(tk, codes).catch(() => {})
        }} />
      )}
    </div>
    </GoogleAuthProvider>
  )
}
