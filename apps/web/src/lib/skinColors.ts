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

// 帳號層級「風格設定」覆寫（scifi／retro／cute，見 lib/skinOverride.ts）專用色表。單一來源，
// 契約 docs/skins/HOME_FLASH_CONTRACT.md 修法 3／4：
//   ・OVERRIDE_THEME_COLOR：meta[name=theme-color] 用色，原本各自寫死在 lib/skinOverride.ts 的
//     THEME_COLOR（該檔案改 import 這裡）；開機腳本 app/layout.tsx 的 skinOverrideBootJs 也內嵌
//     這份表，三處（開機腳本／React 套用時／React 收回時）只有一份色號，不會分岔。
//   ・OVERRIDE_VEIL_COLORS：白幕／到站彈跳頁（bootJs／middleware.ts）在 <html data-skin> 已是
//     覆寫風格時要用的白幕色，取「非首頁」頁面底色（首頁 --bg 故意 transparent 透出風格背景層，
//     見 globals.css [data-skin="retro"/"cute"] §6「首頁例外」，白幕階段背景層通常還沒掛上，
//     用 transparent 沒有意義，改取頁面實際底色），逐一對照 globals.css：
//     - scifi：[data-skin="scifi"] --bg #02040a／--tx #e8f7ff（本身就是純色，無需近似）
//     - retro：[data-skin="retro"] --bg 是多層漸層 linear-gradient(180deg,#ebddbb→#dbc18d)
//       疊斑點/纖維紋，取與同一份漸層終點同色階的 --bg-1 終點色 #e3c995 當代表色（視覺同一
//       色階，肉眼幾乎無感）；--tx #3b2412。
//     - cute：[data-skin="cute"] --bg 最底層（多層 background 的最後一層）即純色 #fff5f8
//       （milk，圓點圖樣半透明疊在其上，取樣其純色底不失真）；--tx 為 --cute-berry #5b2a3c。
export type OverrideSkin = 'scifi' | 'retro' | 'cute'

export const OVERRIDE_THEME_COLOR: Record<OverrideSkin, string> = {
  scifi: '#02040a',
  retro: '#000000',
  cute: '#fff5f8',
}

export const OVERRIDE_VEIL_COLORS: Record<OverrideSkin, [string, string]> = {
  scifi: ['#02040a', '#e8f7ff'],
  retro: ['#e3c995', '#3b2412'],
  cute: ['#fff5f8', '#5b2a3c'],
}

// overrideVeilColorsOf：middleware.ts 到站彈跳頁用——cookie dor_skin_ov 值若不是覆寫風格三選一
// 之一（含未設定、null、或被竄改成其他字串），一律回 null，呼叫端落回 veilColorsOf(SSR skin)。
export function overrideVeilColorsOf(skin: string | null | undefined): [string, string] | null {
  return skin === 'scifi' || skin === 'retro' || skin === 'cute' ? OVERRIDE_VEIL_COLORS[skin] : null
}
