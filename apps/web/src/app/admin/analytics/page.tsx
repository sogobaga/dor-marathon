'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminAnalyticsApi,
  type MemberAnalyticsReport,
  type AnalyticsDatePoint,
  type AnalyticsGroupAvg,
  type AnalyticsSystemUsage,
  type AnalyticsRunner,
  type AnalyticsRunnersSummary,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { SIGNUP_SOURCE_LABEL, SIGNUP_SOURCE_COLOR } from '@/lib/signupSource'
import { analyticsColor } from '@/lib/analyticsColors'

// 會員活躍度管理後台。資料源＝member_analytics_reports（migration 148）每日 03:00 排程算好存檔的
// JSONB 日報，這裡只讀最新一筆（GET）或觸發立即重算（POST，20s timeout 內完成）。契約鍵名固定，
// 詳見後端 internal/analytics；統計皆已排除 users.is_virtual、活動皆已排除 flagged、時區台灣日。
// 圖表元件三件組（頁內自含，不依賴外部圖表庫）：PieChart／TrendLine／BarChart，風格比照
// admin/overview 的手刻 inline SVG TrendChart + admin/promo-links 的 div/flex 手刻長條慣例。

export default function AdminAnalyticsPage() {
  const router = useRouter()
  const [report, setReport] = useState<MemberAnalyticsReport | null>(null)
  const [stale, setStale] = useState(false)
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    adminAnalyticsApi.get(t).then((r) => { setReport(r.report); setStale(r.stale); setErr('') }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「活躍度分析」權限')
      else setErr(e?.message || '載入失敗')
    })
  }, [router])
  useEffect(() => { load() }, [load])

  async function recompute() {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setBusy(true); setErr(''); setMsg('')
    try {
      const r = await adminAnalyticsApi.recompute(t)
      setReport(r.report); setStale(r.stale)
      setMsg(`✓ 已重新計算，統計基準日 ${r.report.day}`)
    } catch (e: any) {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else if (e?.status === 403) setErr('無「活躍度分析」權限')
      else setErr(e?.message || '重算失敗，可能逾時，稍後再試')
    } finally { setBusy(false) }
  }

  return (
    <div style={{ maxWidth: 980, margin: '0 auto' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', flexWrap: 'wrap', gap: 12 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>會員活躍度分析</h1>
          <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: 0 }}>
            統計基準日 <b style={{ color: 'var(--tx)' }}>{report?.day ?? '—'}</b>（每日 03:00 自動更新）
          </p>
        </div>
        <button onClick={recompute} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.6 : 1 }}>
          {busy ? '重算中…' : '🔄 立即重算'}
        </button>
      </div>

      {stale && (
        <div style={{ color: 'var(--gold)', background: 'rgba(245,194,66,.1)', border: '1px solid rgba(245,194,66,.35)', borderRadius: 10, padding: '9px 14px', fontSize: 12.5, marginTop: 14 }}>
          ⚠️ 最新一筆統計資料已超過 48 小時未更新，建議立即重算。
        </div>
      )}
      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '10px 0', fontSize: 13 }}>{msg}</div>}
      {!report && !err && <div style={{ color: 'var(--tx-dim)', padding: '16px 0' }}>載入中…</div>}

      {report && (
        <div style={{ marginTop: 16 }}>
          {/* ───────── 1. 會員註冊 ───────── */}
          <SectionCard icon="📝" title="會員註冊">
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 4 }}>
              <StatTile label="總會員數（累計）" value={report.registrations.total_members} unit="人" color="var(--fug)" />
            </div>
            <ChartBlock title="新註冊會員數（近 30 日）" caption="口徑：每日新註冊會員數，台灣時區日界，不含虛擬帳號（is_virtual）。">
              <TrendLine data={report.registrations.new_30d} color="var(--fug)" unit=" 人" />
            </ChartBlock>
            <ChartBlock title="註冊時刻分布" caption="口徑：全期間累計註冊時間點分布（24 小時制，台灣時區）。">
              <BarChart
                data={report.registrations.by_hour.map((v, h) => ({ label: String(h), value: v }))}
                orientation="v" color="var(--fug)" unit=" 人" tickEvery={3} showValues={false}
              />
            </ChartBlock>
            <ChartBlock title="註冊來源分布" caption="口徑：全期間累計，依 UTM／referrer 歸因分類（見 migration 147）。">
              <PieChart
                data={report.registrations.by_source.map((d) => ({ label: SIGNUP_SOURCE_LABEL[d.source as keyof typeof SIGNUP_SOURCE_LABEL] || d.source, value: d.count }))}
                colorAt={(i) => {
                  const s = report.registrations.by_source[i]?.source
                  return (s && SIGNUP_SOURCE_COLOR[s as keyof typeof SIGNUP_SOURCE_COLOR]) || analyticsColor(i)
                }}
              />
            </ChartBlock>
          </SectionCard>

          {/* ───────── 2. 登入活躍 ───────── */}
          <SectionCard icon="🔑" title="登入活躍">
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 4 }}>
              <StatTile label="近 7 日活躍" value={report.logins.active_7d} unit="人" color="var(--fug)" />
              <StatTile label="近 30 日活躍" value={report.logins.active_30d} unit="人" color="var(--tx)" />
            </div>
            <ChartBlock title="每日活躍人數 DAU（近 30 日）" caption="口徑：當日有登入事件或活動上傳者計入，台灣時區日界。">
              <TrendLine data={report.logins.dau_30d} color="var(--fug)" unit=" 人" />
            </ChartBlock>
            <ChartBlock title="登入頻率分布（近 30 日）" caption="口徑：每人近 30 日登入次數分桶（0／1-2／3-9／10-29／30+ 次）。">
              <PieChart data={report.logins.freq_dist_30d.map((d) => ({ label: `${d.bucket} 次`, value: d.count }))} />
            </ChartBlock>
            <ChartBlock title="登入時刻分布" caption="口徑：近 30 日登入時間點分布（24 小時制，台灣時區）。">
              <BarChart
                data={report.logins.by_hour.map((v, h) => ({ label: String(h), value: v }))}
                orientation="v" color="var(--gold)" unit=" 次" tickEvery={3} showValues={false}
              />
            </ChartBlock>
          </SectionCard>

          {/* ───────── 3. 跑步里程 ───────── */}
          <SectionCard icon="🏃" title="跑步里程">
            <ChartBlock title="全站每日總里程（近 30 日）" caption="口徑：每日全站上傳里程加總，台灣時區日界，排除已標記異常（flagged）活動。">
              <TrendLine data={report.mileage.daily_km_30d.map((d) => ({ date: d.date, count: d.km }))} color="var(--gold)" unit=" km" />
            </ChartBlock>
            <ChartBlock title="平均配速分布" caption="口徑：每人近 30 日平均配速分桶（&lt;5:00／5-6／6-7／7-8／&gt;8:00 分/公里）。">
              <PieChart data={report.mileage.pace_dist.map((d) => ({ label: `${d.bucket} /km`, value: d.count }))} />
            </ChartBlock>
            <ChartBlock title="月跑量分布" caption="口徑：每人近 30 日累計跑量分桶（0／1-20／21-50／51-100／100+ km）。">
              <PieChart data={report.mileage.monthly_volume_dist.map((d) => ({ label: `${d.bucket} km`, value: d.count }))} />
            </ChartBlock>
            <ChartBlock title="各性別平均跑量對比" caption="口徑：近 30 日各性別平均里程（公里/人），bar 上方標平均值，hover 另附人數。">
              <BarChart
                data={report.mileage.by_gender.map((g) => groupAvgDatum(g))}
                orientation="v" color={(i) => analyticsColor(i)} unit=" km"
              />
            </ChartBlock>
            <ChartBlock title="各年齡層平均跑量對比" caption="口徑：近 30 日各年齡層平均里程（公里/人），未填年齡另列一類；hover 附人數。">
              <BarChart
                data={report.mileage.by_age.map((g) => groupAvgDatum(g))}
                orientation="v" color="var(--gold)" unit=" km"
              />
            </ChartBlock>
          </SectionCard>

          {/* ───────── 4. 賽事參與 ───────── */}
          <SectionCard icon="🎽" title="賽事參與">
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 4 }}>
              <StatTile label="曾報名過賽事佔比" value={fmtPct(report.participation.ever_registered_pct)} color="var(--fug)" />
            </div>
            <ChartBlock title="每日報名筆數（近 30 日）" caption="口徑：每日新增賽事報名筆數，台灣時區日界。">
              <TrendLine data={report.participation.reg_30d} color="var(--fug)" unit=" 筆" />
            </ChartBlock>
            <ChartBlock title="熱門賽事 Top" caption="口徑：全期間累計報名人數最多的賽事。">
              <BarChart
                data={report.participation.top_races.map((r) => ({ label: r.title || '—', value: r.count }))}
                orientation="h" color="var(--fug)" unit=" 人"
              />
            </ChartBlock>
            <ChartBlock title="重複報名分布" caption="口徑：會員累計報名場次分桶（0／1／2-3／4+ 場）。">
              <PieChart data={report.participation.repeat_dist.map((d) => ({ label: `${d.bucket} 場`, value: d.count }))} />
            </ChartBlock>
          </SectionCard>

          {/* ───────── 5. 卡片收集 ───────── */}
          <SectionCard icon="🃏" title="卡片收集">
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 4 }}>
              <StatTile label="收集者人數" value={report.cards.collectors} unit="人" color="var(--gold)" />
              <StatTile label="累計已收集張數" value={report.cards.total_collected} unit="張" color="var(--tx)" />
            </div>
            <ChartBlock title="收集張數分布" caption="口徑：會員累計收集卡片張數分桶（0／1-5／6-20／21-50／50+ 張）。">
              <PieChart data={report.cards.collection_dist.map((d) => ({ label: `${d.bucket} 張`, value: d.count }))} />
            </ChartBlock>
            <ChartBlock title="熱門卡片 Top" caption="口徑：全期間累計被收集次數最多的卡片。">
              <BarChart
                data={report.cards.top_cards.map((c) => ({ label: c.name || '—', value: c.count }))}
                orientation="h" color="var(--gold)" unit=" 次"
              />
            </ChartBlock>
          </SectionCard>

          {/* ───────── 6. 系統使用 ───────── */}
          <SectionCard icon="🧩" title="各系統使用">
            <ChartBlock title="各遊戲系統使用人數" caption="口徑：近 30 日使用人數 vs. 累計曾使用人數，依累計人數由高到低排序。">
              <div style={{ display: 'flex', gap: 14, fontSize: 11.5, color: 'var(--tx-dim)', marginBottom: 10 }}>
                <Legend color={analyticsColor(0)} label="近 30 日使用人數" />
                <Legend color={analyticsColor(3)} label="累計曾使用人數" />
              </div>
              <BarChart
                data={report.systems.usage.slice().sort((a, b) => b.users_total - a.users_total).map((s) => systemDatum(s))}
                orientation="h" color={analyticsColor(0)} color2={analyticsColor(3)} unit=" 人"
              />
            </ChartBlock>
          </SectionCard>

          {/* ───────── 7. 跑步數據排行 ───────── */}
          <SectionCard icon="🏆" title="跑步數據排行">
            <RunnersSection runners={report.runners} summary={report.runners_summary} />
          </SectionCard>
        </div>
      )}
    </div>
  )
}

// ── 資料轉換小工具 ──

function fmtPct(v: number): string {
  if (typeof v !== 'number' || Number.isNaN(v)) return '—'
  return `${Math.round(v * 10) / 10}%`
}
function round1(n: number): number { return Math.round((n || 0) * 10) / 10 }
// 後端 by_gender.group 固定回英文鍵（male/female/other/unspecified，見 genderGroupOrder），
// by_age.group 則本來就是可直接顯示的字串（<18／18-24／…／未填），故只有性別需要中譯。
const GENDER_LABEL: Record<string, string> = { male: '男', female: '女', other: '其他', unspecified: '未填' }
function groupAvgDatum(g: AnalyticsGroupAvg): BarDatum {
  const label = GENDER_LABEL[g.group] ?? g.group
  return { label, value: round1(g.avg_km), hover: `${label}：平均 ${round1(g.avg_km)} km ／ ${g.users} 人` }
}
function systemDatum(s: AnalyticsSystemUsage): BarDatum {
  return {
    label: s.label, value: s.users_30d, value2: s.users_total,
    hover: `${s.label}：近30日 ${s.users_30d} 人`, hover2: `${s.label}：累計 ${s.users_total} 人`,
  }
}
// 累積時間（秒）→「時:分」，四捨五入到分鐘（本區塊只用於排行顯示，不需要秒級精度）。
function fmtDurationHM(totalSeconds: number): string {
  const totalMin = Math.round((totalSeconds || 0) / 60)
  const h = Math.floor(totalMin / 60)
  const m = totalMin % 60
  return `${h}:${String(m).padStart(2, '0')}`
}
// 平均配速（秒/公里）→「分:秒」，比照 admin/TaskItemEditor.tsx paceToStr 同款格式（本頁自成一體，
// 未跨頁匯入，理由同頁首註解：圖表三件組刻意頁內自含）。avg_pace_s<=0（理論上不會發生，後端已保證
// 至少一筆活動）防禦性顯示 —。
function fmtPaceMinSec(paceS: number): string {
  if (!paceS || paceS <= 0) return '—'
  const m = Math.floor(paceS / 60)
  const s = Math.round(paceS % 60)
  return `${m}:${String(s).padStart(2, '0')}`
}

// ── 版面小元件 ──

function SectionCard({ icon, title, children }: { icon: string; title: string; children: React.ReactNode }) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 12, padding: '14px 16px', marginBottom: 18 }}>
      <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)', marginBottom: 10 }}>{icon} {title}</div>
      {children}
    </div>
  )
}

function ChartBlock({ title, caption, children }: { title: string; caption: string; children: React.ReactNode }) {
  return (
    <div style={{ marginTop: 16 }}>
      <div style={{ fontSize: 11.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 6 }}>{title}</div>
      {children}
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 6, lineHeight: 1.5 }}>{caption}</div>
    </div>
  )
}

function StatTile({ label, value, unit, color }: { label: string; value: React.ReactNode; unit?: string; color?: string }) {
  return (
    <div style={{ flex: '1 1 150px', minWidth: 140, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 12px' }}>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 22, fontWeight: 900, color: color ?? 'var(--tx)' }}>
        {value}{unit && <span style={{ fontSize: 12, color: 'var(--tx-dim)', marginLeft: 3 }}>{unit}</span>}
      </div>
    </div>
  )
}

function Legend({ color, label }: { color: string; label: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 5 }}>
      <span style={{ width: 10, height: 10, borderRadius: 3, background: color, display: 'inline-block', flexShrink: 0 }} />
      {label}
    </div>
  )
}

// 第七區塊「跑步數據排行」表格。runners undefined＝舊日報（本欄位上線前算出的、無 runners 鍵），
// 顯示提示要求重算，而非當成空陣列（空陣列＝有算但 0 筆，語意不同）。hideVirtual／showAll 純屬本
// 表格的顯示狀態，不影響其餘區塊，故就地用 useState 管理，不上提到頁面層級。過濾在 client side 做
// （資料已在同一份 report 裡，不必為了篩虛擬選手多打一次 API），過濾後名次（rank）重新從 1 編號。
// summary undefined＝舊日報沒有 runners_summary 鍵，該統計列整段不顯示（比照 runners undefined 的
// 判斷方式，不當成 0 處理，語意不同）。
function RunnersSection({ runners, summary }: { runners: AnalyticsRunner[] | undefined; summary: AnalyticsRunnersSummary | undefined }) {
  const [hideVirtual, setHideVirtual] = useState(true)
  const [showAll, setShowAll] = useState(false)

  if (runners === undefined) {
    return (
      <div style={{ fontSize: 12.5, color: 'var(--tx-faint)', padding: '16px 0' }}>
        本報告為舊版統計（尚未包含本區塊），請按上方「🔄 立即重算」後再查看。
      </div>
    )
  }

  const filtered = hideVirtual ? runners.filter((r) => !r.is_virtual) : runners
  const visible = showAll ? filtered : filtered.slice(0, 50)
  // 排名升降資料可用性：只看真人列（虛擬列本就永遠沒有 rank_delta，不計入判斷）。全部真人列都沒有
  // rank_delta 也不是新進榜＝這份報告還沒有累積到「上週」快照可比較（例如 migration 上線未滿一週），
  // 顯示提示句而非讓每一列都各自顯示一次「—」卻沒有說明。
  const realRunners = runners.filter((r) => !r.is_virtual)
  const noRankData = realRunners.length > 0 && realRunners.every((r) => r.rank_delta == null && !r.is_new)

  return (
    <div>
      <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12.5, color: 'var(--tx-dim)', marginBottom: 10, cursor: 'pointer', width: 'fit-content' }}>
        <input type="checkbox" checked={hideVirtual} onChange={(e) => { setHideVirtual(e.target.checked); setShowAll(false) }} />
        隱藏虛擬選手
      </label>

      {summary && <RunnersSummaryRow summary={summary} hideVirtual={hideVirtual} />}

      {noRankData && (
        <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginBottom: 8 }}>
          排名變化需累積一週快照後顯示。
        </div>
      )}

      {filtered.length === 0 ? (
        <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '16px 0' }}>尚無資料</div>
      ) : (
        <>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12.5, minWidth: 820 }}>
              <thead>
                <tr style={{ background: 'var(--bg-2)' }}>
                  <th style={{ ...runnerThStyle, textAlign: 'left' }}>名次</th>
                  <th style={{ ...runnerThStyle, textAlign: 'left' }}>跑者</th>
                  <th style={runnerThStyle}>等級</th>
                  <th style={runnerThStyle}>DP</th>
                  <th style={runnerThStyle}>GP</th>
                  <th style={runnerThStyle}>累積里程</th>
                  <th style={runnerThStyle}>累積時間</th>
                  <th style={runnerThStyle}>平均配速</th>
                  <th style={runnerThStyle}>筆數</th>
                  <th style={runnerThStyle}>週均跑步天數</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((r, i) => (
                  <tr key={`${r.handle}-${i}`} style={{ borderTop: '1px solid var(--line)' }}>
                    <td style={runnerTdStyle}>
                      {i + 1}
                      <RankDeltaBadge runner={r} />
                    </td>
                    <td style={runnerTdStyle}>
                      <div style={{ display: 'flex', flexDirection: 'column' }}>
                        <span style={{ color: 'var(--tx)' }}>
                          {r.name}
                        </span>
                        <span style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>@{r.handle}</span>
                      </div>
                    </td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{r.level == null ? '—' : `Lv.${r.level}`}</td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{r.dp == null ? '—' : r.dp}</td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{r.gp == null ? '—' : r.gp}</td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{round1(r.total_km)} km</td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{fmtDurationHM(r.total_duration_s)}</td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{fmtPaceMinSec(r.avg_pace_s)} /km</td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{r.runs}</td>
                    <td style={{ ...runnerTdStyle, textAlign: 'right' }}>{r.avg_days_per_week} 天</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {filtered.length > 50 && (
            <div style={{ textAlign: 'center', marginTop: 10 }}>
              <button onClick={() => setShowAll((v) => !v)} style={ghostBtn}>
                {showAll ? '收合' : `顯示更多（共 ${filtered.length} 筆）`}
              </button>
            </div>
          )}
        </>
      )}

      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 10, lineHeight: 1.5 }}>
        口徑：全來源（App GPS + Strava 等同步來源，已排除重複/異常活動），依累積里程由高到低排序，取前 200 名；週均跑步天數＝有跑步的相異台灣日數 ÷ 經過週數（今天−首跑日的天數 ÷7，下限 1 週）；排名變化以真人榜為基準。
      </div>
    </div>
  )
}

// 總覽統計列：依「隱藏虛擬選手」開關切換 real-only／real+virtual 加總口徑，兩個比率（跑者佔會員／
// 昨日開跑佔跑者）的分子分母一律用同一種口徑算，避免出現「real 分子 / real+virtual 分母」這種
// 混搭口徑的誤導數字。
function RunnersSummaryRow({ summary, hideVirtual }: { summary: AnalyticsRunnersSummary; hideVirtual: boolean }) {
  const yesterday = hideVirtual ? summary.ran_yesterday_real : summary.ran_yesterday_real + summary.ran_yesterday_virtual
  const d7 = hideVirtual ? summary.ran_7d_real : summary.ran_7d_real + summary.ran_7d_virtual
  const runnersTotal = hideVirtual ? summary.runners_total_real : summary.runners_total_real + summary.runners_total_virtual
  const members = hideVirtual ? summary.members_real : summary.members_real + summary.members_virtual
  const runnerPct = members > 0 ? Math.round((runnersTotal / members) * 1000) / 10 : 0
  const yesterdayPct = runnersTotal > 0 ? Math.round((yesterday / runnersTotal) * 1000) / 10 : 0

  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 14 }}>
      <StatTile label="昨日開跑" value={yesterday} unit="人" color="var(--fug)" />
      <StatTile label="近 7 日開跑" value={d7} unit="人" color="var(--fug)" />
      <StatTile label="全站跑者" value={runnersTotal} unit="人" color="var(--tx)" />
      <StatTile label="會員數" value={members} unit="人" color="var(--tx)" />
      <StatTile label="跑者佔會員" value={`${runnerPct}%`} color="var(--gold)" />
      <StatTile label="昨日開跑佔跑者" value={`${yesterdayPct}%`} color="var(--gold)" />
    </div>
  )
}

// 名次旁的升降徽章：▲N（上升，綠）／▼N（下降，紅）／—（有比較基準但沒有變化，或缺乏比較資料）／
// NEW（金，新進榜）。虛擬選手永遠不顯示（後端也永遠不會給虛擬列 rank_delta／is_new＝true，見後端
// model.go RunnerStat 型別註解——虛擬選手不屬於「真人榜」，連「—」都不顯示，跟真人的「有榜但不變」
// 語意不同，避免混淆）。
function RankDeltaBadge({ runner }: { runner: AnalyticsRunner }) {
  if (runner.is_virtual) return null
  if (runner.is_new) {
    return <span style={{ color: 'var(--gold)', fontWeight: 700, marginLeft: 5, fontSize: 11 }}>NEW</span>
  }
  if (runner.rank_delta == null) {
    return <span style={{ color: 'var(--tx-faint)', marginLeft: 5, fontSize: 11 }}>—</span>
  }
  if (runner.rank_delta === 0) {
    return <span style={{ color: 'var(--tx-faint)', marginLeft: 5, fontSize: 11 }}>—</span>
  }
  if (runner.rank_delta > 0) {
    return <span style={{ color: 'var(--fug)', marginLeft: 5, fontSize: 11 }}>▲{runner.rank_delta}</span>
  }
  return <span style={{ color: 'var(--hunt)', marginLeft: 5, fontSize: 11 }}>▼{Math.abs(runner.rank_delta)}</span>
}
const runnerThStyle: React.CSSProperties = { padding: '9px 10px', textAlign: 'right', fontSize: 11, letterSpacing: '.04em', color: 'var(--tx-faint)', fontWeight: 700 }
const runnerTdStyle: React.CSSProperties = { padding: '8px 10px' }

// ── 圖表元件三件組（頁內自含，不依賴外部圖表庫；風格比照 admin/overview TrendChart + admin/promo-links 堆疊長條）──

// 1) 圓餅圖：CSS conic-gradient + 圖例（數量與百分比）。colorAt 未提供時依 index 走 ANALYTICS_PALETTE 固定序。
function PieChart({ data, size = 132, colorAt }: { data: { label: string; value: number }[]; size?: number; colorAt?: (i: number) => string }) {
  const total = data.reduce((s, d) => s + d.value, 0)
  if (data.length === 0 || total <= 0) {
    return <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '16px 0' }}>尚無資料</div>
  }
  const pick = colorAt ?? ((i: number) => analyticsColor(i))
  let acc = 0
  const stops = data.map((d, i) => {
    const start = (acc / total) * 360
    acc += d.value
    const end = (acc / total) * 360
    return `${pick(i)} ${start}deg ${end}deg`
  })
  return (
    <div style={{ display: 'flex', gap: 18, alignItems: 'center', flexWrap: 'wrap' }}>
      <div
        style={{ width: size, height: size, borderRadius: '50%', background: `conic-gradient(${stops.join(', ')})`, flexShrink: 0, boxShadow: 'inset 0 0 0 1px var(--line)' }}
        title={data.map((d) => `${d.label}: ${d.value}`).join(' ／ ')}
      />
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5, flex: 1, minWidth: 160 }}>
        {data.map((d, i) => {
          const pct = Math.round((d.value / total) * 1000) / 10
          return (
            <div key={`${d.label}-${i}`} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
              <span style={{ width: 10, height: 10, borderRadius: 3, background: pick(i), flexShrink: 0, display: 'inline-block' }} />
              <span style={{ flex: 1, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.label}</span>
              <span style={{ color: 'var(--tx-dim)' }}>{d.value}</span>
              <span style={{ color: 'var(--tx-faint)', minWidth: 40, textAlign: 'right' }}>{pct}%</span>
            </div>
          )
        })}
      </div>
    </div>
  )
}

// 2) 趨勢線（近 30 日日期序列，手刻 inline SVG 長條，風格同 admin/overview TrendChart，但 x 軸標籤稀疏化避免 30 根擠爆）
function TrendLine({ data, color, unit = '' }: { data: (AnalyticsDatePoint | { date: string; count: number })[]; color: string; unit?: string }) {
  const w = 640, h = 130, padL = 8, padR = 8, padT = 16, padB = 22
  const innerW = w - padL - padR, innerH = h - padT - padB
  const n = data.length
  if (n === 0) return <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '16px 0' }}>尚無資料</div>
  const max = Math.max(1, ...data.map((d) => d.count))
  const barW = innerW / n
  const labelEvery = Math.max(1, Math.ceil(n / 6))
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} style={{ display: 'block', overflow: 'visible' }}>
      <line x1={padL} y1={h - padB} x2={w - padR} y2={h - padB} stroke="var(--line)" strokeWidth={1} />
      {data.map((d, i) => {
        const barH = (d.count / max) * innerH
        const x = padL + i * barW + barW * 0.15
        const bw = Math.max(1, barW * 0.7)
        const y = h - padB - barH
        const dateLabel = d.date.length >= 10 ? d.date.slice(5).replace('-', '/') : d.date
        return (
          <g key={`${d.date}-${i}`}>
            <rect x={x} y={y} width={bw} height={barH} fill={color} rx={1.5} opacity={0.85}>
              <title>{d.date}: {d.count}{unit}</title>
            </rect>
            {i % labelEvery === 0 && (
              <text x={x + bw / 2} y={h - padB + 12} textAnchor="middle" fontSize="8" fill="var(--tx-faint)">{dateLabel}</text>
            )}
          </g>
        )
      })}
    </svg>
  )
}

// 3) 長條圖：orientation='h' 用於 top 榜（label + 橫向 bar + 數值，可疊第二數列 value2/color2 做兩數列對比，
//    如系統使用 30日/累計）；orientation='v' 用於時段分布（24 格，數值標籤關閉避免擠爆）與性別/年齡對比
//    （少量分組，數值標籤開啟）。
type BarDatum = { label: string; value: number; value2?: number; hover?: string; hover2?: string }
function BarChart({
  data, orientation = 'v', color, color2, unit = '', tickEvery = 1, showValues = true,
}: {
  data: BarDatum[]
  orientation?: 'h' | 'v'
  color: string | ((i: number) => string)
  color2?: string
  unit?: string
  tickEvery?: number
  showValues?: boolean
}) {
  if (data.length === 0) return <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '16px 0' }}>尚無資料</div>

  if (orientation === 'h') {
    const max = Math.max(1, ...data.map((d) => Math.max(d.value, d.value2 ?? 0)))
    return (
      <div style={{ overflowX: 'auto' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10, minWidth: 320 }}>
          {data.map((d, i) => {
            const c = typeof color === 'function' ? color(i) : color
            return (
              <div key={`${d.label}-${i}`}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <div style={{ width: 112, flexShrink: 0, fontSize: 12, color: 'var(--tx-dim)', textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={d.label}>{d.label}</div>
                  <div style={{ flex: 1, background: 'var(--bg-2)', borderRadius: 4, height: 15 }}>
                    <div style={{ width: `${Math.max(2, (d.value / max) * 100)}%`, height: '100%', background: c, borderRadius: 4 }} title={d.hover ?? `${d.label}: ${d.value}${unit}`} />
                  </div>
                  <div style={{ width: 52, flexShrink: 0, fontSize: 12, fontWeight: 700, color: 'var(--tx)' }}>{d.value}{unit}</div>
                </div>
                {d.value2 !== undefined && color2 && (
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 3 }}>
                    <div style={{ width: 112, flexShrink: 0 }} />
                    <div style={{ flex: 1, background: 'var(--bg-2)', borderRadius: 4, height: 15 }}>
                      <div style={{ width: `${Math.max(2, (d.value2 / max) * 100)}%`, height: '100%', background: color2, borderRadius: 4 }} title={d.hover2 ?? `${d.label}: ${d.value2}${unit}`} />
                    </div>
                    <div style={{ width: 52, flexShrink: 0, fontSize: 12, fontWeight: 700, color: 'var(--tx-dim)' }}>{d.value2}{unit}</div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </div>
    )
  }

  // orientation === 'v'
  const w = 640, h = 150, padL = 8, padR = 8, padT = 18, padB = 24
  const innerW = w - padL - padR, innerH = h - padT - padB
  const n = data.length
  const max = Math.max(1, ...data.map((d) => d.value))
  const barW = innerW / n
  return (
    <div style={{ overflowX: 'auto' }}>
      <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} style={{ display: 'block', overflow: 'visible', minWidth: n > 20 ? 480 : undefined }}>
        <line x1={padL} y1={h - padB} x2={w - padR} y2={h - padB} stroke="var(--line)" strokeWidth={1} />
        {data.map((d, i) => {
          const barH = (d.value / max) * innerH
          const x = padL + i * barW + barW * 0.15
          const bw = Math.max(1, barW * 0.7)
          const y = h - padB - barH
          const c = typeof color === 'function' ? color(i) : color
          return (
            <g key={`${d.label}-${i}`}>
              <rect x={x} y={y} width={bw} height={barH} fill={c} rx={2} opacity={0.85}>
                <title>{d.hover ?? `${d.label}: ${d.value}${unit}`}</title>
              </rect>
              {showValues && d.value > 0 && (
                <text x={x + bw / 2} y={y - 4} textAnchor="middle" fontSize="8.5" fill="var(--tx-dim)">{d.value}</text>
              )}
              {i % tickEvery === 0 && (
                <text x={x + bw / 2} y={h - padB + 12} textAnchor="middle" fontSize="8" fill="var(--tx-faint)">{d.label}</text>
              )}
            </g>
          )
        })}
      </svg>
    </div>
  )
}

const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 10, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5 }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx-dim)', fontWeight: 700, border: '1px solid var(--line)', borderRadius: 8, padding: '6px 14px', cursor: 'pointer', fontSize: 12.5 }
