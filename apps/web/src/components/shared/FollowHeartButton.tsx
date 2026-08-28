'use client'

import type { CSSProperties } from 'react'

// 共用「追蹤／已追蹤」愛心按鈕：取代原本各排行榜/榜單處的「＋追蹤」「追蹤中」文字按鈕，統一改為愛心圖示。
// - 未追蹤：空心愛心（描邊 var(--tx-dim)）
// - 已追蹤：實心愛心（暖粉紅 FOLLOW_HEART_COLOR）
// 點擊行為／樂觀更新／防連點等邏輯完全由呼叫端（各排行榜元件）負責，本元件只負責顯示與 hit area。
// 抽出自 HundredHeroesScreen / RaceRankingScreen / RaceDetailScreen / BossRankingPanel 的重複「追蹤鈕」樣式。

export const FOLLOW_HEART_COLOR = '#ff5b7f'

// 愛心 SVG path（viewBox 0 0 24 24），供元件內部與（若未來需要）其他地方共用。
export const HEART_PATH = 'M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z'

export function HeartIcon({ filled, size = 18 }: { filled: boolean; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path
        d={HEART_PATH}
        fill={filled ? FOLLOW_HEART_COLOR : 'none'}
        stroke={filled ? FOLLOW_HEART_COLOR : 'var(--tx-dim)'}
        strokeWidth={filled ? 0 : 1.8}
        strokeLinejoin="round"
      />
    </svg>
  )
}

export default function FollowHeartButton({
  following, onClick, size = 18, style,
}: {
  following: boolean
  onClick: () => void
  size?: number
  style?: CSSProperties
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={following ? '取消追蹤' : '追蹤'}
      aria-pressed={following}
      style={{
        flexShrink: 0, background: 'none', border: 'none', cursor: 'pointer',
        padding: 9, display: 'flex', alignItems: 'center', justifyContent: 'center',
        borderRadius: '50%', lineHeight: 0, WebkitTapHighlightColor: 'transparent',
        ...style,
      }}
    >
      <HeartIcon filled={following} size={size} />
    </button>
  )
}
