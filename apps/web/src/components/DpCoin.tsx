// DP 幣（DOR Point）圖示 — 使用美術素材 PNG，可任意縮放；API 與舊版 SVG 相同（size/style），全站沿用。
export default function DpCoin({ size = 20, style }: { size?: number; style?: React.CSSProperties }) {
  return (
    <img
      src="/source/ui/01_icons/icon_dp_coin.png"
      width={size}
      height={size}
      alt="DP"
      draggable={false}
      style={{ display: 'inline-block', objectFit: 'contain', flexShrink: 0, ...style }}
    />
  )
}
