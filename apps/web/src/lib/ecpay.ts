// 動態建立 hidden 表單並 POST 到綠界（瀏覽器導去付款頁）。報名完成頁與個人資訊頁共用。
export function submitEcpayForm(actionURL: string, params: Record<string, string>) {
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = actionURL
  form.acceptCharset = 'UTF-8'
  for (const [k, v] of Object.entries(params)) {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = k
    input.value = v
    form.appendChild(input)
  }
  document.body.appendChild(form)
  form.submit()
}

// --- 站內付 2.0 綁卡 SDK（VIP 訂閱 Phase E）---
//
// ECPay JS SDK 依賴 jQuery + node-forge，且三者載入順序不可調換（SDK 啟動時檢查 typeof jQuery，
// 缺 node-forge 會直接 throw Error）；SDK script 本身依環境分兩個 domain（測試 ecpg-stage、正式
// ecpg），載入的環境必須與 ECPay.initialize(ServerType,...) 的 ServerType 一致，否則 CORS/行為異常。
// 見 BindCardModal 使用方式；後端 server_type 由 /profile/vip/subscribe 回應帶回，前端不用自己猜環境。

export type EcpayServerType = 'Stage' | 'Prod'

declare global {
  interface Window {
    ECPay?: {
      initialize: (serverType: EcpayServerType, isLoading: 0 | 1, cb: (errMsg: string | null) => void) => void
      addBindingCard: (token: string, language: string, cb: (errMsg: string | null) => void) => void
      getBindCardPayToken: (cb: (paymentInfo: { BindCardPayToken: string } | null, errMsg: string | null) => void) => void
      Language?: { zhTW: string; enUS: string }
    }
  }
}

// loadScriptOnce：同一個 src 全站只插入一次 <script>（用 data-ecpay-src 標記去重），
// 重複呼叫（例如彈窗重新掛載）直接複用同一個 Promise/已插入的節點，不會插入第二份 script。
const scriptLoadPromises = new Map<string, Promise<void>>()
function loadScriptOnce(src: string): Promise<void> {
  const cached = scriptLoadPromises.get(src)
  if (cached) return cached
  const p = new Promise<void>((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>(`script[data-ecpay-src="${src}"]`)
    if (existing) {
      if (existing.dataset.loaded === '1') { resolve(); return }
      existing.addEventListener('load', () => resolve())
      existing.addEventListener('error', () => reject(new Error(`載入失敗：${src}`)))
      return
    }
    const s = document.createElement('script')
    s.src = src
    s.async = false // 保留載入/執行順序（jQuery → node-forge → SDK），避免 SDK 早於依賴執行
    s.dataset.ecpaySrc = src
    s.onload = () => { s.dataset.loaded = '1'; resolve() }
    s.onerror = () => reject(new Error(`載入失敗：${src}`))
    document.head.appendChild(s)
  })
  scriptLoadPromises.set(src, p)
  return p
}

// loadEcpaySdk 依序載入 jQuery 3.5.1 → node-forge 0.7.0 → 綠界站內付 SDK（依 serverType 選 stage/prod
// domain），全站只需成功跑過一次；serverType 理論上同一次瀏覽只會有一種（後端環境固定），若曾用
// 另一種 serverType 載過，沿用第一次的結果，避免同頁插入兩份互相覆蓋全域 window.ECPay 的 SDK。
let sdkPromise: Promise<void> | null = null
export function loadEcpaySdk(serverType: EcpayServerType): Promise<void> {
  if (typeof window === 'undefined') return Promise.reject(new Error('no window'))
  if (sdkPromise) return sdkPromise
  const sdkHost = serverType === 'Prod' ? 'https://ecpg.ecpay.com.tw' : 'https://ecpg-stage.ecpay.com.tw'
  sdkPromise = loadScriptOnce('https://code.jquery.com/jquery-3.5.1.min.js')
    .then(() => loadScriptOnce('https://cdn.jsdelivr.net/npm/node-forge@0.7.0/dist/forge.min.js'))
    .then(() => loadScriptOnce(`${sdkHost}/Scripts/sdk-1.0.0.js?t=20210121100116`))
  return sdkPromise
}
