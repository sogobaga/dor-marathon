'use client'

import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { overlayMount } from '@/lib/overlayMount'
import type { RunMeetCard } from '@/lib/api'
import { coverFallbackGlyph, distanceBandLabel, fmtMeetAt, meetCountdown, memberCountText, memberPct, runMeetLocationIcon, runMeetLocationText } from '@/lib/runMeet'

// 團練邀請共用 UI：樣式常數、彈窗殼、toast、卡片。
// ⚠️ 所有彈窗一律 createPortal 到 overlayMount()——桌機 .phone-shell 有 transform，
//    直接 position:fixed 會被 containing block 框歪（見 memory pc-phone-frame-overlays）。
//    modal zIndex 3500（要蓋過可拖曳資訊面板的 500）、toast zIndex 600。
// ⚠️ 本目錄所有元件禁止使用 dangerouslySetInnerHTML：團練是全站第一個 UGC，
//    所有欄位都是純文字，靠 React 文字節點自動跳脫即安全（說明欄用 white-space:pre-wrap）。

// ── 樣式常數 ────────────────────────────────────────────────────────────

export const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0, fontFamily: 'inherit' }
export const headerStyle: React.CSSProperties = { padding: 'var(--app-top) 18px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 10 }
export const scrollBody: React.CSSProperties = { flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '10px 16px 28px' }

export const cardBox: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }
export const chip: React.CSSProperties = { border: '1px solid var(--line-2)', background: 'var(--bg-2)', color: 'var(--tx-dim)', borderRadius: 999, padding: '6px 12px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit', whiteSpace: 'nowrap', flexShrink: 0 }
export const chipActive: React.CSSProperties = { ...chip, border: '1px solid var(--fug)', background: 'var(--fug)', color: 'var(--fug-ink)' }

export const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 10, padding: '11px 0', fontSize: 14, fontWeight: 800, cursor: 'pointer', fontFamily: 'inherit', width: '100%' }
export const outlineBtn: React.CSSProperties = { background: 'transparent', color: 'var(--fug)', border: '1px solid var(--fug)', borderRadius: 10, padding: '11px 0', fontSize: 14, fontWeight: 800, cursor: 'default', fontFamily: 'inherit', width: '100%' }
export const mutedBtn: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx-faint)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '11px 0', fontSize: 14, fontWeight: 800, cursor: 'default', fontFamily: 'inherit', width: '100%' }
export const ghostBtn: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '9px 14px', fontSize: 13, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
export const dangerBtn: React.CSSProperties = { ...ghostBtn, color: 'var(--hunt)', borderColor: 'var(--hunt)' }
export const tinyBtn: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '4px 9px', fontSize: 11.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }

export const fieldLabel: React.CSSProperties = { fontSize: 12.5, fontWeight: 800, color: 'var(--tx)', marginBottom: 5, display: 'block' }
export const fieldHint: React.CSSProperties = { fontSize: 11, color: 'var(--tx-dim)', lineHeight: 1.65, marginTop: 5 }
export const inputStyle: React.CSSProperties = { width: '100%', boxSizing: 'border-box', background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10, color: 'var(--tx)', fontSize: 14, padding: '10px 12px', fontFamily: 'inherit' }
export const textareaStyle: React.CSSProperties = { ...inputStyle, minHeight: 84, resize: 'vertical', lineHeight: 1.6 }

export const errText: React.CSSProperties = { color: 'var(--hunt)', fontSize: 12.5, lineHeight: 1.6, marginTop: 8 }
export const emptyBox: React.CSSProperties = { textAlign: 'center', padding: '34px 12px', color: 'var(--tx-dim)' }

// 金底一律白字（memory gold-bg-white-text）
export const goldPill: React.CSSProperties = { display: 'inline-flex', alignItems: 'center', gap: 4, background: 'var(--gold)', color: '#fff', borderRadius: 999, padding: '3px 10px', fontSize: 11, fontWeight: 800, flexShrink: 0 }
export const tagPill: React.CSSProperties = { display: 'inline-flex', alignItems: 'center', gap: 3, background: 'var(--bg-2)', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 999, padding: '2px 8px', fontSize: 10.5, fontWeight: 700, flexShrink: 0 }

// ── 彈窗殼（portal 到 overlayMount） ───────────────────────────────────

export function RunMeetModal({
  onClose, children, maxWidth = 380, dismissOnBackdrop = true,
}: {
  onClose: () => void
  children: React.ReactNode
  maxWidth?: number
  dismissOnBackdrop?: boolean
}) {
  const [mount, setMount] = useState<{ node: HTMLElement | null; position: 'fixed' | 'absolute' }>({ node: null, position: 'fixed' })
  useEffect(() => { setMount(overlayMount()) }, [])
  if (!mount.node) return null
  return createPortal(
    <div
      data-skin="default"
      onClick={dismissOnBackdrop ? onClose : undefined}
      style={{ position: mount.position, inset: 0, zIndex: 3500, background: 'rgba(4,8,6,.88)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 14 }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        // ⚠️ App 手感：只能垂直捲動，擋掉水平/自由拖曳（touch-action:pan-y）與捲到底把背景一起帶動
        // （overscrollBehavior:contain）；overflowX:hidden 兜底子元素萬一溢出也不會冒出水平捲軸。
        // 例外：地圖選點（RunMeetLocationPicker 的 Leaflet 容器）需要吃滿手勢，該容器自帶
        // touchAction:'none'——與此處 pan-y 取交集後仍是 none，等同完全交給 Leaflet 自己的手勢處理，不受影響。
        style={{ width: '100%', maxWidth, maxHeight: '92dvh', overflowY: 'auto', overflowX: 'hidden', touchAction: 'pan-y', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--line-2)', borderRadius: 16, padding: '18px 16px', boxShadow: '0 16px 50px rgba(0,0,0,.7)' }}
      >
        {children}
      </div>
    </div>,
    mount.node,
  )
}

export const modalTitle: React.CSSProperties = { fontSize: 16, fontWeight: 900, color: '#fff', textAlign: 'center' }
export const modalText: React.CSSProperties = { fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, marginTop: 12 }
export const modalSubText: React.CSSProperties = { fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.8, marginTop: 10 }
export const modalActions: React.CSSProperties = { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 18 }

// ── Toast（父層需 position:relative） ─────────────────────────────────

export function Toast({ text, tone = 'ok' }: { text: string; tone?: 'ok' | 'err' }) {
  if (!text) return null
  return (
    <div style={{
      position: 'absolute', left: '50%', bottom: 'calc(env(safe-area-inset-bottom, 0px) + 28px)', transform: 'translateX(-50%)',
      background: tone === 'err' ? 'var(--hunt)' : 'var(--fug)', color: tone === 'err' ? '#fff' : 'var(--fug-ink)',
      fontWeight: 800, fontSize: 13, padding: '9px 18px', borderRadius: 999,
      boxShadow: '0 6px 20px rgba(0,0,0,.3)', zIndex: 600, maxWidth: '86%', textAlign: 'center',
    }}>
      {text}
    </div>
  )
}

// ── 小元件 ────────────────────────────────────────────────────────────

export function Avatar({ url, name, size = 30 }: { url?: string; name?: string; size?: number }) {
  return (
    <span style={{ width: size, height: size, borderRadius: '50%', overflow: 'hidden', flexShrink: 0, background: 'var(--bg-2)', border: '1px solid var(--line-2)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center' }}>
      {url
        // eslint-disable-next-line @next/next/no-img-element
        ? <img src={url} alt="" loading="lazy" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
        : <span style={{ fontSize: size * 0.45, fontWeight: 800, color: 'var(--tx-dim)' }}>{(name || '?').slice(0, 1)}</span>}
    </span>
  )
}

/** 無封面時的漸層底：私密團顯示 🔒（不透露團練名稱首字這種可辨識資訊），
 * 非私密團維持既有的標題首字（不留白框）。
 * ⚠️ 私密團在「未解鎖」與「已解鎖但 show_cover=false」兩種情況下都會走到這支
 * （cover_url 皆為 null，見 model.go resolveCoverURL）——鎖頭圖示對兩者都適用，
 * 不需要再細分「是不是因為沒解鎖」。 */
function CoverFallback({ title, isPrivate }: { title: string; isPrivate: boolean }) {
  const glyph = coverFallbackGlyph(isPrivate, title)
  return (
    <div style={{ width: '100%', aspectRatio: '16 / 9', background: 'linear-gradient(135deg, var(--bg-2), var(--bg-1))', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <span style={{ fontSize: 34, ...(isPrivate ? {} : { fontWeight: 900, color: 'var(--tx-faint)' }) }}>{glyph}</span>
    </div>
  )
}

// 卡片：名稱、預計時間（絕對＋相對）、公開層地點、人數 x/上限、簡短說明、公開/🔒私密標籤。
// ⚠️ 地點只顯示 region / place_label 兩個公開欄位——列表永遠拿不到 lat/lng（後端 DTO 就沒有）。
export function MeetCard({ meet, onOpen, now }: { meet: RunMeetCard; onOpen: () => void; now?: Date }) {
  const cd = meetCountdown(meet.meet_at, now)
  const pct = memberPct(meet.member_count, meet.capacity)
  const full = meet.member_count >= meet.capacity
  const band = distanceBandLabel(meet.distance_band)
  const dimmed = meet.is_ended || meet.status !== 'open'
  return (
    <div
      onClick={onOpen}
      style={{ ...cardBox, cursor: 'pointer', filter: dimmed ? 'grayscale(.6)' : undefined, opacity: dimmed ? 0.7 : 1 }}
    >
      {meet.cover_url
        // eslint-disable-next-line @next/next/no-img-element
        ? <img src={meet.cover_url} alt="" loading="lazy" style={{ width: '100%', aspectRatio: '16 / 9', objectFit: 'cover', display: 'block' }} />
        : <CoverFallback title={meet.title} isPrivate={meet.is_private} />}
      <div style={{ padding: '11px 13px 12px' }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
          <div style={{ flex: 1, minWidth: 0, fontSize: 15, fontWeight: 900, color: 'var(--tx)', lineHeight: 1.35, wordBreak: 'break-word' }}>{meet.title}</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-end' }}>
            <span style={tagPill}>{meet.is_private ? '🔒 私密' : '🌐 公開'}</span>
            <span style={tagPill}>{meet.approval_required ? '⏳ 需審核' : '⚡ 自由加入'}</span>
          </div>
        </div>

        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '2px 8px', marginTop: 7, fontSize: 12, color: 'var(--tx-dim)' }}>
          <span>🕕 {fmtMeetAt(meet.meet_at, now)}</span>
          <span style={{ color: cd.urgent ? '#f4623a' : 'var(--tx-faint)', fontWeight: cd.urgent ? 800 : 400 }}>· {cd.text}</span>
        </div>
        <div style={{ marginTop: 4, fontSize: 12, color: 'var(--tx-dim)', wordBreak: 'break-word' }}>
          {runMeetLocationIcon(meet.no_location)} {runMeetLocationText(meet)}
          {band && <span style={{ marginLeft: 6, color: 'var(--fug)', fontWeight: 800 }}>· {band}</span>}
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 800, color: full ? 'var(--gold)' : 'var(--tx)', fontVariantNumeric: 'tabular-nums', flexShrink: 0 }}>
            👥 {memberCountText(meet.member_count, meet.capacity)}
          </span>
          <span style={{ flex: 1, height: 6, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden' }}>
            <span style={{ display: 'block', height: '100%', width: `${pct}%`, background: full ? 'var(--gold)' : 'var(--fug)', borderRadius: 999 }} />
          </span>
          {full && <span style={tagPill}>✅ 已滿</span>}
          {meet.is_ended && <span style={tagPill}>已結束</span>}
          {!meet.is_ended && meet.status === 'closed' && <span style={tagPill}>已關閉</span>}
          {!meet.is_ended && meet.status === 'cancelled' && <span style={tagPill}>已中止</span>}
          {/* 只有發起人／後台視角才會是 true（見 api.ts RunMeetCard.hidden_by_owner 註解），
              其他人看到的這張卡永遠不會帶著這個標籤——不會外洩「這團被誰隱藏了」。 */}
          {meet.hidden_by_owner && <span style={tagPill}>🙈 已隱藏</span>}
        </div>

        {meet.excerpt && (
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8, lineHeight: 1.6, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden', wordBreak: 'break-word' }}>
            {meet.excerpt}
          </div>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 10, paddingTop: 9, borderTop: '1px solid var(--line)', fontSize: 12, color: 'var(--tx-dim)' }}>
          <Avatar url={meet.owner.avatar_url} name={meet.owner.name} size={20} />
          <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{meet.owner.name}</span>
          <span>🔥 {meet.reaction_count}</span>
          <span>💬 {meet.comment_count}</span>
        </div>
      </div>
    </div>
  )
}
