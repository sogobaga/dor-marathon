'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminOverviewApi, adminMetricsApi, type AdminOverview, type DataSourceMetrics } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const STATUS: Record<string, { label: string; color: string }> = {
  upcoming_reg: { label: '未開放報名', color: '#9aa0a6' },
  registering: { label: '報名中', color: '#46E3A0' },
  reg_closed: { label: '報名結束', color: '#FFC24B' },
  starting_soon: { label: '即將開始', color: '#FFC24B' },
  racing: { label: '進行中', color: '#FF4B5C' },
  paused: { label: '暫停報名', color: '#9aa0a6' },
  suspended: { label: '賽事中止', color: '#9aa0a6' },
}
function fmtDate(iso: string) {
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())}`
}

export default function AdminOverviewPage() {
  const router = useRouter()
  const [data, setData] = useState<AdminOverview | null>(null)
  const [src, setSrc] = useState<DataSourceMetrics | null>(null)
  const [err, setErr] = useState('')
  const [open, setOpen] = useState<Record<string, boolean>>({})

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    adminOverviewApi.get(t).then((d) => { setData(d); setErr('') }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else setErr(e?.message || '載入失敗')
    })
    adminMetricsApi.dataSource(t).then(setSrc).catch(() => {})
  }, [router])
  useEffect(() => { load(); const id = setInterval(load, 20000); return () => clearInterval(id) }, [load])

  return (
    <div style={{ padding: 20, maxWidth: 900, margin: '0 auto' }}>
      <h1 style={{ fontSize: 20, fontWeight: 900, color: 'var(--tx)', marginBottom: 4 }}>數據總覽</h1>
      <div style={{ fontSize: 12.5, color: 'var(--tx-faint)', marginBottom: 16 }}>近半年的賽事狀態與即時在跑名單（每 20 秒自動更新）</div>
      {err && <div style={{ color: 'var(--hunt)', marginBottom: 12, fontSize: 13 }}>{err}</div>}

      <div style={{ display: 'flex', gap: 12, marginBottom: 18 }}>
        <div style={{ flex: 1, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '14px 16px' }}>
          <div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>🏃 全站目前在跑</div>
          <div style={{ fontSize: 30, fontWeight: 900, color: 'var(--fug)' }}>{data?.tracking_total ?? '—'} <span style={{ fontSize: 14, color: 'var(--tx-dim)' }}>人</span></div>
        </div>
        <div style={{ flex: 1, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '14px 16px' }}>
          <div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>📅 近半年賽事</div>
          <div style={{ fontSize: 30, fontWeight: 900, color: 'var(--tx)' }}>{data?.races.length ?? '—'} <span style={{ fontSize: 14, color: 'var(--tx-dim)' }}>場</span></div>
        </div>
      </div>

      {/* 運動資料來源分布（評估直連手錶 / Terra 成本） */}
      <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '14px 16px', marginBottom: 18 }}>
        <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', marginBottom: 2 }}>⌚ 運動資料來源</div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 12, lineHeight: 1.6 }}>
          「需直連手錶」＝有 Garmin/COROS 活動、但完全沒有 Strava 的用戶（＝ Strava 覆蓋不到、真正需要 Terra 直連的人數）。用來評估是否值得直連手錶／Terra 成本。
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10 }}>
          {[
            ['需直連手錶', src?.need_direct_watch, 'var(--hunt)'],
            ['手錶(G/C)用戶', src?.watch_users, 'var(--gold)'],
            ['Garmin', src?.garmin_users, 'var(--tx)'],
            ['COROS', src?.coros_users, 'var(--tx)'],
            ['Strava', src?.strava_users, '#fc4c02'],
            ['App GPS', src?.gps_users, 'var(--fug)'],
          ].map(([label, val, color]) => (
            <div key={label as string} style={{ flex: '1 1 90px', minWidth: 90, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 12px' }}>
              <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', whiteSpace: 'nowrap' }}>{label as string}</div>
              <div style={{ fontSize: 22, fontWeight: 900, color: color as string }}>{(val as number | undefined) ?? '—'}</div>
            </div>
          ))}
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {data && data.races.length === 0 && <div style={{ color: 'var(--tx-faint)', fontSize: 13 }}>近半年沒有即將／進行中的賽事</div>}
        {data?.races.map((r) => {
          const st = STATUS[r.display_status] || { label: r.display_status, color: '#9aa0a6' }
          const isOpen = !!open[r.id]
          return (
            <div key={r.id} style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '13px 15px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)', flex: 1, minWidth: 140 }}>{r.title}</span>
                <span style={{ fontSize: 11.5, fontWeight: 800, color: st.color, border: `1px solid ${st.color}`, borderRadius: 999, padding: '2px 10px' }}>{st.label}</span>
              </div>
              <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 4 }}>{fmtDate(r.start_date)} – {fmtDate(r.end_date)}</div>
              <div style={{ display: 'flex', gap: 20, marginTop: 10, alignItems: 'baseline' }}>
                <div><span style={{ fontSize: 18, fontWeight: 900, color: 'var(--tx)' }}>{r.registrations}</span> <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>報名</span></div>
                <button onClick={() => setOpen((o) => ({ ...o, [r.id]: !o[r.id] }))} disabled={r.tracking_count === 0}
                  style={{ background: 'none', border: 'none', cursor: r.tracking_count ? 'pointer' : 'default', padding: 0, display: 'flex', alignItems: 'baseline', gap: 5 }}>
                  <span style={{ fontSize: 18, fontWeight: 900, color: r.tracking_count ? 'var(--fug)' : 'var(--tx-dim)' }}>{r.tracking_count}</span>
                  <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>在跑{r.tracking_count ? (isOpen ? ' ▲' : ' ▼') : ''}</span>
                </button>
              </div>
              {isOpen && r.tracking_names.length > 0 && (
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 10 }}>
                  {r.tracking_names.map((n, i) => (
                    <span key={i} style={{ fontSize: 12, background: 'rgba(70,227,160,.12)', border: '1px solid rgba(70,227,160,.4)', color: 'var(--fug)', borderRadius: 999, padding: '3px 10px' }}>🏃 {n}</span>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
