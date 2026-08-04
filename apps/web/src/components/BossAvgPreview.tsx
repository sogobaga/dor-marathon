// 關主 AVG 合成預覽（場景圖當背景 cover ＋ 角色立繪頂部對齊只露上半身 ＋ 底部漸層放名稱/星等/稱號）。
// 從 BossChallengePanel 抽出的共用元件，讓前台挑戰彈窗與後台預覽牆（QA 用）共用同一份合成邏輯與樣式，
// 確保兩邊像素一致。純呈現、無 hook，不需要 'use client'。
export default function BossAvgPreview({
  scene, master, name, stars, title, showLabel = true, radius = 18, lazy = false,
}: {
  scene?: string
  master?: string
  name?: string
  stars?: number
  title?: string
  showLabel?: boolean // 預設 true：底部漸層＋⚔️關主挑戰＋名稱＋星等＋稱號
  radius?: string | number // borderRadius，預設 18
  lazy?: boolean // true → <img loading="lazy">（後台預覽牆用，避免一次載 572 張）
}) {
  return (
    // data-skin="default" 讓 var(--gold) 等 CSS 變數在後台頁（非前台 skin context）也能解析，維持與前台一致外觀。
    <div data-skin="default" style={{ position: 'relative', width: '100%', aspectRatio: '16 / 9', overflow: 'hidden', borderRadius: radius, background: '#0b0e13' }}>
      {scene && (
        <img src={scene} alt="" loading={lazy ? 'lazy' : undefined} style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'cover' }} />
      )}
      {master && (
        // 立繪為直式全身圖（頭在頂、腳在底）：頭部對齊容器上緣（top:0）＋ height 放大到容器 210%
        // → 容器只露出「頭～腰」的上半身，腿往下被容器 overflow:hidden 裁掉；角色像前景站在鏡頭前、場景在後方。
        <img src={master} alt={name} loading={lazy ? 'lazy' : undefined} style={{ position: 'absolute', left: '50%', top: 0, transform: 'translateX(-50%)', height: '210%', width: 'auto', maxWidth: 'none', objectFit: 'contain', objectPosition: 'top', pointerEvents: 'none' }} />
      )}
      {showLabel && (
        <div style={{ position: 'absolute', left: 0, right: 0, bottom: 0, padding: '24px 16px 10px', background: 'linear-gradient(transparent, rgba(4,8,6,.92))' }}>
          <div style={{ fontSize: 11, letterSpacing: '.25em', color: 'var(--gold)', fontWeight: 800 }}>⚔️ 關主挑戰</div>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginTop: 2 }}>
            <span style={{ fontSize: 20, fontWeight: 900, color: '#fff' }}>{name}</span>
            <span style={{ fontSize: 12, color: 'var(--gold)', letterSpacing: 1 }}>{'★'.repeat(Math.max(0, stars ?? 0))}</span>
          </div>
          {title && <div style={{ fontSize: 12, color: 'rgba(255,255,255,.75)', marginTop: 1 }}>{title}</div>}
        </div>
      )}
    </div>
  )
}
