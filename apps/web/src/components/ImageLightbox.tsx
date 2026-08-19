'use client'

// 單張圖片放大檢視（縮圖點擊 → 全螢幕 lightbox）。
// 給加購項目／參賽物資／完賽物資等「單一 image_url、無需多圖滑動」的縮圖共用。
// 多圖輪播需求請改用 shared/MediaCarousel 的 Lightbox（簡章多圖用）。
export default function ImageLightbox({ src, onClose }: { src: string; onClose: () => void }) {
  return (
    <div onClick={onClose} style={overlay}>
      <button onClick={(e) => { e.stopPropagation(); onClose() }} style={closeBtn} aria-label="關閉">✕</button>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={src} alt="" onClick={(e) => e.stopPropagation()} style={img} />
    </div>
  )
}

// z-index：面板慣例 500，本 overlay 需覆蓋一切內容 → 比照站內既有全螢幕圖片放大（CardGalleryScreen 卡片放大）用 3300。
const overlay: React.CSSProperties = {
  position: 'fixed', inset: 0, zIndex: 3300, background: 'rgba(0,0,0,.85)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 24, cursor: 'zoom-out',
  opacity: 1, transition: 'opacity .15s ease',
}
const closeBtn: React.CSSProperties = {
  position: 'absolute', top: 14, right: 14, zIndex: 1, width: 38, height: 38, borderRadius: 999,
  border: 'none', cursor: 'pointer', background: 'rgba(0,0,0,.5)', color: '#fff', fontSize: 18,
  display: 'flex', alignItems: 'center', justifyContent: 'center',
}
const img: React.CSSProperties = {
  maxWidth: '92%', maxHeight: '86%', objectFit: 'contain', borderRadius: 12,
  boxShadow: '0 10px 40px rgba(0,0,0,.6)', cursor: 'default',
}
