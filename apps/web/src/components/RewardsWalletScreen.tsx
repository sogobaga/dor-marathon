'use client'

// 活動獎勵系統 P3：玩家活動獎勵錢包（列表+詳情，比照 PartnerPerksScreen「跑者充電站」版型）。
// 顯示序號類（kind='serial'）與活動優惠券類（kind='coupon'，migration 138）兩種獎勵；
// EXP/DP/GP/VIP 中獎直接入帳不進此表，見後端 activityreward。
import { useState } from 'react'
import { createPortal } from 'react-dom'
import useSWR from 'swr'
import { rewardsApi, type UserReward } from '@/lib/api'
import { getUserToken, withUserAuth } from '@/lib/userAuth'
import { overlayMount } from '@/lib/overlayMount'

function fmtDateTime(iso?: string) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

// 只取月/日（不含年份與時間），用於「有效期間：M/D ~ M/D」區間顯示（migration 139 valid_from），
// 避免 valid_from~valid_until 兩個完整時間戳並列過長。iso 空或無效日期回傳空字串。
function fmtMD(iso?: string) {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  return `${d.getMonth() + 1}/${d.getDate()}`
}

// 「尚未開始」＝有設定開始時間、且開始時間還沒到、且尚未使用（已使用者不受開始時間擋，避免序號組事後
// 被改成有開始時間反而讓舊資料的已使用狀態顯示矛盾）。只有序號類（migration 139 valid_from）會有值，
// coupon 類此欄位恆為 undefined 不受影響。
function isNotStarted(reward: UserReward): boolean {
  return !reward.used && !!reward.valid_from && Date.now() < new Date(reward.valid_from).getTime()
}

const THIRTY_DAYS_MS = 30 * 24 * 60 * 60 * 1000

type SortedReward = UserReward & { urgent?: 'soon' | 'expired' }

// 排序規則（使用者拍板：近到期置頂+外框、其餘新到舊；過期的合理處理＝仍顯示、獨立標示「已過期」，
// 排在「即將到期」群組之後、「其餘」群組之前——比純粹「離現在最近」排序更符合直覺：尚未過期者按
// 「還剩多久」由少到多排（最快到期在最上面）；已過期者按「多久前過期」由近到遠排（剛過期的比很久
// 以前過期的更值得被看見，避免陳年過期項目反而排到最頂端）。
function sortRewards(list: UserReward[]): SortedReward[] {
  const now = Date.now()
  const soon: SortedReward[] = []
  const expired: SortedReward[] = []
  const rest: UserReward[] = []
  for (const r of list) {
    if (!r.used && r.valid_until) {
      const t = new Date(r.valid_until).getTime()
      if (!isNaN(t)) {
        const diff = t - now
        if (diff >= 0 && diff <= THIRTY_DAYS_MS) { soon.push({ ...r, urgent: 'soon' }); continue }
        if (diff < 0) { expired.push({ ...r, urgent: 'expired' }); continue }
      }
    }
    rest.push(r)
  }
  soon.sort((a, b) => new Date(a.valid_until!).getTime() - new Date(b.valid_until!).getTime())
  expired.sort((a, b) => new Date(b.valid_until!).getTime() - new Date(a.valid_until!).getTime())
  rest.sort((a, b) => new Date(b.obtained_at).getTime() - new Date(a.obtained_at).getTime())
  return [...soon, ...expired, ...rest]
}

export default function RewardsWalletScreen({ onBack }: { onBack: () => void }) {
  const token = getUserToken() || undefined
  const { data, error, isLoading, mutate } = useSWR(
    token ? ['profile-rewards'] : null,
    () => withUserAuth((t) => rewardsApi.list(t)),
  )
  const rewards = data?.rewards ?? null
  const sorted = rewards ? sortRewards(rewards) : []
  const [detailId, setDetailId] = useState<string | null>(null)
  const detail = sorted.find((r) => r.id === detailId) ?? null

  // 標記已使用：樂觀更新 SWR 快取（不重打列表 API），失敗回滾。
  // 用 functional mutate（以「當下最新快取」為基礎、只 patch 這一筆），避免以呼叫當下的舊快照重建整個
  // 清單——否則快速標記兩筆不同獎勵時，後解析的請求會用舊快照把另一筆的更新覆蓋掉、退回「未使用」。
  async function markUsed(id: string) {
    if (!rewards) return
    const optimisticAt = new Date().toISOString()
    const patchOne = (used: boolean, usedAt: string | null) =>
      (cur: { rewards: typeof rewards } | undefined) => ({
        rewards: (cur?.rewards ?? rewards).map((r) => (r.id === id ? { ...r, used, used_at: usedAt ?? r.used_at } : r)),
      })
    mutate(patchOne(true, optimisticAt), { revalidate: false })
    try {
      const res = await withUserAuth((t) => rewardsApi.markUsed(t, id))
      mutate(patchOne(res.used, res.used_at ?? null), { revalidate: false })
    } catch {
      // 回滾：只把這一筆退回未使用（markUsed 只對未使用的獎勵開放，故原狀即 used=false/used_at=null）
      mutate(patchOne(false, null), { revalidate: false })
    }
  }

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', background: 'var(--bg)' }}>
      <header style={{ padding: 'var(--app-top) 22px 0', minHeight: 'calc(var(--app-top) + 34px)', boxSizing: 'border-box', flexShrink: 0, display: 'flex', alignItems: 'center', gap: 12 }}>
        <button onClick={onBack} style={backBtn}>← 返回</button>
        <span style={{ fontSize: 15, fontWeight: 800, color: 'var(--tx)' }}>🎁 活動獎勵</span>
      </header>

      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', padding: '10px 18px 28px' }}>
        <p style={{ fontSize: 12.5, color: 'var(--tx-dim)', margin: '0 2px 14px', lineHeight: 1.7 }}>
          完成挑戰機率獲得的序號好禮都放在這裡，點卡片看兌換方式。
        </p>

        {isLoading ? (
          <div style={{ color: 'var(--tx-faint)', fontSize: 13, padding: '20px 2px' }}>載入中…</div>
        ) : error ? (
          <div style={{ color: 'var(--hunt)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>載入失敗，請稍後再試</div>
        ) : sorted.length === 0 ? (
          <div style={{ color: 'var(--tx-dim)', fontSize: 13.5, textAlign: 'center', padding: '24px 2px' }}>尚未獲得任何活動獎勵，完成挑戰試試手氣吧！</div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {sorted.map((r) => <RewardCard key={r.id} reward={r} onDetail={() => setDetailId(r.id)} />)}
          </div>
        )}
      </div>

      {detail && (
        <RewardDetailModal reward={detail} onClose={() => setDetailId(null)} onMarkUsed={() => markUsed(detail.id)} />
      )}
    </div>
  )
}

function RewardCard({ reward, onDetail }: { reward: SortedReward; onDetail: () => void }) {
  const urgent = reward.urgent
  return (
    <div
      onClick={onDetail}
      style={{
        display: 'flex', gap: 12, alignItems: 'center', background: 'var(--bg-1)',
        border: urgent ? '2px solid var(--gold)' : '1px solid var(--line)',
        borderRadius: 'var(--radius-lg, 14px)', padding: '12px 14px', cursor: 'pointer',
        boxShadow: urgent ? '0 4px 18px rgba(197,139,29,.22)' : 'var(--card-shadow, none)',
      }}
    >
      <div style={iconWrap}>
        {reward.icon_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={reward.icon_url} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
        ) : <span style={{ fontSize: 24 }}>{reward.kind === 'coupon' ? '🎟️' : '🎁'}</span>}
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
          <span style={{ fontSize: 14.5, fontWeight: 800, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {reward.item_label || (reward.kind === 'coupon' ? '活動優惠券' : '活動獎勵')}
          </span>
          {reward.kind === 'coupon' && reward.amount_cents != null && (
            <span style={{ fontSize: 13, fontWeight: 800, color: 'var(--gold)' }}>NT$ {Math.round(reward.amount_cents / 100)}</span>
          )}
          {urgent === 'soon' && <span style={soonBadge}>即將到期</span>}
          {urgent === 'expired' && <span style={expiredBadge}>已過期</span>}
        </div>
        {reward.merchant_name && <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{reward.merchant_name}</div>}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 6, fontSize: 11, flexWrap: 'wrap' }}>
          <span style={{ color: 'var(--tx-faint)' }}>{fmtDateTime(reward.obtained_at)} 取得</span>
          {reward.kind === 'coupon' ? (
            // 活動優惠券三態文案（可使用／已使用含日期／已過期），比照 serial 類但多帶已使用日期。
            <span style={{ color: reward.used ? 'var(--tx-faint)' : 'var(--fug)', fontWeight: 700 }}>
              {reward.used ? `已使用${reward.used_at ? `（${fmtDateTime(reward.used_at)}）` : ''}` : urgent === 'expired' ? '已過期' : '可使用'}
            </span>
          ) : (
            // 序號類多一種「尚未開始」（migration 139 valid_from，區別於可使用的正向色，用中性色）。
            <span style={{ color: reward.used ? 'var(--tx-faint)' : isNotStarted(reward) ? 'var(--tx-dim)' : 'var(--fug)', fontWeight: 700 }}>
              {reward.used ? '已使用' : isNotStarted(reward) ? '尚未開始' : '未使用'}
            </span>
          )}
        </div>
      </div>
      <button onClick={(e) => { e.stopPropagation(); onDetail() }} style={detailBtn}>獎勵資訊</button>
    </div>
  )
}

function RewardDetailModal({ reward, onClose, onMarkUsed }: { reward: UserReward; onClose: () => void; onMarkUsed: () => void }) {
  const [copied, setCopied] = useState(false)
  // 掛載點：手機模擬框內→portal 進框(桌機不鋪滿視窗)；獨立路由(無手機框)→退回 document.body(視窗)
  const om = overlayMount()
  const content = (
    // data-skin="default"：本卡片背景固定深色（#0b0e13），比照 PartnerPerksScreen 的 VipLockedModal，
    // 固定用暗色變數值，避免淺色 skin 下文字對比消失。
    <div data-skin="default" onClick={onClose} style={{ ...overlay, position: om.position }}>
      <div onClick={(e) => e.stopPropagation()} style={panel}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 10 }}>
          <div style={{ fontSize: 16, fontWeight: 900, color: '#fff', wordBreak: 'break-word' }}>
            {reward.item_label || (reward.kind === 'coupon' ? '活動優惠券' : '活動獎勵')}
          </div>
          <button onClick={onClose} style={closeX} aria-label="關閉">✕</button>
        </div>

        {reward.kind === 'coupon' && reward.amount_cents != null && (
          <div style={{ textAlign: 'center', fontSize: 26, fontWeight: 900, color: 'var(--gold)', marginTop: 10 }}>
            NT$ {Math.round(reward.amount_cents / 100)}
          </div>
        )}

        {reward.icon_url && (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={reward.icon_url} alt="" style={{ width: 72, height: 72, borderRadius: 14, objectFit: 'cover', margin: '14px auto 0', display: 'block' }} />
        )}

        {reward.merchant_name && <div style={{ textAlign: 'center', fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 8 }}>{reward.merchant_name}</div>}

        {reward.description && (
          <div style={{ fontSize: 13, color: 'var(--tx)', lineHeight: 1.7, marginTop: 14, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{reward.description}</div>
        )}

        <div style={infoRow}>
          {/* 有 valid_from（migration 139，序號類專用）時改顯示區段「有效期間：M/D ~ M/D」；
              沒有時維持原本單一「使用期限」的完整日期時間顯示，行為不變。 */}
          <span style={infoLabel}>{reward.valid_from ? '有效期間' : '使用期限'}</span>
          <span style={infoVal}>
            {reward.valid_from
              ? `${fmtMD(reward.valid_from)} ~ ${reward.valid_until ? fmtMD(reward.valid_until) : '無期限'}`
              : reward.valid_until ? fmtDateTime(reward.valid_until) : '無期限'}
          </span>
        </div>

        {reward.usage_note && (
          <div style={{ marginTop: 10 }}>
            <div style={infoLabel}>使用說明</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', lineHeight: 1.7, marginTop: 4, whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{reward.usage_note}</div>
          </div>
        )}

        {reward.code && (
          <div style={{ marginTop: 14 }}>
            <div style={infoLabel}>序號</div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 6 }}>
              <div style={codeBox}>{reward.code}</div>
              <button
                onClick={() => {
                  navigator.clipboard?.writeText(reward.code).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1500) }).catch(() => {})
                }}
                style={ghostBtn}
              >{copied ? '已複製' : '複製'}</button>
            </div>
          </div>
        )}

        {reward.link && (
          <button onClick={() => window.open(reward.link, '_blank', 'noopener,noreferrer')} style={{ ...primaryBtn, marginTop: 12 }}>前往兌換 ›</button>
        )}

        <div style={infoRow}>
          <span style={infoLabel}>使用狀態</span>
          <span style={{ ...infoVal, color: reward.used ? 'var(--tx-dim)' : isNotStarted(reward) ? 'var(--tx-dim)' : 'var(--fug)' }}>
            {reward.used
              ? `已使用${reward.used_at ? `（${fmtDateTime(reward.used_at)}）` : ''}`
              : isNotStarted(reward)
                ? '尚未開始'
                : reward.kind === 'coupon' && reward.valid_until && new Date(reward.valid_until).getTime() < Date.now()
                  ? '已過期'
                  : reward.kind === 'coupon' ? '可使用' : '未使用'}
          </span>
        </div>

        {/* coupon 類不提供手動標記已使用：券由報名折抵流程自動核銷（CAS），不是玩家自行回報。
            序號類尚未開始（migration 139 valid_from）時 disable，避免點了才被後端 400 擋下；
            後端 MarkRewardUsed 同步擋（見 internal/profile/rewards.go），前端只是提早防呆。 */}
        {!reward.used && reward.kind !== 'coupon' && (
          <button
            onClick={onMarkUsed}
            disabled={isNotStarted(reward)}
            style={{ ...ghostFullBtn, marginTop: 14, opacity: isNotStarted(reward) ? 0.5 : 1, cursor: isNotStarted(reward) ? 'not-allowed' : 'pointer' }}
          >
            {isNotStarted(reward) ? '尚未開始' : '標記為已使用'}
          </button>
        )}
      </div>
    </div>
  )
  return om.node ? createPortal(content, om.node) : content
}

const backBtn: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', cursor: 'pointer', fontSize: 14, padding: 0, flexShrink: 0 }
const iconWrap: React.CSSProperties = { width: 52, height: 52, borderRadius: 12, background: 'var(--bg-2)', flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', overflow: 'hidden' }
const detailBtn: React.CSSProperties = { flexShrink: 0, background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '7px 11px', fontSize: 12, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const soonBadge: React.CSSProperties = { fontSize: 10.5, fontWeight: 800, color: '#fff', background: 'var(--gold)', borderRadius: 999, padding: '2px 8px' }
const expiredBadge: React.CSSProperties = { fontSize: 10.5, fontWeight: 800, color: '#fff', background: 'var(--hunt)', borderRadius: 999, padding: '2px 8px' }
// z-index 比可拖曳資訊面板（500）高很多，比照 PartnerPerksScreen 的 VipLockedModal（見 frontend-draggable-sheet 慣例）
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, zIndex: 3300, background: 'rgba(4,8,6,.82)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 18 }
const panel: React.CSSProperties = { width: '100%', maxWidth: 400, maxHeight: '86dvh', overflowY: 'auto', overscrollBehavior: 'contain', WebkitOverflowScrolling: 'touch', background: '#0b0e13', border: '1px solid var(--line-2)', borderRadius: 16, padding: '18px 18px 20px', boxShadow: '0 16px 50px rgba(0,0,0,.7)' }
const closeX: React.CSSProperties = { background: 'none', border: 'none', color: 'var(--tx-dim)', fontSize: 16, cursor: 'pointer', padding: 4, lineHeight: 1, flexShrink: 0 }
const infoRow: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 12, fontSize: 12.5, gap: 10 }
const infoLabel: React.CSSProperties = { color: 'var(--tx-faint)', flexShrink: 0 }
const infoVal: React.CSSProperties = { color: 'var(--tx)', fontWeight: 700, textAlign: 'right' }
const codeBox: React.CSSProperties = { flex: 1, minWidth: 0, background: 'var(--bg-2)', border: '1px dashed var(--line-2)', borderRadius: 8, padding: '9px 12px', fontSize: 14, fontWeight: 800, color: 'var(--gold)', fontFamily: 'monospace', letterSpacing: '.03em', overflowWrap: 'break-word', wordBreak: 'break-all' }
const ghostBtn: React.CSSProperties = { flexShrink: 0, background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '9px 14px', fontSize: 12.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
const primaryBtn: React.CSSProperties = { width: '100%', background: 'var(--fug)', color: 'var(--fug-ink)', border: 'none', borderRadius: 10, padding: '11px 0', fontSize: 13.5, fontWeight: 800, cursor: 'pointer', fontFamily: 'inherit' }
const ghostFullBtn: React.CSSProperties = { width: '100%', background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 10, padding: '11px 0', fontSize: 13.5, fontWeight: 700, cursor: 'pointer', fontFamily: 'inherit' }
