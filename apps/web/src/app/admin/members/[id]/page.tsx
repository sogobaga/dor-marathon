'use client'

import { useEffect, useState } from 'react'
import { useRouter, useParams } from 'next/navigation'
import Link from 'next/link'
import { adminMembersApi, type MemberDetail } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const GENDER_LABEL: Record<string, string> = { male: '男', female: '女', other: '其他' }

export default function AdminMemberDetailPage() {
  const router = useRouter()
  const params = useParams()
  const id = params.id as string

  const [m, setM] = useState<MemberDetail | null>(null)
  const [err, setErr] = useState('')
  const [savingPerm, setSavingPerm] = useState(false)
  const [expInput, setExpInput] = useState('')
  const [vipInput, setVipInput] = useState('')
  const [kmInput, setKmInput] = useState('5')
  const [kmMsg, setKmMsg] = useState('')
  const [busy, setBusy] = useState(false)

  async function addMileage() {
    const token = getToken()
    if (!token) return
    const km = parseFloat(kmInput)
    if (!(km > 0)) { setErr('里程需大於 0'); return }
    setBusy(true); setErr(''); setKmMsg('')
    try {
      await adminMembersApi.addMileage(token, id, km)
      setKmMsg(`已送出 +${km} km，數秒後由背景處理並發放日常里程 EXP（該會員下次開 App 會跳結算）。`)
      setTimeout(reload, 1500)
    } catch (e: any) { setErr(e?.message || '加里程失敗') } finally { setBusy(false) }
  }

  function reload() {
    const token = getToken()
    if (!token) { router.replace('/admin/login'); return }
    adminMembersApi.get(token, id)
      .then((res) => { setM(res.member); setExpInput(String(res.member.exp)); setVipInput(res.member.vip_expires_at ? res.member.vip_expires_at.slice(0, 10) : '') })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else setErr(e?.message || '載入失敗')
      })
  }
  useEffect(() => { reload() }, [id, router]) // eslint-disable-line react-hooks/exhaustive-deps

  async function saveExp() {
    const token = getToken()
    if (!token) return
    const v = parseInt(expInput, 10)
    if (isNaN(v)) return
    setBusy(true); setErr('')
    try { await adminMembersApi.setExp(token, id, { set: v }); reload() }
    catch (e: any) { setErr(e?.message || '更新失敗') } finally { setBusy(false) }
  }
  async function saveVip(clear = false) {
    const token = getToken()
    if (!token) return
    const val = clear ? '' : (vipInput ? new Date(vipInput + 'T23:59:59').toISOString() : '')
    setBusy(true); setErr('')
    try { await adminMembersApi.setVip(token, id, val); reload() }
    catch (e: any) { setErr(e?.message || '更新失敗') } finally { setBusy(false) }
  }

  async function toggleTeamGroupPerm() {
    const token = getToken()
    if (!token || !m) return
    const next = !m.can_create_team_group
    setSavingPerm(true)
    setErr('')
    try {
      await adminMembersApi.setTeamGroupPermission(token, id, next)
      setM((prev) => (prev ? { ...prev, can_create_team_group: next } : prev))
    } catch (e: any) {
      setErr(e?.message || '更新失敗')
    } finally {
      setSavingPerm(false)
    }
  }

  if (err) return <Centered>{err}</Centered>
  if (!m) return <Centered color="var(--tx-dim)">載入中…</Centered>

  return (
    <div style={{ maxWidth: 640, margin: '0 auto' }}>
      <Link href="/admin/members" style={{ color: 'var(--tx-dim)', fontSize: 13, textDecoration: 'none' }}>
        ← 返回會員列表
      </Link>
      <h1 style={{ margin: '14px 0 6px', fontSize: 24, fontWeight: 800 }}>{m.name || m.handle}</h1>
      <div style={{ color: 'var(--tx-faint)', fontSize: 13, marginBottom: 22 }}>
        @{m.handle} · {m.role} · Lv.{m.level}{m.level_title ? ` ${m.level_title}` : ''} · {m.exp} EXP
        {m.is_vip ? <span style={{ color: 'var(--gold)', fontWeight: 700 }}> · VIP</span> : ' · 一般會員'}
        · 已報名 {m.race_count} 場 · 累積 {m.total_km.toFixed(1)} K
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        <Info label="Email" value={m.email} />
        <Info label="真實名稱" value={m.real_name} />
        <Info label="暱稱" value={m.nickname} />
        <Info label="手機" value={m.phone} />
        <Info label="生日" value={m.birthday} />
        <Info label="性別" value={GENDER_LABEL[m.gender] || ''} />
        <div style={{ gridColumn: '1 / -1' }}>
          <Info label="地址" value={m.address} />
        </div>
      </div>

      {/* 權限設定 */}
      <h2 style={{ margin: '26px 0 10px', fontSize: 16, fontWeight: 800 }}>權限設定</h2>
      <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 16, display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12 }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 14, fontWeight: 600 }}>開放建立跑團分組</div>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
            開啟後，此會員可在有「開放跑團分組申請」的競賽中，於前台自建跑團分組。
          </div>
        </div>
        <button
          onClick={toggleTeamGroupPerm}
          disabled={savingPerm}
          style={{
            flexShrink: 0, borderRadius: 999, cursor: savingPerm ? 'default' : 'pointer',
            padding: '8px 16px', fontSize: 13, fontWeight: 700,
            background: m.can_create_team_group ? 'var(--fug)' : 'var(--bg-2)',
            color: m.can_create_team_group ? '#05140e' : 'var(--tx-dim)',
            border: m.can_create_team_group ? '1px solid var(--fug)' : '1px solid var(--line-2)',
            opacity: savingPerm ? 0.6 : 1,
          }}
        >
          {savingPerm ? '更新中…' : m.can_create_team_group ? '已開放 ✓' : '未開放'}
        </button>
      </div>

      {/* 等級 / EXP */}
      <h2 style={{ margin: '26px 0 10px', fontSize: 16, fontWeight: 800 }}>等級 / 經驗值</h2>
      <div style={ctrlCard}>
        <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginBottom: 8 }}>目前 Lv.{m.level}{m.level_title ? ` ${m.level_title}` : ''} · {m.exp} EXP</div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>設定 EXP</span>
          <input style={ctrlInp} type="number" value={expInput} onChange={(e) => setExpInput(e.target.value)} />
          <button onClick={saveExp} disabled={busy} style={primaryBtnSm}>儲存</button>
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6 }}>（EXP 之後會由賽事結算自動累加，這裡供測試/營運手動調整）</div>
      </div>

      {/* 加里程（測試） */}
      <h2 style={{ margin: '20px 0 10px', fontSize: 16, fontWeight: 800 }}>加里程（測試模擬跑步）</h2>
      <div style={ctrlCard}>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>里程 (km)</span>
          <input style={ctrlInp} type="number" value={kmInput} onChange={(e) => setKmInput(e.target.value)} />
          <button onClick={addMileage} disabled={busy} style={primaryBtnSm}>＋ 加里程</button>
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6 }}>模擬一筆跑步活動：背景寫入並依「日常里程」規則發 EXP（每整公里 × 每公里EXP），該會員下次開 App 會跳出里程結算演出。</div>
        {kmMsg && <div style={{ fontSize: 12, color: 'var(--fug)', marginTop: 6 }}>{kmMsg}</div>}
      </div>

      {/* VIP */}
      <h2 style={{ margin: '20px 0 10px', fontSize: 16, fontWeight: 800 }}>VIP 會員</h2>
      <div style={ctrlCard}>
        <div style={{ fontSize: 13, marginBottom: 8 }}>
          {m.is_vip
            ? <span style={{ color: 'var(--gold)', fontWeight: 700 }}>VIP 有效，至 {m.vip_expires_at?.slice(0, 10)}</span>
            : <span style={{ color: 'var(--tx-dim)' }}>非 VIP（{m.vip_expires_at ? `已於 ${m.vip_expires_at.slice(0, 10)} 到期` : '未設定'}）</span>}
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>VIP 到期日</span>
          <input style={ctrlInp} type="date" value={vipInput} onChange={(e) => setVipInput(e.target.value)} />
          <button onClick={() => saveVip(false)} disabled={busy} style={primaryBtnSm}>設定</button>
          <button onClick={() => saveVip(true)} disabled={busy} style={ghostBtnSm}>清除</button>
        </div>
      </div>

      {/* 選手分級（依匯入數據；僅後台顯示） */}
      <h2 style={{ margin: '26px 0 10px', fontSize: 16, fontWeight: 800 }}>選手分級<span style={{ fontSize: 11, color: 'var(--tx-faint)', fontWeight: 400, marginLeft: 8 }}>（依數據自動計算，前台不顯示）</span></h2>
      <div style={ctrlCard}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10 }}>
          <span style={{ fontSize: 18, fontWeight: 800, color: 'var(--fug)' }}>{m.athlete.level || '—'}</span>
          <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>綜合分數 {m.athlete.score}</span>
        </div>
        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6 }}>
          <span>跑量 {m.athlete.volume_km.toFixed(1)} K（{m.athlete.activities} 次）</span>
          <span>配速 {m.athlete.pace_s ? `${Math.floor(m.athlete.pace_s / 60)}:${String(m.athlete.pace_s % 60).padStart(2, '0')}/km` : '—'}</span>
          <span>平均每次 {m.athlete.avg_dist_km.toFixed(1)} K</span>
          <span>最長 {m.athlete.longest_km.toFixed(1)} K</span>
          <span>月均 {m.athlete.monthly_freq.toFixed(1)} 次</span>
        </div>
      </div>
    </div>
  )
}

const ctrlCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 16 }
const ctrlInp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: 150 }
const primaryBtnSm: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 8, padding: '7px 14px', cursor: 'pointer', fontSize: 13 }
const ghostBtnSm: React.CSSProperties = { background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 13 }

function Info({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: 14 }}>
      <div style={{ fontSize: 11, letterSpacing: '.1em', color: 'var(--tx-faint)', textTransform: 'uppercase', marginBottom: 6 }}>
        {label}
      </div>
      <div style={{ fontSize: 15, color: value ? 'var(--tx)' : 'var(--tx-faint)' }}>{value || '未填'}</div>
    </div>
  )
}

function Centered({ children, color = 'var(--hunt)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '60px 0', color }}>{children}</div>
}
