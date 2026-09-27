// 未來科幻世界（scifi）變體共用字型（CONTRACT.md §3／§4.4）：Orbitron 只在這裡呼叫一次 next/font/google，
// 由 SciFiMap.tsx（track/page.tsx 以 next/dynamic ssr:false 動態載入）與 OrbitronText.tsx（RaceFocusMode.tsx
// 以 next/dynamic 動態載入、只在 `scifi` 為真時才進 JSX 樹）共用同一個實例，避免兩處各自呼叫 Orbitron()
// 產生兩份重複的 @font-face CSS。本檔本身位於 track/scifi/ 目錄，只會被上述兩個「動態 import 之後」的
// 模組引用，非白名單使用者（或白名單但偏好關閉）不會觸發任何一個動態 import，本檔就不會被下載/執行
// （2026-09-27 review 修正：RaceFocusMode.tsx 原本在自己的靜態 import 頂層直接呼叫 Orbitron()，導致
// 所有 /track 訪客都會多一個 CSS chunk，見該檔改動說明）。
import { Orbitron } from 'next/font/google'

export const orbitron = Orbitron({ subsets: ['latin'], weight: ['700', '900'], display: 'swap' })
