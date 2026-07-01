'use client'

import useSWR from 'swr'
import { useState, useEffect } from 'react'
import { racesApi, METRIC_BY_KEY, type Race, type TaskProgress } from '@/lib/api'
import { getUserToken } from '@/lib/userAuth'
import { renderCertificate, downloadDataURL } from '@/lib/certificate'
import ExpSettlementModal from './ExpSettlementModal'
import { BrochureBody } from './BrochureScreen'
import { RankingBody } from './RaceRankingScreen'
import { ExploreBody } from './ExploreBody'
import ScrollArea from './ScrollArea'

const STATUS_LABEL: Record<string, string> = {
  registering: '報名中', upcoming_reg: '即將報名', reg_closed: '報名結束',
  starting_soon: '即將開始', racing: '進行中', ended: '已結束',
  paused: '暫停報名', suspended: '賽事中止',
}

function fmt(iso?: string | null) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function paceFmt(sec: number) {
  return `${Math.floor(sec / 60)}:${String(Math.round(sec % 60)).padStart(2, '0')}`
}

type Tab = 'brochure' | 'progress' | 'explore' | 'rank'

export default function RaceDetailScreen({
  race, onBack, onRegister, initialTab,
}: {
  race: Race
  onBack: () => void
  onRegister?: (race: Race) => void
  initialTab?: Tab
}) {
  const token = getUserToken() || undefined
  const { data: detailData } = useSWR(['detail', race.id], () => racesApi.detail(race.id, token))
  const { data: standings } = useSWR(
    race.event_mode === 'competition' ? ['standings', race.id] : null,
    () => racesApi.standings(race.id, token),
  )
  const detail = detailData?.race
  const registration = detailData?.registration

  // 完賽證明：賽事結束後、已報名、已登入才查
  const ended = race.display_status === 'ended'
  const { data: certData } = useSWR(
    ended && registration && token ? ['cert', race.id] : null,
    () => racesApi.certificate(race.id, token!),
  )
  const cert = certData?.certificate
  const [certImg, setCertImg] = useState('')
  const [certZoom, setCertZoom] = useState(false)
  useEffect(() => {
    if (cert?.completed) renderCertificate(cert).then(setCertImg).catch(() => {})
    else setCertImg('')
  }, [cert])

  // 本場 EXP 結算明細（賽事結束 + 已報名）
  const { data: bdData } = useSWR(
    ended && registration && token ? ['exp-bd', race.id] : null,
    () => racesApi.expBreakdown(race.id, token!),
  )
  const breakdown = bdData?.breakdown
  const [showExp, setShowExp] = useState(false)
  useEffect(() => {
    if (!breakdown || breakdown.gained <= 0) return
    const key = `dor_exp_seen_${race.id}`
    if (typeof window !== 'undefined' && !localStorage.getItem(key)) {
      localStorage.setItem(key, '1')
      setShowExp(true) // 完賽後首次自動演出
    }
  }, [breakdown, race.id])

  const started = race.display_status === 'racing' || race.display_status === 'ended'
  // 競賽/分組對抗才有「當天揭曉分組＋分組戰報」；一般模式分組直接顯示
  const battleMode = race.event_mode === 'competition' || race.event_mode === 'faction_battle'
  const defaultTab: Tab = race.display_status === 'racing' ? 'progress' : race.display_status === 'ended' ? 'rank' : 'brochure'
  const [tab, setTab] = useState<Tab>(initialTab ?? defaultTab)
  // 是否有打卡點任務 → 決定是否顯示「探索」頁籤（與 ProgressBody 共用同一 SWR key，去重）
  const { data: progData } = useSWR(['progress', race.id], () => racesApi.progress(race.id, token))
  const hasCheckpoints = (progData?.progress.tasks ?? []).some((t) => t.metric_type === 'checkpoint')

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: '52px 22px 10px', flexShrink: 0 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <h1 style={{ margin: '10px 0 0', fontSize: 22, fontWeight: 800, color: 'var(--tx)' }}>{race.title}</h1>
      </header>

      <ScrollArea padding="4px 18px 30px">
        {/* 賽事 Banner */}
        {(detail?.hero_image_url || race.hero_image_url) && (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={detail?.hero_image_url || race.hero_image_url}
            alt=""
            style={{ width: '100%', display: 'block', margin: '0 auto 14px', borderRadius: 12, maxHeight: 220, objectFit: 'contain', objectPosition: 'center' }}
          />
        )}
        {/* 賽事資訊 Dashboard */}
        <div style={dashCard}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <span style={statusBadge}>{STATUS_LABEL[race.display_status] ?? race.display_status}</span>
            <span style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
              {race.event_mode === 'competition' ? '競賽' : race.event_mode === 'faction_battle' ? '分組對抗' : '一般'}
            </span>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr', gap: 4, marginTop: 10, fontSize: 12 }}>
            <Row k="報名期間" v={`${fmt(race.registration_start)} – ${fmt(race.registration_end)}`} />
            <Row k="賽事期間" v={`${fmt(race.start_date)} – ${fmt(race.end_date)}`} />
          </div>

          {/* 我的分組（競賽/分組對抗：當天揭曉＋戰報；一般：直接顯示分組） */}
          {registration && (battleMode || registration.group_name) && (
            <div style={{ marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>我的分組</div>
              {battleMode ? (
                <>
                  <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>
                    {standings?.my_group?.group_name || (registration.group_revealed ? '已加入分組' : '分組賽事當天公布')}
                  </div>
                  {started ? (
                    standings?.my_group && (
                      <div style={{ display: 'flex', gap: 16, marginTop: 6, fontSize: 12, color: 'var(--tx-dim)' }}>
                        <span>累積榜 第 <b style={{ color: 'var(--fug)' }}>{standings.my_group.cumulative_rank}</b> 名</span>
                        <span>{standings.my_group.total_km.toFixed(1)} K</span>
                      </div>
                    )
                  ) : (
                    <div style={{ fontSize: 12, color: 'var(--tx-faint)', marginTop: 6 }}>賽事開始後顯示分組戰報</div>
                  )}
                </>
              ) : (
                <div style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>{registration.group_name}</div>
              )}
            </div>
          )}

          {/* 報名按鈕 / 已報名（修正：不再多一層） */}
          <div style={{ marginTop: 14 }}>
            {registration ? (
              <div style={registeredBox}>✓ 你已報名此賽事{registration.status === 'pending' ? '（待繳費）' : registration.status === 'paid' ? '（已完成）' : ''}</div>
            ) : detail?.can_register && onRegister ? (
              <button onClick={() => onRegister(race)} style={registerBtn}>立即報名</button>
            ) : null}
          </div>

          {/* 完賽證明（賽事結束後，完賽者：預覽縮圖→點擊放大→下載） */}
          {ended && cert?.completed && (
            <div style={{ marginTop: 14 }}>
              <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 8 }}>完賽證明</div>
              {certImg ? (
                <>
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={certImg}
                    alt="完賽證明"
                    onClick={() => setCertZoom(true)}
                    style={{ width: '100%', borderRadius: 12, border: '1px solid var(--line-2)', cursor: 'zoom-in', display: 'block' }}
                  />
                  <button
                    onClick={() => downloadDataURL(certImg, `完賽證明_${cert.race_title}.png`)}
                    style={certBtn}
                  >🏅 下載完賽證明</button>
                </>
              ) : (
                <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '8px 0' }}>產生證明中…</div>
              )}
            </div>
          )}
          {ended && cert && !cert.completed && registration && (
            <div style={{ marginTop: 10, fontSize: 12, color: 'var(--tx-faint)', textAlign: 'center' }}>本場未達完賽標準，無完賽證明</div>
          )}

          {/* 本場 EXP 結算（重看） */}
          {ended && breakdown && breakdown.gained > 0 && (
            <button onClick={() => setShowExp(true)} style={expBtn}>🎮 查看本場結算（+{breakdown.gained} EXP）</button>
          )}
        </div>

        {/* 頁籤 */}
        <div style={{ display: 'flex', gap: 6, margin: '16px 0 14px', borderBottom: '1px solid var(--line)' }}>
          {(([['brochure', '簡章'], ['progress', '進度'], ...(hasCheckpoints ? [['explore', '探索']] : []), ['rank', '排名']]) as [Tab, string][]).map(([v, label]) => (
            <button key={v} onClick={() => setTab(v)} style={{
              padding: '8px 14px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 14,
              color: tab === v ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: tab === v ? 700 : 400,
              borderBottom: tab === v ? '2px solid var(--fug)' : '2px solid transparent',
            }}>{label}</button>
          ))}
        </div>

        {!started && (tab === 'progress' || tab === 'rank') && (
          <div style={notStartedHint}>賽事尚未開始，敬請期待。</div>
        )}

        {tab === 'brochure' && (detail ? <BrochureBody detail={detail} /> : <Hint>載入中…</Hint>)}
        {tab === 'progress' && <ProgressBody race={race} />}
        {tab === 'explore' && <ExploreBody race={race} />}
        {tab === 'rank' && <RankingBody race={race} />}
      </ScrollArea>

      {/* 完賽證明全屏檢視 */}
      {certZoom && certImg && cert && (
        <div onClick={() => setCertZoom(false)} style={lightbox}>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={certImg} alt="完賽證明" style={{ maxWidth: '96%', maxHeight: '82%', borderRadius: 8, boxShadow: '0 8px 40px rgba(0,0,0,.6)' }} />
          <button onClick={(e) => { e.stopPropagation(); downloadDataURL(certImg, `完賽證明_${cert.race_title}.png`) }} style={lightboxDl}>🏅 下載完賽證明</button>
          <div onClick={() => setCertZoom(false)} style={{ position: 'absolute', top: 14, right: 20, color: '#fff', fontSize: 30, cursor: 'pointer', lineHeight: 1 }}>✕</div>
        </div>
      )}

      {/* 本場 EXP 結算演出 */}
      {showExp && breakdown && breakdown.gained > 0 && (
        <ExpSettlementModal breakdown={breakdown} subtitle={race.title} onClose={() => setShowExp(false)} />
      )}
    </div>
  )
}

function ProgressBody({ race }: { race: Race }) {
  const token = getUserToken() || undefined
  const { data, isLoading } = useSWR(['progress', race.id], () => racesApi.progress(race.id, token), { refreshInterval: 30000 })
  const prog = data?.progress
  if (isLoading || !prog) return <Hint>載入中…</Hint>

  const tasks = prog.tasks ?? []
  const my = prog.my ?? { total_km: 0, activities: 0, ascent_m: 0 }
  const groupsBy: { label: string; tasks: TaskProgress[] }[] = []
  for (const label of ['賽事集體', '所有分組共同（團體）', '本組團體', '所有分組共同（個人）', '本組個人']) {
    const ts = tasks.filter((t) => t.scope_label === label)
    if (ts.length) groupsBy.push({ label, tasks: ts })
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {/* 我的統計 */}
      <div style={{ display: 'flex', gap: 10 }}>
        <Stat label="我的里程" value={`${my.total_km.toFixed(1)} K`} />
        <Stat label="活動" value={`${my.activities}`} />
        <Stat label="爬升" value={`${Math.round(my.ascent_m)} m`} />
      </div>

      {tasks.length === 0 && <Hint>此賽事尚未設定任務目標</Hint>}

      {groupsBy.map((g) => (
        <div key={g.label}>
          <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)', marginBottom: 8 }}>{g.label}任務</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {g.tasks.map((t, i) => <TaskRow key={t.id ?? i} t={t} />)}
          </div>
        </div>
      ))}
    </div>
  )
}

function TaskRow({ t }: { t: TaskProgress }) {
  const m = METRIC_BY_KEY[t.metric_type]
  if (m?.kind === 'checkpoint') {
    const cps = t.checkpoints ?? []
    const collected = cps.filter((c) => c.collected).length
    return (
      <div style={{ background: 'var(--bg-1)', border: `1px solid ${t.done ? 'var(--fug)' : 'var(--line)'}`, borderRadius: 12, padding: '11px 13px' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, alignItems: 'baseline' }}>
          <span style={{ fontSize: 13.5, fontWeight: 700 }}>{t.done ? '✓ ' : ''}{t.title || m?.label}</span>
          <span style={{ fontSize: 12, color: t.done ? 'var(--fug)' : 'var(--tx-dim)', whiteSpace: 'nowrap' }}>集章 {collected}/{cps.length}</span>
        </div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>指定地點打卡</div>
        {cps.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8 }}>
            {cps.map((c, i) => (
              <span key={c.id ?? i} style={{
                fontSize: 11.5, padding: '3px 9px', borderRadius: 999,
                background: c.collected ? 'rgba(70,227,160,.15)' : c.pending ? 'rgba(245,194,66,.15)' : 'var(--bg-2)',
                border: `1px solid ${c.collected ? 'var(--fug)' : c.pending ? 'var(--gold)' : 'var(--line-2)'}`,
                color: c.collected ? 'var(--fug)' : c.pending ? 'var(--gold)' : 'var(--tx-dim)',
              }}>{c.collected ? '✓ ' : c.pending ? '⏳ ' : '○ '}{c.title || `點 ${i + 1}`}</span>
            ))}
          </div>
        )}
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 8 }}>到各點半徑內以「開始跑步」打卡，集滿即完成</div>
      </div>
    )
  }
  const isRange = m?.kind === 'range'
  let targetText = ''
  let pct = 0
  if (isRange) {
    if (t.metric_type === 'avg_pace_range') targetText = `${paceFmt(t.range_lo ?? 0)}–${paceFmt(t.range_hi ?? 0)} /km`
    else targetText = `${t.range_lo ?? '—'}–${t.range_hi ?? '—'} ${m?.unit ?? ''}`
  } else {
    targetText = `≥ ${t.target_value ?? '—'} ${m?.unit ?? ''}`
    const target = t.target_value ?? 0
    pct = target > 0 ? Math.min(100, (t.current / target) * 100) : 0
  }
  return (
    <div style={{ background: 'var(--bg-1)', border: `1px solid ${t.done ? 'var(--fug)' : 'var(--line)'}`, borderRadius: 12, padding: '11px 13px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, alignItems: 'baseline' }}>
        <span style={{ fontSize: 13.5, fontWeight: 700 }}>{t.done ? '✓ ' : ''}{t.title || m?.label}</span>
        <span style={{ fontSize: 12, color: t.done ? 'var(--fug)' : 'var(--tx-dim)', whiteSpace: 'nowrap' }}>{targetText}</span>
      </div>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>{m?.label}</div>
      {isRange ? (
        <div style={{ fontSize: 12, marginTop: 6, color: t.done ? 'var(--fug)' : 'var(--tx-dim)' }}>
          {t.done ? `已達標 · 符合 ${t.qualify_count} 筆` : '尚未有符合區間的活動'}
        </div>
      ) : (
        <>
          <div style={{ height: 6, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 7 }}>
            <div style={{ height: '100%', width: `${pct}%`, background: t.done ? 'var(--fug)' : 'var(--gold)', borderRadius: 999 }} />
          </div>
          <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 4, textAlign: 'right' }}>
            {t.current} / {t.target_value ?? '—'} {m?.unit ?? ''}
          </div>
        </>
      )}
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ flex: 1, background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '10px 12px', textAlign: 'center' }}>
      <div style={{ fontSize: 16, fontWeight: 800, color: 'var(--tx)' }}>{value}</div>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 2 }}>{label}</div>
    </div>
  )
}
function Row({ k, v }: { k: string; v: string }) {
  return (
    <div style={{ display: 'flex', gap: 8 }}>
      <span style={{ color: 'var(--tx-faint)', width: 56, flexShrink: 0 }}>{k}</span>
      <span style={{ color: 'var(--tx-dim)' }}>{v}</span>
    </div>
  )
}
function Hint({ children, color = 'var(--tx-dim)' }: { children: React.ReactNode; color?: string }) {
  return <div style={{ textAlign: 'center', padding: '40px 20px', fontSize: 13.5, color }}>{children}</div>
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0 }
const dashCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, padding: 16 }
const statusBadge: React.CSSProperties = { fontSize: 12, fontWeight: 700, color: 'var(--fug)', background: 'rgba(45,212,150,.1)', border: '1px solid var(--fug)', borderRadius: 999, padding: '2px 10px' }
const registerBtn: React.CSSProperties = { background: 'var(--fug)', color: '#05140e', fontWeight: 700, border: 'none', borderRadius: 12, padding: '12px 20px', cursor: 'pointer', fontSize: 15, width: '100%' }
const registeredBox: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '11px 16px', textAlign: 'center', fontSize: 14, fontWeight: 700, color: 'var(--fug)' }
const certBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'linear-gradient(135deg,#E5C46B,#caa64e)', color: '#1a1200', fontWeight: 800, border: 'none', borderRadius: 12, padding: '12px 20px', cursor: 'pointer', fontSize: 15 }
const expBtn: React.CSSProperties = { marginTop: 10, width: '100%', background: 'rgba(70,227,160,.1)', color: 'var(--fug)', fontWeight: 800, border: '1px solid rgba(70,227,160,.35)', borderRadius: 12, padding: '11px 20px', cursor: 'pointer', fontSize: 14 }
const lightbox: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 90, background: 'rgba(0,0,0,.88)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 16, padding: 16 }
const lightboxDl: React.CSSProperties = { background: 'linear-gradient(135deg,#E5C46B,#caa64e)', color: '#1a1200', fontWeight: 800, border: 'none', borderRadius: 10, padding: '11px 22px', cursor: 'pointer', fontSize: 15 }
const notStartedHint: React.CSSProperties = { background: 'rgba(255,210,90,.08)', border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px', fontSize: 13, color: 'var(--gold)', marginBottom: 14, textAlign: 'center' }
