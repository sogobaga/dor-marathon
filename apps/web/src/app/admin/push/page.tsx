'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminPushApi, adminPushGroupsApi, adminRacesApi, type PushGroup, type Race, type AdminPushBroadcastResult } from '@/lib/api'
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

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch(() => {})
    adminPushGroupsApi.list(t).then((r) => setGroups(r.groups)).catch(() => {})
  }, [router])

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
              <label style={chk}><input type="checkbox" checked={doEmail} onChange={(e) => setDoEmail(e.target.checked)} /> Email</label>
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
