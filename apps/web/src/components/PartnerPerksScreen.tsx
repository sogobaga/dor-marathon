'use client'

import { useEffect, useMemo, useState } from 'react'
import useSWR from 'swr'
import { partnersApi, type PartnerShop, type PartnerListMeta } from '@/lib/api'
import { getUserToken, useUser, withUserAuth } from '@/lib/userAuth'
import { MediaCarousel, Lightbox, YouTubeEmbed, ytId } from './shared/MediaCarousel'

// 跑者充電站（特約商店）前台，兩個畫面：
// - 列表頁：Banner 卡 + 收藏愛心（樂觀更新＋失敗回滾，比照 RaceRankingScreen 的追蹤鈕）＋「只看最愛」篩選（本地過濾）
//   ＋詳細／前往兩顆按鈕。未登入：不顯示愛心與「只看最愛」（比照 RaceRankingScreen 的 loggedIn && 慣例，
//   不做「顯示但點下去才處理」的分支，直接避免任何未登入的點擊路徑）。
// - 詳細頁：Banner → 名稱/summary → 多圖(共用 MediaCarousel+Lightbox) → 消毒後 HTML 內文 → YouTube → 底部 CTA。
// OptionalAuth：未登入也能瀏覽兩頁，只是拿不到 is_favorited/收藏功能。
//
// VIP 精選 gate：audience='vip_featured' 商家現在全體玩家都看得到卡片本身，真正的門檻擋在「前往」這顆
// CTA 上——後端算出 cta_locked/cta_lock_reason（不合格時 cta_url 也會被清空），前端點擊時只要看
// cta_locked 就好，不需要自己重算資格。
// initialShopId：合作商家專屬連結 /shop/{id}（或 PhoneShell 的 ?shop= 深連結）帶入，進頁即直接顯示該商家詳細頁。
export default function PartnerPerksScreen({ onBack, initialShopId }: { onBack: () => void; initialShopId?: string }) {
  const [selectedId, setSelectedId] = useState<string | null>(() => initialShopId ?? null)
  // 收藏樂觀更新 override 提升到這層：PartnerPerksScreen 本身在「列表 ⇄ 詳細」切換時不會被卸載
  // （卸載的只有子元件 PartnerShopListView/PartnerShopDetailView），所以 override 放這裡才能在
  // 「收藏 → 進詳細 → 返回」之後存活，不會被 SWR 的舊快取值蓋回。
  const [favOverride, setFavOverride] = useState<Record<string, boolean>>({})
  // VIP 精選鎖定原因彈窗：同樣提升到這層，理由與 favOverride 一致——列表／詳細任一邊點到鎖定的
  // 「前往」都可能觸發，彈窗狀態不能放進會被卸載的子元件，否則「開彈窗 → 剛好被返回卸載」會憑空消失。
  const [lockedShop, setLockedShop] = useState<PartnerShop | null>(null)

  const user = useUser()
  // 與 PartnerShopListView 用同一個 SWR key：只是為了讓 meta（is_vip/user_km/min_km）在詳細頁的鎖定
  // 彈窗也能用到，兩邊會共用同一份快取／同一次請求，不會多打一次 API。
  const { data: metaData } = useSWR(
    ['partner-shops', user?.id ?? 'guest'],
    () => partnersApi.list(getUserToken() ?? undefined),
  )
  const meta = metaData?.meta ?? null

  // 前往 CTA 的統一處理：鎖定就開原因彈窗，否則才開新分頁。
  function handleCta(shop: PartnerShop) {
    if (shop.cta_locked) {
      setLockedShop(shop)
    } else if (shop.cta_url) {
      window.open(shop.cta_url, '_blank', 'noopener,noreferrer')
    }
  }

  return (
    <>
      {selectedId ? (
        <PartnerShopDetailView id={selectedId} onBack={() => setSelectedId(null)} onCta={handleCta} />
      ) : (
        <PartnerShopListView
          onBack={onBack}
          onOpenDetail={(id) => setSelectedId(id)}
          override={favOverride}
          setOverride={setFavOverride}
          onCta={handleCta}
        />
      )}
      {lockedShop && <VipLockedModal shop={lockedShop} meta={meta} onClose={() => setLockedShop(null)} />}
    </>
  )
}

function PartnerShopListView({
  onBack, onOpenDetail, override, setOverride, onCta,
}: {
  onBack: () => void
  onOpenDetail: (id: string) => void
  override: Record<string, boolean>
  setOverride: React.Dispatch<React.SetStateAction<Record<string, boolean>>>
  onCta: (shop: PartnerShop) => void
}) {
  const user = useUser()
  const { data, error, isLoading } = useSWR(
    ['partner-shops', user?.id ?? 'guest'],
    () => partnersApi.list(getUserToken() ?? undefined),
  )
  const shops = data?.shops ?? null
  const meta = data?.meta ?? null

  const [onlyFav, setOnlyFav] = useState(false)
  const [tab, setTab] = useState<'all' | 'vip_featured'>('all')
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

  // 依 audience 分組。VIP 精選商家全體玩家都看得到卡片內容，差別只在卡片「前往」按鈕會被鎖住
  // （見 ShopCard／onCta）。一般商家與 VIP 精選「不同列表」，用頁籤切換（見下方 tab）。
  const shownAll = useMemo(() => shown.filter((s) => s.audience !== 'vip_featured'), [shown])
  const shownVip = useMemo(() => shown.filter((s) => s.audience === 'vip_featured'), [shown])
  // 有 VIP 精選商家時才顯示「一般商家 / VIP 精選」頁籤；沒有(現在商家少/後台未設)就只出單一列表，
  // 避免空頁籤。未來商家變多再細分更多類型（現在細分只會顯得每一類的商家很少）。
  const hasVip = !!meta && meta.vip_featured_count > 0
  const curTab: 'all' | 'vip_featured' = hasVip ? tab : 'all'
  const tabShops = curTab === 'vip_featured' ? shownVip : shownAll

  const tabBarStyle: React.CSSProperties = { display: 'flex', gap: 8, padding: '2px 0 12px' }
  const tabBase: React.CSSProperties = { flex: 1, padding: '9px 0', borderRadius: 10, fontSize: 13, fontWeight: 800, cursor: 'pointer', border: '1px solid var(--line)', background: 'var(--bg-1)', color: 'var(--tx-dim)' }
  const tabActive: React.CSSProperties = { ...tabBase, background: 'var(--tx)', color: 'var(--bg)', borderColor: 'var(--tx)' }
  const tabActiveGold: React.CSSProperties = { ...tabBase, background: 'var(--gold)', color: '#fff', borderColor: 'var(--gold)' }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)', position: 'relative' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>⚡ 跑者充電站</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        {hasVip && (
          <div style={tabBarStyle}>
            <button onClick={() => setTab('all')} style={curTab === 'all' ? tabActive : tabBase}>精選商家</button>
            <button onClick={() => setTab('vip_featured')} style={curTab === 'vip_featured' ? tabActiveGold : tabBase}>✦ VIP好物分享專區</button>
          </div>
        )}

        {/* 描述文案：隨頁籤切換顯示對應一句（兩句不同時出現） */}
        <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '0 2px 12px', lineHeight: 1.7 }}>
          {curTab === 'vip_featured'
            ? 'VIP好物分享專區的產品，為 DOR 真心推薦，點卡片看詳細內容。'
            : '精選特約商店與跑者優惠，點卡片看詳細介紹。'}
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
        ) : !shops || !meta ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>目前尚無特約商店</div>
        ) : (
          <>
            {/* VIP 精選頁籤：不合格者頂端顯示門檻橫幅（合格者卡片本身即可前往，不需橫幅）。 */}
            {curTab === 'vip_featured' && meta && !meta.qualifies && <VipThresholdBanner meta={meta} />}

            {tabShops.length === 0 ? (
              <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>
                {onlyFav ? '尚未收藏任何商店' : hasVip ? '此分類目前尚無商店' : '目前尚無特約商店'}
              </div>
            ) : (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                {tabShops.map((s) => (
                  <ShopCard
                    key={s.id}
                    shop={s}
                    loggedIn={!!user}
                    isFav={favorited(s)}
                    onToggleFav={() => toggleFav(s)}
                    onDetail={() => onOpenDetail(s.id)}
                    onCta={onCta}
                  />
                ))}
              </div>
            )}
          </>
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
  shop, loggedIn, isFav, onToggleFav, onDetail, onCta,
}: {
  shop: PartnerShop
  loggedIn: boolean
  isFav: boolean
  onToggleFav: () => void
  onDetail: () => void
  onCta: (shop: PartnerShop) => void
}) {
  // 鎖定的商家後端會把 cta_url 清空，所以按鈕是否顯示不能只看 cta_url，鎖定時也要顯示（點下去開原因彈窗）。
  const showCta = !!shop.cta_url || !!shop.cta_locked
  return (
    // 整張卡片可點 → 進詳細頁；內部的愛心／前往／詳細按鈕各自 stopPropagation，避免點它們也觸發進詳細。
    <div onClick={onDetail} style={{ background: 'var(--bg-1)', border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden', cursor: 'pointer' }}>
      <div style={{ position: 'relative' }}>
        {shop.banner_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={shop.banner_url} alt="" style={{ width: '100%', aspectRatio: '2 / 1', objectFit: 'cover', display: 'block' }} />
        ) : (
          <div style={{ width: '100%', aspectRatio: '2 / 1', background: 'var(--bg-2)' }} />
        )}
        {loggedIn && (
          <button onClick={(e) => { e.stopPropagation(); onToggleFav() }} aria-label={isFav ? '取消收藏' : '收藏'} style={heartBtn}>
            {/* 用 SVG 而非 ♥ 字符：字符的實際圖形高度只有字級約 7 成、且各平台字型渲染不一，
                無法精準控制「愛心佔圓圈 65%」；SVG 尺寸可直接寫死 = heartBtnSize × 0.65。 */}
            <svg viewBox="0 0 24 24" width={heartGlyphSize} height={heartGlyphSize} aria-hidden="true"
              fill={isFav ? 'var(--hunt)' : 'none'} stroke={isFav ? 'var(--hunt)' : '#8a9099'}
              strokeWidth={2} strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z" />
            </svg>
          </button>
        )}
      </div>
      <div style={{ padding: '12px 14px' }}>
        {shop.audience === 'vip_featured' && <span style={vipBadge}>✦ VIP好物分享專區</span>}
        <div style={{ fontSize: 15.5, fontWeight: 900, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{shop.name}</div>
        {shop.summary && (
          <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.6, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden', wordBreak: 'break-word' }}>
            {shop.summary}
          </div>
        )}
        <div style={{ display: 'grid', gridTemplateColumns: showCta ? 'minmax(0,1fr) minmax(0,1fr)' : 'minmax(0,1fr)', gap: 8, marginTop: 10 }}>
          <button onClick={(e) => { e.stopPropagation(); onDetail() }} style={ghostFullBtn}>詳細</button>
          {showCta && (
            <button onClick={(e) => { e.stopPropagation(); onCta(shop) }} style={primaryFullBtn}>{shop.cta_locked ? '🔒 前往' : '前往'}</button>
          )}
        </div>
      </div>
    </div>
  )
}

// VIP 精選門檻橫幅：VIP 精選商家現在全體玩家都看得到卡片內容，不合格者只是「前往」按鈕被鎖住
// （見 ShopCard／handleCta），這裡改成放在卡片列表「上方」的精簡說明，取代原本整卡取代的鎖定卡。
// 合格者不顯示這條橫幅，直接用原本的金色「✦ VIP好物分享專區」標題（見呼叫端）。
function VipThresholdBanner({ meta }: { meta: PartnerListMeta }) {
  const pct = meta.min_km > 0 ? Math.min(100, Math.round((meta.user_km / meta.min_km) * 100)) : 100
  return (
    <div style={vipBannerCard}>
      <div style={{ fontSize: 12.5, fontWeight: 900, color: 'var(--gold)', lineHeight: 1.6 }}>
        ✦ VIP好物分享專區 · 需 VIP 會員且累積里程 ≥ {meta.min_km}KM 才能前往
      </div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 10, fontSize: 12, fontWeight: 800 }}>
        <span style={{ color: meta.is_vip ? 'var(--fug)' : 'var(--tx-faint)' }}>{meta.is_vip ? '✓ VIP 有效' : '尚非 VIP'}</span>
        <span style={{ color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums' }}>{meta.user_km.toFixed(1)} / {meta.min_km} KM</span>
      </div>
      <div style={barOuter}><div style={{ ...barInner, width: `${pct}%` }} /></div>
    </div>
  )
}

// VIP 精選「前往」鎖定原因彈窗：點到 cta_locked 的商家時開啟。內文優先用後端算好的
// shop.cta_lock_reason；理論上 cta_locked=true 時後端一定會給，這裡的 meta 組字串只是防禦性 fallback
// （例如 meta 還沒載完就先點到，理論上不會發生，但型別上 cta_lock_reason 是 optional）。
function VipLockedModal({ shop, meta, onClose }: { shop: PartnerShop; meta: PartnerListMeta | null; onClose: () => void }) {
  const reason = shop.cta_lock_reason
    || (meta ? `欲前往須滿足 VIP 會員身分，及累積跑步里程至少 ${meta.min_km}KM` : '欲前往須滿足 VIP 會員身分，及累積跑步里程門檻')

  return (
    <div data-skin="default" onClick={onClose} style={modalOverlay}>
      <div onClick={(e) => e.stopPropagation()} style={modalCard}>
        <div style={{ fontSize: 16, fontWeight: 900, color: '#fff', textAlign: 'center' }}>此為 VIP好物分享專區</div>
        <div style={{ fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, marginTop: 12 }}>{reason}</div>
        {meta && (
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 14, fontSize: 12.5, fontWeight: 800, background: 'var(--bg-2)', borderRadius: 10, padding: '10px 12px' }}>
            <span style={{ color: meta.is_vip ? 'var(--fug)' : 'var(--tx-faint)' }}>VIP：{meta.is_vip ? '有效' : '尚非會員'}</span>
            <span style={{ color: 'var(--tx-dim)', fontVariantNumeric: 'tabular-nums' }}>目前 {meta.user_km.toFixed(1)}KM ／ 需 {meta.min_km}KM</span>
          </div>
        )}
        <button onClick={onClose} style={modalPrimaryBtn}>我知道了</button>
      </div>
    </div>
  )
}

function PartnerShopDetailView({ id, onBack, onCta }: { id: string; onBack: () => void; onCta: (shop: PartnerShop) => void }) {
  const user = useUser()
  const { data, error, isLoading } = useSWR(
    ['partner-shop-detail', id, user?.id ?? 'guest'],
    () => partnersApi.get(getUserToken() ?? undefined, id),
  )
  const shop = data?.shop ?? null
  const [zoom, setZoom] = useState<{ images: string[]; index: number } | null>(null)
  // 同 ShopCard：鎖定的商家 cta_url 會被後端清空，按鈕顯示與否要看 cta_locked，不能只看 cta_url。
  const showCta = !!shop && (!!shop.cta_url || !!shop.cta_locked)

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>特約商店</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        {isLoading ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : error || !shop ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
        ) : (
          <>
            {shop.banner_url && (
              // eslint-disable-next-line @next/next/no-img-element
              // 點擊 banner 開全螢幕 Lightbox（複用下方已存在的 zoom state / <Lightbox>）。
              <img
                src={shop.banner_url} alt=""
                onClick={() => shop.banner_url && setZoom({ images: [shop.banner_url], index: 0 })}
                style={{ width: '100%', maxWidth: '100%', aspectRatio: '2 / 1', objectFit: 'cover', display: 'block', borderRadius: 14, cursor: shop.banner_url ? 'zoom-in' : 'default' }}
              />
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

            {(() => {
              // video_urls 為新主來源；空陣列時過渡期 fallback 舊單支 video_url。
              const vids = shop.video_urls && shop.video_urls.length ? shop.video_urls : (shop.video_url ? [shop.video_url] : [])
              const validVids = vids.filter((u) => ytId(u))
              return validVids.length > 0 && (
                <div style={{ marginTop: 18 }}>
                  {validVids.map((u, i) => (
                    <div key={i} style={i > 0 ? { marginTop: 14 } : undefined}>
                      <YouTubeEmbed url={u} title={shop.name} />
                    </div>
                  ))}
                </div>
              )
            })()}

            {/* 底部 CTA 前預留空間，避免內容被固定底列遮住 */}
            {showCta && <div style={{ height: 8 }} />}
          </>
        )}
      </div>

      {shop && showCta && (
        <div style={{ flexShrink: 0, padding: '12px 18px calc(env(safe-area-inset-bottom, 0px) + 14px)', borderTop: '1px solid var(--line)', background: 'var(--bg)' }}>
          <button
            onClick={() => onCta(shop)}
            style={{ ...primaryFullBtn, width: '100%', padding: '12px 0', fontSize: 14.5 }}
          >
            {shop.cta_locked ? '🔒 ' : ''}{shop.cta_label || '立即前往'}
          </button>
        </div>
      )}

      {zoom && <Lightbox images={zoom.images} index={zoom.index} onClose={() => setZoom(null)} />}
    </div>
  )
}

// VIP 精選商家卡片上的金底白字小徽章（金底白字慣例：金色實心底一律用 #fff 文字）
const vipBadge: React.CSSProperties = { display: 'inline-block', fontSize: 10.5, fontWeight: 800, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '2px 8px', marginBottom: 6 }
// VIP 精選門檻橫幅（不合格者，放卡片列表上方）
const vipBannerCard: React.CSSProperties = { background: 'var(--bg-1)', border: '1px solid var(--line-2)', borderRadius: 12, padding: '12px 14px', marginBottom: 12 }
const barOuter: React.CSSProperties = { height: 7, background: 'var(--bg-2)', borderRadius: 999, overflow: 'hidden', marginTop: 6 }
const barInner: React.CSSProperties = { height: '100%', background: 'var(--gold)', borderRadius: 999, transition: 'width .3s' }
// VIP 精選鎖定原因彈窗：比照 CancelSubscriptionModal 的深色置中卡片風格。z-index 比可拖曳資訊面板（500）
// 高很多，避免被蓋住（見 frontend-draggable-sheet 慣例：新增 overlay 要蓋過面板必須 >500）。
const modalOverlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 3500, background: 'rgba(4,8,6,.88)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18 }
const modalCard: React.CSSProperties = { width: '100%', maxWidth: 360, background: '#0b0e13', border: '1px solid var(--line-2)', borderRadius: 16, padding: '18px 16px', boxShadow: '0 16px 50px rgba(0,0,0,.7)' }
const modalPrimaryBtn: React.CSSProperties = { marginTop: 16, width: '100%', background: 'var(--gold)', color: '#fff', fontWeight: 900, border: 'none', borderRadius: 11, padding: '12px', fontSize: 14, cursor: 'pointer', fontFamily: 'inherit' }
const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const ghostFullBtn: React.CSSProperties = { background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '10px 0', fontSize: 13.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const primaryFullBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 10, padding: '10px 0', fontSize: 13.5, fontWeight: 800, cursor: 'pointer', fontFamily: 'inherit' }
const filterChip: React.CSSProperties = { border: '1px solid var(--line-2)', background: 'var(--bg-2)', color: 'var(--tx-dim)', borderRadius: 999, padding: '6px 14px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const filterChipActive: React.CSSProperties = { border: '1px solid var(--hunt)', background: 'var(--hunt)', color: '#fff', borderRadius: 999, padding: '6px 14px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
// 收藏愛心：白底圓鈕，圓圈大小不變（32px），愛心圖形佔圓圈 65%。白底疊在淺色 banner 上會糊掉，
// 故補一道淡陰影做出邊界。
const heartBtnSize = 32
const heartGlyphSize = Math.round(heartBtnSize * 0.65) // 21px
const heartBtn: React.CSSProperties = { position: 'absolute', top: 8, right: 8, width: heartBtnSize, height: heartBtnSize, borderRadius: 999, border: 'none', background: '#fff', display: 'flex', alignItems: 'center', justifyContent: 'center', cursor: 'pointer', lineHeight: 1, padding: 0, boxShadow: '0 1px 4px rgba(0,0,0,.28)' }
