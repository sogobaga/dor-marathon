// 風格覆寫（scifi／retro／cute）專屬字型的 family／URL 單一來源。
//
// 契約 docs/skins/HOME_FLASH_CONTRACT.md 修法 3：開機腳本（app/layout.tsx 的 skinOverrideBootJs，
// 純字串 JS、在 React 開始渲染前執行）要「搶先」以 FontFace 註冊字型，不能等
// components/{retro,cute}/RetroBackground.tsx／CuteBackground.tsx 掛載才開始下載（那正是「風格
// 字型晚到」的根因，見契約「根因」第 2 點）——但開機腳本是伺服器端組出來的純字串，不能
// import 帶有 'use client'／React 依賴的 components/{retro,cute}/fonts.ts。
//
// 這支檔案沒有任何 React／DOM／Next 依賴，是唯一能同時被以下三處安全 import 的地方：
//   1. components/retro/fonts.ts（瀏覽器端 FontFace 註冊，RetroBackground 掛載時呼叫）
//   2. components/cute/fonts.ts（同上，CuteBackground）
//   3. app/layout.tsx（伺服器端 RootLayout，組 skinOverrideBootJs 的字串時內嵌這裡的常數）
// family／URL 只在這裡宣告一次，三處改用同一份，不會有「開機腳本」與「背景層掛載後」用
// 不同字型檔／family 名稱而各自為政的風險。

export const RETRO_FONT_FAMILY = 'DORPixel'
export const RETRO_FONT_URL = '/fonts/cubic11/Cubic_11.woff2'

export const CUTE_FONT_FAMILY = 'DORCute'
export const CUTE_FONT_URL = '/fonts/dorcute/DORCute.woff2'
