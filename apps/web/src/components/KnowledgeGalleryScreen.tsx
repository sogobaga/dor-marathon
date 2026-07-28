'use client'

import { useState } from 'react'
import { createPortal } from 'react-dom'
import useSWR from 'swr'
import { monopolyApi, type KnowledgeCard } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'

// 知識卡圖鑑：擲骰停在機會/命運格抽到的知識卡收集頁。全螢幕覆蓋層（createPortal 到 body），
// 不受呼叫端（MemberPanel）目前所在頁面版位影響。防劇透規則完全交給後端：未擁有的卡片後端只回
// id/theme/main_category/rarity/owned/obtained_count，展示欄位（title/body/...）一律不存在，
// 前端顯示「？」卡背即可，main_category/rarity 可當朦朧提示但不需額外遮蔽。
// 主題色：跑者訓練＝綠（沿用全站 --fug／--fug-ink），跑者照護＝金（沿用全站 --gold，金底一律白字）。
const THEME_INFO: Record<'training' | 'care', { label: string; emoji: string; accent: string; headerBg: string }> = {
  training: {
    label: '跑者訓練',
    emoji: '🎁',
    accent: 'var(--fug)',
    headerBg: 'linear-gradient(135deg, rgba(255,255,255,.2), rgba(255,255,255,0) 55%), var(--fug)',
  },
  care: {
    label: '跑者照護',
    emoji: '📜',
    accent: 'var(--gold)',
    headerBg: 'linear-gradient(135deg, rgba(255,255,255,.2), rgba(255,255,255,0) 55%), var(--gold)',
  },
}

export default function KnowledgeGalleryScreen({ onClose }: { onClose: () => void }) {
  const user = useUser()
  const uid = user?.id ?? null
  const { data } = useSWR(
    uid && getUserToken() ? ['monopoly-knowledge', uid] : null,
    () => withUserAuth((t) => monopolyApi.knowledge(t)),
  )
  const [detail, setDetail] = useState<KnowledgeCard | null>(null)
  const [tab, setTab] = useState<'training' | 'care'>('training')

  const trainingCards = data ? data.cards.filter((c) => c.theme === 'training') : null
  const careCards = data ? data.cards.filter((c) => c.theme === 'care') : null
  const activeCards = tab === 'training' ? trainingCards : careCards
  const activeOwned = data ? (tab === 'training' ? data.counts.training_owned : data.counts.care_owned) : 0
  const activeTotal = data ? (tab === 'training' ? data.counts.training_total : data.counts.care_total) : 0

  const body = (
    <div style={{ position: 'fixed', inset: 0, zIndex: 2000, background: 'var(--bg)', display: 'flex', flexDirection: 'column' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onClose} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>📚 知識探索</span>
      </header>

      {user && (
        <div style={{ display: 'flex', gap: 4, padding: '0 18px', borderBottom: '1px solid var(--line)', flexShrink: 0 }}>
          {(['training', 'care'] as const).map((t) => {
            const info = THEME_INFO[t]
            const active = tab === t
            return (
              <button
                key={t}
                onClick={() => setTab(t)}
                style={{
                  flex: 1, padding: '10px 6px', border: 'none', background: 'none', cursor: 'pointer', fontSize: 13.5,
                  whiteSpace: 'nowrap', fontFamily: 'inherit',
                  color: active ? 'var(--tx)' : 'var(--tx-dim)', fontWeight: active ? 800 : 500,
                  borderBottom: active ? `2px solid ${info.accent}` : '2px solid transparent',
                }}
              >{info.emoji} {info.label}</button>
            )
          })}
        </div>
      )}

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 32px' }}>
        {!user ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>請先登入才能查看知識圖鑑</div>
        ) : data === undefined ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : (
          <>
            <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '2px 2px 16px', lineHeight: 1.7 }}>
              擲骰停在機會或命運格，有機會抽到知識卡，收集跑者訓練與照護的實用知識。
            </p>
            <ThemeSection theme={tab} cards={activeCards} owned={activeOwned} total={activeTotal} onSelect={setDetail} />
          </>
        )}
      </div>

      {detail && <DetailModal card={detail} onClose={() => setDetail(null)} />}
    </div>
  )

  if (typeof document === 'undefined') return null
  return createPortal(body, document.body)
}

function ThemeSection({
  theme, cards, owned, total, onSelect,
}: {
  theme: 'training' | 'care'
  cards: KnowledgeCard[] | null
  owned: number
  total: number
  onSelect: (c: KnowledgeCard) => void
}) {
  const info = THEME_INFO[theme]
  return (
    <div>
      <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginBottom: 10 }}>
        已收集 <b style={{ color: info.accent, fontWeight: 800 }}>{owned}</b> / {total} 張
      </div>
      {cards === null || cards.length === 0 ? (
        <div style={{ color: 'var(--tx-faint)', fontSize: 12.5, padding: '10px 2px' }}>{cards === null ? '載入中…' : '尚無卡片'}</div>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 10 }}>
          {cards.map((c) => (
            <KnowledgeCardTile key={c.id} card={c} theme={theme} onSelect={onSelect} />
          ))}
        </div>
      )}
    </div>
  )
}

function KnowledgeCardTile({ card, theme, onSelect }: { card: KnowledgeCard; theme: 'training' | 'care'; onSelect: (c: KnowledgeCard) => void }) {
  const info = THEME_INFO[theme]
  if (!card.owned) {
    // 未取得卡片統一無實線外框（不依稀有度上色），視覺一致，避免透露稀有度以外的線索。
    return (
      <div style={{
        aspectRatio: '3 / 4', borderRadius: 10, position: 'relative', overflow: 'hidden',
        border: '1px solid transparent',
        background: 'var(--bg-2)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 6,
      }}>
        <span style={{ fontSize: 30, fontWeight: 900, color: 'var(--tx-faint)' }}>？</span>
        <span style={{ fontSize: 9.5, color: 'var(--tx-faint)', padding: '0 8px', textAlign: 'center', lineHeight: 1.4 }}>{card.main_category}</span>
      </div>
    )
  }
  return (
    <button
      onClick={() => onSelect(card)}
      style={{
        aspectRatio: '3 / 4', borderRadius: 10, position: 'relative', overflow: 'hidden', padding: 0, cursor: 'pointer',
        border: card.rarity === 'rare' ? '1px solid var(--gold)' : '1px solid var(--line)',
        background: 'var(--bg-1)', display: 'flex', flexDirection: 'column', fontFamily: 'inherit', textAlign: 'left',
      }}
    >
      {card.image_url ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={card.image_url} alt={card.title} style={{ width: '100%', flex: 1, minHeight: 0, objectFit: 'cover' }} />
      ) : (
        <div style={{ width: '100%', flex: 1, minHeight: 0, background: info.headerBg, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 24 }}>{info.emoji}</div>
      )}
      <div style={{ padding: '5px 6px', display: 'flex', flexDirection: 'column', gap: 2 }}>
        <span style={{ fontSize: 10.5, fontWeight: 800, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{card.title}</span>
        <span style={{ fontSize: 9, color: 'var(--tx-faint)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{card.main_category}</span>
      </div>
      {card.rarity === 'rare' && (
        <span style={{ position: 'absolute', top: 4, left: 4, fontSize: 9, fontWeight: 800, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '1px 6px' }}>★稀有</span>
      )}
      {card.obtained_count > 1 && (
        <span style={{ position: 'absolute', top: 4, right: 4, fontSize: 9.5, fontWeight: 900, color: '#fff', background: 'rgba(0,0,0,.55)', borderRadius: 999, padding: '1px 6px' }}>×{card.obtained_count}</span>
      )}
    </button>
  )
}

function DetailModal({ card, onClose }: { card: KnowledgeCard; onClose: () => void }) {
  const theme = card.theme
  const info = THEME_INFO[theme]
  return (
    <div
      onClick={onClose}
      style={{ position: 'fixed', inset: 0, zIndex: 2100, background: 'rgba(10,13,12,.72)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 20 }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 16, maxWidth: 380, width: '100%', maxHeight: '84vh', overflowY: 'auto', boxShadow: '0 12px 40px rgba(0,0,0,.4)' }}
      >
        {card.image_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={card.image_url} alt={card.title} style={{ width: '100%', maxHeight: 180, objectFit: 'cover', display: 'block' }} />
        ) : (
          <div style={{ width: '100%', height: 90, background: info.headerBg, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 34 }}>{info.emoji}</div>
        )}
        <div style={{ padding: '18px 20px 20px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
            <span style={tagStyle}>{card.main_category}</span>
            {card.rarity === 'rare' && <span style={rareBadge}>★ 稀有</span>}
            {card.obtained_count > 1 && <span style={{ ...tagStyle, marginLeft: 'auto' }}>已獲得 ×{card.obtained_count}</span>}
          </div>
          <div style={{ fontSize: 17, fontWeight: 900, color: 'var(--tx)' }}>{card.title}</div>
          {card.subtopic && <div style={{ fontSize: 11.5, color: 'var(--tx-faint)', marginTop: 2 }}>{card.subtopic}</div>}
          {card.body && <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 10, lineHeight: 1.7 }}>{card.body}</div>}

          {card.player_action && (
            <DetailBlock label="玩家行動" value={card.player_action} accent="var(--fug)" />
          )}
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 10 }}>
            {card.timing && <MiniTag label="適用時機" value={card.timing} />}
            {card.importance && <MiniTag label="重要程度" value={card.importance} />}
            {card.handling_level && <MiniTag label="處置層級" value={card.handling_level} />}
          </div>
          {card.risk_note && (
            <div style={{ fontSize: 11, color: 'var(--hunt)', marginTop: 10, lineHeight: 1.6 }}>⚠ {card.risk_note}</div>
          )}
          {(card.source_org || card.source_url) && (
            <div style={{ fontSize: 10.5, color: 'var(--tx-faint)', marginTop: 14, lineHeight: 1.6, borderTop: '1px solid var(--line)', paddingTop: 10 }}>
              資料來源：{card.source_org}
              {card.source_url && (
                <>
                  {' '}
                  <a href={card.source_url} target="_blank" rel="noreferrer" style={{ color: 'var(--tx-dim)', textDecoration: 'underline' }}>連結</a>
                </>
              )}
            </div>
          )}
          <button onClick={onClose} style={{ ...closeBtn, marginTop: 18 }}>關閉</button>
        </div>
      </div>
    </div>
  )
}

function DetailBlock({ label, value, accent }: { label: string; value: string; accent: string }) {
  return (
    <div style={{ marginTop: 10, background: 'var(--bg-2)', borderRadius: 10, padding: '8px 12px' }}>
      <div style={{ fontSize: 11, fontWeight: 800, color: accent }}>{label}</div>
      <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 3, lineHeight: 1.6 }}>{value}</div>
    </div>
  )
}

function MiniTag({ label, value }: { label: string; value: string }) {
  return (
    <span style={{ fontSize: 10.5, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 8, padding: '3px 8px' }}>
      <b style={{ color: 'var(--tx-faint)', fontWeight: 700 }}>{label}</b> {value}
    </span>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const tagStyle: React.CSSProperties = { fontSize: 10.5, fontWeight: 700, color: 'var(--tx-dim)', background: 'var(--bg-2)', borderRadius: 999, padding: '3px 10px' }
const rareBadge: React.CSSProperties = { fontSize: 10.5, fontWeight: 800, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '3px 10px' }
const closeBtn: React.CSSProperties = {
  width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 999,
  padding: '10px 0', fontSize: 13.5, fontWeight: 900, fontFamily: 'inherit', cursor: 'pointer',
}
