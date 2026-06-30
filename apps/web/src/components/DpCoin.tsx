// DP 幣（DOR Point）金色硬幣圖示 — 純 SVG，可任意縮放
export default function DpCoin({ size = 20, style }: { size?: number; style?: React.CSSProperties }) {
  const id = 'dpcoin' // 單一漸層即可（同頁多顆共用沒問題）
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg" style={style} aria-label="DP">
      <defs>
        <radialGradient id={`${id}-face`} cx="38%" cy="32%" r="75%">
          <stop offset="0%" stopColor="#FFF1B8" />
          <stop offset="45%" stopColor="#FFD24D" />
          <stop offset="100%" stopColor="#E0A211" />
        </radialGradient>
        <linearGradient id={`${id}-rim`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="#FFE07A" />
          <stop offset="100%" stopColor="#B97B0C" />
        </linearGradient>
      </defs>
      {/* 外緣 */}
      <circle cx="24" cy="24" r="23" fill={`url(#${id}-rim)`} />
      {/* 幣面 */}
      <circle cx="24" cy="24" r="19" fill={`url(#${id}-face)`} stroke="#C8890E" strokeWidth="1" />
      {/* 內圈 */}
      <circle cx="24" cy="24" r="16" fill="none" stroke="#C8890E" strokeOpacity="0.55" strokeWidth="1.2" />
      {/* DP 字樣 */}
      <text x="24" y="25" textAnchor="middle" dominantBaseline="central"
        fontFamily="Arial, Helvetica, sans-serif" fontWeight="800" fontSize="17" fill="#8A5A06" letterSpacing="0.5">DP</text>
      {/* 高光 */}
      <ellipse cx="18" cy="15" rx="6" ry="3.2" fill="#FFFFFF" opacity="0.45" />
    </svg>
  )
}
