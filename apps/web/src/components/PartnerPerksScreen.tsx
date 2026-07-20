'use client'

import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { partnersApi, type PartnerShop } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { MediaCarousel, Lightbox, YouTubeEmbed, ytId } from './shared/MediaCarousel'

// 跑者充電站（特約商店）前台，兩個畫面：
// - 列表頁：Banner 卡 + 收藏愛心（樂觀更新＋失敗回滾，比照 RaceRankingScreen 的追蹤鈕）＋「只看最愛」篩選（本地過濾）
//   ＋詳細／前往兩顆按鈕。未登入：不顯示愛心與「只看最愛」（比照 RaceRankingScreen 的 loggedIn && 慣例，
//   不做「顯示但點下去才處理」的分支，直接避免任何未登入的點擊路徑）。
// - 詳細頁：Banner → 名稱/summary → 多圖(共用 MediaCarousel+Lightbox) → 消毒後 HTML 內文 → YouTube → 底部 CTA。
// OptionalAuth：未登入也能瀏覽兩頁，只是拿不到 is_favorited/收藏功能。
export default function PartnerPerksScreen({ onBack }: { onBack: () => void }) {
  const [selectedId, setSelectedId] = useState<string | null>(null)
  // 收藏樂觀更新 override 提升到這層：PartnerPerksScreen 本身在「列表 ⇄ 詳細」切換時不會被卸載
  // （卸載的只有子元件 PartnerShopListView/PartnerShopDetailView），所以 override 放這裡才能在
  // 「收藏 → 進詳細 → 返回」之後存活，不會被 SWR 的舊快取值蓋回。
  const [favOverride, setFavOverride] = useState<Record<string, boolean>>({})

  if (selectedId) {
    return <PartnerShopDetailView id={selectedId} onBack={() => setSelectedId(null)} />
  }
  return (
    <PartnerShopListView
      onBack={onBack}
      onOpenDetail={(id) => setSelectedId(id)}
      override={favOverride}
      setOverride={setFavOverride}
    />
  )
}

function PartnerShopListView({
  onBack, onOpenDetail, override, setOverride,
}: {
  onBack: () => void
  onOpenDetail: (id: string) => void
  override: Record<string, boolean>
  setOverride: React.Dispatch<React.SetStateAction<Record<string, boolean>>>
}) {
  const user = useUser()
  const { data, error, isLoading } = useSWR(
    ['partner-shops', user?.id ?? 'guest'],
    () => partnersApi.list(getUserToken() ?? undefined),
  )
  const shops = data?.shops ?? null

  const [onlyFav, setOnlyFav] = useState(false)
  const [favErr, setFavErr] = useState('')
  useEffect(() => { if (favErr) { const t = setTimeout(() => setFavErr(''), 3200); return () => clearTimeout(t) } }, [favErr])

  const favorited = (s: PartnerShop) => override[s.id] ?? s.is_favorited
  async function toggleFav(s: PartnerShop) {
    if (!getUserToken()) return
    const cur = favorited(s)
    setOverride((o) => ({ ...o, [s.id]: !cur }))
    try {
      // 比照既有慣例走 withUserAuth：access token 過期時自動 refresh 重試一次，
      // 而不是讓 401 直接靜默回滾、使用者卻毫無所知。
      await withUserAuth((t) => (cur ? partnersApi.unfavorite(t, s.id) : partnersApi.favorite(t, s.id)))
    } catch (e: any) {
      setOverride((o) => ({ ...o, [s.id]: cur })) // 失敗回滾
      setFavErr(e?.message || '操作失敗，請稍後再試')
    }
  }

  const shown = useMemo(() => {
    if (!shops) return []
    if (onlyFav && user) return shops.filter((s) => favorited(s))
    return shops
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shops, onlyFav, user, override])

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>⚡ 跑者充電站</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '2px 2px 12px', lineHeight: 1.7 }}>
          精選特約商店與跑者優惠，點卡片看詳細介紹。
        </p>

        {user && shops && shops.length > 0 && (
          <div style={{ display: 'flex', padding: '0 0 12px' }}>
            <button onClick={() => setOnlyFav((v) => !v)} style={onlyFav ? filterChipActive : filterChip}>
              {onlyFav ? '♥ 只看最愛' : '♡ 只看最愛'}
            </button>
          </div>
        )}

        {isLoading ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : error ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
        ) : !shops || shops.length === 0 ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>目前尚無特約商店</div>
        ) : shown.length === 0 ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>尚未收藏任何商店</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {shown.map((s) => (
              <ShopCard
                key={s.id}
                shop={s}
                loggedIn={!!user}
                isFav={favorited(s)}
                onToggleFav={() => toggleFav(s)}
                onDetail={() => onOpenDetail(s.id)}
              />
            ))}
          </div>
        )}
      </div>

      {favErr && (
        <div style={{ position: 'absolute', left: '50%', bottom: 'calc(env(safe-area-inset-bottom, 0px) + 28px)', transform: 'translateX(-50%)', background: 'var(--hunt)', color: '#fff', fontWeight: 800, fontSize: 13, padding: '9px 18px', borderRadius: 999, boxShadow: '0 6px 20px rgba(0,0,0,.3)', zIndex: 600, maxWidth: '86%', textAlign: 'center' }}>
          {favErr}
        </div>
      )}
    </div>
  )
}

function ShopCard({
  shop, loggedIn, isFav, onToggleFav, onDetail,
}: {
  shop: PartnerShop
  loggedIn: boolean
  isFav: boolean
  onToggleFav: () => void
  onDetail: () => void
}) {
  return (
    <div style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
      <div style={{ position: 'relative' }}>
        {shop.banner_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={shop.banner_url} alt="" style={{ width: '100%', aspectRatio: '16 / 9', objectFit: 'cover', display: 'block' }} />
        ) : (
          <div style={{ width: '100%', aspectRatio: '16 / 9', background: 'var(--bg-2)' }} />
        )}
        {loggedIn && (
          <button onClick={onToggleFav} aria-label={isFav ? '取消收藏' : '收藏'} style={heartBtn}>
            <span style={{ color: isFav ? 'var(--hunt)' : '#fff' }}>{isFav ? '♥' : '♡'}</span>
          </button>
        )}
      </div>
      <div style={{ padding: '12px 14px' }}>
        <div style={{ fontSize: 15.5, fontWeight: 900, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{shop.name}</div>
        {shop.summary && (
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden', wordBreak: 'break-word' }}>
            {shop.summary}
          </div>
        )}
        <div style={{ display: 'grid', gridTemplateColumns: shop.cta_url ? 'minmax(0,1fr) minmax(0,1fr)' : 'minmax(0,1fr)', gap: 8, marginTop: 10 }}>
          <button onClick={onDetail} style={ghostFullBtn}>詳細</button>
          {shop.cta_url && (
            <button onClick={() => window.open(shop.cta_url, '_blank', 'noopener,noreferrer')} style={primaryFullBtn}>前往</button>
          )}
        </div>
      </div>
    </div>
  )
}

function PartnerShopDetailView({ id, onBack }: { id: string; onBack: () => void }) {
  const user = useUser()
  const { data, error, isLoading } = useSWR(
    ['partner-shop-detail', id, user?.id ?? 'guest'],
    () => partnersApi.get(getUserToken() ?? undefined, id),
  )
  const shop = data?.shop ?? null
  const [zoom, setZoom] = useState<{ images: string[]; index: number } | null>(null)

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>特約商店</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        {isLoading ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : error || !shop ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
        ) : (
          <>
            {shop.banner_url && (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={shop.banner_url} alt="" style={{ width: '100%', maxWidth: '100%', aspectRatio: '16 / 9', objectFit: 'cover', display: 'block', borderRadius: 14 }} />
            )}

            <div style={{ marginTop: 14 }}>
              <div style={{ fontSize: 18, fontWeight: 900, color: 'var(--tx)', wordBreak: 'break-word' }}>{shop.name}</div>
              {shop.summary && <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.7, wordBreak: 'break-word' }}>{shop.summary}</div>}
            </div>

            {shop.photo_urls && shop.photo_urls.length > 0 && (
              <div style={{ marginTop: 18 }}>
                <MediaCarousel images={shop.photo_urls} onZoom={(images, index) => setZoom({ images, index })} />
              </div>
            )}

            {shop.detail_html && (
              <div
                style={{ marginTop: 18, fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, wordBreak: 'break-word', overflowWrap: 'break-word', maxWidth: '100%', overflowX: 'hidden' }}
                dangerouslySetInnerHTML={{ __html: shop.detail_html }}
              />
            )}

            {ytId(shop.video_url) && (
              <div style={{ marginTop: 18 }}>
                <YouTubeEmbed url={shop.video_url} title={shop.name} />
              </div>
            )}

            {/* 底部 CTA 前預留空間，避免內容被固定底列遮住 */}
            {shop.cta_url && <div style={{ height: 8 }} />}
          </>
        )}
      </div>

      {shop && shop.cta_url && (
        <div style={{ flexShrink: 0, padding: '12px 18px calc(env(safe-area-inset-bottom, 0px) + 14px)', borderTop: '1px solid var(--line)', background: 'var(--bg)' }}>
          <button
            onClick={() => window.open(shop.cta_url, '_blank', 'noopener,noreferrer')}
            style={{ ...primaryFullBtn, width: '100%', padding: '12px 0', fontSize: 14.5 }}
          >
            {shop.cta_label || '立即前往'}
          </button>
        </div>
      )}

      {zoom && <Lightbox images={zoom.images} index={zoom.index} onClose={() => setZoom(null)} />}
    </div>
  )
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const ghostFullBtn: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 0', fontSize: 13.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const primaryFullBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 10, padding: '10px 0', fontSize: 13.5, fontWeight: 800, cursor: 'pointer', fontFamily: 'inherit' }
const filterChip: React.CSSProperties = { border: '1px solid var(--line-2)', background: 'var(--bg-2)', color: 'var(--tx-dim)', borderRadius: 999, padding: '6px 14px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const filterChipActive: React.CSSProperties = { border: '1px solid var(--hunt)', background: 'var(--hunt)', color: '#fff', borderRadius: 999, padding: '6px 14px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const heartBtn: React.CSSProperties = { position: 'absolute', top: 8, right: 8, width: 32, height: 32, borderRadius: 999, border: 'none', background: 'rgba(0,0,0,.45)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 17, cursor: 'pointer', lineHeight: 1 }
