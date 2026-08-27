// 會員活躍度分析（/admin/analytics）圖表色盤。延續 signupSource.ts 的 CVD-safe 思路：
// 固定順序的類別色階，直接沿用 SIGNUP_SOURCE_COLOR 前 8 色的同一批來源（dataviz skill 已驗證的
// 類別色階，dark 步階），依「桶次序（index）」固定指派——同一桶在不同圖表、不同次載入間顏色一致，
// 不依資料排序動態上色。每個色塊一律搭配中文文字標籤（legend 文字／<title> hover），
// 身分辨識不單獨依賴顏色。
export const ANALYTICS_PALETTE: string[] = [
  '#3987e5',
  '#d55181',
  '#199e70',
  '#c98500',
  '#9085e9',
  '#008300',
  '#d95926',
  '#e66767',
]

export function analyticsColor(index: number): string {
  return ANALYTICS_PALETTE[((index % ANALYTICS_PALETTE.length) + ANALYTICS_PALETTE.length) % ANALYTICS_PALETTE.length]
}
