import { NextRequest, NextResponse } from 'next/server'

// Google 登入整頁導轉（google_login_ux_mode='redirect'，見 lib/appSettings.ts、components/UserAuthBar.tsx）
// 的收件端點：iOS 上 GIS（Google Identity Services）以 <GoogleLogin ux_mode="redirect" login_uri={這支路由}>
// 設定後，使用者選完 Google 帳號會由 Google 端把驗證結果整頁 POST 回這裡（不是 JS callback）。
//
// 【CSRF 雙重送出驗證】GIS 在載入按鈕時，會在本站網域種一個 cookie g_csrf_token，並在 POST body 附上同名欄位
// （Google 官方文件的既定作法）；只有 cookie 與 body 兩者都存在且完全相符，才代表這次 POST 真的來自剛才那次
// 登入流程，否則一律視為偽造請求拒絕（不細分原因，避免對攻擊者洩漏驗證細節）。
//
// 【body 編碼】官方文件對 login_uri 收到的 POST body 編碼沒有給出可 100% 確認的單一答案（不同官方頁面/範例
// 出現過 JSON 與 form-urlencoded兩種說法），因此這裡兩種都嘗試解析，避免正式環境因編碼落差整組登入直接掛掉；
// 之後如在正式環境用 ?vpdebug=1 類方式觀察到穩定的單一編碼，可以再收斂成只解一種。
//
// 【安全】credential 是使用者的 Google ID token，等同一次性登入憑證：全程不得寫進 console.log／任何持久化
// 記錄，只透過 303 redirect 的 URL fragment（伺服器端本來就讀不到、也不會被任何 log 記下）轉交給客端頁面
// app/auth/google/complete/page.tsx 處理。回應一律 no-store，避免被瀏覽器或中介層快取。

async function parseBody(req: NextRequest): Promise<{ credential: string | null; csrfToken: string | null }> {
  const contentType = req.headers.get('content-type') || ''
  try {
    if (contentType.includes('application/json')) {
      const body = await req.json()
      return {
        credential: typeof body?.credential === 'string' ? body.credential : null,
        csrfToken: typeof body?.g_csrf_token === 'string' ? body.g_csrf_token : null,
      }
    }
    // 預設走 form-urlencoded／multipart（GIS 官方文件多處引用的形狀）
    const form = await req.formData()
    const credential = form.get('credential')
    const csrfToken = form.get('g_csrf_token')
    return {
      credential: typeof credential === 'string' ? credential : null,
      csrfToken: typeof csrfToken === 'string' ? csrfToken : null,
    }
  } catch {
    return { credential: null, csrfToken: null }
  }
}

function failurePage(): NextResponse {
  // 不透露具體失敗原因（CSRF 不符／缺憑證皆同一句），避免對攻擊者洩漏驗證細節；使用者只需重新登入一次。
  const html = `<!doctype html>
<html lang="zh-TW">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>登入失敗｜DOR</title>
<style>html,body{margin:0;height:100%;background:#09090f;color:#e8e8ef;font-family:-apple-system,BlinkMacSystemFont,"PingFang TC",sans-serif;display:flex;align-items:center;justify-content:center}.b{text-align:center;padding:24px}a{color:#2DE59A}</style>
</head>
<body><div class="b"><p>登入驗證失敗，請重新嘗試。</p><p><a href="/">回首頁</a></p></div></body>
</html>`
  return new NextResponse(html, {
    status: 400,
    headers: { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' },
  })
}

export async function POST(req: NextRequest): Promise<NextResponse> {
  const cookieToken = req.cookies.get('g_csrf_token')?.value || null
  const { credential, csrfToken: bodyToken } = await parseBody(req)

  // 雙重送出比對：cookie／body 任一缺席，或兩者不完全相符，一律拒絕
  if (!cookieToken || !bodyToken || cookieToken !== bodyToken) return failurePage()
  if (!credential) return failurePage()

  // 303 See Other：把這次 POST 轉成 GET 導覽，credential 只放在 fragment（# 後面，永遠不會送到任何伺服器，
  // 也不會出現在 Railway/CDN 的存取記錄裡）。fragment 內容經 encodeURIComponent，避免 JWT 本身若含特殊字元
  // 破壞網址結構（一般 JWT 只含 base64url 字元集，但保守處理不假設）。
  const target = new URL('/auth/google/complete', req.url)
  target.hash = `credential=${encodeURIComponent(credential)}`

  return NextResponse.redirect(target, {
    status: 303,
    headers: { 'cache-control': 'no-store' },
  })
}

// 直接用 GET 訪問這支端點（不是 Google 導回的正常流程）：導去首頁，不留在一支只接受 POST 的路由上出錯頁。
export async function GET(req: NextRequest): Promise<NextResponse> {
  return NextResponse.redirect(new URL('/', req.url), {
    status: 302,
    headers: { 'cache-control': 'no-store' },
  })
}
