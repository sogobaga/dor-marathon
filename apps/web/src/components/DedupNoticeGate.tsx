'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { profileApi, type DedupSide } from '@/lib/api'
import { getUserToken, useUser } from '@/lib/userAuth'

// 首次偵測到「同一趟跑步 GPS + Strava 兩筆重複」時的全域彈窗：讓玩家選擇要以哪個來源為準。
// 仿 MileageExpGate：登入後輪詢 /profile/dedup-notice；有 notice 才顯示。

function fmtDate(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export default function DedupNoticeGate() {
  const user = useUser()
  const token = getUserToken() || undefined
  const { data, mutate } = useSWR(
    user && token ? ['dedup-notice', user.id] : null,
    () => profileApi.dedupNotice(token!),
    { refreshInterval: 25000 },
  )
  const notice = data?.notice || null
  const [choice, setChoice] = useState<'gps' | 'strava' | null>(null)
  const [remember, setRemember] = useState(true)
  const [busy, setBusy] = useState(false)

  if (!notice) return null
  const pick = choice ?? notice.current_preference

  const submit = async () => {
    if (!token) return
    setBusy(true)
    try { await profileApi.dedupResolve(token, pick, remember); await mutate() } catch { /* keep open */ } finally { setBusy(false) }
  }

  const OptionCard = ({ src, label, side }: { src: 'gps' | 'strava'; label: string; side: DedupSide }) => {
    const on = pick === src
    return (
      <button onClick={() => setChoice(src)} style={{
        flex: 1, textAlign: 'left', cursor: 'pointer',
        background: on ? 'rgba(70,227,160,.12)' : 'var(--bg-2)',
        border: `2px solid ${on ? 'var(--fug)' : 'var(--line-2)'}`,
        borderRadius: 14, padding: '12px 12px',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{ width: 15, height: 15, borderRadius: '50%', border: `2px solid ${on ? 'var(--fug)' : 'var(--tx-faint)'}`, background: on ? 'var(--fug)' : 'transparent', flexShrink: 0 }} />
          <span style={{ fontSize: 13.5, fontWeight: 800, color: 'var(--tx)' }}>{label}</span>
        </div>
        <div style={{ fontSize: 20, fontWeight: 900, color: 'var(--tx)', marginTop: 8 }}>{side.distance_km.toFixed(2)} <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>km</span></div>
        <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 2 }}>{Math.round(side.duration_s / 60)} 分 · {fmtDate(side.recorded_at)}</div>
      </button>
    )
  }

  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 3200, background: 'rgba(0,0,0,.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 22 }}>
      <div style={{ width: '100%', maxWidth: 380, background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 18, padding: '20px 18px', boxShadow: '0 16px 48px rgba(0,0,0,.5)' }}>
        <div style={{ fontSize: 17, fontWeight: 900, color: 'var(--tx)' }}>發現重複的運動數據</div>
        <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7, marginTop: 8 }}>
          偵測到同一趟跑步同時有「GPS 跑步追蹤」與「Strava」兩筆高度類似的紀錄。為避免里程重複計入賽事，請選擇以哪一個為準（另一個將不計入）：
        </div>
        <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
          <OptionCard src="gps" label="GPS 跑步追蹤" side={notice.gps} />
          <OptionCard src="strava" label="Strava" side={notice.strava} />
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 14, fontSize: 12.5, color: 'var(--tx-dim)', cursor: 'pointer' }}>
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} style={{ width: 16, height: 16 }} />
          以後都以此來源為優先（可到「個人資料 → 運動數據」再變更）
        </label>
        <button onClick={submit} disabled={busy} style={{ width: '100%', marginTop: 16, background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 12, padding: '12px', fontSize: 15, cursor: busy ? 'default' : 'pointer', opacity: busy ? 0.6 : 1 }}>
          {busy ? '處理中…' : `以「${pick === 'strava' ? 'Strava' : 'GPS 跑步追蹤'}」為準`}
        </button>
      </div>
    </div>
  )
}
