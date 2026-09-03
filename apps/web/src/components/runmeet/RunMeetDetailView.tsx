'use client'

import { useEffect, useRef, useState } from 'react'
import useSWR from 'swr'
import {
  runMeetApi, type RunMeetCard, type RunMeetDetail, type RunMeetMemberDetail, type RunMeetQuota,
} from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { loadLeaflet } from '@/lib/leaflet'
import { MediaCarousel, Lightbox } from '../shared/MediaCarousel'
import {
  REACTION_META, ctaInputOf, fmtMeetRange, memberCountText, memberPct, phaseCountdown, runMeetCta,
  runMeetLocationIcon, runMeetLocationText,
  showViewAllComments, viewAllCommentsLabel, isFetchPending, LOADING_TEXT } from '@/lib/runMeet'
import RunMeetFormModal from './RunMeetFormModal'
import RunMeetManageSheet from './RunMeetManageSheet'
import RunMeetUnlockModal from './RunMeetUnlockModal'
import RunMeetThreadModal from './RunMeetThreadModal'
import { CommentComposer, TopLevelCommentBlock, useCommentThread } from './RunMeetCommentThread'
import {
  Avatar, PhaseBadge, RunMeetModal, backBtn, cardBox, errText, fieldHint, ghostBtn, headerStyle,
  modalTitle, mutedBtn, outlineBtn, primaryBtn, scrollBody, tagPill, textareaStyle, tinyBtn,
} from './ui'

// 團練詳情：資訊區 + 地點（分層）+ 成員 + 審核專區入口 + 留言 + 心情 + CTA。
//
// ⚠️ 地點分層完全依後端 DTO：`location_locked` 為 true 時，回應裡**根本沒有** lat/lng/meeting_detail，
//    前端連地圖都不載入，只顯示公開層文字 + 「成功加入後才會顯示完整詳細地點」。
//    不要寫成 `d.lat ?? 隱藏` 之類的判斷——那會讓「哪天後端多吐了欄位」變成靜默外洩。
// ⚠️ 全檔零 dangerouslySetInnerHTML：說明/留言都是純文字，用 white-space:pre-wrap 呈現換行。

export default function RunMeetDetailView({
  id, fallbackCard, quota, onBack, onToast, onChanged, onLearnVip, onDuplicate,
}: {
  id: string
  fallbackCard: RunMeetCard | null
  quota: RunMeetQuota | null
  onBack: () => void
  // 已被刪除時倒數結束要「切回首頁」，不是「返回上一頁」（deep link 進來的人上一頁可能就是這個已刪除的
  // 團練）。不提供時退回 onBack——仍優於停在一個永遠 404 的畫面。見 RunMeetScreen 怎麼接（接的是它自己
  // 的 onBack，也就是真正離開整個團練邀請畫面回到首頁，而不是這個元件自己的 onBack＝回列表）。
  onToast: (text: string, tone?: 'ok' | 'err') => void
  onChanged: () => void
  onLearnVip?: () => void
  // 「再辦一次」：把這個團練的完整資料交給 RunMeetScreen——配額/VIP 閘門與「建立成功」的分享
  // 彈窗都是 Screen 層的既有狀態（quota / created），這裡只負責「收集這一團的資料 + 關閉管理面板」。
  onDuplicate?: (meet: RunMeetMemberDetail) => void
}) {
  const user = useUser()
  const uid = user?.id ?? 'guest'
  const [locked, setLocked] = useState(false)
  const [deletedCountdown, setDeletedCountdown] = useState<number | null>(null)
  const [showUnlock, setShowUnlock] = useState(false)
  const [showManage, setShowManage] = useState(false)
  const [showEdit, setShowEdit] = useState(false)
  const [showJoinNote, setShowJoinNote] = useState(false)
  const [showReport, setShowReport] = useState<{ commentId?: string } | null>(null)
  const [zoom, setZoom] = useState<{ images: string[]; index: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const { data, error, isLoading, mutate: reload } = useSWR(
    getUserToken() ? ['run-meet', id, uid] : null,
    () => withUserAuth((t) => runMeetApi.detail(t, id)),
    { shouldRetryOnError: false },
  )
  // 私密團未解鎖 → 後端回 403（body 另帶摘要卡）；已被發起人刪除 → 410（其餘不可見仍是 404，
  // 落到下面泛用的「找不到這個團練」）。request() 只保留訊息，所以用 status 判定。
  useEffect(() => {
    const status = (error as any)?.status
    setLocked(status === 403)
    setDeletedCountdown(status === 410 ? 3 : null)
  }, [error])

  // 3 秒倒數：每秒遞減，歸零時切回首頁。cleanup 清掉 setTimeout，避免使用者提早自己按返回、
  // 元件已卸載後，還在跑的計時器對它呼叫 setState / 導頁。
  useEffect(() => {
    if (deletedCountdown === null) return
    if (deletedCountdown <= 0) {
      // 回「團練邀請」列表，不是首頁（2026-08-31 使用者定案）：使用者本來就在團練情境裡，
      // 丟回首頁會讓人不知道自己在哪。順便 onChanged() 讓列表重抓——這個團剛被刪掉，
      // 不重抓的話它還會留在列表上（尤其別人刪、我正在看的情況）。
      onChanged()
      onBack()
      return
    }
    const t = setTimeout(() => setDeletedCountdown((s) => (s === null ? null : s - 1)), 1000)
    return () => clearTimeout(t)
  }, [deletedCountdown, onBack, onChanged])

  const meet = data?.meet ?? null
  const card: RunMeetCard | null = meet ?? fallbackCard
  const isOwner = card?.my_state === 'owner'
  const isMember = card?.my_state === 'joined' || isOwner

  async function act(fn: () => Promise<unknown>, okMsg: string) {
    if (busy) return
    setBusy(true); setErr('')
    try {
      await fn()
      await reload()
      onChanged()
      onToast(okMsg)
    } catch (e: any) {
      setErr(e?.message || '系統忙碌中，請稍後再試')
      onToast(e?.message || '系統忙碌中，請稍後再試', 'err')
    } finally { setBusy(false) }
  }

  // 「再辦一次」：關閉管理面板，把目前這團（owner 視角、含成員層地點）交給上層開複製表單。
  function handleDuplicate() {
    if (!meet) return
    setShowManage(false)
    onDuplicate?.(meet as RunMeetMemberDetail)
  }

  function share() {
    // 分享短網址 /m/{id}：帶 OG 社群預覽卡（見 app/m/[id]/page.tsx generateMetadata），
    // 落地頁按鈕會導向既有的 /?runmeet={id} 深連結——PhoneShell 的 ?runmeet= 處理維持不動，
    // 舊分享連結（/?runmeet=）仍可正常使用。
    const url = `${window.location.origin}/m/${id}`
    const text = card ? `🏃 ${card.title}｜${fmtMeetRange(card.meet_at, card.ends_at)}｜${runMeetLocationText(card)}` : ''
    if (navigator.share) {
      navigator.share({ title: card?.title, text, url }).catch(() => {})
      return
    }
    navigator.clipboard?.writeText(`${text}\n👉 ${url}`).then(
      () => onToast('連結已複製，快去揪人吧！'),
      () => onToast('複製失敗，請手動複製網址', 'err'),
    )
  }

  const cta = card ? runMeetCta(ctaInputOf(card)) : null

  function onCta() {
    if (!cta || !card) return
    switch (cta.action) {
      case 'manage': setShowManage(true); break
      case 'unlock': setShowUnlock(true); break
      case 'apply': setShowJoinNote(true); break
      case 'join': void act(() => withUserAuth((t) => runMeetApi.join(t, id)), '已加入團練 🎉'); break
      default: break
    }
  }
  function onSecondary() {
    if (!cta?.secondary) return
    if (cta.secondary.action === 'share') { share(); return }
    const withdraw = cta.secondary.action === 'withdraw'
    void act(() => withUserAuth((t) => runMeetApi.leave(t, id)), withdraw ? '已撤回申請' : '已退出團練')
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
      <header style={headerStyle}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ flex: 1, fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>團練詳情</span>
        {card && !isOwner && deletedCountdown === null && <button onClick={() => setShowReport({})} style={{ ...backBtn, color: 'var(--tx-faint)', fontSize: 12 }}>⚠ 檢舉</button>}
      </header>

      <div style={scrollBody}>
        {/* ⚠️ 載入中的判斷不可加上「!card」這個前提（2026-08-31 使用者回報）：
            從列表點進來時 card 已經有列表帶的摘要（fallbackCard），`!card` 為 false 會跳過這一支，
            但詳情 meet 還沒到，於是直接掉進下面 `!meet` 的「找不到這個團練」——一進頁面就先看到錯誤。
            正確順序是「還在等 → 已刪除 → 未解鎖 → 真的找不到 → 內容」；
            有摘要時順便把標題/時間先畫出來，比整頁空白的載入感更快。 */}
        {isFetchPending(isLoading, meet, error) ? (
          <div style={{ padding: '4px 2px' }}>
            {card && (
              <>
                <div style={{ fontSize: 18, fontWeight: 900, color: 'var(--tx)', lineHeight: 1.35, wordBreak: 'break-word' }}>{card.title}</div>
                <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.8 }}>
                  {fmtMeetRange(card.meet_at, card.ends_at)}<br />{runMeetLocationIcon(card.no_location)} {runMeetLocationText(card)}
                </div>
              </>
            )}
            <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: card ? '14px 0 0' : '20px 0' }}>{LOADING_TEXT}</div>
          </div>
        ) : deletedCountdown !== null ? (
          // ⚠️ 這裡刻意不管 fallbackCard（可能是使用者從列表點進來時帶的舊卡片摘要）——
          // 一旦後端回 410，畫面就只顯示這句倒數導頁文案，不能讓下面「找到 card 就渲染完整內容」
          // 的分支用過期資料畫出一個其實已經被刪除的團練詳情。
          <div style={{ color: 'var(--hunt)', fontSize: 15, fontWeight: 800, textAlign: 'center', padding: '40px 2px', lineHeight: 1.8 }}>
            該團練已被刪除，{deletedCountdown} 秒後將返回團練邀請頁。
          </div>
        ) : locked ? (
          <LockedPanel card={fallbackCard} onUnlock={() => setShowUnlock(true)} />
        ) : error || !meet ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>
            找不到這個團練，可能已被發起人刪除。
          </div>
        ) : (
          <>
            {meet.image_urls.length > 0 && (
              <div style={{ marginBottom: 14 }}>
                <MediaCarousel images={meet.image_urls} onZoom={(images, index) => setZoom({ images, index })} />
              </div>
            )}

            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
              <div style={{ flex: 1, minWidth: 0, fontSize: 18, fontWeight: 900, color: 'var(--tx)', lineHeight: 1.35, wordBreak: 'break-word' }}>{meet.title}</div>
            </div>
            <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
              <span style={tagPill}>{meet.is_private ? '🔒 私密' : '🌐 公開'}</span>
              <span style={tagPill}>{meet.approval_required ? '⏳ 需審核' : '⚡ 自由加入'}</span>
              <PhaseBadge phase={meet.phase} />
              {meet.phase !== 'ended' && meet.status === 'closed' && <span style={tagPill}>已關閉</span>}
              {meet.phase !== 'ended' && meet.status === 'cancelled' && <span style={tagPill}>已中止</span>}
              {meet.hidden_by_owner && <span style={tagPill}>🙈 已隱藏</span>}
            </div>

            {/* 資訊區 */}
            <div style={{ ...cardBox, padding: '12px 13px', marginTop: 12 }}>
              <InfoRow icon="🕕" text={`${fmtMeetRange(meet.meet_at, meet.ends_at)} · ${phaseCountdown(meet.phase, meet.meet_at).text}`} />
              <InfoRow icon={runMeetLocationIcon(meet.no_location)} text={runMeetLocationText(meet)} />
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 8 }}>
                <span style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', fontVariantNumeric: 'tabular-nums' }}>👥 {memberCountText(meet.member_count, meet.capacity)}</span>
                <span style={{ flex: 1, height: 6, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }}>
                  <span style={{ display: 'block', height: '100%', width: `${memberPct(meet.member_count, meet.capacity)}%`, background: 'var(--fug)', borderRadius: 999 }} />
                </span>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 10, paddingTop: 9, borderTop: '1px solid var(--line)' }}>
                <Avatar url={meet.owner.avatar_url} name={meet.owner.name} size={24} />
                <span style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>發起人 {meet.owner.name}</span>
              </div>
            </div>

            {/* 地點分層：未加入者只有公開層文字 + 提示，且不載入地圖 */}
            <LocationBlock meet={meet} />

            {meet.description && (
              <div style={{ marginTop: 14, fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                {meet.description}
              </div>
            )}

            {/* 安全提示（固定、不可關閉） */}
            <div style={{ ...cardBox, padding: '10px 12px', marginTop: 14, fontSize: 11.5, color: 'var(--tx-dim)', lineHeight: 1.75 }}>
              線下相約請注意安全：建議揪伴同行、選擇公開明亮的集合點，勿提供住家地址、身分證件或金錢往來。遇到可疑情況請使用右上角「檢舉」。
            </div>

            {/* 心情（成員限定） */}
            {isMember && (
              <div style={{ display: 'flex', gap: 6, marginTop: 14, flexWrap: 'wrap' }}>
                {REACTION_META.map((r) => {
                  const on = meet.my_reaction === r.kind
                  return (
                    <button
                      key={r.kind}
                      disabled={busy}
                      onClick={() => void act(
                        () => withUserAuth((t) => (on ? runMeetApi.clearReaction(t, id) : runMeetApi.setReaction(t, id, r.kind))),
                        on ? '已收回心情' : `已送出「${r.label}」`,
                      )}
                      style={{ ...tinyBtn, padding: '6px 10px', borderColor: on ? 'var(--fug)' : 'var(--line-2)', color: on ? 'var(--fug)' : 'var(--tx)' }}
                    >
                      {r.emoji} {r.label}
                    </button>
                  )
                })}
                <span style={{ alignSelf: 'center', fontSize: 12, color: 'var(--tx-faint)', marginLeft: 4 }}>🔥 {meet.reaction_count}</span>
              </div>
            )}

            {/* 成員區（成員/發起人才拿得到名單） */}
            {isMember && <MembersBlock meetId={id} />}

            {/* 審核專區入口（發起人） */}
            {isOwner && meet.pending_count > 0 && (
              <button onClick={() => setShowManage(true)} style={{ ...ghostBtn, width: '100%', marginTop: 12, borderColor: 'var(--gold)', color: 'var(--gold)' }}>
                ⏳ 有 {meet.pending_count} 筆待審核申請，前往處理
              </button>
            )}

            {/* 留言區 */}
            {isMember && <CommentsBlock meetId={id} canComment={meet.can_comment} onToast={onToast} onReport={(cid) => setShowReport({ commentId: cid })} onChanged={() => void reload()} />}

            {err && <div style={errText}>{err}</div>}
            <div style={{ height: 12 }} />
          </>
        )}
      </div>

      {/* 底部 CTA（deletedCountdown !== null 時強制不顯示——cta 可能是用舊 fallbackCard 算出來的） */}
      {cta && !locked && deletedCountdown === null && (
        <div style={{ flexShrink: 0, padding: '10px 16px calc(env(safe-area-inset-bottom, 0px) + 24px)', borderTop: '1px solid var(--line)', background: 'var(--bg)', display: 'flex', gap: 8 }}>
          <button
            onClick={onCta}
            disabled={cta.disabled || busy}
            style={{
              ...(cta.variant === 'primary' ? primaryBtn : cta.variant === 'outline' ? outlineBtn : mutedBtn),
              flex: 1, cursor: cta.disabled ? 'default' : 'pointer', opacity: busy ? 0.7 : 1,
            }}
          >{cta.label}</button>
          {cta.secondary && (
            <button onClick={onSecondary} disabled={busy} style={{ ...ghostBtn, flexShrink: 0, padding: '11px 14px' }}>{cta.secondary.label}</button>
          )}
        </div>
      )}
      {locked && (
        <div style={{ flexShrink: 0, padding: '10px 16px calc(env(safe-area-inset-bottom, 0px) + 24px)', borderTop: '1px solid var(--line)', background: 'var(--bg)' }}>
          <button onClick={() => setShowUnlock(true)} style={primaryBtn}>🔒 輸入密碼</button>
        </div>
      )}

      {showUnlock && (
        <RunMeetUnlockModal
          meetId={id}
          title={card?.title ?? '這個團練'}
          onClose={() => setShowUnlock(false)}
          onUnlocked={() => { setShowUnlock(false); setLocked(false); void reload(); onToast('已解鎖，來看看團練內容吧') }}
        />
      )}

      {showJoinNote && (
        <JoinNoteModal
          busy={busy}
          onClose={() => setShowJoinNote(false)}
          onSubmit={(note) => { setShowJoinNote(false); void act(() => withUserAuth((t) => runMeetApi.join(t, id, note)), '已送出申請，等發起人回覆囉') }}
        />
      )}

      {showManage && meet && !meet.location_locked && (
        <RunMeetManageSheet
          meet={meet as RunMeetMemberDetail}
          onClose={() => { setShowManage(false); void reload(); onChanged() }}
          onEdit={() => { setShowManage(false); setShowEdit(true) }}
          onDuplicate={handleDuplicate}
          onChanged={() => { void reload(); onChanged() }}
          onToast={onToast}
        />
      )}

      {showEdit && meet && !meet.location_locked && quota && (
        <RunMeetFormModal
          mode="edit"
          initial={meet as RunMeetMemberDetail}
          quota={quota}
          onClose={() => setShowEdit(false)}
          onSaved={(_m, info) => {
            setShowEdit(false)
            void reload(); onChanged()
            onToast(info.pendingKept ? `已更新，仍有 ${info.pendingKept} 筆待審核申請` : '已更新，團員會收到通知')
          }}
          onLearnVip={onLearnVip}
        />
      )}

      {showReport && (
        <ReportModal
          onClose={() => setShowReport(null)}
          onSubmit={(reason) => {
            const commentId = showReport.commentId
            setShowReport(null)
            void act(() => withUserAuth((t) => runMeetApi.report(t, id, { comment_id: commentId, reason })), '已送出檢舉，我們會盡快處理')
          }}
        />
      )}

      {zoom && <Lightbox images={zoom.images} index={zoom.index} onClose={() => setZoom(null)} />}
    </div>
  )
}

function InfoRow({ icon, text }: { icon: string; text: string }) {
  return (
    <div style={{ display: 'flex', gap: 7, fontSize: 13, color: 'var(--tx)', lineHeight: 1.7, wordBreak: 'break-word' }}>
      <span style={{ flexShrink: 0 }}>{icon}</span><span>{text}</span>
    </div>
  )
}

// 私密團未解鎖：只渲染已知的公開層卡片摘要 + 密碼 CTA（後端 403 的 body 也只給公開層卡片）。
function LockedPanel({ card, onUnlock }: { card: RunMeetCard | null; onUnlock: () => void }) {
  return (
    <div style={{ ...cardBox, padding: '18px 16px', textAlign: 'center' }}>
      <div style={{ fontSize: 30 }}>🔒</div>
      <div style={{ fontSize: 15.5, fontWeight: 900, color: 'var(--tx)', marginTop: 8 }}>{card?.title ?? '這是私密團練'}</div>
      {card && (
        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.8 }}>
          🕕 {fmtMeetRange(card.meet_at, card.ends_at)}<br />
          {runMeetLocationIcon(card.no_location)} {runMeetLocationText(card)}<br />
          👥 {memberCountText(card.member_count, card.capacity)}
        </div>
      )}
      <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 12, lineHeight: 1.8 }}>
        這是私密團練，請輸入發起人提供的密碼。
      </div>
      <button onClick={onUnlock} style={{ ...primaryBtn, marginTop: 14 }}>輸入密碼</button>
    </div>
  )
}

// 地點區塊：型別即閘門——location_locked 為 true 的 DTO 上根本沒有 lat/lng/meeting_detail。
//
// ⚠️ no_location=true（「不限地點」，migration 161）獨立於 location_locked 之外自成一支分流：
//    完全不載入地圖（連 Leaflet 都不載——MiniMap 元件本身在這支就沒被渲染到），也不顯示
//    「成功加入後才會顯示完整詳細地點」這句提示——沒有地點可隱藏，顯示那句提示只會讓人誤會
//    以為「加入後就會看到座標」。meeting_detail 仍是成員層欄位，只有 !location_locked（已加入
//    /發起人/後台）才看得到，未加入者即使是 no_location 團也一樣看不到。
function LocationBlock({ meet }: { meet: RunMeetDetail }) {
  if (meet.no_location) {
    return (
      <div style={{ ...cardBox, padding: '12px 13px', marginTop: 12 }}>
        <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)' }}>集合地點</div>
        <div style={{ fontSize: 13, color: 'var(--tx)', marginTop: 6, lineHeight: 1.7 }}>🌏 不限地點</div>
        <div style={fieldHint}>這場團練不指定集合地點，各自在方便的地方跑。</div>
        {!meet.location_locked && meet.meeting_detail && (
          <div style={{ fontSize: 13, color: 'var(--tx)', marginTop: 6, lineHeight: 1.8, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>📌 {meet.meeting_detail}</div>
        )}
      </div>
    )
  }
  if (meet.location_locked) {
    return (
      <div style={{ ...cardBox, padding: '12px 13px', marginTop: 12 }}>
        <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)' }}>集合地點</div>
        <div style={{ fontSize: 13, color: 'var(--tx)', marginTop: 6, lineHeight: 1.7 }}>{runMeetLocationText(meet)}</div>
        <div style={{ ...fieldHint, color: 'var(--gold)', fontWeight: 700 }}>🔒 {meet.location_note}</div>
      </div>
    )
  }
  return (
    <div style={{ ...cardBox, padding: '12px 13px', marginTop: 12 }}>
      <div style={{ fontSize: 12.5, fontWeight: 800, color: 'var(--tx)' }}>集合地點（團員可見）</div>
      <div style={{ fontSize: 13, color: 'var(--tx)', marginTop: 6, lineHeight: 1.7 }}>{runMeetLocationText(meet)}</div>
      {meet.meeting_detail && (
        <div style={{ fontSize: 13, color: 'var(--tx)', marginTop: 6, lineHeight: 1.8, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>📌 {meet.meeting_detail}</div>
      )}
      {meet.lat != null && meet.lng != null && <MiniMap lat={meet.lat} lng={meet.lng} />}
    </div>
  )
}

// 唯讀小地圖（只有成員才會渲染到這裡；Leaflet 走既有 lib/leaflet.ts CDN 載入器）
function MiniMap({ lat, lng }: { lat: number; lng: number }) {
  const idRef = useRef(`runmeet-map-${Math.random().toString(36).slice(2, 8)}`)
  const mapRef = useRef<any>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let cancelled = false
    loadLeaflet().then((L) => {
      if (cancelled || mapRef.current) return
      const el = document.getElementById(idRef.current)
      if (!el) return
      const map = L.map(idRef.current, { zoomControl: false, attributionControl: false }).setView([lat, lng], 16)
      L.tileLayer('https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png', { maxZoom: 19 }).addTo(map)
      L.marker([lat, lng]).addTo(map)
      mapRef.current = map
      setTimeout(() => { try { map.invalidateSize() } catch { /* 已卸載 */ } }, 80)
    }).catch(() => setFailed(true))
    return () => {
      cancelled = true
      if (mapRef.current) { try { mapRef.current.remove() } catch { /* ignore */ } mapRef.current = null }
    }
  }, [lat, lng])
  if (failed) return <div style={fieldHint}>地圖載入失敗，座標：{lat}, {lng}</div>
  return <div id={idRef.current} style={{ width: '100%', height: 170, borderRadius: 10, overflow: 'hidden', marginTop: 10, border: '1px solid var(--line-2)' }} />
}

function MembersBlock({ meetId }: { meetId: string }) {
  const { data } = useSWR(
    ['run-meet-members', meetId, 'joined'],
    () => withUserAuth((t) => runMeetApi.members(t, meetId, 'joined')),
  )
  const items = data?.items ?? []
  return (
    <div style={{ marginTop: 16 }}>
      <div style={{ fontSize: 12.5, fontWeight: 900, color: 'var(--tx)', marginBottom: 8 }}>成員（{items.length}）</div>
      {items.length === 0 ? (
        <div style={{ fontSize: 12.5, color: 'var(--tx-faint)' }}>還沒有其他人加入</div>
      ) : (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10 }}>
          {items.map((m) => (
            <div key={m.user_id} style={{ display: 'flex', alignItems: 'center', gap: 6, background: 'var(--bg-2)', borderRadius: 999, padding: '4px 10px 4px 4px' }}>
              <Avatar url={m.avatar_url} name={m.name} size={22} />
              <span style={{ fontSize: 12, color: 'var(--tx)', fontWeight: 700 }}>{m.name}</span>
              {m.is_owner && <span style={{ fontSize: 10.5, color: 'var(--gold)', fontWeight: 800 }}>發起人</span>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// 詳情頁的精簡留言區：預設 10 筆頂層留言（每則帶最多 2 則回覆預覽），reply_count>2 可展開全部；
// total>10 才顯示「查看全部留言」開完整討論區（RunMeetThreadModal，初次 20 筆 + 捲動每次 10 筆）。
// 狀態機／API 呼叫集中在 useCommentThread（與完整討論區共用同一份，見 RunMeetCommentThread.tsx）。
function CommentsBlock({ meetId, canComment, onToast, onReport, onChanged }: {
  meetId: string
  canComment: boolean
  onToast: (t: string, tone?: 'ok' | 'err') => void
  onReport: (commentId: string) => void
  onChanged: () => void
}) {
  const thread = useCommentThread({ meetId, initialLimit: 10, onChanged })
  const [showFull, setShowFull] = useState(false)

  return (
    <div style={{ marginTop: 18 }}>
      <div style={{ fontSize: 12.5, fontWeight: 900, color: 'var(--tx)', marginBottom: 8 }}>留言（{thread.total}）</div>

      {/* ⚠️ 輸入框放在標題下方、留言串上方（2026-08-31 使用者定案）：
          留言是「新的在前」，輸入框擺頂端符合閱讀順序（送出後新留言就出現在正下方）；
          置底的話留言一多就要一路滑到最下面才找得到輸入框。 */}
      <div style={{ marginBottom: 12 }}>
        <CommentComposer
          canComment={canComment} replyTo={thread.replyTo} body={thread.composerBody}
          onBodyChange={thread.setComposerBody} busy={thread.composerBusy}
          onSubmit={() => void thread.submitComment()} onCancelReply={thread.cancelReply}
        />
        {thread.err && <div style={errText}>{thread.err}</div>}
      </div>
      {thread.loadingInitial ? (
        <div style={{ fontSize: 12.5, color: 'var(--tx-faint)' }}>載入中…</div>
      ) : thread.items.length === 0 ? (
        <div style={{ fontSize: 12.5, color: 'var(--tx-faint)', lineHeight: 1.7 }}>還沒有人留言。說點什麼，讓大家知道你會到 👋</div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
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

      {showViewAllComments(thread.total) && (
        <button type="button" onClick={() => setShowFull(true)} style={{ ...ghostBtn, width: '100%', marginTop: 12 }}>
          {viewAllCommentsLabel(thread.total)}
        </button>
      )}


      {showFull && (
        <RunMeetThreadModal
          meetId={meetId} canComment={canComment} onClose={() => setShowFull(false)}
          onToast={onToast} onReport={onReport} onChanged={onChanged}
        />
      )}
    </div>
  )
}

function JoinNoteModal({ busy, onClose, onSubmit }: { busy: boolean; onClose: () => void; onSubmit: (note: string) => void }) {
  const [note, setNote] = useState('')
  return (
    <RunMeetModal onClose={onClose} maxWidth={340}>
      <div style={modalTitle}>申請加入</div>
      <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.8, marginTop: 10 }}>
        這個團練需要發起人同意。可以留一句話讓對方認識你（選填，60 字內）。
      </div>
      <textarea value={note} onChange={(e) => setNote(e.target.value)} maxLength={60} placeholder="我是新手，配速大約 7:00，想跟大家一起跑！" style={{ ...textareaStyle, minHeight: 64, marginTop: 10 }} />
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 16 }}>
        <button onClick={onClose} style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}>取消</button>
        <button onClick={() => onSubmit(note.trim())} disabled={busy} style={{ ...primaryBtn, opacity: busy ? 0.7 : 1 }}>送出申請</button>
      </div>
    </RunMeetModal>
  )
}

function ReportModal({ onClose, onSubmit }: { onClose: () => void; onSubmit: (reason: string) => void }) {
  const [reason, setReason] = useState('')
  return (
    <RunMeetModal onClose={onClose} maxWidth={340}>
      <div style={modalTitle}>檢舉</div>
      <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.8, marginTop: 10 }}>
        請簡述問題（例如不實資訊、騷擾、廣告）。我們會盡快處理，處理結果不會公開。
      </div>
      <textarea value={reason} onChange={(e) => setReason(e.target.value)} maxLength={300} placeholder="請描述問題…" style={{ ...textareaStyle, minHeight: 80, marginTop: 10 }} />
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 16 }}>
        <button onClick={onClose} style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}>取消</button>
        <button onClick={() => onSubmit(reason.trim())} style={{ ...primaryBtn, background: 'var(--hunt)', color: '#fff' }}>送出檢舉</button>
      </div>
    </RunMeetModal>
  )
}
