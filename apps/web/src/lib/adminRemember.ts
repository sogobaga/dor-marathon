// 後台登入「記住密碼」用：把帳密以 AES-GCM 加密後存 localStorage（非明碼，devtools 直接看不到）。
// 說明：金鑰也在本機（每台裝置隨機一把），所以這是「避免被一眼看光」的混淆等級，並非防得住「能操作這台裝置的人」。
// 真正的安全靠 HTTPS（傳輸）＋伺服器端雜湊（儲存）＋不共用/不外流裝置。需要更安全可改為「保持登入（存 token）」而非存密碼。
const KEY_LS = 'adm_rk'  // 隨機金鑰（base64）
const CRED_LS = 'adm_rc' // { iv, data }（base64）

function hasCrypto(): boolean {
  return typeof crypto !== 'undefined' && !!crypto.subtle // 需安全環境（HTTPS/localhost）
}
function b64(bytes: Uint8Array): string { return btoa(String.fromCharCode(...bytes)) }
function unb64(s: string): Uint8Array { return Uint8Array.from(atob(s), (c) => c.charCodeAt(0)) }
const buf = (u: Uint8Array): BufferSource => u as unknown as BufferSource // 繞過 TS 對 ArrayBufferLike 的過嚴檢查

async function getKey(): Promise<CryptoKey> {
  const raw = localStorage.getItem(KEY_LS)
  let bytes: Uint8Array
  if (raw) bytes = unb64(raw)
  else {
    bytes = crypto.getRandomValues(new Uint8Array(32))
    localStorage.setItem(KEY_LS, b64(bytes))
  }
  return crypto.subtle.importKey('raw', buf(bytes), 'AES-GCM', false, ['encrypt', 'decrypt'])
}

export async function saveCreds(email: string, password: string): Promise<void> {
  if (!hasCrypto()) return // 非安全環境：不存（避免落地明碼）
  try {
    const key = await getKey()
    const iv = crypto.getRandomValues(new Uint8Array(12))
    const enc = new TextEncoder().encode(JSON.stringify({ email, password }))
    const ct = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: buf(iv) }, key, buf(enc)))
    localStorage.setItem(CRED_LS, JSON.stringify({ iv: b64(iv), data: b64(ct) }))
  } catch { /* ignore */ }
}

export async function loadCreds(): Promise<{ email: string; password: string } | null> {
  if (!hasCrypto()) return null
  try {
    const raw = localStorage.getItem(CRED_LS)
    if (!raw) return null
    const { iv, data } = JSON.parse(raw)
    const key = await getKey()
    const pt = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: buf(unb64(iv)) }, key, buf(unb64(data)))
    const obj = JSON.parse(new TextDecoder().decode(pt))
    return { email: String(obj.email || ''), password: String(obj.password || '') }
  } catch { return null }
}

export function clearCreds(): void {
  try { localStorage.removeItem(CRED_LS) } catch { /* ignore */ }
}
