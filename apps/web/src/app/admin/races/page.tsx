'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import Link from 'next/link'
import { adminRacesApi, raceStatusFlags, type Race } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import NewRaceModal from './NewRaceModal'

const CONTROL_LABEL: Record<string, string> = {
  active: '正常運作', paused: '暫停報名', suspended: '賽事中止',
  closed: '賽事關閉', hidden: '賽事隱藏', testing: '測試中',
}
const DISPLAY_LABEL: Record<string, string> = {
  upcoming_reg: '即將報名', registering: '報名中', reg_closed: '報名結束',
  starting_soon: '賽事即將開始', racing: '賽事進行中', ended: '賽事結束',
  paused: '暫停報名', suspended: '賽事中止',
}
const MODE_LABEL: Record<string, string> = {
  general: '一般', competition: '競賽', faction_battle: '分組對抗', personal: '個人挑戰',
}

// 賽事狀態（活動期間，3 值）／報名狀態（可否報名，2 值）：用 raceStatusFlags 解耦計算，
// 供卡片醒目標籤 + 篩選列共用。個人挑戰模式活動中會同時是「進行中」+「報名中」，兩維各自獨立勾選。
type EventStatus = 'upcoming' | 'ongoing' | 'ended'
type RegStatus = 'open' | 'closed'
const ALL_EVENT_STATUSES: EventStatus[] = ['upcoming', 'ongoing', 'ended']
const ALL_REG_STATUSES: RegStatus[] = ['open', 'closed']
// 控制狀態（管理控制維，值＝control_status；標籤用 CONTROL_LABEL）
const ALL_CONTROL_STATUSES: string[] = ['active', 'paused', 'suspended', 'closed', 'hidden', 'testing']
const EVENT_STATUS_LABEL: Record<EventStatus, string> = { upcoming: '尚未開始', ongoing: '進行中', ended: '已結束' }
const REG_STATUS_LABEL: Record<RegStatus, string> = { open: '報名中', closed: '未開放' }

function eventStatusOf(r: Race): EventStatus {
  const { ended, ongoing } = raceStatusFlags(r)
  return ended ? 'ended' : ongoing ? 'ongoing' : 'upcoming'
}
function regStatusOf(r: Race): RegStatus {
  return raceStatusFlags(r).regOpen ? 'open' : 'closed'
}

type BadgeTone = 'fug' | 'gold' | 'violet' | 'gray'
const BADGE_STYLE: Record<BadgeTone, React.CSSProperties> = {
  fug: { background: 'rgba(45,212,150,.12)', color: 'var(--fug)', border: '1px solid rgba(45,212,150,.3)' },
  gold: { background: 'rgba(255,196,0,.12)', color: 'var(--gold)', border: '1px solid rgba(255,196,0,.3)' },
  violet: { background: 'rgba(157,140,255,.12)', color: 'var(--violet)', border: '1px solid rgba(157,140,255,.3)' },
  gray: { background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line)' },
}
const EVENT_STATUS_TONE: Record<EventStatus, BadgeTone> = { upcoming: 'violet', ongoing: 'fug', ended: 'gray' }
const REG_STATUS_TONE: Record<RegStatus, BadgeTone> = { open: 'gold', closed: 'gray' }

function Badge({ tone, children }: { tone: BadgeTone; children: React.ReactNode }) {
  return (
    <span
      style={{
        ...BADGE_STYLE[tone],
        borderRadius: 999, padding: '3px 10px', fontSize: 11.5, fontWeight: 700,
        display: 'inline-block', whiteSpace: 'nowrap', lineHeight: 1.4,
      }}
    >
      {children}
    </span>
  )
}

function fmtDate(s?: string | null) {
  if (!s) return '—'
  const d = new Date(s)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
function fmtFee(cents: number) {
  return cents > 0 ? `NT$${Math.round(cents / 100).toLocaleString()}` : '免費'
}

function InfoItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div style={{ minWidth: 0 }}>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginBottom: 1 }}>{label}</div>
      <div style={{ fontSize: 12, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{value}</div>
    </div>
  )
}

export default function AdminRacesList() {
  const router = useRouter()
  const [races, setRaces] = useState<Race[] | null>(null)
  const [err, setErr] = useState('')
  const [showNew, setShowNew] = useState(false)
  const [token, setTokenState] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const [settlingId, setSettlingId] = useState<string | null>(null)
  // 勾選篩選：預設全勾＝全部顯示；純 client 端 filter，不打 API
  const [eventStatusFilter, setEventStatusFilter] = useState<Set<EventStatus>>(new Set(ALL_EVENT_STATUSES))
  const [regStatusFilter, setRegStatusFilter] = useState<Set<RegStatus>>(new Set(ALL_REG_STATUSES))
  const [controlStatusFilter, setControlStatusFilter] = useState<Set<string>>(new Set(ALL_CONTROL_STATUSES))

  function toggleEventStatus(s: EventStatus) {
    setEventStatusFilter((prev) => {
      const next = new Set(prev)
      if (next.has(s)) next.delete(s)
      else next.add(s)
      return next
    })
  }
  function toggleRegStatus(s: RegStatus) {
    setRegStatusFilter((prev) => {
      const next = new Set(prev)
      if (next.has(s)) next.delete(s)
      else next.add(s)
      return next
    })
  }
  function toggleControlStatus(s: string) {
    setControlStatusFilter((prev) => {
      const next = new Set(prev)
      if (next.has(s)) next.delete(s)
      else next.add(s)
      return next
    })
  }

  async function handleSettle(e: React.MouseEvent, r: Race) {
    e.preventDefault()
    e.stopPropagation()
    if (!token) return
    setErr('')
    setSettlingId(r.id)
    try {
      const { result } = await adminRacesApi.settleExp(token, r.id)
      window.alert(result.already_settled
        ? `「${r.title}」先前已結算過（可至會員看 EXP）。`
        : `「${r.title}」EXP 結算完成：${result.awarded_users} 人共獲得 ${result.total_exp} EXP（參賽 ${result.participants} 人）。`)
    } catch (e: any) {
      setErr(e?.message || '結算失敗')
    } finally {
      setSettlingId(null)
    }
  }

  function handleRewardsClick(e: React.MouseEvent, r: Race) {
    e.preventDefault() // 阻止外層卡片 Link 導航，改為自行導向獎勵管理頁
    e.stopPropagation()
    router.push(`/admin/races/${r.id}/rewards`)
  }

  function handleVirtualRunnersClick(e: React.MouseEvent, r: Race) {
    e.preventDefault() // 阻止外層卡片 Link 導航，改為自行導向虛擬選手管理頁
    e.stopPropagation()
    router.push(`/admin/races/${r.id}/virtual-runners`)
  }

  async function handleDelete(e: React.MouseEvent, r: Race) {
    e.preventDefault() // 阻止 Link 導航
    e.stopPropagation()
    if (!token) return
    if (!window.confirm(`確定要刪除賽事「${r.title}」？此動作無法復原。`)) return
    setErr('')
    setDeletingId(r.id)
    try {
      await adminRacesApi.remove(token, r.id)
      setRaces((rs) => (rs ? rs.filter((x) => x.id !== r.id) : rs))
    } catch (e: any) {
      setErr(e?.message || '刪除失敗')
    } finally {
      setDeletingId(null)
    }
  }

  const load = useCallback(() => {
    const t = getToken()
    if (!t) {
      router.replace('/admin/login')
      return
    }
    setTokenState(t)
    adminRacesApi
      .list(t)
      .then((res) => setRaces(res.races))
      .catch((e) => {
        if (e?.status === 401) {
          clearToken()
          router.replace('/admin/login')
        } else {
          setErr(e?.message || '載入失敗')
        }
      })
  }, [router])

  useEffect(() => {
    load()
  }, [load])

  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', margin: '0 0 20px' }}>
        <h1 style={{ fontSize: 24, fontWeight: 800, margin: 0 }}>賽事管理</h1>
        <button
          onClick={() => setShowNew(true)}
          style={{
            background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 700, border: 'none',
            borderRadius: 10, padding: '10px 18px', cursor: 'pointer', fontSize: 14,
          }}
        >
          ＋ 新增賽事
        </button>
      </div>

      {showNew && token && (
        <NewRaceModal
          token={token}
          onClose={() => setShowNew(false)}
          onCreated={() => {
            setShowNew(false)
            setRaces(null)
            load()
          }}
        />
      )}

      {err && <div style={{ color: 'var(--hunt)', padding: 20 }}>{err}</div>}
      {!races && !err && <div style={{ color: 'var(--tx-dim)', padding: 20 }}>載入中…</div>}

      {races && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div
            style={{
              display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 16,
              background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12,
              padding: '12px 16px', fontSize: 13,
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <span style={{ color: 'var(--tx-dim)', fontSize: 12, fontWeight: 700 }}>賽事狀態</span>
              {ALL_EVENT_STATUSES.map((s) => (
                <label key={s} style={{ display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer', color: 'var(--tx)' }}>
                  <input type="checkbox" checked={eventStatusFilter.has(s)} onChange={() => toggleEventStatus(s)} />
                  {EVENT_STATUS_LABEL[s]}
                </label>
              ))}
            </div>
            <div style={{ width: 1, height: 18, background: 'var(--line)' }} />
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <span style={{ color: 'var(--tx-dim)', fontSize: 12, fontWeight: 700 }}>報名狀態</span>
              {ALL_REG_STATUSES.map((s) => (
                <label key={s} style={{ display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer', color: 'var(--tx)' }}>
                  <input type="checkbox" checked={regStatusFilter.has(s)} onChange={() => toggleRegStatus(s)} />
                  {REG_STATUS_LABEL[s]}
                </label>
              ))}
            </div>
            <div style={{ width: 1, height: 18, background: 'var(--line)' }} />
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <span style={{ color: 'var(--tx-dim)', fontSize: 12, fontWeight: 700 }}>控制狀態</span>
              {ALL_CONTROL_STATUSES.map((s) => (
                <label key={s} style={{ display: 'flex', alignItems: 'center', gap: 4, cursor: 'pointer', color: 'var(--tx)' }}>
                  <input type="checkbox" checked={controlStatusFilter.has(s)} onChange={() => toggleControlStatus(s)} />
                  {CONTROL_LABEL[s] ?? s}
                </label>
              ))}
            </div>
            <div style={{ display: 'flex', gap: 8, marginLeft: 'auto' }}>
              <button
                onClick={() => {
                  setEventStatusFilter(new Set(ALL_EVENT_STATUSES))
                  setRegStatusFilter(new Set(ALL_REG_STATUSES))
                  setControlStatusFilter(new Set(ALL_CONTROL_STATUSES))
                }}
                style={{
                  background: 'none', border: '1px solid var(--line)', borderRadius: 8,
                  padding: '4px 10px', fontSize: 12, color: 'var(--tx-dim)', cursor: 'pointer',
                }}
              >
                全選
              </button>
              <button
                onClick={() => {
                  setEventStatusFilter(new Set())
                  setRegStatusFilter(new Set())
                  setControlStatusFilter(new Set())
                }}
                style={{
                  background: 'none', border: '1px solid var(--line)', borderRadius: 8,
                  padding: '4px 10px', fontSize: 12, color: 'var(--tx-dim)', cursor: 'pointer',
                }}
              >
                清除
              </button>
            </div>
          </div>

          {races
            .filter((r) => eventStatusFilter.has(eventStatusOf(r)) && regStatusFilter.has(regStatusOf(r)) && controlStatusFilter.has(r.control_status))
            .map((r) => (
            <Link
              key={r.id}
              href={`/admin/races/${r.id}`}
              style={{
                textDecoration: 'none',
                color: 'inherit',
                background: 'var(--bg-1)',
                border: '1px solid var(--line)',
                borderRadius: 14,
                padding: 18,
                display: 'flex',
                flexDirection: 'column',
                gap: 12,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
                <div style={{ minWidth: 0 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                    <div style={{ fontSize: 16, fontWeight: 700 }}>{r.title}</div>
                    <Badge tone={EVENT_STATUS_TONE[eventStatusOf(r)]}>{EVENT_STATUS_LABEL[eventStatusOf(r)]}</Badge>
                    <Badge tone={REG_STATUS_TONE[regStatusOf(r)]}>{REG_STATUS_LABEL[regStatusOf(r)]}</Badge>
                  </div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3 }}>
                    {r.subtitle} · {r.distances.join('/')}K · {r.slots_total} 名額
                  </div>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexShrink: 0 }}>
                  <span style={{ color: 'var(--fug)', fontSize: 13 }}>編輯 →</span>
                  <button
                    onClick={(e) => handleRewardsClick(e, r)}
                    title={r.event_mode === 'personal' ? '個人挑戰模式：後台獎勵管理（完成者名單/抽獎/發放追蹤）' : '後台獎勵管理（完賽者抽獎/發放追蹤）'}
                    style={{
                      background: 'rgba(255,196,0,.1)', color: 'var(--gold)', border: '1px solid rgba(255,196,0,.3)',
                      borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
                    }}
                  >
                    🎁 獎勵
                  </button>
                  <button
                    onClick={(e) => handleVirtualRunnersClick(e, r)}
                    title="虛擬選手管理（加入/移除 is_virtual 人頭帳號補熱度）"
                    style={{
                      background: 'rgba(157,140,255,.1)', color: 'var(--violet)', border: '1px solid rgba(157,140,255,.3)',
                      borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
                    }}
                  >
                    🤖 虛擬選手
                  </button>
                  {r.display_status === 'ended' && (
                    <button
                      onClick={(e) => handleSettle(e, r)}
                      disabled={settlingId === r.id}
                      title="結算此賽事 EXP（完成賽事/任務/里程）"
                      style={{
                        background: 'rgba(45,212,150,.1)', color: 'var(--fug)', border: '1px solid rgba(45,212,150,.3)',
                        borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
                      }}
                    >
                      {settlingId === r.id ? '結算中…' : '結算 EXP'}
                    </button>
                  )}
                  <button
                    onClick={(e) => handleDelete(e, r)}
                    disabled={deletingId === r.id}
                    title="刪除賽事"
                    style={{
                      background: 'rgba(255,80,80,.08)', color: 'var(--hunt)', border: '1px solid rgba(255,80,80,.25)',
                      borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
                    }}
                  >
                    {deletingId === r.id ? '刪除中…' : '刪除'}
                  </button>
                </div>
              </div>

              <div
                style={{
                  display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))', gap: 10,
                  borderTop: '1px solid var(--line)', paddingTop: 12,
                }}
              >
                <InfoItem label="賽事模式" value={MODE_LABEL[r.event_mode] ?? r.event_mode} />
                <InfoItem label="報名費" value={r.fee_mode === 'per_group' ? `${fmtFee(r.display_fee_cents)} 起（各組獨立）` : fmtFee(r.entry_fee)} />
                <InfoItem label="顯示狀態" value={DISPLAY_LABEL[r.display_status] ?? r.display_status} />
                <InfoItem label="控制狀態" value={CONTROL_LABEL[r.control_status] ?? r.control_status} />
                <InfoItem label="報名時間" value={`${fmtDate(r.registration_start)} ~ ${fmtDate(r.registration_end)}`} />
                <InfoItem label="賽事時間" value={`${fmtDate(r.start_date)} ~ ${fmtDate(r.end_date)}`} />
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
