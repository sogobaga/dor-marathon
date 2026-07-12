'use client'

// 稱號解鎖慶祝彈窗：依 tier 越高越華麗（光暈/星塵/金框）。多個稱號一個一個看，最後關閉由父層標記 seen。
import { useState } from 'react'

const CAT_LABEL: Record<string, string> = {
  single_dist: '單次距離', cum_dist: '累積距離', cum_time: '累積時間', checkin: '打卡地點',
  boss: '關主挑戰', personal: '個人任務', level: '玩家等級', card: '卡片收藏',
}
function tierColor(tier: number) {
  return ['#9fb0c3', '#63a9ff', '#2de59a', '#c77dff', '#ffb24d', '#ffd24d'][Math.max(0, Math.min(5, tier - 1))]
}

export default function TitleUnlockModal({ titles, onClose }: { titles: { code: string; name: string; tier: number; category: string }[]; onClose: () => void }) {
  const [i, setI] = useState(0)
  if (!titles.length) return null
  const t = titles[Math.min(i, titles.length - 1)]
  const c = tierColor(t.tier)
  const last = i >= titles.length - 1

  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 700, background: 'rgba(3,6,4,.82)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24 }}>
      {/* 放射光暈背景 */}
      <div style={{ position: 'absolute', width: 460, height: 460, borderRadius: '50%', background: `radial-gradient(circle, ${c}44 0%, transparent 62%)`, filter: 'blur(6px)', animation: 'ttl-pulse 2.4s ease-in-out infinite' }} />
      {/* 星塵 */}
      {Array.from({ length: 14 }, (_, k) => (
        <span key={k} aria-hidden style={{
          position: 'absolute', fontSize: 10 + (k % 4) * 4, color: c,
          left: `${8 + (k * 61) % 84}%`, top: `${12 + (k * 37) % 74}%`,
          opacity: 0.8, animation: `ttl-twinkle ${1.4 + (k % 5) * 0.3}s ease-in-out ${(k % 6) * 0.18}s infinite`,
        }}>✦</span>
      ))}

      <div style={{ position: 'relative', width: '100%', maxWidth: 360, textAlign: 'center' }}>
        <div style={{ fontSize: 13, letterSpacing: '.3em', color: c, fontWeight: 800 }}>🎉 解鎖新稱號</div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 10 }}>{CAT_LABEL[t.category] || ''}</div>

        {/* 稱號框 */}
        <div style={{
          margin: '14px auto 0', padding: '22px 18px', borderRadius: 18,
          border: `2px solid ${c}`, background: `linear-gradient(160deg, ${c}22, rgba(0,0,0,.25))`,
          boxShadow: `0 0 34px ${c}88, inset 0 0 24px ${c}33`, animation: 'ttl-pop .5s cubic-bezier(.2,1.4,.4,1)',
        }}>
          <div style={{ fontSize: 28, fontWeight: 900, color: c, textShadow: `0 0 18px ${c}aa`, letterSpacing: '.04em', lineHeight: 1.3 }}>{t.name}</div>
        </div>

        {titles.length > 1 && (
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 12 }}>{i + 1} / {titles.length}</div>
        )}

        <button
          onClick={() => (last ? onClose() : setI((x) => x + 1))}
          style={{ marginTop: 18, background: c, color: '#06120c', fontWeight: 900, border: 'none', borderRadius: 12, padding: '13px 30px', fontSize: 15, cursor: 'pointer', fontFamily: 'inherit', boxShadow: `0 4px 18px ${c}66` }}
        >
          {last ? '太棒了！' : '下一個 ›'}
        </button>
      </div>

      <style>{`
        @keyframes ttl-pulse { 0%,100%{transform:scale(.92);opacity:.7} 50%{transform:scale(1.08);opacity:1} }
        @keyframes ttl-twinkle { 0%,100%{opacity:.15;transform:scale(.7)} 50%{opacity:.95;transform:scale(1.25)} }
        @keyframes ttl-pop { 0%{transform:scale(.6);opacity:0} 100%{transform:scale(1);opacity:1} }
      `}</style>
    </div>
  )
}
