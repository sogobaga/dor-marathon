'use client'

import { useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminPushApi, adminPushGroupsApi, adminRacesApi, adminEmailBroadcastApi, type PushGroup, type Race, type AdminPushBroadcastResult, type EmailBroadcastItem, type EmailBroadcastCreateResult } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

type TargetType = 'all' | 'user' | 'race' | 'group'

export default function AdminPushPage() {
  const router = useRouter()
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [url, setUrl] = useState('')
  const [doPush, setDoPush] = useState(true)
  const [doEmail, setDoEmail] = useState(false)
  const [doMail, setDoMail] = useState(false)
  const [level, setLevel] = useState<'normal' | 'important' | 'urgent'>('normal')
  const [targetType, setTargetType] = useState<TargetType>('all')
  const [identifier, setIdentifier] = useState('')
  const [raceId, setRaceId] = useState('')
  const [groupId, setGroupId] = useState('')
  const [races, setRaces] = useState<Race[]>([])
  const [groups, setGroups] = useState<PushGroup[]>([])
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<AdminPushBroadcastResult | null>(null)
  const [err, setErr] = useState('')

  // --- Email 廣播（Resend，全玩家或指定帳號/Email 批次寄送，migration 142 加指定對象）---
  const [ebSubject, setEbSubject] = useState('')
  const [ebBody, setEbBody] = useState('')
  const [ebAudience, setEbAudience] = useState<'all' | 'custom'>('all')
  const [ebRecipients, setEbRecipients] = useState('')
  const [ebSending, setEbSending] = useState(false)
  const [ebErr, setEbErr] = useState('')
  const [ebResult, setEbResult] = useState<EmailBroadcastCreateResult | null>(null)
  const [ebHistory, setEbHistory] = useState<EmailBroadcastItem[]>([])
  const ebPollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch(() => {})
    adminPushGroupsApi.list(t).then((r) => setGroups(r.groups)).catch(() => {})
    loadEbHistory()
    return () => { if (ebPollRef.current) clearInterval(ebPollRef.current) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router])

  async function loadEbHistory() {
    const t = getToken()
    if (!t) return
    try {
      const r = await adminEmailBroadcastApi.list(t)
      setEbHistory(r.broadcasts)
      const hasSending = r.broadcasts.some((b) => b.status === 'sending')
      if (hasSending && !ebPollRef.current) {
        ebPollRef.current = setInterval(loadEbHistory, 5000)
      } else if (!hasSending && ebPollRef.current) {
        clearInterval(ebPollRef.current)
        ebPollRef.current = null
      }
    } catch { /* 靜默失敗，下次輪詢再試 */ }
  }

  function parseRecipients(raw: string): string[] {
    return raw.split(/[\n,]+/).map((s) => s.trim()).filter(Boolean)
  }

  async function sendEmailBroadcast() {
    const token = getToken()
    if (!token) { router.replace('/admin/login'); return }
    const subject = ebSubject.trim()
    const body = ebBody.trim()
    if (!subject || !body) { setEbErr('請填寫主旨與內文'); return }

    const recipients = ebAudience === 'custom' ? parseRecipients(ebRecipients) : undefined
    if (ebAudience === 'custom') {
      if (!recipients || recipients.length === 0) { setEbErr('請輸入至少一個 Email'); return }
      if (recipients.length > 500) { setEbErr('收件人數量上限 500 筆'); return }
    }
    setEbErr(''); setEbResult(null)

    // 送出前先問一次真實數字（全部玩家＝recipient-count；指定對象＝Create 的 dry_run 預覽，
    // 兩者都不影響「同時只能一則寄送中」的防呆，dry_run 不建立紀錄也不觸發寄送）。
    let preview: { total: number; not_found: string[]; unsubscribed_excluded: number }
    try {
      if (ebAudience === 'all') {
        const r = await adminEmailBroadcastApi.recipientCount(token)
        preview = { total: r.count, not_found: [], unsubscribed_excluded: 0 }
      } else {
        const r = await adminEmailBroadcastApi.create(token, { subject, body_html: body, recipients, dry_run: true })
        preview = { total: r.total, not_found: r.not_found, unsubscribed_excluded: r.unsubscribed_excluded }
      }
    } catch (e: any) {
      setEbErr(e?.message || '無法取得寄送對象人數'); return
    }

    const skipMsg = ebAudience === 'custom'
      ? `（略過查無會員 ${preview.not_found.length} 筆／已退訂 ${preview.unsubscribed_excluded} 筆）`
      : ''
    const audienceWord = ebAudience === 'all' ? '位玩家' : '位會員'
    if (!window.confirm(`將寄給 ${preview.total} ${audienceWord}${skipMsg}，確定？`)) return

    setEbSending(true)
    try {
      const r = await adminEmailBroadcastApi.create(token, { subject, body_html: body, recipients })
      setEbResult(r)
      setEbSubject(''); setEbBody(''); setEbRecipients('')
      await loadEbHistory()
    } catch (e: any) {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setEbErr(e?.message || '發送失敗')
    } finally {
      setEbSending(false)
    }
  }

  function ebStatusLabel(s: EmailBroadcastItem['status']) {
    switch (s) {
      case 'sending': return { text: '寄送中…', color: 'var(--fug)' }
      case 'done': return { text: '已完成', color: 'var(--fug)' }
      case 'partial': return { text: '部分失敗', color: '#e0a020' }
      case 'failed': return { text: '失敗', color: 'var(--hunt)' }
      default: return { text: s, color: 'var(--tx-dim)' }
    }
  }

  function ebAudienceLabel(a: string) {
    if (a === 'all') return '全部玩家'
    const m = a.match(/^custom:(\d+)人$/)
    return m ? `指定 ${m[1]} 人` : a
  }

  async function send() {
    const token = getToken()
    if (!token) { router.replace('/admin/login'); return }
    if (!title.trim() || !body.trim()) { setErr('請填寫標題與內容'); return }
    const channels = [...(doPush ? ['push'] : []), ...(doEmail ? ['email'] : []), ...(doMail ? ['mail'] : [])] as ('push' | 'email' | 'mail')[]
    if (!channels.length) { setErr('請至少勾選一個發送頻道（推播／Email／站內信）'); return }
    if (targetType === 'user' && !identifier.trim()) { setErr('請輸入帳號編碼或 Email'); return }
    if (targetType === 'race' && !raceId) { setErr('請選擇賽事'); return }
    if (targetType === 'group' && !groupId) { setErr('請選擇群組'); return }
    setSending(true); setErr(''); setResult(null)
    try {
      const r = await adminPushApi.broadcast(token, {
        title: title.trim(), body: body.trim(), url: url.trim() || undefined,
        channels, target_type: targetType,
        level: doMail ? level : undefined,
        identifier: targetType === 'user' ? identifier.trim() : undefined,
        race_id: targetType === 'race' ? raceId : undefined,
        group_id: targetType === 'group' ? groupId : undefined,
      })
      setResult(r)
    } catch (e: any) {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '發送失敗')
    } finally {
      setSending(false)
    }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 8px', fontSize: 24, fontWeight: 800 }}>推播通知</h1>
      <p style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7, margin: '0 0 16px', maxWidth: 640 }}>
        發送 Web Push／Email／站內信給指定對象。<b>點擊網址</b>留空預設導向首頁 <code>/</code>。Email 需管理員設好 SMTP 後才會實際寄出（未設定則只發推播）。站內信會出現在會員的「訊息中心」，不受瀏覽器推播權限或 Email 設定影響。
      </p>

      <div style={card}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <F label="標題"><input style={inp} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="例：新賽事開放報名！" /></F>
          <F label="內容"><textarea style={{ ...inp, minHeight: 80, resize: 'vertical' }} value={body} onChange={(e) => setBody(e.target.value)} placeholder="通知內文" /></F>
          <F label="點擊網址（可留空，預設 /）"><input style={inp} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="/races/xxx" /></F>

          <F label="發送頻道">
            <div style={{ display: 'flex', gap: 18, paddingTop: 4 }}>
              <label style={chk}><input type="checkbox" checked={doPush} onChange={(e) => setDoPush(e.target.checked)} /> 推播</label>
              {/* 舊 SMTP email 頻道已自介面移除（2026-08-22 使用者拍板）：無退訂連結/無寄送記錄，
                  有行銷信合規風險；Email 一律改用下方「📧 Email 廣播」（Resend＋退訂＋稽核）。
                  後端 email 頻道保留未動（僅 UI 移除，最小風險）。 */}
              <label style={chk}><input type="checkbox" checked={doMail} onChange={(e) => setDoMail(e.target.checked)} /> 站內信</label>
            </div>
          </F>
          {doMail && (
            <F label="重要程度（站內信）">
              <select style={inp} value={level} onChange={(e) => setLevel(e.target.value as 'normal' | 'important' | 'urgent')}>
                <option value="normal">一般</option>
                <option value="important">重要</option>
                <option value="urgent">緊急</option>
              </select>
            </F>
          )}

          <F label="發送對象">
            <select style={inp} value={targetType} onChange={(e) => setTargetType(e.target.value as TargetType)}>
              <option value="all">全部帳號</option>
              <option value="user">單一帳號</option>
              <option value="race">指定賽事的參賽者</option>
              <option value="group">帳號群組</option>
            </select>
          </F>
          {targetType === 'user' && (
            <F label="帳號編碼或 Email"><input style={inp} value={identifier} onChange={(e) => setIdentifier(e.target.value)} placeholder="#8U2TGUWE 或 someone@example.com" /></F>
          )}
          {targetType === 'race' && (
            <F label="賽事">
              <select style={inp} value={raceId} onChange={(e) => setRaceId(e.target.value)}>
                <option value="">— 選擇賽事 —</option>
                {races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}
              </select>
            </F>
          )}
          {targetType === 'group' && (
            <F label="群組">
              <select style={inp} value={groupId} onChange={(e) => setGroupId(e.target.value)}>
                <option value="">— 選擇群組 —</option>
                {groups.map((g) => <option key={g.id} value={g.id}>{g.name}（{g.member_count} 人）</option>)}
              </select>
            </F>
          )}
        </div>

        <div style={{ display: 'flex', gap: 12, marginTop: 14, alignItems: 'center', flexWrap: 'wrap' }}>
          <button onClick={send} disabled={sending} style={primaryBtn}>{sending ? '發送中…' : '發送'}</button>
          {result && (
            <span style={{ fontSize: 13, color: 'var(--tx-dim)' }}>
              對象 <b style={{ color: 'var(--tx)' }}>{result.recipients}</b> 人　·　推播 <b style={{ color: 'var(--fug)' }}>{result.push_sent}</b>／失敗 <b style={{ color: result.push_failed > 0 ? 'var(--hunt)' : 'var(--tx-dim)' }}>{result.push_failed}</b>　·　Email <b style={{ color: 'var(--fug)' }}>{result.email_sent}</b>／失敗 <b style={{ color: result.email_failed > 0 ? 'var(--hunt)' : 'var(--tx-dim)' }}>{result.email_failed}</b>　·　站內信 <b style={{ color: 'var(--fug)' }}>{result.mail_sent}</b>
            </span>
          )}
        </div>
        {err && <div style={{ color: 'var(--hunt)', marginTop: 10, fontSize: 13 }}>{err}</div>}
      </div>

      <h1 style={{ margin: '32px 0 8px', fontSize: 24, fontWeight: 800 }}>📧 Email 廣播</h1>
      <p style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7, margin: '0 0 16px', maxWidth: 640 }}>
        透過 Resend 寄給全部有 Email 的玩家，或改選「指定帳號/Email」只寄給貼上的清單（已退訂者一律自動排除；查無會員的 email 不會寄出）。內文支援簡單 HTML（例如 <code>&lt;b&gt;</code>／<code>&lt;a href&gt;</code>／<code>&lt;br/&gt;</code>），會直接嵌入信件內文，請自行確認格式正確。每封信結尾會自動附上退訂連結與聯絡資訊。同時間只能有一則廣播在寄送中。
      </p>

      <div style={card}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <F label="主旨"><input style={inp} value={ebSubject} onChange={(e) => setEbSubject(e.target.value)} placeholder="例：8 月份新賽事開跑！" maxLength={200} /></F>
          <F label="內文（支援簡單 HTML）">
            <textarea style={{ ...inp, minHeight: 160, resize: 'vertical', fontFamily: 'monospace', fontSize: 13 }} value={ebBody} onChange={(e) => setEbBody(e.target.value)} placeholder="信件內文…" />
          </F>

          <F label="發送對象">
            <div style={{ display: 'flex', gap: 18, paddingTop: 4 }}>
              <label style={chk}><input type="radio" name="eb-audience" checked={ebAudience === 'all'} onChange={() => setEbAudience('all')} /> 全部玩家</label>
              <label style={chk}><input type="radio" name="eb-audience" checked={ebAudience === 'custom'} onChange={() => setEbAudience('custom')} /> 指定帳號/Email</label>
            </div>
          </F>
          {ebAudience === 'custom' && (
            <F label="收件 Email（每行一個或逗號分隔，上限 500 筆；只會寄給平台會員）">
              <textarea style={{ ...inp, minHeight: 100, resize: 'vertical', fontFamily: 'monospace', fontSize: 13 }} value={ebRecipients} onChange={(e) => setEbRecipients(e.target.value)} placeholder={'someone@example.com\nanother@example.com'} />
            </F>
          )}
        </div>

        <div style={{ display: 'flex', gap: 12, marginTop: 14, alignItems: 'center', flexWrap: 'wrap' }}>
          <button onClick={sendEmailBroadcast} disabled={ebSending} style={primaryBtn}>{ebSending ? '發送中…' : ebAudience === 'all' ? '發送給全部玩家' : '發送給指定對象'}</button>
        </div>
        {ebErr && <div style={{ color: 'var(--hunt)', marginTop: 10, fontSize: 13 }}>{ebErr}</div>}
        {ebResult && (
          <div style={{ marginTop: 12, fontSize: 13, color: 'var(--tx-dim)', borderTop: '1px solid var(--line-2)', paddingTop: 10 }}>
            已送出，寄給 <b style={{ color: 'var(--fug)' }}>{ebResult.total}</b> 位
            {ebResult.audience !== 'all' && (
              <>
                　·　略過查無會員 <b style={{ color: ebResult.not_found.length > 0 ? '#e0a020' : 'var(--tx-dim)' }}>{ebResult.not_found.length}</b> 筆
                　·　已退訂 <b style={{ color: ebResult.unsubscribed_excluded > 0 ? '#e0a020' : 'var(--tx-dim)' }}>{ebResult.unsubscribed_excluded}</b> 筆
                {ebResult.not_found.length > 0 && (
                  <div style={{ marginTop: 6, wordBreak: 'break-all' }}>
                    查無會員：{ebResult.not_found.join('、')}
                  </div>
                )}
              </>
            )}
          </div>
        )}
      </div>

      {ebHistory.length > 0 && (
        <div style={{ ...card, maxWidth: 720 }}>
          <div style={{ fontSize: 13, fontWeight: 700, marginBottom: 10, color: 'var(--tx)' }}>廣播歷史</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {ebHistory.map((b) => {
              const st = ebStatusLabel(b.status)
              return (
                <div key={b.id} style={{ border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 12px', fontSize: 13 }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap' }}>
                    <b style={{ color: 'var(--tx)' }}>{b.subject}</b>
                    <span style={{ color: st.color, fontWeight: 700 }}>{st.text}</span>
                  </div>
                  <div style={{ color: 'var(--tx-dim)', marginTop: 4 }}>
                    {new Date(b.created_at).toLocaleString('zh-TW')}　·　對象 {ebAudienceLabel(b.audience)}　·　進度 {b.sent_count + b.fail_count}／{b.total_count}　·　成功 <b style={{ color: 'var(--fug)' }}>{b.sent_count}</b>／失敗 <b style={{ color: b.fail_count > 0 ? 'var(--hunt)' : 'var(--tx-dim)' }}>{b.fail_count}</b>
                  </div>
                  {b.error_note && <div style={{ color: '#e0a020', marginTop: 4 }}>{b.error_note}</div>}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

function F({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
      <span style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit', boxSizing: 'border-box' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 18px', cursor: 'pointer', fontSize: 14, fontFamily: 'inherit' }
const card: React.CSSProperties = { border: '1px solid var(--line)', borderRadius: 14, padding: 18, marginTop: 14, marginBottom: 4, maxWidth: 560 }
const chk: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 7, fontSize: 14, color: 'var(--tx)', cursor: 'pointer' }
