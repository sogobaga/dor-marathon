'use client'

import { useEffect, useState } from 'react'
import RacesScreen from './RacesScreen'
import RaceRankingScreen from './RaceRankingScreen'
import type { Race } from '@/lib/api'

export default function PhoneShell() {
  const [isMobile, setIsMobile] = useState(false)
  const [rankingRace, setRankingRace] = useState<Race | null>(null)

  useEffect(() => {
    setIsMobile(window.innerWidth <= 430)
  }, [])

  return (
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
        {/* 賽事列表 / 排行榜 — 串接 Go API 真實資料 */}
        {rankingRace ? (
          <RaceRankingScreen race={rankingRace} onBack={() => setRankingRace(null)} />
        ) : (
          <RacesScreen onOpenRanking={setRankingRace} />
        )}
      </div>
    </div>
  )
}
