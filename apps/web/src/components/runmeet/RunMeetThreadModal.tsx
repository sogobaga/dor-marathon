'use client'

import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { overlayMount } from '@/lib/overlayMount'
import { hasMoreCursor } from '@/lib/runMeet'
import { backBtn, errText, headerStyle } from './ui'
import { CommentComposer, TopLevelCommentBlock, useCommentThread } from './RunMeetCommentThread'

// 完整討論區：詳情頁的留言區只給前 10 筆頂層留言（見 RunMeetDetailView CommentsBlock），
// 這裡才是「查看全部留言」之後的完整畫面——初次載入 20 筆，往下捲動時每次再載 10 筆
// （IntersectionObserver 監看列表底部的哨兵元素，不用 scroll 事件輪詢，避免瞬間載入過多筆數）。
//
// ⚠️ 用全螢幕 sheet 取代置中的 RunMeetModal 卡片殼——聊天式的「固定輸入框＋長列表」在手機上
// 比塞進一個有限高度的卡片好操作，但仍沿用 RunMeetModal 同一套「鎖垂直手勢」慣例：
// touchAction:'pan-y' + overscrollBehavior:'contain'，避免水平滑動或把背景一起帶動。
export default function RunMeetThreadModal({
  meetId, canComment, onClose, onToast, onReport, onChanged,
}: {
  meetId: string
  canComment: boolean
  onClose: () => void
  onToast: (text: string, tone?: 'ok' | 'err') => void
  onReport: (commentId: string) => void
  onChanged: () => void
}) {
  const [mount, setMount] = useState<{ node: HTMLElement | null; position: 'fixed' | 'absolute' }>({ node: null, position: 'fixed' })
  useEffect(() => { setMount(overlayMount()) }, [])

  const thread = useCommentThread({ meetId, initialLimit: 20, onChanged })
  const scrollRef = useRef<HTMLDivElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)

  // loadMoreTop 每次分頁載完（nextCursor 變了）身分就會變——用 ref 存最新版本，讓下面的
  // observer 只需要建立一次，呼叫到的永遠是最新的閉包，但不必因為它變了就重建 observer。
  const loadMoreRef = useRef(thread.loadMoreTop)
  useEffect(() => { loadMoreRef.current = thread.loadMoreTop }, [thread.loadMoreTop])

  // root 必須是實際捲動的容器（不是 window）——桌機 .phone-shell 用 absolute 定位，
  // 用預設 root（viewport）在那個情境下量不出交集，一定要指到 scrollRef 自己。
  // 依賴陣列刻意只含 mount.node（不含 loadMoreTop）：第一次 render 時 mount.node 還是 null
  // （要等 overlayMount 的 useEffect 跑完才會有值），JSX（含 scrollRef/sentinelRef 掛載的節點）
  // 也還沒畫出來；observer 建好後就不再重建——若依賴 loadMoreTop，每次分頁載完它就換一個身分，
  // 舊 observer 被 disconnect、新 observer 一 observe() 到仍留在畫面內的哨兵就會立刻回報
  // isIntersecting=true，變成使用者停在底部不動也會被連續自動載完所有分頁，違背「一次只多載
  // 10 筆、避免瞬間載入過多筆數」的節流目的。只在真正的捲動交集事件觸發時才呼叫（讀 ref 拿最新版）。
  useEffect(() => {
    if (!mount.node) return
    const root = scrollRef.current
    const target = sentinelRef.current
    if (!root || !target) return
    const io = new IntersectionObserver((entries) => {
      if (entries[0]?.isIntersecting) void loadMoreRef.current()
    }, { root, rootMargin: '160px' })
    io.observe(target)
    return () => io.disconnect()
  }, [mount.node])

  if (!mount.node) return null

  return createPortal(
    <div
      data-skin="default"
      style={{ position: mount.position, inset: 0, zIndex: 3500, background: 'var(--bg)', display: 'flex', flexDirection: 'column' }}
    >
      <header style={headerStyle}>
        <button onClick={onClose} style={backBtn}>← 返回</button>
        <span style={{ flex: 1, fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>討論串（{thread.total}）</span>
      </header>

      {/* ⚠️ 輸入框固定在標題下方、留言串上方（2026-08-31 使用者定案）：
          留言是「新的在前」，這個位置符合閱讀順序，且討論串再長也永遠看得到輸入框——
          置底的話留言一多就要一路滑到最下面才找得到，這正是使用者回報的問題。 */}
      <div style={{ flexShrink: 0, padding: '10px 16px 12px', borderBottom: '1px solid var(--line)', background: 'var(--bg)' }}>
        <CommentComposer
          canComment={canComment} replyTo={thread.replyTo} body={thread.composerBody}
          onBodyChange={thread.setComposerBody} busy={thread.composerBusy}
          onSubmit={() => void thread.submitComment()} onCancelReply={thread.cancelReply}
        />
        {thread.err && <div style={errText}>{thread.err}</div>}
      </div>

      <div
        ref={scrollRef}
        style={{
          flex: 1, minHeight: 0, overflowY: 'auto', overflowX: 'hidden', touchAction: 'pan-y',
          overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch',
          padding: '10px 16px calc(env(safe-area-inset-bottom, 0px) + 20px)',
        }}
      >
        {thread.loadingInitial ? (
          <SkeletonList />
        ) : thread.items.length === 0 ? (
          <div style={{ fontSize: 12.5, color: 'var(--tx-faint)', textAlign: 'center', padding: '30px 4px' }}>
            還沒有人留言。說點什麼，讓大家知道你會到 👋
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
            {thread.items.map((c) => (
              <TopLevelCommentBlock
                key={c.id} meetId={meetId} comment={c} canComment={canComment}
                threadUI={thread.threadUI[c.id]}
                onExpand={thread.expandReplies} onCollapse={thread.collapseReplies} onLoadMore={thread.loadMoreReplies}
                onReply={thread.startReply} onReport={onReport}
                onReactionChanged={thread.reactionChanged} onDeleted={thread.commentDeleted} onToast={onToast}
              />
            ))}
          </div>
        )}

        <div ref={sentinelRef} style={{ height: 1 }} />
        {!thread.loadingInitial && thread.items.length > 0 && (
          <div style={{ textAlign: 'center', fontSize: 11.5, color: 'var(--tx-faint)', padding: '10px 0 2px' }}>
            {thread.loadingMore ? '載入中…' : !hasMoreCursor(thread.nextCursor) ? '已經到底了' : null}
          </div>
        )}
      </div>

    </div>,
    mount.node,
  )
}

function SkeletonList() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {[0, 1, 2, 3].map((i) => (
        <div key={i} style={{ display: 'flex', gap: 8 }}>
          <div style={{ width: 26, height: 26, borderRadius: '50%', background: 'var(--bg-2)', flexShrink: 0 }} />
          <div style={{ flex: 1 }}>
            <div style={{ width: '30%', height: 10, borderRadius: 4, background: 'var(--bg-2)' }} />
            <div style={{ width: '80%', height: 12, borderRadius: 4, background: 'var(--bg-2)', marginTop: 8 }} />
          </div>
        </div>
      ))}
    </div>
  )
}
