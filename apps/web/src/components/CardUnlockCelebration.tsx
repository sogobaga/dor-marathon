'use client'

// 關主挑戰 3★ 完成、取得卡片時的恭喜彈窗（顯示卡片圖 + 恭喜文字 + 前往卡片圖鑑）。
// 圖鑑內才播放翻轉+星星粒子的解鎖特效（見 CardGalleryScreen focusCardId）。
export default function CardUnlockCelebration({ name, cardUrl, onGallery, onClose }: {
  name: string
  cardUrl?: string
  onGallery: () => void
  onClose: () => void
}) {
  return (
    <div data-skin="default" style={{ position: 'fixed', inset: 0, zIndex: 3400, background: 'rgba(4,8,6,.9)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 22 }}>
      <style>{`@keyframes cardPop { 0%{transform:scale(.55) rotate(-9deg);opacity:0} 60%{transform:scale(1.07) rotate(2deg);opacity:1} 100%{transform:scale(1) rotate(0)} }`}</style>
      <div style={{ width: '100%', maxWidth: 320, textAlign: 'center' }}>
        <div style={{ fontSize: 12.5, letterSpacing: '.32em', color: 'var(--gold)', fontWeight: 800 }}>★ 收 服 成 功 ★</div>
        <div style={{ fontSize: 20, fontWeight: 900, color: '#fff', margin: '6px 0 18px' }}>恭喜獲得卡片！</div>
        {cardUrl && (
          <div style={{ display: 'flex', justifyContent: 'center' }}>
            <img src={cardUrl} alt={name} style={{ width: '62%', aspectRatio: '3 / 4', objectFit: 'cover', borderRadius: 14, border: '2px solid var(--gold)', boxShadow: '0 0 30px rgba(231,184,75,.6)', animation: 'cardPop .7s ease-out' }} />
          </div>
        )}
        <div style={{ fontSize: 14, color: 'var(--tx)', marginTop: 16, fontWeight: 700 }}>「{name}」的專屬卡片已加入圖鑑</div>
        <button onClick={onGallery} style={{ marginTop: 18, width: '100%', background: 'var(--gold)', color: '#fff', fontWeight: 900, border: 'none', borderRadius: 12, padding: '13px', fontSize: 15, cursor: 'pointer', fontFamily: 'inherit' }}>前往卡片探索 ›</button>
        <button onClick={onClose} style={{ marginTop: 10, width: '100%', background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '11px', fontSize: 13.5, cursor: 'pointer', fontFamily: 'inherit' }}>稍後再看</button>
      </div>
    </div>
  )
}
