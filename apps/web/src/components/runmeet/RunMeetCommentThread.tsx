'use client'

import { useCallback, useEffect, useRef, useState } from 'react'
import { runMeetApi, type RunMeetComment, type RunMeetReactionKind } from '@/lib/api'
import { withUserAuth } from '@/lib/userAuth'
import {
  REACTION_META, hasMoreCursor, insertReply, insertTopComment, markCommentDeleted, mentionPrefix,
  mergeCommentPages, optimisticReactionUpdate, reactionPills, replyTargetId, replyToggleLabel,
  showReplyToggle, updateCommentReaction, type ReactionCount,
} from '@/lib/runMeet'
import { Avatar, fieldHint, ghostBtn, inputStyle } from './ui'

// 留言討論串共用邏輯／渲染：detail 頁的精簡留言區（RunMeetDetailView CommentsBlock）與完整討論區
// （RunMeetThreadModal）都是同一份 useCommentThread + 同一組列渲染元件，只差初次載入筆數與是否有
// 捲動載入——狀態與 API 呼叫全部集中在這裡，兩處只需各自決定佈局（精簡預覽 vs 全螢幕無限捲動）。
//
// ⚠️ 純函式（表情彙總、@提及前綴、cursor 判斷、樂觀更新、頂層/回覆插入、軟刪遮蔽）全部在
// lib/runMeet.ts，可被 scripts/verify-run-meet.mjs 驗證；這裡只做「呼叫 API + 呼叫那些純函式改 state」。

const PREVIEW_COUNT = 2 // 頂層留言隨附的回覆預覽則數，需與後端 defReplyPreview 一致

export type ThreadUIState = { expanded: boolean; loading: boolean; cursor: string | null }

export interface UseCommentThreadOptions {
  meetId: string
  initialLimit: number
  onChanged?: () => void
}

/** 留言討論串的狀態機：初次載入、（B 專用）捲動載入更多頂層留言、單一頂層留言的回覆展開/收起/
 *  載入更多、回覆目標（@提及 + parent_id）、送出留言、表情樂觀更新、軟刪。 */
export function useCommentThread({ meetId, initialLimit, onChanged }: UseCommentThreadOptions) {
  const [items, setItems] = useState<RunMeetComment[]>([])
  const [total, setTotal] = useState(0)
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [loadingInitial, setLoadingInitial] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [threadUI, setThreadUI] = useState<Record<string, ThreadUIState>>({})
  const [replyTo, setReplyTo] = useState<{ parentId: string; name: string } | null>(null)
  const [composerBody, setComposerBody] = useState('')
  const [composerBusy, setComposerBusy] = useState(false)
  const [err, setErr] = useState('')
  const loadedOnce = useRef(false)

  const loadInitial = useCallback(async () => {
    setLoadingInitial(true); setErr('')
    try {
      const res = await withUserAuth((t) => runMeetApi.comments(t, meetId, initialLimit))
      setItems(res.items); setTotal(res.total); setNextCursor(res.next_cursor)
    } catch (e: any) {
      setErr(e?.message || '載入留言失敗，請稍後再試')
    } finally { setLoadingInitial(false) }
  }, [meetId, initialLimit])

  useEffect(() => {
    if (loadedOnce.current) return
    loadedOnce.current = true
    void loadInitial()
  }, [loadInitial])

  // 只有 B（完整討論區）會用到：捲動觸底載入更多「頂層」留言，每次 10 筆。
  const loadMoreTop = useCallback(async () => {
    if (loadingMore || loadingInitial || !hasMoreCursor(nextCursor)) return
    setLoadingMore(true)
    try {
      const res = await withUserAuth((t) => runMeetApi.comments(t, meetId, 10, nextCursor))
      setItems((prev) => mergeCommentPages(prev, res.items))
      setNextCursor(res.next_cursor)
      setTotal(res.total)
    } catch {
      // 靜默失敗：使用者仍停在同一頁，捲動再次觸底會自動重試，不用彈錯誤打斷瀏覽
    } finally { setLoadingMore(false) }
  }, [meetId, nextCursor, loadingMore, loadingInitial])

  function reactionChanged(commentId: string, reactions: ReactionCount[], myReaction: RunMeetReactionKind | null) {
    setItems((prev) => updateCommentReaction(prev, commentId, reactions, myReaction))
  }
  function commentDeleted(commentId: string) {
    setItems((prev) => markCommentDeleted(prev, commentId))
    onChanged?.()
  }

  const expandReplies = useCallback(async (commentId: string) => {
    setThreadUI((prev) => ({ ...prev, [commentId]: { expanded: true, loading: true, cursor: prev[commentId]?.cursor ?? null } }))
    try {
      const res = await withUserAuth((t) => runMeetApi.replies(t, meetId, commentId, 10))
      setItems((prev) => prev.map((c) => (c.id === commentId ? { ...c, replies: res.items } : c)))
      setThreadUI((prev) => ({ ...prev, [commentId]: { expanded: true, loading: false, cursor: res.next_cursor } }))
    } catch {
      setThreadUI((prev) => ({ ...prev, [commentId]: { expanded: false, loading: false, cursor: null } }))
    }
  }, [meetId])

  const collapseReplies = useCallback((commentId: string) => {
    setThreadUI((prev) => ({ ...prev, [commentId]: { ...(prev[commentId] ?? { cursor: null, loading: false }), expanded: false } }))
  }, [])

  // cursor 由呼叫端帶入（TopLevelCommentBlock 本來就拿著自己的 threadUI.cursor），
  // 避免在 setState updater 裡讀值再發 API 呼叫的糾結寫法。重覆點擊的防呆交給呼叫端
  // （按鈕在 loading===true 時 disabled）；就算真的重入，mergeCommentPages 也會依 id 去重。
  const loadMoreReplies = useCallback(async (commentId: string, cursor: string | null) => {
    if (!hasMoreCursor(cursor)) return
    setThreadUI((prev) => ({ ...prev, [commentId]: { expanded: true, loading: true, cursor } }))
    try {
      const res = await withUserAuth((t) => runMeetApi.replies(t, meetId, commentId, 10, cursor))
      setItems((prev) => prev.map((c) => (c.id === commentId ? { ...c, replies: mergeCommentPages(c.replies, res.items) } : c)))
      setThreadUI((prev) => ({ ...prev, [commentId]: { expanded: true, loading: false, cursor: res.next_cursor } }))
    } catch {
      setThreadUI((prev) => ({ ...prev, [commentId]: { ...(prev[commentId] ?? { expanded: true, cursor }), loading: false } }))
    }
  }, [meetId])

  function startReply(target: RunMeetComment) {
    setReplyTo({ parentId: replyTargetId(target), name: target.name })
    setComposerBody(mentionPrefix(target.name))
  }
  function cancelReply() { setReplyTo(null); setComposerBody('') }

  async function submitComment() {
    const text = composerBody.trim()
    if (!text || composerBusy) return
    setComposerBusy(true); setErr('')
    try {
      const parentId = replyTo?.parentId ?? null
      const res = await withUserAuth((t) => runMeetApi.addComment(t, meetId, text, parentId))
      if (parentId) {
        setItems((prev) => insertReply(prev, parentId, res.comment))
        setThreadUI((prev) => ({ ...prev, [parentId]: { expanded: true, loading: false, cursor: prev[parentId]?.cursor ?? null } }))
      } else {
        setItems((prev) => insertTopComment(prev, res.comment))
        setTotal((t) => t + 1)
      }
      setReplyTo(null); setComposerBody('')
      onChanged?.()
    } catch (e: any) {
      setErr(e?.message || '留言失敗，請稍後再試')
    } finally { setComposerBusy(false) }
  }

  return {
    items, total, nextCursor, loadingInitial, loadingMore, threadUI, replyTo, composerBody, setComposerBody,
    composerBusy, err, loadMoreTop, reactionChanged, commentDeleted, expandReplies, collapseReplies, loadMoreReplies,
    startReply, cancelReply, submitComment, reload: loadInitial,
  }
}

const linkBtnStyle: React.CSSProperties = { background: 'none', border: 'none', padding: 0, fontSize: 11, color: 'var(--tx-faint)', cursor: 'pointer', fontFamily: 'inherit' }

// ── 單則表情反應列：已有的表情 pill（點一下＝切換成我的反應；已是我的則取消）＋「＋表情」選擇器 ──
function ReactionRow({ meetId, comment, onReactionChanged, onToast }: {
  meetId: string
  comment: RunMeetComment
  onReactionChanged: (reactions: ReactionCount[], myReaction: RunMeetReactionKind | null) => void
  onToast: (t: string, tone?: 'ok' | 'err') => void
}) {
  const [busy, setBusy] = useState(false)
  const [pickerOpen, setPickerOpen] = useState(false)
  const pills = reactionPills(comment.reactions, comment.my_reaction)

  async function apply(kind: RunMeetReactionKind) {
    if (busy) return
    const prevReactions = comment.reactions
    const prevMine = comment.my_reaction
    const target = prevMine === kind ? null : kind
    // 樂觀更新：先改本地，失敗再回捲並提示
    const optimistic = optimisticReactionUpdate(prevReactions, prevMine, target)
    onReactionChanged(optimistic.reactions, optimistic.myReaction)
    setBusy(true)
    try {
      const res = target
        ? await withUserAuth((t) => runMeetApi.setCommentReaction(t, meetId, comment.id, target))
        : await withUserAuth((t) => runMeetApi.clearCommentReaction(t, meetId, comment.id))
      onReactionChanged(res.reactions, res.my_reaction) // 覆蓋成伺服器權威狀態
    } catch (e: any) {
      onReactionChanged(prevReactions, prevMine) // 回捲
      onToast(e?.message || '操作失敗，請稍後再試', 'err')
    } finally {
      setBusy(false)
      setPickerOpen(false)
    }
  }

  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap', marginTop: 6, position: 'relative' }}>
      {pills.map((p) => (
        <button
          key={p.kind} type="button" disabled={busy} onClick={() => void apply(p.kind)}
          style={{
            display: 'inline-flex', alignItems: 'center', gap: 3, borderRadius: 999, padding: '3px 9px',
            fontSize: 11.5, fontWeight: 800, cursor: 'pointer', fontFamily: 'inherit',
            background: p.mine ? 'var(--fug)' : 'var(--bg-2)', color: p.mine ? 'var(--fug-ink)' : 'var(--tx-dim)',
            border: `1px solid ${p.mine ? 'var(--fug)' : 'var(--line-2)'}`,
          }}
        >{p.emoji} {p.count}</button>
      ))}
      <button
        type="button" disabled={busy} onClick={() => setPickerOpen((v) => !v)}
        style={{ background: 'none', border: '1px dashed var(--line-2)', borderRadius: 999, padding: '3px 8px', fontSize: 11, color: 'var(--tx-faint)', cursor: 'pointer', fontFamily: 'inherit' }}
      >＋表情</button>
      {pickerOpen && (
        <div style={{ position: 'absolute', top: '100%', left: 0, marginTop: 4, display: 'flex', gap: 4, background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 10, padding: 6, zIndex: 5, boxShadow: '0 6px 16px rgba(0,0,0,.25)' }}>
          {REACTION_META.map((m) => {
            const mine = comment.my_reaction === m.kind
            return (
              <button
                key={m.kind} type="button" disabled={busy} onClick={() => void apply(m.kind)} title={m.label}
                style={{
                  fontSize: 17, width: 30, height: 30, borderRadius: 8, cursor: 'pointer', fontFamily: 'inherit',
                  background: mine ? 'var(--fug)' : 'var(--bg-2)', border: `1px solid ${mine ? 'var(--fug)' : 'var(--line-2)'}`,
                }}
              >{m.emoji}</button>
            )
          })}
        </div>
      )}
    </div>
  )
}

// ── 單則留言（頂層或回覆共用）：已刪除只顯示頭像 + 灰字佔位，不可回覆/按表情。 ──
function SingleCommentRow({
  meetId, comment, canComment, isReply, onReply, onReport, onReactionChanged, onDeleted, onToast,
}: {
  meetId: string
  comment: RunMeetComment
  canComment: boolean
  isReply: boolean
  onReply: (comment: RunMeetComment) => void
  onReport: (commentId: string) => void
  onReactionChanged: (reactions: ReactionCount[], myReaction: RunMeetReactionKind | null) => void
  onDeleted: () => void
  onToast: (t: string, tone?: 'ok' | 'err') => void
}) {
  const [busyDelete, setBusyDelete] = useState(false)

  if (comment.deleted) {
    return (
      <div style={{ display: 'flex', gap: 8 }}>
        <Avatar url={comment.avatar_url} name={comment.name} size={isReply ? 22 : 26} />
        <div style={{ flex: 1, minWidth: 0, fontSize: 12.5, color: 'var(--tx-faint)', fontStyle: 'italic', paddingTop: 4 }}>這則留言已刪除</div>
      </div>
    )
  }

  async function del() {
    if (busyDelete) return
    setBusyDelete(true)
    try {
      await withUserAuth((t) => runMeetApi.deleteComment(t, meetId, comment.id))
      onDeleted()
      onToast('留言已刪除')
    } catch (e: any) {
      onToast(e?.message || '刪除失敗', 'err')
    } finally { setBusyDelete(false) }
  }

  return (
    <div style={{ display: 'flex', gap: 8 }}>
      <Avatar url={comment.avatar_url} name={comment.name} size={isReply ? 22 : 26} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 12, fontWeight: 800, color: 'var(--tx)' }}>{comment.name}</div>
        {/* 純文字：React 文字節點自動跳脫，說明/留言一律不 linkify、不解析 HTML */}
        <div style={{ fontSize: 13, color: 'var(--tx)', lineHeight: 1.7, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{comment.body}</div>
        <ReactionRow meetId={meetId} comment={comment} onReactionChanged={onReactionChanged} onToast={onToast} />
        <div style={{ display: 'flex', gap: 12, marginTop: 4 }}>
          {canComment && <button type="button" onClick={() => onReply(comment)} style={linkBtnStyle}>回覆</button>}
          {comment.can_delete && <button type="button" onClick={() => void del()} disabled={busyDelete} style={linkBtnStyle}>刪除</button>}
          <button type="button" onClick={() => onReport(comment.id)} style={linkBtnStyle}>檢舉</button>
        </div>
      </div>
    </div>
  )
}

// ── 一則頂層留言 + 最多兩則回覆預覽（reply_count>2 時可展開全部／收起）；回覆縮排一層。 ──
export function TopLevelCommentBlock({
  meetId, comment, canComment, threadUI, onExpand, onCollapse, onLoadMore, onReply, onReport,
  onReactionChanged, onDeleted, onToast,
}: {
  meetId: string
  comment: RunMeetComment
  canComment: boolean
  threadUI: ThreadUIState | undefined
  onExpand: (id: string) => void
  onCollapse: (id: string) => void
  onLoadMore: (id: string, cursor: string | null) => void
  onReply: (c: RunMeetComment) => void
  onReport: (id: string) => void
  onReactionChanged: (id: string, reactions: ReactionCount[], myReaction: RunMeetReactionKind | null) => void
  onDeleted: (id: string) => void
  onToast: (t: string, tone?: 'ok' | 'err') => void
}) {
  const expanded = threadUI?.expanded ?? false
  const loading = threadUI?.loading ?? false
  const displayed = expanded ? comment.replies : comment.replies.slice(0, PREVIEW_COUNT)
  const showToggle = showReplyToggle(comment.reply_count, PREVIEW_COUNT)
  const showLoadMore = expanded && hasMoreCursor(threadUI?.cursor ?? null)

  return (
    <div>
      <SingleCommentRow
        meetId={meetId} comment={comment} canComment={canComment} isReply={false}
        onReply={onReply} onReport={onReport} onToast={onToast}
        onReactionChanged={(r, m) => onReactionChanged(comment.id, r, m)}
        onDeleted={() => onDeleted(comment.id)}
      />
      {displayed.length > 0 && (
        <div style={{ marginLeft: 34, display: 'flex', flexDirection: 'column', gap: 10, marginTop: 8 }}>
          {displayed.map((rep) => (
            <SingleCommentRow
              key={rep.id} meetId={meetId} comment={rep} canComment={canComment} isReply
              onReply={onReply} onReport={onReport} onToast={onToast}
              onReactionChanged={(r, m) => onReactionChanged(rep.id, r, m)}
              onDeleted={() => onDeleted(rep.id)}
            />
          ))}
        </div>
      )}
      {(showToggle || showLoadMore) && (
        <div style={{ marginLeft: 34, marginTop: 6, display: 'flex', gap: 14 }}>
          {showToggle && (
            <button type="button" onClick={() => (expanded ? onCollapse(comment.id) : onExpand(comment.id))} disabled={loading} style={linkBtnStyle}>
              {loading && !expanded ? '載入中…' : replyToggleLabel(comment.reply_count, expanded)}
            </button>
          )}
          {showLoadMore && (
            <button type="button" onClick={() => onLoadMore(comment.id, threadUI?.cursor ?? null)} disabled={loading} style={linkBtnStyle}>
              {loading ? '載入中…' : '載入更多回覆'}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

// ── 共用輸入框：唯讀期隱藏；回覆狀態顯示「回覆 @某某 ×」可取消。 ──
export function CommentComposer({
  canComment, replyTo, body, onBodyChange, busy, onSubmit, onCancelReply,
}: {
  canComment: boolean
  replyTo: { parentId: string; name: string } | null
  body: string
  onBodyChange: (v: string) => void
  busy: boolean
  onSubmit: () => void
  onCancelReply: () => void
}) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => { if (replyTo) ref.current?.focus() }, [replyTo])

  if (!canComment) {
    return <div style={fieldHint}>這個團練已結束超過 7 天，留言區已關閉。</div>
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {replyTo && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11.5, color: 'var(--tx-dim)' }}>
          <span>回覆 @{replyTo.name}</span>
          <button type="button" onClick={onCancelReply} style={{ background: 'none', border: 'none', padding: 0, color: 'var(--tx-faint)', cursor: 'pointer', fontFamily: 'inherit', fontSize: 13 }}>×</button>
        </div>
      )}
      <div style={{ display: 'flex', gap: 8 }}>
        <input
          ref={ref}
          value={body}
          onChange={(e) => onBodyChange(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') onSubmit() }}
          maxLength={200}
          placeholder={replyTo ? `回覆 ${replyTo.name}…` : '說點什麼…'}
          style={{ ...inputStyle, fontSize: 13.5 }}
        />
        <button onClick={onSubmit} disabled={busy || !body.trim()} style={{ ...ghostBtn, flexShrink: 0, opacity: busy || !body.trim() ? 0.6 : 1 }}>
          {busy ? '送出中…' : '送出'}
        </button>
      </div>
    </div>
  )
}
