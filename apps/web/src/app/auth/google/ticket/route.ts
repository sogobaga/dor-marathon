import { NextRequest, NextResponse } from 'next/server'

// M2 修法：/auth/google/complete 登入完成頁的「一次性入場券」驗證端點——完整脈絡見
// app/auth/google/callback/route.ts 檔案頂端的說明。
//
// 只有真的剛從 /auth/google/callback 的 303 redirect 過來的瀏覽器，才會同時持有：
//   1) 這支路由檢查的 HttpOnly cookie dor_glogin_ok（伺服器在 callback 那次回應裡種下的，
//      攻擊者在「別人的瀏覽器」裡種不出來、寫不了也讀不到——HttpOnly 擋 JS 讀取，
//      且 cookie 本來就是逐瀏覽器/逐裝置的，無法從外部替受害者植入）
//   2) URL fragment 帶出的同一個隨機值 t（callback 那次 303 redirect 一併塞進 hash）
// 兩者相符才放行；否則視為「有人把整段導轉網址（含 fragment 裡的 credential）轉傳/誘騙受害者
// 開啟」的登入 CSRF——受害者的瀏覽器不會有攻擊者那次登入種下的 cookie，比對不符直接擋下，
// credential 不會被拿去換發本站 token，受害者的瀏覽器不會被換成攻擊者指定的身分登入。
//
// 驗證完（無論成功或失敗）一律清掉這顆 cookie——每張 ticket 只能兌換一次，防止被重複拿去試。
// 回應一律 no-store，避免被瀏覽器或中介層快取住「上一次」的驗證結果。

export async function GET(req: NextRequest): Promise<NextResponse> {
  const t = req.nextUrl.searchParams.get('t') || ''
  const cookieVal = req.cookies.get('dor_glogin_ok')?.value || ''

  const ok = t !== '' && cookieVal !== '' && t === cookieVal

  const res = NextResponse.json(
    { ok },
    { status: ok ? 200 : 400, headers: { 'cache-control': 'no-store' } },
  )
  res.cookies.set('dor_glogin_ok', '', {
    httpOnly: true,
    secure: true,
    sameSite: 'lax',
    maxAge: 0,
    path: '/',
  })
  return res
}
