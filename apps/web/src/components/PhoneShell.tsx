'use client'

import { useEffect, useState } from 'react'
import RacesScreen from './RacesScreen'
import RegistrationScreen from './RegistrationScreen'
import ProfileScreen from './ProfileScreen'
import RaceDetailScreen from './RaceDetailScreen'
import GoogleAuthProvider from './GoogleAuthProvider'
import VersionBadge from './VersionBadge'
import MileageExpGate from './MileageExpGate'
import { validateSession } from '@/lib/userAuth'
import type { Race } from '@/lib/api'

export default function PhoneShell() {
  const [isMobile, setIsMobile] = useState(false)
  const [detailRace, setDetailRace] = useState<Race | null>(null)
  const [detailTab, setDetailTab] = useState<'brochure' | 'progress' | 'rank' | undefined>(undefined)
  const [registerRace, setRegisterRace] = useState<Race | null>(null)
  const [showProfile, setShowProfile] = useState(false)
  const [payRace, setPayRace] = useState<Race | null>(null)

  useEffect(() => {
    setIsMobile(window.innerWidth <= 430)
    // 開啟 app 即驗證/換發 token：避免「顯示已登入但實際過期」的不一致
    validateSession()
    // Strava 授權導回（?strava=...）→ 直接開個人資訊頁顯示結果
    if (new URLSearchParams(window.location.search).has('strava')) {
      setShowProfile(true)
    }
  }, [])

  return (
    <GoogleAuthProvider>
    <div className={isMobile ? 'w-full h-dvh' : 'phone-shell'}>
      {/* iOS notch */}
      {!isMobile && (
        <div style={{
          position: 'absolute', top: 12, left: '50%', transform: 'translateX(-50%)',
          width: 120, height: 34, background: '#000', borderRadius: 999, zIndex: 50,
        }} />
      )}

      {/* App content 區域 */}
      <div style={{
        position: 'absolute',
        inset: 0,
        paddingTop: isMobile ? 'env(safe-area-inset-top)' : 0,
        paddingBottom: isMobile ? 'env(safe-area-inset-bottom)' : 0,
        overflow: 'hidden',
      }}>
        {/* 賽事列表 / 賽事資訊(簡章·進度·排名) / 報名 / 個人資訊 — 串接 Go API 真實資料 */}
        {showProfile || payRace ? (
          <ProfileScreen
            focusRaceID={payRace?.id}
            onBack={() => { setShowProfile(false); setPayRace(null) }}
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
            onOpenBrochure={(r) => { setDetailTab(undefined); setDetailRace(r) }}
          />
        )}
      </div>

      {/* 版號（置底置中） */}
      <VersionBadge absolute />

      {/* 日常里程 EXP 結算彈窗（全域） */}
      <MileageExpGate />
    </div>
    </GoogleAuthProvider>
  )
}
