'use client'

import { useEffect, useState } from 'react'
import RacesScreen from './RacesScreen'
import RaceRankingScreen from './RaceRankingScreen'
import RegistrationScreen from './RegistrationScreen'
import ProfileScreen from './ProfileScreen'
import GoogleAuthProvider from './GoogleAuthProvider'
import type { Race } from '@/lib/api'

export default function PhoneShell() {
  const [isMobile, setIsMobile] = useState(false)
  const [rankingRace, setRankingRace] = useState<Race | null>(null)
  const [registerRace, setRegisterRace] = useState<Race | null>(null)
  const [showProfile, setShowProfile] = useState(false)

  useEffect(() => {
    setIsMobile(window.innerWidth <= 430)
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
        {showProfile ? (
          <ProfileScreen onBack={() => setShowProfile(false)} />
        ) : registerRace ? (
          <RegistrationScreen race={registerRace} onBack={() => setRegisterRace(null)} />
        ) : rankingRace ? (
          <RaceRankingScreen race={rankingRace} onBack={() => setRankingRace(null)} />
        ) : (
          <RacesScreen
            onOpenRanking={setRankingRace}
            onRegister={setRegisterRace}
            onOpenProfile={() => setShowProfile(true)}
          />
        )}
      </div>
    </div>
    </GoogleAuthProvider>
  )
}
