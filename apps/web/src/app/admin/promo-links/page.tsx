'use client'

import { useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import { getToken, clearToken } from '@/lib/adminAuth'
import { adminSignupStatsApi, type SignupStats } from '@/lib/api'
import { SIGNUP_SOURCE_LABEL, SIGNUP_SOURCE_OPTIONS, SIGNUP_SOURCE_COLOR, signupSourceText } from '@/lib/signupSource'

// 推廣連結產生器：純前端工具頁，不呼叫任何後端 API。
// 產生帶 utm_source（+ 選填 utm_medium/campaign/content）的連結，貼到各社群通路個人資料/貼文，
// 讓會員經此連結註冊時，後端 attribution（migration 147 + internal/attribution/classify.go）自動歸類來源。
// 別名清單需與後端 classify.go 保持同步：facebook/instagram/line/google(adwords)/threads/tiktok/x(twitter)/youtube/dcard/ptt。

const BASE_URL = 'https://www.dor.tw/'

const CHANNELS: { label: string; params: [string, string][] }[] = [
  { label: 'Facebook', params: [['utm_source', 'facebook']] },
  { label: 'Instagram', params: [['utm_source', 'instagram']] },
  { label: 'Threads', params: [['utm_source', 'threads']] },
  { label: 'LINE', params: [['utm_source', 'line']] },
  { label: 'X／Twitter', params: [['utm_source', 'x']] },
  { label: 'YouTube', params: [['utm_source', 'youtube']] },
  { label: 'TikTok', params: [['utm_source', 'tiktok']] },
  { label: 'Dcard', params: [['utm_source', 'dcard']] },
  { label: 'PTT', params: [['utm_source', 'ptt']] },
  { label: 'Google 廣告', params: [['utm_source', 'google'], ['utm_medium', 'cpc']] },
]

function buildUrl(params: [string, string][]): string {
  const qs = params.filter(([, v]) => v.trim()).map(([k, v]) => `${k}=${encodeURIComponent(v.trim())}`).join('&')
  return qs ? `${BASE_URL}?${qs}` : BASE_URL
}

// utm_source 正規化：只允許英數字、- 、_，並強制小寫（後端 classify.go 比對也是小寫）。
function normalizeSource(v: string): string {
  return v.toLowerCase().replace(/[^a-z0-9_-]/g, '')
}

export default function AdminPromoLinksPage() {
  const router = useRouter()
  const [source, setSource] = useState('')
  const [medium, setMedium] = useState('')
  const [campaign, setCampaign] = useState('')
  const [content, setContent] = useState('')
  const [copied, setCopied] = useState<Record<string, boolean>>({})
  const [stats, setStats] = useState<SignupStats | null>(null)
  const [statsErr, setStatsErr] = useState('')

  useEffect(() => {
    const t = getToken()
    if (!t) router.replace('/admin/login')
  }, [router])

  // 成效統計：各通路週別趨勢＋彙總（見後端 internal/profile/signup_stats.go）
  useEffect(() => {
    const t = getToken()
    if (!t) return
    adminSignupStatsApi.get(t).then((d) => { setStats(d); setStatsErr('') }).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
      else setStatsErr(e?.message || '載入失敗')
    })
  }, [router])

  const customUrl = useMemo(() => {
    if (!source.trim()) return null
    return buildUrl([
      ['utm_source', source],
      ['utm_medium', medium],
      ['utm_campaign', campaign],
      ['utm_content', content],
    ])
  }, [source, medium, campaign, content])

  function copy(key: string, text: string) {
    navigator.clipboard.writeText(text)
      .then(() => {
        setCopied((c) => ({ ...c, [key]: true }))
        setTimeout(() => setCopied((c) => ({ ...c, [key]: false })), 1800)
      })
      .catch(() => {})
  }

  return (
    <div style={{ maxWidth: 960, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>推廣連結</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 20px', lineHeight: 1.7 }}>
        新會員經此連結進站註冊，會員管理的「來源」欄會自動歸類。
      </p>

      {/* ───────── 預設通路 ───────── */}
      <h2 style={{ fontSize: 16, fontWeight: 800, margin: '0 0 10px' }}>預設通路</h2>
      <div style={{ ...card, padding: 0, overflow: 'hidden' }}>
        <div style={{ ...gridRow, background: 'var(--bg-1)', color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.06em', textTransform: 'uppercase' }}>
          <div>通路</div>
          <div>連結</div>
          <div />
        </div>
        {CHANNELS.map((c) => {
          const url = buildUrl(c.params)
          return (
            <div key={c.label} style={{ ...gridRow, borderTop: '1px solid var(--line)' }}>
              <div style={{ fontSize: 13.5, fontWeight: 700 }}>{c.label}</div>
              <div style={{ fontSize: 12.5, fontFamily: 'var(--font-mono, monospace)', color: 'var(--tx-dim)', wordBreak: 'break-all' }}>
                {url}
              </div>
              <div>
                <button onClick={() => copy(c.label, url)} style={copied[c.label] ? copiedBtn : ghostBtn}>
                  {copied[c.label] ? '已複製' : '複製'}
                </button>
              </div>
            </div>
          )
        })}
      </div>

      {/* ───────── 自訂產生器 ───────── */}
      <h2 style={{ fontSize: 16, fontWeight: 800, margin: '28px 0 10px' }}>自訂產生器</h2>
      <div style={card}>
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
          <Field label="utm_source（必填）">
            <input
              style={{ ...inp, width: 200 }}
              value={source}
              onChange={(e) => setSource(normalizeSource(e.target.value))}
              placeholder="如 partner_abc"
            />
          </Field>
          <Field label="utm_medium（選填）">
            <input style={{ ...inp, width: 160 }} value={medium} onChange={(e) => setMedium(e.target.value)} placeholder="如 social" />
          </Field>
          <Field label="utm_campaign（選填）">
            <input style={{ ...inp, width: 180 }} value={campaign} onChange={(e) => setCampaign(e.target.value)} placeholder="如 2026_summer" />
          </Field>
        </div>
        <div style={{ marginTop: 10, maxWidth: 320 }}>
          <Field label="utm_content（選填）">
            <input style={inp} value={content} onChange={(e) => setContent(e.target.value)} placeholder="如 bio_link" />
          </Field>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4, lineHeight: 1.6 }}>
            用於 A/B 測試——同一來源不同 content 可區分版本，會員詳情頁可見完整 UTM。
          </div>
        </div>

        <div style={{ marginTop: 16, paddingTop: 14, borderTop: '1px solid var(--line)' }}>
          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 6 }}>連結預覽</div>
          {customUrl ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <div style={{ fontSize: 12.5, fontFamily: 'var(--font-mono, monospace)', color: 'var(--tx)', wordBreak: 'break-all', flex: 1, minWidth: 200 }}>
                {customUrl}
              </div>
              <button onClick={() => copy('custom', customUrl)} style={copied.custom ? copiedBtn : primaryBtn}>
                {copied.custom ? '已複製' : '複製'}
              </button>
            </div>
          ) : (
            <div style={{ fontSize: 12.5, color: 'var(--tx-faint)' }}>請先填寫 utm_source</div>
          )}
        </div>
      </div>

      {/* ───────── 附註說明 ───────── */}
      <h2 style={{ fontSize: 16, fontWeight: 800, margin: '28px 0 10px' }}>歸類規則說明</h2>
      <div style={{ ...card, fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.9 }}>
        <p style={{ margin: '0 0 8px' }}>
          後台會依 <code style={codeInline}>utm_source</code> 自動對映下列專屬標籤（不分大小寫）：
          <b style={{ color: 'var(--tx)' }}> facebook、instagram、line、google、threads、tiktok、x（或 twitter）、youtube、dcard、ptt</b>。
        </p>
        <p style={{ margin: '0 0 8px' }}>
          若未帶 <code style={codeInline}>utm_source</code>，系統會改依 referrer 網域判斷，其中 threads.net → threads、tiktok.com → tiktok 也會被自動歸類。
        </p>
        <p style={{ margin: 0 }}>
          自訂的 <code style={codeInline}>utm_source</code> 值若不在上述清單，後台會顯示為「其他（你填的值）」；完整 UTM 參數（source／medium／campaign／content）仍會保留，可在該會員的詳情頁查看。
        </p>
      </div>

      {/* ───────── 成效統計 ───────── */}
      <h2 style={{ fontSize: 16, fontWeight: 800, margin: '28px 0 10px' }}>📈 成效統計</h2>
      {statsErr && <div style={{ color: 'var(--hunt)', marginBottom: 12, fontSize: 13 }}>{statsErr}</div>}
      {!stats && !statsErr && <div style={{ fontSize: 12.5, color: 'var(--tx-faint)' }}>載入中…</div>}
      {stats && <SignupStatsSection stats={stats} />}
    </div>
  )
}

// 各通路成效統計：近 12 週堆疊長條圖（無外部圖表庫，div/flex 手刻）＋各來源彙總表。
// 資料來源：GET /admin/signup-stats（見後端 internal/profile/signup_stats.go）。
function SignupStatsSection({ stats }: { stats: SignupStats }) {
  const chartH = 150
  const sourceOrder = SIGNUP_SOURCE_OPTIONS.map((o) => o.value)
  const weekTotal = (counts: Record<string, number>) => Object.values(counts).reduce((a, b) => a + b, 0)
  const maxWeek = Math.max(0, ...stats.weekly.map((w) => weekTotal(w.counts)))
  // 只在圖例列出近 12 週內實際出現過的來源（避免 13 種來源全列造成圖例過長；未出現的來源本來
  // 就不會在堆疊圖中畫出任何色塊）。
  const presentSources = sourceOrder.filter((s) => stats.weekly.some((w) => (w.counts[s] || 0) > 0))

  return (
    <>
      <div style={card}>
        <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', marginBottom: 2 }}>各通路週別註冊趨勢</div>
        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 14 }}>近 12 週、每週一根直條，依來源堆疊（台灣時區，週一起算）</div>

        {maxWeek === 0 ? (
          <div style={{ fontSize: 12.5, color: 'var(--tx-faint)', padding: '20px 0', textAlign: 'center' }}>尚無註冊資料</div>
        ) : (
          <>
            <div style={{ display: 'flex', alignItems: 'flex-end', gap: 4, height: chartH, borderBottom: '1px solid var(--line)' }}>
              {stats.weekly.map((w) => {
                const total = weekTotal(w.counts)
                return (
                  <div key={w.week_start} style={{ flex: '1 1 0', display: 'flex', flexDirection: 'column', alignItems: 'stretch', height: '100%', justifyContent: 'flex-end' }}>
                    <div style={{ display: 'flex', flexDirection: 'column-reverse', width: '100%' }} title={`${w.week_start} 起：共 ${total} 人`}>
                      {sourceOrder.map((s) => {
                        const n = w.counts[s] || 0
                        if (n <= 0) return null
                        const h = Math.max(2, (n / maxWeek) * chartH)
                        return (
                          <div
                            key={s}
                            title={`${SIGNUP_SOURCE_LABEL[s]} ${n} 人`}
                            style={{ height: h, background: SIGNUP_SOURCE_COLOR[s], borderTop: '1px solid var(--bg-1)', cursor: 'default' }}
                          />
                        )
                      })}
                    </div>
                  </div>
                )
              })}
            </div>
            <div style={{ display: 'flex', gap: 4, marginTop: 4 }}>
              {stats.weekly.map((w) => (
                <div key={w.week_start} style={{ flex: '1 1 0', textAlign: 'center', fontSize: 9, color: 'var(--tx-faint)' }}>
                  {w.week_start.slice(5).replace('-', '/')}
                </div>
              ))}
            </div>

            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, marginTop: 16, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
              {presentSources.map((s) => (
                <div key={s} style={{ display: 'flex', alignItems: 'center', gap: 5, fontSize: 11.5, color: 'var(--tx-dim)' }}>
                  <span style={{ width: 10, height: 10, borderRadius: 3, background: SIGNUP_SOURCE_COLOR[s], display: 'inline-block', flexShrink: 0 }} />
                  {SIGNUP_SOURCE_LABEL[s]}
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      <div style={{ ...card, marginTop: 14, padding: 0, overflow: 'hidden' }}>
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12.5, minWidth: 420 }}>
            <thead>
              <tr style={{ background: 'var(--bg-1)' }}>
                <th style={{ ...thStyle, textAlign: 'left' }}>來源</th>
                <th style={thStyle}>近 7 天</th>
                <th style={thStyle}>近 30 天</th>
                <th style={thStyle}>累計</th>
              </tr>
            </thead>
            <tbody>
              {stats.totals.length === 0 && (
                <tr><td colSpan={4} style={{ padding: '16px 12px', textAlign: 'center', fontSize: 12.5, color: 'var(--tx-faint)' }}>尚無資料</td></tr>
              )}
              {stats.totals.map((t, i) => (
                <tr key={`${t.source}-${t.utm_source}-${i}`} style={{ borderTop: '1px solid var(--line)' }}>
                  <td style={tdStyle}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      <span style={{ width: 9, height: 9, borderRadius: 2, background: SIGNUP_SOURCE_COLOR[t.source] || 'var(--tx-faint)', display: 'inline-block', flexShrink: 0 }} />
                      <span style={{ fontWeight: 700, color: 'var(--tx)' }}>{signupSourceText(t.source, null, t.utm_source || null)}</span>
                    </div>
                  </td>
                  <td style={{ ...tdStyle, textAlign: 'right', color: 'var(--tx-dim)' }}>{t.c7}</td>
                  <td style={{ ...tdStyle, textAlign: 'right', color: 'var(--tx-dim)' }}>{t.c30}</td>
                  <td style={{ ...tdStyle, textAlign: 'right', fontWeight: 800, color: 'var(--tx)' }}>{t.total}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{label}</span>
      {children}
    </label>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16 }
const thStyle: React.CSSProperties = { padding: '10px 12px', textAlign: 'right', fontSize: 11, letterSpacing: '.06em', textTransform: 'uppercase', color: 'var(--tx-faint)', fontWeight: 700 }
const tdStyle: React.CSSProperties = { padding: '9px 12px' }
const gridRow: React.CSSProperties = { display: 'grid', gridTemplateColumns: '140px 1fr 90px', gap: 12, alignItems: 'center', padding: '10px 16px' }
const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit', width: '100%', boxSizing: 'border-box' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '8px 16px', cursor: 'pointer', fontSize: 13 }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 12.5, width: '100%' }
const copiedBtn: React.CSSProperties = { ...ghostBtn, color: 'var(--gold)', borderColor: 'var(--gold)' }
const codeInline: React.CSSProperties = { fontFamily: 'var(--font-mono, monospace)', fontSize: 12, background: 'var(--bg-2)', padding: '1px 5px', borderRadius: 4 }
