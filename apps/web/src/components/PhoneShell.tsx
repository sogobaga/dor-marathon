'use client'

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import dynamic from 'next/dynamic'
import { useRouter } from 'next/navigation'
import { useIsMobile } from '@/lib/useIsMobile'
import RacesScreen from './RacesScreen'
import ActivityExploreScreen from './ActivityExploreScreen'
import ProfileScreen from './ProfileScreen'
import TitleUnlockModal from './TitleUnlockModal'
import GoogleAuthProvider from './GoogleAuthProvider'
import VersionBadge from './VersionBadge'
import MileageExpGate from './MileageExpGate'
import DedupNoticeGate from './DedupNoticeGate'
import { validateSession, getUserToken, withUserAuth } from '@/lib/userAuth'
import { captureAcquisition } from '@/lib/acquisition'
import { useDashboard, refreshDashboard } from '@/lib/useDashboard'
import { useVipSubscribeFlow } from '@/lib/useVipSubscribeFlow'
import { pageview } from '@/lib/analytics'
import { profileApi, titleApi, racesApi, rpgBattleApi, type Race, type RpgBattleBootstrap } from '@/lib/api'
import { APP_VERSION } from '@/lib/version'
import { sampleFromBootstrap, configFromBootstrap, summonPoolFromBootstrap } from '@/lib/dorpg/fromApi'
import { battleAudio } from '@/lib/dorpg/audio'
import type { BattleReportStats } from './dorpg/BattleScreen'
import UpgradeVipModal from './UpgradeVipModal'
import BindCardModal from './BindCardModal'

// 非首屏必用的畫面 → code-split，減少首開 bundle（點了才載入對應 chunk）。
// RacesScreen（首屏必用）、ActivityExploreScreen（2026-09-03 改版：原本首頁內嵌的活動列表拆出去的
// 全頁，取代原本「幾乎必用」的地位，故沿用原本 RacesScreen 的靜態 import 待遇）
// 與 ProfileScreen（登入後高機率立即用）保留靜態 import。
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
const RunMeetScreen = dynamic(() => import('./RunMeetScreen'), { ssr: false })
const CharacterScreen = dynamic(() => import('./CharacterScreen'), { ssr: false })
// DORPG 戰鬥畫面：素材約 3MB、只從角色頁進入，故獨立 chunk 且不 SSR（元件自量尺寸）。
// P2 起中間多一層遭遇選單（EncounterPicker），同樣只從角色頁進入、同一顆 chunk 群組。
const BattleScreen = dynamic(() => import('./dorpg/BattleScreen'), { ssr: false })
const EncounterPicker = dynamic(() => import('./dorpg/EncounterPicker'), { ssr: false })
// DORPG P6：酒館（隊伍／傭兵腳本），同樣只從角色頁進入，不需要 SSR。
const TavernScreen = dynamic(() => import('./TavernScreen'), { ssr: false })
// DORPG P7：裝備（武器），同樣只從角色頁進入，不需要 SSR。
const EquipmentScreen = dynamic(() => import('./EquipmentScreen'), { ssr: false })

// openEventSlug：廣告落地頁 /event/{slug} 傳入，開頁即直接顯示該活動簡章（見 app/event/[slug]/EventLanding.tsx）。
// openShopId：合作商家專屬連結 /shop/{id} 傳入，開頁即直接顯示該商家詳細頁（見 app/shop/[id]/ShopLanding.tsx）。
export default function PhoneShell({ openEventSlug, openShopId }: { openEventSlug?: string; openShopId?: string } = {}) {
  const isMobile = useIsMobile()
  const router = useRouter()
  const [detailRace, setDetailRace] = useState<Race | null>(null)
  const [detailTab, setDetailTab] = useState<'brochure' | 'progress' | 'rank' | undefined>(undefined)
  const [registerRace, setRegisterRace] = useState<Race | null>(null)
  // 活動探索（2026-09-03 改版：原首頁內嵌的可拖曳活動列表拆成獨立全頁，見 ActivityExploreScreen）
  const [showActivityExplore, setShowActivityExplore] = useState(false)
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
  // 團練邀請（見 components/RunMeetScreen）：?runmeet={id} 深連結可直接開到某個團練詳情
  const [showRunMeet, setShowRunMeet] = useState(false)
  const [runMeetInitialId, setRunMeetInitialId] = useState<string | undefined>(undefined)
  // 遊戲化角色數值（第 21 套）：入口只在 dash.rpg_entry==='shown' 時出現，見 MemberPanel
  const [showCharacter, setShowCharacter] = useState(false)
  // DORPG P6：酒館（隊伍／傭兵腳本）——只能從角色頁的新按鈕開啟，跟角色頁互斥顯示（見下方渲染
  // 三元鏈；onOpenTavern／TavernScreen.onBack 各自切換這兩個布林，不會同時為 true）。
  const [showTavern, setShowTavern] = useState(false)
  // DORPG P7：裝備（武器）——同樣只能從角色頁的新按鈕開啟，跟角色頁互斥顯示（同上 showTavern
  // 的既有慣例；onOpenEquipment／EquipmentScreen.onBack 各自切換這兩個布林，不會同時為 true）。
  const [showEquipment, setShowEquipment] = useState(false)
  // DORPG 戰鬥畫面（第 21 套 P2）：只能從角色頁的「進入戰鬥」開啟，疊在角色頁之上（渲染鏈排在
  // showCharacter 前）。P2 起中間多一層遭遇選單：null→未開；{mode:'picker'}→選單；
  // {mode:'battle',code,nonce}→戰鬥中。nonce 只在「再戰一場」遞增，用來強制重建 BattleScreen
  // （key={nonce}）卻不重新打 bootstrap——bootstrap 結果快取在下面的 battleBootstrapCache
  // （契約 §4：「再戰一場」不得重新 fetch，300ms 內要能回到可操作）。
  const [battleView, setBattleView] = useState<null | { mode: 'picker' } | { mode: 'battle'; code: string; nonce: number }>(null)
  const battleBootstrapCache = useRef<Map<string, RpgBattleBootstrap>>(new Map())
  // 2026-09-14 SCREENS 接線：BGM 要跨越「選單→戰鬥→再戰一場→換一場」連續播放，只有真的離開整個
  // 戰鬥流程才停。不在進入時主動播放（自動播放政策），播放交給 EncounterPicker／BattleScreen 自己
  // 的手勢 handler。
  //
  // 2026-09-14 修復 E：battleView !== null 只代表「有意進入戰鬥流程」，不代表戰鬥畫面此刻真的被
  // 渲染出來——下面 return 的渲染三元鏈裡，battleView 分支之前還排著 9 個全螢幕布林
  // （showGallery/showTitle/showAchievement/showTraining/showPerks/showRewards/showMonopoly/
  // showHeroes/showRunMeet），任一個為 true 都會蓋過戰鬥畫面。若只拿 battleView !== null 當「該不
  // 該播戰鬥音樂」的依據，未來任何新入口在戰鬥中把其中一個布林設成 true，戰鬥畫面會被靜默取代、
  // 但 battleView 仍非 null，音樂就永遠停不下來。改用「戰鬥畫面此刻是否真的被渲染」這個具名布林
  // 當 effect 依賴（不改動三元鏈本身的順序，只是把同一份判斷抽出來命名）；battleView 內的
  // code/nonce 變動（換一場／再戰一場）不影響這個布林的值，effect 依然不會重新 setup/cleanup。
  const battleScreenRendered =
    battleView !== null &&
    !showGallery &&
    !showTitle &&
    !showAchievement &&
    !showTraining &&
    !showPerks &&
    !showRewards &&
    !showMonopoly &&
    !showHeroes &&
    !showRunMeet
  useEffect(() => {
    if (!battleScreenRendered) return
    return () => {
      battleAudio.stopBgm()
    }
  }, [battleScreenRendered])
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
    // Terra 手錶連接導回（?terra=connected|failed|error&provider=...，見 integration/terra.go Callback：
    // Terra widget 只能導回固定網址，所以一律落在首頁）→ 開個人資訊頁「運動數據」分頁顯示結果；
    // 參數由 ProfileScreen 讀完自己清。
    if (params.has('terra')) {
      setShowProfile(true)
      setProfileInitialTab('sports')
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
    // 團練邀請深連結（?runmeet={id}）：分享文案帶的連結，開頁即進該團練詳情。
    // 清掉參數避免重整重播；未過入口 gate 的帳號進去只會拿到 403（後端 requireEntry），不會外洩內容。
    const runMeetParam = params.get('runmeet')
    if (runMeetParam) {
      setShowRunMeet(true)
      // 'list' 是「只開團練邀請頁、不進任何詳情」的哨兵值（團練被刪除後的倒數導頁用，
      // 見 app/m/[id]/DeletedRedirect.tsx）——導回首頁會讓人不知道自己在哪，
      // 回到團練列表才接得上「那個團沒了，看看別的」。其餘值一律當團練 id 開詳情。
      if (runMeetParam !== 'list') setRunMeetInitialId(runMeetParam)
      window.history.replaceState({}, '', '/')
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
    else if (showRunMeet) { path = '/run-meets'; title = '團練邀請' }
    else if (battleView?.mode === 'battle') { path = '/battle'; title = '戰鬥' }
    else if (battleView?.mode === 'picker') { path = '/battle/picker'; title = '戰鬥選單' }
    else if (showCharacter) { path = '/character'; title = '角色' }
    else if (showEquipment) { path = '/equipment'; title = '裝備' }
    else if (showExplore) { path = '/explore'; title = '城市探索' }
    else if (showPersonalTasks) { path = '/personal-tasks'; title = '個人任務' }
    else if (showProfile || payRace) { path = '/profile'; title = '會員管理' }
    else if (registerRace) { path = `/register/${registerRace.slug}`; title = `報名 - ${registerRace.title}` }
    else if (detailRace) { path = `/race/${detailRace.slug}`; title = detailRace.title }
    // 活動探索：與畫面渲染鏈同一順序評估（見下方 JSX），registerRace/detailRace 蓋在它上面時優先算那兩個
    else if (showActivityExplore) { path = '/activities'; title = '活動探索' }
    pageview(path, title)
  }, [showGallery, showTitle, showAchievement, showTraining, showPerks, showRewards, showMonopoly, showHeroes, showRunMeet, battleView, showCharacter, showEquipment, showExplore, showPersonalTasks, showProfile, payRace, registerRace, detailRace, showActivityExplore])

  return (
    <GoogleAuthProvider>
    <div id="app-shell" className={isMobile ? 'w-full app-h' : 'phone-shell'} style={{ position: 'relative', overflow: 'hidden' }}>
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
        ) : showRunMeet ? (
          <RunMeetScreen onBack={() => { setShowRunMeet(false); setRunMeetInitialId(undefined) }} initialMeetId={runMeetInitialId} />
        ) : battleView && battleView.mode === 'picker' ? (
          <EncounterPicker
            onBack={() => { setBattleView(null); setShowCharacter(true) }}
            onOpenCharacter={() => { setBattleView(null); setShowCharacter(true) }}
            onPick={(code) => setBattleView({ mode: 'battle', code, nonce: 0 })}
          />
        ) : battleView && battleView.mode === 'battle' ? (
          // key={battleView.code}：換到不同遭遇（「換一場」在選單挑了別的卡）才整個重掛、重新打
          // bootstrap；同一個 code 只有 nonce 變動（「再戰一場」）不會重掛這層，bootstrap 快取才留得住。
          <DorpgBattleFlow
            key={battleView.code}
            code={battleView.code}
            nonce={battleView.nonce}
            cache={battleBootstrapCache.current}
            onExit={() => { setBattleView(null); setShowCharacter(true) }}
            onSwitch={() => setBattleView({ mode: 'picker' })}
            onRestart={() => setBattleView((v) => (v && v.mode === 'battle' ? { ...v, nonce: v.nonce + 1 } : v))}
          />
        ) : showCharacter ? (
          <CharacterScreen
            onBack={() => setShowCharacter(false)}
            onOpenBattle={() => {
              // 從角色頁進戰鬥選單前清掉 bootstrap 快取：玩家很可能剛在這頁配點，快取住的舊角色
              // 數值（atk/hp 等）會讓下一場戰鬥用到配點前的數字——寧可讓「換一場/重進選單」多打一次
              // API，也不要讓「剛配完點」變成沒生效的錯覺。同一次選單內來回「再戰一場」不受影響
              // （nonce 遞增不經過這裡，也不會重新叫 onOpenBattle）。
              battleBootstrapCache.current.clear()
              setBattleView({ mode: 'picker' })
            }}
            onOpenTavern={() => { setShowCharacter(false); setShowTavern(true) }}
            onOpenEquipment={() => { setShowCharacter(false); setShowEquipment(true) }}
          />
        ) : showTavern ? (
          <TavernScreen onBack={() => { setShowTavern(false); setShowCharacter(true) }} />
        ) : showEquipment ? (
          <EquipmentScreen onBack={() => { setShowEquipment(false); setShowCharacter(true) }} />
        ) : showExplore ? (
          <ExploreScreen onBack={() => setShowExplore(false)} onOpenTrack={(bossId) => { router.push(bossId ? '/track?focus=' + encodeURIComponent(bossId) : '/track') }} />
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
            onOpenRunMeet={() => setShowRunMeet(true)}
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
        ) : showActivityExplore ? (
          // registerRace/detailRace 在鏈中排它前面：從活動探索開賽事詳情/報名時 showActivityExplore
          // 仍保持 true（不清），返回時（上面兩支 onBack 只清各自的 state）自然落回這支分支而非首頁。
          <ActivityExploreScreen
            onBack={() => setShowActivityExplore(false)}
            onOpenRanking={(r) => { setDetailTab('rank'); setDetailRace(r) }}
            onRegister={setRegisterRace}
            onPay={setPayRace}
            onOpenBrochure={(r) => { setDetailTab(undefined); setDetailRace(r) }}
          />
        ) : (
          <RacesScreen
            onOpenProfile={() => setShowProfile(true)}
            onOpenActivityExplore={() => setShowActivityExplore(true)}
            onOpenPersonalTasks={() => setShowPersonalTasks(true)}
            onOpenTraining={() => setShowTraining(true)}
            onOpenExplore={() => setShowExplore(true)}
            onOpenGallery={() => setShowGallery(true)}
            onOpenTitle={() => setShowTitle(true)}
            onOpenAchievement={() => setShowAchievement(true)}
            onOpenPerks={() => setShowPerks(true)}
            onOpenMonopoly={() => setShowMonopoly(true)}
            onOpenRewards={() => setShowRewards(true)}
            onOpenHeroes={() => setShowHeroes(true)}
            onOpenRunMeet={() => setShowRunMeet(true)}
            onOpenRpg={() => setShowCharacter(true)}
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

// ---------------------------------------------------------------------------
// DORPG P2：遭遇選單→戰鬥中間的 bootstrap 載入層。獨立成元件（而不是寫在 PhoneShell 本體裡）單純是
// 為了讓「同一 code 換 nonce 不重掛」這件事乾淨——靠 PhoneShell 用 key={battleView.code} 掛/卸載這整層，
// nonce 只往下傳給 BattleScreen 當 key，兩層 key 的職責分得很清楚：外層 key 控制「要不要重新打
// bootstrap」，內層 key 控制「要不要重建戰鬥引擎」。
// ---------------------------------------------------------------------------
function DorpgBattleFlow({
  code,
  nonce,
  cache,
  onExit,
  onSwitch,
  onRestart,
}: {
  code: string
  nonce: number
  cache: Map<string, RpgBattleBootstrap>
  onExit: () => void
  onSwitch: () => void
  onRestart: () => void
}) {
  const [bootstrap, setBootstrap] = useState<RpgBattleBootstrap | null>(() => cache.get(code) ?? null)
  const [loading, setLoading] = useState(() => !cache.has(code))
  const [error, setError] = useState('')

  useEffect(() => {
    const cached = cache.get(code)
    if (cached) {
      setBootstrap(cached)
      setLoading(false)
      return
    }
    let alive = true
    setLoading(true)
    setError('')
    const token = getUserToken()
    if (!token) {
      setError('尚未登入，無法進入戰鬥')
      setLoading(false)
      return
    }
    withUserAuth((t) => rpgBattleApi.bootstrap(t, code))
      .then((r) => {
        if (!alive) return
        cache.set(code, r) // 契約 §4：只有這裡（真的打了 API）才寫入快取，「再戰一場」永遠不會走到這條路
        setBootstrap(r)
      })
      .catch((e: any) => {
        if (!alive) return
        // 401/403：入口理論上已 gate 過，這裡只是保險；503＝migration 176 尚未套用，訊息由後端帶（e.message）。
        setError(e?.status === 401 || e?.status === 403 ? '尚未登入或沒有權限使用戰鬥功能' : e?.message || '載入失敗，請稍後再試')
      })
      .finally(() => {
        if (alive) setLoading(false)
      })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [code])

  // 遙測回報：只在真的有 bootstrap 資料時才可能觸發（BattleScreen 還沒渲染前 onReport 不會被呼叫），
  // player_power 是契約 §3.2 PlayerPower 公式（round(Atk+Def+HPMax/10)）的前端鏡像——純遙測不要求
  // 跟後端逐位元一致，只是給後台儀表板一個大致戰力參考。失敗只 console.warn，不影響任何戰鬥流程。
  const handleReport = useCallback(
    (stats: BattleReportStats) => {
      const token = getUserToken()
      const player = bootstrap?.sample.party[0]
      if (!token || !player) return
      const atk = player.stats?.atk ?? 0
      const def = player.stats?.def ?? 0
      const hpMax = player.stats?.hpMax ?? player.hpMax ?? 0
      withUserAuth((t) =>
        rpgBattleApi.report(t, {
          encounter_code: stats.encounterCode,
          outcome: stats.outcome,
          duration_ms: stats.durationMs,
          damage_dealt: stats.damageDealt,
          damage_taken: stats.damageTaken,
          enemies_defeated: stats.enemiesDefeated,
          attacks: stats.attacks,
          charged_attacks: stats.chargedAttacks,
          skills_used: stats.skillsUsed,
          items_used: stats.itemsUsed,
          guard_ms: stats.guardMs,
          player_level: player.level,
          player_power: Math.round(atk + def + hpMax / 10),
          client_version: APP_VERSION,
        }),
      ).catch((e) => console.warn('[dorpg] battle report failed', e))
    },
    [bootstrap],
  )

  // 2026-09-14 P2 修正第1輪 審查2（PLAUSIBLE）：原本直接寫在 JSX 裡的 sampleFromBootstrap(...)／
  // configFromBootstrap(...) 每次 render 都會回傳全新物件——bootstrap 不變時用 useMemo 鎖住 identity，
  // 避免 PhoneShell 因任何無關原因重繪（全站 WS data_updated／SWR revalidate／VIP 輪詢…都會讓
  // PhoneShell 重繪，進而重繪這層）時，新的 identity 一路往下傳到 BattleStage 的 scene prop，擊穿
  // BattleScreen.tsx 內 MemoBattleStage 的 React.memo 保護。bootstrap 本身在同一個 DorpgBattleFlow
  // 實例存活期間只會被 setBootstrap 設定一次（見上面 effect：cache 命中或 fetch 成功各設一次），
  // 「再戰一場」的 nonce 遞增不經過這裡，故依賴 [bootstrap] 已足夠、不必依賴 nonce。
  const sample = useMemo(() => (bootstrap ? sampleFromBootstrap(bootstrap.sample) : undefined), [bootstrap])
  const config = useMemo(() => (bootstrap ? configFromBootstrap(bootstrap.config) : undefined), [bootstrap])
  // DORPG P11（INTEGRATOR 2026-09-20，同上：見 sample/config 為何要 useMemo 鎖 identity 的既有
  // 說明）：bootstrap.summonPool 是召喚池原始 wire 資料，要過 summonPoolFromBootstrap() 驗證/
  // 防禦（同 sample 過 sampleFromBootstrap()），未鎖 identity 會讓 BattleScreen→useBattle 每次
  // 無關重繪都拿到新陣列 identity，雖然 summonPool 目前只在 useBattle 初始化時讀一次（不影響
  // 戰鬥中途行為），仍比照既有慣例鎖住，避免之後有人在其他地方依賴這個 prop 的 identity 穩定性。
  const summonPool = useMemo(() => (bootstrap ? summonPoolFromBootstrap(bootstrap.summonPool) : undefined), [bootstrap])

  if (loading) {
    return (
      <div data-skin="default" style={{ position: 'absolute', inset: 0, background: '#001523', display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#B8CCD6', fontSize: 13 }}>
        載入中…
      </div>
    )
  }
  if (error || !bootstrap) {
    return (
      <div data-skin="default" style={{ position: 'absolute', inset: 0, background: '#001523', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 14, padding: 24, color: '#FFF9EA', textAlign: 'center' }}>
        <div style={{ fontSize: 13, lineHeight: 1.7, color: '#B8CCD6' }}>{error || '載入失敗'}</div>
        <button onClick={onSwitch} style={{ background: '#F3BD62', color: '#fff', border: 'none', borderRadius: 10, padding: '9px 22px', fontSize: 13.5, fontWeight: 800, fontFamily: 'inherit', cursor: 'pointer' }}>返回選單</button>
      </div>
    )
  }

  return (
    <BattleScreen
      key={nonce}
      sample={sample}
      config={config}
      // 2026-09-19 修復：bootstrap.autoBattle 一直存在（WIRE §戰鬥 bootstrap 頂層欄位），但這裡
      // 從未傳給 BattleScreen，導致玩家上次開啟的自動戰鬥每次進戰鬥都被重置成關閉（見
      // BattleScreen.tsx autoBattle prop／useBattle.ts 的修復註解）。
      autoBattle={bootstrap.autoBattle ?? false}
      // DORPG P11（INTEGRATOR 2026-09-20 補上，已知整合缺口）：bootstrap 頂層的召喚池與強度
      // 九級標籤欄位原本從未轉給 BattleScreen，召喚機制在真實對戰裡永遠不會觸發（見
      // BattleScreen.tsx／useBattle.ts 對應欄位的修復註解）。
      summonPool={summonPool}
      scalingMode={bootstrap.scalingMode}
      levelMode={bootstrap.levelMode}
      monsterLevel={bootstrap.monsterLevel}
      encounter={{ code, title: bootstrap.encounter.title }}
      onBack={onExit}
      onRestart={onRestart}
      onNext={onSwitch}
      onReport={handleReport}
    />
  )
}
