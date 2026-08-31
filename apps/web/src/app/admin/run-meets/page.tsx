'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  adminRunMeetsApi, type RunMeetAdminReport, type RunMeetAdminRow, type RunMeetComment,
  type RunMeetMember, type RunMeetMemberDetail, type RunMeetStatus,
} from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import { runMeetLocationText } from '@/lib/runMeet'

// 團練邀請後台（perm run_meets）：列表／詳情／強制下架／刪違規留言／檢舉審核／人工調整配額／孤兒圖 GC。
//
// ⚠️ 這是全站第一個 UGC 功能，「下架」是上線前提：下架後前台任何端點對該團一律 404（不外洩差異），
//    並自動發 urgent 站內信給發起人。
// ⚠️ 配額「人工調整」是唯一的返還管道——close/cancel/delete 一律不回補（後端 quota.go 只有 consume()）。
//    delta 負數＝返還次數；所有操作都會被 Audit middleware 留痕。

const STATUS_LABEL: Record<RunMeetStatus, string> = { open: '開放中', closed: '已關閉', cancelled: '已中止' }
const PAGE = 30

export default function AdminRunMeetsPage() {
  const router = useRouter()
  const [token, setToken] = useState<string | null>(null)
  const [tab, setTab] = useState<'meets' | 'reports'>('meets')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')

  // 列表
  const [q, setQ] = useState('')
  const [status, setStatus] = useState<RunMeetStatus | ''>('')
  const [hiddenOnly, setHiddenOnly] = useState(false)
  const [includeDeleted, setIncludeDeleted] = useState(false)
  const [rows, setRows] = useState<RunMeetAdminRow[] | null>(null)
  const [total, setTotal] = useState(0)
  const [offset, setOffset] = useState(0)

  // 詳情
  const [detail, setDetail] = useState<{
    meet: RunMeetMemberDetail; members: RunMeetMember[]; pending: RunMeetMember[]
    comments: RunMeetComment[]; hidden_by_admin: boolean; hidden_reason: string
  } | null>(null)
  const [takedownReason, setTakedownReason] = useState('')
  const [quotaInfo, setQuotaInfo] = useState<{ user_id: string; month: string; cap: number; used: number; remaining: number; is_vip: boolean } | null>(null)
  const [quotaDelta, setQuotaDelta] = useState(-1)
  const [quotaReason, setQuotaReason] = useState('')
  const [busy, setBusy] = useState(false)

  // 檢舉
  const [reportStatus, setReportStatus] = useState<'pending' | 'handled' | 'dismissed' | ''>('pending')
  const [reports, setReports] = useState<RunMeetAdminReport[] | null>(null)
  const [reviewNote, setReviewNote] = useState<Record<string, string>>({})

  const loadList = useCallback((t: string, off = 0) => {
    adminRunMeetsApi.list(t, {
      q: q.trim() || undefined,
      status: status || undefined,
      hidden: hiddenOnly ? '1' : undefined,
      include_deleted: includeDeleted ? '1' : undefined,
      limit: PAGE, offset: off,
    })
      .then((r) => { setRows(r.items); setTotal(r.total); setOffset(off) })
      .catch((e) => {
        if (e?.status === 401) { clearToken(); router.replace('/admin/login') }
        else if (e?.status === 403) setErr('無「團練邀請」權限')
        else setErr(e?.message || '載入失敗')
      })
  }, [q, status, hiddenOnly, includeDeleted, router])

  const loadReports = useCallback((t: string) => {
    adminRunMeetsApi.reports(t, { status: reportStatus || undefined, limit: 50 })
      .then((r) => setReports(r.items))
      .catch((e) => setErr(e?.message || '載入檢舉失敗'))
  }, [reportStatus])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setToken(t)
    loadList(t, 0)
  }, [loadList, router])

  useEffect(() => {
    if (token && tab === 'reports') loadReports(token)
  }, [token, tab, loadReports])

  function openDetail(id: string) {
    if (!token) return
    setErr(''); setMsg(''); setTakedownReason(''); setQuotaInfo(null)
    adminRunMeetsApi.detail(token, id)
      .then((d) => {
        setDetail(d)
        return adminRunMeetsApi.quota(token, d.meet.owner.id).then(setQuotaInfo).catch(() => {})
      })
      .catch((e) => setErr(e?.message || '載入詳情失敗'))
  }

  async function run(fn: () => Promise<unknown>, ok: string) {
    if (!token || busy) return
    setBusy(true); setErr(''); setMsg('')
    try {
      await fn()
      setMsg(ok)
      loadList(token, offset)
      if (detail) openDetail(detail.meet.id)
      if (tab === 'reports') loadReports(token)
    } catch (e: any) {
      setErr(e?.message || '操作失敗')
    } finally { setBusy(false) }
  }

  return (
    <div style={{ maxWidth: 1080, margin: '0 auto' }}>
      <h1 style={{ fontSize: 24, fontWeight: 800, margin: '0 0 4px' }}>團練邀請管理</h1>
      <p style={{ color: 'var(--tx-dim)', fontSize: 13, margin: '0 0 14px', lineHeight: 1.7 }}>
        會員自行發起的「團練邀請」（run_meets）。可強制下架違規團練、刪除違規留言、處理檢舉，並人工調整發起配額。
        下架後前台任何端點對該團一律回 404，發起人會收到緊急站內信。
      </p>
      {err && <div style={{ color: 'var(--hunt)', padding: '8px 0', fontSize: 13 }}>{err}</div>}
      {msg && <div style={{ color: 'var(--fug)', padding: '8px 0', fontSize: 13 }}>{msg}</div>}

      <div style={{ display: 'flex', gap: 8, marginBottom: 14, alignItems: 'center' }}>
        <button onClick={() => setTab('meets')} style={tab === 'meets' ? tabBtnActive : tabBtn}>團練列表（{total}）</button>
        <button onClick={() => setTab('reports')} style={tab === 'reports' ? tabBtnActive : tabBtn}>檢舉</button>
        <span style={{ flex: 1 }} />
        <button
          onClick={() => { if (confirm('清理 24 小時前上傳、且沒有被任何團練引用的孤兒圖片？此操作不可復原。')) void run(() => adminRunMeetsApi.imageGC(token!).then((r) => setMsg(`已清理 ${r.deleted} 張孤兒圖片`)), '已執行孤兒圖片清理') }}
          disabled={busy}
          style={ghostBtn}
        >清理孤兒圖片</button>
      </div>

      {tab === 'meets' ? (
        <>
          <div style={{ ...card, marginBottom: 14, display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
            <input style={{ ...inp, width: 220 }} value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜尋名稱／地點" onKeyDown={(e) => { if (e.key === 'Enter' && token) loadList(token, 0) }} />
            <select style={{ ...inp, width: 130 }} value={status} onChange={(e) => setStatus(e.target.value as RunMeetStatus | '')}>
              <option value="">全部狀態</option>
              <option value="open">開放中</option>
              <option value="closed">已關閉</option>
              <option value="cancelled">已中止</option>
            </select>
            <label style={chk}><input type="checkbox" checked={hiddenOnly} onChange={(e) => setHiddenOnly(e.target.checked)} />只看已下架</label>
            <label style={chk}><input type="checkbox" checked={includeDeleted} onChange={(e) => setIncludeDeleted(e.target.checked)} />含已刪除</label>
            <button onClick={() => token && loadList(token, 0)} style={primaryBtn}>查詢</button>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,360px) minmax(0,1fr)', gap: 16, alignItems: 'start' }}>
            <div style={card}>
              <b style={{ fontSize: 14 }}>團練（{total}）</b>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 10 }}>
                {rows?.map((r) => (
                  <button
                    key={r.id}
                    onClick={() => openDetail(r.id)}
                    style={{ ...rowCard, textAlign: 'left', cursor: 'pointer', borderColor: detail?.meet.id === r.id ? 'var(--fug)' : 'var(--line)', opacity: r.deleted || r.hidden_by_admin ? 0.55 : 1 }}
                  >
                    <div style={{ fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      {r.is_private ? '🔒 ' : ''}{r.title}
                    </div>
                    <div style={{ fontSize: 11, color: 'var(--tx-dim)', marginTop: 2 }}>
                      {new Date(r.meet_at).toLocaleString('zh-TW', { timeZone: 'Asia/Taipei', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
                      ｜{r.region}｜{r.member_count}/{r.capacity} 人｜{STATUS_LABEL[r.status]}
                    </div>
                    <div style={{ fontSize: 11, marginTop: 2, color: r.hidden_by_admin ? 'var(--hunt)' : r.deleted ? 'var(--tx-faint)' : 'var(--fug)' }}>
                      {r.hidden_by_admin ? '● 已下架' : r.deleted ? '○ 已刪除' : '● 正常'}
                      <span style={{ color: 'var(--tx-faint)', marginLeft: 8 }}>發起人 {r.owner.name}｜配額月 {r.quota_month}</span>
                    </div>
                  </button>
                ))}
                {rows && rows.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>查無資料</div>}
              </div>
              {rows && total > PAGE && (
                <div style={{ display: 'flex', gap: 8, marginTop: 10, alignItems: 'center' }}>
                  <button disabled={offset === 0} onClick={() => token && loadList(token, Math.max(0, offset - PAGE))} style={tinyBtn}>上一頁</button>
                  <span style={{ fontSize: 11.5, color: 'var(--tx-dim)' }}>{offset + 1}–{Math.min(offset + PAGE, total)} / {total}</span>
                  <button disabled={offset + PAGE >= total} onClick={() => token && loadList(token, offset + PAGE)} style={tinyBtn}>下一頁</button>
                </div>
              )}
            </div>

            <div style={card}>
              {!detail ? (
                <div style={{ fontSize: 13, color: 'var(--tx-dim)' }}>← 從左側選一個團練查看詳情</div>
              ) : (
                <>
                  <div style={{ fontSize: 16, fontWeight: 800 }}>{detail.meet.title}</div>
                  <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.8 }}>
                    發起人：{detail.meet.owner.name}（{detail.meet.owner.id}）<br />
                    時間：{new Date(detail.meet.meet_at).toLocaleString('zh-TW', { timeZone: 'Asia/Taipei' })}<br />
                    公開地點：{runMeetLocationText(detail.meet)}<br />
                    {/* 後台視角看得到成員層地點——處理檢舉/糾紛需要完整資訊 */}
                    精確座標：{detail.meet.lat != null ? `${detail.meet.lat}, ${detail.meet.lng}` : '未設定'}<br />
                    集合細節：{detail.meet.meeting_detail || '（未填）'}<br />
                    人數：{detail.meet.member_count}/{detail.meet.capacity}｜待審 {detail.meet.pending_count}｜
                    {detail.meet.is_private ? '私密團（密碼已雜湊，不可讀取）' : '公開團'}｜
                    {detail.meet.approval_required ? '需審核' : '自由加入'}｜{STATUS_LABEL[detail.meet.status]}
                  </div>
                  {detail.meet.description && (
                    <div style={{ fontSize: 12.5, color: 'var(--tx)', marginTop: 8, whiteSpace: 'pre-wrap', wordBreak: 'break-word', background: 'var(--bg-2)', padding: 10, borderRadius: 8, lineHeight: 1.7 }}>
                      {detail.meet.description}
                    </div>
                  )}
                  {detail.meet.image_urls.length > 0 && (
                    <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
                      {/* eslint-disable-next-line @next/next/no-img-element */}
                      {detail.meet.image_urls.map((u) => <img key={u} src={u} alt="" style={{ width: 92, height: 60, objectFit: 'cover', borderRadius: 6 }} />)}
                    </div>
                  )}

                  {/* 下架／恢復 */}
                  <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
                    <b style={{ fontSize: 13 }}>內容管理</b>
                    {detail.hidden_by_admin ? (
                      <div style={{ marginTop: 8 }}>
                        <div style={{ fontSize: 12, color: 'var(--hunt)' }}>已下架：{detail.hidden_reason || '（未填原因）'}</div>
                        <button onClick={() => void run(() => adminRunMeetsApi.restore(token!, detail.meet.id), '已取消下架')} disabled={busy} style={{ ...primaryBtn, marginTop: 8 }}>取消下架</button>
                      </div>
                    ) : (
                      <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                        <input style={{ ...inp, width: 260 }} value={takedownReason} onChange={(e) => setTakedownReason(e.target.value)} placeholder="下架原因（會寫進給發起人的站內信）" maxLength={100} />
                        <button
                          onClick={() => { if (confirm('確定強制下架這個團練？發起人會收到緊急站內信。')) void run(() => adminRunMeetsApi.takedown(token!, detail.meet.id, takedownReason.trim()), '已強制下架') }}
                          disabled={busy}
                          style={dangerBtn}
                        >強制下架</button>
                      </div>
                    )}
                  </div>

                  {/* 配額 */}
                  {quotaInfo && (
                    <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
                      <b style={{ fontSize: 13 }}>發起人本月配額</b>
                      <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4 }}>
                        {quotaInfo.month}：已用 {quotaInfo.used} / 上限 {quotaInfo.cap}（剩 {quotaInfo.remaining}）{quotaInfo.is_vip ? '· VIP' : ''}
                      </div>
                      <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                        <input style={{ ...inp, width: 90 }} type="number" min={-50} max={50} value={quotaDelta} onChange={(e) => setQuotaDelta(Number(e.target.value))} />
                        <input style={{ ...inp, width: 220 }} value={quotaReason} onChange={(e) => setQuotaReason(e.target.value)} placeholder="調整原因（Audit 留痕）" />
                        <button
                          onClick={() => void run(() => adminRunMeetsApi.adjustQuota(token!, detail.meet.owner.id, { delta: quotaDelta, reason: quotaReason.trim() }), '已調整配額')}
                          disabled={busy || quotaDelta === 0}
                          style={primaryBtn}
                        >調整</button>
                      </div>
                      <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 4 }}>負數＝返還次數（把已用次數往下調）。這是唯一的返還管道：關閉／取消／刪除一律不回補。</div>
                    </div>
                  )}

                  {/* 成員 */}
                  <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
                    <b style={{ fontSize: 13 }}>成員（{detail.members.length}）／待審（{detail.pending.length}）</b>
                    <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.8 }}>
                      {detail.members.map((m) => `${m.name}${m.is_owner ? '（發起人）' : ''}`).join('、') || '（無）'}
                      {detail.pending.length > 0 && <><br />待審：{detail.pending.map((m) => m.name).join('、')}</>}
                    </div>
                  </div>

                  {/* 留言 */}
                  <div style={{ marginTop: 14, paddingTop: 12, borderTop: '1px solid var(--line)' }}>
                    <b style={{ fontSize: 13 }}>留言（{detail.comments.length}）</b>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 8 }}>
                      {detail.comments.map((c) => (
                        <div key={c.id} style={{ ...rowCard, display: 'flex', alignItems: 'flex-start', gap: 8 }}>
                          <div style={{ flex: 1, minWidth: 0 }}>
                            <div style={{ fontSize: 12, fontWeight: 700 }}>{c.name}</div>
                            <div style={{ fontSize: 12.5, color: 'var(--tx)', whiteSpace: 'pre-wrap', wordBreak: 'break-word', lineHeight: 1.6 }}>{c.body}</div>
                          </div>
                          <button
                            onClick={() => { if (confirm('刪除這則留言？')) void run(() => adminRunMeetsApi.deleteComment(token!, detail.meet.id, c.id), '留言已刪除') }}
                            style={{ ...tinyBtn, color: 'var(--hunt)' }}
                          >刪除</button>
                        </div>
                      ))}
                      {detail.comments.length === 0 && <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>（無留言）</div>}
                    </div>
                  </div>
                </>
              )}
            </div>
          </div>
        </>
      ) : (
        <div style={card}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 10 }}>
            <b style={{ fontSize: 14 }}>檢舉（{reports?.length ?? '—'}）</b>
            <select style={{ ...inp, width: 130 }} value={reportStatus} onChange={(e) => setReportStatus(e.target.value as any)}>
              <option value="pending">待處理</option>
              <option value="handled">已處理</option>
              <option value="dismissed">已駁回</option>
              <option value="">全部</option>
            </select>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {reports?.map((r) => (
              <div key={r.id} style={rowCard}>
                <div style={{ fontSize: 13, fontWeight: 700 }}>
                  {r.meet_title}
                  <span style={{ fontSize: 11, color: 'var(--tx-faint)', marginLeft: 8 }}>{r.comment_id ? '檢舉留言' : '檢舉團練'}</span>
                </div>
                <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3, lineHeight: 1.7, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                  檢舉人：{r.reporter_name}｜{new Date(r.created_at).toLocaleString('zh-TW', { timeZone: 'Asia/Taipei' })}<br />
                  理由：{r.reason || '（未填）'}
                  {r.comment_body && <><br />被檢舉留言：{r.comment_body}</>}
                  {r.review_note && <><br />審核備註：{r.review_note}</>}
                </div>
                <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                  <button onClick={() => { setTab('meets'); openDetail(r.meet_id) }} style={tinyBtn}>查看團練</button>
                  {r.status === 'pending' ? (
                    <>
                      <input style={{ ...inp, width: 220 }} value={reviewNote[r.id] ?? ''} onChange={(e) => setReviewNote((m) => ({ ...m, [r.id]: e.target.value }))} placeholder="審核備註" maxLength={200} />
                      <button onClick={() => void run(() => adminRunMeetsApi.reviewReport(token!, r.id, { status: 'handled', review_note: (reviewNote[r.id] ?? '').trim() }), '已標記為已處理')} disabled={busy} style={primaryBtn}>已處理</button>
                      <button onClick={() => void run(() => adminRunMeetsApi.reviewReport(token!, r.id, { status: 'dismissed', review_note: (reviewNote[r.id] ?? '').trim() }), '已駁回')} disabled={busy} style={ghostBtn}>駁回</button>
                    </>
                  ) : (
                    <span style={{ fontSize: 12, color: r.status === 'handled' ? 'var(--fug)' : 'var(--tx-faint)' }}>
                      {r.status === 'handled' ? '● 已處理' : '○ 已駁回'}
                    </span>
                  )}
                </div>
              </div>
            ))}
            {reports && reports.length === 0 && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>目前沒有檢舉</div>}
          </div>
        </div>
      )}
    </div>
  )
}

const card: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, padding: 16, minWidth: 0 }
const inp: React.CSSProperties = { background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 13, boxSizing: 'border-box', fontFamily: 'inherit' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const ghostBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 12px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const dangerBtn: React.CSSProperties = { background: 'transparent', color: 'var(--hunt)', border: '1px solid var(--hunt)', borderRadius: 8, padding: '7px 14px', cursor: 'pointer', fontSize: 12.5, fontFamily: 'inherit' }
const rowCard: React.CSSProperties = { background: 'var(--bg-0, #0d0f14)', border: '1px solid var(--line)', borderRadius: 8, padding: 8, width: '100%', color: 'inherit', fontFamily: 'inherit' }
const tinyBtn: React.CSSProperties = { background: 'rgba(255,255,255,.05)', border: '1px solid var(--line-2)', borderRadius: 5, color: 'var(--tx)', cursor: 'pointer', fontSize: 11, padding: '3px 8px', fontFamily: 'inherit' }
const tabBtn: React.CSSProperties = { background: 'var(--bg-1)', color: 'var(--tx-dim)', border: '1px solid var(--line)', borderRadius: 8, padding: '7px 14px', cursor: 'pointer', fontSize: 13, fontWeight: 700, fontFamily: 'inherit' }
const tabBtnActive: React.CSSProperties = { ...tabBtn, background: 'var(--fug)', color: 'var(--fug-ink)', borderColor: 'var(--fug)' }
const chk: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 5, fontSize: 12.5, color: 'var(--tx-dim)' }
