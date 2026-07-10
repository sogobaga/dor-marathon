'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminOverviewApi, adminMetricsApi, type AdminOverview, type DataSourceMetrics, type VipAnalytics } from '@/lib/api'
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
  const [va, setVa] = useState<VipAnalytics | null>(null)
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
    adminMetricsApi.vipAnalytics(t).then(setVa).catch(() => {})
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

      {/* VIP 訂閱分析 */}
      <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '14px 16px', marginBottom: 18 }}>
        <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', marginBottom: 12 }}>👑 VIP 訂閱分析</div>

        {/* 一般 vs VIP */}
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 14 }}>
          <div style={{ flex: '1 1 140px', minWidth: 140, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 12px' }}>
            <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>VIP 會員</div>
            <div style={{ fontSize: 22, fontWeight: 900, color: 'var(--gold)' }}>
              {va?.vip ?? '—'} <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
                ({va && va.total > 0 ? Math.round((va.vip / va.total) * 100) : 0}%)
              </span>
            </div>
          </div>
          <div style={{ flex: '1 1 140px', minWidth: 140, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 12px' }}>
            <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>一般會員</div>
            <div style={{ fontSize: 22, fontWeight: 900, color: 'var(--tx)' }}>
              {va?.general ?? '—'} <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
                ({va && va.total > 0 ? Math.round((va.general / va.total) * 100) : 0}%)
              </span>
            </div>
          </div>
        </div>

        {/* VIP 方案分布 */}
        <div style={{ fontSize: 11.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 6 }}>VIP 方案分布</div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 14 }}>
          {[
            ['試用', va?.vip_by_plan.trial],
            ['月繳', va?.vip_by_plan.monthly],
            ['年繳', va?.vip_by_plan.annual],
          ].map(([plabel, pval]) => {
            const v = (pval as number | undefined) ?? 0
            const vipTotal = va?.vip ?? 0
            const pct = vipTotal > 0 ? Math.round((v / vipTotal) * 100) : 0
            return (
              <div key={plabel as string} style={{ flex: '1 1 100px', minWidth: 100, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 12px' }}>
                <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>{plabel as string}</div>
                <div style={{ fontSize: 18, fontWeight: 900, color: 'var(--tx)' }}>
                  {va ? v : '—'} <span style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{va ? `(${pct}%)` : ''}</span>
                </div>
              </div>
            )
          })}
        </div>

        {/* 上月未續訂名單 */}
        <div style={{ fontSize: 11.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 6 }}>上月未續訂名單</div>
        {!va && <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 14 }}>載入中…</div>}
        {va && va.last_month_non_renewers.length === 0 && (
          <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginBottom: 14 }}>上月無未續訂</div>
        )}
        {va && va.last_month_non_renewers.length > 0 && (
          <div style={{ maxHeight: 220, overflowY: 'auto', border: '1px solid var(--line)', borderRadius: 10, marginBottom: 14 }}>
            {va.last_month_non_renewers.map((r) => (
              <div key={r.user_id} style={{ display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center', padding: '8px 12px', borderBottom: '1px solid var(--line)', fontSize: 12.5 }}>
                <div style={{ flex: '1 1 100px', fontWeight: 700, color: 'var(--tx)' }}>{r.name || '—'}</div>
                <div style={{ flex: '2 1 160px', color: 'var(--tx-dim)', wordBreak: 'break-all' }}>{r.email}</div>
                <div style={{ flex: '0 1 60px', color: 'var(--gold)' }}>{r.plan}</div>
                <div style={{ flex: '0 1 90px', color: 'var(--tx-faint)' }}>{fmtDate(r.expired_at)}</div>
              </div>
            ))}
          </div>
        )}

        {/* 成長 / 未續訂趨勢 */}
        <TrendChart data={va?.growth ?? []} color="var(--fug)" label="會員成長趨勢（近 12 月）" />
        <TrendChart data={va?.churn ?? []} color="var(--hunt)" label="每月未續訂趨勢（近 12 月）" />
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

// 手刻長條趨勢圖（無圖表庫依賴）：x=月份、y=數值，標月份與數值，全 0 時不除以 0。
function TrendChart({ data, color, label }: { data: { month: string; count: number }[]; color: string; label: string }) {
  const w = 640
  const h = 150
  const padL = 8
  const padR = 8
  const padT = 18
  const padB = 26
  const innerW = w - padL - padR
  const innerH = h - padT - padB
  const n = data.length
  const max = Math.max(1, ...data.map((d) => d.count))
  const barW = n > 0 ? innerW / n : innerW

  return (
    <div style={{ marginTop: 14 }}>
      <div style={{ fontSize: 11.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 6 }}>{label}</div>
      {n === 0 ? (
        <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>無資料</div>
      ) : (
        <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} style={{ display: 'block', overflow: 'visible' }}>
          <line x1={padL} y1={h - padB} x2={w - padR} y2={h - padB} stroke="var(--line)" strokeWidth={1} />
          {data.map((d, i) => {
            const barH = (d.count / max) * innerH
            const x = padL + i * barW + barW * 0.18
            const bw = Math.max(1, barW * 0.64)
            const y = h - padB - barH
            const monthLabel = d.month.length >= 7 ? d.month.slice(5, 7) : d.month
            return (
              <g key={`${d.month}-${i}`}>
                <rect x={x} y={y} width={bw} height={barH} fill={color} rx={2} opacity={0.85}>
                  <title>{d.month}: {d.count}</title>
                </rect>
                {d.count > 0 && (
                  <text x={x + bw / 2} y={y - 4} textAnchor="middle" fontSize="9" fill="var(--tx-dim)">{d.count}</text>
                )}
                <text x={x + bw / 2} y={h - padB + 13} textAnchor="middle" fontSize="8.5" fill="var(--tx-faint)">{monthLabel}</text>
              </g>
            )
          })}
        </svg>
      )}
    </div>
  )
}
