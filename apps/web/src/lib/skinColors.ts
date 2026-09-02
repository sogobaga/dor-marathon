// 白幕底色／前景色：跟 skin 走（帶子區域在網頁圖層之外、任何元素都畫不到，只能靠 html/body 背景把它染成同色）。
// 抽成獨立、純模組（無 Next/React import）：src/middleware.ts 跑在 edge runtime，不能拉進伺服器端 App Router 的東西；
// layout.tsx 與 middleware.ts 共用同一份色表，兩邊才不會有一邊改色沒改到另一邊的風險。
export const VEIL_COLORS: Record<string, [string, string]> = {
  default: ['#09090f', '#e8e8ef'],
  warm: ['#FBF4E9', '#6b5a3e'],
  warm2: ['#FBF5EA', '#6b5a3e'],
}

export function veilColorsOf(skin: string | undefined): [string, string] {
  return (skin && VEIL_COLORS[skin]) || VEIL_COLORS.default
}
