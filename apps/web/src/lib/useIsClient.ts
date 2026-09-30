// useIsClient：契約 docs/skins/TRACK_HYDRATION_CONTRACT.md 修法 1——凡是「輸出依賴 new Date()／
// Math.random()／localStorage 等只有瀏覽器才知道正確答案的東西」，SSR 那一輪與 hydration 那一輪
// 用的是兩個不同的時間點／環境，逐字比對文字內容的 React 一定會判定不一致（React 19 起会觸發
// #418 hydration mismatch，並把整棵樹改為 client 端重繪——這正是 /track 頁「整頁先閃預設風格才
// 跳成帳號風格」的根因，見契約 §根因）。
//
// 用 useSyncExternalStore 而非 useState(false)+useEffect：subscribe 給一個恆不通知變化的
// no-op（本來就不必訂閱任何東西，只是要借用 SSR/hydration 與之後的 client render 走不同
// snapshot 這個內建機制）；getServerSnapshot 固定回 false（SSR 與 hydration 那一輪都是 false，
// 兩邊逐字相同，不會 mismatch）；getSnapshot 在瀏覽器一律回 true。hydration 完成後 React 會用
// getSnapshot() 的新值（true）強制同步重渲染一次，呼叫端才會換成真正的 new Date()／localStorage
// 值——使用者不會看到「先閃一下假值」這一幀（與 lib/skinOverride.ts 的 subscribeSkinChange／
// getActiveSkin 是同一種手法，理由詳見該檔案的大段註解）。
import { useSyncExternalStore } from 'react'

const subscribe = () => () => {}
const getSnapshot = () => true
const getServerSnapshot = () => false

export function useIsClient(): boolean {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
