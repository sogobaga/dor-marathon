// 會員註冊來源歸因（migration 147_signup_attribution）：App 首次進站擷取 referrer/landing url，
// 留到註冊/Google 登入成功時才送給後端 classify（facebook/instagram/line/google/threads/tiktok/other/direct）。
//
// 與既有 ?ref= 推廣連結機制（見 PhoneShell.tsx 的 dor:ref_code、referral.BindReferrer）並存、互不覆蓋：
// 後端優先看「有 ref 碼且解析到推薦人」→ referral；查無 ref 碼才會落到這裡的 utm/referrer 網域分類。
// 兩者是「精確推薦人綁定」vs「來源分類 fallback」的關係，各自獨立擷取、獨立送出。

const ACQ_KEY = 'dor:acq:v1'
const MAX_LEN = 500 // 契約：landing_url/referrer_url 各截斷 500 字元才送給後端

interface AcquisitionData {
  referrer: string
  landing: string
  ts: number
}

// App 開啟時呼叫一次（見 PhoneShell.tsx 進站初始化 useEffect，與 ?ref= 擷取同一區塊、互不影響）。
// first-touch：只在尚未記錄過時才寫入，之後不論重進幾次都不覆寫——保留使用者「第一次」進站的來源。
// try/catch 防守：無痕/隱私模式下 localStorage 可能擲錯，靜默放棄即可，不該讓整個進站流程掛掉。
export function captureAcquisition(): void {
  if (typeof window === 'undefined') return
  try {
    if (localStorage.getItem(ACQ_KEY)) return // 已有記錄，first-touch 不覆寫
    const data: AcquisitionData = {
      referrer: document.referrer || '',
      landing: window.location.href,
      ts: Date.now(),
    }
    localStorage.setItem(ACQ_KEY, JSON.stringify(data))
  } catch {
    // 隱私模式/儲存空間滿等 → 靜默放棄，不影響其他功能
  }
}

// 讀出 first-touch 記錄（供其他用途；一般直接用下面 buildAcqPayload 即可）。讀不到就回 null。
export function getAcquisition(): AcquisitionData | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = localStorage.getItem(ACQ_KEY)
    if (!raw) return null
    return JSON.parse(raw) as AcquisitionData
  } catch {
    return null
  }
}

function truncate(s: string): string {
  return s.length > MAX_LEN ? s.slice(0, MAX_LEN) : s
}

// 組成註冊/Google 登入 request body 要帶的選填 acq 欄位（各截斷 500 字元）。
// 讀不到 first-touch 資料（例如隱私模式、或 captureAcquisition 從未成功寫入）就回 undefined，
// 呼叫端不附加 acq 欄位即可——後端只在有值時才用來 classify，缺欄位一律視為 direct。
export function buildAcqPayload(): { landing_url: string; referrer_url: string } | undefined {
  const acq = getAcquisition()
  if (!acq) return undefined
  return {
    landing_url: truncate(acq.landing || ''),
    referrer_url: truncate(acq.referrer || ''),
  }
}
