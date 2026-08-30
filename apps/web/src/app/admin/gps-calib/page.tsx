'use client'

// 後台「GPS 校正紀錄」：全站校正概況列表 → 點會員展開詳情（摘要 / 來源數據比對 / 歷史校正變化 / 管理操作）。
// 資料全部來自 internal/gpscalib 的兩個 admin 端點（GET /admin/gps-calib、GET /admin/gps-calib/{user_id}），
// 權限沿用 members（main.go 的 Mount 已覆蓋兩者）。圖表比照 admin/analytics 慣例：頁內自含手刻 inline SVG，
// 不引入圖表套件。所有表格都包在 overflowX:'auto' 的容器裡，手機寬度下橫向捲動而非撐破版面。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminGpsCalibApi, type GpsCalibRow, type GpsCalibInfo, type GpsCalibPair, type GpsCalibLogEntry } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

const PAGE = 50

// 係數容許區間（後端 estimator.go ClampLo/ClampHi），同時是折線圖的 y 軸定域與凍結輸入的驗證範圍。
const CLAMP_LO = 0.92
const CLAMP_HI = 1.0

const STATUS_LABEL: Record<string, string> = {
  warming: '暖機中',
  active: '校正中',
  unstable: '資料不一致',
  stale: '已過期',
  frozen: '後台鎖定',
}
const STATUS_COLOR: Record<string, string> = {
  warming: 'var(--tx-dim)',
  active: 'var(--fug)',
  unstable: 'var(--gold)',
  stale: 'var(--tx-faint)',
  frozen: 'var(--gold)',
}
const STATUS_HELP: Record<string, string> = {
  warming: '樣本還不足以下結論，係數維持 1.0（不校正）。',
  active: '已估出穩定係數並實際套用在新上傳的跑步距離上。',
  unstable: '配對之間離散度過大，暫不套用校正，係數維持 1.0。',
  stale: '太久沒有新的手錶紀錄可比對，已退回原始值；有新資料就會自動恢復。',
  frozen: '由後台手動鎖定係數，系統重算不會覆蓋。',
}

// 配對被拒的原因（estimator.go gateOne 的 switch 分支，逐一對照）。
const REJECT_LABEL: Record<string, string> = {
  flagged: '該筆跑步已被標記為跨帳號重複',
  other_source: '非目前參考來源的裝置',
  ambiguous: '配對不唯一（一對多）',
  partial: '時長差異過大（不是同一趟）',
  short: '距離太短，誤差放大',
  edge: '起訖時間差過大',
  range: '距離比值超出合理範圍',
  superseded: '已被「重設」作廢',
}

// 校正「沒有生效」的原因（後端 not_apply_reason，見 gpscalib effectiveState）。列表用短標籤、
// 詳情用完整句子。這組是本頁最重要的資訊：Recompute 是影子模式（對全體會員無條件執行），非白名單
// 會員一樣會被算出 status='active'、factor=0.97xx，但他的距離一公里都沒被校正過。
const NOT_APPLY_SHORT: Record<string, string> = {
  entry: '影子模式',
  no_data: '無資料',
  disabled: '使用者關閉',
  status: '未達門檻',
  stale: '已過期',
}
const NOT_APPLY_LABEL: Record<string, string> = {
  entry: '前台入口尚未開放給這位會員（未在 GPS 校正白名單）——系統照常在背景估計，但不套用到任何距離。',
  no_data: '尚未有過任何候選配對，沒有可用的係數。',
  disabled: '這位會員自己在個人資料頁把「GPS 距離校正」關掉了。',
  status: '狀態未達啟用門檻（暖機中／資料不一致），係數一律 1.0。',
  stale: '太久沒有新的手錶紀錄可比對，係數已退回 1.0；有新資料就會自動恢復。',
}

// 校正紀錄的變動原因（user_gps_calib_log.reason）。
const REASON_LABEL: Record<string, string> = {
  recompute: '系統重算',
  enable: '使用者啟用',
  disable: '使用者關閉',
  reset: '重設',
  admin_freeze: '後台凍結',
  admin_unfreeze: '後台解凍',
}

const SOURCE_LABEL: Record<string, string> = { strava: 'Strava', garmin: 'Garmin', coros: 'COROS' }

function errInfo(e: unknown): { status?: number; message?: string } {
  return (e ?? {}) as { status?: number; message?: string }
}

const p2 = (n: number) => String(n).padStart(2, '0')
function fmtDateTime(iso?: string) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  return `${d.getFullYear()}/${p2(d.getMonth() + 1)}/${p2(d.getDate())} ${p2(d.getHours())}:${p2(d.getMinutes())}`
}
function fmtShortDt(iso?: string) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  return `${d.getMonth() + 1}/${d.getDate()} ${p2(d.getHours())}:${p2(d.getMinutes())}`
}
// 係數一律 4 位小數，與站內信文案的 %.4f、後端 round 口徑一致。
const fmtFactor = (f: number) => `×${(f ?? 1).toFixed(4)}`
// 係數換算成「距離被縮短幾 %」，比 ×0.9778 直觀。
const fmtShrink = (f: number) => `${(1 - (f ?? 1)) * 100 >= 0 ? '−' : '+'}${Math.abs((1 - (f ?? 1)) * 100).toFixed(2)}%`
const fmtSigned = (n: number, digits = 2) => `${n >= 0 ? '+' : '−'}${Math.abs(n).toFixed(digits)}`
// 誤差著色：越接近 0 越好。±1% 內綠、±3% 內黃、其餘紅。
function diffColor(pct: number) {
  const a = Math.abs(pct)
  if (a <= 1) return 'var(--fug)'
  if (a <= 3) return 'var(--gold)'
  return 'var(--hunt)'
}

export default function AdminGpsCalibPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [items, setItems] = useState<GpsCalibRow[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)
  const [statusF, setStatusF] = useState('')
  const [err, setErr] = useState('')

  const [sel, setSel] = useState<GpsCalibRow | null>(null)
  const [detail, setDetail] = useState<GpsCalibInfo | null>(null)
  const [detailErr, setDetailErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [fzFactor, setFzFactor] = useState('')
  const detailRef = useRef<HTMLDivElement | null>(null)

  const load = useCallback((t: string, off: number, status: string) => {
    setErr('')
    adminGpsCalibApi.list(t, { limit: PAGE, offset: off, status: status || undefined })
      .then((r) => { setItems(r.items); setTotal(r.total) })
      .catch((e) => {
        const x = errInfo(e)
        // 對抗式審查修正（low finding）：失敗時 items 必須離開 null，否則表格區永遠停在
        // 「載入中…」，畫面同時出現紅色錯誤訊息與載入中，看起來像還在跑而不是已經失敗。
        setItems([]); setTotal(0)
        if (x.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (x.status === 403) setErr('權限不足，無法檢視此頁')
        else setErr(x.message || '載入失敗')
      })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    load(t, 0, '')
  }, [router, load])

  const loadDetail = useCallback(async (t: string, userID: string) => {
    setDetailErr('')
    try {
      const d = await adminGpsCalibApi.get(t, userID)
      setDetail(d)
      setFzFactor(d.factor.toFixed(4))
    } catch (e) {
      const x = errInfo(e)
      setDetail(null)
      setDetailErr(x.status === 403 ? '權限不足，無法檢視此會員的校正紀錄' : (x.message || '載入詳情失敗'))
    }
  }, [])

  function openDetail(row: GpsCalibRow) {
    if (!token) return
    setSel(row)
    setDetail(null)
    setMsg('')
    void loadDetail(token, row.user_id)
  }
  // 對抗式審查修正（low finding）：後台外殼實際捲動的是 AdminShell 主內容區那個 overflowY:'auto'
  // 的 div（root 是 height:100vh），document/window 從不產生捲軸，所以 window.scrollTo 是 no-op；
  // 列表長時點下面的列，詳情面板（渲染在列表上方）會開在畫面外，看起來像沒反應。改用
  // scrollIntoView，由瀏覽器自己找最近的可捲動祖先。
  useEffect(() => {
    if (sel) detailRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [sel])

  function changeStatus(v: string) {
    setStatusF(v)
    setOffset(0)
    if (token) load(token, 0, v)
  }
  function page(off: number) {
    setOffset(off)
    if (token) load(token, off, statusF)
  }

  // 管理操作共用流程：確認 → 呼叫 → 重新載入詳情與列表（列表的係數/狀態也會跟著變）。
  async function runOp(label: string, fn: () => Promise<unknown>) {
    if (!token || !sel) return
    if (!window.confirm(`確定要對「${sel.name}」執行「${label}」嗎？`)) return
    setBusy(true); setMsg(''); setDetailErr('')
    try {
      await fn()
      setMsg(`${label} 已完成`)
      await loadDetail(token, sel.user_id)
      load(token, offset, statusF)
    } catch (e) {
      const x = errInfo(e)
      setDetailErr(x.message || `${label} 失敗`)
    } finally {
      setBusy(false)
    }
  }

  function doFreeze() {
    const f = Number(fzFactor)
    if (!isFinite(f) || f < CLAMP_LO || f > CLAMP_HI) {
      setDetailErr(`係數必須介於 ${CLAMP_LO.toFixed(2)} ~ ${CLAMP_HI.toFixed(2)}`)
      return
    }
    void runOp(`凍結係數 ×${f.toFixed(4)}`, () => adminGpsCalibApi.freeze(token!, sel!.user_id, f))
  }

  return (
    <div style={{ maxWidth: 980, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>GPS 校正紀錄</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, marginTop: 0, lineHeight: 1.6 }}>
        以會員連接的手錶（Strava／Garmin／COROS）活動為參考，估計 App GPS 的系統性偏差。係數只准向下修正
        （{CLAMP_LO.toFixed(2)}～{CLAMP_HI.toFixed(2)}）且只向前生效，不回溯改寫既有紀錄。<strong>「生效係數」</strong>
        才是目前真的乘在新上傳距離上的值；<strong>「估計係數」</strong>是系統在背景算出來的結果，入口未開放、
        會員自己關閉、或狀態未達門檻時只算不套（生效係數為 1.0000）。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '10px 0', fontSize: 13 }}>{err}</div>}

      <div ref={detailRef} />
      {sel && (
        <DetailPanel
          row={sel}
          detail={detail}
          detailErr={detailErr}
          msg={msg}
          busy={busy}
          fzFactor={fzFactor}
          setFzFactor={setFzFactor}
          onClose={() => { setSel(null); setDetail(null); setDetailErr(''); setMsg('') }}
          onFreeze={doFreeze}
          onUnfreeze={() => void runOp('解凍（解除鎖定並立即重算）', () => adminGpsCalibApi.unfreeze(token!, sel.user_id))}
          onReset={() => void runOp('重設（清空係數與歷史配對）', () => adminGpsCalibApi.reset(token!, sel.user_id))}
        />
      )}

      {/* ── 篩選 ── */}
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', margin: '14px 0 12px' }}>
        <label style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>狀態</label>
        <select value={statusF} onChange={(e) => changeStatus(e.target.value)} style={{ ...ctrlInp, width: 160 }}>
          <option value="">全部</option>
          {Object.keys(STATUS_LABEL).map((k) => <option key={k} value={k}>{STATUS_LABEL[k]}</option>)}
        </select>
        <button onClick={() => token && load(token, offset, statusF)} style={ghost}>重新整理</button>
      </div>

      {/* ── 全站列表 ── */}
      <div style={{ border: '1px solid var(--line)', borderRadius: 12, overflowX: 'auto' }}>
        <div style={{ minWidth: LIST_MIN_WIDTH }}>
          <div style={{ ...listRow, ...headRow }}>
            <span>會員</span>
            <span>帳號編碼</span>
            <span style={{ textAlign: 'right' }}>生效係數</span>
            <span style={{ textAlign: 'right' }}>估計係數</span>
            <span>是否生效</span>
            <span>狀態</span>
            <span>參考來源</span>
            <span style={{ textAlign: 'right' }}>採用配對</span>
            <span style={{ textAlign: 'right' }}>n_eff</span>
            <span style={{ textAlign: 'right' }}>離散度 σ</span>
            <span>最後配對</span>
            <span>最後計算</span>
          </div>
          {!items && <div style={{ padding: 16, color: 'var(--tx-dim)', fontSize: 13 }}>載入中…</div>}
          {items && items.length === 0 && (
            <div style={{ padding: 16, color: 'var(--tx-dim)', fontSize: 13 }}>
              目前沒有校正紀錄{statusF ? `（狀態＝${STATUS_LABEL[statusF]}）` : '（還沒有任何會員產生過配對資料）'}
            </div>
          )}
          {items?.map((m) => (
            <button
              key={m.user_id}
              onClick={() => openDetail(m)}
              style={{
                ...listRow,
                width: '100%', textAlign: 'left', background: sel?.user_id === m.user_id ? 'var(--bg-2)' : 'transparent',
                color: 'inherit', font: 'inherit', fontSize: 13, cursor: 'pointer', border: 'none',
                borderBottom: '1px solid var(--line)',
              }}
            >
              <span style={{ minWidth: 0 }}>
                <span style={{ display: 'block', fontWeight: 700, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{m.name || '—'}</span>
                <span style={{ display: 'block', fontSize: 11, color: 'var(--tx-faint)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={m.email}>{m.email || '—'}</span>
              </span>
              <span style={{ fontFamily: 'monospace', fontSize: 12, color: 'var(--tx-dim)' }}>{m.account_code || '—'}</span>
              <span style={{ textAlign: 'right', fontWeight: 800, fontFamily: 'monospace', color: m.applied ? 'var(--fug)' : 'var(--tx)' }}>
                {fmtFactor(m.effective_factor)}
              </span>
              <span style={{ textAlign: 'right', fontFamily: 'monospace', color: 'var(--tx-dim)' }}>{fmtFactor(m.factor)}</span>
              <span
                style={{ color: m.applied ? 'var(--fug)' : 'var(--gold)', fontWeight: 700, fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                title={m.applied ? '校正實際套用中' : (NOT_APPLY_LABEL[m.not_apply_reason || ''] || '未套用校正')}
              >
                {m.applied ? '✓ 生效中' : `✗ ${NOT_APPLY_SHORT[m.not_apply_reason || ''] || '未生效'}`}
              </span>
              <span style={{ color: STATUS_COLOR[m.status] || 'var(--tx)', fontWeight: 700 }}>{STATUS_LABEL[m.status] || m.status}</span>
              <span style={{ color: 'var(--tx-dim)' }}>{SOURCE_LABEL[m.ref_source] || m.ref_source || '—'}</span>
              <span style={{ textAlign: 'right' }}>{m.n_pairs}</span>
              <span style={{ textAlign: 'right', color: 'var(--tx-dim)' }}>{m.n_eff.toFixed(1)}</span>
              <span style={{ textAlign: 'right', color: 'var(--tx-dim)' }}>{m.sigma.toFixed(4)}</span>
              <span style={{ color: 'var(--tx-faint)', fontSize: 12 }}>{fmtShortDt(m.last_pair_at)}</span>
              <span style={{ color: 'var(--tx-faint)', fontSize: 12 }}>{fmtShortDt(m.computed_at)}</span>
            </button>
          ))}
        </div>
      </div>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 6, lineHeight: 1.5 }}>
        口徑：只列出已經產生過校正資料的會員（排除虛擬選手），依「最後計算時間」新到舊。
        「生效係數」＝GPS 上傳當下實際乘上去的值，已含四道閘門（前台入口未開放／會員自己關閉／狀態未達門檻／太久沒新配對，
        任一成立即為 1.0000）；「估計係數」是系統在背景算出來的值——重算對全體會員無條件執行，所以未開放的會員也會有估計值，
        兩欄不同時以「生效係數」為準。「採用配對」＝上次重算時估計視窗（最近 120 天內最多 20 組）內真正採納的組數；
        σ 為配對比值取對數後的離散度，越小代表越一致。
      </div>

      {/* 分頁 */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 12, fontSize: 13, color: 'var(--tx-dim)' }}>
        <span>共 {total} 筆 · 第 {Math.floor(offset / PAGE) + 1} / {Math.max(1, Math.ceil(total / PAGE))} 頁</span>
        <div style={{ display: 'flex', gap: 8 }}>
          <button disabled={offset <= 0} onClick={() => page(Math.max(0, offset - PAGE))} style={{ ...pgBtn, opacity: offset <= 0 ? 0.4 : 1 }}>上一頁</button>
          <button disabled={offset + PAGE >= total} onClick={() => page(offset + PAGE)} style={{ ...pgBtn, opacity: offset + PAGE >= total ? 0.4 : 1 }}>下一頁</button>
        </div>
      </div>
    </div>
  )
}

// ── 詳情面板 ──────────────────────────────────────────────────────────────

function DetailPanel(props: {
  row: GpsCalibRow
  detail: GpsCalibInfo | null
  detailErr: string
  msg: string
  busy: boolean
  fzFactor: string
  setFzFactor: (v: string) => void
  onClose: () => void
  onFreeze: () => void
  onUnfreeze: () => void
  onReset: () => void
}) {
  const { row, detail, detailErr, msg, busy, fzFactor, setFzFactor, onClose, onFreeze, onUnfreeze, onReset } = props
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16, marginBottom: 18 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 12, marginBottom: 12, flexWrap: 'wrap' }}>
        <div style={{ minWidth: 0 }}>
          <strong style={{ fontSize: 17 }}>{row.name || '—'}</strong>
          <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>
            {row.email || '—'}{row.account_code ? ` · #${row.account_code}` : ''}
          </div>
        </div>
        <button onClick={onClose} style={ghost}>關閉</button>
      </div>

      {detailErr && <div style={{ color: 'var(--hunt)', fontSize: 13, padding: '6px 0' }}>{detailErr}</div>}
      {msg && <div style={{ color: 'var(--fug)', fontSize: 13, padding: '6px 0' }}>{msg}</div>}
      {!detail && !detailErr && <div style={{ color: 'var(--tx-dim)', fontSize: 13, padding: '10px 0' }}>載入中…</div>}

      {detail && (
        <>
          <SummaryCard d={detail} />
          <PairsSection
            pairs={detail.pairs}
            effectiveFactor={detail.effective_factor}
            applied={detail.applied}
            notApplyReason={detail.not_apply_reason}
          />
          <HistorySection log={detail.log} />
          <OpsSection
            d={detail} busy={busy} fzFactor={fzFactor} setFzFactor={setFzFactor}
            onFreeze={onFreeze} onUnfreeze={onUnfreeze} onReset={onReset}
          />
        </>
      )}
    </div>
  )
}

// B1 摘要卡
function SummaryCard({ d }: { d: GpsCalibInfo }) {
  return (
    <SectionCard title="目前狀態">
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10 }}>
        <StatTile label="生效係數（實際入帳）" value={fmtFactor(d.effective_factor)} color={d.applied ? 'var(--fug)' : 'var(--tx)'} />
        <StatTile label="估計係數（背景計算）" value={fmtFactor(d.factor)} color="var(--tx-dim)" />
        <StatTile
          label="是否生效"
          value={d.applied ? '生效中' : (NOT_APPLY_SHORT[d.not_apply_reason || ''] || '未生效')}
          color={d.applied ? 'var(--fug)' : 'var(--gold)'}
        />
        <StatTile label="距離修正幅度" value={fmtShrink(d.effective_factor)} />
        <StatTile label="狀態" value={STATUS_LABEL[d.status] || d.status} color={STATUS_COLOR[d.status]} />
        <StatTile label="有效樣本 n_eff" value={d.n_eff.toFixed(2)} />
        <StatTile label="離散度 σ" value={d.sigma.toFixed(4)} />
        <StatTile label="參考來源" value={SOURCE_LABEL[d.ref_source] || d.ref_source || '—'} />
        <StatTile label="係數版本" value={`v${d.version}`} />
        <StatTile label="使用者開關" value={d.enabled ? '啟用中' : '已關閉'} color={d.enabled ? 'var(--tx)' : 'var(--tx-faint)'} />
      </div>

      {/* 估計了但沒生效：本頁最容易被誤讀的情況，必須明講（影子模式／使用者關閉／未達門檻／過期）。 */}
      {!d.applied && (
        <div style={{ fontSize: 11.5, color: 'var(--gold)', marginTop: 10, lineHeight: 1.6, border: '1px solid rgba(212,175,55,.35)', borderRadius: 8, padding: '8px 10px' }}>
          <strong>目前沒有在校正</strong>：實際入帳係數為 {fmtFactor(d.effective_factor)}。
          {NOT_APPLY_LABEL[d.not_apply_reason || ''] || ''}
          {d.factor < 1 && ` 上面的「估計係數 ${fmtFactor(d.factor)}」只是背景計算結果，這位會員的距離沒有被改過。`}
        </div>
      )}

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 18, marginTop: 10, fontSize: 12, color: 'var(--tx-dim)' }}>
        <span>最後配對：{fmtDateTime(d.last_pair_at)}</span>
        <span>最後計算：{fmtDateTime(d.computed_at)}</span>
        <span>前台卡片：{d.entry === 'shown' ? '看得到' : d.entry === 'locked' ? '顯示但鎖定' : '隱藏'}</span>
        <span>套用入口：{d.apply_entry === 'shown' ? '已開放' : d.apply_entry === 'locked' ? '鎖定（只算不套）' : '未開放（只算不套）'}</span>
      </div>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 8, lineHeight: 1.5 }}>
        {STATUS_HELP[d.status] || ''}
        「前台卡片」是可見性（超管恆可見）；「套用入口」才是決定校正是否真的動到距離的那一道（不含超管旁路）。
        重算對全體會員無條件執行，因此未開放的會員也會有估計係數。
      </div>
    </SectionCard>
  )
}

// B2 來源數據比對
const PAIR_COLS = '90px 60px 76px 96px 96px 76px 58px 112px 1fr 50px'
const PAIR_MIN_WIDTH = 1000

function PairsSection({ pairs, effectiveFactor, applied, notApplyReason }: {
  pairs: GpsCalibPair[]
  effectiveFactor: number
  applied: boolean
  notApplyReason?: string
}) {
  // 統計只看「真的進了估計視窗」的配對（in_window），不是 DB 的 accepted——後者不含視窗條件，
  // 第 21 組以後與超過 120 天的舊列仍是 accepted=TRUE，拿它統計會跟列表的「採用配對」對不上。
  const stats = useMemo(() => {
    const win = pairs.filter((p) => p.in_window)
    if (win.length === 0) return null
    let rawKm = 0, creditedKm = 0, backKm = 0, rawPct = 0, creditedPct = 0, backPct = 0
    for (const p of win) {
      rawKm += Math.abs(p.gps_km - p.ext_km)
      creditedKm += Math.abs(p.credited_km - p.ext_km)
      backKm += Math.abs(p.calib_km - p.ext_km)
      if (p.ext_km > 0) {
        rawPct += Math.abs(p.gps_km - p.ext_km) / p.ext_km
        creditedPct += Math.abs(p.credited_km - p.ext_km) / p.ext_km
        backPct += Math.abs(p.calib_km - p.ext_km) / p.ext_km
      }
    }
    const n = win.length
    return {
      n,
      rawKm: rawKm / n, creditedKm: creditedKm / n, backKm: backKm / n,
      rawPct: (rawPct / n) * 100, creditedPct: (creditedPct / n) * 100, backPct: (backPct / n) * 100,
    }
  }, [pairs])

  const inWindowN = pairs.filter((p) => p.in_window).length
  const acceptedN = pairs.filter((p) => p.accepted).length

  return (
    <SectionCard title="來源數據比對">
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 4 }}>
        <StatTile label="估計視窗採用組數" value={`${inWindowN} / ${pairs.length}`} unit={acceptedN > inWindowN ? `（通過閘門 ${acceptedN}）` : undefined} />
        {stats && <StatTile label="原始平均絕對誤差" value={stats.rawKm.toFixed(3)} unit={`km（${stats.rawPct.toFixed(2)}%）`} />}
        {stats && (
          <StatTile
            label="實際入帳平均絕對誤差"
            value={stats.creditedKm.toFixed(3)}
            unit={`km（${stats.creditedPct.toFixed(2)}%）`}
            color={stats.creditedKm <= stats.rawKm ? 'var(--fug)' : 'var(--tx)'}
          />
        )}
        {stats && (
          <StatTile
            label="回推校正後平均絕對誤差"
            value={stats.backKm.toFixed(3)}
            unit={`km（${stats.backPct.toFixed(2)}%）`}
            color={stats.backKm <= stats.rawKm ? 'var(--fug)' : 'var(--hunt)'}
          />
        )}
        {stats && (
          <StatTile
            label="回推可改善"
            value={stats.rawKm > 0 ? `${(((stats.rawKm - stats.backKm) / stats.rawKm) * 100).toFixed(1)}%` : '—'}
            color={stats.backKm <= stats.rawKm ? 'var(--fug)' : 'var(--hunt)'}
          />
        )}
      </div>

      {!applied && (
        <div style={{ fontSize: 11.5, color: 'var(--gold)', margin: '8px 0 0', lineHeight: 1.5 }}>
          目前生效係數＝1.0000（{NOT_APPLY_SHORT[notApplyReason || ''] || '未生效'}），因此下表「回推校正後」與「App 原始」相同、
          「回推可改善」為 0%——這是正確的：現在沒有在校正。
        </div>
      )}

      <div style={{ border: '1px solid var(--line)', borderRadius: 10, overflowX: 'auto', marginTop: 12 }}>
        <div style={{ minWidth: PAIR_MIN_WIDTH }}>
          <div style={{ ...pairRow, ...headRow }}>
            <span>活動時間</span>
            <span>來源</span>
            <span style={{ textAlign: 'right' }}>App 原始</span>
            <span style={{ textAlign: 'right' }}>實際入帳</span>
            <span style={{ textAlign: 'right' }}>回推校正後</span>
            <span style={{ textAlign: 'right' }}>手錶</span>
            <span style={{ textAlign: 'right' }}>比值</span>
            <span style={{ textAlign: 'right' }}>入帳−手錶</span>
            <span>採用／原因</span>
            <span style={{ textAlign: 'right' }}>權重</span>
          </div>
          {pairs.length === 0 && <div style={{ padding: 14, fontSize: 12, color: 'var(--tx-faint)' }}>尚無配對資料</div>}
          {pairs.map((p, i) => {
            const diffKm = p.credited_km - p.ext_km
            const diffPct = p.ext_km > 0 ? (diffKm / p.ext_km) * 100 : 0
            const rawDiff = p.gps_km - p.ext_km
            return (
              <div
                key={`${p.gps_run_id}-${p.ext_activity_id}-${i}`}
                style={{
                  ...pairRow,
                  background: p.in_window ? 'rgba(70,227,160,.06)' : 'transparent',
                  borderLeft: p.in_window ? '2px solid var(--fug)' : '2px solid transparent',
                  opacity: p.accepted ? 1 : 0.55,
                }}
              >
                <span
                  style={{ color: 'var(--tx-dim)', fontSize: 12 }}
                  title={`GPS 跑步 ${p.gps_run_id}\n外部活動 ${p.ext_activity_id}`}
                >
                  {fmtShortDt(p.activity_at)}
                </span>
                <span style={{ color: 'var(--tx-dim)', fontSize: 12 }}>{SOURCE_LABEL[p.ext_source] || p.ext_source || '—'}</span>
                <span style={{ textAlign: 'right', fontFamily: 'monospace' }} title={`與手錶差 ${fmtSigned(rawDiff)} km`}>{p.gps_km.toFixed(2)}</span>
                <span
                  style={{ textAlign: 'right', fontFamily: 'monospace', fontWeight: 800, color: p.credited_factor < 1 ? 'var(--fug)' : 'var(--tx)' }}
                  title={p.credited_factor < 1 ? `這趟入帳當下的係數 ${fmtFactor(p.credited_factor)}` : '這趟入帳當下沒有套校正（係數 ×1.0000）'}
                >
                  {p.credited_km.toFixed(2)}
                </span>
                <span style={{ textAlign: 'right', fontFamily: 'monospace', color: 'var(--tx-dim)' }} title="以目前生效係數回推的假設值，非實際入帳">
                  {p.calib_km.toFixed(2)}
                </span>
                <span style={{ textAlign: 'right', fontFamily: 'monospace' }}>{p.ext_km.toFixed(2)}</span>
                <span style={{ textAlign: 'right', fontFamily: 'monospace', color: 'var(--tx-dim)' }}>{p.gps_km > 0 ? p.ratio.toFixed(4) : '—'}</span>
                <span style={{ textAlign: 'right', fontFamily: 'monospace', color: diffColor(diffPct), fontWeight: 700 }}>
                  {fmtSigned(diffKm)}<span style={{ fontSize: 11, opacity: 0.8 }}> ({fmtSigned(diffPct, 1)}%)</span>
                </span>
                <span
                  style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: p.in_window ? 'var(--fug)' : 'var(--tx-faint)' }}
                  title={p.in_window ? '在估計視窗內，這次係數是用它算的' : (p.accepted ? '通過閘門，但不在估計視窗（最近 120 天內最多 20 組）內，沒有參與這次估計' : '')}
                >
                  {p.in_window ? '✓ 採用' : (p.accepted ? '通過但未進視窗' : (REJECT_LABEL[p.reject_reason || ''] || p.reject_reason || '未採用'))}
                </span>
                <span style={{ textAlign: 'right', fontFamily: 'monospace', color: 'var(--tx-faint)', fontSize: 12 }}>
                  {p.inlier_w != null ? p.inlier_w.toFixed(2) : '—'}
                </span>
              </div>
            )
          })}
        </div>
      </div>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 6, lineHeight: 1.5 }}>
        口徑：「App 原始」＝手機 GPS 未套校正的距離（永遠不會被改寫）；<strong>「實際入帳」</strong>＝這趟上傳當下真正記到帳上的
        距離（逐趟凍結在 gps_runs.calib_distance_km，係數會隨時間演進，舊的趟次多半是 ×1.0000）；<strong>「回推校正後」</strong>
        ＝拿<em>目前</em>生效係數（{fmtFactor(effectiveFactor)}）重算的假設值，只用來評估「現在這組係數會不會更貼近手錶」，
        <strong>不是</strong>實際入帳的距離。「手錶」＝外部來源距離，是本系統的參考真值；比值＝手錶÷App 原始。
        綠底列為<em>真的進了估計視窗</em>（最近 120 天內最多 20 組）的配對；「通過但未進視窗」是通過閘門但沒被這次估計用到，
        灰列為被閘門擋掉、附中文原因。權重＝最近一次重算的離群權重（越接近 0 越被視為離群，只有進視窗的才有）。
        三個平均絕對誤差都只統計視窗內的配對。最多顯示最近 200 組。
      </div>
    </SectionCard>
  )
}

// B3 歷史校正變化
const LOG_COLS = '132px 150px 96px 1fr 130px 54px'
const LOG_MIN_WIDTH = 760

function HistorySection({ log }: { log: GpsCalibLogEntry[] }) {
  // 後端依 created_at DESC 回傳；折線圖要時間軸由左到右，所以反轉一份。
  const series = useMemo(() => {
    return log
      .filter((l) => l.factor_after != null)
      .map((l) => ({ t: l.created_at, f: l.factor_after as number, version: l.version, reason: l.reason, actor: l.actor }))
      .reverse()
  }, [log])

  return (
    <SectionCard title="歷史校正變化">
      <div style={{ fontSize: 11.5, fontWeight: 700, color: 'var(--tx-dim)', marginBottom: 6 }}>係數軌跡</div>
      <FactorLine points={series} />
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 6, marginBottom: 12, lineHeight: 1.5 }}>
        口徑：y 軸固定為係數容許區間 {CLAMP_LO.toFixed(2)}～{CLAMP_HI.toFixed(2)}（系統只准向下修正，不會超出）。
        每個點是一次寫入紀錄後的係數，滑鼠移上去可看時間、版本與變動原因。
      </div>

      <div style={{ border: '1px solid var(--line)', borderRadius: 10, overflowX: 'auto' }}>
        <div style={{ minWidth: LOG_MIN_WIDTH }}>
          <div style={{ ...logRow, ...headRow }}>
            <span>時間</span>
            <span>係數變化</span>
            <span>狀態</span>
            <span>原因</span>
            <span>觸發者</span>
            <span style={{ textAlign: 'right' }}>版本</span>
          </div>
          {log.length === 0 && <div style={{ padding: 14, fontSize: 12, color: 'var(--tx-faint)' }}>尚無校正紀錄</div>}
          {log.map((l, i) => {
            const before = l.factor_before
            const after = l.factor_after
            const up = before != null && after != null && after !== before
            return (
              <div key={`${l.created_at}-${l.version}-${i}`} style={logRow}>
                <span style={{ color: 'var(--tx-dim)', fontSize: 12 }}>{fmtDateTime(l.created_at)}</span>
                <span style={{ fontFamily: 'monospace' }}>
                  <span style={{ color: 'var(--tx-faint)' }}>{before != null ? before.toFixed(4) : '—'}</span>
                  <span style={{ color: 'var(--tx-faint)', margin: '0 5px' }}>→</span>
                  <span style={{ fontWeight: 800, color: up ? (after! < before! ? 'var(--gold)' : 'var(--fug)') : 'var(--tx)' }}>
                    {after != null ? after.toFixed(4) : '—'}
                  </span>
                </span>
                <span style={{ color: STATUS_COLOR[l.status || ''] || 'var(--tx-dim)' }}>{STATUS_LABEL[l.status || ''] || l.status || '—'}</span>
                <span style={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{REASON_LABEL[l.reason] || l.reason}</span>
                <span style={{ color: 'var(--tx-dim)', fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={l.actor}>{actorLabel(l.actor)}</span>
                <span style={{ textAlign: 'right', color: 'var(--tx-faint)', fontSize: 12 }}>v{l.version}</span>
              </div>
            )
          })}
        </div>
      </div>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 6, lineHeight: 1.5 }}>
        口徑：每次係數或狀態實際變動才會留下一列（沒有新配對、重算結果不變時不寫）。最多顯示最近 200 筆。
      </div>
    </SectionCard>
  )
}

function actorLabel(actor: string) {
  if (actor === 'system') return '系統'
  if (actor === 'user') return '使用者本人'
  if (actor.startsWith('admin:')) return `後台管理員（${actor.slice(6, 14)}…）`
  return actor || '—'
}

// 係數折線圖（手刻 inline SVG，比照 admin/analytics 頁內自含慣例，不引入圖表套件）。
// y 軸定域固定在 [CLAMP_LO, CLAMP_HI]：係數本來就被夾在這個區間，從 0 起算會壓成一條平線看不出軌跡。
function FactorLine({ points }: { points: { t: string; f: number; version: number; reason: string; actor: string }[] }) {
  const w = 640, h = 150, padL = 40, padR = 12, padT = 12, padB = 24
  const innerW = w - padL - padR, innerH = h - padT - padB
  if (points.length === 0) return <div style={{ fontSize: 12, color: 'var(--tx-faint)', padding: '16px 0' }}>尚無資料</div>
  const n = points.length
  const y = (f: number) => padT + innerH * (1 - (Math.min(CLAMP_HI, Math.max(CLAMP_LO, f)) - CLAMP_LO) / (CLAMP_HI - CLAMP_LO))
  const x = (i: number) => (n === 1 ? padL + innerW / 2 : padL + (innerW * i) / (n - 1))
  const grid = [0.92, 0.94, 0.96, 0.98, 1.0]
  const labelEvery = Math.max(1, Math.ceil(n / 6))
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} style={{ display: 'block', overflow: 'visible' }}>
      {grid.map((g) => (
        <g key={g}>
          <line x1={padL} y1={y(g)} x2={w - padR} y2={y(g)} stroke="var(--line)" strokeWidth={1} strokeDasharray={g === 1 ? '' : '3 3'} />
          <text x={padL - 6} y={y(g) + 3} textAnchor="end" fontSize="9" fill="var(--tx-faint)">{g.toFixed(2)}</text>
        </g>
      ))}
      {n > 1 && (
        <polyline
          points={points.map((p, i) => `${x(i)},${y(p.f)}`).join(' ')}
          fill="none" stroke="var(--fug)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round"
        />
      )}
      {points.map((p, i) => (
        <g key={`${p.t}-${i}`}>
          <circle cx={x(i)} cy={y(p.f)} r={3.5} fill="var(--fug)" stroke="var(--bg-1)" strokeWidth={1.5}>
            <title>{fmtDateTime(p.t)}｜{fmtFactor(p.f)}｜v{p.version}｜{REASON_LABEL[p.reason] || p.reason}｜{actorLabel(p.actor)}</title>
          </circle>
          {(i % labelEvery === 0 || i === n - 1) && (
            <text x={x(i)} y={h - padB + 13} textAnchor="middle" fontSize="8" fill="var(--tx-faint)">{fmtShortDt(p.t)}</text>
          )}
        </g>
      ))}
    </svg>
  )
}

// B4 管理操作
function OpsSection(props: {
  d: GpsCalibInfo
  busy: boolean
  fzFactor: string
  setFzFactor: (v: string) => void
  onFreeze: () => void
  onUnfreeze: () => void
  onReset: () => void
}) {
  const { d, busy, fzFactor, setFzFactor, onFreeze, onUnfreeze, onReset } = props
  return (
    <SectionCard title="管理操作">
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center' }}>
        <label style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>凍結係數</label>
        <input
          value={fzFactor}
          onChange={(e) => setFzFactor(e.target.value)}
          inputMode="decimal"
          placeholder="0.9778"
          style={{ ...ctrlInp, width: 110 }}
        />
        <button onClick={onFreeze} disabled={busy} style={{ ...primary, opacity: busy ? 0.5 : 1 }}>凍結</button>
        <button onClick={onUnfreeze} disabled={busy || d.status !== 'frozen'} style={{ ...ghostBtn, opacity: busy || d.status !== 'frozen' ? 0.4 : 1 }}>解凍並重算</button>
        <button onClick={onReset} disabled={busy} style={{ ...danger, opacity: busy ? 0.5 : 1 }}>重設</button>
      </div>
      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 10, lineHeight: 1.6 }}>
        · <strong>凍結</strong>：把係數手動鎖在指定值（限 {CLAMP_LO.toFixed(2)}～{CLAMP_HI.toFixed(2)}），系統重算不會覆蓋；狀態變成「後台鎖定」。<br />
        · <strong>解凍並重算</strong>：解除鎖定，並立即依現有配對重新估一次係數（僅在狀態為「後台鎖定」時可按）。<br />
        · <strong>重設</strong>：係數歸 1.0000、狀態回「暖機中」，既有配對全部作廢（原因標為「已被重設作廢」），
        之後只吃重設時間點之後的新配對——只向前生效，不會回頭改寫已入帳的距離。<br />
        · 三個操作都會寫入上方「歷史校正變化」，觸發者記為後台管理員。
      </div>
    </SectionCard>
  )
}

// ── 版面小元件 ──

function SectionCard({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div style={{ background: 'var(--bg-0)', border: '1px solid var(--line)', borderRadius: 12, padding: '14px 16px', marginBottom: 14 }}>
      <div style={{ fontSize: 14, fontWeight: 800, color: 'var(--tx)', marginBottom: 10 }}>{title}</div>
      {children}
    </div>
  )
}

function StatTile({ label, value, unit, color }: { label: string; value: React.ReactNode; unit?: string; color?: string }) {
  return (
    <div style={{ flex: '1 1 150px', minWidth: 140, background: 'var(--bg-2)', border: '1px solid var(--line)', borderRadius: 10, padding: '10px 12px' }}>
      <div style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 900, color: color ?? 'var(--tx)' }}>
        {value}{unit && <span style={{ fontSize: 11, color: 'var(--tx-dim)', marginLeft: 3 }}>{unit}</span>}
      </div>
    </div>
  )
}

// ── 樣式常數（比照後台各頁自帶、不共用的慣例） ──

const LIST_MIN_WIDTH = 1260
const LIST_COLS = 'minmax(160px,1.4fr) 96px 88px 88px 104px 84px 76px 70px 60px 74px 92px 92px'

const listRow: React.CSSProperties = {
  display: 'grid', gridTemplateColumns: LIST_COLS, alignItems: 'center', gap: 10,
  padding: '9px 14px', borderBottom: '1px solid var(--line)', fontSize: 13,
}
const pairRow: React.CSSProperties = {
  display: 'grid', gridTemplateColumns: PAIR_COLS, alignItems: 'center', gap: 8,
  padding: '8px 12px', borderBottom: '1px solid var(--line)', fontSize: 12.5,
}
const logRow: React.CSSProperties = {
  display: 'grid', gridTemplateColumns: LOG_COLS, alignItems: 'center', gap: 8,
  padding: '8px 12px', borderBottom: '1px solid var(--line)', fontSize: 12.5,
}
const headRow: React.CSSProperties = {
  background: 'var(--bg-2)', color: 'var(--tx-faint)', fontSize: 11, letterSpacing: '.05em', fontWeight: 700,
}
const primary: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '8px 16px', cursor: 'pointer', fontSize: 13 }
const danger: React.CSSProperties = { background: 'rgba(255,80,80,.1)', color: 'var(--hunt)', fontWeight: 800, border: '1px solid rgba(255,80,80,.3)', borderRadius: 8, padding: '8px 16px', cursor: 'pointer', fontSize: 13 }
const ghost: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13 }
const ghostBtn: React.CSSProperties = { ...ghost, padding: '8px 16px', fontWeight: 700 }
const ctrlInp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 10px', color: 'var(--tx)', fontSize: 13.5, fontFamily: 'inherit' }
const pgBtn: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 14px', fontSize: 13, cursor: 'pointer', color: 'var(--tx)' }
