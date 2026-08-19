// 站內／站外連結判定 + 導向工具。抽自蓋板廣告(InterstitialAd) CTA 的既有慣例，供簡章圖片
// 點擊連結等其他「使用者可設定網址，需分站內/站外導向」情境共用，確保判定邏輯只有一份、
// 各處永遠一致。

// 判斷網址是否為「本站內部連結」：相對路徑，或絕對網址但網域為本站/dor.tw。
// 是則回傳可供 router.push 的路徑（含 search/hash）；否則回傳 null（視為外部網址）。
export function toInternalPath(url: string): string | null {
  if (url.startsWith('/')) return url
  try {
    const u = new URL(url, window.location.origin)
    const host = u.host.toLowerCase()
    if (host === window.location.host.toLowerCase() || host === 'dor.tw' || host === 'www.dor.tw') {
      return u.pathname + u.search + u.hash
    }
  } catch { /* 非合法 URL */ }
  return null
}

// 依內外連結慣例導向：站內用 router.push（不開新分頁）；站外開新分頁（noopener）。
// 外部網址僅允許 http/https scheme，其餘（javascript: data: 等）一律忽略、不開分頁
// （比照 htmlsafe 消毒政策的精神：使用者可控網址一律驗證 scheme）。
export function navigateLink(url: string, router: { push: (href: string) => void }) {
  const u = (url ?? '').trim()
  if (!u) return
  const internal = toInternalPath(u)
  if (internal) {
    router.push(internal)
    return
  }
  try {
    const parsed = new URL(u, window.location.origin)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return
  } catch {
    return
  }
  window.open(u, '_blank', 'noopener')
}
