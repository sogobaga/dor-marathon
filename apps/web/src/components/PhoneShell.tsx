'use client'

import { useEffect, useRef, useState } from 'react'
import { useIsMobile } from '@/lib/useIsMobile'
import RacesScreen from './RacesScreen'
import RegistrationScreen from './RegistrationScreen'
import ProfileScreen from './ProfileScreen'
import PersonalTasksScreen from './PersonalTasksScreen'
import ExploreScreen from './ExploreScreen'
import CardGalleryScreen from './CardGalleryScreen'
import TitleScreen from './TitleScreen'
import AchievementScreen from './AchievementScreen'
import TitleUnlockModal from './TitleUnlockModal'
import RaceDetailScreen from './RaceDetailScreen'
import GoogleAuthProvider from './GoogleAuthProvider'
import VersionBadge from './VersionBadge'
import MileageExpGate from './MileageExpGate'
import DedupNoticeGate from './DedupNoticeGate'
import { validateSession, getUserToken } from '@/lib/userAuth'
import { useDashboard } from '@/lib/useDashboard'
import { pageview } from '@/lib/analytics'
import { profileApi, titleApi, type Race } from '@/lib/api'
import UpgradeVipModal from './UpgradeVipModal'

export default function PhoneShell() {
  const isMobile = useIsMobile()
  const [detailRace, setDetailRace] = useState<Race | null>(null)
  const [detailTab, setDetailTab] = useState<'brochure' | 'progress' | 'rank' | undefined>(undefined)
  const [registerRace, setRegisterRace] = useState<Race | null>(null)
  const [showProfile, setShowProfile] = useState(false)
  const [showPersonalTasks, setShowPersonalTasks] = useState(false)
  const [showExplore, setShowExplore] = useState(false)
  const [showGallery, setShowGallery] = useState(false)
  const [showTitle, setShowTitle] = useState(false)
  const [showAchievement, setShowAchievement] = useState(false)
  const [titlesModal, setTitlesModal] = useState<{ code: string; name: string; tier: number; category: string }[]>([])
  const titlesHandled = useRef(false)
  const [unlockCardId, setUnlockCardId] = useState<string | undefined>(undefined)
  const [payRace, setPayRace] = useState<Race | null>(null)
  const [trialModal, setTrialModal] = useState(false)
  const trialHandled = useRef(false)

  // 試用到期且尚未提示過 → 自動跳一次升級彈窗（標記已顯示，之後不再跳，改由「升級VIP」鈕）
  const { dash } = useDashboard()
  useEffect(() => {
    if (dash?.show_trial_expiry_notice && !trialHandled.current) {
      trialHandled.current = true
      setTrialModal(true)
      const t = getUserToken()
      if (t) profileApi.markTrialNoticeShown(t).catch(() => {})
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

  useEffect(() => {
    // 開啟 app 即驗證/換發 token：避免「顯示已登入但實際過期」的不一致
    validateSession()
    const params = new URLSearchParams(window.location.search)
    // Strava 授權導回（?strava=...）→ 直接開個人資訊頁顯示結果
    if (params.has('strava')) {
      setShowProfile(true)
    }
    // 關主挑戰取卡導回（?unlock=<bossId>）→ 開卡片圖鑑並跳到該卡、播翻轉解鎖特效
    const unlock = params.get('unlock')
    if (unlock) {
      setShowGallery(true)
      setUnlockCardId(unlock)
      window.history.replaceState({}, '', '/') // 清掉參數，避免重整重播
    }
  }, [])

  // GA4：SPA 換畫面（狀態切換、非 URL 變動）也送一次 page_view。初始首頁由 initGA 的 config 送出，故略過首次。
  const firstView = useRef(true)
  useEffect(() => {
    if (firstView.current) { firstView.current = false; return }
    let path = '/', title = '首頁'
    if (showGallery) { path = '/gallery'; title = '卡片探索' }
    else if (showTitle) { path = '/titles'; title = 'PB探索' }
    else if (showAchievement) { path = '/achievements'; title = '成就探索' }
    else if (showExplore) { path = '/explore'; title = '城市探索' }
    else if (showPersonalTasks) { path = '/personal-tasks'; title = '個人任務' }
    else if (showProfile || payRace) { path = '/profile'; title = '會員資訊' }
    else if (registerRace) { path = `/register/${registerRace.slug}`; title = `報名 - ${registerRace.title}` }
    else if (detailRace) { path = `/race/${detailRace.slug}`; title = detailRace.title }
    pageview(path, title)
  }, [showGallery, showTitle, showAchievement, showExplore, showPersonalTasks, showProfile, payRace, registerRace, detailRace])

  return (
    <GoogleAuthProvider>
    <div className={isMobile ? 'w-full h-dvh' : 'phone-shell'}>
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
        ) : showExplore ? (
          <ExploreScreen onBack={() => setShowExplore(false)} onOpenTrack={(bossId) => { window.location.href = bossId ? '/track?focus=' + encodeURIComponent(bossId) : '/track' }} />
        ) : showPersonalTasks ? (
          <PersonalTasksScreen onBack={() => setShowPersonalTasks(false)} />
        ) : showProfile || payRace ? (
          <ProfileScreen
            focusRaceID={payRace?.id}
            onBack={() => { setShowProfile(false); setPayRace(null) }}
            onOpenPersonalTasks={() => setShowPersonalTasks(true)}
            onOpenExplore={() => setShowExplore(true)}
            onOpenGallery={() => setShowGallery(true)}
            onOpenTitle={() => setShowTitle(true)}
            onOpenAchievement={() => setShowAchievement(true)}
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
            onOpenExplore={() => setShowExplore(true)}
            onOpenGallery={() => setShowGallery(true)}
            onOpenTitle={() => setShowTitle(true)}
            onOpenAchievement={() => setShowAchievement(true)}
            onOpenBrochure={(r) => { setDetailTab(undefined); setDetailRace(r) }}
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
      {trialModal && <UpgradeVipModal expired onClose={() => setTrialModal(false)} />}
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
