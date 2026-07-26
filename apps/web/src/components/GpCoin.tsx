// GP 幣（Game Point，環台大富翁貨幣）圖示 — 純 SVG 徽章（無額外美術素材依賴），
// 與 DpCoin 同尺寸/style API（size/style），全站沿用；用 --violet 與 DP 的金色區隔。
export default function GpCoin({ size = 20, style }: { size?: number; style?: React.CSSProperties }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 20 20"
      style={{ display: 'inline-block', flexShrink: 0, ...style }}
      aria-label="GP"
      role="img"
    >
      <circle cx="10" cy="10" r="9" fill="var(--violet)" stroke="rgba(255,255,255,.35)" strokeWidth="1" />
      <text x="10" y="13.5" textAnchor="middle" fontSize="8" fontWeight="900" fill="#fff" fontFamily="system-ui, sans-serif">GP</text>
    </svg>
  )
}
