'use client'

import { useEffect, useState } from 'react'
import RacesScreen from './RacesScreen'
import RaceRankingScreen from './RaceRankingScreen'
import RegistrationScreen from './RegistrationScreen'
import ProfileScreen from './ProfileScreen'
import GoogleAuthProvider from './GoogleAuthProvider'
import { validateSession } from '@/lib/userAuth'
import type { Race } from '@/lib/api'

export default function PhoneShell() {
  const [isMobile, setIsMobile] = useState(false)
  const [rankingRace, setRankingRace] = useState<Race | null>(null)
  const [registerRace, setRegisterRace] = useState<Race | null>(null)
  const [showProfile, setShowProfile] = useState(false)
  const [payRace, setPayRace] = useState<Race | null>(null)

  useEffect(() => {
    setIsMobile(window.innerWidth <= 430)
    // 開啟 app 即驗證/換發 token：避免「顯示已登入但實際過期」的不一致
    validateSession()
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
        overflow: 'hidden',
      }}>
        {/* 賽事列表 / 排行榜 / 報名 / 個人資訊 — 串接 Go API 真實資料 */}
        {showProfile || payRace ? (
          <ProfileScreen
            focusRaceID={payRace?.id}
            onBack={() => { setShowProfile(false); setPayRace(null) }}
          />
        ) : registerRace ? (
          <RegistrationScreen race={registerRace} onBack={() => setRegisterRace(null)} />
        ) : rankingRace ? (
          <RaceRankingScreen race={rankingRace} onBack={() => setRankingRace(null)} />
        ) : (
          <RacesScreen
            onOpenRanking={setRankingRace}
            onRegister={setRegisterRace}
            onPay={setPayRace}
            onOpenProfile={() => setShowProfile(true)}
          />
        )}
      </div>
    </div>
    </GoogleAuthProvider>
  )
}
